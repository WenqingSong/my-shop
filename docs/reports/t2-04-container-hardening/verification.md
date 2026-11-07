# T2-04 Docker 镜像与容器安全加固 — 交付验证报告

- 日期：2026-11-07
- 基线 Commit：`02c62ac874b3b9d41e76e137fdc5b8d34342fbea`（`feat/k8s-t2-02-health-observability` tip）
- 任务分支：`feat/k8s-t2-04-container-hardening`
- 实现提交：`8b621bff6c74ef1a9960dea5bf365be861e292a8`
- 文档与报告提交：本文件所在提交（分支 tip 见交付回复）
- 最终状态：**T2-04_PASS**（NOT_VERIFIED 项见 §8）

## 1. 基线与规范审计

| 项目 | 结论 |
| --- | --- |
| 当前分支 / HEAD / 工作区 | `feat/k8s-t2-02-health-observability`，HEAD `02c62ac`，工作区干净 |
| T2-03 / T2-03.1 | `7064611` / `273d013`，均在基线祖先链上 |
| T2-01 | `fd9b4d8`，在基线祖先链上 |
| T2-02 | `02c62ac`，基线本身 |
| T2-04 远端分支 | 不存在（`git ls-remote --heads origin` 无匹配） |
| T2-04 正式任务定义 | **未在仓库中找到**（`docs/tasks/`、`.agent/tasks/` 均无 T2-04 记录） |
| Kubernetes 迁移总体规划 | `docs/design/kubernetes-adaptation.md` 仅存在于未合并分支 `feat/surgecart-kubernetes-base`（`886b945`），通过 `git show` 阅读，未合并该分支 |

**冲突报告**：本任务只有 Prompt 形式的任务定义，仓库中没有 T2-04 的正式任务文档或验收标准文件。本报告以 Prompt 的验收项为准，并以 T2-01/02/03 已验收的配置契约、健康语义与终止预算为不可破坏的约束。未修改 T2-01/02/03 的历史提交，未合并 `develop` / `main`。

基线选择：`02c62ac` 是 T2-01/02/03/03.1 全部验收提交的 tip；T2-04 无远端分支或历史实现需要审计，因此从该提交创建独立分支，未覆盖任何已有历史。

## 2. 改造前审计结论

| 项目 | 改造前事实 | 风险 |
| --- | --- | --- |
| 基础镜像 | `golang:1.23-alpine`、`alpine:3.20`（浮动 minor/patch） | 不可复现；Go 1.23 与 Alpine 3.20 已停止安全维护 |
| 构建缓存 | 仅 `go mod download` 分层，无 BuildKit cache mount | 重复构建重复下载 |
| 可复现参数 | 无 `GOTOOLCHAIN` / `-mod=readonly` / `-buildvcs=false` | 构建期可能改写依赖或受构建目录影响 |
| 运行用户 | `USER 10001:10001`，但 `chown -R app:app /app` | 运行用户可改写二进制与配置 |
| 根文件系统 | 可写 | 运行期可写入镜像层 |
| capabilities | 未 drop | 保留默认 capability 集合 |
| 提权 | 无 `no-new-privileges` | setuid 提权面 |
| HEALTHCHECK | 仅 Compose 有；Dockerfile 无 | `docker run` 场景无健康语义 |
| 容器生命周期验证 | 仅进程级 harness，无真实 Docker 容器 SIGTERM/drain 验证 | 无法证明容器 PID1 与 Docker stop 行为 |
| 扫描 | 未安装 Trivy / govulncheck，无基线结果 | 漏洞不可见 |

## 3. 实现摘要

### 3.1 Dockerfile

- 多阶段：`builder` → `runtime-base` → `runtime`（默认目标）；`lifecycle-verify` 为测试专用目标。
- 基础镜像固定：`golang:1.27.3-alpine3.24`、`alpine:3.24.4`；`apk upgrade --no-cache` 吸收同 minor 安全修订。
- 可复现：`GOTOOLCHAIN=local`、`GOFLAGS=-mod=readonly`、`-trimpath`、`-buildvcs=false`、BuildKit cache mount、`.dockerignore` 收敛上下文。
- 运行层最小化：仅二进制、配置、entrypoint、tzdata、CA；无源码/测试/Go 工具链/.env。
- 权限：二进制 `root:root 0555`、配置 `0444`、entrypoint `0555`；仅 `/app/storage` 归属 `app:app`。
- `HEALTHCHECK`：`wget /health`，10s/5s/60s/5 次，与 Compose 同值。
- 入口保持 `exec /app/my-shop serve`，Go 进程为 PID 1。

### 3.2 Compose（仅 app 服务）

`user: "10001:10001"`、`read_only: true`、`/tmp` tmpfs（`noexec,nosuid,nodev,64m`）、`cap_drop: [ALL]`、`security_opt: ["no-new-privileges:true"]`；保留 `stop_grace_period: 60s` 与 `/health` healthcheck。MySQL / Redis / Kafka / Nginx / Prometheus 定义未改变。

### 3.3 验证工具

- `scripts/test-container.sh`：扩展静态断言（版本固定、缓存参数、镜像权限、HEALTHCHECK 一致性、Compose 加固、负向特权断言）。
- `scripts/test-container-runtime.sh`：真实构建 + 启动 + HTTP + 在途请求 + `docker stop` + 日志事件顺序 + 退出码 + 残留进程。
- `test/container/`：测试专用七牛 Mock、验证镜像 Dockerfile 与 Compose 覆盖文件；独立 project/container/volume，不触碰开发环境。

## 4. 镜像事实

```text
Image:      my-shop-app:t204
Image ID:   sha256:9a0e7acc98a526b5b0f731be2bf7b2b3c5292360776ed3887266d55aef2b3504
Size:       63,559,680 bytes (≈60.6 MiB)
User:       10001:10001
Workdir:    /app
Entrypoint: ["/app/docker-entrypoint.sh"]
Healthcheck: wget -q -O /dev/null http://127.0.0.1:8000/health || exit 1
             interval=10s timeout=5s start-period=60s retries=5
Go:         go1.27.3（`go version -m`）
Builder:    golang:1.27.3-alpine3.24 @ sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18
Runtime:    alpine:3.24.4 @ sha256:93b41ef40b1677fce8fcc64ff4af09daddfacf5040afde618bc46c1e2d1cc7d4
```

镜像文件系统审计：包含 `/app/my-shop`、`/app/manifest/config/config.yaml`、`/app/docker-entrypoint.sh`、`/usr/share/zoneinfo/Asia/Shanghai`、`/etc/ssl/certs/ca-certificates.crt`；不含 `*.go`、`/src`、`/usr/local/go`、`.env`、`test/`。私钥/开发默认 JWT 密钥模式扫描无命中。

## 5. 验收项结果

| 验收项 | 结果 | 证据 |
| --- | --- | --- |
| 镜像构建 | PASS | `docker build --target runtime` 退出码 0；镜像元数据与文件系统审计通过 |
| 非 root 用户 | PASS | `Config.User=10001:10001`；`/proc/1/status` Uid=10001；`id -u`=10001 |
| 只读根文件系统 | PASS | `HostConfig.ReadonlyRootfs=true`；写 `/app` 失败；`/app/storage`、`/tmp` 可写 |
| 必要目录读写 | PASS | `/app/storage`（卷）与 `/tmp`（tmpfs）`touch` 成功并清理 |
| 容器启动 | PASS | app 容器 `healthy`；`/health` 200、`/ready` 200 |
| HEALTHCHECK | PASS | 容器 `State.Health.Status=healthy`；镜像与 Compose 参数一致 |
| HTTP 正常访问 | PASS | 经发布端口请求 `/health`、`/ready` 均 200 |
| SIGTERM 传递 | PASS | 应用日志 `http_server_drain_begin` 且 `signal=terminated` |
| 优雅关闭 | PASS | 空闲路径 `timed_out=false`；在途路径 `drain_timeout → force_closed → stopped(timed_out=true)`，之后 Runtime 关闭 |
| stop_grace_period | PASS | 容器 `Config.StopTimeout=60`；空闲 171ms、在途 9129ms 停止，退出码 0 |
| 镜像安全扫描 | PASS（1 UNKNOWN） | Trivy：0 Critical/High/Medium/Low，1 UNKNOWN；govulncheck 0 可达 |
| 业务回归 | PASS | 单元 / 集成 / Race / 进程级生命周期 / Kafka E2E 全部退出码 0 |

### 5.1 关闭日志（最终运行，在途请求）

```text
05:25:45.697 lifecycle_block_handler_entered
05:25:45.809 http_server_drain_begin            signal=terminated
05:25:54.810 http_server_drain_timeout          elapsed_ms=9000 active_connections=1
05:25:54.810 http_server_force_closed           forced_connections=1
05:25:54.810 http_server_stopped                timed_out=true
05:25:54.810 flashsale_runtime_shutdown_begin
05:25:54.810 flashsale_scanners_stopped
05:25:54.810 flashsale_runtime_shutdown_complete within_budget=true
```

`docker stop` 耗时 9129ms，退出码 0（非 137），容器 PID=0，宿主机 PID 不再存活，`OOMKilled=false`；未发生 Docker 提前 SIGKILL。

## 6. 门禁执行结果（真实退出码）

| 门禁 | 命令 | 退出码 | 结果 |
| --- | --- | ---: | --- |
| 格式 | `gofmt -l .` | 0 | 无未格式化文件 |
| 构建 | `go build ./...` | 0 | PASS |
| 静态检查 | `go vet ./...` | 0 | PASS |
| 单元 | `scripts/test-unit.sh` | 0 | PASS |
| 完整测试 | `scripts/test.sh` | 0 | PASS（含集成测试、Registry、Prometheus、容器静态、Nginx 静态） |
| Race | `scripts/test-race.sh` | 0 | PASS（Outbox 11 项 + Consumer 2 项） |
| 进程生命周期 | `scripts/test-serve-lifecycle.sh` | 0 | PASS（3 个真实进程用例） |
| Kafka E2E | `scripts/test-kafka-e2e.sh` | 0 | PASS（7 个用例） |
| 容器静态 | `scripts/test-container.sh` | 0 | PASS |
| 容器运行 | `scripts/test-container-runtime.sh` | 0 | PASS（15 项断言） |

说明：`scripts/test.sh` 中 Prometheus 的 promtool 与 Nginx 的 `nginx -t` 因环境未安装对应工具/镜像而跳过，脚本明确输出 WARN，静态断言仍执行；这不属于本任务新增门禁，也不冒充 PASS。

## 7. 漏洞扫描

工具：Trivy 0.76.2；govulncheck v1.9.0（DB 更新于 2026-11-05）。

### 7.1 镜像漏洞（修复前 → 修复后）

| 严重级 | 修复前 | 修复后 |
| --- | ---: | ---: |
| Critical | 0 | 0 |
| High | 25 | 0 |
| Medium | 19 | 0 |
| Low | 1 | 0 |
| Unknown | 2 | 1 |

修复前共 47 项，主要来自过期 `golang.org/x/crypto`、`x/net`、`x/text`、`x/sys`、`otel` 与 alpine `zlib 1.3.2-r1`；升级 Go 1.27.3、相关模块并 `apk upgrade` 后清零。

剩余 1 项：

- `GO-2026-5932`（UNKNOWN）：`golang.org/x/crypto/openpgp` 不再维护。
- 修复版本只有未发布的伪版本 `0.58.1-0.20261009181718-2f1f834330b2`。
- 影响：应用不导入 `openpgp`；`govulncheck ./...` 显示 0 个可达漏洞（0 symbols / 0 packages），仅模块级提示。
- 缓解：保持 `x/crypto` 最新正式版；官方发布正式修复后升级并重扫；发布前保留 govulncheck 门禁。

### 7.2 配置与敏感文件

- `trivy config Dockerfile`：0 failures。
- `trivy image --scanners secret` 在该镜像的 alpine/gobinary 目标上为 `Not scanned`，不作为通过依据；改用静态凭据断言 + 镜像文件系统私钥/密钥模式检查（无命中）。
- 镜像 `Config.Env` 不含 PASSWORD/SECRET/TOKEN/JWT/ACCESS_KEY 等键。
- 测试用占位凭据仅存在于 `test/container/compose.runtime-verify.yml`，不进入生产镜像或仓库默认值。

## 8. NOT_VERIFIED

1. **Kubernetes 探针 / preStop / 滚动更新**：无集群，未验证（T2-06 范围）。
2. **生产镜像完整 `serve` 启动**：需要真实七牛凭据与外网 `GetBucketInfo`。已验证生产镜像的构建、元数据、文件系统、非 root/只读下 `migrate version` 与配置可读；HTTP/SIGTERM/drain 语义在 `lifecycle-verify` 目标上验证（同一 runtime-base、同一 entrypoint、同一 USER，二进制仅多 `serve_lifecycle` 测试构建标签）。
3. **生产镜像 HEALTHCHECK 健康态**：`serve` 未在容器内完整启动，未观察到 `healthy`；同值 HEALTHCHECK 在验证镜像容器上为 `healthy`。
4. **多副本 migration/seed 并发、Kafka 重平衡、跨 Pod 本地存储**：单副本范围外，未验证。
5. **Secret 轮换与外部密钥管理**：未改变注入方式，具体系统不在本任务范围。

## 9. 剩余风险

- Go 工具链从 1.23 升级到 1.27.3、`go.mod` go 指令升至 1.26.0，属于较大跨度；已由全部门禁覆盖，但生产运行时行为仍以发布后观测为准。
- `apk upgrade --no-cache` 使同一基础标签在不同时间构建可能得到不同 patch 包；基础标签仍固定，digest 记录在容器安全文档中，发布镜像应固定并记录实际 digest。
- 容器生命周期验证使用共享网络命名空间的 loopback Mock（`network_mode: service:qiniu-mock`），这是测试拓扑，不是生产拓扑；生产网络仍为 Compose 默认网络 + Nginx 唯一入口。
- Trivy / govulncheck 结果依赖扫描时点的漏洞库；后续需在发布流程中重复执行。
- 本地存储 `/app/storage` 在只读根文件系统下可写依赖 named volume；若未来改为 bind mount，需要保证宿主机目录属主为 10001 或使用受控初始化。

## 10. 回滚

- 代码回滚：`git revert` 本任务的两个提交，或从 `02c62ac` 重新拉取分支。
- 镜像回滚：使用历史成功构建的短 SHA 标签（`docker.cnb.cool/framework-station/my-shop:<短SHA>`），不依赖 `latest`。
- 编排回滚：移除 `user` / `read_only` / `tmpfs` / `cap_drop` / `security_opt` 字段即可恢复改造前行为；`stop_grace_period` 与健康检查语义来自 T2-03/T2-02，不应回滚。
