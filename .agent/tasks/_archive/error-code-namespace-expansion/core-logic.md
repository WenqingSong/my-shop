# Core Logic

Owner Verification Status: ACCEPTED

本任务为纯治理/设计规则改动，无生产代码变更。唯一需要 Owner 掌握的核心机制是「域序制扩展规则」及其由 Validator 保证的「域独占不重叠」不变量。

## CL-001：域序制扩展规则与域独占校验

- Owner 需要理解：错误码域按「域序 `domain_seq = code / 1000`、区间 `[seq×1000, seq×1000+999]`」划分，下一空闲域 = `max(已记录域序)+1`。因此当前最大域序 8（`8000-8999`）之后，下一个申请任务自然派生序 9 = `9000-9999`，再下一个任务派生序 10 = `10000-10999`，不受「4 位上限 → 9 域」约束；`9000-9999` 不预留给任何假定模块，由下一个真实完成 Reservation 的 Task 获得。同时「域独占」由 `check-registry.sh` 机械保证——一旦两个域区间重叠，校验即失败，防止并行任务抢占同一域。
- 生产代码（治理规则落点）：`.agent/registry/error-codes.md`「分配规则」、`docs/agent/AgentCollaborationSpecification.md` §11.3、`docs/design/error-codes.md` §2/§3
- 关键测试：`scripts/check-registry.sh`（错误码域重叠检测 + `codes.go` 归属检测）
- 基线验证：`bash scripts/check-registry.sh` → 输出「校验通过：未发现 Reservation 重复或 Registry ↔ 实现明显不一致」，exit 0
- 可选 Mutation：在 `.agent/registry/error-codes.md` 分配表临时追加一行与现有域重叠的 RESERVED 条目（如 `| 1500-2499 | X | RESERVED | 验证 |`）
- 预期失败：`bash scripts/check-registry.sh` 必须报 `[FAIL] 错误码域区间 1500-2499（RESERVED）与 1000-1999（ACTIVE）重叠` 并 exit 1
- 恢复确认：删除该临时行，再次 `bash scripts/check-registry.sh` 恢复 exit 0，并 `git status` 确认工作区干净
