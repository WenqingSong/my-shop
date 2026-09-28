# Delivery Verification

## Milestone and Target

- Milestone：IAM V2（JWT + Redis 有状态会话与登出）最终交付验收
- Delivery Target：HEAD `e368a1f5`（commit `feat(iam): 实现 JWT 有状态会话与登出`），分支 `feat/auth-v2`，working tree clean
- Cleaner Review Target：`8e4ff4df`（同一批变更，当时为工作区未提交状态）
- Target Match：YES

说明：Cleaner 在 `8e4ff4df`（工作区未提交）完成 `CLEAN`；随后该批变更被提交为 `e368a1f5`。`git diff 8e4ff4df..e368a1f5` 恰好为 IAM V2 变更集（19 文件，+1105/-21），文件清单与 `findings.md` 的 Review Target 完全一致；`findings.md`/`core-logic.md` 引用的行号（`session.go:84-101/97-99/103-110`、`auth.go:19-39`、`logic:128-133`、`controller:40-49`）与当前源码逐行吻合。故提交内容与 Cleaner 审查内容一致，无 CLEAN 后实质变化。

## Environment

- OS：Linux（CNB DinD，每日重置，数据不持久）
- 运行时：Go 1.24.1 linux/amd64
- MySQL：8.0（容器 `my-shop-mysql`，healthy，127.0.0.1:3306）
- Redis：7-alpine（容器 `my-shop-redis`，healthy，127.0.0.1:6379）
- 配置来源：`manifest/config/config.yaml`（dev 默认值）+ 环境变量覆盖（本次未额外注入）
- 测试数据：Smoke 使用一次性用户名 `smoke<epoch>`，仅写开发库 `my_shop`，未改动生产；服务进程已停止

## Verification

| Check | Result | Evidence |
| --- | --- | --- |
| 正式构建 `go build ./...` | PASS | 退出码 0 |
| 静态检查 `go vet ./...` | PASS | 退出码 0 |
| 格式 `gofmt -l`（api/internal/main） | PASS | 无输出 |
| 全量测试 `go test ./...` | PASS | 全部 ok；`controller/iam` 5.410s（真实 MySQL+Redis 集成） |
| Race `go test -race ./internal/controller/iam/ ./internal/auth/ ./internal/logic/iam/` | PASS | 全部 ok；`controller/iam` 27.309s（并发注册/并发登出无 data race） |
| 服务启动与健康检查 | PASS | 真实启动二进制于 :8000，`GET /health` 返回 200 `{code:0,status:"ok"}`，配置加载正常 |
| 主链路 Smoke（注册→登录→me→logout→me 401） | PASS | 见下 Acceptance Evidence |
| 最终数据核对（Redis session + MySQL users） | PASS | 见下 |
| 设计文档一致性 | PASS | `docs/design/iam.md` 覆盖架构/数据模型/鉴权·登出·撤销/安全边界/错误码/配置，与 Contract 及实现一致 |

## Acceptance Evidence

主链路 Smoke（本次真实运行，服务端口 :8000）：

- `POST /register` → 200 `{code:0,data:{id:223,username:"smoke1790523661"}}`
- `POST /login` → 200 `{code:0,data:{access_token, token_type:"Bearer", expires_in:3600}}`；JWT payload 解码为 `{iss:"surgecart", sub:"223", sid:"e54c1bb5eaecd23b7e625052fe64e987", exp=iat+3600}`
- `GET /me`（Bearer token）→ 200 `{code:0,data:{id:223,username:"smoke1790523661"}}`，`id == sub == 223`（INV-002 / AC-002）
- Redis 登出前：`HGETALL iam:session:e54c1bb5...` → `user_id=223`、`revoked=0`、`TTL=3600`（INV-001 / AC-001）
- `POST /logout` → 200 `{code:0,data:null}`（AC-008）
- Redis 登出后：`user_id=223`、`revoked=1`、`TTL=3600`（key 保留、未 DEL、TTL 未清零）（INV-003 / AC-003 / AC-004）
- `GET /me`（同 token）→ 401 `{code:1002,message:"未授权",data:null}`（INV-003 / AC-003）
- MySQL：`SELECT id,username FROM users WHERE username='smoke1790523661'` → `223 smoke1790523661`（最终落库）

关键 AC/INV 覆盖（测试证据，来自 `go test ./...` 集成套件）：

- AC-005 / INV-002（缺失/坏格式/坏签名/过期/错误 issuer/session 缺失/无 sid/未知 sid 均 401）：`TestIAMEndToEnd` badCases、`TestSessionMissingTokenReturns401`、`TestTokenWithoutSidReturns401`、`TestUnknownSidTokenReturns401` — PASS
- AC-006 / INV-005（logout 需 token、幂等、忽略客户端 sid）：`TestLogoutRequiresToken`、`TestLogoutIdempotent`、`TestLogoutIgnoresClientSid` — PASS
- AC-007 / INV-004（登出不影响其他会话）：`TestLogoutDoesNotAffectOtherSessions` — PASS
- INV-006（Redis 查询失败 fail-closed → 401）：`TestRedisWrongTypeFailClosed` — PASS
- AC-004（TTL 到期 key 消失且 /me 仍 401）：`TestSessionTTLExpiryReturns401` — PASS
- 并发撤销原子性：`TestConcurrentLogoutSameToken` — PASS
- AC-009（环境变量覆盖）：`TestSessionTTLEnvOverride` / `TestSessionTTLRejectsNonPositive` — PASS

## Not Executed

| Check | Reason | Risk |
| --- | --- | --- |
| 停 Redis 容器的手工全量中断演练 | 已有 `TestRedisWrongTypeFailClosed` 覆盖同一 fail-closed 错误分支（`ValidateSession` 返回 error → 401），未重复停机演练 | 低；停机与 WRONGTYPE 走同一代码分支，证据已由集成测试提供 |

## Remaining Risks

- `CLEAN-001`（P3，OPEN）：`ValidateSession` 的 `user_id != sub` 交叉校验分支无测试覆盖（纵深防御分支，非直接可利用漏洞）。
- `CLEAN-002`（P3，OPEN）：登录「写 session 失败 → 500 且不签发 token」分支无测试覆盖（与鉴权侧对称，逻辑简单）。
- 以上两条为 Cleaner 已登记的 P3 非阻塞 Finding，Deliverer 不关闭、不降级。
- Owner 核心逻辑验证（`core-logic.md` 的 CL-001/CL-002）的显式完成确认未在本轮交付材料中记录；本次按 Owner 直接发起里程碑验收执行，技术验证结论见下，最终接受由 Owner 决定。

## Result

PASS
