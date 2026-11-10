# T5-04：环境快照（只读）。每次故障注入前 / 期间 / 恢复后各拍一次，用于交叉验证。
#
# 用法：
#   ./snapshot.ps1 -Phase baseline
#   ./snapshot.ps1 -Phase a-paused -OutDir ..\..\docs\reports\t5-04-...\raw\a
#
# 产出：<OutDir>/<Phase>.snapshot.json + <OutDir>/<Phase>.snapshot.txt
#   为什么同时落 JSON 与 TXT：JSON 便于后续脚本/文档引用精确值，
#   TXT 是「人可直接阅读的原始证据」，避免报告里的数字无法回溯。

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Phase,
    [string]$OutDir = '',
    [switch]$SkipKafkaDescribe,
    # 由实验脚本批量调用时静默：快照仍完整落盘，只是不再把摘要打到控制台。
    [switch]$Quiet
)

. (Join-Path $PSScriptRoot 'lib.ps1')

if ([string]::IsNullOrWhiteSpace($OutDir)) {
    $OutDir = Join-Path $script:RepoRoot 'docs/reports/t5-04-kafka-backlog-failure-recovery/raw'
}
if (-not (Test-Path $OutDir)) { New-Item -ItemType Directory -Path $OutDir -Force | Out-Null }

Write-Step "采集快照 phase=$Phase -> $OutDir"

# ---- Kafka -------------------------------------------------------------------
# Broker 故障期间（实验 B）这些命令必然失败：快照必须继续采集其余部分，
# 并把失败原因原样记录 —— 「Kafka CLI 不可用」本身就是故障证据。
$kafkaErrors = @()
$topicEtas = @()
$dlqEtas = @()
$groupRows = @()
$quota = @()
try { $topicEtas = @(Get-TopicEtas -Topic $script:Topic) } catch { $kafkaErrors += "topic-offsets: $($_.Exception.Message)" }
try { $dlqEtas = @(Get-TopicEtas -Topic $script:DlqTopic) } catch { $kafkaErrors += "dlq-offsets: $($_.Exception.Message)" }
try { $groupRows = @(Get-ConsumerGroupState) } catch { $kafkaErrors += "group-describe: $($_.Exception.Message)" }
try { $quota = @(Get-ClientQuota -ClientId $script:ConsumerClientID) } catch { $kafkaErrors += "quota-describe: $($_.Exception.Message)" }

# ---- MySQL -------------------------------------------------------------------
# 一条 SQL 覆盖所有需要长期追踪的计数：口径固定，避免不同脚本各写一套 SQL。
$mysqlKv = Invoke-SqlKv -Sql @'
SELECT 'activities_total', COUNT(*) FROM flash_sale_activities
UNION ALL SELECT 'activity_skus_total', COUNT(*) FROM flash_sale_activity_skus
UNION ALL SELECT 'orders_total', COUNT(*) FROM flash_sale_orders
UNION ALL SELECT 'requests_total', COUNT(*) FROM flash_sale_order_requests
UNION ALL SELECT 'requests_queued', COUNT(*) FROM flash_sale_order_requests WHERE status = 0
UNION ALL SELECT 'requests_success', COUNT(*) FROM flash_sale_order_requests WHERE status = 1
UNION ALL SELECT 'requests_failed', COUNT(*) FROM flash_sale_order_requests WHERE status = 2
UNION ALL SELECT 'requests_dead', COUNT(*) FROM flash_sale_order_requests WHERE status = 3
UNION ALL SELECT 'requests_unpublished', COUNT(*) FROM flash_sale_order_requests WHERE published_at IS NULL
UNION ALL SELECT 'requests_unpublished_queued', COUNT(*) FROM flash_sale_order_requests WHERE published_at IS NULL AND status = 0
UNION ALL SELECT 'requests_retry_pending', COUNT(*) FROM flash_sale_order_requests WHERE retry_count > 0
UNION ALL SELECT 'requests_lease_active', COUNT(*) FROM flash_sale_order_requests WHERE publish_lease_until IS NOT NULL
UNION ALL SELECT 'max_request_id', COALESCE(MAX(id),0) FROM flash_sale_order_requests
UNION ALL SELECT 'max_order_id', COALESCE(MAX(id),0) FROM flash_sale_orders
UNION ALL SELECT 'mysql_now', NOW()
UNION ALL SELECT 'mysql_tz', @@session.time_zone
'@

# ---- Redis -------------------------------------------------------------------
$redisKv = [ordered]@{}
try {
    $redisKv['dbsize'] = (@(Invoke-Kubectl -Arguments @('exec', '-n', $script:Namespace, $script:RedisTarget, '--',
                'redis-cli', 'dbsize')) -join '').Trim()
    foreach ($pattern in @('flashsale:stock:*', 'flashsale:bought:*', 'flashsale:activity:*', 'flashsale:null:*', 'flashsale:soldout:*')) {
        $cmd = "redis-cli --scan --pattern $pattern | wc -l"
        $n = (@(Invoke-Kubectl -Arguments @('exec', '-n', $script:Namespace, $script:RedisTarget, '--', 'sh', '-c', $cmd)) -join '').Trim()
        $redisKv[$pattern] = $n
    }
} catch {
    $redisKv['error'] = $_.Exception.Message
}

# ---- Prometheus --------------------------------------------------------------
# 指标名以源码 internal/metrics/*.go 为准（全部已在 /metrics 实际出现）。
$promQueries = [ordered]@{
    'kafka_consumer_lag'          = "surgecart_kafka_consumer_lag{consumer_group=`"$($script:ConsumerGroup)`"}"
    'outbox_backlog_pending'      = 'flashsale_outbox_backlog_pending'
    'outbox_oldest_age_seconds'   = 'surgecart_outbox_oldest_age_seconds'
    'sampler_up_outbox'           = 'surgecart_sampler_up{sampler="outbox"}'
    'sampler_up_kafka_lag'        = 'surgecart_sampler_up{sampler="kafka_lag"}'
    'consumer_joined'             = 'flashsale_consumer_joined'
    'consumer_records_total'      = 'flashsale_consumer_records_total'
    'consumer_processed_success'  = 'flashsale_consumer_processed_total{result="success"}'
    'consumer_processed_failed'   = 'flashsale_consumer_processed_total{result="failed"}'
    'consumer_processed_duplicate'= 'flashsale_consumer_processed_total{result="duplicate"}'
    'consumer_processed_invalid'  = 'flashsale_consumer_processed_total{result="invalid"}'
    'consumer_processed_dead'     = 'flashsale_consumer_processed_total{result="dead"}'
    'consumer_processed_error'    = 'flashsale_consumer_processed_total{result="error"}'
    'consumer_retries_total'      = 'flashsale_consumer_retries_total'
    'consumer_dlq_total'          = 'sum(flashsale_consumer_dlq_total)'
    'consumer_commit_failures'    = 'flashsale_consumer_commit_failures_total'
    'consumer_restarts_total'     = 'flashsale_consumer_restarts_total'
    'outbox_published_total'      = 'flashsale_outbox_published_total'
    'outbox_publish_failed_total' = 'sum(flashsale_outbox_publish_failed_total)'
    'outbox_retried_total'        = 'flashsale_outbox_retried_total'
    'outbox_claimed_total'        = 'flashsale_outbox_claimed_total'
    'outbox_lease_reclaimed'      = 'flashsale_outbox_lease_reclaimed_total'
    'http_requests_total'         = 'sum(surgecart_http_requests_total)'
}
$prom = [ordered]@{}
foreach ($key in $promQueries.Keys) {
    try { $prom[$key] = Get-PromValue -Query $promQueries[$key] } catch { $prom[$key] = "query-failed: $($_.Exception.Message)" }
}

# ---- Kubernetes --------------------------------------------------------------
$pods = @(Invoke-Kubectl -Arguments @('get', 'pods', '-n', $script:Namespace, '-o', 'wide'))
$kafkaReplicas = (@(Invoke-Kubectl -Arguments @('get', 'deploy/kafka', '-n', $script:Namespace,
            '-o', 'jsonpath={.spec.replicas}/{.status.readyReplicas}')) -join '').Trim()
$pvc = @(Invoke-Kubectl -Arguments @('get', 'pvc', '-n', $script:Namespace))

$snapshot = [ordered]@{
    phase             = $Phase
    captured_at_utc   = Get-UtcStamp
    captured_at_cst   = Get-ShanghaiStamp
    kafka             = [ordered]@{
        topic         = $script:Topic
        dlq_topic     = $script:DlqTopic
        topic_etas    = $topicEtas
        dlq_etas      = $dlqEtas
        consumer_group= $script:ConsumerGroup
        group_state   = $groupRows
        # Broker 不可用时 Lag 无从查询：写 -1 并保留 kafka_errors，避免把「查不到」误写成 0。
        lag_total     = if ($kafkaErrors.Count -eq 0) { (Get-ConsumerGroupLagTotal) } else { -1 }
        client_quota  = $quota
        kafka_errors  = $kafkaErrors
    }
    mysql             = $mysqlKv
    redis             = $redisKv
    prometheus        = $prom
    kubernetes        = [ordered]@{
        kafka_replicas = $kafkaReplicas
        pods           = $pods
        pvc            = $pvc
    }
}

$jsonPath = Save-Evidence -Directory $OutDir -Name "$Phase.snapshot.json" -Data $snapshot

# ---- 人可读摘要 --------------------------------------------------------------
$txt = New-Object System.Collections.Generic.List[string]
$txt.Add("=== T5-04 snapshot: $Phase ===")
$txt.Add("utc=$($snapshot.captured_at_utc) cst=$($snapshot.captured_at_cst)")
$txt.Add('')
$txt.Add('--- Kafka topic ETAs ---')
foreach ($p in $topicEtas) { $txt.Add("  partition=$($p.partition) earliest=$($p.earliest) latest=$($p.latest)") }
$txt.Add('--- Kafka DLQ ETAs ---')
foreach ($p in $dlqEtas) { $txt.Add("  partition=$($p.partition) earliest=$($p.earliest) latest=$($p.latest)") }
$txt.Add("--- consumer group $($script:ConsumerGroup) (lag_total=$($snapshot.kafka.lag_total)) ---")
foreach ($g in $groupRows) { $txt.Add("  $($g | ConvertTo-Json -Compress -Depth 4)") }
$txt.Add('--- client quota (client.id=' + $script:ConsumerClientID + ') ---')
foreach ($q in $quota) { $txt.Add("  $q") }
$txt.Add('--- MySQL ---')
foreach ($k in $mysqlKv.Keys) { $txt.Add("  $k = $($mysqlKv[$k])") }
$txt.Add('--- Redis ---')
foreach ($k in $redisKv.Keys) { $txt.Add("  $k = $($redisKv[$k])") }
$txt.Add('--- Prometheus ---')
foreach ($k in $prom.Keys) { $txt.Add("  $k = $($prom[$k])") }
$txt.Add('--- Kubernetes ---')
$txt.Add("  kafka deployment (spec/ready) = $kafkaReplicas")
foreach ($line in $pods) { $txt.Add("  $line") }
foreach ($line in $pvc) { $txt.Add("  $line") }
$txtPath = Save-EvidenceText -Directory $OutDir -Name "$Phase.snapshot.txt" -Lines $txt.ToArray()

Write-Step "快照已写入：$jsonPath"
Write-Step "              $txtPath"
if (-not $Quiet) {
    Write-Host (($txt | Select-Object -First 40) -join "`n")
}
