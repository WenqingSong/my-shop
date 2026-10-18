#!/usr/bin/env bash
# Task 目录文档一致性校验（只读）。
#
# 职责：校验 docs/task-management/ 的人工导航是否与真实 Task 分类布局、官方派生索引一致：
#   1. 目录 / 布局清单 / 官方索引 / task-catalog 的 Task ID 集合一致，且无重复；
#   2. catalog 状态词汇合法，UNKNOWN 不被写成 DONE，状态与 layout 分类映射一致；
#   3. 没有遗留的旧平铺 Task 目录（legacy-layout 必须为 0）；
#   4. 导航文档的相对链接存在；
#   5. Mermaid 代码块成对闭合；
#   6. 索引中的 corrupt / ghost 与文件系统事实一致。
#
# 边界：不生成第二套状态，不修改任何 Task/State/Evidence，不自动迁移目录；
#       布局与索引仍以 .agent/bin/workflow-status 为官方来源，缺失时只跳过对应比对。
#
# 用法：./scripts/check-task-docs.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
DOC_DIR="${ROOT_DIR}/docs/task-management"
CATALOG="${DOC_DIR}/task-catalog.md"
TASKS_DIR="${ROOT_DIR}/.agent/tasks"
LAYOUT_FILE="${TASKS_DIR}/layout.yaml"
STATUS_CLI="${ROOT_DIR}/.agent/bin/workflow-status"

failures=0
fail() { printf '[FAIL] %s\n' "$*"; failures=$((failures + 1)); }
pass() { printf '[PASS] %s\n' "$*"; }
skip() { printf '[SKIP] %s\n' "$*"; }

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

for required in "${DOC_DIR}" "${CATALOG}" "${TASKS_DIR}" "${LAYOUT_FILE}"; do
  if [ ! -e "${required}" ]; then
    printf '[FAIL] 缺少必需路径：%s\n' "${required}"
    exit 1
  fi
done

# ---------------------------------------------------------------------------
# 1. 真实目录 vs 布局清单 vs catalog
# ---------------------------------------------------------------------------
if [ -x "${STATUS_CLI}" ]; then
  # 官方布局视图：task_id / category / basis / path / legacy
  "${STATUS_CLI}" layout list > "${tmpdir}/layout.tsv" 2>"${tmpdir}/layout.err" || {
    fail "workflow-status layout list 执行失败：$(cat "${tmpdir}/layout.err")"
    exit 1
  }
  awk -F'\t' 'NR>2 && $1 !~ /^#/ && $1 != "" { print $1 }' "${tmpdir}/layout.tsv" | sort > "${tmpdir}/dirs.txt"
  awk -F'\t' 'NR>2 && $1 !~ /^#/ && $1 != "" { print $1"\t"$2"\t"$4"\t"$5 }' "${tmpdir}/layout.tsv" > "${tmpdir}/layout-rows.tsv"
else
  skip "未找到 .agent/bin/workflow-status；改用文件系统发现（跳过分类/索引比对）"
  find "${TASKS_DIR}" -mindepth 1 -maxdepth 2 -type d -printf '%f\n' \
    | grep -vE '^(active|completed|blocked|archive)$' | sort > "${tmpdir}/dirs.txt"
  : > "${tmpdir}/layout-rows.tsv"
fi

awk -F'|' '/^\| `/{ gsub(/[`[:space:]]/, "", $2); if ($2 != "") print $2 }' "${CATALOG}" \
  | sort > "${tmpdir}/catalog.txt"

dupes="$(uniq -d "${tmpdir}/dirs.txt" || true)"
if [ -n "${dupes}" ]; then
  fail "Task 目录中 ID 重复：$(echo "${dupes}" | tr '\n' ' ')"
else
  pass "Task 目录 ID 无重复（$(wc -l < "${tmpdir}/dirs.txt" | tr -d ' ') 项）"
fi

if diff -q "${tmpdir}/dirs.txt" "${tmpdir}/catalog.txt" >/dev/null; then
  pass "catalog 覆盖全部 Task 目录（$(wc -l < "${tmpdir}/dirs.txt" | tr -d ' ') 项）"
else
  fail "catalog 与 Task 目录不一致"
  printf '  仅在目录中：\n'
  comm -23 "${tmpdir}/dirs.txt" "${tmpdir}/catalog.txt" | sed 's/^/    /'
  printf '  仅在 catalog 中：\n'
  comm -13 "${tmpdir}/dirs.txt" "${tmpdir}/catalog.txt" | sed 's/^/    /'
fi

# ---------------------------------------------------------------------------
# 2. 状态词汇、UNKNOWN 语义与分类映射
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

# 状态 → 允许的分类（与 README §3 映射一致）
if [ -s "${tmpdir}/layout-rows.tsv" ]; then
  mapping_fail=0
  while IFS='|' read -r _ domain status _rest; do
    id="$(echo "${domain}" | sed -E 's/.*`([^`]+)`.*/\1/')"
    st="$(echo "${status}" | tr -d '[:space:]')"
    row="$(awk -F'\t' -v id="${id}" '$1==id{print $2"\t"$3"\t"$4; exit}' "${tmpdir}/layout-rows.tsv")"
    [ -z "${row}" ] && continue
    cat="$(echo "${row}" | cut -f1)"
    legacy_flag="$(echo "${row}" | cut -f3)"
    case "${st}" in
      DONE/STATE|DONE/DOC) expected="completed" ;;
      ACTIVE) expected="active|blocked" ;;
      INCONSISTENT) expected="blocked|archive" ;;
      UNKNOWN) expected="archive|blocked" ;;
      *) continue ;;
    esac
    if ! echo "${expected}" | tr '|' '\n' | grep -qx "${cat}"; then
      fail "分类与状态不一致：${id} 状态=${st} 目录=${cat}（允许 ${expected}）"
      mapping_fail=$((mapping_fail + 1))
    fi
    if [ "${legacy_flag}" = "true" ]; then
      fail "Task ${id} 仍在旧平铺目录（legacy-layout）"
      mapping_fail=$((mapping_fail + 1))
    fi
  done < <(grep '^| `' "${CATALOG}")
  if [ "${mapping_fail}" -eq 0 ]; then
    pass "catalog 状态与 layout 分类映射一致，且无旧平铺目录"
  fi
else
  skip "无 layout 视图；跳过状态与分类映射比对"
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

    unclassified="$(awk -F'\t' 'NR>2 && $1 !~ /^#/ && ($22=="" || $22=="-") { n++ } END { print n+0 }' "${tmpdir}/index.tsv")"
    if [ "${unclassified}" -eq 0 ]; then
      pass "官方索引全部 Task 带分类"
    else
      fail "官方索引含 ${unclassified} 个未分类 Task"
    fi

    missing=0
    schema0=0
    while IFS= read -r dir; do
      rel="$(awk -F'\t' -v id="${dir}" '$1==id{print $3; exit}' "${tmpdir}/layout-rows.tsv")"
      [ -z "${rel}" ] && rel=".agent/tasks/${dir}"
      state="${ROOT_DIR}/${rel}/state.yaml"
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
