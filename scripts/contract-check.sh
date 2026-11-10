#!/usr/bin/env bash
# =============================================================================
# B1-02-04 契约自动化校验
#
# 目标：让 OpenAPI 不再是一份只能人工阅读的静态文档，而是具备自动回归能力的工程资产。
#
# 检查项：
#   1. OpenAPI 3.1 语法与结构
#   2. Spectral lint（OAS 规则）
#   3. $ref 引用完整性（含外部组件文件）
#   4. 生成结果一致性（DTO/codes.go → openapi 无漂移）
#   5. operationId 唯一性
#   6. 路由 ↔ OpenAPI 双向比对（真实 GoFrame 路由表）
#   7. DTO 字段 ↔ Schema 双向比对
#   8. 错误码注册表 ↔ codes.go
#   9. 权限码 ↔ 路由 ↔ seed
#  10. planned 接口与实际路由分离
#  11. 未引用组件治理（可达性 + 允许保留清单）
#  12. 主文件 Paths 与分域 Paths 结构化一致
#  13. 跨包错误码补充登记表与代码事实一致
#
# 环境缺失的检查项一律输出 NOT RUN，绝不伪造 PASS。
# =============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

GENERATED=(
  "openapi"
  "docs/phase2/B1-02_DTO_SCHEMA_INVENTORY.md"
)

declare -a NOT_RUN=()
declare -a FAILED=()

log()  { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
ok()   { printf '  \033[32mPASS\033[0m  %s\n' "$*"; }
skip() { printf '  \033[33mNOT RUN\033[0m  %s\n' "$*"; NOT_RUN+=("$*"); }
bad()  { printf '  \033[31mFAIL\033[0m  %s\n' "$*"; FAILED+=("$*"); }

hash_generated() {
  # 对生成物做内容指纹，用于判断生成器是否重写了已提交文件。
  find "${GENERATED[@]}" -type f -print0 2>/dev/null \
    | sort -z \
    | xargs -0 sha256sum 2>/dev/null \
    | sha256sum \
    | awk '{print $1}'
}

# ---------------------------------------------------------------------------
log "0. 前置条件"
# ---------------------------------------------------------------------------
command -v go >/dev/null 2>&1 && ok "go: $(go version)" || { bad "缺少 go"; exit 1; }
command -v git >/dev/null 2>&1 && ok "git: $(git --version)" || bad "缺少 git（生成一致性检查降级）"

# ---------------------------------------------------------------------------
log "1. 生成结果一致性（生成前 → 生成 → 生成后）"
# ---------------------------------------------------------------------------
BEFORE_HASH="$(hash_generated)"
go run ./tools/openapi-gen -repo . >/tmp/openapi-gen.log 2>&1 || {
  cat /tmp/openapi-gen.log
  bad "openapi-gen 执行失败"
}
AFTER_HASH="$(hash_generated)"
if [[ "${BEFORE_HASH}" == "${AFTER_HASH}" ]]; then
  ok "生成器输出与仓库中的契约文件完全一致（无漂移）"
else
  bad "生成器重写了已提交的契约文件：存在手工修改或未提交的 DTO 变更，请检查后提交生成结果"
fi

# ---------------------------------------------------------------------------
log "2. 契约结构与一致性（go run ./tools/openapi-gen -check）"
# ---------------------------------------------------------------------------
CHECK_LOG="$(mktemp)"
if go run ./tools/openapi-gen -repo . -check >"${CHECK_LOG}" 2>&1; then
  cat "${CHECK_LOG}"
  ok "OpenAPI 3.1 版本/结构、\$ref 完整性、operationId 唯一性"
  ok "DTO ↔ Schema、错误码 ↔ codes.go、权限码 ↔ 路由 ↔ seed 双向一致"
  ok "planned 接口与实际路由分离"
  ok "未引用组件可达性 + 允许保留清单（stale/已引用/新未引用均失败）"
  ok "主文件 Paths 与分域 Paths 逐字段一致（含重复/缺失/内容漂移）"
  ok "跨包错误码补充登记与代码事实一致"
else
  cat "${CHECK_LOG}"
  bad "契约结构与一致性检查未通过"
fi
rm -f "${CHECK_LOG}"

# ---------------------------------------------------------------------------
log "3. 路由 ↔ OpenAPI 双向比对（真实 GoFrame 路由表）"
# ---------------------------------------------------------------------------
ROUTE_LOG="$(mktemp)"
if go test ./internal/cmd/ -run 'TestRoute|TestContractPlannedNotRouted' -count=1 >"${ROUTE_LOG}" 2>&1; then
  ok "142 条常驻路由与契约双向一致；planned 接口未被实现"
else
  cat "${ROUTE_LOG}"
  bad "路由与契约比对失败"
fi
rm -f "${ROUTE_LOG}"

# ---------------------------------------------------------------------------
log "4. Spectral lint"
# ---------------------------------------------------------------------------
SPECTRAL_RULESET="openapi/.spectral.yaml"
if ! command -v npx >/dev/null 2>&1; then
  skip "Spectral lint（缺少 npx）"
elif ! npx --yes @stoplight/spectral-cli@6 --version >/dev/null 2>&1; then
  skip "Spectral lint（无法获取 @stoplight/spectral-cli，通常是离线环境）"
else
  SPECTRAL_LOG="$(mktemp)"
  if npx --yes @stoplight/spectral-cli@6 lint \
      openapi/surgecart.openapi.yaml \
      --ruleset "${SPECTRAL_RULESET}" >"${SPECTRAL_LOG}" 2>&1; then
    ok "Spectral 无 error 级问题"
  else
    cat "${SPECTRAL_LOG}"
    bad "Spectral lint 未通过"
  fi
  rm -f "${SPECTRAL_LOG}"
fi

# ---------------------------------------------------------------------------
log "5. 生成器单元测试"
# ---------------------------------------------------------------------------
if go test ./tools/openapi-gen/ -count=1 >/tmp/openapi-gen-test.log 2>&1; then
  ok "tools/openapi-gen 单元测试通过"
else
  cat /tmp/openapi-gen-test.log
  bad "生成器单元测试失败"
fi

# ---------------------------------------------------------------------------
log "结果汇总"
# ---------------------------------------------------------------------------
if ((${#NOT_RUN[@]} > 0)); then
  printf '\nNOT RUN（环境缺失，未伪造通过）:\n'
  printf '  - %s\n' "${NOT_RUN[@]}"
fi

if ((${#FAILED[@]} > 0)); then
  printf '\n\033[31m契约检查失败（%d 项）\033[0m:\n' "${#FAILED[@]}"
  printf '  - %s\n' "${FAILED[@]}"
  exit 1
fi

printf '\n\033[32m契约检查全部通过\033[0m（NOT RUN %d 项）\n' "${#NOT_RUN[@]}"
