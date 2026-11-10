# T5-04 实验 C：Consumer 异常退出与 Offset 恢复（两个子实验）
#
# 子实验 C-dup（重复消费安全）：
#   把「已经处理成功的请求」的同一事件再次投递，验证消费端用 MySQL 请求行状态
#   （status != queued）挡掉重复，不会产生重复订单、不会重复扣库存。
#   这是对「消息可能被重复投递」这一 at-least-once 后果的直接、确定性证明。
#
# 子实验 C-crash（未提交消息跨崩溃恢复）：
#   用实验 A 的客户端配额把消费能力压到 0，制造「10 条消息已发布但 offset 未提交」，
#   然后 `kubectl delete pod --grace-period=0 --force`（SIGKILL，无优雅关闭），
#   重启后观察：已提交 offset 是否保留、未提交消息是否被重新投递、最终是否无丢失无重复。
#
# 明确 NOT_VERIFIED（写入报告，不夸大）：
#   「MySQL 事务已提交、但 Kafka offset 尚未提交」这个微秒级窗口，无法在真实集群里
#   用可复现手段精确命中（代码顺序：先 COMMIT 后 CommitRecords，窗口只有几毫秒）。
#   本实验证明的是该窗口之外的两个事实：未提交消息跨崩溃不丢、重复投递被幂等闸门挡住。
#   由这两点可以推出该窗口的后果（至多重复投递一次，不会丢），但不能声称「实测命中该窗口」。

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][long]$DupActivityId,
    [Parameter(Mandatory = $true)][long]$DupSkuId,
    [Parameter(Mandatory = $true)][long]$CrashActivityId,
    [Parameter(Mandatory = $true)][long]$CrashSkuId,
    [int]$Users = 10,
    [int]$VUs = 10,
    [string]$RunIdDup = 't504-c-dup',
    [string]$RunIdCrash = 't504-c-crash',
    [string]$OutDir = '',
    [int]$RecoverTimeoutSeconds = 240,
    # 断点续跑开关：故障注入脚本必须能从「已产生未提交积压」的状态继续，
    # 否则一次脚本缺陷就会逼着重做整个子实验（重复造数据、污染上一个子实验的结论）。
    [switch]$SkipDup,
    [switch]$SkipProduce,
    # 只做 C-dup 时用：否则复投续跑会顺带对应用 Pod 再做一次 SIGKILL（无积压的空故障），
    # 把上一轮真实的 crash 证据覆盖掉（实测踩到）。
    [switch]$SkipCrash,
    # 仅复投指定 offset（续跑用）：跳过 k6 提交，直接重放历史事件。
    # 用字符串接收再自行切分：从命令行传 `-DupReplayOffsets 94,95,96` 时，
    # powershell -File 会把整串当成一个字符串，直接声明 [long[]] 会得到 949596 这种单值。
    [string]$DupReplayOffsets = ''
)

. (Join-Path $PSScriptRoot 'lib.ps1')

if ([string]::IsNullOrWhiteSpace($OutDir)) {
    $OutDir = Join-Path $script:RepoRoot 'docs/reports/t5-04-kafka-backlog-failure-recovery/raw/c'
}
if (-not (Test-Path $OutDir)) { New-Item -ItemType Directory -Path $OutDir -Force | Out-Null }
if ([string]::IsNullOrWhiteSpace($env:K6_TEST_PASSWORD)) {
    throw '缺少 K6_TEST_PASSWORD（测试用户密码，8~24 位）。'
}

function Invoke-K6Order {
    param([long]$ActivityId, [long]$SkuId, [string]$RunId, [string]$Prefix)
    $env:FLASH_EXECUTOR = 'once'
    $env:FLASH_ITERATIONS = '1'
    $env:FLASH_USERS = "$Users"
    $env:FLASH_VUS = "$VUs"
    $env:FLASH_VERIFY = 'false'
    $env:FLASH_TRACE = 'true'
    $env:ACTIVITY_ID = "$ActivityId"
    $env:SKU_ID = "$SkuId"
    $env:DATA_PREFIX = $Prefix
    $k6Runner = Join-Path $script:RepoRoot 'test/k6/scripts/run.ps1'
    $prevEap = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    $out = & powershell -NoProfile -ExecutionPolicy Bypass -File $k6Runner `
        -Scenario flashsale-order -RunId $RunId -BaseUrl 'http://host.docker.internal:18000' 2>&1
    $exit = $LASTEXITCODE
    $ErrorActionPreference = $prevEap
    $lines = @($out | ForEach-Object { $_.ToString() })
    Save-EvidenceText -Directory $OutDir -Name "$RunId.k6-console.log" -Lines $lines | Out-Null
    $trace = @($lines | Where-Object { $_ -match 'FLASH_TRACE' })
    Save-EvidenceText -Directory $OutDir -Name "$RunId.flash-trace.txt" -Lines $trace | Out-Null
    Write-Step "   k6 exit=$exit trace=$($trace.Count)"
    return $exit
}

function Get-DuplicateCount {
    try { return Get-PromValue -Query 'sum(flashsale_consumer_processed_total{result="duplicate"})' } catch { return $null }
}

Assert-Preconditions

# ============================================================================
# 子实验 C-dup
# ============================================================================
if ($SkipDup) {
    Write-Step '子实验 C-dup：按 -SkipDup 跳过（结论已在上一轮落盘）'
    $dupBefore = $null; $dupAfter = $null
    $dbBeforeDup = [ordered]@{}; $dbAfterDup = [ordered]@{}
    $dupMessages = @()
} else {
Write-Step "子实验 C-dup：重复消息安全（activity=$DupActivityId sku=$DupSkuId）"
& (Join-Path $PSScriptRoot 'snapshot.ps1') -Phase 'c1-dup-before' -OutDir $OutDir -Quiet | Out-Null
$baseBefore = Get-LagSample
$baseOffset = $baseBefore.committed_offset

if (-not [string]::IsNullOrWhiteSpace($DupReplayOffsets)) {
    # 仅复投模式：用于「上一轮已经把这批请求跑成功、但复投环节本身有缺陷」的续跑。
    # 不重复提交业务请求（那会消耗库存、改变对照基线），只重放指定 offset 的历史事件。
    $dupOffsets = @($DupReplayOffsets -split '[,\s]+' | Where-Object { $_ -match '^\d+$' } | ForEach-Object { [long]$_ })
    if ($dupOffsets.Count -eq 0) { throw "-DupReplayOffsets 解析后为空：$DupReplayOffsets" }
    Write-Step "   -DupReplayOffsets 指定复投（跳过 k6 提交与 drain）：$($dupOffsets -join ', ')"
} else {
    Write-Step "   提交 10 个请求（正常消费）"
    Invoke-K6Order -ActivityId $DupActivityId -SkuId $DupSkuId -RunId $RunIdDup -Prefix 't504cf' | Out-Null

    # 等待这 10 条全部消费完（lag 归零）
    Measure-LagTimeline -Label 'c1-dup-drain' -DurationSeconds 60 -IntervalSeconds 3 -StopWhenZero -OutDir $OutDir | Out-Null

    # PowerShell 解析陷阱（本任务实测踩到，导致首轮 C-dup 中断）：
    #   @([long]$baseOffset, [long]$baseOffset + 1, [long]$baseOffset + 2)
    # 里的 `[long]$baseOffset + 1` 会被拆成两个数组元素（实测得到 82,82,1,82,2），
    # 于是脚本读到了偏移 1 这种错误位置。必须先把偏移算进变量或用括号包住整个表达式。
    $dupOffsets = @([long]$baseOffset, ([long]$baseOffset + 1), ([long]$baseOffset + 2))
}
$dupBefore = Get-DuplicateCount
$dbBeforeDup = Get-FlashSaleDbState -ActivityId $DupActivityId
Write-Step "   复投 3 条已成功请求的事件（offset $($dupOffsets -join ', ')）"
$dupMessages = @()
foreach ($off in $dupOffsets) {
    $msg = Get-TopicMessage -Topic $script:Topic -Partition 0 -Offset $off
    $dupMessages += $msg
    Produce-TopicMessage -Topic $script:Topic -Payload $msg | Out-Null
    Write-Step "   re-produced offset=$off event_id=$($msg.Substring(0, [Math]::Min(40, $msg.Length)))..."
}
Save-EvidenceText -Directory $OutDir -Name 'c1-dup-reproduced.txt' -Lines $dupMessages | Out-Null

# 复投后立即读回校验：消息体必须以 '{' 开头。
# 这条断言的存在原因：PowerShell 5.1 的文本管道会给 native stdin 写入 UTF-8 BOM，
# 复投出来的消息首字符变成 U+FEFF，消费端会把它判成 parse_error 走 DLQ ——
# 表面上「复投成功、lag 归零」，实际上完全跑偏了另一个分支（实测踩到）。
$lastLatest = (Get-TopicOffset -Topic $script:Topic -Time -1)['0']
$readBack = Get-TopicMessage -Topic $script:Topic -Partition 0 -Offset ([long]$lastLatest - 1)
if (-not $readBack.StartsWith('{')) {
    throw "复投消息体未以 '{' 开头（疑似 BOM 污染）：$($readBack.Substring(0, [Math]::Min(40, $readBack.Length)))"
}
Write-Step "   复投消息读回校验通过（无 BOM，latest=$lastLatest）"

Start-Sleep -Seconds 12
$dupAfter = Get-DuplicateCount
$dbAfterDup = Get-FlashSaleDbState -ActivityId $DupActivityId
Save-Evidence -Directory $OutDir -Name 'c1-dup-result.json' -Data ([ordered]@{
        duplicate_before = $dupBefore
        duplicate_after  = $dupAfter
        db_before        = $dbBeforeDup
        db_after         = $dbAfterDup
    }) | Out-Null
}

# ============================================================================
# 子实验 C-crash
# ============================================================================
if ($SkipCrash) {
    Write-Step '子实验 C-crash：按 -SkipCrash 跳过（本轮只验证 C-dup）'
    $paused = [pscustomobject]@{ committed_offset = ''; log_end_offset = ''; broker_lag = '' }
    $after = $paused
    $recover = @()
    $dbAfterCrash = [ordered]@{}
} else {
Write-Step "子实验 C-crash：未提交消息跨 SIGKILL 恢复（activity=$CrashActivityId sku=$CrashSkuId）"
if (-not $SkipProduce) {
    & (Join-Path $PSScriptRoot 'snapshot.ps1') -Phase 'c2-crash-before' -OutDir $OutDir -Quiet | Out-Null

    Write-Step '   步骤 1：暂停消费（客户端配额），制造未提交积压'
    Set-ClientQuota -ClientId $script:ConsumerClientID -ConsumerByteRate 1 | Out-Null
    Invoke-K6Order -ActivityId $CrashActivityId -SkuId $CrashSkuId -RunId $RunIdCrash -Prefix 't504cg' | Out-Null
    Start-Sleep -Seconds 12
} else {
    # 断点续跑：上一轮已经在「配额已加、消息已产生、offset 未提交」的状态停下，
    # 这里重新确认配额仍在（幂等）并直接沿用现有积压，绝不重复提交测试请求。
    Write-Step '   步骤 1（续跑）：沿用上一轮已产生的未提交积压，重新确认消费配额'
    Set-ClientQuota -ClientId $script:ConsumerClientID -ConsumerByteRate 1 | Out-Null
}
$paused = Get-LagSample
& (Join-Path $PSScriptRoot 'snapshot.ps1') -Phase 'c2-crash-paused' -OutDir $OutDir -Quiet | Out-Null
Write-Step "   暂停后：committed=$($paused.committed_offset) logEnd=$($paused.log_end_offset) lag=$($paused.broker_lag)"

Write-Step '   步骤 2：SIGKILL 应用 Pod（grace-period=0 --force，无优雅关闭）'
# 选择器必须限定 status.phase=Running 且只取第一个名字：
# 同标签的 surgecart-migrate-* Job Pod 处于 Completed，会被 -l 一起匹配；
# 早先版本用 (-join '') 拼接多行，得到了 "surgecart-xxxsurgecart-migrate-yyy" 这种
# 不存在的 Pod 名，kubectl delete 直接 NotFound（实测踩到）。
$runningPods = @(Invoke-Kubectl -Arguments @('get', 'pods', '-n', $script:Namespace,
        '-l', 'app.kubernetes.io/name=surgecart', '--field-selector=status.phase=Running',
        '-o', 'jsonpath={.items[*].metadata.name}'))
$podName = (($runningPods -join ' ').Trim() -split '\s+')[0]
if ([string]::IsNullOrWhiteSpace($podName)) { throw '未找到 Running 状态的 surgecart Pod，无法执行 SIGKILL 故障注入' }
Write-Step "   目标 Pod：$podName"
$killOut = Invoke-Kubectl -Arguments @('delete', 'pod', $podName, '-n', $script:Namespace, '--grace-period=0', '--force')
Save-EvidenceText -Directory $OutDir -Name 'c2-crash-kill.txt' -Lines $killOut | Out-Null

Write-Step '   步骤 3：等待新 Pod 就绪（readiness 只依赖 MySQL+Redis，不依赖 Kafka）'
# 不能用 `kubectl wait pod -l ...`：该标签同时匹配 Completed 的 migrate Job，
# 它对 Ready 条件永远不成立，会一直等到超时。改为等待 Deployment 的滚动完成。
$wait = Invoke-Kubectl -Arguments @('rollout', 'status', 'deploy/surgecart', '-n', $script:Namespace,
    '--timeout=240s') -AllowFailure
Save-EvidenceText -Directory $OutDir -Name 'c2-crash-rollout.txt' -Lines @($wait) | Out-Null
& (Join-Path $PSScriptRoot 'snapshot.ps1') -Phase 'c2-crash-restarted' -OutDir $OutDir -Quiet | Out-Null

Write-Step '   步骤 4：恢复消费（删除配额），观察未提交消息被重新投递'
Set-ClientQuota -ClientId $script:ConsumerClientID -Remove | Out-Null
$recover = Measure-LagTimeline -Label 'c2-crash-recovery' -DurationSeconds $RecoverTimeoutSeconds `
    -IntervalSeconds 3 -StopWhenZero -OutDir $OutDir
$after = Get-LagSample
$dbAfterCrash = Get-FlashSaleDbState -ActivityId $CrashActivityId
Save-Evidence -Directory $OutDir -Name 'c2-crash-result.json' -Data ([ordered]@{
        committed_before_kill = $paused.committed_offset
        logend_before_kill    = $paused.log_end_offset
        committed_after       = $after.committed_offset
        lag_after             = $after.broker_lag
        recovery_samples      = $recover.Count
        db_final              = $dbAfterCrash
    }) | Out-Null
& (Join-Path $PSScriptRoot 'snapshot.ps1') -Phase 'c2-crash-recovered' -OutDir $OutDir -Quiet | Out-Null
}

# ============================================================================
# 汇总
# ============================================================================
# 续跑（-SkipDup）时本轮没有 C-dup 数据：从上一轮已落盘的 c1-dup-result.json 回填，
# 避免汇总文件把真实结论覆盖成 null。
$dupResultPath = Join-Path $OutDir 'c1-dup-result.json'
if ($SkipDup -and (Test-Path $dupResultPath)) {
    $prevDup = Get-Content $dupResultPath -Raw | ConvertFrom-Json
    $dupBefore = $prevDup.duplicate_before
    $dupAfter = $prevDup.duplicate_after
    $dbAfterDup = $prevDup.db_after
}
$crashResultPath = Join-Path $OutDir 'c2-crash-result.json'
if ($SkipCrash -and (Test-Path $crashResultPath)) {
    $prevCrash = Get-Content $crashResultPath -Raw | ConvertFrom-Json
    $paused = [pscustomobject]@{
        committed_offset = $prevCrash.committed_before_kill
        log_end_offset   = $prevCrash.logend_before_kill
    }
    $after = [pscustomobject]@{ committed_offset = $prevCrash.committed_after; broker_lag = $prevCrash.lag_after }
    $dbAfterCrash = $prevCrash.db_final
}
function Format-KvLines($kv, [string]$Prefix) {
    # 汇总时 $dbAfterDup 可能来自本轮（ordered hashtable）或上一轮 JSON（PSCustomObject），
    # 两种类型的枚举方式不同，统一在这里处理，避免汇总文本悄悄变成空行。
    if ($null -eq $kv) { return @() }
    if ($kv -is [System.Collections.IDictionary]) {
        return @($kv.Keys | ForEach-Object { "$Prefix $_ = $($kv[$_])" })
    }
    return @($kv.PSObject.Properties | ForEach-Object { "$Prefix $($_.Name) = $($_.Value)" })
}

$verdict = [ordered]@{
    experiment        = 'C: consumer crash & offset recovery'
    dup_activity      = $DupActivityId
    crash_activity    = $CrashActivityId
    dup_duplicate_before = $dupBefore
    dup_duplicate_after  = $dupAfter
    dup_db_final         = $dbAfterDup
    crash_committed_before_kill = $paused.committed_offset
    crash_logend_before_kill    = $paused.log_end_offset
    crash_committed_after       = $after.committed_offset
    crash_lag_after             = $after.broker_lag
    crash_db_final              = $dbAfterCrash
    note_not_verified = 'MySQL已提交但offset未提交的微秒级窗口无法精确复现，标记 NOT_VERIFIED；本实验证明其两侧（不丢/幂等）。'
}
$verdictPath = Save-Evidence -Directory $OutDir -Name 'c.summary.json' -Data $verdict
Save-EvidenceText -Directory $OutDir -Name 'c.summary.txt' -Lines @(
    '=== 实验 C 结论 ==='
    "dup activity=${DupActivityId}: duplicate $dupBefore -> $dupAfter"
    (Format-KvLines -kv $dbAfterDup -Prefix '  dup.db')
    "crash activity=${CrashActivityId}: committed $($paused.committed_offset) -> $($after.committed_offset) (logEnd $($paused.log_end_offset)), lag now $($after.broker_lag)"
    (Format-KvLines -kv $dbAfterCrash -Prefix '  crash.db')
    "NOT_VERIFIED: $($verdict.note_not_verified)"
) | Out-Null

Write-Step "实验 C 完成，结论写入 $verdictPath"
Write-Host ($verdict | ConvertTo-Json -Depth 4)
