#!/usr/bin/env bash
# 全局资源预留 Registry 只读校验器（Global Resource Reservation Registry Validator）。
#
# 职责：仅做机械检查，给出确定 pass/fail：
#   1. 错误码域 Reservation 是否重复（含区间重叠）；
#   2. Migration Version Reservation 是否重复；
#   3. Registry 与 internal/codes/codes.go、internal/migrations/sql/* 实际使用是否明显不一致。
#
# 边界：不分配资源、不修改 Registry、不替代 Analyst/Cleaner 的语义判断；
#       最终 Registry ↔ Contract ↔ 实现 的三边一致性仍由 Cleaner 审查。
# 用法：./scripts/check-registry.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

ERROR_CODES_REG="${ROOT_DIR}/.agent/registry/error-codes.md"
MIGRATIONS_REG="${ROOT_DIR}/.agent/registry/migrations.md"
CODES_GO="${ROOT_DIR}/internal/codes/codes.go"
MIGRATIONS_DIR="${ROOT_DIR}/internal/migrations/sql"

failures=0

fail() { printf '[FAIL] %s\n' "$*"; failures=$((failures+1)); }

# ---------------------------------------------------------------------------
# 提取函数：把 Registry 表格数据行解析成「空格分隔字段」逐行输出。
# 表格列：| 第1列 | 第2列 | 状态列 | 备注 |，状态固定在第 4 列（awk $4）。
# ---------------------------------------------------------------------------

# error-codes.md：输出 "start end status"（仅含合法区间 + 状态的表格行）。
# 两种 Registry 表格列数不同（error-codes 4 列、migrations 5 列），
# 因此不依赖列位置，改为扫描整行字段识别「区间」与「状态」。
_error_domain_rows() {
  awk -F'[|]' '
    /^[[:space:]]*\|/ {
      range=""; status="";
      for (i=1; i<=NF; i++) {
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", $i);
        if ($i ~ /^[0-9]+-[0-9]+$/) range=$i;
        else if ($i=="ACTIVE" || $i=="RESERVED" || $i=="RELEASED") status=$i;
      }
      if (range!="" && status!="") { split(range, r, "-"); print r[1], r[2], status; }
    }
  ' "${ERROR_CODES_REG}"
}

# migrations.md：输出 "version status"（仅含合法数字 + 状态的表格行）。
_migration_rows() {
  awk -F'[|]' '
    /^[[:space:]]*\|/ {
      version=""; status="";
      for (i=1; i<=NF; i++) {
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", $i);
        if ($i ~ /^[0-9]+$/) version=$i;
        else if ($i=="ACTIVE" || $i=="RESERVED" || $i=="RELEASED") status=$i;
      }
      if (version!="" && status!="") print version, status;
    }
  ' "${MIGRATIONS_REG}"
}

# ---------------------------------------------------------------------------
# 检查 1：错误码域重复（RESERVED / ACTIVE 区间两两不得重叠）。
# ---------------------------------------------------------------------------
check_error_overlap() {
  local line s1 e1 st1 s2 e2 st2 i
  local -a starts=() ends=() stats=()
  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    read -r s1 e1 st1 <<<"$line"
    [[ "$st1" == "ACTIVE" || "$st1" == "RESERVED" ]] || continue
    for i in "${!starts[@]}"; do
      s2="${starts[$i]}"; e2="${ends[$i]}"; st2="${stats[$i]}"
      if (( s1 <= e2 && s2 <= e1 )); then
        fail "错误码域区间 $s1-$e1（$st1）与 $s2-$e2（$st2）重叠"
      fi
    done
    starts+=("$s1"); ends+=("$e1"); stats+=("$st1")
  done < <(_error_domain_rows)
}

# ---------------------------------------------------------------------------
# 检查 2：migration version 重复（任意状态都不得重复）。
# ---------------------------------------------------------------------------
check_migration_dup() {
  local dup v
  dup="$(_migration_rows | awk '{print $1}' | sort | uniq -d)"
  if [[ -n "$dup" ]]; then
    while IFS= read -r v; do
      [[ -n "$v" ]] && fail "migration version $v 重复"
    done <<<"$dup"
  fi
}

# ---------------------------------------------------------------------------
# 检查 3a：错误码域 ↔ codes.go 漂移。codes.go 每个非 0 错误码必须落在某个 ACTIVE 域内；
#   落在 RESERVED 域内的错误码为「已预留、尚未合并进 develop」的预期中间态，仅提示、不算漂移；
#   既不落 ACTIVE 也不落 RESERVED 域（含 RELEASED 域）才判为漂移。
# ---------------------------------------------------------------------------
check_error_drift() {
  local line s e st code i covered reserved
  local -a astarts=() aends=() rstarts=() rends=()
  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    read -r s e st <<<"$line"
    if [[ "$st" == "ACTIVE" ]]; then
      astarts+=("$s"); aends+=("$e")
    elif [[ "$st" == "RESERVED" ]]; then
      rstarts+=("$s"); rends+=("$e")
    fi
  done < <(_error_domain_rows)

  if [[ ${#astarts[@]} -eq 0 ]]; then
    fail "Registry 中无 ACTIVE 错误码域，无法校验 codes.go"
    return
  fi

  while IFS= read -r code; do
    [[ -n "$code" ]] || continue
    [[ "$code" == "0" ]] && continue   # CodeOK 不属于任何域
    covered=0
    for i in "${!astarts[@]}"; do
      if (( code >= astarts[i] && code <= aends[i] )); then
        covered=1
        break
      fi
    done
    if (( covered == 1 )); then
      continue
    fi
    # 未落入 ACTIVE 域：落入 RESERVED 域为「已预留、未合并」，属预期中间态，仅提示。
    reserved=0
    for i in "${!rstarts[@]}"; do
      if (( code >= rstarts[i] && code <= rends[i] )); then
        reserved=1
        break
      fi
    done
    if (( reserved == 1 )); then
      printf '[INFO] 错误码 %s 在 RESERVED 域内（已预留、未合并进 develop，非漂移）\n' "$code"
      continue
    fi
    fail "错误码 $code 不在任何 ACTIVE/RESERVED 域内（Registry ↔ codes.go 不一致）"
  done < <(grep -oE '=[[:space:]]*[0-9]+' "$CODES_GO" | grep -oE '[0-9]+')
}

# ---------------------------------------------------------------------------
# 检查 3b：migration version ↔ sql 文件漂移。
#   - 每个 .up.sql 文件的 version 必须已在 Registry 登记（任意状态）；
#   - Registry 中每个 ACTIVE version 必须有对应迁移文件。
# ---------------------------------------------------------------------------
check_migration_drift() {
  local line v st f fname fver vv
  local -A reg_by_version=()
  local -A file_versions=()
  while IFS= read -r line; do
    [[ -n "$line" ]] || continue
    read -r v st <<<"$line"
    reg_by_version["$v"]="$st"
  done < <(_migration_rows)

  for f in "$MIGRATIONS_DIR"/*.up.sql; do
    [[ -e "$f" ]] || continue
    fname="$(basename "$f")"
    fver="${fname%%_*}"
    file_versions["$fver"]=1
    if [[ -z "${reg_by_version[$fver]:-}" ]]; then
      fail "迁移文件 $fname 的 version $fver 未在 Registry 登记"
    fi
  done

  for vv in "${!reg_by_version[@]}"; do
    if [[ "${reg_by_version[$vv]}" == "ACTIVE" && -z "${file_versions[$vv]:-}" ]]; then
      fail "Registry 中 ACTIVE version $vv 无对应迁移文件"
    fi
  done
}

# ---------------------------------------------------------------------------
# 主流程
# ---------------------------------------------------------------------------
main() {
  printf '检查 Registry 文件与实现事实：\n'
  printf '  error-codes : %s\n' "${ERROR_CODES_REG}"
  printf '  migrations  : %s\n' "${MIGRATIONS_REG}"
  printf '  codes.go    : %s\n' "${CODES_GO}"
  printf '  sql dir     : %s\n\n' "${MIGRATIONS_DIR}"

  [[ -f "$ERROR_CODES_REG" ]] || fail "缺少 ${ERROR_CODES_REG}"
  [[ -f "$MIGRATIONS_REG" ]] || fail "缺少 ${MIGRATIONS_REG}"
  [[ -f "$CODES_GO" ]] || fail "缺少 ${CODES_GO}"

  if [[ -f "$ERROR_CODES_REG" ]]; then
    check_error_overlap
    check_error_drift
  fi
  if [[ -f "$MIGRATIONS_REG" ]]; then
    check_migration_dup
    check_migration_drift
  fi

  echo
  if (( failures > 0 )); then
    printf '校验失败：共 %d 处不一致\n' "$failures"
    exit 1
  fi
  printf '校验通过：未发现 Reservation 重复或 Registry ↔ 实现明显不一致\n'
}

main "$@"
