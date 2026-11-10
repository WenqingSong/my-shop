# T5-04：Prometheus 区间证据导出（故障前 / 期间 / 恢复后的指标变化）。
#
# 为什么必须在实验结束后单独做一次区间查询：
#   应用侧 lag / backlog 是「采样器」写的 Gauge，最后一次写入就是当前值；
#   只有 Prometheus 的时间序列能还原「变化过程」。报告里的曲线数据全部来自这里。
#
# 用法：
#   ./prom-window.ps1 -Prefix a -StartUtc '2026-11-10T09:33:00Z' -EndUtc '2026-11-10T09:37:30Z'
#   产出：<OutDir>/<Prefix>.prom-window.json + <Prefix>.prom-window.txt

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Prefix,
    [Parameter(Mandatory = $true)][string]$StartUtc,
    [Parameter(Mandatory = $true)][string]$EndUtc,
    [string]$OutDir = '',
    [int]$StepSeconds = 10
)

. (Join-Path $PSScriptRoot 'lib.ps1')

if ([string]::IsNullOrWhiteSpace($OutDir)) {
    $OutDir = Join-Path $script:RepoRoot 'docs/reports/t5-04-kafka-backlog-failure-recovery/raw'
}
if (-not (Test-Path $OutDir)) { New-Item -ItemType Directory -Path $OutDir -Force | Out-Null }

$start = [datetime]::Parse($StartUtc, [System.Globalization.CultureInfo]::InvariantCulture,
    [System.Globalization.DateTimeStyles]::AdjustToUniversal)
$end = [datetime]::Parse($EndUtc, [System.Globalization.CultureInfo]::InvariantCulture,
    [System.Globalization.DateTimeStyles]::AdjustToUniversal)
if ($end -le $start) { throw "EndUtc 必须晚于 StartUtc（$StartUtc -> $EndUtc）" }

# 指标清单直接对应任务书「5.4 Prometheus」要求，键名即报告里的行标题。
# 计数类用 increase(...) 表达「这段时间新增了多少」，避免把累计值误读成区间值。
$queries = [ordered]@{
    'kafka_consumer_lag'         = "surgecart_kafka_consumer_lag{consumer_group=`"$($script:ConsumerGroup)`"}"
    'outbox_backlog_pending'     = 'flashsale_outbox_backlog_pending'
    'outbox_oldest_age_seconds'  = 'surgecart_outbox_oldest_age_seconds'
    'sampler_up_outbox'          = 'surgecart_sampler_up{sampler="outbox"}'
    'sampler_up_kafka_lag'       = 'surgecart_sampler_up{sampler="kafka_lag"}'
    'consumer_joined'            = 'flashsale_consumer_joined'
    'http_rps'                   = 'sum(rate(surgecart_http_requests_total[1m]))'
    'http_5xx_rate'              = 'sum(rate(surgecart_http_requests_total{status=~"5.."}[1m]))'
    'http_target_route_rps'      = 'sum(rate(surgecart_http_requests_total{route=~".*flash.*"}[1m]))'
    'consumer_records_total'     = 'flashsale_consumer_records_total'
    'consumer_processed_success' = 'sum(flashsale_consumer_processed_total{result="success"})'
    'consumer_processed_duplicate' = 'sum(flashsale_consumer_processed_total{result="duplicate"})'
    'consumer_processed_invalid' = 'sum(flashsale_consumer_processed_total{result="invalid"})'
    'consumer_processed_error'   = 'sum(flashsale_consumer_processed_total{result="error"})'
    'consumer_processed_dead'    = 'sum(flashsale_consumer_processed_total{result="dead"})'
    'consumer_dlq_total'         = 'sum(flashsale_consumer_dlq_total)'
    'consumer_retries_total'     = 'flashsale_consumer_retries_total'
    'consumer_restarts_total'    = 'flashsale_consumer_restarts_total'
    'consumer_commit_failures'   = 'flashsale_consumer_commit_failures_total'
    'outbox_published_total'     = 'flashsale_outbox_published_total'
    'outbox_publish_failed_total'= 'sum(flashsale_outbox_publish_failed_total)'
    'outbox_retried_total'       = 'flashsale_outbox_retried_total'
    'outbox_claimed_total'       = 'flashsale_outbox_claimed_total'
    'outbox_lease_reclaimed'     = 'flashsale_outbox_lease_reclaimed_total'
}

$series = [ordered]@{}
$txt = New-Object System.Collections.Generic.List[string]
$txt.Add("=== T5-04 Prometheus 区间证据: $Prefix ===")
$txt.Add("window: $StartUtc .. $EndUtc (step=${StepSeconds}s)")
$txt.Add('')
foreach ($name in $queries.Keys) {
    $points = @(Get-PromRange -Query $queries[$name] -StartUtc $start -EndUtc $end -StepSeconds $StepSeconds)
    $series[$name] = [ordered]@{ query = $queries[$name]; points = $points }
    $txt.Add("--- $name ---")
    $txt.Add("  query: $($queries[$name])")
    if ($points.Count -eq 0) {
        $txt.Add('  (no samples)')
    } else {
        $txt.Add('  ' + (($points | ForEach-Object {
                    # 注意括号：`[datetime]'1970-01-01'.AddSeconds()` 会被解析成
                    # 先对字符串调用 AddSeconds（方法不存在）——必须先把字符串转成 datetime。
                    $t = ([datetime]'1970-01-01').AddSeconds([double]$_.ts).ToString('HH:mm:ss')
                    "$t=$($_.value)"
                }) -join ' '))
    }
}

Save-Evidence -Directory $OutDir -Name "$Prefix.prom-window.json" -Data $series | Out-Null
$path = Save-EvidenceText -Directory $OutDir -Name "$Prefix.prom-window.txt" -Lines $txt.ToArray()
Write-Step "Prometheus 区间证据已写入 $path"
