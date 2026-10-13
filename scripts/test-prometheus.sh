#!/usr/bin/env bash
# Prometheus 接入与 recording rules 静态校验（无需启动容器）。
#
# 校验「能区分正确与错误实现」的关键性质：
#   1. 抓取配置存在且 job_name=my-shop、metrics_path=/metrics、
#      目标为 app:8000（Compose DNS 名）、scrape_interval 已设置；
#   2. docker-compose 配置可解析，prometheus 服务具备镜像内置的抓取配置与 /-/ready healthcheck，
#      且不再经 host.docker.internal/host-gateway 打通宿主机（app 与 prometheus 同处 Compose 网络）；
#   3. recording rules 存在、prometheus.yml 经 rule_files 引用、rules 目录已镜像内置；
#   4. recording rules 表达式用 histogram_quantile + rate(...[5m]) + interface="order" +
#      sum by (le)，且指标名后缀与单位一致（秒/毫秒不混配）；promtool 可用时校验语法。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

PROM_CFG="${ROOT_DIR}/prometheus/prometheus.yml"
PROM_DIR="${ROOT_DIR}/prometheus"
PROM_DOCKERFILE="${PROM_DIR}/Dockerfile"
COMPOSE_CFG="${ROOT_DIR}/docker-compose.yml"
RULES_DIR="${ROOT_DIR}/prometheus/rules"
RULES_FILE="${RULES_DIR}/flashsale_latency.yml"

fail() { log_error "$*"; exit 1; }

# 1) 抓取配置文件存在。
[[ -f "${PROM_CFG}" ]] || fail "缺少抓取配置 ${PROM_CFG}"

# 2) 校验抓取 job 关键字段（错误实现会因字段缺失/错值在此失败）。
grep -q '^  - job_name: my-shop$' "${PROM_CFG}" || fail "prometheus.yml 缺少 my-shop 抓取 job"
grep -q '^    metrics_path: /metrics$' "${PROM_CFG}" || fail "prometheus.yml 缺少 metrics_path=/metrics"
grep -q '"app:8000"' "${PROM_CFG}" || fail "prometheus.yml 缺少目标 app:8000（Compose DNS 名）"
grep -Eq '^  scrape_interval: [0-9]+s$' "${PROM_CFG}" || fail "prometheus.yml 缺少合理的 scrape_interval"

# 3) compose 配置可解析（语法/合并校验）。
# compose 已对 MYSQL_ROOT_PASSWORD / AUTH_JWT_SECRET 使用 ${VAR:?} 必填，语法校验时注入占位值（非真实凭据）。
docker_compose_with_placeholder() {
  MYSQL_ROOT_PASSWORD=test-root AUTH_JWT_SECRET=0123456789abcdef0123456789abcdef docker_compose "$@"
}
docker_compose_with_placeholder -f "${COMPOSE_CFG}" config -q || fail "docker compose config 校验失败"

# 4) prometheus 服务具备镜像内置配置与 healthcheck，且不再经宿主机网络打通。
#    说明：rootless/嵌套容器环境 bind mount 单文件会退化为目录挂载（文件变目录），
#    故抓取配置与 rules 改为 prometheus/Dockerfile COPY 内置（镜像层天然只读）。
#    抓取目标为 app:8000（Compose DNS），app 与 prometheus 同处 Compose 网络，
#    不应再出现 host.docker.internal / host-gateway 宿主机映射。
compose_out="$(docker_compose_with_placeholder -f "${COMPOSE_CFG}" config)"
if grep -q 'host.docker.internal' <<<"${compose_out}"; then
  fail "compose 仍残留 host.docker.internal 宿主机映射（应经 Compose DNS 名 app 通信）"
fi
if grep -q 'host-gateway' <<<"${compose_out}"; then
  fail "compose 仍残留 host-gateway 网络打通（应移除 extra_hosts）"
fi
grep -qF -- '--config.file=/etc/prometheus/prometheus.yml' <<<"${compose_out}" || fail "prometheus 服务缺少 --config.file 抓取配置路径"
grep -q '/-/ready' <<<"${compose_out}" || fail "prometheus 服务缺少 /-/ready healthcheck"

# 5) recording rules 存在，且 prometheus.yml 经 rule_files 引用、配置随镜像内置。
[[ -f "${RULES_FILE}" ]] || fail "缺少 recording rules 文件 ${RULES_FILE}"
grep -q 'rule_files:' "${PROM_CFG}" || fail "prometheus.yml 缺少 rule_files"
grep -q 'flashsale_latency.yml' "${PROM_CFG}" || fail "prometheus.yml 的 rule_files 未引用 flashsale_latency.yml"
[[ -f "${PROM_DOCKERFILE}" ]] || fail "缺少 ${PROM_DOCKERFILE}（配置应镜像内置）"
grep -qF 'prometheus.yml' "${PROM_DOCKERFILE}" || fail "Dockerfile 未内置 prometheus.yml"
grep -qF 'rules' "${PROM_DOCKERFILE}" || fail "Dockerfile 未内置 rules 目录"

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
  prom_image="$(grep -oE 'prom/prometheus:[^[:space:]"]+' "${PROM_DOCKERFILE}" | head -n 1 || true)"
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
