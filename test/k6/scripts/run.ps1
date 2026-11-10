<#
.SYNOPSIS
  SurgeCart k6 测试统一执行入口（T5-01）。

.DESCRIPTION
  为什么需要这一层：
    1. 本机没有安装 k6（Windows 11 + Docker Desktop），官方镜像 grafana/k6 是唯一
       零安装的运行方式，但 docker run 的参数（挂载、工作目录、环境变量、结果目录）
       每次手写容易出错，也容易在 T5-02~T5-04 各写一遍导致口径不一致；
    2. 结果必须可追溯：k6 自己只能产出指标，Git SHA / Kubernetes Context /
       应用镜像 / 起止时间这些「上下文」只能由外部收集，本脚本负责统一落盘；
    3. 一次执行同时产出：k6 控制台日志、summary.json / summary.txt、meta.json。

.PARAMETER Scenario
  smoke | read-load | flashsale-order | prepare-data。对应 scenarios/ 或 tools/ 下的脚本。

.PARAMETER RunId
  运行标识；省略时按 UTC 紧凑时间戳生成（r20261110t034126）。
  同一 RunId 重复执行会覆盖结果文件，便于「同条件复跑对比」。

.PARAMETER Profile
  负载档位 low/medium/high（仅 read-load / flashsale-order 生效）。

.PARAMETER BaseUrl
  被测入口（k6 容器视角）。默认 http://host.docker.internal:8000，
  即「先 kubectl port-forward svc/surgecart 8000:8000，再从容器访问宿主机」。

.PARAMETER K6Args
  额外 k6 参数，例如 --out json=/results/raw.json。

.PARAMETER Set
  额外环境变量表，例如 @{ ADMIN_USERNAME='admin'; K6_TEST_PASSWORD='...' }。
  敏感值优先通过进程环境变量传入（本脚本会白名单透传），避免进入命令历史。

.EXAMPLE
  ./run.ps1 -Scenario smoke

.EXAMPLE
  $env:ADMIN_USERNAME='admin'; $env:ADMIN_PASSWORD='***'
  ./run.ps1 -Scenario prepare-data -RunId r20261110t040000
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('smoke', 'read-load', 'flashsale-order', 'prepare-data')]
    [string]$Scenario,

    [string]$RunId = '',

    [ValidateSet('low', 'medium', 'high', '')]
    [string]$Profile = '',

    [string]$BaseUrl = '',

    [string[]]$K6Args = @(),

    [hashtable]$Set = @{}
)

$ErrorActionPreference = 'Stop'

# 路径约定：本文件在 test/k6/scripts/，仓库根在上三级。
$k6Dir = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$repoRoot = (Resolve-Path (Join-Path $k6Dir '../..')).Path
$resultsDir = Join-Path $k6Dir 'results'
$dataDir = Join-Path $k6Dir 'data'

$K6Image = 'grafana/k6:0.54.0'

function ConvertTo-DockerPath([string]$path) {
    # Docker Desktop for Windows 接受 D:/a/b 形式；统一正斜杠避免转义问题。
    return $path.Replace('\', '/')
}

function New-Timestamp() {
    return (Get-Date).ToUniversalTime().ToString('yyyyMMddtHHmmss')
}

if (-not (Test-Path $resultsDir)) { New-Item -ItemType Directory -Path $resultsDir | Out-Null }
if (-not (Test-Path $dataDir)) { New-Item -ItemType Directory -Path $dataDir | Out-Null }

if ([string]::IsNullOrWhiteSpace($RunId)) {
    $RunId = 'r' + (New-Timestamp)
}

# ---- 前置检查：Docker 与镜像 -------------------------------------------------
if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    throw '未找到 docker 命令。请先启动 Docker Desktop（k6 通过官方镜像运行，不安装到宿主机）。'
}
docker info --format '{{.ServerVersion}}' | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw 'docker 守护进程不可用。请启动 Docker Desktop 后重试。'
}
$images = docker images --format '{{.Repository}}:{{.Tag}}'
if (-not ($images -contains $K6Image)) {
    Write-Host "[k6-run] 本地缺少镜像 $K6Image，开始拉取 ..."
    docker pull $K6Image
    if ($LASTEXITCODE -ne 0) { throw "拉取 $K6Image 失败，请检查网络或镜像源。" }
}

# ---- 环境变量表 --------------------------------------------------------------
if ([string]::IsNullOrWhiteSpace($BaseUrl)) {
    if ([string]::IsNullOrWhiteSpace($env:SURGECART_BASE_URL)) {
        $BaseUrl = 'http://host.docker.internal:8000'
    } else {
        $BaseUrl = $env:SURGECART_BASE_URL
    }
}
if ($BaseUrl -match '127\.0\.0\.1|localhost') {
    Write-Warning 'BASE_URL 指向宿主回环地址：k6 在容器内运行，127.0.0.1 指向容器自身。请使用 host.docker.internal 或集群内 Service DNS。'
}

$envMap = [ordered]@{
    'SURGECART_BASE_URL' = $BaseUrl
    'RUN_ID'             = $RunId
    'SCENARIO'           = $Scenario
    'K6_SUMMARY_DIR'     = '/results'
}
if (-not [string]::IsNullOrWhiteSpace($env:TEST_ENV)) { $envMap['TEST_ENV'] = $env:TEST_ENV }
if ($Profile -ne '') { $envMap['LOAD_PROFILE'] = $Profile }

# 白名单透传：这些变量只从当前进程环境读取，避免把凭据写进命令行/脚本。
$passthrough = @(
    'K6_TEST_PASSWORD', 'ADMIN_USERNAME', 'ADMIN_PASSWORD',
    'DATA_PREFIX', 'TEST_ENV', 'EXPECT_DISPATCH_MODE', 'PROMETHEUS_BASE_URL',
    'ACTIVITY_ID', 'SKU_ID', 'PRODUCT_ID', 'CATEGORY_ID',
    'FLASH_USERS', 'FLASH_TOTAL_STOCK', 'INVENTORY_QTY', 'SKU_PRICE', 'FLASH_PRICE',
    'FLASH_IDEMPOTENCY_REPLAY', 'FLASH_VERIFY', 'FLASH_POLL_ATTEMPTS', 'FLASH_POLL_INTERVAL_MS',
    'LOAD_PROFILE', 'LOAD_VUS', 'THINK_TIME_MS', 'HTTP_TIMEOUT_MS', 'K6_RAW_JSON'
)
foreach ($name in $passthrough) {
    $value = [Environment]::GetEnvironmentVariable($name)
    if (-not [string]::IsNullOrWhiteSpace($value)) { $envMap[$name] = $value }
}
foreach ($key in $Set.Keys) { $envMap[$key] = [string]$Set[$key] }

$dockerArgs = @('run', '--rm',
    '-v', "$(ConvertTo-DockerPath $k6Dir):/scripts",
    '-v', "$(ConvertTo-DockerPath $resultsDir):/results",
    '-v', "$(ConvertTo-DockerPath $dataDir):/data",
    '-w', '/scripts')
foreach ($key in $envMap.Keys) {
    $dockerArgs += @('-e', "$key=$($envMap[$key])")
}

# ---- 选择脚本 ----------------------------------------------------------------
if ($Scenario -eq 'prepare-data') {
    $scriptRel = 'tools/prepare-data.js'
} else {
    $scriptRel = "scenarios/$Scenario.js"
}
$dockerArgs += @($K6Image, 'run')
if ($K6Args.Count -gt 0) { $dockerArgs += $K6Args }
$dockerArgs += @("/scripts/$scriptRel")

# ---- 执行 --------------------------------------------------------------------
$startedAt = (Get-Date).ToUniversalTime().ToString('o')
$logPath = Join-Path $resultsDir "$RunId.k6.log"
Write-Host "[k6-run] scenario=$Scenario run_id=$RunId profile=$($envMap['LOAD_PROFILE']) base_url=$BaseUrl"
Write-Host "[k6-run] docker $($dockerArgs -join ' ')"

# k6 把 console 日志写到 stderr，而 PowerShell 5.1 在 $ErrorActionPreference='Stop'
# 下会把原生命令的 stderr 当终止错误。这里局部降为 Continue 并统一转成字符串，
# 既保留完整日志，又不会让「k6 正常输出日志」被误判为脚本失败。
$prevEap = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
$rawOutput = & docker @dockerArgs 2>&1
$exitCode = $LASTEXITCODE
$ErrorActionPreference = $prevEap

$output = @($rawOutput | ForEach-Object { $_.ToString() })
$output | Set-Content -Path $logPath -Encoding utf8
$output | ForEach-Object { Write-Host $_ }

# ---- 元数据收集（k6 自己拿不到的外部上下文） ---------------------------------
# 为什么必须收集：k6 结果只有指标，没有 Git SHA / 集群 / 镜像信息。
# 没有这些上下文，T5-02 的两次结果无法判断「是不是同一个被测版本」。
# 全部使用 PowerShell 原生调用 + try/catch，避免 cmd 对 jsonpath 花括号的解析差异。
function Get-FirstLine([scriptblock]$block) {
    try {
        $value = & $block 2>$null
        if ($null -eq $value) { return '' }
        return ($value | Select-Object -First 1).ToString().Trim()
    } catch {
        return ''
    }
}

$meta = [ordered]@{
    run_id             = $RunId
    scenario           = $Scenario
    k6_image           = $K6Image
    base_url           = $BaseUrl
    profile            = $envMap['LOAD_PROFILE']
    started_at_utc     = $startedAt
    finished_at_utc    = (Get-Date).ToUniversalTime().ToString('o')
    exit_code          = $exitCode
    git_branch         = Get-FirstLine { git -C $repoRoot branch --show-current }
    git_sha            = Get-FirstLine { git -C $repoRoot rev-parse HEAD }
    git_dirty          = (Get-FirstLine { git -C $repoRoot status --porcelain }) -ne ''
    kube_context       = Get-FirstLine { kubectl config current-context }
    app_image          = Get-FirstLine { kubectl -n surgecart get deploy surgecart -o 'jsonpath={.spec.template.spec.containers[0].image}' }
    prometheus_service = 'monitoring/prometheus:9090（kubectl port-forward svc/prometheus 9090:9090 后查询）'
    k6_log             = $logPath
    env                = ($envMap.Keys | Where-Object { $_ -notmatch 'PASSWORD' } | ForEach-Object { "$_=$($envMap[$_])" }) -join '; '
}

$metaPath = Join-Path $resultsDir "$RunId.meta.json"
$meta | ConvertTo-Json -Depth 4 | Set-Content -Path $metaPath -Encoding utf8

# ---- 数据文件抽取（仅 prepare-data） -----------------------------------------
if ($Scenario -eq 'prepare-data') {
    $marker = $output | Where-Object { $_ -like 'K6_DATA_MARKER *' } | Select-Object -Last 1
    if ($marker) {
        $json = $marker.ToString().Substring('K6_DATA_MARKER '.Length)
        $dataPath = Join-Path $dataDir "$RunId.data.json"
        $json | Set-Content -Path $dataPath -Encoding utf8
        Write-Host "[k6-run] 已写入 $dataPath"
    } else {
        Write-Warning '未在输出中找到 K6_DATA_MARKER，数据准备可能未完成，请检查日志。'
    }
}

# ---- 结果提示 ----------------------------------------------------------------
$summaryPath = Join-Path $resultsDir "$RunId.summary.txt"
if (Test-Path $summaryPath) {
    Write-Host ''
    Get-Content $summaryPath | Write-Host
} else {
    Write-Warning "未找到 summary 文件 $summaryPath（脚本可能未定义 handleSummary 或提前失败）。"
}
Write-Host "[k6-run] meta: $metaPath"
Write-Host "[k6-run] log : $logPath"

exit $exitCode
