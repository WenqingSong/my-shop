#!/usr/bin/env bash
# Prometheus 接入与 recording rules 静态校验（无需启动容器）。
#
# 校验「能区分正确与错误实现」的关键性质：
#   1. 抓取配置存在且 job_name=my-shop、metrics_path=/metrics、
#      目标为 host.docker.internal:8000、scrape_interval 已设置；
#   2. docker-compose 配置可解析，prometheus 服务具备只读挂载的抓取配置、
#      host.docker.internal:host-gateway 网络打通与 /-/ready healthcheck；
#   3. recording rules 存在、prometheus.yml 经 rule_files 引用、rules 目录已只读挂载；
#   4. recording rules 表达式用 histogram_quantile + rate(...[5m]) + interface="order" +
#      sum by (le)，且指标名后缀与单位一致（秒/毫秒不混配）；promtool 可用时校验语法。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

PROM_CFG="${ROOT_DIR}/prometheus/prometheus.yml"
COMPOSE_CFG="${ROOT_DIR}/docker-compose.yml"
RULES_DIR="${ROOT_DIR}/prometheus/rules"
RULES_FILE="${RULES_DIR}/flashsale_latency.yml"

fail() { log_error "$*"; exit 1; }

# 1) 抓取配置文件存在。
[[ -f "${PROM_CFG}" ]] || fail "缺少抓取配置 ${PROM_CFG}"

# 2) 校验抓取 job 关键字段（错误实现会因字段缺失/错值在此失败）。
grep -q '^  - job_name: my-shop$' "${PROM_CFG}" || fail "prometheus.yml 缺少 my-shop 抓取 job"
grep -q '^    metrics_path: /metrics$' "${PROM_CFG}" || fail "prometheus.yml 缺少 metrics_path=/metrics"
grep -q 'host.docker.internal:8000' "${PROM_CFG}" || fail "prometheus.yml 缺少目标 host.docker.internal:8000"
grep -Eq '^  scrape_interval: [0-9]+s$' "${PROM_CFG}" || fail "prometheus.yml 缺少合理的 scrape_interval"

# 3) compose 配置可解析（语法/合并校验）。
docker_compose -f "${COMPOSE_CFG}" config -q || fail "docker compose config 校验失败"

# 4) prometheus 服务具备网络打通、只读挂载与 healthcheck。
compose_out="$(docker_compose -f "${COMPOSE_CFG}" config)"
grep -q 'host.docker.internal' <<<"${compose_out}" || fail "prometheus 服务缺少 host.docker.internal 宿主机映射"
grep -q 'host-gateway' <<<"${compose_out}" || fail "prometheus 服务缺少 host-gateway 网络打通"
grep -q '/etc/prometheus/prometheus.yml' <<<"${compose_out}" || fail "prometheus 服务缺少抓取配置只读挂载"
grep -q '/-/ready' <<<"${compose_out}" || fail "prometheus 服务缺少 /-/ready healthcheck"

# 5) recording rules 存在，且 prometheus.yml 经 rule_files 引用、rules 目录已只读挂载。
[[ -f "${RULES_FILE}" ]] || fail "缺少 recording rules 文件 ${RULES_FILE}"
grep -q 'rule_files:' "${PROM_CFG}" || fail "prometheus.yml 缺少 rule_files"
grep -q 'flashsale_latency.yml' "${PROM_CFG}" || fail "prometheus.yml 的 rule_files 未引用 flashsale_latency.yml"
grep -q '/etc/prometheus/rules' <<<"${compose_out}" || fail "prometheus 服务缺少 rules 目录只读挂载"

# 6) 校验 recording rules 表达式关键性质（仅针对 expr/record 行，避免注释干扰）。
expr_lines="$(grep -E '^[[:space:]]*expr:' "${RULES_FILE}")" || fail "recording rule 缺少 expr 表达式"
record_lines="$(grep -E '^[[:space:]]*-[[:space:]]*record:' "${RULES_FILE}")" || fail "recording rule 缺少 record 指标名"
grep -qF 'histogram_quantile' <<<"${expr_lines}" || fail "recording rule 缺少 histogram_quantile"
grep -qF 'flashsale_request_duration_seconds_bucket' <<<"${expr_lines}" || fail "recording rule 未作用于 flashsale_request_duration_seconds_bucket"
grep -qF 'rate(' <<<"${expr_lines}" || fail "recording rule 缺少 rate()"
grep -qF '[5m]' <<<"${expr_lines}" || fail "recording rule 窗口不是 [5m]"
grep -qF 'sum by (le)' <<<"${expr_lines}" || fail "recording rule 缺少 sum by (le) 聚合"
grep -qF 'interface="order"' <<<"${expr_lines}" || fail "recording rule 未过滤 interface=\"order\"（应只取下单接口）"
grep -qF '0.95' <<<"${expr_lines}" || fail "recording rule 缺少 p95（0.95）"
grep -qF '0.99' <<<"${expr_lines}" || fail "recording rule 缺少 p99（0.99）"
if grep -qF 'interface="consume"' <<<"${expr_lines}"; then
  fail "recording rule 错误地过滤了 interface=\"consume\"（应只取 order）"
fi
# 单位一致性：秒值配 _seconds（无 * 1000），毫秒值配 _milliseconds（有 * 1000），不得混配。
if grep -qF '_milliseconds' <<<"${record_lines}"; then
  grep -qF '* 1000' <<<"${expr_lines}" || fail "指标名以 _milliseconds 结尾但表达式缺少 * 1000 换算"
elif grep -qF '_seconds' <<<"${record_lines}"; then
  if grep -qF '* 1000' <<<"${expr_lines}"; then
    fail "指标名以 _seconds 结尾但表达式出现 * 1000（单位错配，应为 _milliseconds）"
  fi
else
  fail "指标名后缀既不是 _seconds 也不是 _milliseconds（单位不明确）"
fi

# 7) promtool 语法校验：优先本机 promtool；否则从 prometheus 镜像提取 promtool 运行
#    （从镜像提取可在 rootless/权限受限环境下规避 bind 挂载不可达的问题）；
#    二者都不可用则跳过（静态断言仍是权威校验）。
if command -v promtool >/dev/null 2>&1; then
  promtool check rules "${RULES_FILE}" || fail "promtool check rules 失败"
else
  prom_image="$(grep -oE 'prom/prometheus:[^[:space:]"]+' <<<"${compose_out}" | head -n 1 || true)"
  if command -v docker >/dev/null 2>&1 && [[ -n "${prom_image}" ]] \
    && docker image inspect "${prom_image}" >/dev/null 2>&1; then
    promtool_tmp="$(mktemp)"
    docker run --rm --entrypoint cat "${prom_image}" /bin/promtool > "${promtool_tmp}" \
      || { rm -f "${promtool_tmp}"; fail "无法从镜像 ${prom_image} 提取 promtool"; }
    chmod +x "${promtool_tmp}"
    "${promtool_tmp}" check rules "${RULES_FILE}" \
      || { rm -f "${promtool_tmp}"; fail "promtool（镜像提取）check rules 失败"; }
    rm -f "${promtool_tmp}"
  else
    log_warn "未找到 promtool 或 prometheus 镜像，跳过 recording rules 语法校验（静态断言仍执行）"
  fi
fi

log_info "Prometheus 接入与 recording rules 静态校验通过"
