#!/usr/bin/env bash
# 查看应用与依赖容器日志。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

docker_compose logs --tail=100 app mysql redis prometheus || true
