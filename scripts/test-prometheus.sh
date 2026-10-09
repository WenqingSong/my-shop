#!/usr/bin/env bash
# Prometheus 接入静态校验（无需启动容器）。
#
# 校验「能区分正确与错误实现」的关键性质：
#   1. 抓取配置存在且 job_name=my-shop、metrics_path=/metrics、
#      目标为 host.docker.internal:8000、scrape_interval 已设置；
#   2. docker-compose 配置可解析，prometheus 服务具备只读挂载的抓取配置、
#      host.docker.internal:host-gateway 网络打通与 /-/ready healthcheck。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

PROM_CFG="${ROOT_DIR}/prometheus/prometheus.yml"
COMPOSE_CFG="${ROOT_DIR}/docker-compose.yml"

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

log_info "Prometheus 接入静态校验通过"
