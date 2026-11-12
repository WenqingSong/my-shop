#!/usr/bin/env bash
# Nginx 生产路由行为测试（隔离回显上游，不依赖真实 Go / MySQL / Redis）。
#
# 为什么需要这个脚本：
#   nginx -t 只校验语法，test-nginx.sh 只做静态断言；两者都无法证明
#   「/api/products 到上游时到底变成了什么 URI」。路径前缀的剥离/保留完全由
#   proxy_pass 尾部斜杠决定，写错时配置依然合法、前端依然 200（SPA fallback），
#   故障只在真实 API 调用时暴露。本脚本用真实 Nginx 进程 + 回显上游验证线上契约。
#
# 校验范围：
#   1. /api 前缀剥离：/api/products?page=1 → /products?page=1（Query String 保留）；
#   2. /api/admin/login → /admin/login；
#   3. Authorization 请求头完整透传（未被清除/改写）；
#   4. 单跳信任边界：客户端伪造的 X-Forwarded-For / X-Request-ID 被覆盖；
#   5. /storage/ 原样透传（/storage/banners/test.png 不被剥离、不进入 SPA fallback）；
#   6. React 首页 / 与深层路由 /products/1 返回 React 入口，/assets/* 命中静态资源；
#   7. Vue 首页 /console/ 与深层路由 /console/order/list 返回 Vue 入口，/console/static/* 命中；
#   8. /console → 301 /console/；
#   9. /health 透传上游；
#  10. /metrics 维持公共入口 404（不转发到上游）；
#  11. 安全响应头（nosniff / DENY）与 server_tokens off 未因新增 location 退化。
#
# 实现约束：
#   - 不用单文件 bind mount（rootless/嵌套容器下会退化为目录挂载），配置与占位静态文件
#     统一用 docker cp 放入临时容器；
#   - 不向宿主机发布端口：请求经 `docker exec ... nc` 在容器内发起（沙箱环境不一定允许
#     宿主端口转发，容器内回环是稳定可达的），因此脚本不占用任何宿主端口；
#   - 退出时删除全部临时容器与网络（trap）。
#
# 用法：
#   bash scripts/test-nginx-routing.sh                 # 测试 nginx/nginx.conf
#   bash scripts/test-nginx-routing.sh <conf 路径>     # 测试指定配置（如修复前的旧配置）
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

NGINX_CONF="${1:-${ROOT_DIR}/nginx/nginx.conf}"
IMAGE="${NGINX_TEST_IMAGE:-nginx:1.27.3-alpine}"
NET="surgecart-r1-routing-net"
UP_NAME="surgecart-r1-routing-upstream"
WEB_NAME="surgecart-r1-routing-web"
TMP_DIR="$(mktemp -d)"

PASS=0
FAILED=0
STATUS=""
BODY=""
HEADERS=""

cleanup() {
  docker rm -f "${UP_NAME}" "${WEB_NAME}" >/dev/null 2>&1 || true
  docker network rm "${NET}" >/dev/null 2>&1 || true
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

ok() {
  PASS=$((PASS + 1))
  log_info "PASS  $*"
}

ng() {
  FAILED=$((FAILED + 1))
  log_error "FAIL  $*"
}

expect_status() { # 期望状态码 说明
  if [[ "${STATUS}" == "$1" ]]; then
    ok "$2（status=${STATUS}）"
  else
    ng "$2：期望 status=$1，实际 status=${STATUS}"
  fi
}

expect_body_has() { # 期望子串 说明
  if [[ "${BODY}" == *"$1"* ]]; then
    ok "$2（body=${BODY}）"
  else
    ng "$2：期望响应体包含 '$1'，实际 body=${BODY}"
  fi
}

expect_body_lacks() { # 禁止子串 说明
  if [[ "${BODY}" != *"$1"* ]]; then
    ok "$2（body 不含 '$1'）"
  else
    ng "$2：响应体不应包含 '$1'，实际 body=${BODY}"
  fi
}

expect_header() { # 头名 期望值 说明
  local actual
  actual="$(grep -i "^$1:" <<<"${HEADERS}" | head -1 | sed -E "s/^[^:]+:[[:space:]]*//")"
  if [[ "${actual}" == "$2" ]]; then
    ok "$3（$1: ${actual}）"
  else
    ng "$3：期望 $1: $2，实际 '$1: ${actual}'"
  fi
}

# 在容器内发起原始 HTTP/1.1 请求，避免依赖宿主机端口映射与容器内 curl。
raw_http() { # 方法 路径 [请求头...]
  local method="$1" path="$2" h
  shift 2
  local req="${method} ${path} HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n"
  for h in "$@"; do
    req+="${h}\r\n"
  done
  req+="\r\n"

  local raw attempt
  raw=""
  # docker exec -i 偶发提前关闭 stdin（容器内 nc 收不到完整请求），重试而非误报失败。
  for attempt in 1 2 3; do
    raw="$(printf '%b' "${req}" \
      | timeout 10 docker exec -i "${WEB_NAME}" nc 127.0.0.1 80 \
      | tr -d '\r' || true)"
    [[ -n "$(sed -n '1s/^HTTP\/1\.[01] \([0-9][0-9][0-9]\).*/\1/p' <<<"${raw}")" ]] && break
    sleep 0.2
  done

  STATUS="$(sed -n '1s/^HTTP\/1\.[01] \([0-9][0-9][0-9]\).*/\1/p' <<<"${raw}")"
  HEADERS="$(sed -n '1,/^$/p' <<<"${raw}")"
  BODY="$(sed -e '1,/^$/d' <<<"${raw}")"
}

# ---------------------------------------------------------------------------
# 前置检查
# ---------------------------------------------------------------------------
[[ -f "${NGINX_CONF}" ]] || fail "缺少配置文件 ${NGINX_CONF}"
command -v docker >/dev/null 2>&1 || fail "未找到 docker，无法进行路由行为测试"

NGINX_CONF="$(cd "$(dirname "${NGINX_CONF}")" && pwd)/$(basename "${NGINX_CONF}")"
log_info "被测配置：${NGINX_CONF}"

if ! docker image inspect "${IMAGE}" >/dev/null 2>&1; then
  log_warn "本地缺少镜像 ${IMAGE}，尝试拉取"
  docker pull "${IMAGE}" >/dev/null || fail "无法获取镜像 ${IMAGE}（无网络时请先手动准备）"
fi

# 清理同名残留（上次异常退出），再创建隔离网络。
docker rm -f "${UP_NAME}" "${WEB_NAME}" >/dev/null 2>&1 || true
docker network rm "${NET}" >/dev/null 2>&1 || true
docker network create "${NET}" >/dev/null
log_info "已创建隔离网络 ${NET}"

# ---------------------------------------------------------------------------
# 回显上游：把收到的 method / 原始 URI / 关键请求头写进响应体
# ---------------------------------------------------------------------------
cat > "${TMP_DIR}/upstream.conf" <<'EOF'
server {
    listen 8000;
    server_name _;
    default_type text/plain;
    location / {
        return 200 "method=$request_method uri=$request_uri auth=$http_authorization xff=$http_x_forwarded_for xri=$http_x_request_id host=$host";
    }
}
EOF

docker create --name "${UP_NAME}" --network "${NET}" --network-alias app "${IMAGE}" >/dev/null
docker cp "${TMP_DIR}/upstream.conf" "${UP_NAME}:/etc/nginx/conf.d/default.conf"
docker start "${UP_NAME}" >/dev/null

# ---------------------------------------------------------------------------
# 被测 Nginx：内置 nginx.conf + 最小占位静态站点（模拟 web/dist 与 admin/dist）
# ---------------------------------------------------------------------------
mkdir -p "${TMP_DIR}/html/web/assets" "${TMP_DIR}/html/console/static"
printf 'WEB_SPA_INDEX' > "${TMP_DIR}/html/web/index.html"
printf 'WEB_ASSET_JS' > "${TMP_DIR}/html/web/assets/app.js"
printf 'CONSOLE_SPA_INDEX' > "${TMP_DIR}/html/console/index.html"
printf 'CONSOLE_ASSET_JS' > "${TMP_DIR}/html/console/static/app.js"

docker create --name "${WEB_NAME}" --network "${NET}" "${IMAGE}" >/dev/null
docker cp "${NGINX_CONF}" "${WEB_NAME}:/etc/nginx/nginx.conf"
docker cp "${TMP_DIR}/html/web/." "${WEB_NAME}:/usr/share/nginx/html/web"
docker cp "${TMP_DIR}/html/console/." "${WEB_NAME}:/usr/share/nginx/html/console"
docker start "${WEB_NAME}" >/dev/null

# 等待 Nginx 就绪；进程退出（配置非法）时打印日志并失败。
ready=false
for _ in $(seq 1 50); do
  if [[ "$(docker inspect --format '{{.State.Status}}' "${WEB_NAME}")" != "running" ]]; then
    docker logs "${WEB_NAME}" 2>&1 | tail -20 >&2 || true
    fail "被测 Nginx 容器启动失败（配置可能非法）"
  fi
  if printf 'GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n' \
    | timeout 5 docker exec -i "${WEB_NAME}" nc 127.0.0.1 80 2>/dev/null | grep -q 'HTTP/1.1 200'; then
    ready=true
    break
  fi
  sleep 0.2
done
[[ "${ready}" == "true" ]] || {
  docker logs "${WEB_NAME}" 2>&1 | tail -20 >&2 || true
  fail "被测 Nginx 在 10s 内未就绪"
}
log_info "被测 Nginx 已就绪（容器内回环 127.0.0.1:80）"

# ---------------------------------------------------------------------------
# 1) React 用户商城
# ---------------------------------------------------------------------------
raw_http GET /
expect_status 200 "React 首页 /"
expect_body_has "WEB_SPA_INDEX" "React 首页 / 返回 React 入口"

raw_http GET /products/1
expect_status 200 "React 深层路由 /products/1"
expect_body_has "WEB_SPA_INDEX" "/products/1 走 SPA fallback 至 React 入口"

raw_http GET /assets/app.js
expect_status 200 "React 静态资源 /assets/app.js"
expect_body_has "WEB_ASSET_JS" "/assets/* 命中真实静态文件"

# ---------------------------------------------------------------------------
# 2) Vue 管理后台
# ---------------------------------------------------------------------------
raw_http GET /console/
expect_status 200 "Vue 首页 /console/"
expect_body_has "CONSOLE_SPA_INDEX" "/console/ 返回 Vue 入口"

raw_http GET /console/order/list
expect_status 200 "Vue 深层路由 /console/order/list"
expect_body_has "CONSOLE_SPA_INDEX" "/console/order/list 走 SPA fallback 至 Vue 入口"

raw_http GET /console/static/app.js
expect_status 200 "Vue 静态资源 /console/static/app.js"
expect_body_has "CONSOLE_ASSET_JS" "/console/static/* 命中真实静态文件"

raw_http GET /console
expect_status 301 "/console 重定向"
expect_header Location /console/ "/console 重定向目标为 /console/"

# ---------------------------------------------------------------------------
# 3) API 前缀剥离
# ---------------------------------------------------------------------------
raw_http GET "/api/products?page=1"
expect_status 200 "/api/products?page=1"
expect_body_has "uri=/products?page=1" "上游收到 /products?page=1（前缀已剥离、Query 保留）"
expect_body_lacks "/api/" "上游未收到 /api/ 前缀"

raw_http POST /api/login 'Authorization: Bearer r1-test-token'
expect_status 200 "POST /api/login"
expect_body_has "method=POST uri=/login" "上游收到 POST /login"
expect_body_has "auth=Bearer r1-test-token" "Authorization 请求头完整透传"

raw_http POST /api/admin/login 'Authorization: Bearer r1-admin-token'
expect_status 200 "POST /api/admin/login"
expect_body_has "uri=/admin/login" "上游收到 /admin/login"

raw_http GET "/api/products?page=2&keyword=abc&sort=price" 'Authorization: Bearer r1-q-token'
expect_body_has "uri=/products?page=2&keyword=abc&sort=price" "多参数 Query String 完整保留"
expect_body_has "auth=Bearer r1-q-token" "带 Query 时 Authorization 仍透传"

# ---------------------------------------------------------------------------
# 4) 信任边界：覆盖客户端伪造的转发头
# ---------------------------------------------------------------------------
raw_http GET /api/products 'X-Forwarded-For: 203.0.113.9' 'X-Request-ID: spoofed-request-id'
expect_body_lacks "203.0.113.9" "客户端伪造的 X-Forwarded-For 被丢弃"
expect_body_has "xff=127.0.0.1" "上游 X-Forwarded-For 为直连地址"
expect_body_lacks "spoofed-request-id" "客户端伪造的 X-Request-ID 被覆盖"
if grep -Eq 'xri=[0-9a-f]{32}' <<<"${BODY}"; then
  ok "上游 X-Request-ID 为 Nginx 重新生成的 32 位十六进制"
else
  ng "上游 X-Request-ID 不是 Nginx 生成值：body=${BODY}"
fi

# ---------------------------------------------------------------------------
# 5) storage 静态资源代理
# ---------------------------------------------------------------------------
raw_http GET /storage/banners/test.png
expect_status 200 "/storage/banners/test.png"
expect_body_has "uri=/storage/banners/test.png" "上游收到原始 URI /storage/banners/test.png（前缀未被剥离）"
expect_body_lacks "WEB_SPA_INDEX" "/storage/ 未进入 React SPA fallback"

raw_http GET /storage/banners/banner-1.png
expect_body_has "uri=/storage/banners/banner-1.png" "storage 二级文件名原样透传"

# ---------------------------------------------------------------------------
# 6) 健康检查与监控隔离
# ---------------------------------------------------------------------------
raw_http GET /health
expect_status 200 "/health"
expect_body_has "uri=/health" "/health 透传到上游"

raw_http GET /metrics
expect_status 404 "/metrics 公共入口"
expect_body_lacks "uri=/metrics" "/metrics 未转发到上游"

# ---------------------------------------------------------------------------
# 7) 安全响应头与版本隐藏
# ---------------------------------------------------------------------------
raw_http GET /
expect_header X-Content-Type-Options nosniff "静态响应带 X-Content-Type-Options"
expect_header X-Frame-Options DENY "静态响应带 X-Frame-Options"

raw_http GET /api/products
expect_header X-Content-Type-Options nosniff "代理响应带 X-Content-Type-Options"
expect_header X-Frame-Options DENY "代理响应带 X-Frame-Options"

raw_http GET /storage/banners/test.png
expect_header X-Content-Type-Options nosniff "storage 响应带 X-Content-Type-Options"

if grep -qx 'Server: nginx' <<<"${HEADERS}"; then
  ok "Server 响应头不暴露 Nginx 版本（Server: nginx）"
else
  ng "Server 响应头不符合预期：$(grep -i '^server:' <<<"${HEADERS}" | head -1)"
fi

# ---------------------------------------------------------------------------
# 汇总
# ---------------------------------------------------------------------------
if (( FAILED > 0 )); then
  log_error "Nginx 路由行为测试失败：${FAILED} 项不通过，${PASS} 项通过"
  exit 1
fi
log_info "Nginx 路由行为测试通过：${PASS} 项断言全部通过"
