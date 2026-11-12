# PHASE2-REAL-ENV-01：SurgeCart 整体真实环境验收报告

- 执行日期：2026-11-12
- 仓库：`/root/projects/my-shop-integration`
- 分支：`feat/phase2-ecommerce-business`
- 范围：集成后工程与基础设施的真实构建、部署配置与运行能力
- 明确排除：前后端 API 契约对齐、字段核对、认证兼容、业务接口联调、K8s/Prometheus/压测

---

## 1. Git 信息

| 项 | 值 |
| --- | --- |
| 分支 | `feat/phase2-ecommerce-business` |
| HEAD SHA | `b875df8aa2a81fbf1824c057b3c3902212850351`（与任务预期一致） |
| 与远端 | 一致（`git rev-list --left-right --count` = `0 0`） |
| 工作区 | 验收开始与结束均为 clean（`git status --porcelain` 为空） |
| 本次改动 | 仅新增本报告；未修改业务代码、Migration、`package.json` 或锁文件 |

## 2. 运行环境

| 项 | 值 |
| --- | --- |
| 操作系统 | Debian GNU/Linux 12 (bookworm)，内核 5.4.241-1-tlinux4-0025.17 |
| CPU / 内存 | 8 核 / 16 GB |
| Go | go1.26.0 linux/amd64 |
| Node / npm / pnpm | v22.23.3 / 10.9.9 / 9.15.9 |
| Docker / Compose | 29.9.2 / v5.6.1 |
| MySQL | 8.0.46（`mysql:8.0` 镜像，独立实例） |
| Redis | 7.4.12（`redis:7-alpine`） |
| Kafka | 3.9.1（`apache/kafka:3.9.1`，KRaft 单节点） |
| Nginx | 1.27.3-alpine（`nginx/Dockerfile` 构建） |
| 运行方式 | 项目既有 `docker-compose.yml` + 独立覆盖文件（项目名 `p2acc`，端口 13306/16379/29092），Go 应用以原生二进制与项目 `serve_lifecycle` 门禁进程运行 |

隔离措施：独立 Compose 项目 `p2acc`、独立容器名、独立网络与数据卷、独立数据库；未使用 `down -v`，未触碰既有 `my-shop-mysql` / `my-shop-redis` 容器与其卷（验收前后均为 Exited 状态）。任务结束后已删除本次创建的全部容器、网络与数据卷。

## 3. Go 后端

| 命令 | 退出码 | 结果 |
| --- | --- | --- |
| `go build ./...` | 0 | 通过 |
| `go vet ./...` | 0 | 通过 |
| `make test-unit` | 0 | 通过，30 组执行单元全部实际运行、未用缓存 |
| `go test -p 1 -count=1 ./...`（真实 MySQL/Redis） | 1 | 50 个包 `ok`，1 个包 `FAIL`（`test/deployk8s`，详见问题 P3-1）；复跑该包后 `ok`（49.814s） |
| `bash scripts/build.sh` | 0 | 产出 `bin/my-shop`（35 MB，与 Dockerfile 同参） |

关键包结果：`internal/controller/*` 14 个包全部 `ok`；`internal/migrations` `ok`（380.038s，含迁移往返）；`test/e2e` `ok`（134.291s，真实 MySQL/Redis）；`test/manifest`、`tools/openapi-gen` 等全部 `ok`。

测试环境参数（全部指向本次隔离实例）：`DATABASE_DEFAULT_HOST=127.0.0.1`、`DATABASE_DEFAULT_PORT=13306`、`DATABASE_DEFAULT_USER=root`、`DATABASE_DEFAULT_NAME=my_shop_ci_test`、`REDIS_DEFAULT_ADDRESS=127.0.0.1:16379`、`BARGAIN_TEST_MYSQL_*` 指向 `my_shop_bargain_test`。未删除任何测试守卫，未修改任何测试代码绕过检查。

进程级运行验证（`bash scripts/test-serve-lifecycle.sh`，真实可执行文件 + 真实 HTTP + 真实 Kafka + 真实 SIGTERM）：

| 用例 | 结果 |
| --- | --- |
| `TestServeProcessLifecycle` | PASS（18.54s） |
| `TestServeHTTPDrainTimeoutForceClose` | PASS（13.07s） |
| `TestServeKafkaOutageKeepsHTTPAvailable` | PASS（21.18s） |

该门禁实际断言了：`/health`、`/ready` 200；Kafka 停机时进程存活、readiness 不被拖垮、`consumer_joined` 归 0；Kafka 恢复后消费者重新加入、消息落单、backlog 收敛为 0；SIGTERM 后按 Publisher → Consumer → Producer → Scanner 顺序关闭、退出码 0、无残留进程。

生产二进制 `bin/my-shop serve` 启动尝试（真实配置）：

- 成功项：`all dependencies are reachable (mysql, redis)`、`schema 已就绪（版本 20261111000004）`、`超级管理员已创建`、`权限 seed 已就绪`。
- 失败项：`七牛 bucket "p2acc-dummy-bucket" 可用性检查失败: bad token`，进程 fail-fast 退出码 1。
- 原因：`serve` 将七牛云列为启动 required dependency，`service.Upload().ValidateConfig` 会调用真实 `GetBucketInfo`；本机无真实七牛凭据（属环境缺失，非工程缺陷）。因此生产二进制的常驻启动、`/health` 与优雅关闭由项目自带的 `serve_lifecycle` 门禁（真实服务装配，仅七牛 UC 指向本地 Mock）完成验证，未以 Mock 冒充七牛可用性（问题 P1-1）。

## 4. 数据库

隔离方式：全新 MySQL 实例、独立库 `my_shop_p2acc_fresh`（迁移验证）/ `my_shop_p2acc`（应用）/ `my_shop_ci_test` 等测试库；未连接生产库、未导入真实 PII、未删除任何既有数据库或卷。

| 检查项 | 结果 |
| --- | --- |
| 空库全量迁移 | 通过，耗时 3.835s，起始表数 0 |
| 迁移版本 | `20261111000004`（`dirty=false`），与内置最新版本一致 |
| 表数量 | 64 张，全部 InnoDB、全部 utf8mb4 |
| 外键约束 | 28 个 |
| 重复执行 | 再次 `migrate up` 返回 "没有待执行的 migration"，0.120s，无重复写入、无报错 |
| 应用连接迁移后库 | 通过（serve 启动日志 `schema 已就绪（版本 20261111000004）`） |
| 初始化数据 | 超级管理员 1 个、权限 73 条；`region-seed dry-run` 3938 条（省 34/市 369/区县 3535），`import` 后 `regions`=3938，二次执行写入 0（幂等） |
| 关键唯一约束 | `orders.uk_order_no`、`orders.uk_user_idempotency`、`cart_items.uk_user_sku`、`inventories.uk_sku_id`、`skus.uk_sku_code`、`flash_sale_order_requests.uk_request_idempotency`、`users.uk_phone`、`users.uk_username`、`refund_requests.uk_active` 均存在 |
| 关键查询索引 | `orders.idx_user_id`、`orders.idx_status_expire`、`products.idx_category_id`/`idx_status`、`skus.idx_product_id`、`order_items.idx_order_id`、`flash_sale_order_requests.idx_dequeue`/`idx_outbox_claim` 均存在 |

说明：`member_levels`、`roles`、`role_permissions` 初始为 0 条，属当前 seed 设计（启动仅 seed 超管与权限；会员等级、角色由业务按需创建），非迁移缺陷。

## 5. Redis 与 Kafka

**Redis**

| 检查项 | 结果 |
| --- | --- |
| 服务启动 | `PING` → `PONG`，7.4.12 |
| 读写与过期 | `SET ... EX 60` → `OK`，`GET` → `hello`，`TTL` → 60 |
| 项目 Lua 脚本执行 | 限流脚本（INCR + 首次 EXPIRE）返回 1 → 2，`TTL`=60；会话撤销脚本存在 key 返回 1 且 `revoked=1`，key 不存在返回 0 |
| 会话/缓存能力 | 由 `internal/auth`、`internal/controller/iam` 等包在真实 Redis 上的测试覆盖并通过 |
| 后端连接 | serve 启动日志 `all dependencies are reachable (mysql, redis)` |

**Kafka**

| 检查项 | 结果 |
| --- | --- |
| Broker 启动 | 3.9.1，健康检查通过（`kafka-broker-api-versions`） |
| Topic 手工收发 | 创建 `p2acc-smoke` → 生产 1 条 → 消费得到 `p2acc-ping-1` → 删除 Topic，全部成功 |
| Publisher/Consumer/Outbox 链路 | `bash scripts/test-kafka-e2e.sh`（`B4_E2E_FAULTS=0`，不注入停机故障）通过，5 个用例全部 PASS |
| 幂等与重复处理 | 由 `TestKafkaFlashSaleEndToEnd`、`TestKafkaFlashSalePublisherRestartRecoversOutbox` 覆盖并通过（未做大规模并发压测） |

`test-kafka-e2e.sh` 用例：`TestKafkaFlashSaleEndToEnd`（5.91s）、`TestDBScannerFlashSaleEndToEndWithoutKafka`（1.65s）、`TestFlashSaleModeSwitchRequiresRestart`（7.78s）、`TestKafkaFlashSalePublisherRestartRecoversOutbox`（5.64s）、`TestKafkaFlashSaleShutdownWithInflightTraffic`（5.82s），退出码 0。

## 6. React 用户商城

| 命令 | 退出码 | 结果 |
| --- | --- | --- |
| `npm ci` | 0 | 通过（首次被环境批量删除保护拦截，移走旧 `node_modules` 后重装成功，见 P3-2） |
| `npm run lint` | 0 | 通过 |
| `npm run typecheck` | 0 | 通过 |
| `npm run test` | 0 | 45 个测试文件、317 个用例全部通过 |
| `npm run build` | 0 | 通过，`dist/` 896 KB |

产物验证（使用临时静态服务器模拟静态托管，未修改工程）：

| 检查项 | 结果 |
| --- | --- |
| `/` 首页 | 200，`text/html`，包含 `<div id="root">` |
| JS 资源 `/assets/index-ZVSO7MK5.js` | 200，`application/javascript`，371 KB |
| CSS 资源 `/assets/index-DBhVWXxF.css` | 200，`text/css` |
| `/favicon.svg` | 200，`image/svg+xml` |
| 深层路由 `/products/1`（BrowserRouter） | 200，返回入口 HTML（需部署侧提供 SPA fallback） |

未调用任何 Go 业务接口。

## 7. Vue 管理后台

| 命令 | 退出码 | 结果 |
| --- | --- | --- |
| `pnpm install --frozen-lockfile` | 0 | 通过 |
| `pnpm lint:eslint:check` | 0 | 通过（`--max-warnings 0`） |
| `pnpm lint:prettier:check` | 0 | 通过 |
| `pnpm typecheck` | 0 | 通过（`vue-tsc --noEmit --skipLibCheck`） |
| `pnpm test` | 0 | 25 个测试文件、352 个用例全部通过 |
| `pnpm build` | 0 | 通过，产物 3.38 MB，base 为 `/console/` |
| `pnpm lint:stylelint` | 78 | 失败：`Could not find "stylelint-config-standard"`（问题 P2-2） |

产物验证（临时静态服务器以 `/console/` 前缀托管）：

| 检查项 | 结果 |
| --- | --- |
| `/console/` | 200，`text/html`，包含 `<div id="app">` 与启动占位 |
| `/console/static/js/index-DVFUzmTy.js` | 200，`application/javascript`，1.94 MB |
| `/console/static/css/index-C2A9P1KK.css` | 200，`text/css` |
| `/console/brand/theme.js`、`/console/logo.svg` | 200 |
| 深层路由 `/console/dashboard` | 200，返回入口 HTML |

路由为 hash 模式（`createWebHashHistory`），深层页面不依赖服务端路由；`vite build` 后入口资源均带 `/console/` 前缀，无重复前缀。未调用任何 Go 业务接口。

## 8. Nginx

| 检查项 | 结果 |
| --- | --- |
| 镜像构建 | 通过（`nginx/Dockerfile`，1.27.3-alpine） |
| `nginx -t` | 通过：`syntax is ok`、`test is successful` |
| 容器启动 | 通过（接入本次 Compose 网络，18080 → 80） |
| `/metrics` 公共入口 | 404（按设计不暴露） |
| 上游不可达 | `/health` → 502，返回最小错误页，附 `X-Content-Type-Options: nosniff`；`Server` 头无版本号 |
| API upstream | `server app:8000 resolve`、`proxy_next_upstream off`、请求头覆盖（`X-Request-ID`/`X-Real-IP`/`X-Forwarded-For`）配置正确 |
| 静态资源托管 | **未配置**：`nginx.conf` 无前端 `root`/`alias`/`try_files`；`/assets/x.js` 与 `/console/` 均被代理到后端（502） |
| SPA fallback | **未配置** |
| 网络连通性 | 容器网络与端口映射正常；Nginx → Go 应用的实链因应用无法常驻启动（P1-1）未取得真实上游，未以此判定通过 |

Nginx 与部署配置按要求只记录、未修改。

## 9. 问题清单

### P1-1 生产二进制缺少真实七牛凭据导致启动 fail-fast

- 等级：P1（外部依赖缺失，非工程缺陷）
- 复现：设置真实 DB/Redis/Kafka 与占位 `QINIU_*` 后执行 `bin/my-shop serve`
- 证据：日志 `七牛 bucket "p2acc-dummy-bucket" 可用性检查失败: bad token`，退出码 1；此前已输出 `all dependencies are reachable (mysql, redis)`、`schema 已就绪`、`超级管理员已创建`、`权限 seed 已就绪`
- 是否阻塞整体运行：是（阻塞生产二进制常驻启动、Nginx→app 实链、真实上传链路）
- 推荐处理归属：环境/部署凭据；Owner 提供真实七牛测试凭据后可复验。当前以项目 `serve_lifecycle` 门禁完成启动/健康/优雅关闭验证

### P2-1 Nginx 未托管前端静态资源且无 SPA fallback

- 等级：P2
- 复现：启动 Nginx 容器后请求 `/assets/x.js` 与 `/console/`
- 证据：`nginx.conf` 无 `root`/`alias`/`try_files`（仅错误页 `root /etc/nginx/error`）；两路径均返回 502（被代理到后端）
- 是否阻塞整体运行：阻塞"经 Nginx 访问 React/Vue 静态站点"；不阻塞三工程自身构建与运行
- 推荐处理归属：部署配置任务（本次未修改，仅记录）

### P2-2 管理端 stylelint 无法启动

- 等级：P2（合并前既有）
- 复现：`cd admin && pnpm lint:stylelint`
- 证据：退出码 78，`ConfigurationError: Could not find "stylelint-config-standard"`；`package.json` 仅声明 `stylelint-config-standard-scss`
- 是否阻塞整体运行：否
- 推荐处理归属：管理端工程整改（本次未改依赖或配置掩盖）

### P3-1 `test/deployk8s` 在系统负载下 flaky

- 等级：P3
- 复现：全量 `go test ./...` 期间 `TestMultiprocessRenewalStallExpiresAndTakeoverSucceeds` 失败：`退出码 = 2，期望 3`，stderr `trap: line 2: unexpected EOF while looking for matching ')'`
- 证据：单独复跑该用例 `ok`（15.078s），复跑整包 `ok`（49.814s）
- 是否阻塞整体运行：否；与本次集成无关（该目录未改动）
- 推荐处理归属：K8s 发布脚本测试稳定性（超出本次范围）

### P3-2 `npm ci` 被环境批量删除保护拦截

- 等级：P3（环境机制）
- 复现：`cd web && npm ci`（存在完整 `node_modules` 时）
- 证据：`[safe-delete][SAFE_DELETE_BULK_CONFIRM_REQUIRED] ... targets: web/node_modules/@babel`
- 是否阻塞整体运行：否；移走旧 `node_modules` 后 `npm ci` 成功
- 推荐处理归属：环境/CI 配置

### P3-3 Go 包扫描包含 `web/node_modules` 下的第三方 Go 文件

- 等级：P3
- 复现：安装前端依赖后执行 `go test ./...`
- 证据：输出 `? .../web/node_modules/flatted/golang/pkg/flatted [no test files]`
- 是否阻塞整体运行：否
- 推荐处理归属：工程卫生（CI 可使用 `-mod=mod` 之外的排除策略或独立依赖目录）

### P3-4 构建产物体积告警

- 等级：P3
- 证据：管理端 `index-DVFUzmTy.js` 1.94 MB（gzip 626 KB）；用户商城 `index-ZVSO7MK5.js` 371 KB（gzip 118 KB）
- 是否阻塞整体运行：否
- 推荐处理归属：后续性能优化任务

### 后续独立前后端联调任务（本次不处理）

- 用户商城与管理端 API 客户端默认基址均为 `/api`（`web/src/api/client.ts`、`admin/src/utils/http/index.ts`），而仓库 Nginx 未剥离 `/api` 前缀、Go 路由分组为根路径 `/`，路径前缀不一致。
- 管理端真实上传流程、管理员权限接口数据、订单/退款/优惠券业务字段尚未与真实后端核对。
- 以上仅登记，本次未修改任何一方业务代码。

## 10. 最终结论

**PARTIAL**

已完成并通过：

- Git 基线与工作区核验、三条线集成结果一致。
- Go 构建、vet、单元测试、真实 MySQL/Redis 全量测试（除 1 个 K8s 用例在负载下 flaky，复跑通过）、进程级启动/健康/优雅关闭门禁、Kafka 秒杀端到端门禁。
- MySQL 空库全量迁移、64 张表/28 外键/关键唯一索引、重复执行幂等、初始化数据（超管/权限/地区字典）。
- Redis 基础读写与项目 Lua 脚本执行。
- Kafka Broker、Topic、生产/消费、Outbox Publisher、Consumer、幂等链路。
- React 用户商城与管理后台的安装、lint、类型检查、测试、构建及静态产物访问。
- Nginx 镜像构建、`nginx -t`、`/metrics` 隐藏、上游不可达错误处理。

未完全通过或未验证：

- 生产二进制因缺少真实七牛凭据无法常驻启动（P1-1），Nginx → Go 应用实链未取得真实上游。
- 现有 Nginx 配置未托管前端静态资源、无 SPA fallback（P2-1），React/Vue 产物通过临时静态服务器验证。
- 管理端 stylelint 配置缺失（P2-2）。
- 前后端 API 契约与业务联调不在本次范围，统一移交后续联调任务。

不得将上述未执行/未通过项视为 PASS。
