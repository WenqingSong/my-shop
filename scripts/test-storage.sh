#!/usr/bin/env bash
# 独立真实存储 E2E 入口（make test-storage）：走真实 HTTP 链路
# 「注册 → 登录 → 签发 token → 直传真实 1×1 PNG → 校验 final_url → 删除测试对象」。
# 缺真实七牛凭据或任一环节失败 → 非零退出（NOT_VERIFIED 以非零退出表达，不以退出 0 冒充通过）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

# 校验四个七牛必填环境变量非空（AK/SK/Bucket/Domain），缺任一即明确报错退出。
missing=0
for var in QINIU_ACCESS_KEY QINIU_SECRET_KEY QINIU_BUCKET QINIU_DOMAIN; do
  if [[ -z "${!var:-}" ]]; then
    log_error "缺少环境变量 ${var}：请先执行 make init，在 .env 中填入真实七牛配置后再执行 make test-storage"
    missing=1
  fi
done
if [[ "${missing}" -eq 1 ]]; then
  exit 1
fi

log_info "确保依赖容器（MySQL、Redis）就绪..."
docker_compose up -d mysql redis
wait_for_deps

log_info "运行真实存储 E2E（签发 token → 直传真实 1×1 PNG → 校验 final_url → 删除测试对象）..."
( cd "${ROOT_DIR}" && go test -tags storage_e2e -run TestStorageE2E -v ./internal/cmd/... )
