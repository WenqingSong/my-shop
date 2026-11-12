# R1：生产部署与 Nginx 路由修复（开发知识文档）

- 分支：`feat/p2-r1-deployment`
- 基线：`ddfd5ab77ced617ae0b0020c46a8220396b52145`（`feat/phase2-ecommerce-business`）
- 范围：只改 `nginx/nginx.conf` 与 Nginx 部署测试脚本；未改 Go / React / Vue 业务代码、Dockerfile、Compose
- 交付报告：`docs/reports/PHASE2_R1_DEPLOYMENT.md`（结论与证据），本文记录**决策依据与可复用知识**

---

## 1. 两个缺陷的根因

### 1.1 `/api` 前缀未剥离

`proxy_pass http://my_shop_backend;` 里**没有 URI 部分**，Nginx 会原样转发完整 URI。因此 `/api/products` 到 Go 仍是 `/api/products`，而 Go 路由表注册在根路径（`internal/cmd/routes_frontend.go` 的 `/login`、`/products`，`internal/cmd/routes_admin.go` 的 `/admin/login`），直接 404。

Nginx 规则（精确表述，避免记错）：

| `proxy_pass` 形式 | 匹配到的 location 前缀 | 上游收到 |
| --- | --- | --- |
| `proxy_pass http://up;`（无 URI） | `/api/` | `/api/products`（原样） |
| `proxy_pass http://up/;`（URI 为 `/`） | `/api/` | `/products`（前缀被替换为 `/`） |
| `proxy_pass http://up/storage/;` | `/api/` | `/storage/products`（前缀被替换） |

所以修复只需在 `/api/` 的 `proxy_pass` 尾部加一个 `/`，但**这个斜杠是唯一开关**：删掉即回退到错误行为，配置仍然语法合法、`nginx -t` 仍然通过。这正是必须加运行时行为测试的原因。

### 1.2 `/storage/` 无代理

Go 用 `s.AddStaticPath("/storage/banners", local.BannerDir())`（`internal/cmd/cmd.go`）对外提供轮播图，`internal/storage/storage.go` 定义 `BannerURLPrefix = "/storage/banners"`。React 首页轮播图直接用 `/storage/banners/<file>`。

修复前无匹配 location → 命中 `location / { try_files $uri $uri/ /index.html; }` → 返回 SPA 入口 HTML（200 + text/html）。图片标签拿到 HTML 时通常只显示破图，不报错，因此很容易被漏测。

正确写法是 `proxy_pass http://my_shop_backend;`（**不带** URI），保留 `/storage/` 前缀；若误加尾斜杠，Go 收到 `/banners/<file>` → 404。

### 1.3 为什么不能改后端迁就前端

Go API 契约（OpenAPI、前端 `vite.config.ts` 的 dev proxy rewrite、后端路由表）全部以「无 `/api` 前缀」为基准。把 Nginx 改成原样透传，再去给 Go 加 `/api` 前缀，会同时打破 dev proxy 语义、OpenAPI 契约和所有既有调用方，属于把配置错误扩散为架构变更，明确禁止。

---

## 2. 改动点与取舍

| 决策 | 选择 | 理由 |
| --- | --- | --- |
| `/api/` 修法 | 只加 `proxy_pass` 尾部 `/` | 最小改动，不动请求头/超时/重试/keepalive 契约 |
| Query String | 不额外配置 | `proxy_pass` 带 URI 时 Nginx 自动把原始 query 追加到新 URI 后，无需 `$is_args$args` |
| Authorization | 不显式 `proxy_set_header` | Nginx 默认透传所有客户端头（仅 `Host`/`Connection` 例外），显式转发反而多一处可维护负担；同时 `scripts/test-nginx.sh` 有一条「生效配置不得出现 authorization 字样」的敏感信息断言（防日志泄漏），显式配置会误触该门禁 |
| `/storage/` 是否加 expires | 不加 | 缓存策略应由上游（Go）响应头决定，Nginx 侧不覆盖，避免与真实响应头冲突 |
| `/storage/` 是否 `proxy_next_upstream off` | 加 | 与 `/api/` 保持同一代理语义，避免出现两套重试策略 |
| 是否改 `/api`（裸路径） | 不改 | 前端始终拼子路径，裸 `/api` 不会出现；新增 404/301 属于行为变更，超出 R1 范围（已在报告「遗留问题」记录） |
| 是否改 Dockerfile / Compose | 不改 | 现有 Dockerfile 已 COPY `nginx/nginx.conf`，实测镜像内配置与工作区 SHA-256 一致 |

---

## 3. 测试脚本设计（可复用的验证手法）

### 3.1 为什么需要运行时行为测试

`test-nginx.sh` 是静态 grep 断言，`nginx -t` 只验语法。两者都无法证明「`/api/products` 到上游变成了什么」。R1 的两个缺陷恰好都是「配置合法、前端不报错、只在真实调用时失败」，所以补一个跑真实 Nginx 进程的行为测试。

### 3.2 拓扑与实现要点

```
docker network surgecart-r1-routing-net
├── surgecart-r1-routing-upstream  (nginx:1.27.3-alpine, --network-alias app, listen 8000)
│     location / { return 200 "method=$request_method uri=$request_uri auth=$http_authorization xff=$http_x_forwarded_for xri=$http_x_request_id host=$host"; }
└── surgecart-r1-routing-web       (nginx:1.27.3-alpine + docker cp 注入 nginx.conf 与占位站点)
```

要点（踩过的坑）：

1. **回显上游用 Nginx 自己实现**：`return 200 "...$request_uri..."` 不需要 python/go/curl 镜像，零额外依赖。
2. **不能用单文件 bind mount**：rootless / 嵌套容器下单文件挂载会退化为目录（容器内文件变目录），Nginx 起不来。统一 `docker create` → `docker cp` → `docker start`。
3. **不要依赖宿主机端口**：本沙箱环境只转发部分白名单端口，`-p 127.0.0.1::80` 随机端口从宿主 curl 不通（容器 IP 也不可达）。改用 `printf ... | docker exec -i <web> nc 127.0.0.1 80` 在容器内发原始 HTTP/1.1 请求，彻底与宿主端口解耦，脚本不占用任何宿主端口。
4. **原始 HTTP 解析**：`tr -d '\r'` 后用 `sed -n '1s/^HTTP\/1\.[01] \([0-9]\{3\}\).*/\1/p'` 取状态码，`sed -n '1,/^$/p'` 取响应头，`sed -e '1,/^$/d'` 取响应体（`Connection: close` 保证响应完整）。
5. **偶发抖动要重试**：`docker exec -i` 偶发提前关闭 stdin，导致容器内 `nc` 收到空请求、响应为空。脚本对「无状态行」重试 3 次，避免误报。
6. **上游 DNS**：`nginx.conf` 用 `resolver 127.0.0.11` + `server app:8000 resolve`，因此上游容器必须用 `--network-alias app` 并入同一用户网络，容器名不必叫 `app`。
7. **静态站点占位**：`/usr/share/nginx/html/web/index.html` = `WEB_SPA_INDEX`、`console/index.html` = `CONSOLE_SPA_INDEX`，用响应体标记区分「命中静态文件」「SPA fallback」「误把 storage 当 SPA」三种情况。
8. **脚本接受配置路径参数**：`bash scripts/test-nginx-routing.sh <conf>` 可用旧配置复跑，用来证明测试能区分正确与错误实现（修复前 8 项 FAIL / 修复后 42 项 PASS）。

### 3.3 静态门禁与运行时门禁分工

- `scripts/test-nginx.sh`（快、无容器，可进 CI）：按 location 块提取断言，锁定两个 `proxy_pass` 的尾斜杠语义、`X-Forwarded-For` 覆盖、`proxy_next_upstream off`、`proxy_read_timeout`，防止后续重构悄悄改回原样透传。**块级提取是必要的**：全局 grep `proxy_pass http://my_shop_backend;` 会被 `/health` 的同类行满足，产生假阳性。
- `scripts/test-nginx-routing.sh`（慢、需 docker）：验证真实转发行为。

未把运行时测试挂进 Makefile/CI：耗时且依赖 docker 网络，是否纳入由主 Chat 决策。

---

## 4. 验证证据（摘要）

| 验证 | 命令 | 结果 |
| --- | --- | --- |
| 语法 | `docker run --rm my-shop-nginx:local nginx -t` | syntax ok / test successful |
| 镜像构建 | `docker compose build nginx`（占位凭据） | `Image my-shop-nginx:local Built` |
| 镜像内配置一致性 | `sha256sum /etc/nginx/nginx.conf` vs 工作区 | 相同 `0596f60e...` |
| 修复前行为 | `bash scripts/test-nginx-routing.sh /tmp/r1-nginx-before.conf` | 8 FAIL / 34 PASS（复现两个缺陷） |
| 修复后行为 | `bash scripts/test-nginx-routing.sh` | 42 PASS |
| 真实产物端到端 | 临时脚本 + `my-shop-nginx:local` | 13 项 PASS，`FAILED=0` |
| 静态门禁 | `bash scripts/test-nginx.sh` | 通过 |
| 清理 | `docker ps -a` / `docker network ls` | 无残留 |

---

## 5. 未做与原因

- 未启动真实 Go 后端（缺 MySQL/Redis/七牛凭据），因此**没有**真实业务接口联调，报告中已明确声明。
- 未跑 Go 单元测试（任务要求不重复运行）。
- 未改 Compose / Dockerfile / K8s manifest（范围冻结）。

---

## 6. 后续可做（不属于 R1）

1. 真实全栈环境补 `/api/login`、`/api/products`、`/storage/banners/*` 联调。
2. 评估 `location = /api { return 404; }` 是否需要（防御裸前缀误用）。
3. 视主 Chat 决策，把 `test-nginx-routing.sh` 纳入发布前检查（如 `make test-nginx-behavior`）。
