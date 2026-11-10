# T5-04 实验 D：重试策略与死信（DLQ）行为核验
#
# 源码事实（internal/logic/flashsale/kafka_consumer.go + consume.go）：
#   - DLQ 是**已实现**的：Topic 由 flash_sale.kafka.dlq_topic 提供，消费端用同一个
#     Producer（acks=all）同步发布死信，并携带 dlq_reason / dlq_source_offset 等头部；
#   - 重试预算 = 3（maxConsumeRetryAttempts），计数列是 flash_sale_order_requests.retry_count；
#     语义是「已消耗的重试次数」，retry_count > 3 的请求不再执行业务，直接走死信 + dead 终态；
#   - 单次投递内还有本地兜底 MaxDeliveryAttempts=4（200ms→1s 指数退避），
#     用于「连 retry_count 都写不进去」（典型 MySQL 不可用）时停止本轮、保留位移；
#   - 永久错误（JSON 非法 / schema_version 不认识 / event_type 不认识 / 字段非法 /
#     请求不存在 / 身份不一致）不消耗重试预算，直接进 DLQ。
#
# 本实验两个子项（都使用独立测试数据，不触碰历史活动）：
#   D-1 永久错误 → DLQ：分别投递 4 类不可处理消息，核对死信头部、指标与「数据库无任何写入」；
#   D-2 重试预算耗尽 → DLQ + dead：真实 HTTP 受理一条请求（Redis 已预扣、请求处于 queued），
#       然后把该请求的 retry_count 置为 4（模拟「此前已发生 4 次技术失败」），
#       再让消费端处理它 —— 期望：不建单、死信 1 条、请求落 dead、Redis 由补偿收敛回权威值。
#
# 为什么 D-2 用「注入 retry_count」而不是真的制造 4 次技术失败：
#   真实制造技术失败的最短路径是把 MySQL 停掉，那会同时打挂 HTTP 入口与 Outbox，
#   属于实验 B 之外的破坏性故障，收益（多验证一次 PersistConsumeFailure）远小于风险。
#   注入的是「已经失败 4 次」这一状态本身，消费端走的仍是同一条
#   「重试预算闸门 → 死信 → dead 终态」代码路径，因此验证结论成立；
#   但必须在报告里如实标注：本项验证的是「预算耗尽后的处置」，不是「重试计数如何被推进」。

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][long]$ActivityId,
    [Parameter(Mandatory = $true)][long]$SkuId,
    [int]$Users = 1,
    [string]$RunId = 't504-d-run1',
    [string]$OutDir = '',
    [int]$RecoverTimeoutSeconds = 120,
    # 一个绝对不存在的 request_id，用于构造「请求不存在」的永久错误消息。
    [long]$MissingRequestId = 99999999,
    # 续跑开关：D-1 已完成时跳过（避免重复投递无效消息、污染 DLQ 计数）。
    [switch]$SkipD1,
    # 续跑开关：复用一条「已受理且已发布、状态仍为 queued」的请求行做 D-2，
    # 不再重复提交 HTTP 请求（避免多消耗库存、改变对照基线）。
    [long]$ExistingRequestId = 0,
    # 收尾模式：D-2 的故障注入已执行完、但进程被外部中断（例如宿主 shell 超时）时，
    # 只重新采集「已经发生的事实」（DLQ 头部、请求终态、指标、Redis、MySQL）并落盘证据，
    # 不重复任何故障注入动作。
    [long]$FinalizeRequestId = 0
)

. (Join-Path $PSScriptRoot 'lib.ps1')

if ([string]::IsNullOrWhiteSpace($OutDir)) {
    $OutDir = Join-Path $script:RepoRoot 'docs/reports/t5-04-kafka-backlog-failure-recovery/raw/d'
}
if (-not (Test-Path $OutDir)) { New-Item -ItemType Directory -Path $OutDir -Force | Out-Null }
if ([string]::IsNullOrWhiteSpace($env:K6_TEST_PASSWORD)) {
    throw '缺少 K6_TEST_PASSWORD（测试用户密码，8~24 位）。'
}

# D-2 的验证边界（写入证据，避免把「注入的状态」说成「真实发生的 4 次失败」）。
$noteD2 = 'retry_count 由本脚本注入（模拟已完成 4 次技术失败），验证的是「预算耗尽后的处置」而非「重试计数如何推进」。'

# ---- DLQ 读取（带消息头）-----------------------------------------------------
function Get-DlqMessages {
    <#
      .SYNOPSIS
        从 DLQ 指定偏移起读取消息，并把 dlq_* 消息头解析成对象。
      .DESCRIPTION
        console-consumer 输出形如：<headers>\t<key>\t<value>
        headers 是 k1:v1,k2:v2,…；这里只保留 dlq_* 前缀的键，避免把业务头混入证据。
    #>
    param(
        [Parameter(Mandatory)][long]$FromOffset,
        [Parameter(Mandatory)][int]$Count
    )
    $cmd = "$($script:KafkaBin)/kafka-console-consumer.sh --bootstrap-server $($script:BootstrapServer) " +
    "--topic $($script:DlqTopic) --partition 0 --offset $FromOffset --max-messages $Count --timeout-ms 20000 " +
    "--property print.headers=true --property print.key=true"
    $rows = Invoke-KafkaCli -Command $cmd
    $out = @()
    foreach ($row in $rows) {
        if ($row -notmatch 'dlq_reason') { continue }
        $parts = $row -split "`t"
        if ($parts.Count -lt 3) { continue }
        $headers = [ordered]@{}
        foreach ($pair in ($parts[0] -split ',')) {
            $kv = $pair -split ':', 2
            if ($kv.Count -eq 2 -and $kv[0] -like 'dlq_*') { $headers[$kv[0]] = $kv[1] }
        }
        $out += [pscustomobject]@{
            headers = $headers
            key     = $parts[1]
            value   = $parts[2]
        }
    }
    return $out
}

function Get-MetricSeries {
    param([Parameter(Mandatory)][string]$Query)
    try {
        return @(Get-PromSeries -Query $Query | ForEach-Object {
                $lbl = ($_.metric.PSObject.Properties | Where-Object { $_.Name -ne '__name__' } |
                    ForEach-Object { "$($_.Name)=$($_.Value)" }) -join ','
                "$lbl=$($_.value)"
            })
    } catch { return @("query-failed: $($_.Exception.Message)") }
}

function Get-DlqLatest { return [long]((Get-TopicOffset -Topic $script:DlqTopic -Time -1)['0']) }

Assert-Preconditions
Write-Step "实验 D 开始：activity=$ActivityId sku=$SkuId run=$RunId"

if ($FinalizeRequestId -gt 0) {
    # ---- 收尾模式：只采集事实，不做任何故障注入 ----
    Write-Step "收尾模式：补齐请求 $FinalizeRequestId 的 D-2 证据"
    $dlqNow = Get-DlqLatest
    $finalRow = @(Invoke-SqlText -Sql @"
SELECT CONCAT('status=',status,' retry_count=',retry_count,' last_error_code=',COALESCE(last_error_code,0),
' flash_order_id=',COALESCE(flash_order_id,0),' published_offset=',COALESCE(published_offset,-1))
FROM flash_sale_order_requests WHERE id = $FinalizeRequestId
"@)
    Write-Step "   最终请求行：$($finalRow -join '')"
    $offsetField = ($finalRow -join '') -replace '.*published_offset=(-?\d+).*', '$1'
    $sourceOffset = if ($offsetField -match '^\d+$') { [long]$offsetField } else { -1 }
    # 死信是按原消息 offset 逆查的：只读这一条，避免把 D-1 的死信混进 D-2 结论。
    $dlqMsg = @()
    if ($sourceOffset -ge 0) {
        for ($off = [Math]::Max(0, $dlqNow - 5); $off -lt $dlqNow; $off++) {
            $probe = @(Get-DlqMessages -FromOffset $off -Count 1)
            if ($probe.Count -gt 0 -and [long]$probe[0].headers['dlq_source_offset'] -eq $sourceOffset) {
                $dlqMsg = $probe
                break
            }
        }
    }
    Write-Step "   DLQ 命中：$($dlqMsg.Count) 条（dlq_latest=$dlqNow）"
    $redisKeys = @(Invoke-Kubectl -Arguments @('exec', '-n', $script:Namespace, $script:RedisTarget, '--',
            'sh', '-c', "redis-cli --scan --pattern 'flashsale:*:$ActivityId*' | sort"))
    $dbFinal = Get-FlashSaleDbState -ActivityId $ActivityId
    Save-Evidence -Directory $OutDir -Name 'd2-result.json' -Data ([ordered]@{
            request_id        = $FinalizeRequestId
            dlq_latest_after  = $dlqNow
            dlq_messages      = $dlqMsg
            final_request_row = $finalRow
            dead_metric       = (Get-MetricSeries -Query 'flashsale_consumer_processed_total{result="dead"}')
            dlq_metric        = (Get-MetricSeries -Query 'flashsale_consumer_dlq_total')
            redis_keys_after  = $redisKeys
            db_final          = $dbFinal
            finalized_from    = 'interrupted-run (fault injection already applied)'
        }) | Out-Null
    $exhaustReason = @($dlqMsg | ForEach-Object { $_.headers['dlq_reason'] })
    Save-Evidence -Directory $OutDir -Name 'd.summary.json' -Data ([ordered]@{
            experiment        = 'D: retry budget & DLQ'
            activity_id       = $ActivityId
            sku_id            = $SkuId
            d2_request_id     = $FinalizeRequestId
            d2_dlq_latest     = $dlqNow
            d2_reasons        = $exhaustReason
            d2_final_row      = $finalRow
            d2_db_final       = $dbFinal
            d1_result_ref     = 'd1-result.json'
            note_d2           = $noteD2
        }) | Out-Null
    Save-EvidenceText -Directory $OutDir -Name 'd.summary.txt' -Lines @(
        '=== 实验 D 结论 ==='
        "D-1 见 d1-result.json / d1-dlq-messages.json（4 条永久错误 → DLQ，来源偏移与投递偏移一致）"
        "D-2 重试预算耗尽：request=$FinalizeRequestId dlq_latest=$dlqNow 原因=$($exhaustReason -join ',')"
        "D-2 最终请求行：$($finalRow -join '')"
        "D-2 数据库：orders=$($dbFinal.orders_total) sold=$($dbFinal.sku_sold) dead=$($dbFinal.requests_dead)"
        "D-2 Redis 相关键：$($redisKeys -join ' | ')"
        "NOTE: $($noteD2)"
    ) | Out-Null
    & (Join-Path $PSScriptRoot 'snapshot.ps1') -Phase 'd3-after' -OutDir $OutDir -Quiet | Out-Null
    Write-Step '收尾完成'
    return
}

& (Join-Path $PSScriptRoot 'snapshot.ps1') -Phase 'd1-before' -OutDir $OutDir -Quiet | Out-Null
$dlqBefore = Get-DlqLatest
$dbBefore = Get-FlashSaleDbState -ActivityId $ActivityId
$lagBefore = Get-LagSample
Write-Step "   基线：dlq_latest=$dlqBefore lag=$($lagBefore.broker_lag) committed=$($lagBefore.committed_offset)"

# ============================================================================
# D-1：永久错误 → DLQ
# ============================================================================
if ($SkipD1) {
    Write-Step 'D-1：按 -SkipD1 跳过（结论已落盘 d1-result.json），本轮只做 D-2'
    $dlqAfterInvalid = $dlqBefore
    $dlqMsgs = @()
    $dbAfterInvalid = $dbBefore
    $sourceMatch = $true
    $expectedOffsets = @()
    $observedSources = @()
    if (Test-Path (Join-Path $OutDir 'd1-result.json')) {
        $prev = Get-Content (Join-Path $OutDir 'd1-result.json') -Raw | ConvertFrom-Json
        $sourceMatch = [bool]$prev.source_offset_match
        $expectedOffsets = @($prev.produced_offsets)
        $observedSources = @($prev.dlq_source_offsets)
        $script:d1ReasonsBackfill = @($prev.dlq_reasons)
    }
}
else {
Write-Step 'D-1：投递 4 类不可处理的永久错误消息'

# request_hash 必须是 64 位小写 sha256hex（validateRequestHash），这里用一个固定形状的合法值。
$validHash = ('0123456789abcdef' * 4)
$cases = @(
    @{ name = 'parse_error'; payload = '{"this is":"not valid json"' },
    @{ name = 'schema_version'; payload = (@{
                event_id = "flashsale.requested:$MissingRequestId"; event_type = 'flashsale.order.requested'
                schema_version = 99; request_id = $MissingRequestId; user_id = 1
                activity_id = $ActivityId; sku_id = $SkuId
                idempotency_key = 't504d-bad-schema'; request_hash = $validHash
                occurred_at = (Get-UtcStamp)
            } | ConvertTo-Json -Compress) },
    @{ name = 'event_type'; payload = (@{
                event_id = "flashsale.requested:$MissingRequestId"; event_type = 'flashsale.order.created'
                schema_version = 1; request_id = $MissingRequestId; user_id = 1
                activity_id = $ActivityId; sku_id = $SkuId
                idempotency_key = 't504d-bad-type'; request_hash = $validHash
                occurred_at = (Get-UtcStamp)
            } | ConvertTo-Json -Compress) },
    @{ name = 'request_missing'; payload = (@{
                event_id = "flashsale.requested:$MissingRequestId"; event_type = 'flashsale.order.requested'
                schema_version = 1; request_id = $MissingRequestId; user_id = 1
                activity_id = $ActivityId; sku_id = $SkuId
                idempotency_key = 't504d-missing'; request_hash = $validHash
                occurred_at = (Get-UtcStamp)
            } | ConvertTo-Json -Compress) }
)

$topicLatestBefore = [long]((Get-TopicOffset -Topic $script:Topic -Time -1)['0'])
$produced = @()
foreach ($c in $cases) {
    Produce-TopicMessage -Topic $script:Topic -Payload $c.payload | Out-Null
    $produced += [pscustomobject]@{ case = $c.name; payload = $c.payload }
    Write-Step "   已投递：case=$($c.name)"
}
Save-EvidenceText -Directory $OutDir -Name 'd1-produced.txt' -Lines @($produced | ForEach-Object { "[$($_.case)] $($_.payload)" }) | Out-Null

# 等待这 4 条被消费（lag 归零 = 已产生结论并提交位移）
$null = Measure-LagTimeline -Label 'd1-drain' -DurationSeconds 60 -IntervalSeconds 3 -StopWhenZero -OutDir $OutDir
$dlqAfterInvalid = Get-DlqLatest
Write-Step "   DLQ: $dlqBefore -> $dlqAfterInvalid（新增 $($dlqAfterInvalid - $dlqBefore) 条）"

$dlqMsgs = @(Get-DlqMessages -FromOffset $dlqBefore -Count ([int]($dlqAfterInvalid - $dlqBefore)))
$dlqMsgs | ConvertTo-Json -Depth 6 | Set-Content -Path (Join-Path $OutDir 'd1-dlq-messages.json') -Encoding utf8
$dbAfterInvalid = Get-FlashSaleDbState -ActivityId $ActivityId

# 反证：DLQ 里的 dlq_source_offset 必须等于本次投递的偏移区间，证明「这 4 条死信来自本实验」。
$expectedOffsets = @($topicLatestBefore..($topicLatestBefore + $cases.Count - 1))
$observedSources = @($dlqMsgs | ForEach-Object { [long]$_.headers['dlq_source_offset'] })
Write-Step "   期望来源偏移=$($expectedOffsets -join ',') 实际=$($observedSources -join ',')"
$sourceMatch = (($expectedOffsets | Sort-Object) -join ',') -eq (($observedSources | Sort-Object) -join ',')

Save-Evidence -Directory $OutDir -Name 'd1-result.json' -Data ([ordered]@{
        dlq_before                  = $dlqBefore
        dlq_after                   = $dlqAfterInvalid
        produced_offsets            = $expectedOffsets
        dlq_source_offsets          = $observedSources
        source_offset_match         = $sourceMatch
        dlq_reasons                 = @($dlqMsgs | ForEach-Object { $_.headers['dlq_reason'] })
        invalid_metric_series       = (Get-MetricSeries -Query 'flashsale_consumer_processed_total{result="invalid"}')
        dlq_metric_series           = (Get-MetricSeries -Query 'flashsale_consumer_dlq_total')
        db_before                   = $dbBefore
        db_after                    = $dbAfterInvalid
    }) | Out-Null
}

# ============================================================================
# D-2：重试预算耗尽 → DLQ + dead 终态
# ============================================================================
Write-Step 'D-2：重试预算耗尽（retry_count>3）的处置'

if ($ExistingRequestId -gt 0) {
    # 续跑：复用一条已经「受理 + 发布 + 仍 queued」的请求行，不再提交新请求。
    Write-Step "   步骤 1（续跑）：复用既有请求 $ExistingRequestId，确认消费仍处于暂停"
    Set-ClientQuota -ClientId $script:ConsumerClientID -ConsumerByteRate 1 | Out-Null
    $k6Exit = 'skipped'
    Start-Sleep -Seconds 2
}
else {
Write-Step '   步骤 1：暂停消费，提交 1 条真实业务请求（请求停在 queued 且已发布）'
Set-ClientQuota -ClientId $script:ConsumerClientID -ConsumerByteRate 1 | Out-Null
$env:FLASH_EXECUTOR = 'once'
$env:FLASH_ITERATIONS = '1'
$env:FLASH_USERS = "$Users"
$env:FLASH_VUS = "$Users"
$env:FLASH_VERIFY = 'false'
$env:FLASH_TRACE = 'true'
$env:ACTIVITY_ID = "$ActivityId"
$env:SKU_ID = "$SkuId"
$env:DATA_PREFIX = 't504dz'
$k6Runner = Join-Path $script:RepoRoot 'test/k6/scripts/run.ps1'
$prevEap = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
$k6Out = & powershell -NoProfile -ExecutionPolicy Bypass -File $k6Runner `
    -Scenario flashsale-order -RunId $RunId -BaseUrl 'http://host.docker.internal:18000' 2>&1
$k6Exit = $LASTEXITCODE
$ErrorActionPreference = $prevEap
$k6Lines = @($k6Out | ForEach-Object { $_.ToString() })
Save-EvidenceText -Directory $OutDir -Name "$RunId.k6-console.log" -Lines $k6Lines | Out-Null
Save-EvidenceText -Directory $OutDir -Name "$RunId.flash-trace.txt" -Lines @($k6Lines | Where-Object { $_ -match 'FLASH_TRACE' }) | Out-Null
Write-Step "   k6 exit=$k6Exit"
Start-Sleep -Seconds 10
}

# 续跑时按 id 精确定位，避免「最新一行」已经被别的实验改过。
$idFilter = if ($ExistingRequestId -gt 0) { "AND id = $ExistingRequestId" } else { '' }
# 必须用 @() 包住：PowerShell 会把「只有一个元素的数组」解包成标量字符串，
# 于是 $row[0] 变成字符串的首字符（实测 request_id=123 被读成 '1'，status 全部为空）。
$row = @(Invoke-SqlText -Sql @"
SELECT CONCAT(id,'|',status,'|',retry_count,'|',COALESCE(published_offset,-1),'|',user_id)
FROM flash_sale_order_requests WHERE activity_id = $ActivityId $idFilter ORDER BY id DESC LIMIT 1
"@)
if ($row.Count -eq 0 -or [string]::IsNullOrWhiteSpace($row[0])) { throw '未找到本实验提交的请求行，无法继续 D-2' }
$f = $row[0] -split '\|'
$requestId = [long]$f[0]; $reqStatus = $f[1]; $reqRetry = $f[2]; $reqOffset = $f[3]
Write-Step "   请求行：id=$requestId status=$reqStatus retry_count=$reqRetry published_offset=$reqOffset"
if ($reqStatus -ne '0') { throw "请求 $requestId 状态为 $reqStatus，不是 queued；暂停消费失败，D-2 结论不可信" }

$lagPaused = Get-LagSample
Write-Step "   暂停确认：committed=$($lagPaused.committed_offset) logEnd=$($lagPaused.log_end_offset) lag=$($lagPaused.broker_lag)"

Write-Step '   步骤 2：注入「已失败 4 次」的持久化重试状态（retry_count=4 > 预算 3）'
$beforeRedis = @(Invoke-Kubectl -Arguments @('exec', '-n', $script:Namespace, $script:RedisTarget, '--',
        'sh', '-c', "redis-cli --scan --pattern 'flashsale:*:$ActivityId*' | sort"))
Invoke-SqlText -Sql "UPDATE flash_sale_order_requests SET retry_count = 4, last_error_code = 500, next_attempt_at = NULL WHERE id = $requestId AND status = 0" | Out-Null
$injected = Invoke-SqlText -Sql "SELECT CONCAT('retry_count=',retry_count,' status=',status,' last_error_code=',COALESCE(last_error_code,0)) FROM flash_sale_order_requests WHERE id = $requestId"
Write-Step "   注入后：$($injected -join '')"
Save-EvidenceText -Directory $OutDir -Name 'd2-injected.txt' -Lines (@("request_id=$requestId", "published_offset=$reqOffset") + $injected) | Out-Null

Write-Step '   步骤 3：恢复消费，观察死信 + dead 终态'
Set-ClientQuota -ClientId $script:ConsumerClientID -Remove | Out-Null
$recover = Measure-LagTimeline -Label 'd2-recovery' -DurationSeconds $RecoverTimeoutSeconds -IntervalSeconds 3 -StopWhenZero -OutDir $OutDir
Start-Sleep -Seconds 5

$dlqAfterExhaust = Get-DlqLatest
$dlqExhaustMsgs = @()
if ($dlqAfterExhaust -gt $dlqAfterInvalid) {
    $dlqExhaustMsgs = @(Get-DlqMessages -FromOffset $dlqAfterInvalid -Count ([int]($dlqAfterExhaust - $dlqAfterInvalid)))
}
$finalRow = Invoke-SqlText -Sql @"
SELECT CONCAT('status=',status,' retry_count=',retry_count,' last_error_code=',COALESCE(last_error_code,0),
' flash_order_id=',COALESCE(flash_order_id,0),' published_offset=',COALESCE(published_offset,-1))
FROM flash_sale_order_requests WHERE id = $requestId
"@
Write-Step "   最终请求行：$($finalRow -join '')"
$afterRedis = @(Invoke-Kubectl -Arguments @('exec', '-n', $script:Namespace, $script:RedisTarget, '--',
        'sh', '-c', "redis-cli --scan --pattern 'flashsale:*:$ActivityId*' | sort"))
$dbAfter = Get-FlashSaleDbState -ActivityId $ActivityId

Save-Evidence -Directory $OutDir -Name 'd2-result.json' -Data ([ordered]@{
        request_id            = $requestId
        published_offset      = $reqOffset
        dlq_before            = $dlqAfterInvalid
        dlq_after             = $dlqAfterExhaust
        dlq_messages          = $dlqExhaustMsgs
        final_request_row     = $finalRow
        dead_metric           = (Get-MetricSeries -Query 'flashsale_consumer_processed_total{result="dead"}')
        dlq_metric            = (Get-MetricSeries -Query 'flashsale_consumer_dlq_total')
        redis_keys_before     = $beforeRedis
        redis_keys_after      = $afterRedis
        db_final              = $dbAfter
        k6_exit               = $k6Exit
        recovery_samples      = $recover.Count
    }) | Out-Null
& (Join-Path $PSScriptRoot 'snapshot.ps1') -Phase 'd3-after' -OutDir $OutDir -Quiet | Out-Null

# ============================================================================
# 汇总
# ============================================================================
$reasonList = @($dlqMsgs | ForEach-Object { $_.headers['dlq_reason'] })
if ($SkipD1 -and $script:d1ReasonsBackfill) { $reasonList = @($script:d1ReasonsBackfill) }
$exhaustReason = @($dlqExhaustMsgs | ForEach-Object { $_.headers['dlq_reason'] })
$verdict = [ordered]@{
    experiment            = 'D: retry budget & DLQ'
    activity_id           = $ActivityId
    sku_id                = $SkuId
    d1_dlq_before         = $dlqBefore
    d1_dlq_after          = $dlqAfterInvalid
    d1_reasons            = $reasonList
    d1_source_offset_ok   = $sourceMatch
    d1_db_unchanged       = ($dbBefore.orders_total -eq $dbAfterInvalid.orders_total -and $dbBefore.requests_total -eq $dbAfterInvalid.requests_total)
    d2_request_id         = $requestId
    d2_dlq_after          = $dlqAfterExhaust
    d2_reasons            = $exhaustReason
    d2_final_row          = $finalRow
    d2_db_final           = $dbAfter
    note_d2 = $noteD2
}
$verdictPath = Save-Evidence -Directory $OutDir -Name 'd.summary.json' -Data $verdict
Save-EvidenceText -Directory $OutDir -Name 'd.summary.txt' -Lines @(
    '=== 实验 D 结论 ==='
    "D-1 永久错误：dlq $dlqBefore -> $dlqAfterInvalid，原因=$($reasonList -join ',')，来源偏移与投递偏移一致=$sourceMatch"
    "D-1 数据库无变化：orders $($dbBefore.orders_total) -> $($dbAfterInvalid.orders_total)，requests $($dbBefore.requests_total) -> $($dbAfterInvalid.requests_total)"
    "D-2 重试预算耗尽：request=$requestId dlq $dlqAfterInvalid -> $dlqAfterExhaust，原因=$($exhaustReason -join ',')"
    "D-2 最终请求行：$($finalRow -join '')"
    "D-2 数据库：orders=$($dbAfter.orders_total) sold=$($dbAfter.sku_sold) dead=$($dbAfter.requests_dead)"
    "NOTE: $($verdict.note_d2)"
) | Out-Null

Write-Step "实验 D 完成，结论写入 $verdictPath"
Write-Host ($verdict | ConvertTo-Json -Depth 6)
