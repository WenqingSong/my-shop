#!/usr/bin/env bash
# 容器化静态断言（无需 Docker 即可运行，验证「能区分正确与错误实现」的关键性质）：
#
#   1. 凭据分离（INV-003）：config.yaml 不残留开发默认 JWT 密钥与非空 MySQL/Redis 密码；
#   2. 构建上下文（AC-002）：.dockerignore 排除 .env / bin / tmp / storage；
#   3. 镜像模型（AC-001/AC-003）：Dockerfile 多阶段、含 tzdata 时区与 ca-certificates、
#      打包 config.yaml、以 entrypoint 启动；
#   4. 启动生命周期（AC-006/AC-007）：entrypoint 先 migrate up 再 exec serve（migrate 失败非零退出、不对外）；
#   5. 非 root 运行（AC-001/INV-008）：Dockerfile 含 USER 10001:10001 与 /app chown；
#   6. runtime 层内容审计（AC-011/INV-003）：运行阶段不 COPY 源码 / .env，仅从 builder 复制编译产物；
#   7. 编排模型（AC-004/AC-005/AC-010）：compose 具备 app 服务、app_storage 卷、
#      /health 健康检查与 depends_on condition: service_healthy，且不残留 extra_hosts 宿主机映射；
#   8. T2-04 镜像构建与运行加固：基础镜像 patch 版本固定、BuildKit 缓存、可复现编译参数、
#      镜像 HEALTHCHECK 与 Compose 同值、只读根文件系统、tmpfs、cap_drop ALL、
#      no-new-privileges、非 root 用户、不暴露特权/主机命名空间/Docker Socket。
#
# 本脚本为纯静态断言，不依赖 Docker 可用；`make test` 已将其纳入统一测试入口。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

CONFIG_CFG="${ROOT_DIR}/manifest/config/config.yaml"
DOCKERIGNORE="${ROOT_DIR}/.dockerignore"
DOCKERFILE="${ROOT_DIR}/Dockerfile"
ENTRYPOINT="${ROOT_DIR}/scripts/docker-entrypoint.sh"
COMPOSE_CFG="${ROOT_DIR}/docker-compose.yml"

# ---------------------------------------------------------------------------
# 1) 凭据分离（INV-003）
# ---------------------------------------------------------------------------
[[ -f "${CONFIG_CFG}" ]] || fail "缺少 ${CONFIG_CFG}"
# 开发默认 JWT 密钥（历史 config.yaml 默认值）不得再出现在 config.yaml。
if grep -q 'c49d0fea5cf77761ca6d0275545b9501686dd40cc313b072c36972d6128fc403' "${CONFIG_CFG}"; then
  fail "config.yaml 仍残留开发默认 JWT 密钥（auth.jwt.secret 应清空为空串）"
fi
# database.default.pass 应清空（redis.default.pass 本为空）：不允许出现非空 pass 值。
if grep -Eq '^[[:space:]]*pass:[[:space:]]*"[^"]+"' "${CONFIG_CFG}"; then
  fail "config.yaml 仍残留非空 MySQL/Redis 密码（database.default.pass 应清空为空串）"
fi
# 正向确认：auth.jwt.secret 与 database.default.pass 均为空串。
grep -Eq '^[[:space:]]*secret:[[:space:]]*""' "${CONFIG_CFG}" || fail "config.yaml 的 auth.jwt.secret 未清空"
grep -Eq '^[[:space:]]*pass:[[:space:]]*""' "${CONFIG_CFG}" || fail "config.yaml 的 database.default.pass 未清空"

# ---------------------------------------------------------------------------
# 2) 构建上下文（AC-002）
# ---------------------------------------------------------------------------
[[ -f "${DOCKERIGNORE}" ]] || fail "缺少 ${DOCKERIGNORE}"
for p in '.env' 'bin/' 'tmp/' 'storage/' '.git/' 'docs/' '.agent/' '*.md' '*_test.go' \
  'test/container/.artifacts/'; do
  grep -qF "${p}" "${DOCKERIGNORE}" || fail ".dockerignore 未排除 ${p}"
done

# ---------------------------------------------------------------------------
# 3) 镜像模型（AC-001/AC-003）
# ---------------------------------------------------------------------------
[[ -f "${DOCKERFILE}" ]] || fail "缺少 ${DOCKERFILE}"
from_count="$(grep -c '^FROM ' "${DOCKERFILE}" || true)"
[[ "${from_count}" -ge 2 ]] || fail "Dockerfile 应为多阶段构建（至少两个 FROM）"
grep -q 'tzdata' "${DOCKERFILE}" || fail "Dockerfile 缺少 tzdata（Asia/Shanghai 时区语义）"
grep -q 'ca-certificates' "${DOCKERFILE}" || fail "Dockerfile 缺少 ca-certificates（七牛 HTTPS 出站）"
grep -qF 'manifest/config/config.yaml' "${DOCKERFILE}" || fail "Dockerfile 未 COPY manifest/config/config.yaml"
grep -q 'CGO_ENABLED=0' "${DOCKERFILE}" || fail "Dockerfile 未使用 CGO_ENABLED=0 静态编译"

# ---------------------------------------------------------------------------
# 4) 非 root 运行（AC-001/INV-008）
# ---------------------------------------------------------------------------
grep -qE '^USER 10001:10001$' "${DOCKERFILE}" || fail "Dockerfile 未以非 root 用户运行（应含 USER 10001:10001）"
grep -qE 'addgroup|adduser' "${DOCKERFILE}" || fail "Dockerfile 未创建专用运行用户（addgroup/adduser）"
grep -q 'chown app:app /app/storage' "${DOCKERFILE}" || fail "Dockerfile 未将 /app/storage 单独 chown 给运行用户"
if grep -q 'chown -R app:app /app' "${DOCKERFILE}"; then
  fail "Dockerfile 将整个 /app chown 给运行用户（二进制/配置应保持 root 只读，避免运行期篡改）"
fi
grep -q 'chmod 0555 /app/docker-entrypoint.sh' "${DOCKERFILE}" || fail "entrypoint 未固定为只读可执行（0555）"
grep -q 'chmod 0444 /app/manifest/config/config.yaml' "${DOCKERFILE}" || fail "镜像内配置未固定为只读（0444）"
grep -q 'HEALTHCHECK --interval=10s --timeout=5s --start-period=60s --retries=5' "${DOCKERFILE}" ||
  fail "Dockerfile 缺少与 Compose 同值的 HEALTHCHECK"
grep -q '/health' "${DOCKERFILE}" || fail "镜像 HEALTHCHECK 未使用 /health 存活语义"
if grep -qE 'apk add[^&]*\b(curl|wget|bash|psql|mysql-client)\b' "${DOCKERFILE}"; then
  fail "运行层安装了不必要的诊断/客户端工具（健康检查应使用 busybox wget）"
fi

# ---------------------------------------------------------------------------
# 5) runtime 层内容审计（AC-011/INV-003）：不含源码 / .env / 构建工具链
# ---------------------------------------------------------------------------
# 运行阶段（最后一个 FROM 之后）只能从 builder 复制二进制与静态资源，不得 COPY 源码目录或 .env。
runtime_body="$(awk '/^FROM /{n++} n>=2' "${DOCKERFILE}")"
if grep -qE 'COPY[[:space:]]+\.( |$)|(^|[[:space:]])\.env([[:space:]]|$)' <<<"${runtime_body}"; then
  fail "runtime 层不应 COPY 源码或 .env"
fi
grep -qE '^COPY --from=builder .*/out/my-shop /app/my-shop$' "${DOCKERFILE}" ||
  fail "runtime 层应从 builder 复制编译产物，而非源码"

# ---------------------------------------------------------------------------
# 5.1) 可复现构建（T2-04）
# ---------------------------------------------------------------------------
grep -q '^# syntax=docker/dockerfile:' "${DOCKERFILE}" || fail "Dockerfile 缺少 BuildKit syntax 指令（cache mount 依赖）"
grep -q 'ARG GO_IMAGE=golang:[0-9]\+\.[0-9]\+\.[0-9]\+-alpine[0-9]\+\.[0-9]\+' "${DOCKERFILE}" ||
  fail "Dockerfile 未以 patch 版本固定 Go 基础镜像（禁止 latest / 浮动 minor 标签）"
grep -q 'ARG ALPINE_IMAGE=alpine:[0-9]\+\.[0-9]\+\.[0-9]\+' "${DOCKERFILE}" ||
  fail "Dockerfile 未以 patch 版本固定运行基础镜像"
if grep -qE ':latest([[:space:]]|$)' "${DOCKERFILE}"; then
  fail "Dockerfile 使用了 latest 标签（不可复现）"
fi
for token in 'GOTOOLCHAIN=local' 'GOFLAGS=-mod=readonly' '-trimpath' '-buildvcs=false' \
  '--mount=type=cache,target=/go/pkg/mod'; do
  grep -qF -- "${token}" "${DOCKERFILE}" || fail "Dockerfile 缺少可复现构建参数 ${token}"
done
grep -q 'apk upgrade --no-cache' "${DOCKERFILE}" || fail "运行层未吸收基础镜像 patch 修订（apk upgrade）"

# ---------------------------------------------------------------------------
# 6) 启动生命周期（AC-006/AC-007）
# ---------------------------------------------------------------------------
[[ -f "${ENTRYPOINT}" ]] || fail "缺少 ${ENTRYPOINT}"
grep -q 'migrate up' "${ENTRYPOINT}" || fail "entrypoint 未执行 migrate up"
grep -q 'exec /app/my-shop serve' "${ENTRYPOINT}" || fail "entrypoint 未以 exec serve 启动服务"
migrate_line="$(grep -n 'migrate up' "${ENTRYPOINT}" | head -n 1 | cut -d: -f1)"
serve_line="$(grep -n 'exec /app/my-shop serve' "${ENTRYPOINT}" | head -n 1 | cut -d: -f1)"
if [[ -z "${migrate_line}" || -z "${serve_line}" || "${migrate_line}" -ge "${serve_line}" ]]; then
  fail "entrypoint 必须先 migrate up 再 exec serve（migrate 失败即非零退出、不对外）"
fi

# ---------------------------------------------------------------------------
# 7) 编排模型（AC-004/AC-005/AC-010）
# ---------------------------------------------------------------------------
[[ -f "${COMPOSE_CFG}" ]] || fail "缺少 ${COMPOSE_CFG}"

# 提取 app 服务的配置块（去掉注释行，避免注释文字干扰负向断言）。
app_block() {
  awk '/^  app:/{in_app=1; next} in_app && /^  [A-Za-z0-9_-]+:/{exit} in_app' "${COMPOSE_CFG}" |
    sed '/^[[:space:]]*#/d'
}
APP_BLOCK="$(app_block)"
grep -q '^  app:' "${COMPOSE_CFG}" || fail "docker-compose.yml 缺少 app 服务"
grep -q 'condition: service_healthy' "${COMPOSE_CFG}" || fail "app 服务缺少 depends_on condition: service_healthy"
grep -q 'app_storage:/app/storage' "${COMPOSE_CFG}" || fail "app 服务缺少 app_storage 持久化卷挂载"
grep -q 'http://127.0.0.1:8000/health' "${COMPOSE_CFG}" || fail "app 服务缺少 /health 健康检查"
grep -q '^  app_storage:$' "${COMPOSE_CFG}" || fail "docker-compose.yml 缺少 app_storage 卷定义"
# 仅针对实际配置键（忽略注释行）：不应出现 extra_hosts / host.docker.internal / host-gateway。
compose_body="$(sed '/^[[:space:]]*#/d' "${COMPOSE_CFG}")"
if grep -q 'host.docker.internal\|host-gateway\|extra_hosts' <<<"${compose_body}"; then
  fail "docker-compose.yml 仍残留 host.docker.internal/host-gateway/extra_hosts 宿主机映射（应经 Compose DNS 通信）"
fi
# 无内置默认回退（AC-004）：MYSQL_ROOT_PASSWORD / AUTH_JWT_SECRET 不得再出现 ${VAR:-固定默认值} 形式回退。
if grep -Eq 'MYSQL_ROOT_PASSWORD:-|AUTH_JWT_SECRET:-' <<<"${compose_body}"; then
  fail "docker-compose.yml 仍残留 MYSQL_ROOT_PASSWORD / AUTH_JWT_SECRET 的 \${VAR:-default} 回退（应使用 \${VAR:?} 必填）"
fi
# 连接字符集统一 utf8mb4（AC-006）：compose 中 DATABASE_DEFAULT_CHARSET 必须为 utf8mb4。
if grep -Eq 'DATABASE_DEFAULT_CHARSET:[[:space:]]*"utf8"([^m]|$)' <<<"${compose_body}"; then
  fail "docker-compose.yml 的 DATABASE_DEFAULT_CHARSET 仍为 utf8（应统一为 utf8mb4）"
fi

# 终止预算（T2-03）：app 必须显式声明 stop_grace_period: 60s。
# Compose 默认 10s 会在 Publisher/Consumer 关闭完成前 SIGKILL，破坏关闭顺序；
# 60s 与 manifest/config/config.yaml 的 lifecycle.termination_grace_period 及后续
# Kubernetes terminationGracePeriodSeconds 同源（Go 侧契约测试见 internal/termination）。
if ! awk '/^  app:/{in_app=1; next} in_app && /^  [A-Za-z0-9_-]+:/{exit} in_app' "${COMPOSE_CFG}" |
  grep -qE '^[[:space:]]+stop_grace_period:[[:space:]]*60s[[:space:]]*$'; then
  fail "docker-compose.yml 的 app 服务缺少 stop_grace_period: 60s（Compose 默认 10s 会提前 SIGKILL）"
fi
# 其他容器（mysql / redis / kafka / nginx / prometheus）的生命周期不受本任务影响。
if [[ "$(grep -cE '^[[:space:]]+stop_grace_period:' <<<"${compose_body}")" != "1" ]]; then
  fail "docker-compose.yml 只允许 app 服务声明 stop_grace_period（不得改变其他容器生命周期）"
fi

# ---------------------------------------------------------------------------
# 8) 容器运行安全加固（T2-04）
# ---------------------------------------------------------------------------
grep -qE '^[[:space:]]+user:[[:space:]]*"?10001:10001"?[[:space:]]*$' <<<"${APP_BLOCK}" ||
  fail "app 服务未显式固定运行用户 10001:10001"
grep -qE '^[[:space:]]+read_only:[[:space:]]*true[[:space:]]*$' <<<"${APP_BLOCK}" ||
  fail "app 服务未启用只读根文件系统（read_only: true）"
grep -qE '^[[:space:]]+- /tmp:' <<<"${APP_BLOCK}" ||
  fail "app 服务缺少 /tmp tmpfs（只读根文件系统下需要可写临时目录）"
grep -qE '^[[:space:]]+cap_drop:' <<<"${APP_BLOCK}" ||
  fail "app 服务缺少 cap_drop"
grep -qE '^[[:space:]]+- ALL[[:space:]]*$' <<<"${APP_BLOCK}" ||
  fail "app 服务的 cap_drop 未包含 ALL"
grep -q 'no-new-privileges:true' <<<"${APP_BLOCK}" ||
  fail "app 服务缺少 security_opt: no-new-privileges:true"
if grep -qE 'privileged:[[:space:]]*true|network_mode:[[:space:]]*host|pid:[[:space:]]*host|ipc:[[:space:]]*host|cap_add:|docker\.sock' <<<"${APP_BLOCK}"; then
  fail "app 服务包含不允许的特权配置（privileged/host namespace/cap_add/Docker Socket）"
fi
if grep -qE '^[[:space:]]+ports:' <<<"${APP_BLOCK}"; then
  fail "app 服务不得映射宿主机端口（唯一入口必须是 Nginx）"
fi
if grep -q '8000/ready' <<<"${APP_BLOCK}"; then
  fail "app healthcheck 不应使用 /ready（Compose 无摘流机制，DB/Redis 抖动会放大为重启）"
fi
# 健康检查参数与镜像 HEALTHCHECK 一致（避免两处漂移）。
for item in 'interval: 10s' 'timeout: 5s' 'retries: 5' 'start_period: 60s'; do
  grep -qF "${item}" <<<"${APP_BLOCK}" || fail "app healthcheck 缺少 ${item}（应与 Dockerfile HEALTHCHECK 同值）"
done
# 加固字段只作用于 app：其他服务不得被意外改写。
for field in 'read_only:' 'cap_drop:' 'security_opt:'; do
  if [[ "$(grep -cE "^[[:space:]]+${field}" <<<"${compose_body}")" != "1" ]]; then
    fail "docker-compose.yml 只允许 app 服务声明 ${field}（不得改变其他服务运行语义）"
  fi
done

# 可选：docker 可用时校验 compose 可解析（与 test-prometheus.sh 保持一致）。
# 注意：compose 已对 MYSQL_ROOT_PASSWORD / AUTH_JWT_SECRET 使用 ${VAR:?} 必填（无内置默认回退），
# 语法校验时经 lib.sh 的 docker_compose_placeholder 注入占位值（非真实凭据）以通过必填拦截；
# 缺失即失败由 ${VAR:?} 语义保证（AC-004）。
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  docker_compose_placeholder -f "${COMPOSE_CFG}" config -q || fail "docker compose config 校验失败"
fi

log_info "容器化配置静态断言通过（凭据分离 / 构建上下文 / 镜像 / 生命周期 / 编排）"
