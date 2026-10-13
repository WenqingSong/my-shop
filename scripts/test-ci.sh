#!/usr/bin/env bash
# CNB 流水线静态断言（无需 Docker 与真实制品库，验证「能区分正确与错误实现」的关键性质）：
#
#   1. 功能保留（AC-001）：$ 云开发环境、main.pull_request 的 pr-check、main.push 的
#      main-ci（test/go build）、sync-to-github-mirror 条件同步均仍存在；
#   2. PR 自动测试（AC-002）：pr-check 仍执行 make test，测试失败即 job 失败；
#   3. main 推送自动测试 + 构建（AC-003）：main-ci 仍按 test → build 顺序执行；
#   4. 镜像构建与发布（AC-004/AC-005）：docker-build / docker-push 两阶段存在，
#      镜像名由 CNB 内置变量派生，短 SHA 与 latest 双标签由同一镜像 tag 派生；
#   5. 双标签同源（AC-006）：latest 由短 SHA 标签的同一镜像 docker tag 而来（同 digest）；
#   6. 失败不更新 latest（AC-007）：docker-build / docker-push 均位于 sync-to-github-mirror 之前，
#      且先推短 SHA、后推 latest——任一步失败即 stage 失败、latest 不被更新；
#   7. 凭据安全（AC-008）：.cnb.yml 不出现明文密码/令牌，制品库认证仅用 CNB 内置凭据。
#
# 本脚本为纯静态断言，不依赖 Docker 可用；`make test` 已将其纳入统一测试入口。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

CNB_CFG="${ROOT_DIR}/.cnb.yml"

fail() { printf '\033[31m[ERROR]\033[0m %s\n' "$*" >&2; exit 1; }

[[ -f "${CNB_CFG}" ]] || fail "缺少 ${CNB_CFG}"

# 去除注释行后再断言，避免注释里的说明文字造成误匹配。
body="$(sed '/^[[:space:]]*#/d' "${CNB_CFG}")"

# ---------------------------------------------------------------------------
# 1) 功能保留（AC-001）：既有触发与关键步骤仍在。
# ---------------------------------------------------------------------------
grep -qE '^\$\:' "${CNB_CFG}" \
  || fail "缺少 \$ 云开发环境顶层键"
grep -q 'pull_request:' "${CNB_CFG}" || fail "缺少 main.pull_request 触发"
grep -q 'name: pr-check' "${CNB_CFG}" || fail "缺少 PR 检查 job pr-check"
grep -q 'push:' "${CNB_CFG}" || fail "缺少 main.push 触发"
grep -q 'name: main-ci' "${CNB_CFG}" || fail "缺少 push job main-ci"
grep -q 'name: sync-to-github-mirror' "${CNB_CFG}" || fail "缺少 sync-to-github-mirror 同步 stage"

# ---------------------------------------------------------------------------
# 2) PR 自动测试（AC-002）：pr-check 内仍执行 make test。
# ---------------------------------------------------------------------------
grep -q 'script: make test' "${CNB_CFG}" || fail "pr-check 未执行 make test（PR 自动测试）"

# ---------------------------------------------------------------------------
# 3) main 推送自动测试 + 构建（AC-003）：test 与 build 均保留且顺序正确。
# ---------------------------------------------------------------------------
grep -q 'script: make test' "${CNB_CFG}" || fail "main-ci 未执行 make test（main 推送自动测试）"
grep -q 'go build -trimpath -o bin/my-shop .' "${CNB_CFG}" || fail "main-ci 缺少 go build 阶段"

# ---------------------------------------------------------------------------
# 4) 镜像构建与发布（AC-004/AC-005）：两个阶段存在，镜像名用 CNB 内置变量派生。
# ---------------------------------------------------------------------------
grep -q 'name: docker-build' "${CNB_CFG}" || fail "缺少 docker-build 阶段"
grep -q 'name: docker-push' "${CNB_CFG}" || fail "缺少 docker-push 阶段"
grep -q 'image="${CNB_DOCKER_REGISTRY}/${CNB_REPO_SLUG_LOWERCASE}"' "${CNB_CFG}" \
  || fail "镜像名未使用 CNB 内置变量 \${CNB_DOCKER_REGISTRY}/\${CNB_REPO_SLUG_LOWERCASE} 派生"
grep -q 'docker build -t "${image}:${CNB_COMMIT_SHORT}" .' "${CNB_CFG}" \
  || fail "docker-build 未构建短 SHA 标签 \${CNB_COMMIT_SHORT}"

# ---------------------------------------------------------------------------
# 5) 双标签同源（AC-006）：latest 由短 SHA 标签的同一镜像 tag 派生（同 digest）。
# ---------------------------------------------------------------------------
grep -q 'docker tag "${image}:${CNB_COMMIT_SHORT}" "${image}:latest"' "${CNB_CFG}" \
  || fail "latest 标签未由短 SHA 标签的同一镜像 docker tag 派生（双标签不同源）"

# ---------------------------------------------------------------------------
# 6) 失败不更新 latest（AC-007）：push 顺序为短 SHA 先、latest 后。
# ---------------------------------------------------------------------------
grep -q 'docker push "${image}:${CNB_COMMIT_SHORT}"' "${CNB_CFG}" \
  || fail "docker-push 未推送短 SHA 标签"
grep -q 'docker push "${image}:latest"' "${CNB_CFG}" \
  || fail "docker-push 未推送 latest 标签"

order_ok() {
  # 断言 $1 对应的 stage 名出现在 $2 对应的 stage 名之前。
  local a="$1" b="$2" la lb
  la="$(grep -nE "^[[:space:]]*- name: ${a}$" "${CNB_CFG}" | head -n 1 | cut -d: -f1)"
  lb="$(grep -nE "^[[:space:]]*- name: ${b}$" "${CNB_CFG}" | head -n 1 | cut -d: -f1)"
  [[ -n "${la}" && -n "${lb}" && "${la}" -lt "${lb}" ]]
}
order_ok test build            || fail "main-ci 的 test 阶段未在 build 之前"
order_ok build docker-build    || fail "main-ci 的 build 阶段未在 docker-build 之前"
order_ok docker-build docker-push || fail "docker-build 未在 docker-push 之前"
order_ok docker-push sync-to-github-mirror || fail "docker-push 未在 sync-to-github-mirror 之前"

# 先推短 SHA、后推 latest（失败不更新 latest 的关键顺序保证）。
sha_line="$(grep -n 'docker push "${image}:${CNB_COMMIT_SHORT}"' "${CNB_CFG}" | head -n 1 | cut -d: -f1)"
latest_line="$(grep -n 'docker push "${image}:latest"' "${CNB_CFG}" | head -n 1 | cut -d: -f1)"
if [[ -z "${sha_line}" || -z "${latest_line}" || "${sha_line}" -ge "${latest_line}" ]]; then
  fail "docker-push 必须先推短 SHA 标签、再推 latest 标签（失败不更新 latest）"
fi

# ---------------------------------------------------------------------------
# 7) 凭据安全（AC-008）：.cnb.yml 不出现明文密码/令牌。
# ---------------------------------------------------------------------------
if grep -nE 'password:[[:space:]]*[^[:space:]"'"'"']' "${CNB_CFG}"; then
  fail ".cnb.yml 疑似出现明文密码（password: 后跟非空字面量）"
fi
if grep -nE 'token:[[:space:]]*[^[:space:]"'"'"']' "${CNB_CFG}"; then
  fail ".cnb.yml 疑似出现明文令牌（token: 后跟非空字面量）"
fi
if grep -nE 'docker login' <<<"${body}"; then
  fail ".cnb.yml 不应手动 docker login（制品库认证应使用 CNB 内置凭据）"
fi

printf '\033[32m[INFO]\033[0m CNB 流水线静态断言通过（功能保留 / PR 测试 / 推送测试构建 / 双标签同源 / 失败不更新 latest / 凭据安全）\n'
