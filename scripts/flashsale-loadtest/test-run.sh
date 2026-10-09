#!/usr/bin/env bash
# =============================================================================
# 秒杀压测脚本 run.sh 纯函数回归测试
#
# 覆盖：
#   - percentile：时延分位计算（含空输入兜底）；
#   - judge_correctness：正确性判定（超卖/订单数不一致/一人一单违例/幂等违例/空值均 FAIL）；
#   - analyze 聚合：429/503 计数、有效请求过滤、样本下限判定（不足/足够两条路径）。
#
# 直接运行：bash scripts/flashsale-loadtest/test-run.sh
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# source run.sh：仅加载函数定义（main 有 BASH_SOURCE 保护，不执行主流程）。
source "$SCRIPT_DIR/run.sh"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

# --- percentile：1..100 的 p50/p95/p99 应为 50/95/99 -----------------------
lat_file="$tmpdir/lat.txt"
seq 1 100 | sort -n > "$lat_file"
[[ "$(percentile 50 < "$lat_file")" == "50" ]] || fail "percentile p50 应为 50，实际 $(percentile 50 < "$lat_file")"
[[ "$(percentile 95 < "$lat_file")" == "95" ]] || fail "percentile p95 应为 95，实际 $(percentile 95 < "$lat_file")"
[[ "$(percentile 99 < "$lat_file")" == "99" ]] || fail "percentile p99 应为 99，实际 $(percentile 99 < "$lat_file")"

# 空输入 → 0。
: > "$tmpdir/empty.txt"
[[ "$(percentile 99 < "$tmpdir/empty.txt")" == "0" ]] || fail "percentile 空输入应返回 0"

# --- judge_correctness：正确性判定 ----------------------------------------
[[ "$(judge_correctness 100 50 50 0 0)" == "PASS" ]] || fail "正常（sold<=total、orders=sold、无违例）应 PASS"
[[ "$(judge_correctness 100 101 101 0 0)" == "FAIL" ]] || fail "超卖（sold>total）应 FAIL"
[[ "$(judge_correctness 100 50 49 0 0)" == "FAIL" ]] || fail "订单数 != sold 应 FAIL"
[[ "$(judge_correctness 100 50 50 1 0)" == "FAIL" ]] || fail "一人一单违例应 FAIL"
[[ "$(judge_correctness 100 50 50 0 1)" == "FAIL" ]] || fail "幂等违例应 FAIL"
[[ "$(judge_correctness "" "" 0 0 0)" == "FAIL" ]] || fail "活动不存在（空值）应 FAIL"

# --- analyze：聚合 429/503 计数、有效请求过滤、样本下限判定 ----------------
WORK="$tmpdir"
RESULTS="$tmpdir/results.csv"
SCENARIO="low"
ELAPSED="10"
QUEUE_PEAK="7"
MIN_SAMPLES="1000"
mkdir -p "$tmpdir"

# 构造 10 行结果（latency,http_code,business_code）：
#   3 个 queued(200/0)、2 个拒绝(409/12003)、2 个限流(429/12009)、
#   2 个熔断(503/1005)、1 个网络失败(-1/-1)。
cat > "$RESULTS" <<'EOF'
0.010,200,0
0.011,200,0
0.012,200,0
0.020,409,12003
0.021,409,12003
0.030,429,12009
0.031,429,12009
0.040,503,1005
0.041,503,1005
0,-1,-1
EOF

analyze > /dev/null 2>&1

[[ "$TOTAL_REQUESTS" == "10" ]] || fail "总请求应 10，实际 $TOTAL_REQUESTS"
[[ "$VALID_REQUESTS" == "9" ]] || fail "有效请求应 9（排除 1 个网络失败），实际 $VALID_REQUESTS"
[[ "$ACCEPTED" == "3" ]] || fail "成功受理(queued)应 3，实际 $ACCEPTED"
[[ "$REJECTED" == "6" ]] || fail "拒绝应 6（12003×2 + 12009×2 + 1005×2），实际 $REJECTED"
[[ "$NETWORK_ERRORS" == "1" ]] || fail "网络失败应 1，实际 $NETWORK_ERRORS"
[[ "$HTTP_429" == "2" ]] || fail "HTTP 429 计数应 2，实际 $HTTP_429"
[[ "$HTTP_503" == "2" ]] || fail "HTTP 503 计数应 2，实际 $HTTP_503"
[[ "$RATIO_429" == "20.00" ]] || fail "429 比例应 20.00%，实际 $RATIO_429"
[[ "$RATIO_503" == "20.00" ]] || fail "503 比例应 20.00%，实际 $RATIO_503"
[[ "$SAMPLE_INSUFFICIENT" == "1" ]] || fail "样本不足（9<1000）应置位"

# 样本充足路径：MIN_SAMPLES=5 时不应置位。
MIN_SAMPLES="5"
analyze > /dev/null 2>&1
[[ "$SAMPLE_INSUFFICIENT" == "0" ]] || fail "样本充足（9>=5）不应置位"

# --- parse_worker_out：worker 下单输出解析（含网络失败 fallback 回归）---------
# 网络失败 fallback（空 body + 时延 0 + http_code -1）必须解析为 "0,-1,-1"，
# 使该行被 analyze 归为 error / 非有效请求、不参与时延统计（回归 CLEAN-001）。
p_out="$(parse_worker_out '|-1|0')"
[[ "$p_out" == "0,-1,-1" ]] || fail "网络失败 fallback 应解析为 0,-1,-1，实际 $p_out"

# 正常 / 429 / 503 等成功响应路径解析保持不变。
p_out="$(parse_worker_out '{"code":0,"message":"ok"}|200|0.010')"
[[ "$p_out" == "0.010,200,0" ]] || fail "正常响应应解析为 0.010,200,0，实际 $p_out"
p_out="$(parse_worker_out '{"code":12009,"message":"限流"}|429|0.030')"
[[ "$p_out" == "0.030,429,12009" ]] || fail "限流 429 应解析为 0.030,429,12009，实际 $p_out"
p_out="$(parse_worker_out '{"code":1005,"message":"熔断"}|503|0.040')"
[[ "$p_out" == "0.040,503,1005" ]] || fail "熔断 503 应解析为 0.040,503,1005，实际 $p_out"
# 有 HTTP 响应但无业务 code 字段 → code=-1（归类为 error，不参与 queued/rejected 计数）。
p_out="$(parse_worker_out 'plain-body|500|0.050')"
[[ "$p_out" == "0.050,500,-1" ]] || fail "无业务码响应应 code=-1，实际 $p_out"

# --- make_worker 端到端：curl 完全失败时应落为 "0,-1,-1"（回归 CLEAN-001 根因）-----
# 用真实 worker（含 if ! 捕获 curl 退出码）对一个必然拒绝连接的端口下单，
# 断言写入 RESULTS 的行为网络失败行 "0,-1,-1"，而非被 -w 输出污染的坏行。
wdir="$tmpdir/worker"
mkdir -p "$wdir"
worker="$wdir/worker.sh"
make_worker "$worker"
wresults="$wdir/results.csv"
: > "$wresults"
export SCENARIO="low" ACTIVITY_ID="1" SKU_ID="1" BASE_URL="http://127.0.0.1:1" RESULTS="$wresults" SCRIPT_DIR="$SCRIPT_DIR"
"$worker" "dummy_token" "1"
wline="$(cat "$wresults")"
[[ "$wline" == "0,-1,-1" ]] || fail "worker 网络失败应输出 0,-1,-1，实际 $wline"

# --- json_escape / num_or_null：纯函数转义与数值输出 --------------------------
[[ "$(json_escape 'a"b\c')" == 'a\"b\\c' ]] || fail "json_escape 应转义引号与反斜杠，实际 $(json_escape 'a"b\c')"
[[ "$(json_escape $'x\ny')" == 'x\ny' ]] || fail "json_escape 应转义换行"
[[ "$(json_escape 'plain')" == 'plain' ]] || fail "json_escape 无特殊字符应原样返回"
[[ "$(num_or_null '8')" == '8' ]] || fail "num_or_null 整数应原样输出"
[[ "$(num_or_null '')" == 'null' ]] || fail "num_or_null 空值应输出 null"
[[ "$(num_or_null 'abc')" == 'null' ]] || fail "num_or_null 非整数应输出 null"

# --- collect_hardware / collect_runtime：采集命令缺失时优雅降级（置空、不崩溃）----
stubbin="$tmpdir/stubbin"
mkdir -p "$stubbin"
for c in nproc lscpu go mysql redis-cli docker; do
  cat > "$stubbin/$c" <<'S'
#!/usr/bin/env bash
exit 1
S
  chmod +x "$stubbin/$c"
done
OLD_PATH="$PATH"
PATH="$stubbin:$PATH"
collect_hardware
collect_runtime
PATH="$OLD_PATH"
[[ -z "$CPU_CORES" ]] || fail "nproc 缺失时 cpu_cores 应置空，实际 '$CPU_CORES'"
[[ -z "$CPU_MODEL" ]] || fail "lscpu 缺失时 cpu_model 应置空，实际 '$CPU_MODEL'"
[[ -z "$GO_VERSION" ]] || fail "go 缺失时 go_version 应置空，实际 '$GO_VERSION'"
[[ -z "$MYSQL_VERSION" ]] || fail "mysql 缺失时 mysql_version 应置空，实际 '$MYSQL_VERSION'"
[[ -z "$REDIS_VERSION" ]] || fail "redis-cli/docker 缺失时 redis_version 应置空，实际 '$REDIS_VERSION'"

# --- save_baseline：新增字段（硬件/运行时/PromQL/命令/时间范围）与未测量标记 ------
OUT_DIR="$tmpdir/out"
mkdir -p "$OUT_DIR"
SCENARIO="low" ACTIVITY_ID="1" SKU_ID="1" USERS="1000" REQUESTS_PER_USER="1"
CONCURRENCY="20" MIN_SAMPLES="1000" BASE_URL="http://127.0.0.1:8000"
TOTAL_REQUESTS="1000" VALID_REQUESTS="1000" SAMPLE_INSUFFICIENT="0"
ACCEPTED="10" REJECTED="990" NETWORK_ERRORS="0"
LAT_P50="0.01" LAT_P95="0.07" LAT_P99="0.31"
QPS="7.0" HTTP_429="990" HTTP_503="0" RATIO_429="99.00" RATIO_503="0.00"
SOLD="10" TOTAL_STOCK="1000" QUEUE_PEAK="5" QUEUED_STEADY="0" MAX_CONNS="20"
CORRECTNESS="PASS"
CPU_CORES="8" CPU_MODEL='AMD EPYC 9K65 192-Core Processor' MEM_TOTAL_KB="16777216"
GO_VERSION="go1.24.1" MYSQL_VERSION="8.0.46" REDIS_VERSION="7.4.11"
START_TS="1760000000.000000" END_TS="1760000000.500000" ELAPSED="0.5"
save_baseline
bfile="$(ls "$OUT_DIR"/*.json | head -1)"
mv "$bfile" "$tmpdir/baseline_measured.json"
bfile="$tmpdir/baseline_measured.json"

grep -q '"cpu_cores": 8' "$bfile" || fail "cpu_cores 应记录为 8"
grep -q '"cpu_model": "AMD EPYC 9K65 192-Core Processor"' "$bfile" || fail "cpu_model 应记录"
grep -q '"memory_total_kb": 16777216' "$bfile" || fail "memory_total_kb 应记录"
grep -q '"go_version": "go1.24.1"' "$bfile" || fail "go_version 应记录"
grep -q '"mysql_version": "8.0.46"' "$bfile" || fail "mysql_version 应记录"
grep -q '"redis_version": "7.4.11"' "$bfile" || fail "redis_version 应记录"
grep -q '"source": "prometheus/rules/flashsale_latency.yml"' "$bfile" || fail "PromQL 来源应记录"
grep -q 'histogram_quantile(0.95' "$bfile" || fail "应记录 p95 PromQL"
grep -q '"window": "5m"' "$bfile" || fail "应记录 5m 观测窗口"
grep -q '"start_epoch_seconds": 1760000000.000000' "$bfile" || fail "应记录压测起始时间戳"
grep -q '"end_epoch_seconds": 1760000000.500000' "$bfile" || fail "应记录压测结束时间戳"
grep -q '"command": "ACTIVITY_ID=1 SKU_ID=1 SCENARIO=low' "$bfile" || fail "应记录压测命令"
grep -q '"CONCURRENCY": "20"' "$bfile" || fail "应记录关键环境变量 CONCURRENCY"

# 未测量路径：采集字段为空时记录 null / 空字符串，而非估计值。
CPU_CORES="" CPU_MODEL="" MEM_TOTAL_KB="" GO_VERSION="" MYSQL_VERSION="" REDIS_VERSION=""
save_baseline
ufile="$(ls "$OUT_DIR"/*.json | head -1)"
grep -q '"cpu_cores": null' "$ufile" || fail "未测量的 cpu_cores 应记录 null"
grep -q '"cpu_model": ""' "$ufile" || fail "未测量的 cpu_model 应记录空字符串"
grep -q '"go_version": ""' "$ufile" || fail "未测量的 go_version 应记录空字符串"

# JSON 合法性（若 python3 可用）：转义后的基线必须是合法 JSON。
if command -v python3 >/dev/null 2>&1; then
  python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$bfile" || fail "基线不是合法 JSON"
  python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$ufile" || fail "未测量基线不是合法 JSON"
fi

# --- make-summary.sh：摘要只含实测数字、可回溯、未测量显式标记 ------------------
if command -v python3 >/dev/null 2>&1; then
  fdir="$tmpdir/fixtures"
  mkdir -p "$fdir"
  cat > "$fdir/low-20261009T230000.json" <<'J1'
{
  "scenario": "low", "concurrency": 20, "users": 1000, "requests_per_user": 1,
  "total_requests": 1000, "valid_requests": 1000, "sample_insufficient": false,
  "throughput_req_per_sec": 7.0, "latency_p50_seconds": 0.004111,
  "latency_p95_seconds": 0.072894, "latency_p99_seconds": 0.316408,
  "http_429_ratio_percent": 86.10, "sold": 139, "total_stock": 1000,
  "correctness": "PASS",
  "hardware": {"cpu_cores": 8, "cpu_model": "AMD EPYC 9K65 192-Core Processor",
    "memory_total_kb": 16777216, "collection_method": {"cpu_cores": "nproc", "cpu_model": "lscpu", "memory_total_kb": "/proc/meminfo"}},
  "runtime": {"go_version": "go1.24.1", "mysql_version": "8.0.46", "redis_version": "",
    "collection_method": {"go_version": "go version", "mysql_version": "mysql SELECT VERSION()", "redis_version": "redis-cli INFO server"}},
  "promql": {"source": "prometheus/rules/flashsale_latency.yml", "scrape_interval": "15s",
    "queries": [{"record": "flashsale_order_request_duration_p95_seconds", "window": "5m", "promql": "histogram_quantile(0.95, sum by (le) (rate(flashsale_request_duration_seconds_bucket{interface=\"order\"}[5m])))"}]},
  "loadtest": {"command": "ACTIVITY_ID=1 SKU_ID=1 SCENARIO=low ./run.sh",
    "key_env_vars": {"SCENARIO": "low", "CONCURRENCY": "20"},
    "start_epoch_seconds": 1760000000.0, "end_epoch_seconds": 1760000142.0, "duration_seconds": 142.0}
}
J1
  cat > "$fdir/medium-20261009T230100.json" <<'J2'
{
  "scenario": "medium", "concurrency": 100, "users": 1000, "requests_per_user": 1,
  "total_requests": 1000, "valid_requests": 1000, "sample_insufficient": false,
  "throughput_req_per_sec": 7.1, "latency_p50_seconds": 0.005,
  "latency_p95_seconds": 0.080, "latency_p99_seconds": 0.320,
  "http_429_ratio_percent": 87.0, "sold": 150, "total_stock": 1000, "correctness": "PASS"
}
J2
  cat > "$fdir/high-20261009T230200.json" <<'J3'
{
  "scenario": "high", "concurrency": 200, "users": 1000, "requests_per_user": 1,
  "total_requests": 1000, "valid_requests": 1000, "sample_insufficient": false,
  "throughput_req_per_sec": 7.2, "latency_p50_seconds": 0.017215,
  "latency_p95_seconds": 0.333111, "latency_p99_seconds": 0.364871,
  "http_429_ratio_percent": 87.90, "sold": 160, "total_stock": 1000, "correctness": "PASS"
}
J3

  summary="$(BASELINES_DIR="$fdir" TIERS="low medium high" bash "$SCRIPT_DIR/make-summary.sh")"
  grep -q 'go1.24.1' <<<"$summary" || fail "摘要应包含实测 Go 版本"
  grep -q '8.0.46' <<<"$summary" || fail "摘要应包含实测 MySQL 版本"
  grep -q '未测量' <<<"$summary" || fail "未测量项（redis_version 空）应标记未测量"
  grep -q '0.316408' <<<"$summary" || fail "摘要应包含实测 p99（low）"
  grep -q 'low-20261009T230000.json' <<<"$summary" || fail "摘要应含基线文件名（可回溯）"
  grep -q 'histogram_quantile(0.95' <<<"$summary" || fail "摘要应包含 PromQL 查询"

  # 缺少 high 档基线时应显式标记无基线/未测量，而非用其他档位数据填充。
  rm -f "$fdir/high-20261009T230200.json"
  summary_missing="$(BASELINES_DIR="$fdir" TIERS="low medium high" bash "$SCRIPT_DIR/make-summary.sh")"
  grep -q '无基线' <<<"$summary_missing" || fail "缺失档位应标记为无基线/未测量"
fi

echo "test-run.sh 全部通过"
