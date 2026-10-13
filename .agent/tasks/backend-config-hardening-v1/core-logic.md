# Owner Core Logic 验证卡

本任务核心是两个安全/正确性不变量：敏感凭据无内置默认回退、连接层字符集统一 utf8mb4。以下两张验证卡让 Owner 通过「代码位置 → 业务后果 → 测试失败」理解因果关系。

## CL-001：MySQL 密码缺失时 fail-fast，不回退内置默认值

- Owner 需要理解：`database.default.pass` 未配置（config 为空且无 `DATABASE_DEFAULT_PASS`）时，应用启动与 `migrate up` 必须以非零退出并指认缺失字段，**绝不回退 `root`**；否则「不安全配置静默启动」的安全漏洞会复活。
- 生产代码：`internal/boot/boot.go` `applyDatabaseConfig()`（L95-101 空密码 fail-fast，且已删除 `defaultDBPass="root"` 常量）；`internal/migrations/migrations.go` `buildDSN()`（空密码 fail-fast，返回 `(string, error)`）；`internal/logic/legacymigrate/db.go` `OpenConnections()`（同）。
- 关键测试：
  - `internal/boot/boot_test.go` `TestApplyDatabaseConfigRejectsEmptyPass`
  - `internal/migrations/charset_test.go` `TestBuildDSNRejectsEmptyPass`
  - `internal/logic/legacymigrate/db_test.go` `TestOpenConnectionsRejectsEmptyPass`
- 基线验证：`DATABASE_DEFAULT_PASS=root go test -count=1 ./internal/boot/ ./internal/migrations/ ./internal/logic/legacymigrate/`，预期全部 PASS。
- 可选 Mutation：删除 `internal/boot/boot.go` 中 `if pass == "" { return gerror.New(...) }` 这段空密码校验（保留 `pass := cfgString(ctx, "database.default.pass", "")`）。
- 预期失败：`TestApplyDatabaseConfigRejectsEmptyPass` 必须失败——因为 `applyDatabaseConfig` 只设置配置不实际连库，校验被删后 `err == nil`，断言 `expected error` 触发 `t.Fatal`。这证明测试保护的是「fail-fast」本身而非「连接失败」。
- 恢复确认：`git checkout -- internal/boot/boot.go` 后重跑上述命令，恢复 PASS。

## CL-002：连接字符集 utf8mb4 决定四字节 Unicode 能否读写

- Owner 需要理解：连接层 `charset=utf8`（即 utf8mb3）只能传输 3 字节字符，写入 emoji / 补充平面字符会报错或乱码；统一为 `utf8mb4` 后四字节字符才能正确读写（表结构本就为 utf8mb4，问题只出在连接层）。
- 生产代码：`internal/migrations/migrations.go` `buildDSN()`（`cfg.Params["charset"] = cfgString(..., defaultDBCharset)`，`defaultDBCharset="utf8mb4"`）；`manifest/config/config.yaml` `database.default.charset: "utf8mb4"`；`internal/boot/boot.go`、`internal/logic/legacymigrate/db.go` 同步为 utf8mb4。
- 关键测试：`internal/migrations/charset_test.go` `TestUTF8MB4FourByteRoundtrip`（真实 MySQL 写入 `😀 🎉 𝄞` 并读回一致）；`TestBuildDSNCharsetUTF8MB4`（migrations 与 legacymigrate）。
- 基线验证：`DATABASE_DEFAULT_PASS=root go test -count=1 -run 'TestUTF8MB4FourByteRoundtrip|TestBuildDSNCharsetUTF8MB4' ./internal/migrations/`，预期 PASS。
- 可选 Mutation：将 `manifest/config/config.yaml` 的 `charset: "utf8mb4"` 改回 `charset: "utf8"`（这是运行时连接字符集的真实来源；改 `defaultDBCharset` 常量无效，因为 config 键已存在、常量被覆盖）。
- 预期失败：`TestUTF8MB4FourByteRoundtrip` 必须失败——INSERT 四字节字符触发 `Error 3988 Conversion from collation utf8mb3_general_ci into utf8mb4_0900_ai_ci impossible`。这证明测试能区分「utf8 连接（错误）」与「utf8mb4 连接（正确）」。
- 恢复确认：`git checkout -- manifest/config/config.yaml` 后重跑，恢复 PASS。

> 说明：`internal/boot` 与 `internal/migrations` 的 `defaultDBCharset` 常量在实际运行中被 `config.yaml`/环境变量覆盖，属于「一致性占位」而非运行时事实来源；因此 Mutation 必须改 `config.yaml`（或 compose 注入的 `DATABASE_DEFAULT_CHARSET`）才能真实破坏不变量。
