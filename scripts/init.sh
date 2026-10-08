#!/usr/bin/env bash
# 两阶段初始化第一阶段（make init）：准备本地依赖容器与 .env。
# 本阶段不要求七牛凭据、不启动 Go 后端、不在终端交互输入 Secret。
# 幂等：不覆盖已有 .env、不破坏已运行的容器与已有数据，可重复执行。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_info "检查本地依赖（Docker / Docker Compose）..."
if ! command -v docker >/dev/null 2>&1; then
  log_error "未找到 docker，请先安装 Docker"
  exit 1
fi
# docker_compose 封装内部会校验 docker compose / docker-compose 可用性，不可用时自动报错退出。
docker_compose version >/dev/null 2>&1

log_info "启动依赖容器（MySQL、Redis）..."
docker_compose up -d mysql redis
wait_for_deps

ENV_FILE="${ROOT_DIR}/.env"
ENV_EXAMPLE="${ROOT_DIR}/.env.example"
if [[ -f "${ENV_FILE}" ]]; then
  log_info "检测到已有 ${ENV_FILE}，保留不覆盖（如需重置请手动删除后重跑）"
else
  if [[ -f "${ENV_EXAMPLE}" ]]; then
    cp "${ENV_EXAMPLE}" "${ENV_FILE}"
    log_info "已从 .env.example 生成 ${ENV_FILE}"
  else
    log_warn "未找到 .env.example，跳过生成 .env"
  fi
fi

cat <<EOF

============================================================
第一阶段完成：依赖容器已就绪，.env 已就绪（若首次创建）。
下一步（编辑 .env 填入七牛云真实配置后执行 make up）：

  1. 编辑 ${ENV_FILE}，填写：
       QINIU_ACCESS_KEY=<你的 AccessKey>
       QINIU_SECRET_KEY=<你的 SecretKey>
       QINIU_BUCKET=<对象存储空间名>
       QINIU_DOMAIN=<对外访问域名，如 https://cdn.example.com>
  2. 执行 make up 完成第二阶段启动（会校验七牛配置与 bucket 可用性）

注意：AK/SK 为机密，仅保存在本地 .env，绝不提交仓库。
============================================================

EOF
