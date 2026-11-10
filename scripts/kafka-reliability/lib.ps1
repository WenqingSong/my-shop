# T5-04 Kafka 可靠性实验：公共函数库（只读/可回滚操作为主）。
#
# 为什么单独抽一个库：
#   1. 实验 A~D 需要的「取 Kafka 位移」「取消费组 Lag」「查 MySQL 计数」「查 Prometheus」
#      口径必须完全一致，否则不同实验之间的数字无法横向对比；
#   2. Windows 上没有 mysql-cli，SQL 只能通过 kubectl exec 进 MySQL Pod 执行；
#      而 PowerShell 5.1 直接给 kubectl exec 传含引号的参数会被 Windows 层转义破坏
#      （实测：SQL 里的双引号被吞掉，MySQL 报 1064），因此统一改为「SQL 走 stdin」；
#   3. 任何实验脚本都不得打印 Secret 明文：凭据只在 Pod 内部由环境变量展开，
#      宿主机侧从不读取、不落盘。
#
# 约束（对应任务书「六、实验数据与安全要求」）：
#   - 不删除 PVC、不重建集群、不清理 Kafka 历史数据；
#   - 不修改 T3-02 历史商品/订单/库存；
#   - 所有故障注入必须可回滚，且脚本退出前尽力恢复环境。

$ErrorActionPreference = 'Stop'

# ---- 环境常量（与 manifest/kubernetes/overlays/local 的事实一致）---------------
$script:RepoRoot         = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
$script:Namespace        = 'surgecart'
$script:MonitoringNS     = 'monitoring'
$script:KafkaTarget      = 'deploy/kafka'
$script:MySQLTarget      = 'deploy/mysql'
$script:RedisTarget      = 'deploy/redis'
$script:AppTarget        = 'deploy/surgecart'
$script:Database         = 'my_shop'

$script:Topic            = 'surgecart.flashsale.order.requested.v1'
$script:DlqTopic         = 'surgecart.flashsale.order.requested.v1.dlq'
$script:ConsumerGroup    = 'surgecart-flashsale-order-worker-v1'
# 消费端 client.id：源码 internal/mq/kafka/consumer.go 的 consumerClientID。
# 它和生产者（surgecart-flashsale-producer）是两个独立的 franz-go 客户端，
# 这是实验 A 能做到「只暂停消费者、不影响发布者」的唯一依据。
$script:ConsumerClientID = 'surgecart-flashsale-consumer'
$script:ProducerClientID = 'surgecart-flashsale-producer'

$script:BootstrapServer  = 'localhost:9092'      # Kafka Pod 内回环
$script:KafkaBin         = '/opt/kafka/bin'
$script:KafkaConfigCmd   = "$($script:KafkaBin)/kafka-configs.sh"
$script:KafkaOffsetsCmd  = "$($script:KafkaBin)/kafka-get-offsets.sh"
$script:KafkaGroupsCmd   = "$($script:KafkaBin)/kafka-consumer-groups.sh"
$script:KafkaTopicsCmd   = "$($script:KafkaBin)/kafka-topics.sh"
$script:KafkaProducerCmd = "$($script:KafkaBin)/kafka-console-producer.sh"

# 宿主机端口转发（先执行 kubectl port-forward 才能用）：
#   kubectl -n surgecart  port-forward svc/surgecart   18000:8000
#   kubectl -n monitoring port-forward svc/prometheus  19090:9090
$script:AppBase          = 'http://127.0.0.1:18000'
$script:PrometheusBase   = 'http://127.0.0.1:19090'

# ---- 时间与输出 ----------------------------------------------------------------
function Get-UtcStamp { (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ') }
function Get-ShanghaiStamp {
    # 实验数据全部按北京时间描述，避免 T3-02/T5-03 已经踩过的 UTC/+08 混淆。
    $utc = (Get-Date).ToUniversalTime()
    $utc.AddHours(8).ToString('yyyy-MM-dd HH:mm:ss')
}

function Write-Step([string]$Message) {
    Write-Host ("[{0}] {1}" -f (Get-UtcStamp), $Message)
}

# ---- kubectl ---------------------------------------------------------------
function Invoke-Kubectl {
    <#
      .SYNOPSIS
        执行 kubectl 并返回字符串行；失败抛异常（带命令与退出码）。
      .DESCRIPTION
        PowerShell 5.1 下原生命令的 stderr 会被当成错误记录，这里统一转成字符串。
    #>
    param(
        [Parameter(Mandatory)][string[]]$Arguments,
        [switch]$AllowFailure
    )
    $prevEap = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    $raw = & kubectl @Arguments 2>&1
    $exit = $LASTEXITCODE
    $ErrorActionPreference = $prevEap
    $lines = @($raw | ForEach-Object { $_.ToString() })
    if ($exit -ne 0 -and -not $AllowFailure) {
        throw "kubectl $($Arguments -join ' ') 失败（exit=$exit）：$($lines -join ' / ')"
    }
    return $lines
}

# ---- MySQL -----------------------------------------------------------------
function Invoke-SqlText {
    <#
      .SYNOPSIS
        在 MySQL Pod 内执行 SQL（SQL 走 stdin，避免 Windows 参数转义破坏引号）。
      .OUTPUTS
        字符串行数组（-N -B：无表头、制表符分隔）。
      .NOTES
        密码由 Pod 内的 $MYSQL_ROOT_PASSWORD 展开，宿主机侧从不接触明文。
    #>
    param(
        [Parameter(Mandatory)][string]$Sql,
        [string]$Database = $script:Database
    )
    # 与 Kafka 消息同源问题：PowerShell 5.1 的文本管道会把 UTF-8 BOM 写进 native stdin，
    # 统一走「无 BOM 临时文件 + Start-Process 重定向」，避免 BOM 被 MySQL 当成非法首字符。
    $args = @('exec', '-i', '-n', $script:Namespace, $script:MySQLTarget, '--',
        'sh', '-c', "MYSQL_PWD=`$MYSQL_ROOT_PASSWORD mysql -uroot -N -B $Database")
    return Invoke-NativeStdinFile -Lines @($Sql) -ArgumentList $args
}

function Invoke-SqlKv {
    <#
      .SYNOPSIS
        执行「label<TAB>value」形态的 SQL，返回有序 hashtable（便于写快照 JSON）。
    #>
    param([Parameter(Mandatory)][string]$Sql)
    $result = [ordered]@{}
    foreach ($line in (Invoke-SqlText -Sql $Sql)) {
        if ([string]::IsNullOrWhiteSpace($line)) { continue }
        $parts = $line -split "`t"
        if ($parts.Count -lt 2) { continue }
        $result[$parts[0]] = $parts[1]
    }
    return $result
}

# ---- Kafka -----------------------------------------------------------------
function Test-KafkaPodRunning {
    <#
      .SYNOPSIS
        判断 Kafka Pod 是否处于 Running。Broker 停机期间所有 Kafka CLI 都不该再被调用：
        kubectl exec 到一个不存在的 Pod 会长时间挂起（实测快照因此卡了 4 分钟），
        必须先快速判否，把「Broker 不可用」当成证据而非让脚本卡死。
    #>
    $pods = @(Invoke-Kubectl -Arguments @('get', 'pods', '-n', $script:Namespace,
            '-l', 'app.kubernetes.io/name=kafka', '--field-selector=status.phase=Running',
            '-o', 'name') -AllowFailure)
    return (($pods -join '').Trim() -ne '')
}

function Invoke-KafkaCli {
    param([Parameter(Mandatory)][string]$Command)
    if (-not (Test-KafkaPodRunning)) {
        throw "Kafka Pod 未运行（Broker 故障中），跳过 CLI：$Command"
    }
    $args = @('exec', '-n', $script:Namespace, $script:KafkaTarget, '--', 'sh', '-c', $Command)
    return Invoke-Kubectl -Arguments $args
}

function Invoke-KafkaStdin {
    <#
      .SYNOPSIS
        通过 stdin 向 Kafka Pod 内命令输入数据（用于 console-producer）。
      .DESCRIPTION
        与 Invoke-SqlText 同理：Windows 参数转义会破坏含引号的 JSON，因此「消息体」走 stdin。

        BOM 问题（本任务实测踩坑，证据见 raw/c 实验首轮）：
          `$payload | & kubectl exec -i ...` 在 Windows PowerShell 5.1 下实际写入 stdin 的
          字节是 `ef bb bf 7b 22 ... 0d 0a` —— 带 UTF-8 BOM，且换行被转成 CRLF。
          设置 $OutputEncoding（无论 UTF8Encoding($false) 还是 ASCII）都不起作用，
          因为该管道不是经由 $OutputEncoding 编码的。
          后果：消息体首字符变成 U+FEFF，消费端 json.Unmarshal 报
          `invalid character '\ufeff' looking for beginning of value`，
          整条消息被判 parse_error 进入 DLQ —— 实验结论会因此完全失真。
        解决：把消息体按「UTF-8 无 BOM」写入临时文件，用 Start-Process 的
        -RedirectStandardInput 直接接管道，绕开 PowerShell 的文本管道编码路径。
    #>
    param(
        [Parameter(Mandatory)][string]$Command,
        [Parameter(Mandatory)][string[]]$Lines
    )
    $args = @('exec', '-i', '-n', $script:Namespace, $script:KafkaTarget, '--', 'sh', '-c', $Command)
    return Invoke-NativeStdinFile -Lines $Lines -ArgumentList $args
}

function Invoke-NativeStdinFile {
    <#
      .SYNOPSIS
        以「UTF-8 无 BOM 临时文件 + Start-Process 重定向」的方式向原生命令喂 stdin。
      .OUTPUTS
        stdout 行数组（stderr 合并进返回，非 0 退出码抛异常）。
    #>
    param(
        [Parameter(Mandatory)][string[]]$Lines,
        [Parameter(Mandatory)][string[]]$ArgumentList
    )
    $tmpIn = [System.IO.Path]::GetTempFileName()
    $tmpOut = [System.IO.Path]::GetTempFileName()
    $tmpErr = [System.IO.Path]::GetTempFileName()
    try {
        $text = (($Lines -join "`n") + "`n")
        [System.IO.File]::WriteAllText($tmpIn, $text, (New-Object System.Text.UTF8Encoding($false)))
        # Start-Process 的 -ArgumentList 是「原样拼成一条命令行」，不会为含空格的元素自动加引号：
        # 传数组会让 `sh -c <含空格的脚本>` 被拆开（实测报 Missing required option(s) [bootstrap-server]）。
        # 因此这里手动为含空格/引号的参数加双引号。
        $argLine = ($ArgumentList | ForEach-Object {
                if ($_ -match '[\s"]') { '"' + ($_ -replace '"', '\"') + '"' } else { $_ }
            }) -join ' '
        $proc = Start-Process -FilePath 'kubectl' -ArgumentList $argLine `
            -RedirectStandardInput $tmpIn -RedirectStandardOutput $tmpOut -RedirectStandardError $tmpErr `
            -NoNewWindow -Wait -PassThru
        # 必须用 .NET 直接读文件，而不是 Get-Content：
        # Get-Content 返回的字符串带 ETS 附加属性（PSPath / PSProvider / ReadCount），
        # 其中 PSProvider 是对 FileSystem provider 的对象引用；后续
        # `... | ConvertTo-Json` 会顺着这个引用序列化整个 provider/驱动器对象图，
        # 实测直接抛 System.OutOfMemoryException（把实验收尾卡死在无输出的忙循环里），
        # 而且汇总文本里会出现 `@{value=...; PSPath=...; PSProvider=...}` 这种脏输出。
        # ReadAllLines 返回纯 .NET string[]，没有 ETS 装饰。
        $stdout = @([System.IO.File]::ReadAllLines($tmpOut, [System.Text.Encoding]::UTF8))
        $stderr = @([System.IO.File]::ReadAllLines($tmpErr, [System.Text.Encoding]::UTF8))
        if ($proc.ExitCode -ne 0) {
            throw "stdin 命令失败（exit=$($proc.ExitCode)）：$(($stdout + $stderr) -join ' / ')"
        }
        return @($stdout + $stderr)
    } finally {
        Remove-Item -Path $tmpIn, $tmpOut, $tmpErr -Force -ErrorAction SilentlyContinue
    }
}

function Get-TopicMessage {
    <#
      .SYNOPSIS
        按 (partition, offset) 精确读取 Topic 中的一条消息体。
      .DESCRIPTION
        console-consumer 会把 "Processed a total of N messages" 打到 stderr，
        本函数只返回以 '{' 开头的 JSON 行，避免把提示文本误当消息。
    #>
    param(
        [Parameter(Mandatory)][string]$Topic,
        [int]$Partition = 0,
        [Parameter(Mandatory)][long]$Offset
    )
    $cmd = "$($script:KafkaBin)/kafka-console-consumer.sh --bootstrap-server $($script:BootstrapServer) " +
    "--topic $Topic --partition $Partition --offset $Offset --max-messages 1 --timeout-ms 8000"
    $rows = Invoke-KafkaCli -Command $cmd
    $json = $rows | Where-Object { $_ -match '^\s*\{' } | Select-Object -First 1
    if (-not $json) { throw "未读到 $Topic/$Partition@$Offset 的 JSON 消息" }
    return $json.Trim()
}

function Produce-TopicMessage {
    <#
      .SYNOPSIS
        向 Topic 发布一条 JSON 消息（单行，无 BOM）。
      .DESCRIPTION
        console-producer 把 stdin 的每一行当作一条消息；调用方必须保证 $Payload 是单行。
    #>
    param(
        [Parameter(Mandatory)][string]$Topic,
        [Parameter(Mandatory)][string]$Payload
    )
    $cmd = "$($script:KafkaBin)/kafka-console-producer.sh --bootstrap-server $($script:BootstrapServer) --topic $Topic"
    return Invoke-KafkaStdin -Command $cmd -Lines @($Payload)
}

function Get-TopicEtas {
    <#
      .SYNOPSIS
        取某 Topic 每分区的 earliest(-2) 与 latest(-1) 位移。
      .OUTPUTS
        PSCustomArray：{ partition; earliest; latest }
    #>
    param([Parameter(Mandatory)][string]$Topic)
    $earliest = Get-TopicOffset -Topic $Topic -Time -2
    $latest   = Get-TopicOffset -Topic $Topic -Time -1
    $out = @()
    foreach ($kv in $latest.GetEnumerator()) {
        $e = if ($earliest.ContainsKey($kv.Key)) { $earliest[$kv.Key] } else { 'NA' }
        $out += [pscustomobject]@{ partition = $kv.Key; earliest = $e; latest = $kv.Value }
    }
    return ($out | Sort-Object partition)
}

function Get-TopicOffset {
    <#
      .SYNOPSIS
        取 Topic 各分区在指定时间点的位移（-2=earliest, -1=latest）。
      .OUTPUTS
        hashtable：partition(字符串) -> offset(字符串)
    #>
    param(
        [Parameter(Mandatory)][string]$Topic,
        [Parameter(Mandatory)][int]$Time
    )
    $cmd = "$($script:KafkaOffsetsCmd) --bootstrap-server $($script:BootstrapServer) --topic $Topic --time $Time"
    $rows = Invoke-KafkaCli -Command $cmd
    $map = @{}
    foreach ($row in $rows) {
        # 形如 surgecart.flashsale.order.requested.v1:0:34
        if ($row -match '^(?<t>.+):(?<p>\d+):(?<o>-?\d+)$') {
            $map[$Matches['p']] = $Matches['o']
        }
    }
    return $map
}

function Get-ConsumerGroupState {
    <#
      .SYNOPSIS
        解析 kafka-consumer-groups --describe 的输出为对象数组。
      .OUTPUTS
        { group; topic; partition; current; logEnd; lag; consumerId; host; clientId }
    #>
    param([string]$Group = $script:ConsumerGroup)
    $cmd = "$($script:KafkaGroupsCmd) --bootstrap-server $($script:BootstrapServer) --describe --group $Group"
    $rows = Invoke-KafkaCli -Command $cmd
    $out = @()
    foreach ($row in $rows) {
        if ($row -match '^GROUP\s+TOPIC' -or [string]::IsNullOrWhiteSpace($row)) { continue }
        if ($row -match '^Consumer group .+ has no active members' -or $row -match '^Error:') {
            $out += [pscustomobject]@{ raw = $row }
            continue
        }
        $f = $row -split '\s+'
        if ($f.Count -ge 6 -and $f[2] -match '^\d+$') {
            $out += [pscustomobject]@{
                group      = $f[0]
                topic      = $f[1]
                partition  = [int]$f[2]
                current    = $f[3]
                logEnd     = $f[4]
                lag        = $f[5]
                consumerId = if ($f.Count -gt 6) { $f[6] } else { '' }
                host       = if ($f.Count -gt 7) { $f[7] } else { '' }
                clientId   = if ($f.Count -gt 8) { $f[8] } else { '' }
            }
        }
    }
    return $out
}

function Get-ConsumerGroupLagTotal {
    param([string]$Group = $script:ConsumerGroup)
    $state = Get-ConsumerGroupState -Group $Group
    $sum = 0
    foreach ($row in $state) {
        if ($null -ne $row.PSObject.Properties['lag'] -and $row.lag -match '^-?\d+$') { $sum += [int]$row.lag }
    }
    return $sum
}

function Get-TopicMessageCount {
    param([Parameter(Mandatory)][string]$Topic)
    $etas = Get-TopicEtas -Topic $Topic
    $sum = 0
    foreach ($p in $etas) {
        if ($p.earliest -match '^\d+$' -and $p.latest -match '^\d+$') {
            $sum += ([int]$p.latest - [int]$p.earliest)
        }
    }
    return $sum
}

# ---- 实验 A 专用：消费端客户端配额（唯一能「只暂停消费者」的杠杆）-------------
function Set-ClientQuota {
    <#
      .SYNOPSIS
        给指定 client.id 设置 / 删除 consumer_byte_rate 配额。
      .DESCRIPTION
        HTTP 服务与 Consumer 在同一个进程、同一个 Pod、同一组网络连接目标上运行，
        因此无法用网络策略、暂停容器等方式「只停消费者」。Kafka 的客户端配额按
        client.id 生效，而本项目生产者与消费者使用不同的 client.id
        （surgecart-flashsale-producer / surgecart-flashsale-consumer），
        所以可以只把消费端的字节速率压到接近 0，让分区消息无法被拉取。
        该操作是 Broker 侧动态配置，可 delete-config 完全回滚，不重启 Broker、不动 Topic。
    #>
    param(
        [Parameter(Mandatory)][string]$ClientId,
        [int]$ConsumerByteRate = 0,   # 0 表示删除配额
        [switch]$Remove
    )
    if ($Remove -or $ConsumerByteRate -le 0) {
        $cmd = "$($script:KafkaConfigCmd) --bootstrap-server $($script:BootstrapServer) --alter " +
        "--entity-type clients --entity-name $ClientId --delete-config consumer_byte_rate"
    } else {
        $cmd = "$($script:KafkaConfigCmd) --bootstrap-server $($script:BootstrapServer) --alter " +
        "--entity-type clients --entity-name $ClientId --add-config consumer_byte_rate=$ConsumerByteRate"
    }
    return (Invoke-KafkaCli -Command $cmd)
}

function Get-ClientQuota {
    param([Parameter(Mandatory)][string]$ClientId)
    $cmd = "$($script:KafkaConfigCmd) --bootstrap-server $($script:BootstrapServer) --describe " +
    "--entity-type clients --entity-name $ClientId"
    return (Invoke-KafkaCli -Command $cmd)
}

# ---- Prometheus --------------------------------------------------------------
function Get-PromValue {
    <#
      .SYNOPSIS
        执行即时查询，返回第一个样本的值（无样本返回 $null）。
    #>
    param([Parameter(Mandatory)][string]$Query)
    $enc = [uri]::EscapeDataString($Query)
    $resp = Invoke-RestMethod -Uri "$($script:PrometheusBase)/api/v1/query?query=$enc" -Method Get
    if ($resp.status -ne 'success') { throw "Prometheus 查询失败：$Query" }
    if (@($resp.data.result).Count -eq 0) { return $null }
    return $resp.data.result[0].value[1]
}

function Get-PromSeries {
    <#
      .SYNOPSIS
        执行即时查询，返回全部样本（labels + value），用于按 label 展开的指标。
    #>
    param([Parameter(Mandatory)][string]$Query)
    $enc = [uri]::EscapeDataString($Query)
    $resp = Invoke-RestMethod -Uri "$($script:PrometheusBase)/api/v1/query?query=$enc" -Method Get
    if ($resp.status -ne 'success') { throw "Prometheus 查询失败：$Query" }
    return @($resp.data.result | ForEach-Object {
        [pscustomobject]@{ metric = $_.metric; value = $_.value[1] }
    })
}

function Get-PromRange {
    <#
      .SYNOPSIS
        执行区间查询（Prometheus /query_range），返回 [(timestamp, value)]。
    #>
    param(
        [Parameter(Mandatory)][string]$Query,
        [Parameter(Mandatory)][datetime]$StartUtc,
        [Parameter(Mandatory)][datetime]$EndUtc,
        [int]$StepSeconds = 15
    )
    $enc = [uri]::EscapeDataString($Query)
    $start = ([int][double]::Parse(($StartUtc - [datetime]'1970-01-01').TotalSeconds.ToString('F0')))
    $end = ([int][double]::Parse(($EndUtc - [datetime]'1970-01-01').TotalSeconds.ToString('F0')))
    $uri = "$($script:PrometheusBase)/api/v1/query_range?query=$enc&start=$start&end=$end&step=$StepSeconds"
    $resp = Invoke-RestMethod -Uri $uri -Method Get
    if ($resp.status -ne 'success') { throw "Prometheus 区间查询失败：$Query" }
    if (@($resp.data.result).Count -eq 0) { return @() }
    return @($resp.data.result[0].values | ForEach-Object {
        [pscustomobject]@{ ts = [int]$_[0]; value = $_[1] }
    })
}

# ---- 证据落盘 ----------------------------------------------------------------
function Save-Evidence {
    <#
      .SYNOPSIS
        把任意对象以 JSON（UTF-8 无 BOM）写入证据目录。
    #>
    param(
        [Parameter(Mandatory)][string]$Directory,
        [Parameter(Mandatory)][string]$Name,
        [Parameter(Mandatory)]$Data
    )
    if (-not (Test-Path $Directory)) { New-Item -ItemType Directory -Path $Directory -Force | Out-Null }
    $path = Join-Path $Directory $Name
    $Data | ConvertTo-Json -Depth 8 | Set-Content -Path $path -Encoding utf8
    return $path
}

function Save-EvidenceText {
    param(
        [Parameter(Mandatory)][string]$Directory,
        [Parameter(Mandatory)][string]$Name,
        # AllowEmptyString：快照文本里刻意保留空行分段，PowerShell 5.1 的
        # 必填 string[] 参数默认拒绝空字符串，会直接把脚本打断。
        [Parameter(Mandatory)][AllowEmptyString()][string[]]$Lines
    )
    if (-not (Test-Path $Directory)) { New-Item -ItemType Directory -Path $Directory -Force | Out-Null }
    $path = Join-Path $Directory $Name
    $Lines | Set-Content -Path $path -Encoding utf8
    return $path
}

# ---- 业务入口（真实 HTTP）----------------------------------------------------
function Invoke-AppRequest {
    param(
        [Parameter(Mandatory)][string]$Path,
        [string]$Method = 'GET',
        $Body = $null,
        [hashtable]$Headers = @{}
    )
    $uri = "$($script:AppBase)$Path"
    $params = @{ Uri = $uri; Method = $Method; UseBasicParsing = $true; TimeoutSec = 15 }
    if ($Headers.Count -gt 0) { $params['Headers'] = $Headers }
    if ($null -ne $Body) {
        $params['Body'] = ($Body | ConvertTo-Json -Depth 6 -Compress)
        $params['ContentType'] = 'application/json'
    }
    return Invoke-RestMethod @params
}

function Assert-Preconditions {
    <#
      .SYNOPSIS
        实验前的环境自检：集群、Pod、端口转发、Prometheus 抓取目标。
      .DESCRIPTION
        任何一项不满足就直接失败，避免在坏环境上跑出「看起来是故障」的假象。
    #>
    Write-Step '环境自检：kubectl / Pod / 端口转发 / Prometheus'
    $ctx = (Invoke-Kubectl -Arguments @('config', 'current-context')) -join ''
    Write-Step "   kube context = $ctx"

    $pods = Invoke-Kubectl -Arguments @('get', 'pods', '-n', $script:Namespace, '-o',
        'jsonpath={range .items[*]}{.metadata.name}={.status.phase}/{.status.containerStatuses[0].ready} {end}')
    Write-Step "   pods: $($pods -join '')"

    foreach ($ep in @(
            @{ name = 'app'; url = "$($script:AppBase)/ready" },
            @{ name = 'prometheus'; url = "$($script:PrometheusBase)/api/v1/targets?state=active" })) {
        try {
            Invoke-RestMethod -Uri $ep.url -Method Get -TimeoutSec 10 | Out-Null
            Write-Step "   $($ep.name) 可达: $($ep.url)"
        } catch {
            throw "端口转发未就绪（$($ep.name) -> $($ep.url)）：$($_.Exception.Message)"
        }
    }

    $targets = Invoke-RestMethod -Uri "$($script:PrometheusBase)/api/v1/targets?state=active" -Method Get
    $down = @($targets.data.activeTargets | Where-Object { $_.health -ne 'up' })
    if ($down.Count -gt 0) {
        throw "Prometheus 存在非 up 的抓取目标：$($down.labels.job -join ',')"
    }
    Write-Step "   Prometheus 抓取目标全部 up（$(@($targets.data.activeTargets).Count) 个）"
}

# ---- 指标时间线（Lag / 积压的「变化过程」证据）--------------------------------
function Get-AppMetricLines {
    param([string]$Pattern = '^(flashsale|surgecart)_')
    $resp = Invoke-WebRequest -Uri "$($script:AppBase)/metrics" -UseBasicParsing -TimeoutSec 10
    return @($resp.Content -split "`n" | Where-Object { $_ -match $Pattern })
}

function Get-LagSample {
    <#
      .SYNOPSIS
        取一个时间点的「三方口径」Lag 样本。
      .DESCRIPTION
        为什么要三种口径：
          - Kafka 官方 offset（kafka-consumer-groups）：Broker 侧事实，最权威；
          - Prometheus surgecart_kafka_consumer_lag：应用异步采样（10s 一次），
            用于证明「指标能观测到积压」，与 Broker 值可能有采样延迟；
          - 应用 /metrics 即时值：抓取时刻的进程内值。
        三者对齐才能说明「积压确实产生，且可被监控观测到」。
    #>
    $group = @()
    $row = $null
    $etas = $null
    $kafkaError = ''
    try {
        $group = @(Get-ConsumerGroupState)
        $row = $group | Where-Object { $null -ne $_.PSObject.Properties['lag'] } | Select-Object -First 1
        $etas = @(Get-TopicEtas -Topic $script:Topic) | Select-Object -First 1
    } catch {
        # Broker 不可用（实验 B）：Lag 无从查询，必须显式标记，不能当成 0。
        $kafkaError = $_.Exception.Message
    }
    $promLag = $null; $promBacklog = $null; $promJoined = $null
    try { $promLag = Get-PromValue -Query "surgecart_kafka_consumer_lag{consumer_group=`"$($script:ConsumerGroup)`"}" } catch { }
    try { $promBacklog = Get-PromValue -Query 'flashsale_outbox_backlog_pending' } catch { }
    try { $promJoined = Get-PromValue -Query 'flashsale_consumer_joined' } catch { }
    return [pscustomobject]@{
        at_utc               = Get-UtcStamp
        at_cst               = Get-ShanghaiStamp
        committed_offset     = if ($row) { $row.current } else { '' }
        log_end_offset       = if ($row) { $row.logEnd } else { '' }
        broker_lag           = if ($row) { $row.lag } elseif ($kafkaError) { 'NA' } else { '' }
        topic_earliest       = if ($etas) { $etas.earliest } else { '' }
        topic_latest         = if ($etas) { $etas.latest } else { '' }
        prom_lag             = $promLag
        prom_backlog_pending = $promBacklog
        prom_consumer_joined = $promJoined
        consumer_id          = if ($row) { $row.consumerId } else { '' }
        kafka_error          = $kafkaError
    }
}

function Measure-LagTimeline {
    <#
      .SYNOPSIS
        周期性采样 Lag，直到时长用完或（可选）Lag 归零。
      .OUTPUTS
        样本数组，同时落盘 <OutDir>/<Label>.lag-timeline.json
    #>
    param(
        [Parameter(Mandatory)][string]$Label,
        [int]$DurationSeconds = 30,
        [int]$IntervalSeconds = 5,
        [switch]$StopWhenZero,
        [string]$OutDir
    )
    $samples = @()
    $deadline = (Get-Date).AddSeconds($DurationSeconds)
    while ((Get-Date) -lt $deadline) {
        $s = Get-LagSample
        $samples += $s
        Write-Step ("   lag  committed={0} logEnd={1} broker={2} prom={3} backlog={4}" -f `
                $s.committed_offset, $s.log_end_offset, $s.broker_lag, $s.prom_lag, $s.prom_backlog_pending)
        if ($StopWhenZero -and $s.broker_lag -eq '0') { break }
        Start-Sleep -Seconds $IntervalSeconds
    }
    if ($OutDir) {
        Save-Evidence -Directory $OutDir -Name "$Label.lag-timeline.json" -Data $samples | Out-Null
        Save-EvidenceText -Directory $OutDir -Name "$Label.lag-timeline.txt" -Lines @(
            ($samples | ForEach-Object {
                    "{0}`tcommitted={1}`tlogEnd={2}`tbroker_lag={3}`tprom_lag={4}`tbacklog={5}" -f `
                        $_.at_utc, $_.committed_offset, $_.log_end_offset, $_.broker_lag, $_.prom_lag, $_.prom_backlog_pending
                })
        ) | Out-Null
    }
    return $samples
}

function Wait-KafkaReady {
    <#
      .SYNOPSIS
        等待 Kafka 可响应元数据请求（用 kafka-get-offsets 作为「Broker 真的能服务」的证据）。
      .DESCRIPTION
        只看 Pod Running 是不够的：KRaft 单节点重启后要先完成日志恢复才能响应请求。
        本函数以「能查到目标 Topic 的位移」为就绪判据。
    #>
    param([int]$TimeoutSeconds = 180)
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        try {
            $null = Get-TopicOffset -Topic $script:Topic -Time -1
            return $true
        } catch {
            Start-Sleep -Seconds 3
        }
    }
    return $false
}

function Wait-KafkaPodGone {
    param([int]$TimeoutSeconds = 120)
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $pods = @(Invoke-Kubectl -Arguments @('get', 'pods', '-n', $script:Namespace,
                '-l', 'app.kubernetes.io/name=kafka', '-o', 'name') -AllowFailure)
        if (($pods -join '').Trim() -eq '') { return $true }
        Start-Sleep -Seconds 2
    }
    return $false
}

function Get-KafkaAdminLogTail {
    <#
      .SYNOPSIS
        取应用侧 Kafka/Outbox 相关日志尾部（结构化事件名过滤），用于故障期间的行为证据。
    #>
    param([int]$Tail = 200, [string]$Pattern = 'flashsale_|kafka|Kafka')
    $lines = Invoke-Kubectl -Arguments @('logs', '-n', $script:Namespace, $script:AppTarget, '--tail', "$Tail") -AllowFailure
    return @($lines | Where-Object { $_ -match $Pattern })
}

function Get-FlashSaleDbState {
    <#
      .SYNOPSIS
        针对单个测试活动，取「业务终态 + 库存 + 请求分布」的权威快照（MySQL）。
      .DESCRIPTION
        MySQL 是秒杀链路的权威来源：HTTP 200 只代表受理，Kafka offset 只代表消息被投递，
        只有这张表能回答「到底有没有成交、有没有重复、库存扣了几次」。
    #>
    param([Parameter(Mandatory)][long]$ActivityId)
    $sql = @"
SELECT 'requests_total', COUNT(*) FROM flash_sale_order_requests WHERE activity_id = $ActivityId
UNION ALL SELECT 'requests_queued', COUNT(*) FROM flash_sale_order_requests WHERE activity_id = $ActivityId AND status = 0
UNION ALL SELECT 'requests_success', COUNT(*) FROM flash_sale_order_requests WHERE activity_id = $ActivityId AND status = 1
UNION ALL SELECT 'requests_failed', COUNT(*) FROM flash_sale_order_requests WHERE activity_id = $ActivityId AND status = 2
UNION ALL SELECT 'requests_dead', COUNT(*) FROM flash_sale_order_requests WHERE activity_id = $ActivityId AND status = 3
UNION ALL SELECT 'requests_unpublished', COUNT(*) FROM flash_sale_order_requests WHERE activity_id = $ActivityId AND published_at IS NULL
UNION ALL SELECT 'requests_retry_exhausted', COUNT(*) FROM flash_sale_order_requests WHERE activity_id = $ActivityId AND retry_count > 3
UNION ALL SELECT 'orders_total', COUNT(*) FROM flash_sale_orders WHERE activity_id = $ActivityId
UNION ALL SELECT 'orders_distinct_users', COUNT(DISTINCT user_id) FROM flash_sale_orders WHERE activity_id = $ActivityId
UNION ALL SELECT 'orders_dup_per_user', COUNT(*) FROM (SELECT user_id FROM flash_sale_orders WHERE activity_id = $ActivityId GROUP BY user_id HAVING COUNT(*) > 1) t
UNION ALL SELECT 'orders_dup_no', COUNT(*) FROM (SELECT order_no FROM flash_sale_orders WHERE activity_id = $ActivityId GROUP BY order_no HAVING COUNT(*) > 1) t
UNION ALL SELECT 'sku_total_stock', COALESCE(SUM(total_stock),0) FROM flash_sale_activity_skus WHERE activity_id = $ActivityId
UNION ALL SELECT 'sku_sold', COALESCE(SUM(sold),0) FROM flash_sale_activity_skus WHERE activity_id = $ActivityId
UNION ALL SELECT 'sku_remaining', COALESCE(SUM(total_stock - sold),0) FROM flash_sale_activity_skus WHERE activity_id = $ActivityId
"@
    $kv = Invoke-SqlKv -Sql $sql
    # 请求状态与失败码分布（用于判断「没有成交」是业务拒绝还是技术失败）。
    $dist = @(Invoke-SqlText -Sql @"
SELECT CONCAT('status=', status, ' code=', COALESCE(last_error_code,0), ' n=', COUNT(*))
FROM flash_sale_order_requests WHERE activity_id = $ActivityId
GROUP BY status, last_error_code ORDER BY status, last_error_code
"@)
    $result = [ordered]@{}
    foreach ($k in $kv.Keys) { $result[$k] = $kv[$k] }
    $result['request_status_distribution'] = $dist
    return $result
}
