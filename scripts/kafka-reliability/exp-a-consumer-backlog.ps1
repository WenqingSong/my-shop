# T5-04 实验 A：Consumer 积压与恢复（Broker 全程存活）
#
# 为什么不是「把 Consumer 停掉」：
#   源码调查结论（internal/cmd/cmd.go + internal/worker/flashsale_runtime.go）：
#   HTTP 服务、Outbox Publisher、Kafka Consumer 在**同一个进程、同一个 Pod** 内启动，
#   没有独立的 consumer Deployment，也没有「只关消费者」的开关。
#   因此下列手段全部不可用（都会连带破坏 HTTP 入口或发布链路）：
#     - kubectl scale deployment/surgecart --replicas=0  → HTTP 也没了；
#     - 网络策略/iptables 阻断到 Kafka 的连接             → 生产者同样被阻断；
#     - 停 Broker                                        → 那是实验 B，不是 A。
#   本项目生产者与消费者是两个独立的 franz-go 客户端，client.id 不同
#   （surgecart-flashsale-producer / surgecart-flashsale-consumer），
#   而 Kafka 的客户端配额按 client.id 生效 —— 这是唯一能「只把消费端拉取能力压到 0、
#   同时让生产者继续发布」的杠杆。它是 Broker 侧动态配置，delete-config 即可完全回滚。
#
# 实验验证的边界（写入报告，不夸大）：
#   它验证的是「消费能力被压到接近 0 时，Kafka 会保留消息、Lag 可观测、恢复后能继续消费」，
#   等价于消费者暂停；它不是「Consumer 进程被 kill」—— 那是实验 C。

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][long]$ActivityId,
    [Parameter(Mandatory = $true)][long]$SkuId,
    [int]$Users = 20,
    [int]$VUs = 20,
    [string]$RunId = 't504-a-run1',
    [string]$OutDir = '',
    [int]$PauseSeconds = 25,
    [int]$RecoverTimeoutSeconds = 180
)

. (Join-Path $PSScriptRoot 'lib.ps1')

if ([string]::IsNullOrWhiteSpace($OutDir)) {
    $OutDir = Join-Path $script:RepoRoot 'docs/reports/t5-04-kafka-backlog-failure-recovery/raw/a'
}
if (-not (Test-Path $OutDir)) { New-Item -ItemType Directory -Path $OutDir -Force | Out-Null }

# k6 需要测试用户密码；本脚本只读取、不打印、不落盘。
if ([string]::IsNullOrWhiteSpace($env:K6_TEST_PASSWORD)) {
    throw '缺少 K6_TEST_PASSWORD（测试用户密码，8~24 位）。请先在当前会话设置后再执行。'
}

Assert-Preconditions
Write-Step "实验 A 开始：activity=$ActivityId sku=$SkuId users=$Users vus=$VUs run=$RunId"

# ---- 1) 故障前基线 ------------------------------------------------------------
Write-Step '步骤 1：采集故障前基线'
& (Join-Path $PSScriptRoot 'snapshot.ps1') -Phase 'a1-before-pause' -OutDir $OutDir -Quiet | Out-Null
$before = Get-LagSample

# ---- 2) 注入：把消费端客户端配额压到 1 B/s（等价于暂停消费）------------------
Write-Step "步骤 2：注入消费暂停（client.id=$($script:ConsumerClientID) consumer_byte_rate=1）"
$quotaApply = Set-ClientQuota -ClientId $script:ConsumerClientID -ConsumerByteRate 1
$quotaDesc = Get-ClientQuota -ClientId $script:ConsumerClientID
Save-EvidenceText -Directory $OutDir -Name 'a2-quota.txt' -Lines (@($quotaApply) + @($quotaDesc)) | Out-Null
& (Join-Path $PSScriptRoot 'snapshot.ps1') -Phase 'a2-consumer-paused' -OutDir $OutDir -Quiet | Out-Null
$paused = Get-LagSample

# ---- 3) 通过真实业务入口产生一批独立测试消息 ---------------------------------
Write-Step "步骤 3：通过 HTTP 入口提交 $VUs 个真实秒杀请求（每用户 1 次）"
$env:FLASH_EXECUTOR = 'once'
$env:FLASH_ITERATIONS = '1'
$env:FLASH_USERS = "$Users"
$env:FLASH_VUS = "$VUs"
$env:FLASH_VERIFY = 'false'
$env:FLASH_TRACE = 'true'
$env:ACTIVITY_ID = "$ActivityId"
$env:SKU_ID = "$SkuId"
$env:DATA_PREFIX = 't504ae'

$k6Runner = Join-Path $script:RepoRoot 'test/k6/scripts/run.ps1'
$prevEap = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
$k6Output = & powershell -NoProfile -ExecutionPolicy Bypass -File $k6Runner `
    -Scenario flashsale-order -RunId $RunId -BaseUrl 'http://host.docker.internal:18000' 2>&1
$k6Exit = $LASTEXITCODE
$ErrorActionPreference = $prevEap
$k6Lines = @($k6Output | ForEach-Object { $_.ToString() })
Save-EvidenceText -Directory $OutDir -Name "$RunId.k6-console.log" -Lines $k6Lines | Out-Null
Write-Step "   k6 exit=$k6Exit"
# 逐条请求证据：只关心 FLASH_TRACE 行，报告里按 outcome 归类。
$traceLines = @($k6Lines | Where-Object { $_ -match 'FLASH_TRACE' })
Save-EvidenceText -Directory $OutDir -Name "$RunId.flash-trace.txt" -Lines $traceLines | Out-Null

# ---- 4) 故障期间：Lag 必须在「消费者没在拉」的状态下持续存在 -------------------
Write-Step "步骤 4：消费暂停期间观察 $PauseSeconds 秒的 Lag 变化"
$pausedTimeline = Measure-LagTimeline -Label 'a3-paused' -DurationSeconds $PauseSeconds -IntervalSeconds 5 -OutDir $OutDir
& (Join-Path $PSScriptRoot 'snapshot.ps1') -Phase 'a3-backlog-observed' -OutDir $OutDir -Quiet | Out-Null
$backlogObserved = Get-LagSample
$dbDuring = Get-FlashSaleDbState -ActivityId $ActivityId
Save-Evidence -Directory $OutDir -Name 'a3-db-during-pause.json' -Data $dbDuring | Out-Null

# ---- 5) 恢复：删除配额，观察 Lag 归零 ----------------------------------------
Write-Step '步骤 5：恢复消费（删除客户端配额）'
$quotaRemove = Set-ClientQuota -ClientId $script:ConsumerClientID -Remove
$quotaAfterRemove = Get-ClientQuota -ClientId $script:ConsumerClientID
Save-EvidenceText -Directory $OutDir -Name 'a4-quota-removed.txt' -Lines (@($quotaRemove) + @($quotaAfterRemove)) | Out-Null
$recoverTimeline = Measure-LagTimeline -Label 'a4-recovery' -DurationSeconds $RecoverTimeoutSeconds `
    -IntervalSeconds 5 -StopWhenZero -OutDir $OutDir
& (Join-Path $PSScriptRoot 'snapshot.ps1') -Phase 'a5-recovered' -OutDir $OutDir -Quiet | Out-Null
$after = Get-LagSample

# ---- 6) 业务终态核验 ---------------------------------------------------------
Write-Step '步骤 6：MySQL 权威核验'
$dbAfter = Get-FlashSaleDbState -ActivityId $ActivityId
Save-Evidence -Directory $OutDir -Name 'a6-db-final.json' -Data $dbAfter | Out-Null

$verdict = [ordered]@{
    experiment              = 'A: consumer backlog & recovery (broker alive)'
    activity_id             = $ActivityId
    sku_id                  = $SkuId
    run_id                  = $RunId
    requests_submitted      = $VUs
    k6_exit_code            = $k6Exit
    k6_trace_lines          = $traceLines.Count
    lag_before              = $before.broker_lag
    lag_while_paused_first  = if ($pausedTimeline.Count -gt 0) { $pausedTimeline[0].broker_lag } else { '' }
    lag_while_paused_last   = if ($pausedTimeline.Count -gt 0) { $pausedTimeline[$pausedTimeline.Count - 1].broker_lag } else { '' }
    lag_after_recovery      = $after.broker_lag
    committed_before        = $before.committed_offset
    committed_after         = $after.committed_offset
    recovery_samples        = $recoverTimeline.Count
    db_during_pause         = $dbDuring
    db_final                = $dbAfter
}
$verdictPath = Save-Evidence -Directory $OutDir -Name 'a.summary.json' -Data $verdict
Save-EvidenceText -Directory $OutDir -Name 'a.summary.txt' -Lines @(
    '=== 实验 A 结论 ==='
    "activity_id=$ActivityId sku_id=$SkuId run=$RunId requests=$VUs k6_exit=$k6Exit"
    "lag: before=$($verdict.lag_before) paused_first=$($verdict.lag_while_paused_first) paused_last=$($verdict.lag_while_paused_last) after_recovery=$($verdict.lag_after_recovery)"
    "committed_offset: before=$($verdict.committed_before) after=$($verdict.committed_after)"
    '--- db during pause ---'
    ($dbDuring.Keys | ForEach-Object { "  $_ = $($dbDuring[$_])" })
    '--- db final ---'
    ($dbAfter.Keys | ForEach-Object { "  $_ = $($dbAfter[$_])" })
) | Out-Null

Write-Step "实验 A 完成，结论写入 $verdictPath"
Write-Host ($verdict | ConvertTo-Json -Depth 4)
