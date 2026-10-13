#!/usr/bin/env bash
# lib.sh 加载语义回归测试（INV-001 / AC-003）：
# 验证 _load_env_file 的「空值不导出」「非空值导出」「首尾引号剥离」「行内注释剥离」「已导出环境变量优先」。
# 这是 AC-003（.env 空值不覆盖 config.yaml 默认值）的核心修复回归测试：
# 若有人把「空值跳过」逻辑改回「无条件导出」，本测试会失败。
# 直接运行：make test-lib（或 bash scripts/test-lib.sh）
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 构造受控 .env（使用独立键名，避免与真实 .env 键冲突）。
tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

cat > "${tmpdir}/.env" <<'EOF'
LIB_TEST_EMPTY=
LIB_TEST_SET=hello
LIB_TEST_QUOTED="  spaced  "
LIB_TEST_COMMENT=value  # 行内注释应被剥离
EOF

# 场景 1：空值不导出 / 非空值导出 / 引号剥离 / 注释剥离。
(
  source "${SCRIPT_DIR}/lib.sh"
  _load_env_file "${tmpdir}/.env"

  # 空值不导出：键应处于「未设置」状态（用 ${var+x} 区分「未设置」与「空值」）。
  if [[ -n "${LIB_TEST_EMPTY+x}" ]]; then
    echo "FAIL: LIB_TEST_EMPTY 不应被导出（值=${LIB_TEST_EMPTY:-<empty>}）" >&2
    exit 1
  fi

  [[ "${LIB_TEST_SET:-}" == "hello" ]] || { echo "FAIL: LIB_TEST_SET=${LIB_TEST_SET:-<unset>}" >&2; exit 1; }
  [[ "${LIB_TEST_QUOTED:-}" == "  spaced  " ]] || { echo "FAIL: LIB_TEST_QUOTED=${LIB_TEST_QUOTED:-<unset>}" >&2; exit 1; }
  [[ "${LIB_TEST_COMMENT:-}" == "value" ]] || { echo "FAIL: LIB_TEST_COMMENT=${LIB_TEST_COMMENT:-<unset>}" >&2; exit 1; }
)

# 场景 2：已导出环境变量优先于 .env 非空值。
(
  source "${SCRIPT_DIR}/lib.sh"
  export LIB_TEST_SET=pre
  _load_env_file "${tmpdir}/.env"
  [[ "${LIB_TEST_SET:-}" == "pre" ]] || { echo "FAIL: 已导出环境变量应优先，实际=${LIB_TEST_SET:-<unset>}" >&2; exit 1; }
)

echo "test-lib.sh 全部通过"
