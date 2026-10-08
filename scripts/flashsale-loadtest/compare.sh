#!/usr/bin/env bash
# =============================================================================
# 秒杀 V5 压测基线对比脚本（优化前后对比）
#
# 对比两个由 run.sh 保存的 JSON 基线，输出关键指标（吞吐/时延/正确性）的前后差异，
# 用于验证限流/熔断开关或阈值/容量参数调整前后的效果。
#
# 用法：
#   ./compare.sh baselines/normal-20260101T120000.json baselines/normal-20260101T130000.json
# =============================================================================
set -euo pipefail

[[ $# -eq 2 ]] || { echo "用法：$0 <baseline_before.json> <baseline_after.json>" >&2; exit 2; }
BEFORE="$1"
AFTER="$2"
[[ -f "$BEFORE" && -f "$AFTER" ]] || { echo "基线文件不存在" >&2; exit 2; }

# 优先使用 python3 做 JSON 字段提取（更稳健），否则退化为整文件 diff。
if command -v python3 >/dev/null 2>&1; then
  field() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(d.get(sys.argv[2], ""))' "$1" "$2"; }
  fmt_num() { python3 -c 'import sys; v=sys.argv[1]; print(v if v=="" else ("%.2f" % float(v)))' "$1" 2>/dev/null || echo "$1"; }

  echo "===== 基线对比（优化前后） ====="
  printf "%-28s | %-12s | %-12s\n" "指标" "优化前" "优化后"
  printf "%-28s-+-%-12s-+-%-12s\n" "----------------------------" "------------" "------------"
  for key in scenario total_requests queued rejected error throughput_req_per_sec latency_p50_seconds latency_p95_seconds latency_p99_seconds sold total_stock queued_backlog max_used_connections correctness; do
    b="$(field "$BEFORE" "$key")"
    a="$(field "$AFTER" "$key")"
    printf "%-28s | %-12s | %-12s\n" "$key" "$b" "$a"
  done
  echo "==============================="
else
  echo "未检测到 python3，输出原始 diff：" >&2
  diff -u "$BEFORE" "$AFTER" || true
fi
