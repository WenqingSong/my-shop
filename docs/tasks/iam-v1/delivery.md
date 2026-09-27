# Delivery Verification

## Milestone

IAM V1（用户注册、登录与 JWT 认证）—— 本任务为单一完整业务模块，无子里程碑拆分。

## Environment

- OS: linux（CNB DinD，环境每日重置、数据不持久）
- Go: go1.24.1 linux/amd64（task.md / contract.md 要求 Go 1.23+，向上兼容）
- MySQL: 8.0（容器 `my-shop-mysql`，`Up (healthy)`，端口 3306）
- Redis: 7（容器 `my-shop-redis`，`Up (healthy)`，端口 6379；IAM V1 本身不使用 Redis，仅参与启动连通性校验）
- Kafka: 不适用（本任务无 MQ）
- Docker: 依赖容器已运行（`docker ps` 显示两个容器 healthy）

## Delivery Target

- Commit / Worktree / Artifact:
  - 当前 HEAD：`ba7560ffb3a518962a74ffbd3a541d17e121335d`（`chore: 清理已删除的任务草稿文件`）
  - 已跟踪修改：`go.mod`、`go.sum`、`internal/boot/boot.go`、`internal/cmd/cmd.go`、`internal/logic/logic.go`、`manifest/config/config.yaml`
  - 新增未跟踪：`api/iam/`、`internal/auth/`、`internal/codes/`、`internal/controller/iam/`、`internal/logic/iam/`、`internal/middleware/`、`internal/service/iam.go`、`docs/tasks/iam-v1/contract.md`
  - 独立构建产物：`go build -o /tmp/my-shop-deliverer .`（退出码 0）
- Cleaner Review Target: 见 `findings.md`「Review Target」——HEAD `ba7560f` + 上述同一组已跟踪修改与新增未跟踪文件
- Target Match: YES（工作区生产代码文件与 Cleaner 审查对象一致；`core-logic.md`/`findings.md` 为 Cleaner 自身输出文档，属预期变更）
- Task: `docs/tasks/iam-v1/task.md`
- Contract: `docs/tasks/iam-v1/contract.md`（状态 `APPROVED`，Owner 于 2026-09-27 确认）

## Build

| Check | Result | Evidence |
| --- | --- | --- |
| go build | PASS | `go build ./...` 退出码 0，无输出；`go build -o /tmp/my-shop-deliverer .` 退出码 0，产物 36MB 可执行 |

## Tests

| Test | Result | Evidence |
| --- | --- | --- |
| Unit / Integration | PASS | `go test ./...` 全部 ok；`internal/controller/iam` 0.841s（真实 MySQL 集成：注册/登录/`/me` 正常路径、错误路径与边界） |
| Race | PASS | `go test -race ./internal/controller/iam/ ./internal/auth/ ./internal/logic/iam/` 全部 ok；`controller/iam` 7.207s（并发注册同名用户无 data race） |
| Vet | PASS | `go vet ./...` 退出码 0，无输出 |
| API Smoke | PASS | 见下「API Smoke Test」与「Data Verification」，均为本次实际启动服务后的真实请求 |

### API Smoke Test（本次真实运行）

服务以正式启动命令 `go build` 产物 + `manifest/config/config.yaml` 默认配置启动，监听 `:8000`，启动日志确认路由边界：

```
| :8000 | GET  | /health   | health.Liveness | middleware.Response |
| :8000 | POST | /register | iam.Register    | middleware.Response |
| :8000 | POST | /login    | iam.Login       | middleware.Response |
| :8000 | GET  | /me       | iam.Me          | middleware.Response | middleware.Auth |
```

关键请求与响应：

| 场景 | HTTP | code | 说明 |
| --- | --- | --- | --- |
| 注册合法用户 | 200 | 0 | `data:{id:124,username:...}` |
| 重复注册同名 | 409 | 2001 | `用户名已存在`，`data:null` |
| 注册非法 username | 400 | 1001 | `参数错误` |
| 登录正确凭据 | 200 | 0 | `access_token`/`token_type:Bearer`/`expires_in:3600` |
| `/me` 有效 token | 200 | 0 | `data:{id:124,username}`，id 与 token sub 一致 |
| `/me` 无 token | 401 | 1002 | `未授权`，`data:null` |
| `/me` 篡改 token | 401 | 1002 | `未授权` |
| 登录错误密码 | 401 | 2002 | `用户名或密码错误` |
| 登录不存在用户 | 401 | 2002 | `用户名或密码错误`（与错误密码一致，防枚举） |
| `/health` | 200 | 0 | `{status:ok,time:...}` |

JWT 声明核对（对登录返回的 token 解码）：`iss=surgecart`、`sub=124`、`iat` 与 `exp` 均为 Unix 秒，`exp-iat=3600`。

## Acceptance Criteria

| ID | Result | Evidence |
| --- | --- | --- |
| AC-001 | PASS | 注册合法用户返回 200/0；查库 `password_hash` 前缀 `$2a$10$`、长度 60、`is_plaintext=0`、`is_bcrypt=1`；集成测试额外断言同密码两次注册哈希不同（含盐） |
| AC-002 | PASS | 非法 username/password 返回 400 code=1001（smoke 实测）；集成测试逐项断言 `userCount=0` 未写入 |
| AC-003 | PASS | 重复注册返回 409 code=2001（smoke 实测）；集成测试断言 hash 不变（未覆盖），并发测试 10 并发同名仅 1 成功 9 冲突 |
| AC-004 | PASS | 登录成功返回 token；解码声明 `sub==id`、`iss==surgecart`、`iat/exp` 且 `exp-iat==3600`；`token_type==Bearer`、`expires_in==3600` |
| AC-005 | PASS | 错误密码与不存在用户均 401 code=2002、message/status 一致（smoke 实测），且不签发 token |
| AC-006 | PASS | 有效 token 访问 `/me` 返回 200/0，`id==124` 且 `username` 匹配，id 与 token `sub` 一致 |
| AC-007 | PASS | 无 token、篡改 token 均 401 code=1002 且 `data:null`（smoke 实测）；集成测试额外覆盖坏格式/坏签名/过期/错误 issuer |
| AC-008 | PASS | 启动路由表确认 `/register`、`/login` 仅挂 `middleware.Response`，`/me` 额外挂 `middleware.Auth`；集成测试断言无 token 访问公开接口成功、访问 `/me` 401 |
| AC-009 | PASS | 全量 grep 无硬编码密钥于 `.go` 源码（密钥仅经 `g.Cfg().GetEffective("auth.jwt.secret")` 读取）；`AUTH_JWT_SECRET=short` 启动 fail-fast（退出码 1，日志 `auth.jwt.secret 长度至少为 32 字节`）；单测 `TestSecretFromEnv` 验证环境变量覆盖生效 |

## Data Verification

- `users` 表结构：`id` BIGINT UNSIGNED 主键自增、`username` VARCHAR(24) NOT NULL、`password_hash` VARCHAR(60) NOT NULL、`created_at/updated_at` DATETIME；`UNIQUE KEY uk_username (username)` 存在（`SHOW INDEX` 确认）。
- 注册用户 `password_hash`：前缀 `$2a$10$`（bcrypt cost=10）、长度 60、非明文。
- 事实来源：MySQL `users` 表，无 Redis/MQ 参与，与 Contract「Consistency Semantics」一致。

## Runtime / Infrastructure Verification

- 服务启动流程：`boot.Bootstrap` 顺序执行 配置应用 → JWT 密钥校验 → MySQL/Redis 连通性校验 → `users` 幂等建表 → HTTP 监听；启动日志依次输出 `all dependencies are reachable (mysql, redis)`、`users 表已就绪`、`http server started listening on [:8000]`。
- 依赖连通：MySQL、Redis 容器 healthy，启动校验通过。
- 幂等建表：`CREATE TABLE IF NOT EXISTS users` 在启动时执行，满足每日重置环境可重复建表。
- 密钥 fail-fast：短密钥启动即失败（退出码 1），非静默降级。
- Docker/Compose：本里程碑 Task 未要求额外 Docker 化交付（`docker-compose.yml` 仅作 MySQL/Redis 依赖编排，已 healthy 运行），故不新增镜像构建验收。

## Tests Not Executed

| Test | Reason | Risk |
| --- | --- | --- |
| 全项目 `go test -race ./...` | 本任务并发不变量仅集中在 IAM 注册唯一性（`internal/controller/iam`、`internal/auth`、`internal/logic/iam` 已 race 通过）；其余为无共享可变状态的样板包 | 低。健康检查等既有包无并发业务不变量 |
| 过期 token / 错误 issuer 的运行时 Smoke | 已由集成测试 `TestIAMEndToEnd` AC-007 用真实 HTTP 请求覆盖（`internal/controller/iam/iam_test.go`） | 低。行为与中间件同一条代码路径 |
| `AUTH_JWT_SECRET` 覆盖后重启的运行时 Smoke | 已由单测 `TestSecretFromEnv` 覆盖；短密钥运行时 fail-fast 已实测 | 低 |

## Remaining Risks

- 两个开放 P3 Finding（见 `findings.md`，均非阻塞，是否处理由 Owner 决定）：
  - `CLEAN-001`：`/health` 成功响应 `message` 由空串变为 `"OK"`（与 README 文档格式一致，`/health` 的 200/status/time 不变）。
  - `CLEAN-002`：登录防枚举的「耗时对齐」未被测试断言保护（现有测试仅断言 code/message/status 一致）。
- `manifest/config/config.yaml` 提交了 dev-only 默认 JWT 密钥（Contract Owner Decision #5 明确授权）；生产环境必须用 `AUTH_JWT_SECRET` 覆盖，属发布纪律，非本任务缺陷。

## Rollback / Recovery Notes

- 本里程碑未包含故障恢复/回滚类 AC，未执行破坏性演练；服务为无状态 HTTP 应用，事实来源在 MySQL，重启即可恢复（幂等建表支持每日重置环境）。

## Result

PASS
