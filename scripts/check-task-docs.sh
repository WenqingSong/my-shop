#!/usr/bin/env bash
# Task 目录文档一致性校验（只读）。
#
# 职责：校验 docs/task-management/ 的人工导航是否与真实 Task 目录、官方派生索引一致：
#   1. 目录 / 官方索引 / task-catalog 的 Task ID 集合一致，且无重复；
#   2. catalog 状态词汇合法，UNKNOWN 不被写成 DONE；
#   3. 导航文档的相对链接存在；
#   4. Mermaid 代码块成对闭合；
#   5. 索引中的 corrupt / ghost 与文件系统事实一致。
#
# 边界：不生成第二套状态，不修改任何 Task/State/Evidence；
#       索引仍以 .agent/bin/workflow-status 为官方来源，缺失时只跳过索引比对。
#
# 用法：./scripts/check-task-docs.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
DOC_DIR="${ROOT_DIR}/docs/task-management"
CATALOG="${DOC_DIR}/task-catalog.md"
TASKS_DIR="${ROOT_DIR}/.agent/tasks"
STATUS_CLI="${ROOT_DIR}/.agent/bin/workflow-status"

failures=0
fail() { printf '[FAIL] %s\n' "$*"; failures=$((failures + 1)); }
pass() { printf '[PASS] %s\n' "$*"; }
skip() { printf '[SKIP] %s\n' "$*"; }

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

for required in "${DOC_DIR}" "${CATALOG}" "${TASKS_DIR}"; do
  if [ ! -e "${required}" ]; then
    printf '[FAIL] 缺少必需路径：%s\n' "${required}"
    exit 1
  fi
done

# ---------------------------------------------------------------------------
# 1. 真实目录 vs catalog
# ---------------------------------------------------------------------------
find "${TASKS_DIR}" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | sort > "${tmpdir}/dirs.txt"
awk -F'|' '/^\| `/{ gsub(/[`[:space:]]/, "", $2); if ($2 != "") print $2 }' "${CATALOG}" \
  | sort > "${tmpdir}/catalog.txt"

dupes="$(uniq -d "${tmpdir}/catalog.txt" || true)"
if [ -n "${dupes}" ]; then
  fail "catalog 中 Task ID 重复：$(echo "${dupes}" | tr '\n' ' ')"
else
  pass "catalog Task ID 无重复"
fi

if diff -q "${tmpdir}/dirs.txt" "${tmpdir}/catalog.txt" >/dev/null; then
  pass "catalog 覆盖全部 Task 目录（$(wc -l < "${tmpdir}/dirs.txt" | tr -d ' ') 项）"
else
  fail "catalog 与 .agent/tasks 目录不一致"
  printf '  仅在目录中：\n'
  comm -23 "${tmpdir}/dirs.txt" "${tmpdir}/catalog.txt" | sed 's/^/    /'
  printf '  仅在 catalog 中：\n'
  comm -13 "${tmpdir}/dirs.txt" "${tmpdir}/catalog.txt" | sed 's/^/    /'
fi

# ---------------------------------------------------------------------------
# 2. 状态词汇与 UNKNOWN 语义
# ---------------------------------------------------------------------------
awk -F'|' '/^\| `/{ s=$4; gsub(/^[[:space:]]+|[[:space:]]+$/, "", s); print s }' "${CATALOG}" \
  | sort | uniq -c > "${tmpdir}/status.txt"

allowed="ACTIVE DONE/DOC DONE/STATE INCONSISTENT UNKNOWN"
while read -r count status; do
  case " ${allowed} " in
    *" ${status} "*) ;;
    *) fail "catalog 出现未定义状态：${status}（${count} 项）" ;;
  esac
done < "${tmpdir}/status.txt"
pass "catalog 状态词汇合法：$(tr '\n' ';' < "${tmpdir}/status.txt" | sed 's/;$//')"

if awk -F'|' '/^\| `/{ s=$4; if (s ~ /UNKNOWN/ && s ~ /DONE/) exit 1 }' "${CATALOG}"; then
  pass "UNKNOWN 未与 DONE 混用"
else
  fail "catalog 中存在同一行同时标记 UNKNOWN 与 DONE"
fi

# ---------------------------------------------------------------------------
# 3. 相对链接
# ---------------------------------------------------------------------------
link_fail=0
while IFS= read -r md; do
  while IFS= read -r target; do
    case "${target}" in
      http://*|https://*|mailto:*|'#'*) continue ;;
    esac
    path="${target%%#*}"
    [ -z "${path}" ] && continue
    case "${path}" in
      /*) resolved="${path}" ;;
      *) resolved="$(dirname "${md}")/${path}" ;;
    esac
    if [ ! -e "${resolved}" ]; then
      fail "失效链接：${md#${ROOT_DIR}/} -> ${target}"
      link_fail=$((link_fail + 1))
    fi
  done < <(grep -oE '\]\([^)]+\)' "${md}" | sed -E 's/^\]\(//; s/\)$//')
done < <(find "${DOC_DIR}" -type f -name '*.md' | sort)

if [ "${link_fail}" -eq 0 ]; then
  pass "docs/task-management 相对链接全部存在"
fi

# ---------------------------------------------------------------------------
# 4. Mermaid 代码块
# ---------------------------------------------------------------------------
if awk '
  /^```mermaid[[:space:]]*$/ { if (open) { print "嵌套 mermaid"; exit 1 } open=1; next }
  /^```[[:space:]]*$/ { if (!open) { print "多余的围栏"; exit 1 } open=0; next }
  END { if (open) { print "mermaid 未闭合"; exit 1 } }
' "${DOC_DIR}/task-relationships.md"; then
  pass "Mermaid 代码块闭合"
else
  fail "Mermaid 代码块语法/闭合异常"
fi

# ---------------------------------------------------------------------------
# 5. 官方索引比对（可选，只读）
# ---------------------------------------------------------------------------
if [ -x "${STATUS_CLI}" ]; then
  if "${STATUS_CLI}" index > "${tmpdir}/index.tsv" 2>"${tmpdir}/index.err"; then
    awk -F'\t' 'NR>2 && $1 !~ /^#/ { print $1 }' "${tmpdir}/index.tsv" | sort > "${tmpdir}/index-ids.txt"
    if diff -q "${tmpdir}/dirs.txt" "${tmpdir}/index-ids.txt" >/dev/null; then
      pass "官方索引覆盖全部 Task（$(wc -l < "${tmpdir}/index-ids.txt" | tr -d ' ') 项）"
    else
      fail "官方索引与真实目录不一致"
      diff -u "${tmpdir}/dirs.txt" "${tmpdir}/index-ids.txt" | sed 's/^/    /' || true
    fi

    ghost_count="$(awk -F'\t' 'NR>2 && $20 ~ /ghost/ { n++ } END { print n+0 }' "${tmpdir}/index.tsv")"
    if [ "${ghost_count}" -eq 0 ]; then
      pass "官方索引无 Ghost Task"
    else
      fail "官方索引含 ${ghost_count} 个 Ghost Task"
    fi

    missing=0
    schema0=0
    while IFS= read -r dir; do
      state="${TASKS_DIR}/${dir}/state.yaml"
      if [ ! -f "${state}" ]; then
        missing=$((missing + 1))
      elif ! grep -q '^schema_version:' "${state}"; then
        schema0=$((schema0 + 1))
      fi
    done < "${tmpdir}/dirs.txt"

    index_corrupt="$(awk -F'\t' 'NR>2 && $20 == "corrupt" { n++ } END { print n+0 }' "${tmpdir}/index.tsv")"
    expected_corrupt=$((missing + schema0))
    if [ "${index_corrupt}" -eq "${expected_corrupt}" ]; then
      pass "索引 corrupt=${index_corrupt} 与文件系统一致（缺失 State ${missing} + 无 schema_version ${schema0}）"
    else
      fail "索引 corrupt=${index_corrupt} 与文件系统预期 ${expected_corrupt} 不一致"
    fi
  else
    fail "workflow-status index 执行失败：$(cat "${tmpdir}/index.err")"
  fi
else
  skip "未找到 .agent/bin/workflow-status；跳过官方索引比对（构建方法见 docs/task-management/README.md）"
fi

# ---------------------------------------------------------------------------
# 汇总
# ---------------------------------------------------------------------------
total="$(wc -l < "${tmpdir}/catalog.txt" | tr -d ' ')"
printf '\nTask 目录校验：%s 项，%s 个失败\n' "${total}" "${failures}"
if [ "${failures}" -ne 0 ]; then
  exit 1
fi
printf '全部检查通过。\n'
