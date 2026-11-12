# PHASE2-REAL-ENV-REMEDIATION-01：真实环境验收问题修复与复验报告

- 执行日期：2026-11-12
- 仓库：`/root/projects/my-shop-fix`（修复分支独立 worktree）
- 分支：`fix/phase2-real-env-acceptance`
- 范围：修复上一轮 P1/P2 问题并重新执行整体真实环境验收
- 明确排除：前后端 API 契约对齐、字段核对、认证兼容、业务接口联调、K8s/Prometheus/压测

---

## 1. 原始 PARTIAL 结果

上一轮报告 `docs/reports/PHASE2_REAL_ENVIRONMENT_ACCEPTANCE.md` 的结论为 **PARTIAL**，遗留三项：

| 编号 | 问题 | 等级 |
| --- | --- | --- |
| P1-1 | Go 生产二进制因缺少可用七牛云配置无法常驻启动 | P1 |
| P2-1 | Nginx 未托管 React/Vue 静态资源，缺少 SPA fallback | P2 |
| P2-2 | 管理端 Stylelint 配置缺失（`stylelint-config-standard` 未声明，退出码 78） | P2 |

本轮逐项处理，并按任务要求重新执行完整验收。

## 2. 当前 Git 基线

| 项 | 值 |
| --- | --- |
| 修复起点（集成分支 HEAD） | `4ac12966641af015ee8d08f22289589b104b0776` |
| 起点与远端同步 | 是（`0 0`） |
| 起点工作区 | clean |
| 修复分支 | `fix/phase2-real-env-acceptance`（独立 worktree，基于起点 SHA） |
| 修复分支提交 | 见 `git log fix/phase2-real-env-acceptance` |
| 集成分支合并提交 | 见 `git log feat/phase2-ecommerce-business`（普通 `--no-ff` 合并） |
| main | 未修改 |

未重置到旧 SHA，未改写历史，未使用 force push，未删除任何原始开发分支。

## 3. 七牛云配置加载与真实连接

### 3.1 凭据来源与读取

- 凭据位于 `/root/projects/my-shop/.env`（Git 忽略文件），键项齐全且非空：`QINIU_ACCESS_KEY`、`QINIU_SECRET_KEY`、`QINIU_BUCKET`、`QINIU_DOMAIN`、`QINIU_REGION`。
- 应用通过 `internal/config` 契约表读取环境变量，`internal/logic/upload` 在 `serve` 启动时执行结构、存在性与真实 `GetBucketInfo` 校验。
- 进程环境继承正常：修正下述配置值后，`serve` 与 `qiniu check` 均通过，说明不是"凭据未读取"问题。
- 全文未输出、未记录任何 AK/SK/Token；报告与日志无密钥。

### 3.2 发现的配置值错误（已修正）

凭据本身有效，但两项配置值与实际 bucket 不符，导致首轮上传被七牛拒绝：

| 键 | 修正前 | 修正后 | 依据 |
| --- | --- | --- | --- |
| `QINIU_REGION` | `z2` | `z1` | 七牛 SDK `GetZone` / `GetBucketInfo`：bucket 区域为 `z1`，上传入口 `up-z1.qiniup.com` |
| `QINIU_DOMAIN` | 七牛控制台页面地址 | `https://img.weniqngsongceshi.online` | `ListBucketDomains` 返回的唯一绑定域名 |

错误证据（修正前，真实上传返回）：`{"error":"incorrect region, please use up-z1.qiniup.com, bucket is: ..."}`。

另发现 `.env` 的 `AUTH_JWT_SECRET` 长度小于契约要求（≥32 字节），导致容器内 `serve` 直接退出；已替换为 48 字节随机值（未记录值）。该修正会使既有开发 token 失效，仅影响本地环境。

### 3.3 真实连接验证

| 命令 | 退出码 | 结果 |
| --- | --- | --- |
| `bin/my-shop qiniu check` | 0 | `七牛云配置与 bucket 可用性校验通过` |
| `serve` 启动七牛校验 | 0 | 进程越过 `ValidateConfig` 正常监听（未关闭、未绕过校验） |

bucket 实际信息（通过临时探针查询，运行后已删除）：区域 `z1`、`private=0`、绑定域名 `img.weniqngsongceshi.online`、源站上传入口 `up-z1.qiniup.com`。

## 4. Go 生产二进制启动证据

真实 MySQL 8.0.46、Redis 7.4.12、Kafka 3.9.1、真实七牛配置下执行 `./bin/my-shop serve`：

```
all dependencies are reachable (mysql, redis)
schema 已就绪（版本 20261111000004）
超级管理员已创建
权限 seed 已就绪
http_server_started address=127.0.0.1:28000
flashsale_consumer_joined partitions=3 topic=surgecart.flashsale.order.requested.v1
```

| 检查项 | 结果 |
| --- | --- |
| `GET /health` | 200 `{"code":0,"data":{"status":"ok"}}` |
| `GET /ready` | 200 `{"code":0,"data":{"status":"ready"}}` |
| 持续运行 | 是（多轮请求与上传期间未退出） |
| SIGTERM | `kill -TERM` 后退出码 0；日志 `http_server_stopped(signal=terminated)` → `flashsale_runtime_shutdown_complete(within_budget=true)` → `all servers shutdown` |
| 残留进程 | 无（`ps` 校验通过，端口释放） |

同一镜像以 Compose 方式启动（`p2rem-app`）同样达到 `healthy`，`/health` 经 Nginx 返回 200。

## 5. 真实文件上传与读取证据

流程：管理员登录 → `GET /admin/qiniu/upload/token`（项目 Storage 实现签发）→ 使用返回 token 向 `upload_url` 直传 → 读取对象。

| 检查项 | 结果 |
| --- | --- |
| 测试文件 | 70 字节 PNG，本地 SHA-256 `c414cd0e…7ce77` |
| 凭证签发 | 成功（token 长度 322，key `upload/20261112/<随机>.png`，含过期时间） |
| 上传主机 | `up-z1.qiniup.com`（与 bucket 区域一致） |
| 上传响应 | `{"hash":"FrK752PH4YKMdQ1T94VQcJpv6hm-","key":"..."}` |
| 对象元数据（七牛 API） | `size=70`、`hash=FrK752PH4YKMdQ1T94VQcJpv6hm-`、`mime=image/png`，与本地文件一致 |
| 访问路径 | `https://img.weniqngsongceshi.online/<key>`，符合 `domain/key` 设计 |
| 公网 HTTP 读取 | **失败**：`Could not resolve host: img.weniqngsongceshi.online` |
| 测试对象清理 | 已通过七牛 API 删除本次创建的两个对象，未触碰其他文件 |

公网读取失败的根因是域名没有 DNS 记录，非项目缺陷：对该域名向 `223.5.5.5`、`119.29.29.29`、`1.1.1.1` 查询均为 NXDOMAIN；apex 域名可解析（指向另一台服务器），说明仅在 DNS 服务商处缺少 `img` 子域记录（通常为指向七牛控制台所示 CNAME 目标的记录）。此项无法在仓库内修复，见第 14 节。

## 6. Nginx 配置修改

路径契约（按要求固定）：

| 路径 | 目标 |
| --- | --- |
| `/` | React 用户商城（`web/dist`），SPA fallback 至 `/index.html` |
| `/console/` | Vue 管理后台（`admin/dist`），SPA fallback 至 `/console/index.html` |
| `/api/` | Go 后端反向代理，**原样透传前缀，不做重写** |

修改文件与要点：

- `nginx/nginx.conf`
  - 新增静态站点根 `/usr/share/nginx/html/web` 与 `location /` → `try_files $uri $uri/ /index.html`。
  - 新增 `location /console/` → `alias /usr/share/nginx/html/console/` + `try_files $uri $uri/ /console/index.html`；`/console` 301 到 `/console/`。
  - 新增 `/console/static/`、`/assets/` 长缓存（文件名含内容哈希）。
  - `/api/` 保留原有代理语义：`proxy_pass`（不重写）、`X-Request-ID` 覆盖、`X-Real-IP`/`X-Forwarded-For` 覆盖式重写、`proxy_next_upstream off`、连接复用。
  - 保留 `/health` 透传（容器健康检查依赖）、`/metrics` 返回 404、502/504/413 错误页、`server_tokens off`、安全响应头。
  - 新增 `absolute_redirect off`，避免端口映射下生成错误绝对跳转地址。
- `nginx/Dockerfile`
  - 改为多阶段构建：`node:22-alpine` 内 `npm ci && npm run build`（web）、`corepack pnpm@9.15.9` + `pnpm install --frozen-lockfile && pnpm build`（admin），运行阶段 COPY 两个 `dist` 到 `/usr/share/nginx/html/{web,console}`。
  - 产物在镜像内生成，干净检出即可 `docker compose build nginx`，不依赖宿主机预构建。
- `docker-compose.yml`
  - nginx `build.context` 改为仓库根，`dockerfile: nginx/Dockerfile`；端口、依赖、healthcheck 语义不变。
- `.dockerignore`
  - 排除 `**/node_modules/`、`web/dist/`、`admin/dist/`；允许 `nginx/` 进入上下文。

未修改 `/api` 代理路径语义，未修改 Go 路由，未处理前后端前缀不一致。

## 7. React 静态资源验收

通过完整编排（app + nginx）访问 `http://127.0.0.1:28080`：

| 请求 | 结果 |
| --- | --- |
| `GET /` | 200 `text/html`，`<title>SurgeCart 商城</title>`，含 `<div id="root">` |
| `GET /assets/index-ZVSO7MK5.js` | 200 `application/javascript`，371 KB |
| `GET /assets/index-DBhVWxXF.css` | 200 `text/css` |
| `GET /favicon.svg` | 200 `image/svg+xml` |
| 资源 404 | 无 |

## 8. Vue 静态资源验收

| 请求 | 结果 |
| --- | --- |
| `GET /console/` | 200 `text/html`，`<title>SurgeCart 管理后台</title>`，含启动占位 |
| `GET /console/static/js/index-gr5PorP3.js` | 200 `application/javascript`，1.94 MB |
| `GET /console/static/css/index-H2LN3JQ3.css` | 200 `text/css` |
| `GET /console/brand/theme.js`、`/console/logo.svg` | 200 |
| `GET /console` | 301 → `/console/`（Location 含正确对外端口） |
| 资源路径重复前缀 | 无（均为单次 `/console/`） |

## 9. SPA 深层路由验收

| 场景 | 结果 |
| --- | --- |
| React 深层路由 `GET /products/1`（BrowserRouter，刷新场景） | 200，返回 React 入口 HTML |
| Vue 深层路由 `GET /console/order/list` | 200，返回 `/console/index.html` 内容 |

`nginx -t` 在镜像内执行通过：`syntax is ok` / `test is successful`。

## 10. Stylelint 修复与验证

| 项 | 值 |
| --- | --- |
| Stylelint | 17.6.0（未升级） |
| 新增 devDependency | `stylelint-config-standard@^40.0.0`（peer `stylelint ^17`）、`stylelint-config-html@^1.1.0`（peer `postcss-html ^1`）、`stylelint-config-recommended@^18.0.0`（peer `stylelint ^17`）、`stylelint-scss@^7.4.0`、`stylelint-order@^8.1.3` |
| 理由 | 配置文件中 extends/plugins 实际引用这些包，但此前未声明；均为使既有配置可运行的最小补齐，未升级无关依赖 |
| 锁文件 | `admin/pnpm-lock.yaml` 已同步更新 |
| frozen-lockfile 安装 | `pnpm install --frozen-lockfile` 退出码 0 |
| 样式修复 | 自动修复属性顺序、空行、关键字大小写等；另手工将 3 处已弃用的 `word-break: break-word` 改为 `overflow-wrap: break-word`（等价语义） |
| `pnpm lint:stylelint` | 退出码 0 |
| `pnpm exec stylelint "**/*.{html,vue,css,scss}"`（无 --fix） | 退出码 0，无隐藏配置错误 |

未删除脚本、未禁用规则、未使用忽略文件掩盖问题。

## 11. 全量测试结果

### Go

| 命令 | 退出码 | 结果 |
| --- | --- | --- |
| `go build ./...` | 0 | 通过 |
| `go vet ./...` | 0 | 通过 |
| `make test-unit` | 0 | 通过，30 组执行单元 |
| `go test -p 1 -count=1 ./...` | 0 | **51 包 ok，0 FAIL**；`internal/migrations` 369.729s、`test/e2e` 132.492s、`test/deployk8s` 39.968s |

`test/deployk8s` 稳定性复核（上一轮曾出现负载下 flaky）：本轮全量运行通过；单独连续运行 2 次均通过（43.324s / 44.938s）。未修改测试代码。

### Kafka 门禁（`scripts/test-kafka-e2e.sh`，`B4_E2E_FAULTS=0`）

5/5 通过：`TestKafkaFlashSaleEndToEnd`、`TestDBScannerFlashSaleEndToEndWithoutKafka`、`TestFlashSaleModeSwitchRequiresRestart`、`TestKafkaFlashSalePublisherRestartRecoversOutbox`、`TestKafkaFlashSaleShutdownWithInflightTraffic`。

### React 用户商城

`npm ci` 退出码 0；lint 0；typecheck 0；test 45 文件 / **317 用例通过**；build 0。

### Vue 管理后台

`pnpm install --frozen-lockfile` 0；ESLint 0；Prettier 0；typecheck 0；test 25 文件 / **352 用例通过**；build 0；Stylelint 0。

### 部署静态门禁

`scripts/test-nginx.sh`、`scripts/test-container.sh`、`scripts/check-registry.sh`、`scripts/test-prometheus.sh` 均退出码 0。

## 12. 数据库 / Redis / Kafka

| 项 | 结果 |
| --- | --- |
| 空库全量迁移 | 3.539s，版本 `20261111000004`，`dirty=false` |
| 表 / 外键 | 64 张表、28 个外键，全部 InnoDB、全部 utf8mb4 |
| 唯一索引 | 118 个；`orders.uk_order_no`、`orders.uk_user_idempotency`、`flash_sale_order_requests.uk_request_idempotency`、`cart_items.uk_user_sku`、`inventories.uk_sku_id`、`users.uk_phone`、`refund_requests.uk_active` 均存在 |
| 重复迁移 | 再次 `migrate up` 返回"没有待执行的 migration"，无写入 |
| 初始化数据 | 超管 1、权限 73；区域字典此前已验证 3938 条幂等（本轮数据库为新建实例，未重复导入） |
| Redis | PING/读写/TTL 正常；项目 Lua（限流 INCR+EXPIRE、会话撤销）由 `internal/auth`、`internal/controller/iam` 等在真实 Redis 上覆盖 |
| Kafka | Broker 健康；业务 Topic 与 DLQ 存在；Consumer 加入 3 分区；门禁用例覆盖 Publisher、Outbox 恢复、消费与关闭 |

## 13. 临时资源清理

| 项 | 结果 |
| --- | --- |
| 容器/网络/卷 | `p2rem-*` 全部删除（`docker compose down`，未使用 `-v`，随后仅删除本任务命名的卷） |
| 既有资源 | `my-shop-mysql`、`my-shop-redis` 未被启动、修改或删除 |
| 残留进程 | 无 `my-shop serve` / 测试子进程 |
| 监听端口 | 23306 / 26379 / 29092 / 28080 / 28000 均已释放 |
| 七牛测试对象 | 已删除本次创建的 2 个对象 |
| Git 工作区 | clean（仅本报告与修复文件提交） |

## 14. 剩余问题

| 编号 | 等级 | 问题 | 证据 | 阻塞 | 归属 |
| --- | --- | --- | --- | --- | --- |
| R-1 | P1（外部环境） | `img.weniqngsongceshi.online` 无 DNS 记录，公网无法访问已上传对象 | 上传成功且对象元数据与本地一致；`curl` 报 `Could not resolve host`；`223.5.5.5`/`119.29.29.29`/`1.1.1.1` 均 NXDOMAIN | 阻塞"通过公网 URL 读取文件"这一验收项 | Owner / DNS 服务商：按七牛控制台该域名的 CNAME 目标添加 `img` 子域记录；完成后可立即复验 |
| R-2 | P3 | `test/deployk8s` 曾在高负载下 flaky | 上一轮全量运行失败一次，复跑通过；本轮全量与 2 次单跑均通过 | 否 | K8s 发布脚本测试稳定性（超出本轮范围） |
| R-3 | P3 | 前端包体积告警（管理端 index 1.94 MB、商城 index 371 KB） | 构建输出 | 否 | 后续性能优化 |
| R-4 | — | 前后端 API 前缀不一致（前端默认 `/api`，Go 路由为根路径） | `GET /api/products` 经 Nginx 透传后返回 404 | 否（明确排除） | 后续独立前后端联调任务，本轮未修改任何一方业务代码 |

## 15. 最终结论

**PARTIAL**

- 仓库侧三项遗留问题已全部修复并验证：七牛云配置可加载、bucket 校验通过、生产二进制可启动运行并优雅关闭；Nginx 已托管 React/Vue 静态站点并提供 SPA fallback；Stylelint 可执行且退出码 0。
- 真实上传成功，对象在七牛存储中的大小、哈希、MIME 与本地文件一致，测试对象已清理。
- 未达标项：上传对象的**公网 HTTP 读取**因域名缺少 DNS 记录（外部配置）未通过，属 R-1。该问题不由仓库代码引起，也无法在仓库内修复；补齐 DNS 记录后只需重跑读取验证。
- 因此本轮不宣称整体验收 PASS。其余所有规定门禁均通过，无未解决的 P0/P1/P2 代码或部署配置问题。
