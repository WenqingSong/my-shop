#!/usr/bin/env bash
# 展示 Nginx / App / MySQL / Redis / Prometheus 当前运行状态。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

docker_compose ps
