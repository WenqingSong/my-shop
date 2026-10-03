# Delivery Verification

## Milestone and Target

- Milestone：Shipping Address V1（前台用户收货地址：增删改查 + 默认地址 + 数据隔离）
- Delivery Target：HEAD `561e9c0cc81d140c52f1a7c71fd3c0946f827025`（分支 `develop`，working tree clean）
- Cleaner Review Target：HEAD `a7aad28fd96953a1abfee50776f0865219ac7947`（生产代码）
- Target Match：YES —— `561e9c0` 相对 `a7aad28` 仅改动 `core-logic.md` 与 `findings.md` 两份文档（Cleaner 自身输出），生产代码与迁移文件逐字节一致，无实质变化。
- Task：`.agent/tasks/shipping-address-v1/task.md`
- Contract：`.agent/tasks/shipping-address-v1/contract.md`（`APPROVED`，含 VIRTUAL 修订，Design Impact = NEW）
- Design Artifact：`docs/design/address.md`（已纳入 Cleaner Review Target）

## Environment

- OS：linux（CNB DinD，环境每日重置、数据不持久）
- Go：go1.24.1 linux/amd64
- MySQL：8.0（容器 `my-shop-mysql`，`Up (healthy)`，:3306，库 `my_shop`）
- Redis：7（容器 `my-shop-redis`，`Up (healthy)`，:6379；仅经 `middleware.Auth` 校验会话）
- Kafka：不适用（本任务无 MQ）
- Docker：两个依赖容器 healthy；服务以 `go build` 产物启动，监听 `:18080`（`SERVER_ADDRESS` 覆盖）
- 配置来源：`manifest/config/config.yaml` 开发默认 + 环境变量 `AUTH_JWT_SECRET`（≥32B 测试值）、`ADMIN_SUPER_PASSWORD`（仅用于超级管理员尚不存在时，本次已存在故跳过 seed）；不记录 Secret 值
- 测试数据隔离：注册临时用户 `deliverera`/`delivererb`，验收后已删除（FK `ON DELETE CASCADE` 级联清除地址），Redis 会话键已删除，服务已停止

## Verification

| Check | Result | Evidence |
| --- | --- | --- |
| Build | PASS | `go build ./...` 退出码 0；`go build -o /tmp/my-shop-deliverer .` 退出码 0，产物 36MB 可执行 |
| Vet | PASS | `go vet ./...` 退出码 0，无输出 |
| gofmt | PASS | `gofmt -l`（api/address、controller/address、logic/address、service/address.go、codes、routes_frontend、logic.go、migrations 相关）空输出 |
| 全量测试 | PASS | `go test -p 1 ./...` 全部包 ok；`internal/controller/address` 1.747s（真实 MySQL/Redis 集成）；`internal/migrations` 9.788s |
| Race（并发默认唯一） | PASS | `go test -p 1 -race -count=1 ./internal/controller/address/...` ok |
| Schema/迁移 | PASS | `schema_migrations` 版本 `20261001000005`、非 dirty；`addresses` 表含 `default_key` VIRTUAL 生成列 + `uk_user_default` 唯一索引 + `idx_user_id` + `FK → users.id ON DELETE CASCADE`（`SHOW CREATE TABLE` 实测） |
| 服务启动 | PASS | 启动日志：`all dependencies are reachable (mysql, redis)` → `schema 已就绪（版本 20261001000005）` → 超级管理员跳过 seed → 权限 seed → `http server started listening on [:18080]`；路由表确认 `/addresses*` 均挂 `middleware.Response` + `middleware.Auth` |
| API Smoke（主链路） | PASS | 见下「API Smoke Test」，全部为本次真实 HTTP 请求 + 数据库核对 |

### API Smoke Test（本次真实运行，监听 :18080）

用户 A（`deliverera`，id=6）、用户 B（`delivererb`，id=7），经 `/register` + `/login` 获取 Bearer token：

| 场景 | HTTP | code | 关键结果 |
| --- | --- | --- | --- |
| 创建首条地址（未传 is_default） | 200 | 0 | `is_default=true`（自动默认），`id` 归属 A |
| 创建第二条（未传 is_default） | 200 | 0 | `is_default=false` |
| 列表（A） | 200 | 0 | `items` 仅 A 本人 2 条，无他人地址 |
| 详情（A 查本人） | 200 | 0 | 字段与创建一致 |
| 更新本人（改收货人） | 200 | 0 | `recipient_name` 变更生效 |
| 设第二条为默认 | 200 | 0 | 旧默认自动取消（`is_default=false`） |
| B 读 A 的地址 | 404 | 7001 | 统一「地址不存在」，不泄露存在性/归属 |
| B 更新 A 的地址 | 404 | 7001 | 拒绝且 A 地址无变化 |
| B 删除 A 的地址 | 404 | 7001 | 拒绝且 A 地址无变化 |
| A 查不存在地址（999999） | 404 | 7001 | 统一 404 |
| 未登录 GET/POST `/addresses` | 401 | 1002 | 无任何写入 |
| 创建体注入 `user_id=7` | 200 | 0 | `user_id` 被忽略，落库归属仍为 A（id=6） |
| 删除默认地址 | 200 | 0 | 删除后该用户 0 默认，不自动提升 |

### Data Verification（MySQL 真实核对）

- 注入 `user_id=7` 创建的地址（id=9）落库 `user_id=6`（A），证明请求体 `user_id` 不可伪造（INV-004）。
- `default_key` 生成列：`is_default=1` 时 `default_key=user_id`（非空），否则 `NULL`，与 VIRTUAL 定义一致。
- 默认唯一 DB 兜底（INV-002）：直接 `UPDATE` 将同一用户两条地址置默认，第二条报 `ERROR 1062 Duplicate entry '6' for key 'addresses.uk_user_default'`——即使绕过应用层，数据库仍强约束「每用户最多一个默认」。
- 隔离终态：A 名下 2 条、B 名下 0 条；删除默认后 A 默认数 = 0（允许无默认）。
- Redis：登录后 `iam:session:<sid>` 会话键存在（A/B 各 1），`middleware.Auth` 校验通过才放行受保护接口，未登录返回 401——Redis 仅参与会话，不承载地址数据，与 Contract 一致性语义一致。

## Acceptance Evidence

| AC / INV | Result | 本次运行证据 |
| --- | --- | --- |
| AC-001 创建 | PASS | 首条自动默认、归属 A（HTTP 200 + 落库 `user_id=6`） |
| AC-002 列表 | PASS | 仅返回 A 本人 2 条 |
| AC-003 详情 | PASS | 本人 200；他人/不存在统一 404/7001，不泄露 |
| AC-004 更新 | PASS | 本人 200；他人 404/7001 无写入 |
| AC-005 删除 | PASS | 本人 200；他人 404/7001 无写入 |
| AC-006 隔离 | PASS | 两真实用户 HTTP：B 无法读/改/删 A，A 数据不变、B 名下 0 条 |
| AC-007 默认唯一 | PASS | 切换取消旧默认；删除默认后无默认；DB 1062 兜底；`-race` 并发测试通过 |
| AC-008 未登录 | PASS | 无 token 5 类接口均 401/1002，`addresses` 无新增 |
| INV-001 隔离+不泄露 | PASS | 详情/更新/删除均 `WHERE id AND user_id`；他人/不存在统一 7001 |
| INV-002 默认唯一（DB 约束） | PASS | `uk_user_default` 唯一索引 + VIRTUAL 生成列，实测 1062 |
| INV-003 默认切换原子 | PASS | 事务内先取消旧默认再置新默认，切换后旧默认取消 |
| INV-004 身份不可伪造 | PASS | 请求体注入 `user_id` 被忽略，归属始终为 `Principal.UserID` |

## Not Executed

| Check | Reason | Risk |
| --- | --- | --- |
| 全项目 `go test -race ./...` | 并发不变量仅集中在地址默认唯一（`internal/controller/address` 已 race 通过）；其余包无共享可变状态并发业务 | 低 |
| 重启/恢复/回滚演练 | Task/Contract 未要求该类 AC；服务为无状态 HTTP，事实来源 MySQL | 无（不适用） |
| Kafka/MQ | 本任务无 MQ，不适用 | 无 |

## Remaining Risks

- 并发「设默认」落败方返回 409/7002，客户端需提示重试（V1 已确认的可接受语义）。
- 手机号校验限定中国大陆 `^1[3-9]\d{9}$`，海外号码需未来扩展。
- 无地址数量上限/邮编/标签（Out of Scope）。

## Result

PASS
