#!/usr/bin/env bash
# =============================================================================
# 秒杀压测基线摘要生成（简历可引用）
#
# 读取低 / 中 / 高三档最新基线 JSON，汇总为只含实测数字的 Markdown。
#   - 每个数字都来自基线 JSON 的实测字段，可逐项回溯到对应基线文件；
#   - 未测量项显式标记为「未测量」，不填写任何估算 / 理论值 / 未执行预测；
#   - 三档运行环境一致，环境信息取自首个可用档位基线并注明来源。
#
# 用法：
#   bash scripts/flashsale-loadtest/make-summary.sh > summary.md
#   BASELINES_DIR=/path/to/baselines TIERS="low medium high" \
#     bash scripts/flashsale-loadtest/make-summary.sh
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BASELINES_DIR="${BASELINES_DIR:-$SCRIPT_DIR/baselines}"
TIERS="${TIERS:-low medium high}"

[[ -d "$BASELINES_DIR" ]] || { echo "基线目录不存在：$BASELINES_DIR" >&2; exit 2; }
command -v python3 >/dev/null 2>&1 || { echo "需要 python3 解析基线 JSON" >&2; exit 2; }

python3 - "$BASELINES_DIR" "$TIERS" <<'PY'
import glob
import json
import os
import sys

baselines_dir = sys.argv[1]
tiers = sys.argv[2].split()


def latest(tier):
    """返回该档位最新基线（按文件名时间戳排序）的 (文件名, 解析后对象)，无则 (None, None)。"""
    files = sorted(glob.glob(os.path.join(baselines_dir, f"{tier}-*.json")))
    if not files:
        return None, None
    path = files[-1]
    with open(path, encoding="utf-8") as fh:
        return os.path.basename(path), json.load(fh)


tier_data = {tier: latest(tier) for tier in tiers}

# 三档同环境执行：环境信息取首个可用档位基线。
env_src = None
env = {}
for tier in tiers:
    src, data = tier_data[tier]
    if data is not None:
        env_src, env = src, data
        break


def fmt(value, unit=""):
    """实测字段格式化：None / 空字符串 → 未测量；其余原样输出并附单位。"""
    if value is None or value == "":
        return "未测量"
    if isinstance(value, bool):
        return str(value).lower()
    return f"{value}{unit}"


def get(*keys):
    cur = env
    for k in keys:
        if not isinstance(cur, dict) or k not in cur:
            return None
        cur = cur[k]
    return cur


def latency_ms(seconds):
    """秒 → 毫秒单位换算（实测秒字段 × 1000，非估计）。"""
    if seconds is None or seconds == "":
        return "未测量"
    try:
        return f"{float(seconds) * 1000:.2f}"
    except (TypeError, ValueError):
        return "未测量"


print("# 秒杀下单压测性能数据摘要")
print()
print("> 本摘要所有数字均来自 `scripts/flashsale-loadtest/baselines/` 下的实测基线 JSON，可逐项回溯；")
print("> 未测量项标记为「未测量」，不含任何估算、理论值或未执行的预测。")
print()

print("## 运行环境")
print()
if env_src is None:
    print("未测量（无任何档位基线）")
else:
    print(f"> 来源基线：`{env_src}`（低 / 中 / 高三档在同一环境执行）")
    print()
    hw = env.get("hardware", {}) or {}
    rt = env.get("runtime", {}) or {}
    hw_method = hw.get("collection_method", {}) or {}
    rt_method = rt.get("collection_method", {}) or {}
    print("| 项 | 值 | 采集方式 |")
    print("|---|---|---|")
    print(f"| CPU 核数 | {fmt(hw.get('cpu_cores'))} | {fmt(hw_method.get('cpu_cores'))} |")
    print(f"| CPU 型号 | {fmt(hw.get('cpu_model'))} | {fmt(hw_method.get('cpu_model'))} |")
    print(f"| 内存总量 | {fmt(hw.get('memory_total_kb'), ' kB')} | {fmt(hw_method.get('memory_total_kb'))} |")
    print(f"| Go 版本 | {fmt(rt.get('go_version'))} | {fmt(rt_method.get('go_version'))} |")
    print(f"| MySQL 版本 | {fmt(rt.get('mysql_version'))} | {fmt(rt_method.get('mysql_version'))} |")
    print(f"| Redis 版本 | {fmt(rt.get('redis_version'))} | {fmt(rt_method.get('redis_version'))} |")
print()

print("## 压测结果（低 / 中 / 高三档）")
print()
print("| 档位 | 并发 | 用户数 | 请求/用户 | 总请求 | 有效请求 | 吞吐(QPS) | p95 延迟 | p99 延迟 | 429 比例 | sold | total_stock | 样本判定 | 正确性 | 基线文件 |")
print("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|")

for tier in tiers:
    src, d = tier_data[tier]
    if d is None:
        print(f"| {tier} | 未测量 | 未测量 | 未测量 | 未测量 | 未测量 | 未测量 | 未测量 | 未测量 | 未测量 | 未测量 | 未测量 | 未测量 | 未测量 | 无基线 |")
        continue
    p95 = d.get("latency_p95_seconds")
    p99 = d.get("latency_p99_seconds")
    p95_cell = f"{fmt(p95, ' s')}（{latency_ms(p95)} ms）" if p95 not in (None, "") else "未测量"
    p99_cell = f"{fmt(p99, ' s')}（{latency_ms(p99)} ms）" if p99 not in (None, "") else "未测量"
    sample = "足够" if d.get("sample_insufficient") is False else ("不足" if d.get("sample_insufficient") is True else "未测量")
    print(
        f"| {tier} | {fmt(d.get('concurrency'))} | {fmt(d.get('users'))} | "
        f"{fmt(d.get('requests_per_user'))} | {fmt(d.get('total_requests'))} | "
        f"{fmt(d.get('valid_requests'))} | {fmt(d.get('throughput_req_per_sec'))} | "
        f"{p95_cell} | {p99_cell} | {fmt(d.get('http_429_ratio_percent'), '%')} | "
        f"{fmt(d.get('sold'))} | {fmt(d.get('total_stock'))} | {sample} | "
        f"{fmt(d.get('correctness'))} | `{src}` |"
    )
print()
print("> 延迟毫秒 = 实测秒字段 × 1000，属单位换算，非估计值。")

print()
print("## PromQL 查询与观测窗口")
print()
if env_src is None:
    print("未测量（无任何档位基线）")
else:
    promql = env.get("promql", {}) or {}
    print(f"- 来源：`{fmt(promql.get('source'))}`；抓取间隔：`{fmt(promql.get('scrape_interval'))}`")
    for q in promql.get("queries", []):
        print(f"- `{fmt(q.get('record'))}`（窗口 `{fmt(q.get('window'))}`）：")
        print(f"  ```promql")
        print(f"  {q.get('promql', '')}")
        print(f"  ```")
    lt = env.get("loadtest", {}) or {}
    print(f"- 观测时间范围：{fmt(lt.get('start_epoch_seconds'))} ~ {fmt(lt.get('end_epoch_seconds'))}（epoch 秒），")
    print(f"  压测持续 {fmt(lt.get('duration_seconds'), ' s')}，rate 观测窗口 5m。")
print()
PY
