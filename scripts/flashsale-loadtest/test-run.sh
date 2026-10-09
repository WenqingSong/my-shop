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

echo "test-run.sh 全部通过"
