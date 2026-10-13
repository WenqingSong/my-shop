#!/usr/bin/env bash
# Nginx 反向代理静态校验（无需启动容器，验证「能区分正确与错误实现」的关键性质）。
#
# 校验范围（对应 APPROVED Contract D1~D10 / AC-001/003/004/005/006/007/008）：
#   1. 信任边界（IP 防伪造）：覆盖式重写 X-Forwarded-For / X-Real-IP 为 $remote_addr，
#      且不得使用 $proxy_add_x_forwarded_for（会保留客户端伪造值在首位）；
#   2. 重试边界：proxy_next_upstream off（任何方法不自动重试）；
#   3. 上游 DNS 再解析：resolver 127.0.0.11 + server app:8000 resolve + zone；
#   4. 监控隔离：location = /metrics 返回 404；
#   5. 安全响应头：X-Content-Type-Options nosniff / X-Frame-Options DENY / server_tokens off；
#   6. 日志关联：X-Request-ID 覆盖透传、log_format 含 $request_id 与 $upstream_response_time，
#      且不记录 Authorization / Cookie 等敏感信息；
#   7. 错误语义：不拦截业务错误（无 proxy_intercept_errors），请求体超限返回 413；
#   8. 编排：nginx 服务存在、app 不映射宿主机端口、mysql/redis 回环绑定、nginx 依赖 app healthy；
#   9. nginx -t 语法校验（docker 可用时 best-effort）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

NGINX_CONF="${ROOT_DIR}/nginx/nginx.conf"
NGINX_DOCKERFILE="${ROOT_DIR}/nginx/Dockerfile"
COMPOSE_CFG="${ROOT_DIR}/docker-compose.yml"

fail() { log_error "$*"; exit 1; }

# 1) 配置文件存在。
[[ -f "${NGINX_CONF}" ]] || fail "缺少 ${NGINX_CONF}"
[[ -f "${NGINX_DOCKERFILE}" ]] || fail "缺少 ${NGINX_DOCKERFILE}"

# 2) 信任边界（IP 防伪造）：覆盖式重写，且禁用 $proxy_add_x_forwarded_for。
grep -qF 'proxy_set_header X-Forwarded-For $remote_addr;' "${NGINX_CONF}" \
  || fail "nginx.conf 未覆盖式重写 X-Forwarded-For 为 \$remote_addr（AC-003 防伪造）"
grep -qF 'proxy_set_header X-Real-IP       $remote_addr;' "${NGINX_CONF}" \
  || fail "nginx.conf 未覆盖式重写 X-Real-IP 为 \$remote_addr（AC-003 防伪造）"
if grep -qF '$proxy_add_x_forwarded_for' "${NGINX_CONF}"; then
  fail "nginx.conf 使用 \$proxy_add_x_forwarded_for 会保留客户端伪造值（GetClientIp 取第一项，必须覆盖式重写）"
fi

# 3) 重试边界：任何方法不自动重试。
grep -qF 'proxy_next_upstream off;' "${NGINX_CONF}" \
  || fail "nginx.conf 未关闭 proxy_next_upstream（非幂等请求不得自动重试，AC-004）"
if grep -qF 'non_idempotent' "${NGINX_CONF}"; then
  fail "nginx.conf 不应出现 non_idempotent（应完全关闭代理重试）"
fi

# 4) 上游运行时 DNS 再解析。
grep -qF 'resolver 127.0.0.11' "${NGINX_CONF}" \
  || fail "nginx.conf 缺少 Docker 内嵌 DNS resolver（上游运行时再解析依赖）"
grep -qF 'server app:8000 resolve;' "${NGINX_CONF}" \
  || fail "nginx.conf upstream 未使用 server resolve（app 重建后 IP 变化无法自动恢复）"
grep -qF 'zone my_shop_backend 64k;' "${NGINX_CONF}" \
  || fail "nginx.conf upstream 缺少 zone（server resolve 需共享内存区）"
grep -qF 'keepalive 32;' "${NGINX_CONF}" \
  || fail "nginx.conf upstream 缺少 keepalive 连接复用"

# 5) 监控隔离：/metrics 公共入口 404。
grep -qF 'location = /metrics {' "${NGINX_CONF}" \
  || fail "nginx.conf 缺少 location = /metrics（公共入口应隔离）"
grep -qF 'return 404;' "${NGINX_CONF}" \
  || fail "nginx.conf 未对 /metrics 返回 404（AC-005）"

# 6) 安全响应头与版本隐藏。
grep -qF 'server_tokens   off;' "${NGINX_CONF}" || fail "nginx.conf 未关闭 server_tokens"
grep -qF 'X-Content-Type-Options nosniff always;' "${NGINX_CONF}" || fail "nginx.conf 缺少 X-Content-Type-Options"
grep -qF 'X-Frame-Options DENY always;' "${NGINX_CONF}" || fail "nginx.conf 缺少 X-Frame-Options"

# 7) 日志关联与敏感信息约束。
grep -qF 'proxy_set_header X-Request-ID    $request_id;' "${NGINX_CONF}" \
  || fail "nginx.conf 未覆盖式透传 X-Request-ID（AC-006）"
grep -qF '"request_id":"$request_id"' "${NGINX_CONF}" || fail "nginx.conf log_format 缺少 request_id"
grep -qF '"upstream_response_time":"$upstream_response_time"' "${NGINX_CONF}" \
  || fail "nginx.conf log_format 缺少 upstream_response_time（AC-006 耗时记录）"
# 仅检查生效配置行（排除注释），避免注释里说明「不记录 Authorization/Cookie」造成误报。
nginx_body="$(sed '/^[[:space:]]*#/d' "${NGINX_CONF}")"
if grep -qiE 'authorization|cookie' <<<"${nginx_body}"; then
  fail "nginx.conf 疑似记录 Authorization / Cookie 等敏感信息（禁止入日志）"
fi

# 8) 错误语义：不拦截业务错误；请求体超限返回 413。
if grep -qF 'proxy_intercept_errors' "${NGINX_CONF}"; then
  fail "nginx.conf 不应设置 proxy_intercept_errors（业务错误应原样透传，AC-007）"
fi
grep -qF 'client_max_body_size' "${NGINX_CONF}" || fail "nginx.conf 缺少 client_max_body_size（AC-008）"
grep -qF 'error_page 413 /413.html;' "${NGINX_CONF}" || fail "nginx.conf 未对 413 设置错误页"

# 9) 编排模型：nginx 服务、app 无宿主端口、mysql/redis 回环绑定、nginx 依赖 app healthy。
[[ -f "${COMPOSE_CFG}" ]] || fail "缺少 ${COMPOSE_CFG}"
compose_body="$(sed '/^[[:space:]]*#/d' "${COMPOSE_CFG}")"
grep -q '^  nginx:' <<<"${compose_body}" || fail "docker-compose.yml 缺少 nginx 服务"
grep -qF '"${NGINX_PORT:-8080}:80"' <<<"${compose_body}" || fail "nginx 服务未发布唯一对外端口 \${NGINX_PORT:-8080}"
if grep -qF '"${APP_PORT:-8000}:8000"' <<<"${compose_body}"; then
  fail "app 服务仍映射宿主机端口（应删除 ports，外部只能经 Nginx 访问，AC-001/INV-003）"
fi
grep -qF '127.0.0.1:${MYSQL_PORT:-3306}:3306' <<<"${compose_body}" || fail "mysql 端口未回环绑定"
grep -qF '127.0.0.1:${REDIS_PORT:-6379}:6379' <<<"${compose_body}" || fail "redis 端口未回环绑定"

# 10) docker compose 可解析（与 test-prometheus.sh / test-container.sh 一致）。
docker_compose -f "${COMPOSE_CFG}" config -q || fail "docker compose config 校验失败"

# 11) nginx -t 语法校验：复用「配置经 nginx/Dockerfile COPY 内置」的已构建镜像
#     my-shop-nginx:local 直接运行 nginx -t，不使用单文件 bind mount——
#     rootless/嵌套容器环境下单文件 bind mount 会退化为目录挂载（文件变目录），
#     导致容器启动报 "not a directory"（与 prometheus/Dockerfile 同一约束）。
if command -v docker >/dev/null 2>&1; then
  if docker image inspect my-shop-nginx:local >/dev/null 2>&1; then
    docker run --rm my-shop-nginx:local nginx -t || fail "nginx -t 语法校验失败"
  else
    log_warn "未找到已构建镜像 my-shop-nginx:local，跳过 nginx -t（先执行 docker compose build nginx，静态断言仍执行）"
  fi
else
  log_warn "未找到 docker，跳过 nginx -t（静态断言仍执行）"
fi

# 12) 统一入口一致性：压测默认入口与 README 对外入口不得回退到已移除的宿主端口 8000。
LOADTEST="${ROOT_DIR}/scripts/flashsale-loadtest/run.sh"
[[ -f "${LOADTEST}" ]] || fail "缺少 ${LOADTEST}"
if grep -qF '127.0.0.1:8000' "${LOADTEST}"; then
  fail "flashsale-loadtest/run.sh 默认 BASE_URL 仍指向已移除的宿主端口 8000（应指向 Nginx 入口）"
fi
README="${ROOT_DIR}/README.md"
[[ -f "${README}" ]] || fail "缺少 ${README}"
if grep -qF 'http://127.0.0.1:8000' "${README}"; then
  fail "README.md 仍以 http://127.0.0.1:8000 作为对外入口（应指向 Nginx 入口 ${NGINX_PORT:-8080}）"
fi

log_info "Nginx 反向代理静态校验通过（信任边界 / 重试边界 / DNS 再解析 / 监控隔离 / 日志关联 / 编排 / 统一入口）"
