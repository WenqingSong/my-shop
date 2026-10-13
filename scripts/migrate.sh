#!/usr/bin/env bash
# 数据库 migration 运维入口：运行统一构建产物 bin/my-shop migrate <up|version|force [version]>。
#
# - 复用 make build 的统一产物（bin/my-shop）；缺失时自动构建（原生目标，与 Dockerfile 同参）。
# - 依赖 lib.sh 加载 .env：数据库连接凭据（DATABASE_DEFAULT_PASS 回退到 MYSQL_ROOT_PASSWORD，
#   与 app 容器一致，root 凭据同一来源）。
# - 在仓库根目录运行，保证 GoFrame 默认在 CWD 下查找 manifest/config/config.yaml。
#
# 用法：
#   make migrate-up                             应用所有未执行 migration
#   make migrate-version                        查看当前版本与 dirty 状态
#   make migrate-force VERSION=<14位时间戳>     标记版本已应用（baseline/dirty 恢复，不执行 SQL）
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

BUILD_DIR="${BUILD_DIR:-bin}"
BINARY="${BINARY:-my-shop}"
BIN="${ROOT_DIR}/${BUILD_DIR}/${BINARY}"

# 先校验子命令与参数，再检查环境与构建产物，保证参数错误能给出明确提示。
sub="${1:-}"
case "${sub}" in
  up|version) ;;
  force) ;;
  *) log_error "用法：make migrate-{up|version|force}（force 需 VERSION=<14位时间戳>）"; exit 1 ;;
esac

version=""
if [[ "${sub}" == "force" ]]; then
  version="${2:-}"
  if [[ -z "${version}" ]]; then
    log_error "缺少 version 参数：make migrate-force VERSION=<14位时间戳>"
    exit 1
  fi
fi

# 本地直连 compose 映射的 MySQL（127.0.0.1:3306）：app 以 root 连接，密码与 MySQL root 凭据同源。
# 未显式设置 DATABASE_DEFAULT_PASS 时回退到 .env 的 MYSQL_ROOT_PASSWORD（用户已配置的凭据，非内置默认值）。
export DATABASE_DEFAULT_PASS="${DATABASE_DEFAULT_PASS:-${MYSQL_ROOT_PASSWORD:-}}"

if [[ -z "${DATABASE_DEFAULT_PASS:-}" ]]; then
  log_error "数据库密码未配置：请先执行 make init 并填写 .env 的 MYSQL_ROOT_PASSWORD（或显式导出 DATABASE_DEFAULT_PASS）"
  exit 1
fi

if [[ ! -x "${BIN}" ]]; then
  log_info "未找到 ${BIN}，先执行统一构建..."
  bash "${SCRIPT_DIR}/build.sh"
fi

cd "${ROOT_DIR}"
if [[ "${sub}" == "force" ]]; then
  "${BIN}" migrate force "${version}"
else
  "${BIN}" migrate "${sub}"
fi
