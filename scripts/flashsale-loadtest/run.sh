#!/usr/bin/env bash
# =============================================================================
# 秒杀 V5 三级并发压测脚本（可重复执行）
#
# 按「低 / 中 / 高」三级并发档位对同一秒杀活动 × SKU 发起下单，逐档记录：
#   - 吞吐（QPS，总请求 / 总耗时）
#   - 时延 p50 / p95 / p99（仅统计得到真实 HTTP 响应的有效请求）
#   - HTTP 429 比例（限流 12009 / 排队软上限 12010）
#   - HTTP 503 比例（Redis 闸门熔断 / fail-closed 1005）
#   - queued 积压（压测期间排队峰值 + 压测后稳态值）
# 压测后经 MySQL 核对无超卖（sold ≤ total_stock）、成功订单数 = sold、无一人一单 /
# 幂等违例，并将结果保存为 JSON 基线（baselines/<scenario>-<timestamp>.json），
# 支持优化前后对比（见 compare.sh）。
#
# 前置条件：
#   - 应用已启动（默认 http://127.0.0.1:8000），MySQL/Redis 已就绪；
#   - 已存在一个进行中的秒杀活动与绑定 SKU（提供 ACTIVITY_ID / SKU_ID）；
#   - 依赖：curl、sort、awk、mysql（客户端）、date、mktemp。
#
# 用法：
#   ACTIVITY_ID=1 SKU_ID=1 SCENARIO=low    ./run.sh   # 低并发档（默认 worker=20）
#   ACTIVITY_ID=1 SKU_ID=1 SCENARIO=medium ./run.sh   # 中并发档（默认 worker=100）
#   ACTIVITY_ID=1 SKU_ID=1 SCENARIO=high   ./run.sh   # 高并发档（默认 worker=200）
#
# 说明：
#   - 三档并发 worker 数默认 low=20 / medium=100 / high=200，可用 CONCURRENCY 覆盖；
#   - 每档默认 1000 个用户 × 1 请求 = 1000 有效请求样本（可用 USERS / REQUESTS_PER_USER
#     覆盖），有效请求样本不足 MIN_SAMPLES 时脚本明确提示并在基线记录 sample_insufficient；
#   - 用户按档位隔离（用户名含档位前缀），保证三档对同一活动 × SKU 的测量互不干扰；
#   - SETTLE_SECONDS 为压测后等待后台消费者落单的静置时长，用于取得「压测后稳态」积压。
# =============================================================================
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8000}"
ACTIVITY_ID="${ACTIVITY_ID:-}"
SKU_ID="${SKU_ID:-}"
SCENARIO="${SCENARIO:-low}"
USERS="${USERS:-1000}"
REQUESTS_PER_USER="${REQUESTS_PER_USER:-1}"
MIN_SAMPLES="${MIN_SAMPLES:-1000}"
SETTLE_SECONDS="${SETTLE_SECONDS:-2}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_DIR="$SCRIPT_DIR/baselines"

# MySQL 校验参数（默认与 docker-compose 开发环境一致）。
MYSQL_HOST="${MYSQL_HOST:-127.0.0.1}"
MYSQL_PORT="${MYSQL_PORT:-3306}"
MYSQL_USER="${MYSQL_USER:-root}"
MYSQL_PASS="${MYSQL_PASS:-root}"
MYSQL_DB="${MYSQL_DB:-my_shop}"

# Redis 版本采集参数（仅用于采集版本，不参与压测主流程；默认与 docker-compose 一致）。
REDIS_HOST="${REDIS_HOST:-127.0.0.1}"
REDIS_PORT="${REDIS_PORT:-6379}"

log() { echo "[$(date '+%H:%M:%S')] $*"; }

# ---------------------------------------------------------------------------
# 纯函数：时延分位。$1=pct，从 stdin 读已排序时延（正数），输出分位值。
# ---------------------------------------------------------------------------
percentile() {
  awk -v p="$1" '{a[NR]=$1} END{ if(NR==0){print "0"; exit} i=int(p/100*(NR-1))+1; if(i>NR)i=NR; print a[i] }'
}

# ---------------------------------------------------------------------------
# 纯函数：正确性判定。参数：total_stock sold orders dup_one dup_idem。
# 输出 PASS / FAIL：任一不变量被破坏即 FAIL；活动/绑定缺失（空值）也 FAIL，避免假 PASS。
# ---------------------------------------------------------------------------
judge_correctness() {
  local total_stock="$1" sold="$2" orders="$3" dup_one="$4" dup_idem="$5"
  if [[ -z "$total_stock" || -z "$sold" ]]; then
    echo "FAIL"
  elif [[ "$sold" -gt "$total_stock" || "$orders" -ne "$sold" || "$dup_one" -ne 0 || "$dup_idem" -ne 0 ]]; then
    echo "FAIL"
  else
    echo "PASS"
  fi
}

# ---------------------------------------------------------------------------
# 纯函数：解析 worker 下单输出。$1=curl 输出（body|http_code|time_total）。
# 输出一行 "time_total,http_code,code"。
# 网络失败时 fallback 为 "|-1|0"（空 body），解析为 "0,-1,-1"，使该行被 analyze
# 归为 error / 非有效请求、不参与时延统计；正常 / 429 / 503 等成功响应路径不变。
# ---------------------------------------------------------------------------
parse_worker_out() {
  local out="$1" body time_total http_code code
  body="${out%|*|*}"
  time_total="${out##*|}"
  http_code="${out#*|}"; http_code="${http_code%|*}"
  code="$(printf '%s' "$body" | sed -n 's/.*"code":\([-0-9]*\).*/\1/p')"
  [ -n "$code" ] || code="-1"
  printf '%s,%s,%s\n' "$time_total" "$http_code" "$code"
}

# ---------------------------------------------------------------------------
# 纯函数：JSON 字符串转义。转义反斜杠、双引号与常见控制字符，保证 CPU 型号 /
# 版本号 / PromQL / 命令等字符串安全写入基线 JSON。
# ---------------------------------------------------------------------------
json_escape() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\n'/\\n}"
  s="${s//$'\r'/\\r}"
  s="${s//$'\t'/\\t}"
  printf '%s' "$s"
}

# ---------------------------------------------------------------------------
# 纯函数：数值字段输出。非空且为整数时原样输出，否则输出 null（未测量）。
# ---------------------------------------------------------------------------
num_or_null() {
  local v="$1"
  if [[ -n "$v" && "$v" =~ ^-?[0-9]+$ ]]; then
    printf '%s' "$v"
  else
    printf 'null'
  fi
}

mysql_q() { mysql -h"$MYSQL_HOST" -P"$MYSQL_PORT" -u"$MYSQL_USER" -p"$MYSQL_PASS" -N -B -e "$1" "$MYSQL_DB" 2>/dev/null; }

# ---------------------------------------------------------------------------
# 1) 准备用户：注册 USERS 个用户并登录，得到 token 列表（逐行对应第 1..USERS 个用户）。
# ---------------------------------------------------------------------------
setup_users() {
  log "注册并登录 $USERS 个用户 ..."
  : > "$TOKENS"
  for i in $(seq 1 "$USERS"); do
    # 用户名必须匹配 IAM 校验 ^[a-zA-Z0-9]{3,24}$，且按档位隔离（含 SCENARIO 前缀），
    # 保证三档对同一活动 × SKU 的「一人一单」测量互不干扰。
    local u="fslt${SCENARIO}${ACTIVITY_ID}${i}"
    local p="pass_${i}_x"
    curl -s -o /dev/null -X POST "$BASE_URL/register" -H 'Content-Type: application/json' \
      -d "{\"username\":\"$u\",\"password\":\"$p\"}" || true
    local resp token
    resp="$(curl -s -X POST "$BASE_URL/login" -H 'Content-Type: application/json' \
      -d "{\"username\":\"$u\",\"password\":\"$p\"}")"
    token="$(printf '%s' "$resp" | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')"
    [[ -n "$token" ]] || { echo "登录 $u 失败：$resp" >&2; exit 1; }
    echo "$token" >> "$TOKENS"
  done
  log "用户准备完成"
}

# ---------------------------------------------------------------------------
# 压测期间后台采样排队积压峰值（MySQL 权威 COUNT(status=0)）。
# 由 run_orders 在后台启动，load 结束后以 load_done 标记停止，输出峰值。
# ---------------------------------------------------------------------------
sample_queue_peak() {
  local peak=0 q
  while [[ ! -f "$WORK/load_done" ]]; do
    q="$(mysql_q "SELECT COUNT(*) FROM flash_sale_order_requests WHERE activity_id=$ACTIVITY_ID AND sku_id=$SKU_ID AND status=0")"
    if [[ -n "$q" && "$q" =~ ^[0-9]+$ && "$q" -gt "$peak" ]]; then
      peak="$q"
    fi
    sleep 0.2
  done
  echo "$peak"
}

# ---------------------------------------------------------------------------
# 生成独立 worker 脚本（每个请求由 xargs 派生一个新进程执行）。
# worker 复用 run.sh 的 parse_worker_out 作为单一解析来源，并通过 if ! 捕获 curl
# 退出码：curl 失败时（-w 仍会输出 "|000|<time>"）归一为网络失败行 "|-1|0"。
# ---------------------------------------------------------------------------
make_worker() {
  local worker="$1"
  cat > "$worker" <<'EOF'
#!/usr/bin/env bash
# 复用 run.sh 的 parse_worker_out（单一解析来源），保证网络失败与成功响应解析一致。
source "$SCRIPT_DIR/run.sh"
token="$1"
idx="$2"
key="lt_${SCENARIO}_${ACTIVITY_ID}_${idx}"
# curl 失败（连接拒绝/超时/重启窗口等）时 -w 仍会输出 "|000|<time>" 且退出码非 0；
# 若用 `|| echo` 兜底，会把兜底字符串与 -w 输出拼接成坏行。这里用 if ! 捕获退出码，
# 失败时把 out 归一为 "|-1|0"（网络失败），由 parse_worker_out 解析为 "0,-1,-1"。
if ! out="$(curl -s -w '|%{http_code}|%{time_total}' -X POST "${BASE_URL}/flash-sales/${ACTIVITY_ID}/orders" \
  -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
  -d "{\"sku_id\":${SKU_ID},\"idempotency_key\":\"${key}\"}" 2>/dev/null)"; then
  out="|-1|0"
fi
parse_worker_out "$out" >> "$RESULTS"
EOF
  chmod +x "$worker"
}

# ---------------------------------------------------------------------------
# 2) 并发下单：每个用户以唯一幂等键下单，记录时延（curl time_total）、HTTP 状态与业务 code。
#    用独立 worker 脚本 + xargs 并发执行（避免 export -f 函数跨进程的环境传递问题）。
# ---------------------------------------------------------------------------
run_orders() {
  log "开始下单：$(( USERS * REQUESTS_PER_USER )) 请求，并发 $CONCURRENCY ..."
  : > "$RESULTS"

  local worker="$WORK/worker.sh"
  make_worker "$worker"

  local -a jobs=()
  local i=1
  while read -r token; do
    local r
    for r in $(seq 1 "$REQUESTS_PER_USER"); do
      jobs+=("$token" "$(( i * REQUESTS_PER_USER + r ))")
    done
    i=$((i+1))
  done < "$TOKENS"

  # 压测期间后台采样排队积压峰值；load 结束后以 load_done 标记停止。
  rm -f "$WORK/load_done"
  sample_queue_peak > "$WORK/queue_peak.txt" &
  local sampler_pid=$!

  # 通过环境变量把运行参数传给 worker（worker 是独立进程，仅继承环境变量）。
  # SCRIPT_DIR 供 worker 内 source run.sh 复用 parse_worker_out。
  export SCENARIO ACTIVITY_ID SKU_ID BASE_URL RESULTS SCRIPT_DIR
  printf '%s\n' "${jobs[@]}" | xargs -P "$CONCURRENCY" -n 2 "$worker"
  touch "$WORK/load_done"
  wait "$sampler_pid" 2>/dev/null || true
  QUEUE_PEAK="$(cat "$WORK/queue_peak.txt" 2>/dev/null || echo 0)"
  log "下单完成"
}

# ---------------------------------------------------------------------------
# 3) 统计：吞吐、时延分位、成功/拒绝/失败分布，以及 HTTP 429/503 计数与样本下限判定。
# ---------------------------------------------------------------------------
analyze() {
  local total valid queued rejected error p50 p95 p99 throughput
  local http_429 http_503 ratio_429 ratio_503 insufficient
  local lat_file="$WORK/lat.txt"

  total="$(wc -l < "$RESULTS")"
  # 有效请求 = 得到真实 HTTP 响应的请求（http_code != -1，即 curl 成功）；时延仅对有效请求统计。
  valid="$(awk -F, '$2 != -1' "$RESULTS" | wc -l)"
  awk -F, '$2 != -1 {print $1}' "$RESULTS" | sort -n > "$lat_file"
  p50="$(percentile 50 < "$lat_file")"
  p95="$(percentile 95 < "$lat_file")"
  p99="$(percentile 99 < "$lat_file")"

  queued="$(awk -F, '$3==0' "$RESULTS" | wc -l)"
  rejected="$(awk -F, '$3!=0 && $3!=-1' "$RESULTS" | wc -l)"
  error="$(awk -F, '$3==-1' "$RESULTS" | wc -l)"

  # HTTP 状态码聚合（与逐请求记录第 2 列对账）：429 对应限流/排队软上限，503 对应熔断/fail-closed。
  http_429="$(awk -F, '$2==429' "$RESULTS" | wc -l)"
  http_503="$(awk -F, '$2==503' "$RESULTS" | wc -l)"
  ratio_429="$(awk -v a="$http_429" -v n="$total" 'BEGIN{ if(n>0) printf "%.2f", a*100/n; else print "0" }')"
  ratio_503="$(awk -v a="$http_503" -v n="$total" 'BEGIN{ if(n>0) printf "%.2f", a*100/n; else print "0" }')"

  throughput="$(awk -v n="$total" -v d="$ELAPSED" 'BEGIN{ if(d>0) printf "%.1f", n/d; else print "0" }')"

  # 样本下限核对：有效请求样本不足时明确提示，不在报告中用少量样本冒充 p99。
  insufficient=0
  [[ "$valid" -lt "$MIN_SAMPLES" ]] && insufficient=1

  local sample_verdict
  if [[ "$insufficient" -eq 0 ]]; then
    sample_verdict="足够（有效请求 $valid >= $MIN_SAMPLES）"
  else
    sample_verdict="不足（有效请求 $valid < $MIN_SAMPLES，p95/p99 不具统计意义）"
  fi

  cat <<EOF
===== 压测结果（$SCENARIO 档） =====
总请求:            $total
有效请求(有响应):  $valid
成功受理(queued):  $queued
拒绝(闸门/限流/排队): $rejected
错误(无业务码):    $error
HTTP 429:          $http_429 (${ratio_429}%)
HTTP 503:          $http_503 (${ratio_503}%)
吞吐:              $throughput req/s
时延 p50/p95/p99:  ${p50}s / ${p95}s / ${p99}s
queued 积压(峰值): ${QUEUE_PEAK:-0}
样本判定:          $sample_verdict
================================
EOF

  if [[ "$insufficient" -eq 1 ]]; then
    echo "警告：有效请求样本数 $valid 低于下限 $MIN_SAMPLES，p95/p99 不具备统计意义，请提高 USERS / REQUESTS_PER_USER。" >&2
  fi

  TOTAL_REQUESTS="$total"
  VALID_REQUESTS="$valid"
  ACCEPTED="$queued"
  REJECTED="$rejected"
  NETWORK_ERRORS="$error"
  LAT_P50="$p50"
  LAT_P95="$p95"
  LAT_P99="$p99"
  QPS="$throughput"
  HTTP_429="$http_429"
  HTTP_503="$http_503"
  RATIO_429="$ratio_429"
  RATIO_503="$ratio_503"
  SAMPLE_INSUFFICIENT="$insufficient"
}

# ---------------------------------------------------------------------------
# 4) 正确性核对（MySQL 权威事实）。
# ---------------------------------------------------------------------------
verify() {
  TOTAL_STOCK="$(mysql_q "SELECT total_stock FROM flash_sale_activity_skus WHERE activity_id=$ACTIVITY_ID AND sku_id=$SKU_ID LIMIT 1")"
  SOLD="$(mysql_q "SELECT sold FROM flash_sale_activity_skus WHERE activity_id=$ACTIVITY_ID AND sku_id=$SKU_ID LIMIT 1")"
  ORDERS="$(mysql_q "SELECT COUNT(*) FROM flash_sale_orders WHERE activity_id=$ACTIVITY_ID AND sku_id=$SKU_ID")"
  DUP_ONE="$(mysql_q "SELECT COUNT(*) FROM (SELECT user_id FROM flash_sale_orders WHERE activity_id=$ACTIVITY_ID AND sku_id=$SKU_ID GROUP BY user_id HAVING COUNT(*)>1) t")"
  DUP_IDEM="$(mysql_q "SELECT COUNT(*) FROM (SELECT user_id,idempotency_key FROM flash_sale_orders WHERE activity_id=$ACTIVITY_ID AND sku_id=$SKU_ID GROUP BY user_id,idempotency_key HAVING COUNT(*)>1) t")"
  QUEUED_STEADY="$(mysql_q "SELECT COUNT(*) FROM flash_sale_order_requests WHERE activity_id=$ACTIVITY_ID AND sku_id=$SKU_ID AND status=0")"
  MAX_CONNS="$(mysql_q "SHOW STATUS LIKE 'Max_used_connections'" | awk '{print $2}')"

  if [[ -z "$TOTAL_STOCK" || -z "$SOLD" ]]; then
    # 活动或绑定不存在时无法核对，不得判定通过（避免空值被当作 0 造成假 PASS）。
    echo "错误：未查询到活动/绑定（activity=$ACTIVITY_ID sku=$SKU_ID），无法核对正确性" >&2
  fi

  CORRECTNESS="$(judge_correctness "$TOTAL_STOCK" "$SOLD" "$ORDERS" "$DUP_ONE" "$DUP_IDEM")"

  local sold_ok orders_ok
  sold_ok="FAIL"; [[ -n "$TOTAL_STOCK" && -n "$SOLD" && "$SOLD" -le "$TOTAL_STOCK" ]] && sold_ok="OK"
  orders_ok="FAIL"; [[ -n "$SOLD" && -n "$ORDERS" && "$ORDERS" -eq "$SOLD" ]] && orders_ok="OK"

  cat <<EOF
===== 正确性核对 =====
total_stock: ${TOTAL_STOCK:-<空>}
sold:        ${SOLD:-<空>}  ($sold_ok 要求 sold <= total_stock)
成功订单数:  ${ORDERS:-<空>} ($orders_ok 要求 == sold)
重复订单:    one_per_user=$DUP_ONE idem=$DUP_IDEM（均应为 0）
queued 积压(稳态): ${QUEUED_STEADY:-0}
Max_used_connections: ${MAX_CONNS:-<空>}
判定:        $CORRECTNESS
======================
EOF
}

# ---------------------------------------------------------------------------
# 采集硬件资源（CPU 核数 / 型号、内存总量）。采集命令失败时对应字段置空，
# 由 save_baseline 记录为 null（未测量），不阻塞压测主流程。
# ---------------------------------------------------------------------------
collect_hardware() {
  CPU_CORES="$(nproc 2>/dev/null || true)"
  CPU_MODEL="$(lscpu 2>/dev/null | awk -F: '/^Model name:/{sub(/^[ \t]+/,"",$2); print $2; exit}' || true)"
  MEM_TOTAL_KB="$(awk '/^MemTotal:/{print $2}' /proc/meminfo 2>/dev/null || true)"
}

# ---------------------------------------------------------------------------
# 采集运行时版本（Go / MySQL / Redis）。客户端或容器不可用时字段置空（未测量）。
# Redis 优先本机 redis-cli，其次 docker exec 容器内 redis-cli（docker-compose 部署形态）。
# ---------------------------------------------------------------------------
collect_runtime() {
  GO_VERSION="$(go version 2>/dev/null | sed -n 's/^go version //p' || true)"
  MYSQL_VERSION="$(mysql_q "SELECT VERSION()" || true)"
  REDIS_VERSION="$(redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" INFO server 2>/dev/null | sed -n 's/^redis_version://p' || true)"
  if [[ -z "$REDIS_VERSION" ]]; then
    REDIS_VERSION="$(docker exec my-shop-redis redis-cli INFO server 2>/dev/null | sed -n 's/^redis_version://p' || true)"
  fi
}

# 压测所用的 Prometheus recording rules（下单接口 p95 / p99），与
# prometheus/rules/flashsale_latency.yml 保持一致；观测窗口为 rate 的 5m。
PROMQL_SOURCE="prometheus/rules/flashsale_latency.yml"
PROMQL_SCRAPE_INTERVAL="15s"
PROMQL_P95='histogram_quantile(0.95, sum by (le) (rate(flashsale_request_duration_seconds_bucket{interface="order"}[5m])))'
PROMQL_P99='histogram_quantile(0.99, sum by (le) (rate(flashsale_request_duration_seconds_bucket{interface="order"}[5m])))'

# ---------------------------------------------------------------------------
# 5) 保存基线 JSON：既有性能指标 + 硬件 / 运行时版本 / PromQL / 压测命令与观测时间范围。
# ---------------------------------------------------------------------------
save_baseline() {
  local ts file
  ts="$(date +%Y%m%dT%H%M%S)"
  file="$OUT_DIR/${SCENARIO}-${ts}.json"
  local insufficient_json
  [[ "$SAMPLE_INSUFFICIENT" -eq 1 ]] && insufficient_json="true" || insufficient_json="false"

  # 压测命令（复现用）：记录生效值，不含 MySQL 凭据等敏感信息。
  LOADTEST_COMMAND="ACTIVITY_ID=$ACTIVITY_ID SKU_ID=$SKU_ID SCENARIO=$SCENARIO USERS=$USERS REQUESTS_PER_USER=$REQUESTS_PER_USER CONCURRENCY=$CONCURRENCY MIN_SAMPLES=$MIN_SAMPLES SETTLE_SECONDS=$SETTLE_SECONDS BASE_URL=$BASE_URL ./run.sh"

  # 先统一做 JSON 转义，避免型号 / 版本 / PromQL / 命令中的引号与反斜杠破坏 JSON。
  local cpu_model_json go_json mysql_json redis_json base_url_json
  local promql_p95_json promql_p99_json cmd_json
  cpu_model_json="$(json_escape "${CPU_MODEL:-}")"
  go_json="$(json_escape "${GO_VERSION:-}")"
  mysql_json="$(json_escape "${MYSQL_VERSION:-}")"
  redis_json="$(json_escape "${REDIS_VERSION:-}")"
  base_url_json="$(json_escape "${BASE_URL}")"
  promql_p95_json="$(json_escape "$PROMQL_P95")"
  promql_p99_json="$(json_escape "$PROMQL_P99")"
  cmd_json="$(json_escape "$LOADTEST_COMMAND")"

  local cpu_cores_json mem_kb_json
  cpu_cores_json="$(num_or_null "${CPU_CORES:-}")"
  mem_kb_json="$(num_or_null "${MEM_TOTAL_KB:-}")"

  cat > "$file" <<EOF
{
  "scenario": "$SCENARIO",
  "timestamp": "$ts",
  "activity_id": $ACTIVITY_ID,
  "sku_id": $SKU_ID,
  "users": $USERS,
  "requests_per_user": $REQUESTS_PER_USER,
  "concurrency": $CONCURRENCY,
  "min_samples": $MIN_SAMPLES,
  "total_requests": $TOTAL_REQUESTS,
  "valid_requests": $VALID_REQUESTS,
  "sample_insufficient": $insufficient_json,
  "queued": $ACCEPTED,
  "rejected": $REJECTED,
  "error": $NETWORK_ERRORS,
  "throughput_req_per_sec": $QPS,
  "latency_p50_seconds": $LAT_P50,
  "latency_p95_seconds": $LAT_P95,
  "latency_p99_seconds": $LAT_P99,
  "http_429_count": $HTTP_429,
  "http_429_ratio_percent": $RATIO_429,
  "http_503_count": $HTTP_503,
  "http_503_ratio_percent": $RATIO_503,
  "sold": ${SOLD:-0},
  "total_stock": ${TOTAL_STOCK:-0},
  "queued_peak": ${QUEUE_PEAK:-0},
  "queued_backlog": ${QUEUED_STEADY:-0},
  "max_used_connections": ${MAX_CONNS:-0},
  "correctness": "$CORRECTNESS",
  "hardware": {
    "cpu_cores": $cpu_cores_json,
    "cpu_model": "$cpu_model_json",
    "memory_total_kb": $mem_kb_json,
    "collection_method": {
      "cpu_cores": "nproc",
      "cpu_model": "lscpu 的 Model name 字段",
      "memory_total_kb": "/proc/meminfo 的 MemTotal（单位 kB）"
    }
  },
  "runtime": {
    "go_version": "$go_json",
    "mysql_version": "$mysql_json",
    "redis_version": "$redis_json",
    "collection_method": {
      "go_version": "go version",
      "mysql_version": "mysql -N -B -e 'SELECT VERSION()'",
      "redis_version": "redis-cli INFO server（或 docker exec my-shop-redis redis-cli INFO server）"
    }
  },
  "promql": {
    "source": "$PROMQL_SOURCE",
    "scrape_interval": "$PROMQL_SCRAPE_INTERVAL",
    "queries": [
      {"record": "flashsale_order_request_duration_p95_seconds", "window": "5m", "promql": "$promql_p95_json"},
      {"record": "flashsale_order_request_duration_p99_seconds", "window": "5m", "promql": "$promql_p99_json"}
    ]
  },
  "loadtest": {
    "command": "$cmd_json",
    "key_env_vars": {
      "BASE_URL": "$base_url_json",
      "ACTIVITY_ID": "$ACTIVITY_ID",
      "SKU_ID": "$SKU_ID",
      "SCENARIO": "$SCENARIO",
      "USERS": "$USERS",
      "REQUESTS_PER_USER": "$REQUESTS_PER_USER",
      "CONCURRENCY": "$CONCURRENCY",
      "MIN_SAMPLES": "$MIN_SAMPLES",
      "SETTLE_SECONDS": "$SETTLE_SECONDS"
    },
    "start_epoch_seconds": ${START_TS:-0},
    "end_epoch_seconds": ${END_TS:-0},
    "duration_seconds": ${ELAPSED:-0}
  }
}
EOF
  log "基线已保存：$file"
}

# ---------------------------------------------------------------------------
# 主流程（仅在直接执行时运行；被 source 时仅加载函数，便于回归测试）。
# ---------------------------------------------------------------------------
main() {
  case "$SCENARIO" in
    low)    CONCURRENCY="${CONCURRENCY:-20}" ;;
    medium) CONCURRENCY="${CONCURRENCY:-100}" ;;
    high)   CONCURRENCY="${CONCURRENCY:-200}" ;;
    *) echo "未知 SCENARIO：$SCENARIO（可选 low/medium/high 三级并发档位）" >&2; exit 2 ;;
  esac

  [[ -n "$ACTIVITY_ID" && -n "$SKU_ID" ]] || { echo "必须提供 ACTIVITY_ID 与 SKU_ID" >&2; exit 2; }

  WORK="$(mktemp -d)"
  trap 'rm -rf "$WORK"' EXIT
  TOKENS="$WORK/tokens.txt"
  RESULTS="$WORK/results.csv"      # 每行：latency_seconds,http_status,business_code
  mkdir -p "$OUT_DIR"

  START_TS="$(date +%s.%N)"
  setup_users
  run_orders
  END_TS="$(date +%s.%N)"
  ELAPSED="$(awk -v s="$START_TS" -v e="$END_TS" 'BEGIN{print e-s}')"
  analyze
  # 等待后台消费者把排队请求落单，再取得「压测后稳态」积压与完整订单事实。
  sleep "$SETTLE_SECONDS"
  verify
  # 采集硬件与运行时版本（采集失败置空为「未测量」，不阻塞主流程）后再落基线。
  collect_hardware
  collect_runtime
  save_baseline
  log "压测完成"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
