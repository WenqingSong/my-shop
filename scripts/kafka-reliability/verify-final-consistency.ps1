# T5-04 第三阶段：最终业务一致性核验（只读）
#
# 为什么单独一个脚本：
#   实验 A~D 各自的 summary 只回答「单项实验是否成立」，而最终验收要回答
#   「多次故障注入之后，Kafka / MySQL / Redis 三个存储面是否仍然互相对得上」。
#   这个脚本只做只读采集与交叉核对，不含任何故障注入、不写业务表、不动 Redis/Kafka。
#
# 检查项：
#   1. Kafka：Topic/DLQ 位移、消费组位移与 Lag、是否仍有未消费消息；
#   2. MySQL：本任务 4 个实验的测试活动终态（请求终态分布、订单数、唯一性、库存 sold/remaining）；
#   3. Redis：测试活动的缓存键分布、库存缓存与 MySQL 是否一致（对仍活跃的活动）；
#   4. Prometheus：Lag / Outbox Backlog / Oldest Age / sampler_up / HTTP 的区间数据；
#   5. Kubernetes：Pod、Deployment 副本、Kafka PVC。
#
# 输出：docs/reports/t5-04-.../raw/final/*.json|txt

[CmdletBinding()]
param(
    [string]$OutDir = '',
    [int]$RangeMinutes = 180,
    [int]$StepSeconds = 30,
    # 本任务创建的全部测试活动（只读核对，不修改）。
    [long[]]$ActivityIds = @(16, 17, 18, 19, 20, 21, 22, 23)
)

. (Join-Path $PSScriptRoot 'lib.ps1')

if ([string]::IsNullOrWhiteSpace($OutDir)) {
    $OutDir = Join-Path $script:RepoRoot 'docs/reports/t5-04-kafka-backlog-failure-recovery/raw/final'
}
if (-not (Test-Path $OutDir)) { New-Item -ItemType Directory -Path $OutDir -Force | Out-Null }

Assert-Preconditions
Write-Step "最终一致性核验：activities=$($ActivityIds -join ',')"

# ---- 1) Kafka ----------------------------------------------------------------
Write-Step '1) Kafka 位移与消费组'
$topicEtas = @(Get-TopicEtas -Topic $script:Topic)
$dlqEtas = @(Get-TopicEtas -Topic $script:DlqTopic)
$groupState = @(Get-ConsumerGroupState)
$lag = Get-ConsumerGroupLagTotal
$lastCommitted = $groupState | Where-Object { $null -ne $_.PSObject.Properties['current'] }
$unconsumed = 0
foreach ($p in $topicEtas) {
    $cur = $lastCommitted | Where-Object { [string]$_.partition -eq [string]$p.partition } | Select-Object -First 1
    if ($null -ne $cur) { $unconsumed += ([long]$p.latest - [long]$cur.current) }
}
$kafkaResult = [ordered]@{
    topic               = $script:Topic
    topic_etas          = $topicEtas
    dlq_topic           = $script:DlqTopic
    dlq_etas            = $dlqEtas
    consumer_group      = $script:ConsumerGroup
    group_state         = $groupState
    lag_total           = $lag
    unconsumed_records  = $unconsumed
    captured_at_cst     = Get-ShanghaiStamp
}
Save-Evidence -Directory $OutDir -Name 'kafka.json' -Data $kafkaResult | Out-Null
Write-Step "   lag=$lag unconsumed=$unconsumed dlq_latest=$(($dlqEtas | ForEach-Object { $_.latest }) -join ',')"

# ---- 2) MySQL ----------------------------------------------------------------
Write-Step '2) MySQL 测试活动终态'
$ids = ($ActivityIds -join ',')
$sql = @"
SELECT CONCAT_WS('|',
  a.id,
  COALESCE(s.sku_id,0),
  COALESCE(s.total_stock,0),
  COALESCE(s.sold,0),
  COALESCE(s.total_stock - s.sold,0),
  (SELECT COUNT(*) FROM flash_sale_order_requests r WHERE r.activity_id = a.id),
  (SELECT COUNT(*) FROM flash_sale_order_requests r WHERE r.activity_id = a.id AND r.status = 0),
  (SELECT COUNT(*) FROM flash_sale_order_requests r WHERE r.activity_id = a.id AND r.status = 1),
  (SELECT COUNT(*) FROM flash_sale_order_requests r WHERE r.activity_id = a.id AND r.status = 2),
  (SELECT COUNT(*) FROM flash_sale_order_requests r WHERE r.activity_id = a.id AND r.status = 3),
  (SELECT COUNT(*) FROM flash_sale_order_requests r WHERE r.activity_id = a.id AND r.published_at IS NULL),
  (SELECT COUNT(*) FROM flash_sale_orders o WHERE o.activity_id = a.id),
  (SELECT COUNT(DISTINCT o.user_id) FROM flash_sale_orders o WHERE o.activity_id = a.id),
  (SELECT COUNT(*) FROM (SELECT o.user_id FROM flash_sale_orders o WHERE o.activity_id = a.id GROUP BY o.user_id HAVING COUNT(*) > 1) t),
  (SELECT COUNT(*) FROM (SELECT o.order_no FROM flash_sale_orders o WHERE o.activity_id = a.id GROUP BY o.order_no HAVING COUNT(*) > 1) t),
  (SELECT COUNT(*) FROM flash_sale_order_requests r WHERE r.activity_id = a.id AND r.retry_count > 3),
  (SELECT UNIX_TIMESTAMP(a.end_time) FROM DUAL)
)
FROM flash_sale_activities a
LEFT JOIN flash_sale_activity_skus s ON s.activity_id = a.id
WHERE a.id IN ($ids)
ORDER BY a.id
"@
$rows = @(Invoke-SqlText -Sql $sql)
$activities = @()
foreach ($line in $rows) {
    if ([string]::IsNullOrWhiteSpace($line)) { continue }
    $c = $line -split '\|'
    $activities += [pscustomobject]@{
        activity_id              = $c[0]
        sku_id                   = $c[1]
        total_stock              = $c[2]
        sold                     = $c[3]
        remaining                = $c[4]
        requests_total           = $c[5]
        requests_queued          = $c[6]
        requests_success         = $c[7]
        requests_failed          = $c[8]
        requests_dead            = $c[9]
        requests_unpublished     = $c[10]
        orders_total             = $c[11]
        orders_distinct_users    = $c[12]
        orders_dup_per_user      = $c[13]
        orders_dup_no            = $c[14]
        requests_retry_exhausted = $c[15]
        activity_end_epoch       = $c[16]
    }
}

# 全局不变量（跨全部测试活动）
$globalSql = @'
SELECT CONCAT(
  'requests_total=', (SELECT COUNT(*) FROM flash_sale_order_requests),
  ' | queued=', (SELECT COUNT(*) FROM flash_sale_order_requests WHERE status = 0),
  ' | unpublished=', (SELECT COUNT(*) FROM flash_sale_order_requests WHERE published_at IS NULL AND status = 0),
  ' | orders_total=', (SELECT COUNT(*) FROM flash_sale_orders),
  ' | dup_order_no=', (SELECT COUNT(*) FROM (SELECT order_no FROM flash_sale_orders GROUP BY order_no HAVING COUNT(*) > 1) t),
  ' | dup_user_per_activity=', (SELECT COUNT(*) FROM (SELECT activity_id, user_id FROM flash_sale_orders GROUP BY activity_id, user_id HAVING COUNT(*) > 1) t),
  ' | sold_gt_stock=', (SELECT COUNT(*) FROM flash_sale_activity_skus WHERE sold > total_stock)
)
'@
$globalLine = @(Invoke-SqlText -Sql $globalSql) -join ''
$mysqlResult = [ordered]@{
    activities = $activities
    global     = $globalLine
}
Save-Evidence -Directory $OutDir -Name 'mysql.json' -Data $mysqlResult | Out-Null
Write-Step "   $globalLine"

# ---- 3) Redis ----------------------------------------------------------------
Write-Step '3) Redis 缓存与 MySQL 交叉核对'
$redisScan = @{}
foreach ($pattern in @('flashsale:stock:*', 'flashsale:bought:*', 'flashsale:idem:*', 'flashsale:activity:*', 'flashsale:soldout:*')) {
    $cmd = "redis-cli --scan --pattern '$pattern' | sort"
    $lines = @(Invoke-Kubectl -Arguments @('exec', '-n', $script:Namespace, $script:RedisTarget, '--', 'sh', '-c', $cmd))
    $redisScan[$pattern] = @($lines | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
}

$redisChecks = @()
foreach ($a in $activities) {
    $key = "flashsale:stock:$($a.activity_id):$($a.sku_id)"
    $raw = (@(Invoke-Kubectl -Arguments @('exec', '-n', $script:Namespace, $script:RedisTarget, '--',
                'redis-cli', 'get', $key)) -join '').Trim()
    $ttl = (@(Invoke-Kubectl -Arguments @('exec', '-n', $script:Namespace, $script:RedisTarget, '--',
                'redis-cli', 'ttl', $key)) -join '').Trim()
    # 活动结束后缓存会被对账扫描器清理，此时 Redis 无键属于预期；只有在有值时比较一致性。
    $match = if ([string]::IsNullOrWhiteSpace($raw) -or $raw -eq '(nil)') { 'no-cache-key' }
    elseif ($raw -eq $a.remaining) { 'match' } else { 'MISMATCH' }
    $redisChecks += [pscustomobject]@{
        activity_id     = $a.activity_id
        sku_id          = $a.sku_id
        key             = $key
        redis_stock     = if ([string]::IsNullOrWhiteSpace($raw)) { '(nil)' } else { $raw }
        ttl_seconds     = $ttl
        mysql_remaining = $a.remaining
        verdict         = $match
    }
}
$redisResult = [ordered]@{
    key_counts = [ordered]@{
        stock_keys    = $redisScan['flashsale:stock:*'].Count
        bought_keys   = $redisScan['flashsale:bought:*'].Count
        idem_keys     = $redisScan['flashsale:idem:*'].Count
        activity_keys = $redisScan['flashsale:activity:*'].Count
        soldout_keys  = $redisScan['flashsale:soldout:*'].Count
    }
    sample_keys = [ordered]@{
        stock    = $redisScan['flashsale:stock:*']
        activity = $redisScan['flashsale:activity:*']
    }
    per_activity = $redisChecks
}
Save-Evidence -Directory $OutDir -Name 'redis.json' -Data $redisResult | Out-Null
Write-Step "   stock_keys=$($redisResult.key_counts.stock_keys) markers(bought/idem)=$($redisResult.key_counts.bought_keys)/$($redisResult.key_counts.idem_keys)"

# ---- 4) Prometheus -----------------------------------------------------------
Write-Step "4) Prometheus 区间数据（最近 $RangeMinutes 分钟）"
$endUtc = (Get-Date).ToUniversalTime()
$startUtc = $endUtc.AddMinutes(-$RangeMinutes)
$queries = [ordered]@{
    'kafka_consumer_lag'        = "surgecart_kafka_consumer_lag{consumer_group=`"$($script:ConsumerGroup)`"}"
    'outbox_backlog_pending'    = 'flashsale_outbox_backlog_pending'
    'outbox_oldest_age_seconds' = 'surgecart_outbox_oldest_age_seconds'
    'sampler_up_outbox'         = 'surgecart_sampler_up{sampler="outbox"}'
    'sampler_up_kafka_lag'      = 'surgecart_sampler_up{sampler="kafka_lag"}'
    'consumer_joined'           = 'flashsale_consumer_joined'
    # Get-PromRange 只取第一条序列，因此多标签指标必须写成「单序列」聚合，
    # 否则会静默只报告其中一个标签（例如只报 result=success）。
    'consumer_processed_total'  = 'sum(flashsale_consumer_processed_total)'
    'consumer_processed_invalid'= 'sum(flashsale_consumer_processed_total{result="invalid"})'
    'consumer_processed_duplicate' = 'sum(flashsale_consumer_processed_total{result="duplicate"})'
    'consumer_processed_dead'   = 'sum(flashsale_consumer_processed_total{result="dead"})'
    'consumer_dlq_total'        = 'sum(flashsale_consumer_dlq_total)'
    'outbox_published'          = 'flashsale_outbox_published_total'
    'outbox_publish_failed'     = 'sum by (reason) (flashsale_outbox_publish_failed_total)'
    'http_requests'             = 'sum(rate(surgecart_http_requests_total[1m]))'
}
$promResult = [ordered]@{}
$promText = New-Object System.Collections.Generic.List[string]
$promText.Add("=== Prometheus 区间数据 $($startUtc.ToString('yyyy-MM-ddTHH:mm:ssZ')) ~ $($endUtc.ToString('yyyy-MM-ddTHH:mm:ssZ')) step=${StepSeconds}s ===")
foreach ($name in $queries.Keys) {
    try {
        $series = @(Get-PromRange -Query $queries[$name] -StartUtc $startUtc -EndUtc $endUtc -StepSeconds $StepSeconds)
        $promResult[$name] = @($series | ForEach-Object { [pscustomobject]@{ ts = $_.ts; value = $_.value } })
        $values = @($series | ForEach-Object { [double]$_.value })
        $line = if ($values.Count -eq 0) { 'no samples' } else {
            "min=$([math]::Round(($values | Measure-Object -Minimum).Minimum,3)) max=$([math]::Round(($values | Measure-Object -Maximum).Maximum,3)) last=$([math]::Round($values[$values.Count-1],3)) n=$($values.Count)"
        }
        $promText.Add("$name : $line")
        Write-Step "   $name -> $line"
    } catch {
        $promResult[$name] = "query-failed: $($_.Exception.Message)"
        $promText.Add("$name : query-failed")
    }
}
Save-Evidence -Directory $OutDir -Name 'prometheus-window.json' -Data $promResult | Out-Null
Save-EvidenceText -Directory $OutDir -Name 'prometheus-window.txt' -Lines $promText.ToArray() | Out-Null

# ---- 5) Kubernetes -----------------------------------------------------------
Write-Step '5) Kubernetes 资源状态'
$pods = @(Invoke-Kubectl -Arguments @('get', 'pods', '-n', $script:Namespace, '-o', 'wide'))
$pvc = @(Invoke-Kubectl -Arguments @('get', 'pvc', '-n', $script:Namespace))
$replicas = (@(Invoke-Kubectl -Arguments @('get', 'deploy', '-n', $script:Namespace,
            '-o', 'custom-columns=NAME:.metadata.name,DESIRED:.spec.replicas,READY:.status.readyReplicas,IMAGE:.spec.template.spec.containers[0].image', '--no-headers')) )
$k8sResult = [ordered]@{ pods = $pods; pvc = $pvc; deployments = $replicas }
Save-Evidence -Directory $OutDir -Name 'kubernetes.json' -Data $k8sResult | Out-Null

# ---- 汇总 --------------------------------------------------------------------
$summary = [ordered]@{
    captured_at_cst        = Get-ShanghaiStamp
    kafka_lag_total        = $lag
    kafka_unconsumed       = $unconsumed
    dlq_latest             = ($dlqEtas | ForEach-Object { $_.latest }) -join ','
    mysql_global           = $globalLine
    redis_stock_keys       = $redisResult.key_counts.stock_keys
    redis_marker_keys      = $redisResult.key_counts.bought_keys + $redisResult.key_counts.idem_keys
    redis_mismatch         = @($redisChecks | Where-Object { $_.verdict -eq 'MISMATCH' }).Count
    activities             = $activities
}
Save-Evidence -Directory $OutDir -Name 'final-summary.json' -Data $summary | Out-Null

$txt = New-Object System.Collections.Generic.List[string]
$txt.Add('=== T5-04 最终一致性核验 ===')
$txt.Add("时间(CST) = $($summary.captured_at_cst)")
$txt.Add('--- Kafka ---')
$txt.Add("lag_total=$lag unconsumed_records=$unconsumed")
foreach ($p in $topicEtas) { $txt.Add("  topic partition=$($p.partition) earliest=$($p.earliest) latest=$($p.latest)") }
foreach ($p in $dlqEtas) { $txt.Add("  dlq   partition=$($p.partition) earliest=$($p.earliest) latest=$($p.latest)") }
$txt.Add('--- MySQL 测试活动终态 ---')
$txt.Add('activity|sku|total|sold|remaining|req_total|queued|success|failed|dead|unpublished|orders|distinct_users|dup_user|dup_no|retry_exhausted')
foreach ($a in $activities) {
    $txt.Add(($a.PSObject.Properties | ForEach-Object { $_.Value }) -join '|')
}
$txt.Add("global: $globalLine")
$txt.Add('--- Redis ---')
foreach ($k in $redisResult.key_counts.Keys) { $txt.Add("  $k = $($redisResult.key_counts[$k])") }
foreach ($r in $redisChecks) { $txt.Add("  activity=$($r.activity_id) sku=$($r.sku_id) redis=$($r.redis_stock) ttl=$($r.ttl_seconds) mysql_remaining=$($r.mysql_remaining) -> $($r.verdict)") }
$txt.Add('--- Prometheus ---')
foreach ($l in $promText) { $txt.Add("  $l") }
$txt.Add('--- Kubernetes ---')
foreach ($l in $pods) { $txt.Add("  $l") }
foreach ($l in $pvc) { $txt.Add("  $l") }
foreach ($l in $replicas) { $txt.Add("  $l") }
Save-EvidenceText -Directory $OutDir -Name 'final-summary.txt' -Lines $txt.ToArray() | Out-Null

Write-Step "最终一致性核验完成：$OutDir"
$txt | ForEach-Object { Write-Host $_ }
