# 容器构建与运行安全（T2-04）

本文说明 SurgeCart 后端镜像的构建方式、容器运行安全边界、进程生命周期、健康检查、Secret 注入与镜像扫描方法。事实来源为 `Dockerfile`、`.dockerignore`、`docker-compose.yml`、`scripts/docker-entrypoint.sh` 与 `scripts/test-container*.sh`；文档与这些文件不一致时以文件为准并修正文档。

## 1. 范围

- 生产镜像：`Dockerfile` 默认目标 `runtime`。
- 验证镜像：`Dockerfile` 目标 `lifecycle-verify`，仅用于真实容器生命周期验证（见 §7）。
- 编排：`docker-compose.yml` 的 `app` 服务；MySQL / Redis / Kafka / Nginx / Prometheus 的运行语义不变。
- 不覆盖：Kubernetes 清单、Ingress、多副本滚动更新（T2-06）。

## 2. 镜像构建

### 2.1 命令

```bash
# 生产镜像（默认目标）
docker build -t my-shop-app:local .

# 显式指定目标（语义与默认一致）
docker build --target runtime -t my-shop-app:local .

# CI 发布镜像（短 SHA + latest 双标签，见 scripts/docker-build.sh）
make docker-build
```

构建要求 Docker 23+（BuildKit 默认启用）。`go mod download` 与 `go build` 使用 `--mount=type=cache`，重复构建不会重复下载模块。

### 2.2 基础镜像与版本固定

| 用途 | 镜像 | 解析到的 digest（linux/amd64） |
| --- | --- | --- |
| 构建 | `golang:1.27.3-alpine3.24` | `sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18` |
| 运行 | `alpine:3.24.4` | `sha256:93b41ef40b1677fce8fcc64ff4af09daddfacf5040afde618bc46c1e2d1cc7d4` |

- `FROM` 只使用 patch 级固定标签，不使用 `latest` 或浮动 minor 标签。升级基础镜像必须同时更新 `Dockerfile` 的 `ARG` 默认值与本节 digest。
- `ARG GO_IMAGE` / `ARG ALPINE_IMAGE` 可在构建时覆盖（用于镜像仓库镜像源或临时验证），但默认值就是发布基线。
- 运行层执行 `apk upgrade --no-cache` 吸收同一 alpine minor 分支的 patch 修订（例如 zlib 1.3.2 → 1.4），再安装 `tzdata`、`ca-certificates`。
- Go 工具链：`golang:1.27.3`；`go.mod` 的 `go` 指令为 `1.26.0`。`GOTOOLCHAIN=local` 禁止构建期自动下载其它工具链。
- Go 依赖版本由 `go.mod` / `go.sum` 锁定；`GOFLAGS=-mod=readonly` 保证构建不改写依赖清单。

### 2.3 构建参数与可复现性

- `CGO_ENABLED=0`、`GOOS=linux`：静态二进制，运行层不需要 glibc。
- `-trimpath`：去除构建宿主路径；`-buildvcs=false`：不嵌入 VCS 信息，构建目录无 `.git` 时结果一致。
- `-ldflags="-s -w"`：去除符号表与 DWARF。
- 构建上下文由 `.dockerignore` 收敛：排除 `.env`、`bin/`、`tmp/`、`logs/`、`storage/` 运行数据、`.git/`、IDE、`docs/`、`.agent/`、`*.md`、`*_test.go`、容器测试产物、`nginx/`、`prometheus/`、编排与流水线文件。`test/serveproc` 保留在上下文中，仅供 `lifecycle-verify` 目标编译。
- 已验证从干净上下文构建成功（BuildKit，缓存为空时仍可完成）。镜像大小约 60.6 MiB（`docker image inspect -f '{{.Size}}'`）。

### 2.4 镜像内容

运行层只包含：

```text
/app/my-shop                         静态编译二进制（root:root 0555）
/app/manifest/config/config.yaml     非敏感默认配置（root:root 0444，敏感字段为空）
/app/docker-entrypoint.sh            migrate up → exec serve（root:root 0555）
/app/storage                         运行用户 app:app 0755（named volume 挂载点）
/usr/share/zoneinfo/...              tzdata（Asia/Shanghai）
/etc/ssl/certs/ca-certificates.crt   CA 根证书（七牛 HTTPS）
```

不含：Go 工具链、源码（`*.go`）、测试文件、`.env`、文档、构建工具、curl/wget/bash 等额外诊断软件包（健康检查使用 busybox wget）。`scripts/test-container-runtime.sh` 通过导出镜像文件系统列表对这些事实做自动断言。

## 3. 运行时安全

### 3.1 非 root 与文件权限

| 路径 | 属主 | 权限 | 说明 |
| --- | --- | --- | --- |
| `/app/my-shop` | root | 0555 | 运行用户不可修改二进制 |
| `/app/manifest/config/config.yaml` | root | 0444 | 只读配置，敏感字段为空 |
| `/app/docker-entrypoint.sh` | root | 0555 | 只读可执行 |
| `/app/storage` | app (10001) | 0755 | 唯一持久化写路径（named volume） |
| `/tmp` | root | 1777 | tmpfs，`noexec,nosuid,nodev`，容器重建即清空 |

- 镜像 `USER 10001:10001`；Compose 再显式声明 `user: "10001:10001"`，防止编排层覆盖。
- 启动期不执行 `chown`/`chmod`，不需要 root。
- 数据卷首次挂载继承镜像目录的 `app:app` 属主，非 root 可直接写 `/app/storage/banners`。

### 3.2 只读根文件系统

`docker-compose.yml` 的 `app` 服务：

```yaml
read_only: true
tmpfs:
  - /tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777
```

应用当前唯一的写路径：

1. `/app/storage`：轮播图占位图 seed 与本地文件存储（`STORAGE_LOCAL_ROOT=/app/storage`）。
2. `/tmp`：临时文件（当前代码未依赖，保留给运行时库/未来扩展）。

如果新增写目录，必须显式声明卷或 tmpfs，否则会得到 `Read-only file system`。日志统一写 stdout/stderr（`docker logs`），不落文件。

### 3.3 Linux capabilities 与提权

```yaml
cap_drop:
  - ALL
security_opt:
  - no-new-privileges:true
```

- 应用监听 8000（非特权端口），不需要 `NET_BIND_SERVICE`；不执行 `chown`、不修改网络栈、不加载内核模块，因此不需要任何 capability。
- 验证：`docker exec <app> grep CapEff /proc/1/status` 应为 `0000000000000000`。
- seccomp 使用 Docker 默认 profile；未启用 `privileged`、`cap_add`、host 网络、host PID、Docker Socket 挂载。`scripts/test-container.sh` 对 compose 做负向断言，`scripts/test-container-runtime.sh` 读取 Docker 实际生效值复核。

### 3.4 网络与入口

- `app` 不发布宿主机端口；Compose 网络内由 Nginx（`${NGINX_PORT:-8080}`）作为唯一外部入口，Prometheus 直连 `app:8000`。
- 不使用 host 网络：应用需要访问 Compose DNS（`mysql` / `redis` / `kafka`），也需要出站 HTTPS 访问七牛对象存储。
- 数据库/Redis 宿主机映射仍绑定 `127.0.0.1`（未改变原有边界）。

## 4. 进程生命周期与优雅关闭

### 4.1 PID 1 与信号

`scripts/docker-entrypoint.sh`：

```sh
/app/my-shop migrate up
exec /app/my-shop serve
```

- `exec` 使 Go 应用替换 shell，成为容器 PID 1；Docker/Kubernetes 的 SIGTERM 直接送达应用，不经 shell 转发。
- 容器内只有一个常驻进程，不存在需要 `tini` 回收的孤儿/僵尸进程；未引入额外 init 进程。验证：`docker exec <app> cat /proc/1/cmdline` = `/app/my-shop serve`，`/proc` 中常驻进程数为 1。
- `migrate up` 阶段（exec 之前）尚未监听端口，没有在途请求；此时收到 SIGTERM 会直接终止容器，不进入优雅关闭路径，也不会造成数据不一致（migration 由 `GET_LOCK` + `schema_migrations` 保证幂等）。

### 4.2 终止预算（T2-03 冻结，未改变）

| 阶段 | 时间 |
| --- | ---: |
| Kubernetes preStop | 5s |
| HTTP Drain | 10s |
| Runtime Shutdown | 30s |
| 合计 | 45s |
| terminationGracePeriodSeconds / Compose `stop_grace_period` | 60s |
| 安全余量 | 15s |

- 应用启动时校验预算并输出 `termination_budget`；不足即 fail-fast。
- Compose `app.stop_grace_period: 60s`，其他服务不声明。Docker 实际生效值 `Config.StopTimeout=60`（容器门禁断言）。
- HTTP drain 的前 9s 用于优雅 drain，最后 1s 用于强制关闭 listener/连接；超时路径为 `http_server_drain_timeout → http_server_force_closed → http_server_stopped(timed_out=true)`，之后才关闭 Publisher/Consumer/Producer/Scanner。

### 4.3 真实容器验证结果（2026-11-07）

`bash scripts/test-container-runtime.sh`，15 项断言全部 PASS：

- 空闲停止：`SIGTERM(signal=terminated) → http_server_stopped(timed_out=false) → flashsale_runtime_shutdown_complete`，退出码 0，耗时 269ms。
- 有在途阻塞请求（`/__lifecycle/block`）：`drain_begin → drain_timeout(9000ms, active_connections=1) → force_closed(forced_connections=1) → stopped(timed_out=true) → runtime_shutdown_begin → shutdown_complete`，退出码 0，容器停止耗时 9227ms（< 60s 预算）。
- 停止后容器 PID=0、宿主机 PID 不再存活、`OOMKilled=false`；退出码非 137，说明 Docker 未提前 SIGKILL。

## 5. 健康检查

| 位置 | 检查 | 语义 |
| --- | --- | --- |
| 镜像 `HEALTHCHECK` | `wget /health`，10s 间隔 / 5s 超时 / 60s 启动宽限 / 5 次重试 | 仅进程存活，供 `docker run` 使用 |
| Compose `app.healthcheck` | 同值 `/health` | 不因 DB/Redis 抖动触发 restart |
| Kubernetes（T2-06） | livenessProbe `/health`、readinessProbe `/ready` | K8s 忽略镜像 HEALTHCHECK |

- `/health` 不访问 MySQL/Redis/Kafka；`/ready` 只判定 MySQL + Redis，Kafka 故障不影响两者。
- 镜像不额外安装 curl/wget：alpine 自带 busybox wget 已满足健康检查。
- 两处 healthcheck 参数由 `scripts/test-container.sh` 断言一致，避免漂移。

## 6. 环境变量与 Secret 注入

镜像内 `manifest/config/config.yaml` 只保留非敏感默认值，敏感字段为空。运行时按下表注入：

| 变量 | 敏感 | 用途 |
| --- | --- | --- |
| `DATABASE_DEFAULT_HOST/PORT/USER/NAME/CHARSET` | 否 | MySQL 连接 |
| `DATABASE_DEFAULT_PASS` | 是 | MySQL 密码（Compose 使用 `${MYSQL_ROOT_PASSWORD:?}`） |
| `REDIS_DEFAULT_ADDRESS/DB` | 否 | Redis 连接 |
| `REDIS_DEFAULT_PASS` | 是 | Redis 密码（启用认证时） |
| `AUTH_JWT_SECRET` | 是 | JWT 签名密钥（≥32 字节，无内置默认） |
| `ADMIN_SUPER_USERNAME` / `ADMIN_SUPER_PASSWORD` | 密码是 | 超管初始化（仅首次建号需要） |
| `QINIU_ACCESS_KEY` / `QINIU_SECRET_KEY` | SecretKey 是 | 七牛凭据（启动 required dependency） |
| `QINIU_BUCKET` / `QINIU_DOMAIN` / `QINIU_REGION` | 否 | 七牛存储空间与域名 |
| `STORAGE_LOCAL_ROOT` | 否 | 本地存储根目录（Compose 固定 `/app/storage`） |
| `SERVER_ADDRESS` / `SERVER_GRACEFUL_TIMEOUT` | 否 | 监听地址与 HTTP drain 预算 |
| `LIFECYCLE_PRE_STOP_DELAY` / `LIFECYCLE_TERMINATION_GRACE_PERIOD` | 否 | 终止预算（默认 5s / 60s） |
| `FLASH_SALE_*` | 否 | 秒杀消费模式与 Kafka 参数 |

- 本地：`.env`（`make init` 生成，Git 忽略），Compose 注入；缺失必填值由 `${VAR:?}` 与 serve fail-fast 双重拦截。
- 集群：由 Kubernetes Secret 注入同名环境变量；Secret 名称、RBAC 与轮换由部署环境确定（T2-06）。不要把真实值写入镜像、Compose 默认值、命令行参数或日志。
- 验证：`docker image inspect -f '{{json .Config.Env}}' my-shop-app:local` 只应包含 `PATH` 等非敏感变量；门禁用关键字扫描断言。

## 7. 验证命令

```bash
# 1) 静态门禁（无需构建镜像）
make test-container

# 2) 真实构建 + 容器生命周期门禁（需要 Docker / openssl / python3 / Go）
make test-container-runtime

# 3) 手工构建与检查
docker build -t my-shop-app:local .
docker image inspect -f '{{.Config.User}} {{json .Config.Healthcheck}}' my-shop-app:local
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m \
  --entrypoint /bin/sh my-shop-app:local -c 'id; ls -l /app; touch /app/x || true'

# 4) 以加固参数手工运行（仅供本地排查；生产由 Compose/Nginx 编排）
docker run --rm --name my-shop-app \
  --read-only --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m \
  --cap-drop ALL --security-opt no-new-privileges:true \
  -p 127.0.0.1:8000:8000 \
  -e DATABASE_DEFAULT_HOST=... -e DATABASE_DEFAULT_PASS=... \
  -e AUTH_JWT_SECRET=... -e QINIU_ACCESS_KEY=... -e QINIU_SECRET_KEY=... \
  -e QINIU_BUCKET=... -e QINIU_DOMAIN=... \
  my-shop-app:local

# 5) 停止并查看退出码 / 关闭事件
docker stop my-shop-app
docker inspect -f '{{.State.ExitCode}}' my-shop-app
docker logs my-shop-app | grep -E 'http_server|flashsale_runtime_shutdown'
```

`lifecycle-verify` 说明：生产 `serve` 启动需要真实七牛凭据（`GetBucketInfo` 外部只读校验）。容器生命周期门禁使用：

```bash
docker build --build-context t204-certs=<证书目录> \
  --target lifecycle-verify -t my-shop-app:local-verify .
```

该目标与 `runtime` 共用同一个 `runtime-base` 阶段（相同基础镜像、USER、HEALTHCHECK、目录权限与 entrypoint），仅二进制额外启用 `serve_lifecycle` 构建标签（测试阻塞路由 + 七牛 UC endpoint 覆盖）。生产目标 `runtime` 不包含该二进制，也不读取测试环境变量。

## 8. 镜像安全扫描

工具：Trivy 0.76.2（镜像漏洞 + Dockerfile misconfiguration）、govulncheck v1.9.0（Go 调用图可达性）。

```bash
trivy image --scanners vuln --severity CRITICAL,HIGH,MEDIUM,LOW my-shop-app:local
trivy config Dockerfile
govulncheck ./...
```

### 8.1 结果（2026-11-07，镜像 `my-shop-app:t204`）

| 严重级 | 数量 | 说明 |
| --- | ---: | --- |
| Critical | 0 | — |
| High | 0 | — |
| Medium | 0 | — |
| Low | 0 | — |
| Unknown | 1 | `GO-2026-5932`：`golang.org/x/crypto/openpgp` 不再维护 |

- 修复前（同环境基线）：47 个（High 25 / Medium 19 / Low 1 / Unknown 2），主要是过期的 `golang.org/x/crypto`、`x/net`、`x/text`、`x/sys`、`otel` 与 alpine `zlib`。本次升级 Go 工具链至 1.27.3、升级相关模块并执行 `apk upgrade` 后，OS 包与有版本修复的模块漏洞均清零。
- `govulncheck ./...`：**0 个可达漏洞**（0 symbols / 0 packages）；仅 1 个模块级提示（同上 openpgp），应用调用图不包含 `openpgp`。
- Dockerfile misconfiguration：0 failures（`trivy config Dockerfile`）。
- 敏感文件：镜像文件系统审计未发现源码、`.env`、私钥或开发默认 JWT 密钥；`trivy image --scanners secret` 在该镜像的 alpine/gobinary 目标上为 `Not scanned`，因此不将 Trivy secret 结果作为通过依据，改用 `scripts/test-container.sh` 的凭据断言 + 导出根文件系统的私钥/密钥模式检查。

### 8.2 未修复项与缓解

| 项 | 原因 | 影响 | 缓解 |
| --- | --- | --- | --- |
| `GO-2026-5932`（x/crypto/openpgp 不维护） | 官方仅提供未发布的伪版本修复，升级会引入不可复现的 commit 依赖 | 应用不导入 `openpgp`，govulncheck 判定不可达 | 保持 `x/crypto` 最新正式版；出现正式版本后升级并重扫；发布前保留 govulncheck 门禁 |

Trivy/govulncheck 的漏洞库随时间变化；本文记录的是上述日期的实际扫描结果。无法联网或工具缺失时必须标记 NOT_VERIFIED，不能写成 PASS。

## 9. 故障排查

| 现象 | 可能原因 | 处理 |
| --- | --- | --- |
| `Read-only file system` | 应用尝试写 `/app` 或其它非卷路径 | 只允许写 `/app/storage` 与 `/tmp`；新增写路径需声明卷/tmpfs |
| `Permission denied` 读取 `config.yaml` | `COPY --chmod` 把父目录权限改成 0444，目录失去 `x` 位 | 目录权限用 `RUN chmod 0755` 显式固定；文件权限单独设置 |
| 容器 `unhealthy` | `/health` 未被监听（启动失败）或健康检查命令缺失 | `docker logs` 查看 bootstrap 错误；确认镜像内有 busybox `wget` |
| 启动失败提示 `qiniu bucket ... 可用性检查失败` | 七牛凭据缺失/错误或出站 HTTPS 不可达 | 注入真实 AK/SK/bucket/domain；检查 DNS 与 CA 证书 |
| `docker stop` 耗时接近 60s | 关闭流程未完成，被 SIGKILL | 查看 `http_server_drain_*` / `flashsale_runtime_shutdown_*` 日志；确认 `stop_grace_period: 60s` 未被覆盖 |
| 退出码 137 | SIGKILL（超预算或 OOM） | 检查关闭耗时、内存限制；137 不等于优雅关闭 |
| 容器内 `apk` 不可用 / 无法装包 | 运行层按最小化原则不含额外诊断包 | 用 `docker cp` 或临时调试镜像排查；不要把调试工具带入发布镜像 |
| 嵌套 Docker 下 bind mount 变空目录 | daemon 与工作区文件系统命名空间不同 | 使用 named volume（本项目约定）；测试证书改为构建期注入 |

## 10. NOT_VERIFIED 与已知限制

以下项目在本任务环境无法真实验证，明确标记为 NOT_VERIFIED，不用静态检查冒充运行结果：

1. **Kubernetes 探针与滚动更新**：无集群，`terminationGracePeriodSeconds`、preStop、readinessProbe 摘流未在集群验证（T2-06）。
2. **生产镜像完整 `serve` 启动**：需要真实七牛凭据与外网 `GetBucketInfo`。生产镜像已验证构建、元数据、文件系统、非 root/只读下执行 `migrate version`、配置可读；HTTP/SIGTERM 语义在 `lifecycle-verify` 目标（同一 runtime-base、同一 entrypoint、同一 USER，二进制仅多测试构建标签）上验证。生产二进制不含测试路由/绕过开关。
3. **生产镜像的 Docker HEALTHCHECK 健康态**：`serve` 未在容器内完整启动，无法观察到 `healthy`；同值 HEALTHCHECK 在 `lifecycle-verify` 容器上验证为 `healthy`。
4. **多副本并发 migration/seed、Kafka Consumer Group 重平衡、跨 Pod 本地存储**：单副本范围外的行为未验证。
5. **Secret 轮换/外部密钥管理**：环境变量注入方式未改变；具体 Secret 管理系统不在本任务范围。

## 11. 变更影响

- 业务逻辑、HTTP 契约、错误码、配置键、健康检查语义、终止预算均未改变。
- Go 工具链由 1.23 升级至 1.27.3，`go.mod` 的 `go` 指令由 1.23.0 升至 1.26.0；CI 测试镜像同步为 `golang:1.27.3`。升级原因是旧工具链已停止安全维护且会向发布二进制引入不可修复的 stdlib 漏洞。
- 依赖升级仅涉及有安全修复的模块（jwt / x-crypto / x-net / x-sys / x-text / compress / otel），全部通过单元、集成、Race、进程级生命周期与 Kafka E2E 门禁。
