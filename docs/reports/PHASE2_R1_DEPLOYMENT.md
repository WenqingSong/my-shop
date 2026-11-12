# Phase 2 · R1：生产部署与 Nginx 路由修复报告

- 任务编号：R1
- 任务名称：生产部署与 Nginx 路由修复（SurgeCart / my-shop）
- 分支：`feat/p2-r1-deployment`
- 开发基线：`ddfd5ab77ced617ae0b0020c46a8220396b52145`（`feat/phase2-ecommerce-business`）
- 集成分支：`feat/phase2-ecommerce-business`
- 仓库：`/root/projects/my-shop-integration`
- Worktree：`/root/projects/my-shop-worktrees/r1-deployment`
- 变更范围：仅 `nginx/**` 与 Nginx 部署测试脚本；未改 `web/**`、`admin/**`、`internal/**`、`api/**`、`migrations/**`，未改任何 Go / React / Vue 业务代码。

---

## 1. 修复前的问题

### 问题 1：`/api` 前缀未剥离，生产环境所有前端 API 请求 404

修复前配置（`nginx/nginx.conf`）：

```nginx
# API 反向代理：保持既有语义，原样透传 /api/ 前缀（前缀对齐由后续联调任务处理）。
location /api/ {
    proxy_pass http://my_shop_backend;
}
```

`proxy_pass` 不带 URI 部分时，Nginx 原样转发原始 URI，`/api` 前缀不会被剥离。
而 Go 路由注册在根路径（源码取证）：

```64:64:internal/cmd/routes_admin.go
	root.POST("/admin/login", adminCtrl.Login)
```

```71:75:internal/cmd/routes_frontend.go
	root.POST("/login", iamCtrl.Login)
	...
	root.GET("/products", productCtrl.List)
```

两个前端的生产 API Base URL 均为 `/api`（`admin/.env.production` 的 `VITE_API_BASE_URL = /api`；`web/src/api/client.ts` 默认回退 `/api`，`web/.env.example` 亦注明「转发层剥离 /api 前缀」）。

因此浏览器 `POST /api/login` → Go 实际收到 `POST /api/login`，而 Go 只注册了 `POST /login` → 生产环境全部 API 调用失败。

隔离回显上游实测（修复前配置，见第 5 节命令）：

```text
FAIL  上游收到 /products?page=1（前缀已剥离、Query 保留）
      实际 body=method=GET uri=/api/products?page=1 auth= xff=127.0.0.1 ...
FAIL  上游收到 POST /login
      实际 body=method=POST uri=/api/login auth=Bearer r1-test-token ...
FAIL  上游收到 /admin/login
      实际 body=method=POST uri=/api/admin/login auth=Bearer r1-admin-token ...
```

### 问题 2：`/storage/` 无代理，轮播图请求被 React SPA fallback 接住

Go 侧把本地图片目录映射为 `/storage/banners`（源码取证）：

```231:231:internal/cmd/cmd.go
	s.AddStaticPath(storage.BannerURLPrefix, local.BannerDir())
```

`internal/storage/storage.go` 中 `BannerURLPrefix = "/storage/banners"`，React 轮播图直接使用 `/storage/banners/<file>`（如 `web/src/mocks/data/catalog.ts` 的 `banner-1.png`）。

修复前 `nginx.conf` 没有 `/storage/` location，请求落入 `location / { try_files $uri $uri/ /index.html; }` → 返回 `index.html`（HTTP 200 + `text/html`），图片损坏且掩盖真实错误。

```text
FAIL  上游收到原始 URI /storage/banners/test.png（前缀未被剥离）
      实际 body=WEB_SPA_INDEX
FAIL  /storage/ 未进入 React SPA fallback
      响应体不应包含 'WEB_SPA_INDEX'，实际 body=WEB_SPA_INDEX
```

### 修复前其余行为（同一次实测，均正常，需保持不变）

`/`、`/products/1`、`/assets/*`、`/console/`、`/console/order/list`、`/console/static/*`、`/console` 301、`/health`、`/metrics` 404、安全响应头、XFF/X-Request-ID 覆盖语义在修复前全部通过，本次改动未触碰这些 location。

---

## 2. 修复后的配置

### 2.1 API 前缀剥离（`/api/` → Go 根路径）

```nginx
# API 反向代理：剥离 /api 前缀后转发（Go 路由注册在根路径，无 /api 前缀）。
#
# 路径契约：/api/products → /products，/api/admin/login → /admin/login。
# proxy_pass 带 URI 部分（尾部单个 "/"）时，匹配到的 location 前缀 /api/ 被替换为 /；
# 尾部斜杠是这条语义的唯一开关，删掉它会退回「原样透传 /api → 后端 404」。
# Query String 不受 URI 替换影响，由 Nginx 自动附加；
# Authorization 等客户端请求头默认原样透传（此块未清除任何客户端头）。
location /api/ {
    proxy_pass http://my_shop_backend/;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
    proxy_set_header X-Request-ID    $request_id;
    proxy_set_header X-Real-IP       $remote_addr;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Host            $host;
    proxy_connect_timeout 3s;
    proxy_send_timeout    30s;
    proxy_read_timeout    30s;
    proxy_next_upstream off;
}
```

仅 `proxy_pass` 增加尾部 `/`，其余请求头契约、超时、重试策略（`proxy_next_upstream off`）与 keepalive 复用均保持原样。

### 2.2 storage 静态资源代理（保留原始 URI）

```nginx
# 后端本地静态资源（轮播图等）：/storage/banners/<file> 原样代理到 Go 后端。
#
# Go 侧 s.AddStaticPath("/storage/banners", ...) 直接服务该前缀，
# 因此 proxy_pass 不带 URI 部分，保留原始 URI（含 /storage/ 前缀）；
# 若误加尾部 "/"，前缀会被替换为 /，后端收到 /banners/<file> → 404。
# 独立 location 同时保证该前缀不落入 React SPA fallback（否则图片请求返回 index.html）。
location /storage/ {
    proxy_pass http://my_shop_backend;   # 注意：无尾部斜杠
    proxy_http_version 1.1;
    proxy_set_header Connection "";

    proxy_set_header X-Request-ID    $request_id;
    proxy_set_header X-Real-IP       $remote_addr;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Host            $host;

    proxy_connect_timeout 3s;
    proxy_send_timeout    30s;
    proxy_read_timeout    30s;

    # 静态资源代理同样不做自动重试（与 /api/ 保持同一代理语义）。
    proxy_next_upstream off;
}
```

`/api/` 与 `/storage/` 两个 location 的 `proxy_pass` 尾部斜杠语义相反且互为镜像，因此两处均加了「删除/误加斜杠会导致什么」的显式注释，并由测试脚本按 location 块断言锁定。

### 2.3 未改动项

- `/`、`/assets/`、`/console/`、`/console/static/`、`location = /console`、`location = /health`、`location = /metrics`、`error_page 502 504 413`、`add_header`、`server_tokens off`、`log_format`、`upstream`/`resolver` 全部保持原状。
- `nginx/Dockerfile`、`docker-compose.yml` 未改（镜像已按现有 Dockerfile 正确内置新配置，实测哈希一致）。

---

## 3. 修改文件清单

| 文件 | 类型 | 说明 |
| --- | --- | --- |
| `nginx/nginx.conf` | 修改 | `/api/` 改为剥离前缀转发；新增 `location /storage/`；同步更新头部路径契约注释 |
| `scripts/test-nginx-routing.sh` | 新增 | 隔离回显上游的行为测试（42 项断言），验证真实转发 URI、Query、Authorization、前端路由、安全头 |
| `scripts/test-nginx.sh` | 修改 | 新增 path contract 静态断言：按 location 块提取 `/api/`、`/storage/` 并校验尾部斜杠语义及超时/重试配置未退化 |
| `docs/reports/PHASE2_R1_DEPLOYMENT.md` | 新增 | 本报告 |
| `.agent/tasks/dev/R1-deployment.md` | 新增 | R1 开发记录（决策与取证） |

未修改任何 Go / 前端业务代码、Dockerfile、Compose、密钥或凭据。

---

## 4. 关键路径映射

| 浏览器请求 | Nginx location | 上游（Go）实际收到 | 依据 |
| --- | --- | --- | --- |
| `GET /` | `location /` | 不转发，返回 `web/dist/index.html` | React 入口 |
| `GET /products/1` | `location /` | 不转发，SPA fallback `index.html` | 前端深层路由 |
| `GET /assets/<hash>.js` | `location /assets/` | 不转发，静态文件 + `expires 7d` | React 资源 |
| `GET /console` | `location = /console` | 301 → `/console/` | 管理端入口 |
| `GET /console/` | `location /console/` | 不转发，返回 `admin/dist/index.html` | Vue 入口 |
| `GET /console/order/list` | `location /console/` | 不转发，SPA fallback `/console/index.html` | Vue 深层路由 |
| `GET /console/static/<hash>.css` | `location /console/static/` | 不转发，静态文件 + `expires 7d` | Vue 资源 |
| `GET /api/products?page=1` | `location /api/` | `GET /products?page=1` | 前缀剥离，Query 保留 |
| `POST /api/login` | `location /api/` | `POST /login` | 前缀剥离，Authorization 透传 |
| `POST /api/admin/login` | `location /api/` | `POST /admin/login` | 前缀剥离 |
| `GET /storage/banners/test.png` | `location /storage/` | `GET /storage/banners/test.png` | 原样保留，不进 SPA fallback |
| `GET /health` | `location = /health` | `GET /health` | 容器 healthcheck 依赖 |
| `GET /metrics` | `location = /metrics` | 不转发，404 | 公共入口隔离 |

---

## 5. 验证命令与真实输出摘要

所有验证均在 worktree `/root/projects/my-shop-worktrees/r1-deployment` 下执行；未运行 Go 单元测试。

### 5.1 `nginx -t`（真实构建镜像内）

```bash
docker run --rm my-shop-nginx:local nginx -t
```

```text
nginx: the configuration file /etc/nginx/nginx.conf syntax is ok
nginx: configuration file /etc/nginx/nginx.conf test is successful
```

镜像内置配置与工作区一致（同一 SHA-256）：

```text
0596f60e7c6ca8a1c50ae3d33ac09788d4794a8fdd38425db6a6931c09a941f1  /etc/nginx/nginx.conf
0596f60e7c6ca8a1c50ae3d33ac09788d4794a8fdd38425db6a6931c09a941f1  nginx/nginx.conf
```

### 5.2 Nginx 镜像构建

```bash
MYSQL_ROOT_PASSWORD=test-root AUTH_JWT_SECRET=0123456789abcdef0123456789abcdef \
  docker compose build nginx
```

```text
#19 [stage-2 2/5] COPY nginx/nginx.conf /etc/nginx/nginx.conf            DONE
#23 exporting to image ... done
 Image my-shop-nginx:local Built
```

（占位变量仅用于满足 `docker-compose.yml` 的 `${VAR:?}` 必填校验，不参与运行、不是真实凭据。）

### 5.3 隔离回显上游行为测试（修复前 vs 修复后）

测试拓扑：`nginx:1.27.3-alpine` 作回显上游（`--network-alias app`，监听 8000，响应体回写 `$request_method`、`$request_uri`、`$http_authorization` 等），被测 Nginx 用 `docker cp` 注入 `nginx/nginx.conf` 与占位静态站点。请求经容器内 `nc` 发起，不占用/依赖宿主端口。

```bash
# 修复前（旧配置，用于证明测试能区分正确与错误实现）
bash scripts/test-nginx-routing.sh /tmp/r1-nginx-before.conf
# → Nginx 路由行为测试失败：8 项不通过，34 项通过（exit 1）

# 修复后（工作区配置）
bash scripts/test-nginx-routing.sh
# → Nginx 路由行为测试通过：42 项断言全部通过（exit 0）
```

修复后关键断言的真实输出：

```text
PASS  上游收到 /products?page=1（前缀已剥离、Query 保留）
      body=method=GET uri=/products?page=1 auth= xff=127.0.0.1 xri=6b1de28... host=localhost
PASS  上游未收到 /api/ 前缀
PASS  上游收到 POST /login            body=method=POST uri=/login auth=Bearer r1-test-token ...
PASS  Authorization 请求头完整透传    auth=Bearer r1-test-token
PASS  上游收到 /admin/login           body=method=POST uri=/admin/login auth=Bearer r1-admin-token ...
PASS  多参数 Query String 完整保留    uri=/products?page=2&keyword=abc&sort=price
PASS  上游收到原始 URI /storage/banners/test.png（前缀未被剥离）
PASS  /storage/ 未进入 React SPA fallback（body 不含 'WEB_SPA_INDEX'）
PASS  React 首页 / 返回 React 入口 / /products/1 走 SPA fallback / /assets/* 命中真实静态文件
PASS  Vue 首页 /console/ 与 /console/order/list 返回 Vue 入口 / /console/static/* 命中真实静态文件
PASS  /console 重定向（status=301，Location: /console/）
PASS  /health 透传到上游；/metrics 公共入口 404 且未转发上游
PASS  客户端伪造的 X-Forwarded-For 被丢弃；伪造的 X-Request-ID 被覆盖为 32 位十六进制
PASS  静态/代理/storage 响应均带 X-Content-Type-Options: nosniff、X-Frame-Options: DENY
PASS  Server 响应头不暴露 Nginx 版本（Server: nginx）
```

### 5.4 真实生产镜像 + 真实前端产物端到端校验

用 `my-shop-nginx:local`（含真实 `web/dist`、`admin/dist`）接入回显上游，逐路径请求：

```text
PASS  GET / 返回 React 入口(id=root)
PASS  GET /products/1 返回同一 React 入口
PASS  GET /console/ 返回 Vue 入口(id=app)
PASS  GET /console/order/list 返回 Vue 入口
PASS  GET /assets/account-errors-BoLRk3R3.js 命中 React 静态资源(200)
PASS  GET /console/static/css/403-TO6oBxFK.css 命中 Vue 静态资源(200)
PASS  GET /console 301 / Location: /console/
PASS  /api/products?page=1 → uri=/products?page=1
PASS  /api/admin/login → uri=/admin/login + auth 透传
PASS  /storage/banners/banner-1.png 原样上游
PASS  /health 透传；/metrics 404
PASS  安全头 nosniff / DENY
FAILED=0
```

### 5.5 静态校验（含新增路径契约断言）

```bash
bash scripts/test-nginx.sh
```

```text
nginx: the configuration file /etc/nginx/nginx.conf syntax is ok
[INFO] Nginx 反向代理静态校验通过（信任边界 / 重试边界 / DNS 再解析 / 监控隔离 / 日志关联 / 编排 / 统一入口）
```

新增断言覆盖：`/api/` 块内 `proxy_pass http://my_shop_backend/;`（带尾斜杠）、`/storage/` 块内 `proxy_pass http://my_shop_backend;`（无尾斜杠）、两块的 `X-Forwarded-For` 覆盖、`proxy_next_upstream off`、`proxy_read_timeout` 未被删改。断言以 location 块为单位提取，避免全局 grep 把 `/health` 的 `proxy_pass http://my_shop_backend;` 误当满足条件。

### 5.6 清理

临时容器与网络全部删除，验证结束无残留：

```text
无残留容器
无残留网络
```

---

## 6. 未验证内容

1. **未做真实 Go 后端联调**：本环境无真实 app 容器（需 MySQL/Redis/七牛凭据），未启动 Compose 全栈。所有 API 验证均使用「隔离回显上游」，验证的是 Nginx 转发到上游的 method/URI/Query/请求头，不等于真实业务接口返回正确。未声称完成真实 Go 联调。
2. **未验证真实图片字节**：`/storage/` 验证的是 URI 与「不返回 index.html / 200 且 body 来自上游」，未用真实 `banner-1.png` 校验图片内容与 MIME（Go 上游未启动）。
3. **未验证浏览器端真实登录/下单流程**：未启动前端 dev server 与后端，仅用构建产物验证路由与静态资源命中。
4. **未验证 HTTPS / 多层代理场景**：`X-Forwarded-Proto` 只在单跳 HTTP 下验证。
5. **未做性能/并发验证**（超出 R1 范围）。

---

## 7. Git 分支与最终 SHA

| 项目 | 值 |
| --- | --- |
| 开发基线 | `ddfd5ab77ced617ae0b0020c46a8220396b52145` |
| 开发分支 | `feat/p2-r1-deployment` |
| 配置与测试提交 | `a589a50` fix(nginx): 修复生产 API 前缀剥离与 storage 静态资源代理 |
| 报告提交 | `<见文末 Git 元数据回填>` |

---

## 8. 是否已 Push

`git push -u origin feat/p2-r1-deployment` 普通推送（无 `--force`），结果见文末元数据。

---

## 9. 工作区状态

提交后 `git status --short` 为空，工作区 clean；未触碰 `main`、未合并集成分支、未修改集成分支。

---

## 10. 遗留问题

1. **`/api`（无尾斜杠）仍落入 SPA fallback**：`location /api/` 不匹配裸 `/api`，该请求会返回 React `index.html`（200）。当前两个前端的 baseURL 始终拼接子路径（`/api/xxx`），生产不会产生裸 `/api` 请求，且修复裸 `/api` 需新增重定向/404 语义，超出 R1 范围，故未改动。如需防御可后续加 `location = /api { return 404; }`。
2. **`/storage` 前缀之外不代理**：仅 `location /storage/` 生效；若后续新增 `/storage/avatars` 等其他静态目录，无需改配置（同前缀覆盖），但裸 `/storage`（无尾斜杠）仍会落 SPA。
3. **真实环境验收待补**：需要在有 Go 后端的真实/隔离全栈环境补一次 `/api/login`、`/api/products`、`/storage/banners/*` 的真实联调（本报告未声称完成）。
4. **未纳入 `make test-nginx`**：新增的运行时测试需要拉取/启动容器，耗时且依赖 docker 网络，故未挂进 Makefile 静态门禁；运行方式已写入脚本头注释：`bash scripts/test-nginx-routing.sh`。是否纳入 CI 由主 Chat 决定。

---

## Git 元数据（提交后回填）

- 分支：`feat/p2-r1-deployment`
- 代码提交：`a589a50`
- 报告提交：待回填
- 推送：待回填
- 工作区：待回填
