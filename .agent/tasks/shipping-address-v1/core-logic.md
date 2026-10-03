# Owner 核心逻辑验证

以下验证卡聚焦本任务真正决定「安全边界」与「并发一致性」的两处核心机制。普通 CRUD、字段搬运与样板校验不列入。

## CL-001：数据隔离（`WHERE id AND user_id`）

- Owner 需要理解：所有地址详情/更新/删除必须同时按「地址 id」和「`user_id=Principal.UserID`」过滤。若漏掉 `user_id` 条件，登录用户即可越权读取/篡改/删除他人地址；同时「他人地址」与「不存在地址」统一返回 7001(404)，避免泄露资源存在性与归属（Oracle 风险）。
- 生产代码：
  - `internal/logic/address/address.go` → `findOne`（约 291 行）、`findOneInTx`（约 303 行）、`Delete`（约 268 行）中的 `.Where("user_id", userID)`；
  - 身份来源 `internal/controller/address/address.go` → `currentUserID`（取自 `middleware.PrincipalFromContext`，不接受请求体 `user_id`）。
- 关键测试：`internal/controller/address/address_test.go` → `TestAddressIsolation`。
- 基线验证：`go test -p 1 ./internal/controller/address/ -run TestAddressIsolation -v` → 预期 PASS。
- 可选 Mutation：临时删除 `findOne` 中 `.Where("user_id", userID)`（仅保留 `Where("id", id)`）。
- 预期失败：`TestAddressIsolation` 中用户 B 读 A 地址应得 7001/404，改为命中成功（status 200），断言失败。
- 恢复确认：还原 `.Where("user_id", userID)`，重跑 `TestAddressIsolation` → PASS。

## CL-002：默认地址唯一（生成列 + 唯一索引 + 事务切换）

- Owner 需要理解：每用户最多一个默认地址，由 DB 生成列 `default_key = IF(is_default=1, user_id, NULL)` + 唯一索引 `uk_user_default` 在数据库层强约束（并发亦成立）。「设新默认」= 同一事务内先取消旧默认再置新默认；并发落败方命中唯一索引返回 7002(409)。删除默认地址后允许无默认、不自动提升。
- 生产代码：
  - `internal/migrations/sql/20261001000005_addresses.up.sql` → `default_key`（VIRTUAL 生成列）与 `UNIQUE KEY uk_user_default`；
  - `internal/logic/address/address.go` → `Create`（首条自动默认 + `clearDefault` + 插入）、`Update`（`clearDefault` + 更新）、`clearDefault`（约 279 行）。
- 关键测试：`internal/controller/address/address_test.go` → `TestAddressCrudAndDefaultSwitch`、`TestAddressDefaultConcurrency`。
- 基线验证：`go test -p 1 -race ./internal/controller/address/ -run 'TestAddressCrudAndDefaultSwitch|TestAddressDefaultConcurrency' -v` → 预期 PASS。
- 可选 Mutation：临时注释掉 `Update` 中 `if isDefault == defaultFlag { s.clearDefault(...) }` 整块（不再取消旧默认）。
- 预期失败：`TestAddressCrudAndDefaultSwitch` 的「更新 a2 为默认」`assertOK` 失败——旧默认 a1 未取消，DB 唯一索引报 1062 → 7002(409)，status!=200。证明唯一索引是默认唯一性的兜底保证，且「设默认」必须与 `clearDefault` 原子配合。
- 恢复确认：还原 `clearDefault` 调用，重跑上述测试 → PASS。
