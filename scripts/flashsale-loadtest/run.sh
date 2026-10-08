#!/usr/bin/env bash
# =============================================================================
# 秒杀 V5 逐级压测脚本（可重复执行）
#
# 覆盖「正常库存 / 少库存 / 瞬时洪峰」三级场景，输出吞吐、时延（p50/p95/p99）、
# 成功/拒绝/失败分布；压测后经 MySQL 核对无超卖、无重复订单、队列积压有界、连接数不超阈值；
# 并将结果保存为 JSON 基线（baselines/<scenario>-<timestamp>.json），支持优化前后对比（见 compare.sh）。
#
# 前置条件：
#   - 应用已启动（默认 http://127.0.0.1:8000），MySQL/Redis 已就绪；
#   - 已存在一个进行中的秒杀活动与绑定 SKU（提供 ACTIVITY_ID / SKU_ID）；
#   - 依赖：curl、sort、awk、mysql（客户端）、date、mktemp。
#
# 用法：
#   ACTIVITY_ID=1 SKU_ID=1 SCENARIO=normal ./run.sh
#   SCENARIO=low_stock USERS=200 ./run.sh
#   SCENARIO=flood USERS=500 REQUESTS_PER_USER=2 ./run.sh
# =============================================================================
set -euo pipefail

BASE_URL="${BASE_URL:-http://127.0.0.1:8000}"
ACTIVITY_ID="${ACTIVITY_ID:-}"
SKU_ID="${SKU_ID:-}"
SCENARIO="${SCENARIO:-normal}"
USERS="${USERS:-100}"
REQUESTS_PER_USER="${REQUESTS_PER_USER:-1}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OUT_DIR="$SCRIPT_DIR/baselines"

# MySQL 校验参数（默认与 docker-compose 开发环境一致）。
MYSQL_HOST="${MYSQL_HOST:-127.0.0.1}"
MYSQL_PORT="${MYSQL_PORT:-3306}"
MYSQL_USER="${MYSQL_USER:-root}"
MYSQL_PASS="${MYSQL_PASS:-root}"
MYSQL_DB="${MYSQL_DB:-my_shop}"

# 三级场景预设（并发下单 worker 数）。
case "$SCENARIO" in
  normal)    CONCURRENCY="${CONCURRENCY:-20}" ;;
  low_stock) CONCURRENCY="${CONCURRENCY:-100}" ;;
  flood)     CONCURRENCY="${CONCURRENCY:-200}" ;;
  *) echo "未知 SCENARIO：$SCENARIO（可选 normal/low_stock/flood）" >&2; exit 2 ;;
esac

[[ -n "$ACTIVITY_ID" && -n "$SKU_ID" ]] || { echo "必须提供 ACTIVITY_ID 与 SKU_ID" >&2; exit 2; }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
TOKENS="$WORK/tokens.txt"
RESULTS="$WORK/results.csv"      # 每行：latency_seconds,http_status,business_code
STATS="$WORK/stats.txt"
VERDICT="$WORK/verdict.txt"
mkdir -p "$OUT_DIR"

log() { echo "[$(date '+%H:%M:%S')] $*"; }

# ---------------------------------------------------------------------------
# 1) 准备用户：注册 USERS 个用户并登录，得到 token 列表（逐行对应第 1..USERS 个用户）。
# ---------------------------------------------------------------------------
setup_users() {
  log "注册并登录 $USERS 个用户 ..."
  : > "$TOKENS"
  for i in $(seq 1 "$USERS"); do
    # 用户名必须匹配 IAM 校验 ^[a-zA-Z0-9]{3,24}$（纯字母数字，不含下划线），故用 fslt{activity}{i}。
    local u="fslt${ACTIVITY_ID}${i}"
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
# 2) 并发下单：每个用户以唯一幂等键下单，记录时延（curl time_total）、HTTP 状态与业务 code。
#    用独立 worker 脚本 + xargs 并发执行（避免 export -f 函数跨进程的环境传递问题）。
# ---------------------------------------------------------------------------
run_orders() {
  log "开始下单：$(( USERS * REQUESTS_PER_USER )) 请求，并发 $CONCURRENCY ..."
  : > "$RESULTS"

  local worker="$WORK/worker.sh"
  cat > "$worker" <<'EOF'
#!/usr/bin/env bash
token="$1"
idx="$2"
key="lt_${SCENARIO}_${ACTIVITY_ID}_${idx}"
out="$(curl -s -w '|%{http_code}|%{time_total}' -X POST "${BASE_URL}/flash-sales/${ACTIVITY_ID}/orders" \
  -H "Authorization: Bearer ${token}" -H 'Content-Type: application/json' \
  -d "{\"sku_id\":${SKU_ID},\"idempotency_key\":\"${key}\"}" 2>/dev/null || echo '||-1|0')"
body="${out%|*|*}"
time_total="${out##*|}"
http_code="${out#*|}"; http_code="${http_code%|*}"
code="$(printf '%s' "$body" | sed -n 's/.*"code":\([-0-9]*\).*/\1/p')"
[ -n "$code" ] || code="-1"
echo "$time_total,$http_code,$code" >> "$RESULTS"
EOF
  chmod +x "$worker"

  local -a jobs=()
  local i=1
  while read -r token; do
    local r
    for r in $(seq 1 "$REQUESTS_PER_USER"); do
      jobs+=("$token" "$(( i * REQUESTS_PER_USER + r ))")
    done
    i=$((i+1))
  done < "$TOKENS"

  # 通过环境变量把运行参数传给 worker（worker 是独立进程，仅继承环境变量）。
  export SCENARIO ACTIVITY_ID SKU_ID BASE_URL RESULTS
  printf '%s\n' "${jobs[@]}" | xargs -P "$CONCURRENCY" -n 2 "$worker"
  log "下单完成"
}

# ---------------------------------------------------------------------------
# 3) 统计：吞吐、时延分位、成功/拒绝/失败分布。
# ---------------------------------------------------------------------------
percentile() { # $1=pct，从 stdin 读已排序时延。
  awk -v p="$1" '{a[NR]=$1} END{ if(NR==0){print "0"; exit} i=int(p/100*(NR-1))+1; if(i>NR)i=NR; print a[i] }'
}

analyze() {
  local total lat_file
  total="$(wc -l < "$RESULTS")"
  lat_file="$WORK/lat.txt"
  awk -F, '{print $1}' "$RESULTS" | sort -n > "$lat_file"
  local p50 p95 p99
  p50="$(percentile 50 < "$lat_file")"
  p95="$(percentile 95 < "$lat_file")"
  p99="$(percentile 99 < "$lat_file")"
  local queued rejected error
  queued="$(awk -F, '$3==0' "$RESULTS" | wc -l)"
  rejected="$(awk -F, '$3!=0 && $3!=-1' "$RESULTS" | wc -l)"
  error="$(awk -F, '$3==-1' "$RESULTS" | wc -l)"
  local throughput
  throughput="$(awk -v n="$total" -v d="$ELAPSED" 'BEGIN{ if(d>0) printf "%.1f", n/d; else print "0" }')"
  cat <<EOF
===== 压测结果（$SCENARIO） =====
总请求:          $total
成功受理(queued): $queued
拒绝(闸门/限流/排队): $rejected
错误:            $error
吞吐:            $throughput req/s
时延 p50/p95/p99: ${p50}s / ${p95}s / ${p99}s
================================
EOF
  printf '%s %s %s %s %s %s %s %s\n' "$total" "$queued" "$rejected" "$error" "$p50" "$p95" "$p99" "$throughput" > "$STATS"
}

# ---------------------------------------------------------------------------
# 4) 正确性核对（MySQL 权威事实）。
# ---------------------------------------------------------------------------
mysql_q() { mysql -h"$MYSQL_HOST" -P"$MYSQL_PORT" -u"$MYSQL_USER" -p"$MYSQL_PASS" -N -B -e "$1" "$MYSQL_DB" 2>/dev/null; }

verify() {
  TOTAL_STOCK="$(mysql_q "SELECT total_stock FROM flash_sale_activity_skus WHERE activity_id=$ACTIVITY_ID AND sku_id=$SKU_ID LIMIT 1")"
  SOLD="$(mysql_q "SELECT sold FROM flash_sale_activity_skus WHERE activity_id=$ACTIVITY_ID AND sku_id=$SKU_ID LIMIT 1")"
  ORDERS="$(mysql_q "SELECT COUNT(*) FROM flash_sale_orders WHERE activity_id=$ACTIVITY_ID AND sku_id=$SKU_ID")"
  DUP_ONE="$(mysql_q "SELECT COUNT(*) FROM (SELECT user_id FROM flash_sale_orders WHERE activity_id=$ACTIVITY_ID AND sku_id=$SKU_ID GROUP BY user_id HAVING COUNT(*)>1) t")"
  DUP_IDEM="$(mysql_q "SELECT COUNT(*) FROM (SELECT user_id,idempotency_key FROM flash_sale_orders WHERE activity_id=$ACTIVITY_ID AND sku_id=$SKU_ID GROUP BY user_id,idempotency_key HAVING COUNT(*)>1) t")"
  QUEUED="$(mysql_q "SELECT COUNT(*) FROM flash_sale_order_requests WHERE activity_id=$ACTIVITY_ID AND sku_id=$SKU_ID AND status=0")"
  MAX_CONNS="$(mysql_q "SHOW STATUS LIKE 'Max_used_connections'" | awk '{print $2}')"

  local verdict="PASS"
  if [[ -z "$TOTAL_STOCK" || -z "$SOLD" ]]; then
    # 活动或绑定不存在时无法核对，不得判定通过（避免空值被当作 0 造成假 PASS）。
    echo "错误：未查询到活动/绑定（activity=$ACTIVITY_ID sku=$SKU_ID），无法核对正确性" >&2
    verdict="FAIL"
  elif [[ "$SOLD" -gt "$TOTAL_STOCK" || "$ORDERS" -ne "$SOLD" || "$DUP_ONE" -ne 0 || "$DUP_IDEM" -ne 0 ]]; then
    verdict="FAIL"
  fi

  cat <<EOF
===== 正确性核对 =====
total_stock: $TOTAL_STOCK
sold:        $SOLD  ($([[ "$SOLD" -le "$TOTAL_STOCK" ]] && echo OK || echo FAIL) 要求 sold <= total_stock)
成功订单数:  $ORDERS ($([[ "$ORDERS" -eq "$SOLD" ]] && echo OK || echo FAIL) 要求 == sold)
重复订单:    one_per_user=$DUP_ONE idem=$DUP_IDEM（均应为 0）
queued 积压: $QUEUED
Max_used_connections: $MAX_CONNS
判定:        $verdict
======================
EOF
  echo "$verdict" > "$VERDICT"
}

# ---------------------------------------------------------------------------
# 5) 保存基线 JSON。
# ---------------------------------------------------------------------------
save_baseline() {
  local ts
  ts="$(date +%Y%m%dT%H%M%S)"
  read -r total queued rejected error p50 p95 p99 throughput < "$STATS"
  local file="$OUT_DIR/${SCENARIO}-${ts}.json"
  cat > "$file" <<EOF
{
  "scenario": "$SCENARIO",
  "timestamp": "$ts",
  "activity_id": $ACTIVITY_ID,
  "sku_id": $SKU_ID,
  "users": $USERS,
  "requests_per_user": $REQUESTS_PER_USER,
  "concurrency": $CONCURRENCY,
  "total_requests": $total,
  "queued": $queued,
  "rejected": $rejected,
  "error": $error,
  "throughput_req_per_sec": $throughput,
  "latency_p50_seconds": $p50,
  "latency_p95_seconds": $p95,
  "latency_p99_seconds": $p99,
  "sold": $SOLD,
  "total_stock": $TOTAL_STOCK,
  "queued_backlog": $QUEUED,
  "max_used_connections": $MAX_CONNS,
  "correctness": "$(cat "$VERDICT")"
}
EOF
  log "基线已保存：$file"
}

START_TS="$(date +%s.%N)"
setup_users
run_orders
END_TS="$(date +%s.%N)"
ELAPSED="$(awk -v s="$START_TS" -v e="$END_TS" 'BEGIN{print e-s}')"
analyze
verify
save_baseline
log "压测完成"
