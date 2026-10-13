#!/usr/bin/env bash
# 构建发布镜像并打标签（短 SHA + latest）。
#
# 镜像名由 CNB 内置变量派生（CNB_DOCKER_REGISTRY / CNB_REPO_SLUG_LOWERCASE），
# 不在脚本内写死仓库路径与短 SHA。
# 先构建短 SHA 标签，再对同一镜像打 latest 标签，保证双标签同源（同一 digest）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

log_info() { printf '\033[32m[INFO]\033[0m %s\n' "$*"; }

: "${CNB_DOCKER_REGISTRY:?CNB_DOCKER_REGISTRY required}"
: "${CNB_REPO_SLUG_LOWERCASE:?CNB_REPO_SLUG_LOWERCASE required}"
: "${CNB_COMMIT_SHORT:?CNB_COMMIT_SHORT required}"

image="${CNB_DOCKER_REGISTRY}/${CNB_REPO_SLUG_LOWERCASE}"

log_info "docker build -t ${image}:${CNB_COMMIT_SHORT} ."
( cd "${ROOT_DIR}" && docker build -t "${image}:${CNB_COMMIT_SHORT}" . )

log_info "docker tag ${image}:${CNB_COMMIT_SHORT} ${image}:latest"
docker tag "${image}:${CNB_COMMIT_SHORT}" "${image}:latest"

log_info "docker-build complete"
