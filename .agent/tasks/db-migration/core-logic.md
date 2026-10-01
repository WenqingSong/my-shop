# Core Logic

以下验证卡帮助 Owner 在不逐行 Review 全部 Diff 的情况下，通过「代码位置 → 业务后果 → 测试失败」的因果关系，亲自验证本任务真正决定正确性的两处核心机制。

## CL-001：Migration 幂等按序 + 失败 fail-fast / dirty 人工恢复

- Owner 需要理解：迁移必须按 version 升序各执行一次、已应用版本绝不重跑；一旦某迁移失败立即中止并置 `dirty`，后续 `up` 拒绝执行，只能人工 `force` 恢复。若吞掉失败或自动 force，会导致表结构错乱与版本记录不一致（例如空库上 `users` 只建了一半却记为已迁移）。
- 生产代码：`internal/migrations/migrations.go:54-70`（`Up`：`ErrNoChange`→nil、其余 error 包装返回）、`:74-86`（`Force`：标记版本、不执行 SQL）
- 关键测试：`internal/migrations/migrations_test.go` 的 `TestUpCreatesSchemaAndIsIdempotent`（幂等）、`TestUpAppliesOnlyPendingMigration`（增量）、`TestUpFailsFastAndMarksDirty`（fail-fast + dirty + force）
- 基线验证：`go test -p 1 ./internal/migrations/ -v`，期望全部 PASS（需 MySQL/Redis 容器 healthy）
- 可选 Mutation：删除 `Up()` 中 `if errors.Is(err, migrate.ErrNoChange) { ... return nil }` 分支，使已应用库上重复 `up` 返回错误
- 预期失败：`TestUpCreatesSchemaAndIsIdempotent` 第二次 `Up` 断言失败（`second up should be idempotent`）——因为删除该分支后 `ErrNoChange` 不再被视为幂等成功
- 恢复确认：还原该分支后再次运行同一命令，恢复 PASS

## CL-002：serve 只读 readiness check（绝不建表 / 不自动 migrate）

- Owner 需要理解：`serve` 启动不做任何 DDL，只在「`schema_migrations` 已初始化、非 dirty、版本已最新」时才继续 seed + 起 HTTP；否则 fail-fast。若 readiness 检查被放宽（如允许空库通过），应用会在缺表的库上启动，seed 或首个请求才报错，故障被推迟到运行时且难定位。
- 生产代码：`internal/boot/boot.go:57-72`（`checkSchemaReady`：dirty → 报错、`current < latest` → 报错）、`internal/migrations/migrations.go:94-113`（`Status` 只读，不创建 `schema_migrations`、不建表）
- 关键测试：`internal/migrations/migrations_test.go` 的 `TestStatusIsReadOnly`；`internal/boot/boot_migration_test.go` 的 `TestCheckSchemaReadyFailsOnEmptyDB`、`TestCheckSchemaReadyFailsOnDirty`、`TestCheckSchemaReadyPassesWhenMigrated`
- 基线验证：`go test -p 1 ./internal/migrations/ ./internal/boot/ -v`，期望全部 PASS
- 可选 Mutation：删除 `checkSchemaReady` 中的 `if current < latest { return ... }` 判断（使空库也通过 readiness）
- 预期失败：`TestCheckSchemaReadyFailsOnEmptyDB` 失败——该测试断言空库上 `checkSchemaReady` 必须报错，删除判断后返回 nil，与预期不符
- 恢复确认：还原该判断后再次运行同一命令，恢复 PASS
