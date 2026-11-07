#!/usr/bin/env bash
# T2-06.1 发布脚本行为门禁（不需要真实集群）。
#
# 与 test-k8s-manifest.sh 的分工：
#   - test-k8s-manifest.sh：证明「清单构建产物正确」（Kustomize 构建 + 结构化断言 + Schema）；
#   - 本脚本：证明「发布脚本的语义正确」—— 真正执行 scripts/deploy-k8s.sh，
#     用 fake kubectl 观察调用顺序、状态变化与退出码。
#
# 覆盖范围（详见 test/deployk8s/）：
#   - 发布锁：首次创建、并发竞争、已被占用、过期接管、续租、续租失败、
#     旧持有者不释放新锁、正常释放、SIGTERM 释放、崩溃后过期接管；
#   - 迁移：缺失/完成/失败/运行中/超时/Pod 仍在运行/失败后应用不更新；
#   - 版本保护：首发布/同版本/升级/降级/分叉/无法识别/显式授权/CI 禁止；
#   - 发布成功判定：镜像/版本标记/generation/副本/Pod/被他人改写；
#   - 故障处理：kubectl 缺失、集群不可达、权限不足、apply 失败、SIGTERM、临时文件清理。
#
# 明确边界：
#   - fake kubectl 只能证明脚本行为，**不能**代替真实 API Server 的并发/etcd 语义验证；
#   - 真实集群上的 Lease 竞争、RBAC、rollout 行为仍为 NOT_VERIFIED
#     （见 docs/development/kubernetes.md §14）。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

cd "${ROOT_DIR}"

# 语法错误会让所有行为用例一起失真：先做一次解析检查（不作为行为验证的替代）。
log_info "bash -n scripts/deploy-k8s.sh"
bash -n "${SCRIPT_DIR}/deploy-k8s.sh" || fail "scripts/deploy-k8s.sh 存在语法错误"

log_info "发布脚本行为测试（真实执行脚本 + fake kubectl）..."
go test -count=1 -v ./test/deployk8s/

log_info "发布脚本行为测试全部通过（真实集群语义仍未验证：NOT_VERIFIED）"
