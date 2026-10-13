#!/usr/bin/env bash
# 推送发布镜像（短 SHA 标签 + latest）。
#
# 先推短 SHA 标签，再推 latest；任一 push 失败即非零退出，latest 不被更新。
# 制品库认证使用 CNB 内置凭据（CNB_TOKEN 自动注入），无需手动 docker login。
set -euo pipefail

log_info() { printf '\033[32m[INFO]\033[0m %s\n' "$*"; }

: "${CNB_DOCKER_REGISTRY:?CNB_DOCKER_REGISTRY required}"
: "${CNB_REPO_SLUG_LOWERCASE:?CNB_REPO_SLUG_LOWERCASE required}"
: "${CNB_COMMIT_SHORT:?CNB_COMMIT_SHORT required}"

image="${CNB_DOCKER_REGISTRY}/${CNB_REPO_SLUG_LOWERCASE}"

log_info "docker push ${image}:${CNB_COMMIT_SHORT}"
docker push "${image}:${CNB_COMMIT_SHORT}"

log_info "docker push ${image}:latest"
docker push "${image}:latest"

log_info "docker-push complete"
