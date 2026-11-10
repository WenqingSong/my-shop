# T5-04 实验 B：Kafka Broker 故障与恢复
#
# 目标：验证 Broker 不可用期间
#   1) 真实业务请求仍被正确受理（HTTP 侧不因 Kafka 故障而失败）；
#   2) 待发布事件被 Outbox 保留在 MySQL（不丢），并进入退避重试；
#   3) Broker 恢复后自动重新发布，Consumer 恢复消费，最终 MySQL 一致。
#
# 故障注入方式的选择（先确认工作负载类型再决定）：
#   manifest/kubernetes/infrastructure/base/kafka.yaml 里 Kafka 是 **Deployment**（不是 StatefulSet），
#   数据在 PVC kafka-data 上。因此用 `kubectl scale deployment/kafka --replicas=0` 制造「Broker 进程不存在」，
#   再 scale 回 1。整个过程不删 PVC、不删 Pod 之外的任何资源、不重建集群，
#   KRaft 元数据与日志都留在 PVC 上（CLUSTER_ID 固定，见 infrastructure configmap 注释）。
#
# 不做什么：
#   - 不删除 Topic、不 clean 日志、不改 replication factor；
#   - 不在故障期间重启应用（应用必须保持运行，才能证明 Outbox 在进程内持续重试）。

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][long]$ActivityId,
    [Parameter(Mandatory = $true)][long]$SkuId,
    [int]$Users = 10,
    [int]$VUs = 10,
    [string]$RunId = 't504-b-run1',
    [string]$OutDir = '',
    [int]$OutageSeconds = 45,
    [int]$RecoverTimeoutSeconds = 300
)

. (Join-Path $PSScriptRoot 'lib.ps1')

if ([string]::IsNullOrWhiteSpace($OutDir)) {
    $OutDir = Join-Path $script:RepoRoot 'docs/reports/t5-04-kafka-backlog-failure-recovery/raw/b'
}
if (-not (Test-Path $OutDir)) { New-Item -ItemType Directory -Path $OutDir -Force | Out-Null }
if ([string]::IsNullOrWhiteSpace($env:K6_TEST_PASSWORD)) {
    throw '缺少 K6_TEST_PASSWORD（测试用户密码，8~24 位）。'
}

Assert-Preconditions
Write-Step "实验 B 开始：activity=$ActivityId sku=$SkuId users=$Users vus=$VUs run=$RunId"

# ---- 1) 故障前 ---------------------------------------------------------------
Write-Step '步骤 1：采集故障前基线（Broker/Topic/Lag/Outbox）'
& (Join-Path $PSScriptRoot 'snapshot.ps1') -Phase 'b1-before' -OutDir $OutDir -Quiet | Out-Null
$before = Get-LagSample
$dbBefore = Get-FlashSaleDbState -ActivityId $ActivityId
$appImage = (@(Invoke-Kubectl -Arguments @('get', 'deploy/surgecart', '-n', $script:Namespace,
            '-o', 'jsonpath={.spec.template.spec.containers[0].image}')) -join '').Trim()
$outageStartUtc = Get-UtcStamp

# ---- 2) 注入：Broker 停机（scale to 0）---------------------------------------
Write-Step "步骤 2：注入 Broker 故障（kubectl scale deploy/kafka --replicas=0，之前 image=$appImage）"
$scaleDown = Invoke-Kubectl -Arguments @('scale', 'deploy/kafka', '-n', $script:Namespace, '--replicas=0')
$gone = Wait-KafkaPodGone -TimeoutSeconds 120
Write-Step "   Kafka Pod 已消失：$gone"
$probeDuring = [ordered]@{}
foreach ($p in @('/health', '/ready')) {
    try {
        $r = Invoke-WebRequest -Uri "$($script:AppBase)$p" -UseBasicParsing -TimeoutSec 10
        $probeDuring[$p] = "HTTP $($r.StatusCode): $($r.Content)"
    } catch {
        $code = if ($_.Exception.Response) { [int]$_.Exception.Response.StatusCode } else { 0 }
        # 冒号会触发 PowerShell 的 drive 语法解析（$code:），必须用 ${} 明确界定变量名。
        $probeDuring[$p] = "HTTP ${code}: $($_.Exception.Message)"
    }
}
& (Join-Path $PSScriptRoot 'snapshot.ps1') -Phase 'b2-outage' -OutDir $OutDir -Quiet | Out-Null

# ---- 3) 故障期间提交真实业务请求 ---------------------------------------------
Write-Step "步骤 3：Broker 故障期间通过 HTTP 入口提交 $VUs 个请求"
$env:FLASH_EXECUTOR = 'once'
$env:FLASH_ITERATIONS = '1'
$env:FLASH_USERS = "$Users"
$env:FLASH_VUS = "$VUs"
$env:FLASH_VERIFY = 'false'
$env:FLASH_TRACE = 'true'
$env:ACTIVITY_ID = "$ActivityId"
$env:SKU_ID = "$SkuId"
$env:DATA_PREFIX = 't504be'

$k6Runner = Join-Path $script:RepoRoot 'test/k6/scripts/run.ps1'
$prevEap = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
$k6Output = & powershell -NoProfile -ExecutionPolicy Bypass -File $k6Runner `
    -Scenario flashsale-order -RunId $RunId -BaseUrl 'http://host.docker.internal:18000' 2>&1
$k6Exit = $LASTEXITCODE
$ErrorActionPreference = $prevEap
$k6Lines = @($k6Output | ForEach-Object { $_.ToString() })
Save-EvidenceText -Directory $OutDir -Name "$RunId.k6-console.log" -Lines $k6Lines | Out-Null
$traceLines = @($k6Lines | Where-Object { $_ -match 'FLASH_TRACE' })
Save-EvidenceText -Directory $OutDir -Name "$RunId.flash-trace.txt" -Lines $traceLines | Out-Null
Write-Step "   k6 exit=$k6Exit trace=$($traceLines.Count)"

# ---- 4) 故障期间持续观察 Outbox 积压 -----------------------------------------
Write-Step "步骤 4：Broker 故障期间观察 $OutageSeconds 秒（Outbox 积压 / 发布失败 / 消费组状态）"
$outageTimeline = @()
$deadline = (Get-Date).AddSeconds($OutageSeconds)
while ((Get-Date) -lt $deadline) {
    $s = Get-LagSample
    $pf = $null; $ort = $null
    try { $pf = Get-PromValue -Query 'sum(flashsale_outbox_publish_failed_total)' } catch { $pf = $null }
    try { $ort = Get-PromValue -Query 'flashsale_outbox_retried_total' } catch { $ort = $null }
    # try/catch 不能直接作为 -NotePropertyValue 的表达式，先算到变量再挂上去。
    $s = $s | Add-Member -NotePropertyName publish_failed_total -NotePropertyValue $pf -PassThru -Force
    $s = $s | Add-Member -NotePropertyName outbox_retried_total -NotePropertyValue $ort -PassThru -Force
    $outageTimeline += $s
    Write-Step ("   outage: backlog={0} lag={1} joined={2} publish_failed={3} retried={4}" -f `
            $s.prom_backlog_pending, $s.broker_lag, $s.prom_consumer_joined, $s.publish_failed_total, $s.outbox_retried_total)
    Start-Sleep -Seconds 5
}
$outageSamples = $outageTimeline
Save-Evidence -Directory $OutDir -Name 'b3-outage-timeline.json' -Data $outageSamples | Out-Null
Save-EvidenceText -Directory $OutDir -Name 'b3-outage-timeline.txt' -Lines @($outageSamples | ForEach-Object {
        "{0}`tbacklog={1}`tlag={2}`tjoined={3}`tpublish_failed={4}`tretried={5}`tkafka_error={6}" -f `
            $_.at_utc, $_.prom_backlog_pending, $_.broker_lag, $_.prom_consumer_joined,
        $_.publish_failed_total, $_.outbox_retried_total, $_.kafka_error
    }) | Out-Null

$dbDuring = Get-FlashSaleDbState -ActivityId $ActivityId
Save-Evidence -Directory $OutDir -Name 'b3-db-during-outage.json' -Data $dbDuring | Out-Null
$logsDuring = Get-KafkaAdminLogTail -Tail 400 -Pattern 'flashsale_outbox|flashsale_consumer|kafka'
Save-EvidenceText -Directory $OutDir -Name 'b3-app-logs-outage.txt' -Lines $logsDuring | Out-Null
& (Join-Path $PSScriptRoot 'snapshot.ps1') -Phase 'b3-outage-loaded' -OutDir $OutDir -Quiet | Out-Null
$outageEndUtc = Get-UtcStamp

# ---- 5) 恢复 Broker -----------------------------------------------------------
Write-Step '步骤 5：恢复 Broker（kubectl scale deploy/kafka --replicas=1）'
$scaleUp = Invoke-Kubectl -Arguments @('scale', 'deploy/kafka', '-n', $script:Namespace, '--replicas=1')
$waitPod = Invoke-Kubectl -Arguments @('wait', '--for=condition=ready', 'pod',
    '-l', 'app.kubernetes.io/name=kafka', '-n', $script:Namespace, '--timeout=300s') -AllowFailure
$kafkaUp = Wait-KafkaReady -TimeoutSeconds 180
Write-Step "   Kafka 可响应元数据请求：$kafkaUp"
$recoverStartUtc = Get-UtcStamp

# ---- 6) 恢复观察：Outbox 重发 + Consumer 消费 --------------------------------
Write-Step '步骤 6：等待 Outbox 重发并收敛（backlog→0 且 lag→0）'
$recoverTimeline = @()
$deadline = (Get-Date).AddSeconds($RecoverTimeoutSeconds)
while ((Get-Date) -lt $deadline) {
    $s = Get-LagSample
    $recoverTimeline += $s
    Write-Step ("   recover: backlog={0} lag={1} joined={2}" -f $s.prom_backlog_pending, $s.broker_lag, $s.prom_consumer_joined)
    $backlogOk = ($s.prom_backlog_pending -eq '0' -or $s.prom_backlog_pending -eq 0)
    $lagOk = ($s.broker_lag -eq '0')
    if ($backlogOk -and $lagOk) { break }
    Start-Sleep -Seconds 5
}
$recoverSamples = $recoverTimeline
Save-Evidence -Directory $OutDir -Name 'b4-recovery-timeline.json' -Data $recoverSamples | Out-Null
Save-EvidenceText -Directory $OutDir -Name 'b4-recovery-timeline.txt' -Lines @($recoverSamples | ForEach-Object {
        "{0}`tbacklog={1}`tlag={2}`tjoined={3}" -f $_.at_utc, $_.prom_backlog_pending, $_.broker_lag, $_.prom_consumer_joined
    }) | Out-Null

& (Join-Path $PSScriptRoot 'snapshot.ps1') -Phase 'b5-recovered' -OutDir $OutDir -Quiet | Out-Null
$after = Get-LagSample
$dbAfter = Get-FlashSaleDbState -ActivityId $ActivityId
Save-Evidence -Directory $OutDir -Name 'b5-db-final.json' -Data $dbAfter | Out-Null
$logsAfter = Get-KafkaAdminLogTail -Tail 400 -Pattern 'flashsale_outbox|flashsale_consumer|kafka'
Save-EvidenceText -Directory $OutDir -Name 'b5-app-logs-recovered.txt' -Lines $logsAfter | Out-Null

$verdict = [ordered]@{
    experiment             = 'B: kafka broker outage & recovery'
    activity_id            = $ActivityId
    sku_id                 = $SkuId
    run_id                 = $RunId
    requests_submitted     = $VUs
    k6_exit_code           = $k6Exit
    kafka_pod_gone         = $gone
    kafka_ready_after_up   = $kafkaUp
    app_probe_during       = $probeDuring
    outage_window          = @{ start_utc = $outageStartUtc; end_utc = $outageEndUtc }
    recovery_window_start  = $recoverStartUtc
    lag_before             = $before.broker_lag
    lag_during_last        = if ($outageSamples.Count -gt 0) { $outageSamples[$outageSamples.Count - 1].broker_lag } else { '' }
    backlog_before         = $before.prom_backlog_pending
    backlog_during_last    = if ($outageSamples.Count -gt 0) { $outageSamples[$outageSamples.Count - 1].prom_backlog_pending } else { '' }
    backlog_after          = $after.prom_backlog_pending
    lag_after              = $after.broker_lag
    committed_before       = $before.committed_offset
    committed_after        = $after.committed_offset
    db_before              = $dbBefore
    db_during_outage       = $dbDuring
    db_final               = $dbAfter
    recovery_seconds       = if ($recoverSamples.Count -gt 0) {
        ([datetime]::Parse($recoverSamples[$recoverSamples.Count - 1].at_utc) -
            [datetime]::Parse($recoverStartUtc)).TotalSeconds
    } else { $null }
}
$verdictPath = Save-Evidence -Directory $OutDir -Name 'b.summary.json' -Data $verdict
Save-EvidenceText -Directory $OutDir -Name 'b.summary.txt' -Lines @(
    '=== 实验 B 结论 ==='
    "activity_id=$ActivityId requests=$VUs k6_exit=$k6Exit"
    "kafka pod gone=$gone; kafka ready after restart=$kafkaUp"
    "backlog: before=$($verdict.backlog_before) during_last=$($verdict.backlog_during_last) after=$($verdict.backlog_after)"
    "lag: before=$($verdict.lag_before) during_last=$($verdict.lag_during_last) after=$($verdict.lag_after)"
    "committed: before=$($verdict.committed_before) after=$($verdict.committed_after)"
    "app probes during outage: $($probeDuring.GetEnumerator() | ForEach-Object { "$($_.Key)=$($_.Value)" } | Out-String)"
    '--- db during outage ---'
    ($dbDuring.Keys | ForEach-Object { "  $_ = $($dbDuring[$_])" })
    '--- db final ---'
    ($dbAfter.Keys | ForEach-Object { "  $_ = $($dbAfter[$_])" })
) | Out-Null

Write-Step "实验 B 完成，结论写入 $verdictPath"
Write-Host ($verdict | ConvertTo-Json -Depth 4)
