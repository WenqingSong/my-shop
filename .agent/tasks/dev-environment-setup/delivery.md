# Delivery Verification

## Milestone and Target
- Milestone：dev-environment-setup 开发环境配置与初始化流程修复交付验收（`make init` → 填 `.env` → `make up` 全链路启动 + `make test` 环境变量隔离 + `make test-storage` 真实七牛 E2E）
- Delivery Target（feature_head）：`152aaaf6ff56f51adb16efb158ae622ed4e12860`
- Cleaner Review Target（review.target / C1）：`fb5bbee68ec31893aa7ddcd0e8bb7400c6a2b222`（短 `fb5bbee`）
- develop_base：`99ace202a90de7699ab7ea3cd24dd7fa9742ef8c`（`origin/develop`）
- Target Match：YES（`feature_head` 与 `review.target` 之间仅含 review-neutral tail：`state.yaml` / `findings.md` / `core-logic.md` / `owner-decision.md`，生产代码与 `fb5bbee` 完全一致）

## Environment
- OS：Debian GNU/Linux 12 (bookworm)，x86_64，kernel 5.4.241
- Go：go1.24.1 linux/amd64
- Docker：29.6.2；Compose v5.3.1
- MySQL：8.0.46（镜像 `mysql:8.0`，容器 `my-shop-mysql`，root/root/my_shop 开发默认，数据卷 `mysql_data`）
- Redis：7.4.11（镜像 `redis:7-alpine`，容器 `my-shop-redis`，数据卷 `redis_data`）
- 无 Kafka/MQ（本任务不涉及）
- 七牛：本地 `.env` 中的真实凭据（AK/SK/Bucket/Domain，值不在此记录），bucket 可用性经 `qiniu check` 真实 `GetBucketInfo` 验证通过
- 配置来源：本地 `.env`（被 `.gitignore` 忽略）+ `manifest/config/config.yaml` 开发默认值；Secret 值一律不记录
- 验证方式：直接在 `feat/dev-environment-setup` 当前工作区（HEAD `152aaaf`）执行，生产代码与 `fb5bbee` 一致，无未提交文件、无本机绝对路径依赖

## Verification

| Check | Result | Evidence |
|---|---|---|
| Build | PASS | `go build ./...` exit 0；`gofmt -l .` 无输出 |
| make test-lib（AC-003 回归） | PASS | `make test-lib` → `test-lib.sh 全部通过`，exit 0 |
| make test（AC-010 隔离） | PASS | `.env` 含真实七牛凭据时 `make test`（go vet + go test -p 1 ./...）全部包 `ok`，exit 0；`test.sh` 不 source `lib.sh`，不加载 `.env` |
| make init 保留 .env（AC-004） | PASS | `.env` 已存在时 `make init` 保留不覆盖（前后 md5 均为 `df23f00a…`） |
| make init 生成 .env（AC-004） | PASS | 临时移除 `.env` 后 `make init` 从 `.env.example` 生成，`diff .env .env.example` 完全一致；随后恢复真实 `.env` |
| make init 缺项检测（AC-005） | PASS | 旧 `.env`（仅 5 个七牛键）被一次性列出 26 个缺键 + 必填 `ADMIN_SUPER_PASSWORD`，只打印键名、不打印已填 Secret |
| make up 首次创建缺密码（AC-006/007/008） | PASS | 无超管 + 无 `ADMIN_SUPER_PASSWORD`：`qiniu check` 通过 → `admin check` 失败并指认 `ADMIN_SUPER_PASSWORD` → 非零退出、未启动后端；输出无 Secret |
| make up 注入密码全链路（AC-009） | PASS | `ADMIN_SUPER_PASSWORD` 注入后全链路 deps→build→qiniu check→admin check→migrate→start→`/health` 返回 `{"code":0}`，exit 0 |
| make up 已存在超管幂等（AC-008） | PASS | 超管已存在 + 无密码时 `make up` 通过（exit 0），`admin check` 输出「超级管理员已存在，跳过创建条件校验」，密码哈希前后一致（未覆盖） |
| AC-003 空值不覆盖默认（INV-001） | PASS | `.env` 追加 `AUTH_JWT_SECRET=`、`DATABASE_DEFAULT_PASS=`、`DATABASE_DEFAULT_USER=`（空值）后 `make up` 仍用 config.yaml 默认 secret/root 正常启动，`/health` code:0 |
| make test-storage 正向（AC-011） | PASS | 真实七牛 E2E：`TestStorageE2E` 注册→登录→签发→直传真实 1×1 PNG→校验 final_url HTTP 200→删除测试对象，`PASS`，exit 0 |
| make test-storage 负向（AC-011） | PASS | 临时移除 `.env` 后 `make test-storage` 非零退出（exit 2），逐项指认 `QINIU_ACCESS_KEY/SECRET_KEY/BUCKET/DOMAIN`，不以 0 冒充通过 |

## Acceptance Evidence

- AC-001（配置清单一致）：`.env.example` 31 键未注释 `KEY=VALUE`，与 `config.yaml`/读取点一致（Cleaner 静态核验 + 本机 `make init` 缺项检测间接佐证 26 键模板存在）。
- AC-002（格式与默认值）：`.env.example` 全未注释；`AUTH_JWT_SECRET`/`ADMIN_SUPER_PASSWORD`/`QINIU_SECRET_KEY` 及七牛 4 必填项留空；本机 `.env` 无真实凭据泄漏（所有输出仅含键名）。
- AC-003 / INV-001：空值不覆盖默认（上表 AC-003 行，端到端实测）。
- AC-004：`.env` 保留 + 缺失时生成，均实测通过。
- AC-005：缺项 + 必填一次性提示，无 Secret，实测通过。
- AC-006：`make up` 集中预检、非零退出、不启动后端，实测通过（缺密码场景）。
- AC-007：所有失败路径输出只含键名/依赖名，无 Secret/密码/token/路径/堆栈，实测通过。
- AC-008 / INV-002：首次创建失败提示 + 已存在幂等不覆盖密码，两场景均实测通过（密码哈希 `$2a$10$…` 60 位未变）。
- AC-009：全新环境全链路启动、健康检查 code:0、七牛可用性预检通过，实测通过（超管密码以环境变量注入，与「填 .env」走同一 `GetEffective` 环境变量覆盖路径）。
- AC-010：`make test` 不受真实 `.env` 凭据污染，实测通过。
- AC-011：`make test-storage` 真实七牛 E2E 正负向均实测通过。
- AC-012：各缺项场景输出可定位具体键名 + 期望动作，实测通过。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| `make test-storage` 以「编辑 .env 填密码」的字面路径运行（而非环境变量注入） | 超管密码经环境变量 `ADMIN_SUPER_PASSWORD` 注入，与 `.env` 非空值经 `lib.sh` 导出到同一环境变量、走同一 `GetEffective` 覆盖路径，行为等价 | 低；若担心 `.env` 解析差异，已有 `make test-lib` + `make init` 缺项检测覆盖 `.env` 解析 |
| Race Test / 并发 | 本任务为 shell/config 工程，无并发业务逻辑 | 无 |
| 回滚 / 恢复演练 | 不属本里程碑（dev-environment-setup）要求 | 无 |
| 性能测试 | 不属本里程碑要求 | 无 |

## Remaining Risks

- INV-003（错误不泄漏 Secret）目前靠实测观察验证，未沉淀为持久化回归测试（Owner 已接受，非阻塞）。
- shell 直接 `export AUTH_JWT_SECRET=` 仍会覆盖默认（超出 AC-003 范围，Owner 已接受）。
- 超管预检查询依赖 `DATABASE_DEFAULT_*` 与 docker-compose 根凭据一致（默认 `root/root/my_shop`），不一致时 fail-closed；本机以默认值实测一致。
- `make up` 启动会在 `./storage/` 生成 banner 占位图，而 `.gitignore` 未忽略 `storage/`（object-storage-upload 既有行为，本任务 Out of Scope）；本次验证产生的 `storage/` 已清理。
- 本任务集成目标为 `feat/object-storage-upload`（非 develop 直接集成），由 Owner 决定合并；`develop_base` 记录为验证时 `origin/develop` = `99ace20`。

## 数据隔离与清理

- 测试数据：`make test` 与 E2E 使用开发共享 MySQL/Redis（docker-compose 数据卷保留）。
- 验证后清理：删除本次生成的测试超管（`admins`）、E2E 一次性用户（`users`/`refresh_tokens`）、`storage/banners/*`、`bin/`、`tmp/`；`.env` 恢复原 md5 `df23f00a…`；MySQL/Redis 数据卷未删除。

## Result

PASS
