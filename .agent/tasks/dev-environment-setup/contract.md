# Technical Contract

## Decision Status
APPROVED

## Problem
修复 my-shop 开发环境配置与初始化流程（`make init → 填 .env → make up` 全链路 + `make test` 环境变量隔离 + `make test-storage` 保留真实七牛 E2E）。

两个需固化的点：
1. 根因（空值覆盖）：`.env` 空值经 `scripts/lib.sh` 导出为空环境变量，GoFrame `GetEffective` 把「空但已设置」视为覆盖 `config.yaml` 非空默认，导致 `AUTH_JWT_SECRET=` / `DATABASE_DEFAULT_PASS=` 覆盖默认 secret / `root` 密码而启动失败。
2. `make up` 启动前预检中「超级管理员创建条件」的判定机制（受 AC-006「启动后端前集中校验、失败不启动后端」与 AC-008「首次创建 vs 已存在幂等」约束）。

## Verified Current Behavior
- VERIFIED：配置优先级「命令行 > 环境变量 > config.yaml > 默认值」，命名「`.`→`_` 大写」；读取点 `internal/boot/boot.go` `cfgString` 走 `g.Cfg().GetEffective`，`internal/auth/jwt.go`/`session.go`/`refresh.go`、`internal/logic/upload/upload.go`、`internal/cmd/cmd.go` 同源。
- VERIFIED：空值覆盖根因——`scripts/lib.sh` `_load_env_file`（L32-55）把 `.env` 的 `KEY=` 以 `export KEY=""` 导出，`GetEffective` 命中「空但已设置」覆盖 config.yaml 非空默认。
- VERIFIED：config.yaml 开发默认——`server.address=:8000`、`database.default.*`（`pass=root`）、`redis.default.*`、`auth.jwt.secret` 非空（64hex=32 字节）、`auth.session.ttl=3600`、`auth.refresh.ttl=2592000`、`admin.super.username=admin`、`admin.super.password=""`（刻意无默认）、`storage.local.root=./storage`、`qiniu.*`（AK/SK/bucket/domain 空，其余有默认）、`startup.dependency.timeout=30`。
- VERIFIED：JWT 密钥 `Secret` 要求 ≥32 字节，空/过短启动 fail-fast（`internal/auth/jwt.go`，`minSecretLength=32`）。
- VERIFIED：超管 seed 幂等（`internal/boot/seed.go` `seedSuperAdmin`）：`is_super=1` 存在则跳过不覆盖密码；不存在且 `admin.super.password` 空则 fail-fast；并发靠 `admins.username` 唯一约束 1062 兜底（`isDuplicateKeyError` 判断 mysql 1062）。
- VERIFIED：`make up` 现序 = deps → build → `qiniu check` → migrate → start_app（`scripts/up.sh`）；`qiniu check` 复用 `service.Upload().ValidateConfig`（结构+存在性+GetBucketInfo）。
- VERIFIED：`make test` = `source lib.sh`（会加载 `.env`）→ `go vet` + `go test -p 1`；仅 QINIU_* 已有隔离（commit `6e3f1f9`），其余可覆盖键未隔离。
- VERIFIED：`make test-storage` 校验 QINIU_* 四变量非空后跑 `storage_e2e`，缺凭据非零退出。
- UNKNOWN：无。

## Recommendation
（Owner 决策前的推荐，此处仅作过程留痕。）

方案 A 行为目标：`make up` 启动后端前识别超管是否存在、集中报告缺失配置。实现载体由 Owner 定为 **Go 复用现有数据库访问与超管判断逻辑**，禁止 `docker compose exec mysql` + Shell SQL。

## Selected Design
Owner 2026-10-09 决定，按以下约束实现：

1. **超管创建条件预检（Go 复用，不写 Shell SQL）**
   - 在 `internal/boot` 提取只读判断 `superAdminExists(ctx) (bool, error)`：查 `admins` 是否含 `is_super=1`；`admins` 表不存在（mysql 1146，仿 `isDuplicateKeyError` 的错误码判断）视为「不存在」，其它 DB 错误返回 error（fail-closed）。
   - `seedSuperAdmin` 改用 `superAdminExists`，**保持原有幂等与 fail-fast 语义不变**（已存在跳过不覆盖；不存在且密码空 fail-fast；1062 兜底）。
   - 在 `internal/boot` 新增导出入口 `CheckSuperAdminCondition(ctx) error`：复用 `applyDatabaseConfig` + `cfgString` + `superAdminExists`，返回——超管存在 → nil；不存在且 `admin.super.password` 空 → 指认 `ADMIN_SUPER_PASSWORD` 的错误；不存在且密码非空 → nil（后续 seed 创建）。
   - 在 `internal/cmd` 新增子命令 `my-shop admin check`：薄包装调 `boot.CheckSuperAdminCondition`，通过 exit 0、失败非零、错误只指认键名不泄漏密码值/堆栈（仿 `qiniu check`）。

2. **`make up` 预检顺序**：deps 就绪 → build → `qiniu check`（七牛 required + 结构）→ `admin check`（超管条件）→ migrate → start_app。预检阶段一次性报告全部可预先发现的缺失/非法项（键名/依赖名级别），任一失败非零退出、不启动后端、不泄漏 Secret（AC-006/AC-007/AC-012）。

3. **空值覆盖修复（`lib.sh`）**：`_load_env_file` 仅在值非空时 `export`（`KEY=` 不导出），保持「已导出环境变量 > `.env` 非空值 > config.yaml 默认」优先级（AC-003）。

4. **`make init` 缺项检测**：对比 `.env.example` 与既有 `.env`，检测缺键并一次性提示全部必填项（七牛 4 项 + 超管初始密码），只提示、不改写、不打印已填 Secret（AC-004/AC-005）。

5. **`make test` 隔离**：不加载 `.env` / 不传播真实凭据，`go vet` + `go test -p 1 ./...` 与本地 `.env` 无关；`make test-storage` 保留真实七牛 E2E，缺凭据非零退出（AC-010/AC-011）。

6. 不引入新依赖/新配置框架，复用 GoFrame `g.Cfg`/`gdb` 与现有 `boot` 逻辑。

Design Impact = NONE：不改 `docs/design/*`；超管预检为运维子命令，不属 storage 域；`seedSuperAdmin` 语义不变，仅提取只读函数。

## Interfaces and Data
需要保持/建立的稳定契约：
- 配置键目录（`.env.example ↔ config.yaml ↔ 读取点`一致）：`SERVER_ADDRESS` / `DATABASE_DEFAULT_{TYPE,HOST,PORT,USER,PASS,NAME,CHARSET,DEBUG}` / `REDIS_DEFAULT_{ADDRESS,DB,PASS}` / `AUTH_JWT_SECRET` / `AUTH_SESSION_TTL` / `AUTH_REFRESH_TTL` / `ADMIN_SUPER_{USERNAME,PASSWORD}` / `ORDER_PAY_TIMEOUT` / `ORDER_CANCEL_SCAN_INTERVAL` / `FLASH_SALE_RECONCILE_SCAN_INTERVAL` / `STORAGE_LOCAL_ROOT` / `QINIU_{ACCESS_KEY,SECRET_KEY,BUCKET,DOMAIN,REGION,TOKEN_TTL,MAX_FILE_SIZE,ALLOWED_EXTENSIONS,ALLOWED_MIME_TYPES}` / `STARTUP_DEPENDENCY_TIMEOUT`。
- `.env.example`：全部未注释 `KEY=VALUE`；普通配置填开发默认；真实 Secret（`QINIU_SECRET_KEY`、`AUTH_JWT_SECRET`、`ADMIN_SUPER_PASSWORD`）与七牛必填（`QINIU_ACCESS_KEY`/`QINIU_BUCKET`/`QINIU_DOMAIN`）留空；头注释写明加载优先级。
- `lib.sh` 加载语义：已导出环境变量 > `.env` 非空值 > config.yaml 默认；`.env` 空值不覆盖。
- 新 Go 接口：`boot.CheckSuperAdminCondition(ctx) error`（导出）、`superAdminExists(ctx) (bool, error)`（包内只读）、子命令 `my-shop admin check`。
- 不改 `config.yaml`、不改 `seedSuperAdmin` 幂等/fail-fast 语义、不改 `qiniu ValidateConfig`、不改上传业务逻辑。

## Business Invariants
- INV-001：`.env` 中 `KEY=`（空值）不覆盖 config.yaml 非空默认；非空值必须正常覆盖；已导出环境变量仍优先（AC-003）。
- INV-002：超管 seed 幂等——已有 `is_super=1` 时无论是否提供 `ADMIN_SUPER_PASSWORD` 都不覆盖既有密码、正常启动；不存在且密码为空时启动失败并明确提示（AC-008）。
- INV-003：`make up` 任何失败路径只指认缺失/非法键名或依赖名，不打印 Secret 值、密码、token、内部路径或堆栈（AC-007/AC-012）。

## Failure and Consistency Semantics
- `make up` 成功 = 依赖就绪 + 七牛配置/bucket 可用 + 超管条件满足 + 迁移完成 + 后端健康检查通过；不代表其它运行时保证。
- `make up` 预检任一失败 → 非零退出、不启动后端、一次性报告全部缺失/非法项（键名级别）。
- `admin check` 失败语义：超管不存在且密码空 → 非零退出指认 `ADMIN_SUPER_PASSWORD`；DB 错误 → 非零退出指认依赖（不泄漏密码）；超管存在或密码非空 → 通过。
- `make test` 在干净环境变量下运行（不加载 `.env` / 不传播真实凭据），结果与本地 `.env` 无关。
- `make test-storage` 缺真实凭据 → 非零退出并说明，不以退出 0 冒充通过。
- 无 Redis/MQ/跨库事务一致性语义。

## Allowed / Forbidden Changes
- 允许：改 `scripts/*.sh`、`Makefile`、`.env.example`；`internal/boot` 提取 `superAdminExists`、新增 `CheckSuperAdminCondition`；`internal/cmd` 新增 `admin check` 子命令（Owner 授权，复用现有 DB 访问与超管逻辑）；为测试隔离增改少量 Go 测试；必要时校验 `.gitignore`。
- 禁止：改 `internal/logic/upload`、`internal/storage` 上传业务；新增错误码/migration/全局资源；改 `config.yaml` 默认值或 `seedSuperAdmin` 幂等/fail-fast 语义；引入 dotenv 或新 Secret 加载依赖；用 `docker compose exec mysql` 写 Shell SQL 重复业务规则；覆盖已有 `.env`。

## Verification Requirements
- INV-001 → AC-003：`AUTH_JWT_SECRET=`/`DATABASE_DEFAULT_PASS=` 空值时启动仍读到 config.yaml 默认 secret/`root`；非空覆盖仍生效。
- INV-002 → AC-008：清空 admins 超管后缺密码 `make up` 失败并提示；已有超管且缺密码 `make up` 成功且密码不变。
- INV-003 → AC-007/AC-012：缺项/非法场景输出只含键名/依赖名，无 Secret。
- `admin check` 单测覆盖三态：超管存在、不存在且密码空、不存在且密码非空；以及 `admins` 表不存在（1146 → 视为不存在）。
- 其余按 `task.md` Verification 章节逐条执行（AC-001/002/004/005/006/009/010/011）。

## Open Risks
- 超管判定查询依赖 `DATABASE_DEFAULT_*` 与容器实际 root 凭据一致（默认 `root/root/my_shop`）；若开发者同时覆盖 `DATABASE_DEFAULT_PASS` 与 docker-compose `MYSQL_ROOT_PASSWORD` 且不一致，预检查询会失败（应 fail-closed 且不泄漏凭据）。属既有 docker-compose 与 config 命名割裂，本任务不改 docker-compose。
- `_load_env_file` 空值跳过只覆盖 `.env`；开发者在 shell 直接 `export AUTH_JWT_SECRET=` 仍会覆盖默认（超出 AC-003 范围）。
- task.md 的 Review Baseline/Complexity 仍表述为「改动面集中在 Makefile/scripts/.env.example + 少量 Go 测试」；本 Contract 已授权新增 `internal/boot`、`internal/cmd` 的 Go 预检代码，需 TaskBuilder 同步 task.md 描述（非阻塞，Owner 已授权）。

## Owner Decision Record
- 2026-10-09：Owner 选择方案 A 的行为目标（`make up` 启动后端前识别超管是否存在并集中报告缺失配置），但**不接受** `docker compose exec mysql` + Shell SQL 查询，要求**用 Go 复用现有数据库访问与超管判断逻辑**实现预检。适用范围：`make up` 启动前预检机制、空值覆盖修复、超管幂等保护。Design Impact = NONE。
