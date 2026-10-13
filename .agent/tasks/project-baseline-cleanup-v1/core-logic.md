# Owner 核心逻辑验证

## 结论：本任务无生产代码核心逻辑

`project-baseline-cleanup-v1` 是纯「工程配置、文档与全局资源登记」的一致性收敛任务，未改动任何生产代码、数据库、API、错误码或 migration。因此不存在「决定权限、安全、事务、库存、金额、幂等、状态流转或一致性」的代码逻辑，本文件不建立核心逻辑验证卡。

## 唯一需要 Owner 知悉的治理语义（非运行时逻辑）

本任务唯一有「机制」意义的是 Registry 资源状态翻转的语义正确性，供 Owner 快速理解但无需 Mutation 验证：

- **Owner 需要理解**：`migrations.md` / `error-codes.md` 中 00009–00023、10000–19999 由 `RESERVED` 翻转为 `ACTIVE` 的依据是「对应 feature 已合并进 `develop` 且迁移文件/错误码已实际存在并使用」，而非主观判断。`scripts/check-registry.sh` 只做重复/重叠/漂移的机械校验，**不校验** `RESERVED → ACTIVE` 的语义正确性，故语义正确性由 git 合并历史 + 代码实际使用逐条核实。
- **核实方式（证据）**：
  - `git ls-tree -r 359b8cc --name-only` 确认 23 个 `.up.sql` 与各 feature 代码均已在基线。
  - `internal/codes/codes.go` 确认 10000–19999 各域错误码实际使用。
  - `./scripts/check-registry.sh` 退出码 0 佐证无重叠/漂移。
- **无需 Mutation**：该语义不存在可逆破坏后能由测试捕获的业务不变量（纯文档/登记状态）。

## 交付边界说明

- 废弃目录/脚本（`docs/tasks/`、`docs/学习/`、`scripts/import-legacy.sh`）经引用核验后全部保留，保留原因已留痕于 README「历史遗留目录与脚本保留说明」。
- 本任务不占用任何新的全局资源（`resources.reservations` 为空），未新增/复用任何 migration version 或错误码域。
