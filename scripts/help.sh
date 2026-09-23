#!/usr/bin/env bash
# 输出所有生命周期目标及简要说明（单一数据源：Makefile 中的 ## 注释）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
makefile="${SCRIPT_DIR}/../Makefile"

printf '\033[1mmy-shop 生命周期命令\033[0m\n\n'
awk -F ':[^#]*## ' '/^[a-zA-Z_-]+:[^#]*## / {
  printf "  \033[36m%-10s\033[0m %s\n", $1, $2
}' "${makefile}"
printf '\n用法：make <目标>\n'
