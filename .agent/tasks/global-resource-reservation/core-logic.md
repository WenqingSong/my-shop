# Core Logic

Owner Verification Status: PENDING

## CL-001：全局资源一致性校验器（`scripts/check-registry.sh`）

- Owner 需要理解：本任务唯一的可执行防线是只读校验器，它机械检查「错误码域区间重叠」「migration version 重复」「Registry ↔ `codes.go`/迁移文件 明显漂移」。若它缺失或失效，两个并行 Task 抢占同一错误码域/同一 migration version 会直到 merge 时才暴露（即本次 Address+Cart 事故的重演）。
- 生产代码：`scripts/check-registry.sh`（`check_error_overlap` / `check_migration_dup` / `check_error_drift` / `check_migration_drift`）。
- 关键测试：无 Go 单测（shell 工具），以直接运行 + 可逆突变验证替代；语义层漂移（Contract↔实现）由 Cleaner 三边核对负责。
- 基线验证：`bash scripts/check-registry.sh` → 输出「校验通过：未发现 Reservation 重复或 Registry ↔ 实现明显不一致」，exit 0。
- 可选 Mutation：在 `.agent/registry/error-codes.md` 末尾追加一行与既有 ACTIVE 域重叠的 RESERVED 条目，例如 `| 8500-9499 | probe | RESERVED | 突变 |`（与 cart 8000-8999 重叠）。
- 预期失败：脚本报 `[FAIL] 错误码域区间 8500-9499（RESERVED）与 8000-8999（ACTIVE）重叠` 并 exit 1。
- 恢复确认：`git checkout -- .agent/registry/error-codes.md`，再运行 `bash scripts/check-registry.sh` 恢复通过（exit 0）。
