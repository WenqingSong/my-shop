#!/usr/bin/env bash
# =============================================================================
# 秒杀 V5 热点 Key 分析（可观测分析，非热点防护）
#
# 识别并输出秒杀 Redis 热点 Key（库存 key flashsale:stock:*、活动元数据 key flashsale:activity:*），
# 用于定位压测/洪峰期间被高频访问的 key。仅做可观测分析，不打散、不做本地缓存
# （Out of Scope 排除多级缓存/CDN，本地缓存会破坏多实例一致性）。
#
# 原理：
#   - 优先使用 redis-cli --hotkeys（依赖 Redis 服务端 LFU 淘汰策略 maxmemory-policy=allkeys-lfu/volatile-lfu，
#     其底层即 OBJECT FREQ 访问频次统计）；
#   - 兜底逐 key OBJECT FREQ（同样依赖 LFU 策略；未启用 LFU 时 OBJECT FREQ 会返回错误，此时输出提示）。
#
# 用法：
#   REDIS_HOST=127.0.0.1 REDIS_PORT=6379 ./hotkeys.sh [topN]
# =============================================================================
set -euo pipefail

REDIS_HOST="${REDIS_HOST:-127.0.0.1}"
REDIS_PORT="${REDIS_PORT:-6379}"
TOP_N="${1:-20}"

cli() { redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" "$@"; }

echo "===== 秒杀热点 Key 分析（flashsale:*） ====="

# 1) 优先尝试 --hotkeys（Redis >= 4.0 + LFU 策略）。
if out="$(cli --hotkeys 2>/dev/null)"; then
  echo "--- redis-cli --hotkeys（按访问频次排序，仅展示 flashsale: 前缀） ---"
  printf '%s\n' "$out" | grep -i 'flashsale:' | head -n "$TOP_N"
  if ! printf '%s\n' "$out" | grep -qi 'flashsale:'; then
    echo "（--hotkeys 未返回 flashsale: 前缀 key，可能当前无访问或策略未启用 LFU）"
  fi
else
  echo "--hotkeys 不可用（需 Redis>=4.0 且 maxmemory-policy 为 LFU），回退逐 key OBJECT FREQ ..."
fi

# 2) 兜底：枚举 flashsale:stock:* 与 flashsale:activity:*，逐个 OBJECT FREQ。
echo "--- OBJECT FREQ（flashsale:stock:* / flashsale:activity:*） ---"
found=0
for key in $(cli --scan --pattern 'flashsale:stock:*' 2>/dev/null; cli --scan --pattern 'flashsale:activity:*' 2>/dev/null); do
  freq="$(cli OBJECT FREQ "$key" 2>/dev/null || true)"
  if [[ -n "$freq" ]]; then
    echo "$freq  $key"
    found=$((found+1))
  fi
done | sort -rn | head -n "$TOP_N"
if [[ "$found" -eq 0 ]]; then
  echo "（OBJECT FREQ 未返回频次：请确认 Redis 已启用 LFU 策略，例如 CONFIG SET maxmemory-policy allkeys-lfu）"
fi
echo "========================================"
