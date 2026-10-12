#!/usr/bin/env bash
# 把归档的历史 SQL（storage/private/legacy/shopping.sql）导入隔离 staging 库（my_shop_staging）。
# 只作用于 staging 库：shopping.sql 头部含 SET FOREIGN_KEY_CHECKS=0，仅允许在此隔离库生效（AC-002）。
# 幂等：重复执行重建 staging 库数据（先确保库存在，SQL 内 DROP TABLE IF EXISTS 负责重建）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

STAGING_DB="${STAGING_DB:-my_shop_staging}"
SQL_FILE="${ROOT_DIR}/storage/private/legacy/shopping.sql"
MYSQL_ROOT_PASSWORD="${MYSQL_ROOT_PASSWORD:-root}"

if [[ ! -f "${SQL_FILE}" ]]; then
  log_error "未找到 ${SQL_FILE}，请先将历史 SQL 归档到该路径（见 docs/legacy-data.md §5）"
  exit 1
fi

# 等待 MySQL 就绪（staging 库与目标库同实例）。
wait_for_deps

log_info "创建隔离 staging 库 ${STAGING_DB}（若不存在）..."
docker_compose exec -T mysql mysql -uroot -p"${MYSQL_ROOT_PASSWORD}" \
  -e "CREATE DATABASE IF NOT EXISTS \`${STAGING_DB}\` DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"

log_info "导入 ${SQL_FILE} 到 staging 库 ${STAGING_DB} ..."
docker_compose exec -T mysql mysql -uroot -p"${MYSQL_ROOT_PASSWORD}" "${STAGING_DB}" < "${SQL_FILE}"

log_info "历史数据已导入 staging 库 ${STAGING_DB}，可执行 `my-shop legacy-migrate dry-run` 校验"
