#!/bin/sh
# 容器 entrypoint：先执行数据库迁移，再启动 HTTP 服务。
#
# - `migrate up` 幂等：已全部应用时成功返回；golang-migrate 的 MySQL driver
#   通过 GET_LOCK 串行化并发迁移，配合 schema_migrations 追踪表避免重复应用。
# - 迁移失败（依赖未就绪连接失败 / dirty / 确定性失败）→ 非零退出，
#   容器不对外提供服务，由 compose `restart: unless-stopped` 重试（dirty 需人工 `migrate force`）。
# - `exec serve`：由 serve 自身 fail-fast（依赖连通重试 → schema 就绪只读校验 →
#   超管/权限 seed → 七牛 ValidateConfig），不在此重复预检。
set -e

echo "[entrypoint] 执行数据库迁移（幂等，并发由 golang-migrate GET_LOCK 串行化）..."
/app/my-shop migrate up

echo "[entrypoint] 启动 HTTP 服务..."
exec /app/my-shop serve
