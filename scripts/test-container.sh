#!/usr/bin/env bash
# 容器化静态断言（无需 Docker 即可运行，验证「能区分正确与错误实现」的关键性质）：
#
#   1. 凭据分离（INV-003）：config.yaml 不残留开发默认 JWT 密钥与非空 MySQL/Redis 密码；
#   2. 构建上下文（AC-002）：.dockerignore 排除 .env / bin / tmp / storage；
#   3. 镜像模型（AC-001/AC-003）：Dockerfile 多阶段、含 tzdata 时区与 ca-certificates、
#      打包 config.yaml、以 entrypoint 启动；
#   4. 启动生命周期（AC-006/AC-007）：entrypoint 先 migrate up 再 exec serve（migrate 失败非零退出、不对外）；
#   5. 编排模型（AC-004/AC-005/AC-010）：compose 具备 app 服务、app_storage 卷、
#      /health 健康检查与 depends_on condition: service_healthy，且不残留 extra_hosts 宿主机映射。
#
# 本脚本为纯静态断言，不依赖 Docker 可用；`make test` 已将其纳入统一测试入口。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

CONFIG_CFG="${ROOT_DIR}/manifest/config/config.yaml"
DOCKERIGNORE="${ROOT_DIR}/.dockerignore"
DOCKERFILE="${ROOT_DIR}/Dockerfile"
ENTRYPOINT="${ROOT_DIR}/scripts/docker-entrypoint.sh"
COMPOSE_CFG="${ROOT_DIR}/docker-compose.yml"

fail() { printf '\033[31m[ERROR]\033[0m %s\n' "$*" >&2; exit 1; }

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
for p in '.env' 'bin/' 'tmp/' 'storage/'; do
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
# 4) 启动生命周期（AC-006/AC-007）
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
# 5) 编排模型（AC-004/AC-005/AC-010）
# ---------------------------------------------------------------------------
[[ -f "${COMPOSE_CFG}" ]] || fail "缺少 ${COMPOSE_CFG}"
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

# 可选：docker 可用时校验 compose 可解析（与 test-prometheus.sh 保持一致）。
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  docker compose -f "${COMPOSE_CFG}" config -q || fail "docker compose config 校验失败"
fi

printf '\033[32m[INFO]\033[0m 容器化配置静态断言通过（凭据分离 / 构建上下文 / 镜像 / 生命周期 / 编排）\n'
