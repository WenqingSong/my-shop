# Task: 开发环境配置与初始化流程修复

## Goal

修复 my-shop 项目开发环境的配置与初始化流程，确保开发者执行 `make init → 填写 .env → make up` 后即可正常启动服务，不再因配置遗漏反复返工：`.env.example` 成为与 `config.yaml`/启动代码一致的完整配置清单，`make init` 能检测旧 `.env` 缺项并一次性提示全部必填项，`make up` 能在启动前集中校验依赖与配置并一次性报告缺失（错误不泄漏 Secret），`make test` 不受本地真实环境变量污染，`make test-storage` 保留真实七牛 E2E。整个过程不破坏现有数据库、容器、凭据与业务功能。

## Scope

- 全项目配置审计：梳理 MySQL、Redis、JWT、超级管理员、七牛、服务地址、本地存储、启动超时等全部可由环境变量覆盖的配置项，确认各自默认值、必填条件、首次初始化条件与加载优先级，产出「`.env.example` ↔ `config.yaml` ↔ 启动代码/设计文档」一致的配置清单。
- 修复 `.env.example`：所有配置项采用未注释 `KEY=VALUE` 格式（`#` 仅作说明注释）；普通配置填有效开发默认值，真实 Secret 留空；重点核对 `ADMIN_SUPER_PASSWORD`、`AUTH_JWT_SECRET` 与七牛配置；确保 `.env` 中空值不会意外覆盖 `config.yaml` 默认值。
- 完善 `make init`：准备必要容器与本地环境；`.env` 不存在时复制 `.env.example`、已存在时绝不覆盖；检测旧 `.env` 缺少的配置项；一次性提示开发者需填写的全部必填配置（不只七牛）。
- 完善 `make up`：启动前集中校验必要配置、一次性报告全部缺失项；正确处理超级管理员首次创建与已存在两种情形；校验 MySQL、Redis、七牛等必要依赖；校验失败不启动后端、错误信息不泄漏 Secret。
- 测试与验收：全新环境、已有 `.env` 升级与重复初始化、`make test` 不受真实环境变量污染、保留 `make test-storage` 真实七牛 E2E、失败时能明确定位配置问题。

## Out of Scope

- 不修改对象存储上传业务逻辑（`internal/logic/upload`、`internal/storage` 等既有实现）。
- 不引入 dotenv 或任何新的 Secret 加载依赖。
- 不新增/改写 `docs/design/*` 的长期架构事实；`docs/design/storage.md` §3/§10 已记录的「Secret 注入模型」「两阶段初始化」若因本任务加载语义微调而产生表述不一致，仅做一致性对齐，不改变其架构结论。
- 不新增错误码域、migration 或任何全局资源（本任务为 shell/config 工程，不占号）。
- 不实现交互式 Secret 输入（仅提示编辑 `.env`）。
- 不改变 `.env` 不进 Git、`.env.example` 不含真实凭据的安全边界。
- 不合并分支：本任务在 `feat/dev-environment-setup` 独立执行，合并回 `feat/object-storage-upload` 由 Owner 决定。

## Milestone

Milestone: dev-environment-setup 开发环境配置与初始化流程修复交付验收（`make init` → 填 `.env` → `make up` 全链路启动 + `make test` 环境变量隔离 + `make test-storage` 真实七牛 E2E）

## Acceptance Criteria

- [ ] AC-001（配置清单一致）：`.env.example` 完整覆盖开发环境全部可由环境变量覆盖的配置键（服务、MySQL、Redis、JWT、超级管理员、启动超时、本地存储、七牛），命名遵循「配置键 `.`→`_` 并大写」（如 `database.default.host`→`DATABASE_DEFAULT_HOST`），且与 `manifest/config/config.yaml`、`internal/boot/boot.go` 的读取点一致；加载优先级（已导出环境变量 > `.env` 非空值 > `config.yaml` 默认值）在 `.env.example` 头注释明确。
- [ ] AC-002（`.env.example` 格式与默认值）：所有配置项为未注释 `KEY=VALUE`（`#` 仅作说明注释）；普通配置填有效开发默认值；真实 Secret（`QINIU_SECRET_KEY`、`AUTH_JWT_SECRET`、`ADMIN_SUPER_PASSWORD` 等）留空；`QINIU_ACCESS_KEY`/`QINIU_BUCKET`/`QINIU_DOMAIN` 等必填项留空待填；`.env.example` 不含任何真实凭据。
- [ ] AC-003（空值不覆盖默认）：当 `.env` 中某键为 `KEY=`（空值）时，应用经 `g.Cfg().GetEffective` 读取该键仍得到 `config.yaml` 中的非空默认值（如 `AUTH_JWT_SECRET` 空 → 使用 config.yaml 开发默认 secret 正常启动；`DATABASE_DEFAULT_PASS` 空 → 使用 `root`），不因空值覆盖导致启动失败；`.env` 中非空值仍正常覆盖默认值。
- [ ] AC-004（`make init` 建/保 `.env`）：`make init` 在 `.env` 不存在时从 `.env.example` 复制生成；`.env` 已存在时保留、绝不覆盖；可重复执行，不破坏已运行容器与已有数据。
- [ ] AC-005（`make init` 缺项检测与提示）：`make init` 对比 `.env.example` 与既有 `.env`，检测旧 `.env` 缺少的配置键并一次性提示开发者当前需填写的全部必填配置（含七牛与超级管理员初始密码等），缺项列表清晰且不打印已填 Secret 值。
- [ ] AC-006（`make up` 集中预检）：`make up` 启动后端前集中校验必要配置（MySQL/Redis 依赖就绪 + 七牛 required 配置 + 结构合法性 + 超级管理员创建条件），一次性报告所有缺失/非法项，以非零退出且不启动后端；全部通过才继续迁移并启动后端。
- [ ] AC-007（错误不泄漏 Secret）：任何 `make up` 预检或启动失败路径，错误信息只指认缺失/非法的键名或依赖名，不打印 `.env` 中的 Secret 值、密码、token、内部路径或堆栈。
- [ ] AC-008（超级管理员首次创建 vs 已存在）：首次创建场景——数据库尚无超级管理员时，`.env` 未提供 `ADMIN_SUPER_PASSWORD` 则启动失败并明确提示需填写；已存在场景——数据库已有超级管理员时，即使未提供 `ADMIN_SUPER_PASSWORD` 也正常启动、不覆盖既有超管密码（幂等）。
- [ ] AC-009（全新环境端到端）：在无 `.env` 的全新环境执行 `make init` → 按提示填写 `.env`（含七牛真实凭据与超管密码）→ `make up`，后端成功启动、健康检查通过、七牛可用性预检通过。
- [ ] AC-010（`make test` 不受环境变量污染）：开发者在 `.env` 已填真实七牛凭据（或其它覆盖配置）的情况下执行 `make test`，`go vet ./...` 与 `go test -p 1 ./...` 仍全部通过，测试结果不受真实环境变量污染（不依赖、也不被 `.env` 中真实凭据影响）。
- [ ] AC-011（保留 `make test-storage` 真实 E2E）：`make test-storage` 仍作为独立真实存储 E2E 入口正常工作（真实七牛凭据签发→直传→校验→删除）；缺真实凭据时非零退出并明确说明，不以退出 0 冒充通过。
- [ ] AC-012（失败可定位）：当配置缺失/非法导致 `make up` 或启动失败时，开发者能凭输出明确识别缺失的具体配置键与期望动作，无需猜测，且输出不含 Secret。

## Relevant Context

已核实事实（2026-10-09，TaskBuilder 调查）：

- 分支与基线：`feat/dev-environment-setup` == `feat/object-storage-upload` == `origin/feat/dev-environment-setup` == commit `d6a6e26`，working tree clean（`git status --short` 为空）。上游 `feat/object-storage-upload` 尚未合并，其已提交内容为本任务基线。
- `Makefile` 目标已存在：`help/bootstrap/init/up/down/restart/status/logs/health/test/test-storage/clean`；`scripts/` 已含 `init.sh`/`up.sh`/`bootstrap.sh`/`test.sh`/`test-storage.sh`/`lib.sh` 等（由 object-storage-upload 任务引入）。
- 配置加载机制：GoFrame v2.10.3，`g.Cfg().GetEffective(ctx, key, def)`，优先级为「命令行 > 环境变量 > `config.yaml` > 默认值」；环境变量命名「配置键 `.`→`_` 并大写」（`database.default.host`→`DATABASE_DEFAULT_HOST`，由 `internal/boot/boot_test.go` 与 config.yaml 注释确认）。
- **空值覆盖隐患（根因已定位）**：`scripts/lib.sh` 的 `_load_env_file` 会导出 `.env` 中的空值（`KEY=`→`export KEY=""`）；而 GoFrame `genv.Get` 用 `os.LookupEnv`，「空但已设置」的环境变量会以空值覆盖 `config.yaml` 非空默认（`GetEffective` 第 2 步命中）。因此若把 `AUTH_JWT_SECRET=`、`DATABASE_DEFAULT_PASS=` 等键改为未注释空值，会覆盖 `config.yaml` 里的开发默认 secret/`root` 密码，导致启动失败。当前靠「这些行仍被注释」回避，AC-002 要求全部未注释后必须同时消除该隐患（AC-003）。
- `config.yaml` 开发默认：`server.address=:8000`、`database.default.*`（含 `pass=root`）、`redis.default.*`、`auth.jwt.secret`（**非空开发默认**）、`auth.session.ttl=3600`、`auth.refresh.ttl=2592000`、`admin.super.username=admin`、`admin.super.password=""`（**刻意无默认**）、`order.*`、`flash_sale.*`、`storage.local.root=./storage`、`qiniu.*`（`access_key/secret_key/bucket/domain` 均空、其余有默认）、`startup.dependency.timeout=30`。
- 超级管理员 seed（`internal/boot/seed.go` `seedSuperAdmin`）已幂等：`is_super=1` 存在则跳过且不覆盖密码；不存在且 `admin.super.password` 为空则 fail-fast（「未配置超级管理员初始密码」）；并发靠 `admins.username` 唯一约束 1062 兜底。
- JWT 密钥（`internal/auth/jwt.go` `Secret`）：要求 ≥32 字节，空/过短启动 fail-fast。
- `.env.example` 现状：非七牛项（服务/MySQL/Redis/JWT/超管/超时）全被 `#` 注释，七牛 4 必填项（`QINIU_ACCESS_KEY`/`QINIU_SECRET_KEY`/`QINIU_BUCKET`/`QINIU_DOMAIN`）为未注释空值；头注释已写明命名规则与优先级。
- 本地已有 `.env`（被 `.gitignore` 忽略），含真实七牛凭据（此处不复述其值），非七牛项仍为注释状态。
- `make test` = `go vet ./...` + `go test -p 1 ./...`；已存在 QINIU_* 环境变量隔离（commit `6e3f1f9`），但「不受真实环境变量污染」需覆盖全部可被 `.env` 覆盖的键（AC-010）。
- `docs/design/storage.md` §3/§10 已记录「Secret 注入模型（项目级统一）」与「两阶段初始化 + `make test-storage`」；`.env.example` 头注释记录加载优先级。

Assumption（合理但未经 Owner 单独确认，交 Coder 按此落实，若与 Owner 意图冲突以 Owner 为准）：

- 「必填配置」清单 = 七牛 4 项 required（AK/SK/Bucket/Domain）+ 超级管理员初始密码（仅「超管不存在时创建」必填）；MySQL/Redis/JWT secret/超管用户名等均有 `config.yaml` 开发默认，开发环境不强制在 `.env` 填写（留空回退默认）。
- 「检测旧 `.env` 缺项」只提示、不自动改写 `.env`（与「绝不覆盖已有 `.env`」一致）。
- 「超级管理员已存在 vs 首次创建」的可观察行为以 AC-008 为准，判定载体（shell 是否查询 DB，还是依赖 `serve` 内 `seedSuperAdmin` 幂等 + 清晰 fail-fast）由 Coder 选择，不在本任务预设。

## Verification

环境：可连接的 MySQL 8.0 与 Redis 7（经 docker-compose）；AC-009/AC-011 需 Owner 准备真实七牛凭据。

- AC-001 → 静态核验：`.env.example` 键集合与 `config.yaml` 中标注「对应环境变量」的键、`boot.go`/`upload.go`/`auth/jwt.go` 的 `GetEffective` 读取点逐一对应；命名符合「`.`→`_` 大写」。
- AC-002 → 静态核验：`.env.example` 所有配置行为未注释 `KEY=VALUE`；`grep -nE '^[A-Z_]+=' .env.example` 与 `config.yaml` 默认值对齐；无真实凭据（`git grep` 无真实 AK/SK）。
- AC-003 → 行为验证：`AUTH_JWT_SECRET=` 空时 `my-shop serve`（或启动）仍读到 config.yaml 开发默认 secret（≥32 字节）而继续；`DATABASE_DEFAULT_PASS=` 空时仍以 `root` 连接成功；非空覆盖仍生效（复用 `boot_test.go` 环境变量覆盖测试思路）。
- AC-004 → 环境验证：无 `.env` 时 `make init` 生成 `.env`；有 `.env` 时内容不变；连续两次 `make init` 幂等。
- AC-005 → 环境验证：构造「缺少若干键」的旧 `.env` 后 `make init`，断言提示列出缺项与全部必填项，且不打印已填 Secret。
- AC-006 → 环境验证：缺七牛配置/缺超管密码（首建）等场景 `make up` 非零退出、不启动后端、一次性报告缺失项。
- AC-007 → 静态 + 行为验证：预检/启动失败输出只含键名/依赖名，不含 Secret 值、密码、token、路径、堆栈。
- AC-008 → 需 MySQL：清空 admins 超管后缺密码启动失败并提示；已存在超管且缺密码时启动成功且密码不变。
- AC-009 → 真实凭据（B 就绪）：全新环境走完 `make init → 填 .env → make up`，健康检查通过、七牛预检通过。
- AC-010 → 命令验证：`.env` 已填真实七牛凭据时 `make test` 仍通过；缺 `.env` 时 `go build ./...`/`go test -p 1 ./...` 仍通过。
- AC-011 → 真实凭据：`make test-storage` 走完整链路并清理测试对象；缺凭据时非零退出并说明。
- AC-012 → 行为验证：任一缺项场景的输出能定位到具体键与动作。
- 通用命令：`gofmt -l`、`go build ./...`、`go vet ./...`、`go test -p 1 ./...`；脚本 `shellcheck`（如可用）或 `bash -n scripts/*.sh`。

## Complexity

NORMAL

原因：纯配置/脚本工程修复，不新增数据模型、状态机、公开协议、错误码域或 migration，不改上传业务逻辑、不改安全边界（JWT 密钥校验与超管 seed 已存在）；「空值覆盖」根因已定位（GoFrame `GetEffective` 空环境变量覆盖 + `lib.sh` 导出空值）；Owner 已给出详细且明确的两阶段初始化、Secret 留空、不覆盖 `.env` 等关键决策。剩余为实现细节（缺项检测、集中预检、错误提示），有清晰可观察行为约束，无需 Analyst 建立 Contract。

## Review Baseline

- Base commit：`d6a6e26e28c9d382fbdef84806fd608a63318d11`（分支 `feat/dev-environment-setup`，起始 working tree clean，local == origin）。
- 任务开始时已有修改：无（`git status --short` 为空；无未暂存/暂存/新增文件）。
- 重叠修改的区分方式：本任务改动面集中在 `Makefile`、`scripts/*.sh`、`.env.example`（可能涉及 `.gitignore` 校验与测试隔离的少量 Go 测试），这些在 `feat/object-storage-upload` 上已有初版实现；Coder 须在现有脚本上增量修改，保留既有 `qiniu check`、两阶段初始化、`test-storage`、上传业务逻辑，不得删除或覆盖。`feat/object-storage-upload` 尚未合并，其提交内容为本任务基线；本任务仅新增 `.agent/tasks/dev-environment-setup/*`（TaskBuilder 所有物）与上述脚本/配置改动，不触碰其它 task 目录。

## Initial Route

交 Coder（NORMAL，`contract.status = NOT_REQUIRED`）
