# Technical Contract

## Decision Status

APPROVED

## Problem

消除 JWT Secret 与 MySQL root 密码的内置默认回退，使不安全配置无法静默启动；将数据库连接层字符集从 `utf8`（utf8mb3，3 字节）统一为 `utf8mb4`，使四字节 Unicode 字符可正确读写；保持 IAM、订单等数据库功能无回归。表结构已为 utf8mb4，本次仅改连接层，不新增 migration、不改表结构、不新增业务错误码域（不占用全局资源）。

## Verified Current Behavior

- VERIFIED：`docker-compose.yml` 存在固定默认回退：`MYSQL_ROOT_PASSWORD: ${MYSQL_ROOT_PASSWORD:-root}`（L7）、`DATABASE_DEFAULT_PASS: "${MYSQL_ROOT_PASSWORD:-root}"`（L56）、`AUTH_JWT_SECRET: "${AUTH_JWT_SECRET:-c49d0fea...}"`（L64）。
- VERIFIED：代码级隐式回退默认值：`internal/boot/boot.go` L25 `defaultDBPass = "root"`、L27 `defaultDBCharset = "utf8"`；`internal/migrations/migrations.go` L35 `defaultDBPass = "root"`、L37 `defaultDBCharset = "utf8"`；`internal/logic/legacymigrate/db.go` L18 `defaultDBPass = "root"`、L73 DSN 硬编码 `charset=utf8`。
- VERIFIED：`manifest/config/config.yaml` 已清空敏感默认值：`database.default.pass: ""`（L17）、`auth.jwt.secret: ""`（L45）；`charset: "utf8"`（L19）。
- VERIFIED：GoFrame v2.10.3 `gcfg.Config.GetEffective` 优先级为：命令行 → 环境变量（`os.LookupEnv`，空但已设置也算已设置）→ 配置文件 → `def`。config.yaml 已含 `pass: ""` 与 `secret: ""`，故正常路径下代码级 `defaultDBPass = "root"` 实际为死代码（仅在配置键完全不存在时才生效）；`lib.sh` L47-49 亦确认「空环境变量会覆盖 config 默认值」的既有认知。
- VERIFIED：JWT 校验已存在且 fail-fast：`internal/auth/jwt.go` `Secret()` 空值报错、`<32` 字节报错；`internal/boot/boot.go` `Bootstrap()` 调用 `auth.Secret()` 并在出错时返回。JWT 校验仅在 `serve` 路径执行；`migrate` 子命令（`internal/cmd/cmd.go` `migrateUp`）不校验 JWT，也不使用 JWT（纯 DDL，仅需 MySQL 凭据）。
- VERIFIED：连接字符集为 `utf8` 共 5 处：`docker-compose.yml` L58、`manifest/config/config.yaml` L19、`internal/boot/boot.go` L27、`internal/migrations/migrations.go` L37、`internal/logic/legacymigrate/db.go` L73。
- VERIFIED：所有迁移 DDL 均为 `ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`；`internal/migrations/migrations_test.go` `expectedSchema` 断言表 Collation 为 `utf8mb4_unicode_ci`（当前通过）。四字节问题在连接层（DSN `charset=utf8` 即 utf8mb3，写入 4 字节字符会报 `Incorrect string value`），非表结构问题。
- VERIFIED：测试凭据注入现状：`scripts/test.sh` L19 `export DATABASE_DEFAULT_PASS="${DATABASE_DEFAULT_PASS:-root}"`（隔离测试环境显式注入开发测试凭据，不加载 `.env`）；`make test` 串行 `go test -p 1 ./...` + 容器化静态断言。CI `.cnb.yml` 的 `start-dependencies` 阶段执行 `docker compose up -d` 后 `make test`，当前依赖 compose `${VAR:-root}` 默认值与 test.sh 的 `:-root` 兜底。
- VERIFIED：`scripts/init.sh` `REQUIRED_VARS` 不含 `MYSQL_ROOT_PASSWORD`/`AUTH_JWT_SECRET`；`.env.example` 二者留空并注明「留空时 docker-compose.yml 回退 dev 默认值」。
- VERIFIED：`docs/design/deployment.md` §3 L36 记载「开发测试凭据（如 MySQL root）仅出现在 docker-compose.yml 的 ${VAR:-default}」；`docs/design/migration.md` §5 记载「DSN 从 database.default.* 配置构造」但未明确字符集。
- UNKNOWN：MySQL 8.0 容器服务器级 `character_set_server` 当前取值未直接运行验证（推断为 8.0 默认 utf8mb4，且所有表显式指定 utf8mb4，客户端 `charset=utf8mb4` 即 `SET NAMES utf8mb4` 足以满足四字节读写）。
- UNKNOWN：CI（CNB DinD）在移除 compose 默认值后，`MYSQL_ROOT_PASSWORD`/`AUTH_JWT_SECRET` 的显式注入渠道是否已存在（是否可经 CNB CI 变量/Secret 提供）。

## Recommendation

RECOMMENDATION：统一采用「无默认回退 + 显式注入 + fail-fast」的单一路径，不引入任何运行时内置敏感凭据默认值；连接层字符集全部改为 `utf8mb4`，不设置 MySQL 容器服务器级字符集参数。

1. **移除 compose 固定默认回退**：`docker-compose.yml` 中 `MYSQL_ROOT_PASSWORD`、`DATABASE_DEFAULT_PASS`、`AUTH_JWT_SECRET` 不再使用 `${VAR:-固定默认值}`；`mysql` 服务 `MYSQL_ROOT_PASSWORD` 与 `app` 服务 `AUTH_JWT_SECRET` 改为必填（`${VAR:?...}` 或直接 `${VAR}`，使未显式提供时 `docker compose up` 失败）。`.env.example` 同步更新为「必填、无回退」说明。
2. **清理代码级默认密码**：删除 `internal/boot/boot.go` 与 `internal/migrations/migrations.go` 的 `defaultDBPass = "root"`；密码缺失（config 空且无环境变量）时，DSN/配置节点密码为空串，MySQL 认证失败并终止，不回退 `root`。为提高错误可读性，建议在 `applyDatabaseConfig` 与 `buildDSN` 对空密码 fail-fast（指认 `DATABASE_DEFAULT_PASS`，对齐 `auth.Secret()` 的 fail-fast 语义）。
3. **字符集统一为 utf8mb4**：`docker-compose.yml` `DATABASE_DEFAULT_CHARSET`、`config.yaml` `database.default.charset`、`internal/boot/boot.go` `defaultDBCharset`、`internal/migrations/migrations.go` `defaultDBCharset` 全部改为 `utf8mb4`。
4. **JWT 校验边界**：仅在 `serve` 路径校验 JWT（现状已满足）；`migrate` 子命令不校验 JWT（迁移为纯 DDL，不签发/校验 token，无此需要）。
5. **测试凭据注入**：`scripts/test.sh` 保留显式开发测试凭据注入（`DATABASE_DEFAULT_PASS`），作为隔离测试环境的明确测试凭据，不依赖应用/迁移代码内置默认值；测试凭据来源与 `make init`/`make up` 实际启动 MySQL 的 root 密码保持一致。
6. **测试增强**：补充四字节 Unicode 读写回归测试（写入 emoji / U+1D11E 并读回，断言一致），且该测试在连接字符集为 `utf8` 时失败、`utf8mb4` 时通过（能区分正确/错误实现）；补充「缺失密钥（空）拒绝」负向测试（`auth.Secret()` 对空值返回错误）；补充「密码缺失不回退 root」负向测试（`buildDSN`/`applyDatabaseConfig` 空密码路径）。

关键取舍：本次核心是**移除默认回退的失败语义**与**连接层字符集**，代价是开发/CI 引导流程必须先显式注入真实凭据（不再「开箱即用」）；这是消除「不安全配置静默启动」安全风险的必然成本。

## Selected Design

1. **CI 凭据注入渠道**：使用 CNB CI Secret/变量显式注入；不在 `.cnb.yml` 或代码中增加任何固定默认凭据；本任务不负责 CI Secret 创建与流水线设计，仅保证应用配置机制支持显式注入。禁止任何 `${VAR:-default}` 形式的安全敏感默认值，缺少必要变量必须失败。

2. **legacymigrate 纳入范围**：`internal/logic/legacymigrate/db.go` 存在同类问题（`defaultDBPass = "root"`、DSN 硬编码 `charset=utf8`），与主路径一并处理。所有数据库连接路径（`internal/boot/boot.go`、`internal/migrations/migrations.go`、`internal/logic/legacymigrate/db.go`）统一：移除默认密码回退、连接字符集统一为 `utf8mb4`。

3. **Compose 必填机制（双层 fail-fast）**：
   - 第一层：`docker-compose.yml` 使用 `${VAR:?error}` 必填语法（如 `MYSQL_ROOT_PASSWORD: ${MYSQL_ROOT_PASSWORD:?MYSQL_ROOT_PASSWORD required}`），禁止 `${VAR:-default}`。
   - 第二层：`scripts/init.sh` 将 `MYSQL_ROOT_PASSWORD`、`AUTH_JWT_SECRET` 加入 `REQUIRED_VARS`，缺少时输出明确错误，空字符串不能绕过校验。

4. **启动阶段显式 fail-fast（空密码/JWT Secret 校验）**：
   - 校验 `DATABASE_DEFAULT_PASS` 非空、`AUTH_JWT_SECRET` 非空（JWT 已由 `auth.Secret()` 校验，密码在 `applyDatabaseConfig`/`buildDSN` 新增显式非空校验）。
   - 失败返回明确错误信息、非零退出、不启动 HTTP Server。
   - 清理所有 `defaultDBPass` 回退：`internal/boot/boot.go`、`internal/migrations/migrations.go`、`internal/logic/legacymigrate/db.go`。
   - 修改所有 `charset=utf8` 为 `utf8mb4`：`docker-compose.yml`、`manifest/config/config.yaml`、`internal/boot/boot.go`、`internal/migrations/migrations.go`、`internal/logic/legacymigrate/db.go`。
   - 删除 compose 固定默认安全配置。
   - 增加配置缺失/空值负向测试、utf8mb4 四字节字符读写回归测试。

不包含：CNB Secret 创建、CI/CD 流水线设计、Kubernetes Secret 管理、镜像发布流程。

## Interfaces and Data

- 保持 `my-shop serve` / `my-shop migrate up|force|version` / `qiniu check` / `admin check` / `legacy-migrate` 子命令与公开 HTTP API、错误码、响应格式不变。
- 保持配置键与环境变量契约不变：`database.default.pass` / `DATABASE_DEFAULT_PASS`、`database.default.charset` / `DATABASE_DEFAULT_CHARSET`、`auth.jwt.secret` / `AUTH_JWT_SECRET`。
- 连接层字符集统一为 `utf8mb4`；`config.yaml` 敏感字段保持空串。
- 不新增 migration、不改表结构、不新增业务错误码域、不占用 migration version / error_code_domain 全局资源。

## Business Invariants

- INV-001（无敏感凭据默认回退）：JWT Secret 与 MySQL 密码在配置为空且无对应环境变量时，serve 与 `migrate up` 均不回退任何内置默认值（JWT 报「未配置」、密码不回退 `root`），进程以非零退出、不监听端口、不静默启动。
- INV-002（连接层字符集 utf8mb4）：所有数据库连接层（应用、migration、legacy-migrate、docker-compose）字符集为 utf8mb4，四字节 Unicode 字符经连接写入并读回一致，无截断/报错。

## Failure and Consistency Semantics

- 事实来源：MySQL 为业务数据与 migration 状态权威源；本任务不引入新的多存储/异步一致性语义。
- serve 成功 = Bootstrap（依赖连通 + schema 就绪 + JWT 校验 + seed）+ 七牛校验全部通过并监听端口；JWT 缺失/过短、MySQL 密码缺失 → 非零退出，错误指认字段名，不泄漏凭据值。
- `migrate up` 成功 = 所有未应用 migration 按序执行成功；密码缺失 → DSN 密码为空 → MySQL 认证失败 → 非零退出，不回退 `root`。
- 无 Redis/MQ 一致性变化；不新增重试/部分完成语义（沿用既有 migration dirty 恢复模型）。

## Allowed / Forbidden Changes

- 允许：修改 `docker-compose.yml`、`manifest/config/config.yaml`、`internal/boot/boot.go`、`internal/migrations/migrations.go`、`internal/logic/legacymigrate/db.go`、`scripts/init.sh`、`scripts/test.sh`、`.env.example`、`docs/design/deployment.md`、`docs/design/migration.md` 及新增测试文件。
- 禁止：改动业务逻辑（`internal/logic/*` 除 legacymigrate/db.go 外、`internal/controller/*`、`api/*`）；新增 migration、改表结构；新增业务错误码域、占用全局资源；改动 Redis 密码、七牛凭据、超管密码处理；引入 Kubernetes/HTTPS/TLS/多实例；在 `.cnb.yml` 或代码中增加任何固定默认凭据。

## Verification Requirements

- INV-001 → 单测：`auth.Secret()` 在空值返回错误；`buildDSN`/`applyDatabaseConfig` 在密码缺失时密码为空串（非 `root`），以空密码连接真实 MySQL 认证失败；`docker compose config` 在未提供必填变量时失败。
- INV-002 → 真实 MySQL 集成测试：经统一连接向 utf8mb4 表写入 4 字节字符（emoji / U+1D11E）并读回一致；连接字符集为 `utf8` 时该测试必须失败、`utf8mb4` 时通过。
- AC-008 → `go vet ./...`、`go test -p 1 ./...`、`go build ./...` 通过，IAM/订单等现有测试无回归。

## Open Risks

- CI Secret 创建与流水线设计不在本任务范围，由 CI/Owner 负责；本任务仅保证应用配置机制支持显式注入。若 CI 未及时注入 `MYSQL_ROOT_PASSWORD`/`AUTH_JWT_SECRET`，`docker compose up` 将按预期失败（fail-fast，非静默降级）。
- `make init`/`make up` 需先填写 `.env` 中 `MYSQL_ROOT_PASSWORD`/`AUTH_JWT_SECRET`；首次开发者体验从「开箱即用」变为「先填凭据」，需在 `.env.example` 注释与 `docs/design/deployment.md` 同步。

## Owner Decision Record

- Decision 1（CI 凭据注入渠道）：采用 CNB CI Secret/变量显式注入；不在 `.cnb.yml` 或代码中增加固定默认凭据；本任务不负责 CI Secret 创建与流水线设计，仅保证应用配置机制支持显式注入；禁止 `${VAR:-default}` 安全敏感默认值，缺变量必须失败。
- Decision 2（legacymigrate 纳入范围）：纳入本任务；`internal/logic/legacymigrate/db.go` 的 `defaultDBPass = "root"` 与 `charset=utf8` 与主路径一并清理，所有数据库连接路径统一移除默认密码回退、charset 统一为 utf8mb4。
- Decision 3（Compose 必填机制）：双层 fail-fast——第一层 `docker-compose.yml` 使用 `${VAR:?error}`；第二层 `scripts/init.sh` 将 `MYSQL_ROOT_PASSWORD`/`AUTH_JWT_SECRET` 加入 `REQUIRED_VARS`，缺变量输出明确错误，空字符串不能绕过校验。
- Decision 4（空密码/JWT Secret 校验）：启动阶段显式 fail-fast，校验 `DATABASE_DEFAULT_PASS` 与 `AUTH_JWT_SECRET` 非空，失败返回明确错误、非零退出、不启动 HTTP Server；清理所有 `defaultDBPass` 回退、所有 `charset=utf8` 改为 utf8mb4、删除 compose 固定默认安全配置、增加负向测试与四字节字符读写回归测试；不包含 CNB Secret 创建、CI/CD 流水线设计、Kubernetes Secret 管理、镜像发布流程。
