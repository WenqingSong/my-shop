# Owner 核心逻辑验证卡

## CL-001：`.env` 空值不覆盖 `config.yaml` 默认值（INV-001 / AC-003）

- Owner 需要理解：`scripts/lib.sh` 会把 `.env` 导出为环境变量，而 GoFrame `g.Cfg().GetEffective` 把「空但已设置」的环境变量当作覆盖值。若把 `AUTH_JWT_SECRET=`（或 `DATABASE_DEFAULT_PASS=`）这类空值也导出，会以空串覆盖 `config.yaml` 里的开发默认 JWT secret（64hex=32 字节）或 `root` 密码，导致启动 fail-fast。修复是「空值不导出」，使加载优先级稳定为「已导出环境变量 > `.env` 非空值 > config.yaml 默认值」。
- 生产代码：`scripts/lib.sh` `_load_env_file`（L33-60，关键行 L54 `[[ -n "${val}" ]] || continue`）
- 关键测试：`scripts/test-lib.sh`（`LIB_TEST_EMPTY` 空值不导出断言，L26-30）
- 基线验证：`make test-lib`（或 `bash scripts/test-lib.sh`）→ 预期 `test-lib.sh 全部通过`，exit 0
- 可选 Mutation：把 `[[ -n "${val}" ]] || continue` 删除（恢复为无条件 `export`）
- 预期失败：`make test-lib` 中「`LIB_TEST_EMPTY` 不应被导出」断言失败（`${LIB_TEST_EMPTY+x}` 命中），exit 非零
- 恢复确认：还原 `lib.sh` 后再次 `make test-lib` 通过

## CL-002：超级管理员创建条件预检（INV-002 / AC-008）

- Owner 需要理解：`make up` 启动前必须区分「首次创建 vs 已存在」两种情形——数据库已有 `is_super=1` 超管时，无论是否提供 `ADMIN_SUPER_PASSWORD` 都不覆盖既有密码、正常通过（幂等）；不存在且未配置密码时 fail-fast 并明确指认 `ADMIN_SUPER_PASSWORD`（首次创建必须由开发者主动提供初始密码，禁止静默用弱默认建号）。
- 生产代码：`internal/boot/admin_check.go` `CheckSuperAdminCondition`（L19-39）；只读判断 `internal/boot/seed.go` `superAdminExists`（L150-159，`admins` 表不存在 1146 视为「不存在」）；`make up` 接线 `scripts/up.sh`（L26-30 `admin check`）
- 关键测试：`internal/boot/admin_check_test.go`（`TestCheckSuperAdminConditionExists` / `MissingPasswordFails` / `PasswordPresentPasses` / `TableNotExist`）；幂等/并发由既有 `seed_test.go` `TestSeedSuperAdminIdempotent`/`Concurrent` 覆盖
- 基线验证：`go test -p 1 ./internal/boot/...` → 预期 `ok`
- 可选 Mutation：把 `superAdminExists` 的 `isTableNotExistError(err) → return false, nil` 改为 `return false, err`（把「未迁移」误判为 DB 错误）
- 预期失败：`TestSuperAdminExistsTableNotExist` 断言失败（预期 `err==nil` 得到非 nil）
- 恢复确认：还原 `seed.go` 后再次 `go test -p 1 ./internal/boot/...` 通过
