# Task: Backend Config Hardening V1 — 安全配置加固与数据库字符集统一

## Goal

消除 JWT Secret 与 MySQL root 密码的内置默认回退，使不安全配置无法静默启动；将 Go 应用、Migration、Docker Compose 的数据库连接字符集统一为 utf8mb4，使数据库能正确读写四字节 Unicode 字符；并保持现有 IAM、订单等数据库功能无回归。

## Scope

- 移除 Docker Compose 中固定 JWT Secret 默认值回退，缺失或不符合要求时应用启动失败（非零退出、不监听端口、不回退内置默认密钥）。
- 清理 Go 应用（`internal/boot`）与 Migration（`internal/migrations`）中 MySQL root 密码的隐式回退默认值。
- 保留隔离测试环境显式注入测试凭据（MySQL 密码、JWT Secret）的能力，且该能力不依赖应用/迁移代码的内置默认值。
- 将 Go 应用连接、Migration DSN、Docker Compose 三处数据库连接字符集统一为 utf8mb4（现有表已为 utf8mb4，本次仅改连接层，无需新增 migration）。
- 增加四字节 Unicode 字符写入、读取回归测试。
- 增加缺失密钥、无效密钥、缺失数据库凭据的负向测试。
- 更新项目级长期设计 `docs/design/deployment.md`（凭据无默认回退、连接字符集 utf8mb4），并同步 `docs/design/migration.md` 的 DSN 字符集说明。
- 验证现有 IAM、订单及其他数据库功能无回归。

## Out of Scope

- 不改变业务逻辑、公开 HTTP API、错误码语义与响应格式。
- 不新增数据库 migration、不改表结构（表已为 utf8mb4，无 DDL 需求）。
- 不新增业务错误码域、不占用全局资源（migration version / error_code_domain）。
- 不做 Kubernetes/Ingress、HTTPS/TLS、多实例部署。
- 不涉及 Redis 密码、七牛凭据、超管密码等本任务范围之外的凭据处理（保持现状）。

## Milestone

Milestone: backend-config-hardening-v1

## Design Impact

Design Impact: UPDATE
Design Artifact: docs/design/deployment.md（并同步 docs/design/migration.md 的 DSN 字符集说明）

## Acceptance Criteria

- [ ] AC-001：给定 JWT Secret 未配置（config 为空且无 `AUTH_JWT_SECRET` 环境变量），当应用启动时，进程以非零退出并指认缺失字段，不监听端口、不回退任何内置默认密钥。
- [ ] AC-002：给定 JWT Secret 已配置但长度不足 32 字节，当应用启动时，进程失败退出，不回退默认密钥。
- [ ] AC-003：给定 MySQL 密码未显式配置（config 为空且无 `DATABASE_DEFAULT_PASS` 环境变量），应用启动与 `my-shop migrate up` 均不得回退到 `root` 等内置默认密码，而是认证失败并终止，不静默启动。
- [ ] AC-004：`docker-compose.yml` 中 JWT Secret 与 MySQL root 密码不再存在 `${VAR:-固定默认值}` 形式的回退；未在 `.env`/环境变量显式提供时，对应服务无法启动。
- [ ] AC-005：隔离测试环境（`make test`）仍能显式注入测试 MySQL 凭据与 JWT Secret，各集成测试可连接 MySQL 并通过，且该注入不依赖应用/迁移代码的内置默认值。
- [ ] AC-006：Go 应用连接、Migration DSN 与 Docker Compose 三处数据库连接字符集统一为 utf8mb4，现有 utf8mb4 表读写语义不变。
- [ ] AC-007：经统一后的连接写入四字节 Unicode 字符（如 emoji / 补充平面字符）到 utf8mb4 表并读回，读回值与原值一致、无截断或报错；且存在能区分「utf8 连接（错误实现）」与「utf8mb4 连接（正确实现）」的回归测试。
- [ ] AC-008：现有 IAM、订单及其它数据库相关测试全部通过，无本次改动引入的回归。
- [ ] AC-009：`docs/design/deployment.md` 与最终实现一致，准确反映「敏感凭据无内置默认回退、数据库连接字符集 utf8mb4」的项目级长期事实；`docs/design/migration.md` 的 DSN 说明同步反映 utf8mb4。

## Relevant Context

已核实事实：

- Docker Compose 固定默认值回退（`docker-compose.yml`）：JWT Secret `AUTH_JWT_SECRET: "${AUTH_JWT_SECRET:-c49d0fea...}"`（L64）；MySQL root 密码 `MYSQL_ROOT_PASSWORD: ${MYSQL_ROOT_PASSWORD:-root}`（L7）与 `DATABASE_DEFAULT_PASS: "${MYSQL_ROOT_PASSWORD:-root}"`（L56）。
- 代码级隐式回退：`internal/boot/boot.go` L25 `defaultDBPass = "root"`；`internal/migrations/migrations.go` L35 `defaultDBPass = "root"`。
- `manifest/config/config.yaml` 已清空敏感默认值：`auth.jwt.secret: ""`（L45）、`database.default.pass: ""`（L17）。
- JWT 校验已存在且 fail-fast：`internal/auth/jwt.go` `Secret()` 对空值返回「未配置 auth.jwt.secret」、对 `<32` 字节返回错误；`internal/boot/boot.go` `Bootstrap()` 调用 `auth.Secret()` 并在出错时返回错误（serve 启动失败）。
- 连接字符集当前为 `utf8`（3 字节）共 4 处：`docker-compose.yml` L58 `DATABASE_DEFAULT_CHARSET: "utf8"`、`manifest/config/config.yaml` L19 `charset: "utf8"`、`internal/boot/boot.go` L27 `defaultDBCharset = "utf8"`、`internal/migrations/migrations.go` L37 `defaultDBCharset = "utf8"`。
- 表结构已为 utf8mb4：baseline 及全部迁移均为 `ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`；`internal/migrations/migrations_test.go` 的 `expectedSchema` 断言所有表 Collation 为 `utf8mb4_unicode_ci`（当前通过）。因此四字节问题出在连接层（DSN `charset=utf8` 即 utf8mb3，无法传输 4 字节），而非表结构。
- 测试凭据注入现状：`scripts/test.sh` L19 `export DATABASE_DEFAULT_PASS="${DATABASE_DEFAULT_PASS:-root}"`（隔离测试环境显式注入开发测试凭据，不加载 `.env`）；`make test` 已串行 `go test -p 1 ./...` + 容器化静态断言。
- 现有 JWT 负向测试：`internal/auth/jwt_test.go` 已覆盖「有效密钥」与「过短密钥拒绝」；缺少「缺失密钥（空）拒绝」负向测试。
- 现有静态断言：`scripts/test-container.sh` 已断言 `config.yaml` 的 `auth.jwt.secret` 与 `database.default.pass` 为空串（不覆盖代码级 `defaultDBPass = "root"`）。
- `.env.example` 中 `MYSQL_ROOT_PASSWORD=`、`AUTH_JWT_SECRET=` 均留空，注释说明「留空时 docker-compose.yml 回退 dev 默认值」；`scripts/init.sh` 的 `REQUIRED_VARS` 不含二者，依赖 compose 默认值。
- 设计文档：`docs/design/deployment.md` §3 L36 记载「开发测试凭据（如 MySQL root）仅出现在 docker-compose.yml 的 ${VAR:-default}」，本任务后此事实失效；`docs/design/migration.md` §5 记载「DSN 从 database.default.* 配置构造」但未明确字符集。

Assumption：

- 表结构已为 utf8mb4，本次仅需改连接层字符集即可实现四字节读写，无需数据迁移或 DDL。
- 移除 compose 默认回退后，开发/CNB DinD 环境仍可通过 `.env` 或显式环境变量注入真实凭据，不要求引入新的凭据管理机制。

## Verification

- AC-001 → 单元测试：`auth.Secret()` 在 config 为空且无 `AUTH_JWT_SECRET` 时返回错误；或以缺失密钥启动 serve 断言进程非零退出。预期：返回/退出失败，无默认密钥回退。
- AC-002 → 复用/补充 `internal/auth/jwt_test.go`：`Secret()` 对 `<32` 字节返回错误。预期：失败。
- AC-003 → 单元测试：`internal/migrations.buildDSN` 与 `internal/boot.applyDatabaseConfig` 在密码缺失（config 空且无环境变量）时产生的 DSN/配置节点密码为空串（非 `root`）；以空密码连接真实 MySQL 时迁移/连接认证失败。预期：不回退 `root`。
- AC-004 → 静态检查 `docker-compose.yml` 无 `${VAR:-root}` / `${VAR:-固定密钥}` 回退；`docker compose config` 在未提供变量时对应值缺失（或服务启动失败）。
- AC-005 → 运行 `make test`（`scripts/test.sh` 显式注入测试凭据），集成测试连接 MySQL 通过；且移除应用/迁移代码默认值后仍通过。
- AC-006 → 静态核对 4 处连接字符集均为 `utf8mb4`；`go test ./...` 通过。
- AC-007 → 真实 MySQL 集成测试：经统一连接向 utf8mb4 表（如 `articles.content` 或 `categories.name`）写入 4 字节字符（emoji / U+1D11E）并读回，断言读回值与原值一致；该测试在连接字符集为 `utf8` 时必须失败、为 `utf8mb4` 时通过。
- AC-008 → `go vet ./...`、`go test -p 1 ./...`、`go build ./...` 通过；现有 IAM、订单相关测试无回归。
- AC-009 → 核对 `docs/design/deployment.md`（凭据无默认回退 + 连接字符集 utf8mb4）与 `docs/design/migration.md`（DSN 字符集）与最终实现一致。

## Complexity

COMPLEX

原因：本任务触及重要安全边界（移除敏感凭据内置默认回退、定义「不安全配置不静默启动」的失败语义），且移除 compose 默认值后开发/测试环境的引导流程存在多个现实方案（`.env` 必填项与 `init.sh` 阻断、`test.sh` 测试凭据注入形态、「所有环境」的校验边界、是否同步设置 MySQL 容器服务器级字符集），不同方案会产生不同的运维与安全结果；同时 Design Impact = UPDATE，需 Analyst 负责更新 `docs/design/deployment.md`。按 Owner 约束交 Analyst 分析并由 Owner 决定。

## Analyst Questions

- GoFrame `GetEffective` 对 `database.default.pass: ""`（空字符串但键存在）的语义：空值是否被当作「已配置」从而不回退到 `defaultDBPass`；清理代码级 `defaultDBPass = "root"` 后，空密码是否会导致以空密码连接 MySQL（与「显式缺失」的区分），以及 `cfgString` 空值兜底是否需要一并处理。
- 移除 compose `${VAR:-default}` 后，开发/CNB DinD 环境的引导流程如何提供 `MYSQL_ROOT_PASSWORD` 与 `AUTH_JWT_SECRET`：是否将二者加入 `scripts/init.sh` 的 `REQUIRED_VARS` 并在缺失时阻断（fail-fast），还是采用其它显式注入机制；`scripts/test.sh` 的 `DATABASE_DEFAULT_PASS="${DATABASE_DEFAULT_PASS:-root}"` 是否保留 `:-root` 还是改为显式测试凭据。
- 「所有环境」的 JWT Secret 校验边界：是否需要在 `migrate` 子命令路径也校验 JWT Secret（当前仅在 `serve` 的 `Bootstrap` 校验），还是仅 `serve` 即可满足「缺失/不合规即启动失败」。
- 字符集统一是否需要同时设置 MySQL 容器的服务器级默认字符集/排序规则（如 `--character-set-server=utf8mb4`），还是仅改客户端连接 charset 即足以满足四字节读写目标（需确认现有存量库默认值不造成新差异）。

## Review Baseline

- Base commit：`359b8cceeebefe7b490f783c1ad5a118afd6e31e`（分支 `feat/backend-config-hardening-v1`，local == `origin/feat/backend-config-hardening-v1`）。
- 任务开始时已有修改：无（working tree clean，无 staged / unstaged / untracked 变更）。
- 重叠修改的区分方式：本任务改动集中在 `docker-compose.yml`、`manifest/config/config.yaml`、`internal/boot/boot.go`、`internal/migrations/migrations.go`、`scripts/init.sh`、`scripts/test.sh`、`.env.example`、`docs/design/deployment.md`、`docs/design/migration.md` 及新增测试文件；Coder 不得改动业务逻辑（`internal/logic/*`、`internal/controller/*`、`api/*`），除为字符集/凭据校验必需的最小配置接入外不触碰无关模块。

## Initial Route

交 Analyst（COMPLEX）。
