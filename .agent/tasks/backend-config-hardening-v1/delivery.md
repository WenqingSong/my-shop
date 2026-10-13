# Delivery Verification

## Milestone and Target
- Milestone：backend-config-hardening-v1（安全配置加固与数据库字符集统一）
- Delivery Target：`cee1e6aed2a7527998d662cd69347892ec9fa4c1`（Cleaner CLEAN 的 production+test snapshot）
- Cleaner Review Target：`cee1e6aed2a7527998d662cd69347892ec9fa4c1`
- Owner review_target：`cee1e6aed2a7527998d662cd69347892ec9fa4c1`
- Target Match：YES
- feature_head（实际参与集成验证的 feature snapshot）：`197ae728245345a3c1bf29e8e99581d78468df48`
- develop_base（验证时 origin/develop）：`359b8cceeebefe7b490f783c1ad5a118afd6e31e`

> `cee1e6a..197ae72` 仅改动 `.agent/tasks/backend-config-hardening-v1/{core-logic,findings,owner-decision,state}.md`（review-neutral tail），生产代码/测试未发生 CLEAN 后实质变化。

## Environment
- OS：Debian GNU/Linux 12 (bookworm)，Linux 5.4.241（x86_64）
- Go：go1.24.1 linux/amd64
- Docker：29.6.2；Docker Compose：v5.3.1
- MySQL：8.0.46（容器 `my-shop-mysql`，127.0.0.1:3306，`MYSQL_ROOT_PASSWORD=root`，`character_set_server=utf8mb4`）
- Redis：7-alpine（容器 `my-shop-redis`，127.0.0.1:6379）
- mysql client：MariaDB 10.11.18（仅用于探针验证）
- 配置来源：无 `.env`；测试凭据按 `scripts/test.sh` 显式 `DATABASE_DEFAULT_PASS=root` 注入；JWT Secret 用 32 字节占位值（非真实密钥）
- 测试数据与隔离：`TestUTF8MB4FourByteRoundtrip` 使用临时探针表 `charset_probe` 并自清理；直接四字节探针表 `_deliverer_probe` 已显式 DROP；`serve` 正向验证在超管 seed 边界 fail-fast，未产生数据写入

## Verification

| Check | Result | Evidence |
|---|---|---|
| Build（`go build ./...`） | PASS | exit 0；独立产出二进制 45MB，可运行 |
| `go vet ./...` | PASS | exit 0 |
| `go test -p 1 ./...`（`DATABASE_DEFAULT_PASS=root`） | PASS | 全部 ok（含 `internal/auth`、`internal/boot`、`internal/cmd`、`internal/migrations`、`internal/controller/{iam,order,cart,inventory,product,sku,admin,address,categories}`、`internal/logic/{iam,flashsale,legacymigrate}` 等） |
| AC-001 JWT 缺失 fail-fast | PASS | `serve` 无 `AUTH_JWT_SECRET`：exit=1，`未配置 auth.jwt.secret`（不回退默认密钥） |
| AC-002 JWT 过短 fail-fast | PASS | `serve` + `AUTH_JWT_SECRET=short`：exit=1，`auth.jwt.secret 长度至少为 32 字节` |
| AC-003 MySQL 密码缺失 fail-fast（serve） | PASS | `serve` 无 `DATABASE_DEFAULT_PASS`：exit=1，`未配置 database.default.pass`（不回退 root） |
| AC-003 MySQL 密码缺失 fail-fast（migrate） | PASS | `migrate up` 无 `DATABASE_DEFAULT_PASS`：exit=1，`未配置 database.default.pass` |
| AC-004 compose 无默认回退 | PASS | `docker-compose.yml` 无 `${VAR:-default}` 回退；无 `.env` 时 `docker compose config -q` 报 `required variable AUTH_JWT_SECRET is missing`（exit 1） |
| AC-005 显式测试凭据注入 | PASS | `DATABASE_DEFAULT_PASS=root` 下 `migrate version` 连接成功（`version: 20261001000023 (dirty=false)`），注入不依赖代码内置默认值 |
| AC-006 连接字符集统一 utf8mb4（5 处） | PASS | compose L59 / config.yaml L19 / boot.go L26 / migrations.go L36 / legacymigrate db.go L77 均为 utf8mb4，无 `defaultDBPass` 残留 |
| AC-007 四字节 Unicode 读写 | PASS | `TestUTF8MB4FourByteRoundtrip`（真实 MySQL 2.64s）PASS；直接验证 utf8mb4 连接写入 `😀 🎉 𝄞` 读回一致（HEX `F09F988020F09F8E8920F09D849E`），utf8mb3 连接写入同一内容失败 |
| AC-008 无回归（IAM/订单等） | PASS | `go vet`/`go test -p 1`/`go build` 全通过，IAM/订单/购物车/库存/商品/SKU/后台测试 ok |
| AC-009 docs/design 一致 | PASS | `deployment.md` §3/§10（无内置默认回退 + utf8mb4）与 `migration.md` §5（DSN 字符集）与实现一致 |
| serve 正向启动（in-scope 边界） | PASS | `serve` + 合法 DB pass + 32 字节 JWT：`all dependencies are reachable (mysql, redis)` → `schema 已就绪` → 达超管 seed 边界（无 JWT/DB pass 报错） |

## Acceptance Evidence
- INV-001（无敏感凭据默认回退）：serve/migrate 在密码/JWT 缺失或过短时均非零退出并指认字段，不回退 `root` 或任何内置密钥（T1–T4 本次运行证据）。
- INV-002（连接层字符集 utf8mb4）：四字节字符经应用连接写入并读回一致（`TestUTF8MB4FourByteRoundtrip` + 直接 DB 探针），utf8mb3 连接无法写入，可区分正确/错误实现。
- AC-004/006：compose `${VAR:?}` 必填 + 无回退 + 5 处字符集统一，均本次静态证据。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 完整 HTTP `serve` 到监听端口 | 需真实七牛凭据（`QINIU_ACCESS_KEY/SECRET_KEY/BUCKET/DOMAIN`），本任务 Out of Scope，未提供 | 低：七牛凭据处理不在本任务范围；serve 的配置/JWT/依赖/schema 边界已在本环境验证到超管 seed 边界 |
| 完整 `docker compose up` 容器栈 | 同上，需 `.env` 真实七牛 + 超管密码 | 低：compose 必填语义已由 `docker compose config` 验证（缺失即失败） |

## Remaining Risks
- 生产级完整 serve（含七牛）未在本环境演练，属本任务 Out of Scope 依赖，不影响本里程碑（config hardening + charset）结论。
- `CLEAN-001`（`scripts/import-legacy.sh` 残留 `MYSQL_ROOT_PASSWORD:-root`）：OPEN P3，不在本任务 Allowed Changes 清单，由 Owner 决定是否纳入。
- `CLEAN-002`（`scripts/test-nginx.sh` 无执行位，git mode `100644`）：既有问题，`make test` 末步会 Permission denied（exit 126），但核心 `go vet`/`go test`/`go build` 与三个静态断言脚本（经 `bash` 显式执行）均通过；不影响本任务 AC 结论。

## Result
PASS
