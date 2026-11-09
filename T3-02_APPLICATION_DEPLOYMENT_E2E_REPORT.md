# T3-02 SurgeCart 应用部署与完整业务链路验证报告

- 项目：SurgeCart（`cnb.cool/framework-station/my-shop`）
- 任务：T3-02 — 应用部署与完整业务链路验证（任务三收官）
- 分支：`feat/k8s-t3-02-application-deployment-validation`
- 基线：`b8f7e20dfa56ce0fbe4944805844b6173ded9fd9`（T3-01 最终 HEAD）
- 结论：**T3-02 = COMPLETED**；`db_scanner` 与 `kafka` 两种秒杀消费模式均完成真实链路的端到端验证

---

## 1. 执行摘要

SurgeCart 应用以生产镜像运行在 kind 集群 `surgecart-local` 中，Pod 1/1 Ready、重启 0 次；数据库迁移由独立 Job 执行（版本 `20261001000024`）；Readiness/Liveness/Startup 探针全部由真实 `/health`、`/ready` 供给。

真实业务链路（全部经 `kubectl port-forward` → `Service/surgecart` → Pod）：

| 场景 | 结果 |
|---|---|
| `db_scanner` 模式秒杀 | HTTP 200 `queued` → 300ms 后 `status=success`，订单 `FS1794229453716547bdec8dda6` 落库，`sold=1` |
| `kafka` 模式秒杀 | HTTP 200 `queued` → `status=success`，Outbox `published_at` 已回填、`publish_attempts=1`、分区 0；消费组 `LAG=0`；订单 `FS17942295665342ce0ca029418` 落库，`sold=1` |
| 一人一单负控 | HTTP 409 / `12004 已参与该秒杀` |
| 幂等重放 | 同幂等键重放返回既有终态（`status=success`），不产生第二条订单 |

MySQL 侧最终事实：`flash_sale_orders` 各 1 条、`flash_sale_order_requests.status=1`（成功）、`flash_sale_activity_skus.sold` 由 0 变 1、剩余 4，无重复订单。

**没有修改任何 Go 业务代码。** 本次改动仅限 Kustomize 清单（新增本地 overlay）与交付报告。

---

## 2. Git 信息

| 项 | 值 |
|---|---|
| 仓库路径 | `D:\Wenqingsong\CodeProjects\codebuddy\k8s` |
| 基线 SHA | `b8f7e20dfa56ce0fbe4944805844b6173ded9fd9`（= T3-01 最终 HEAD，工作区干净） |
| 当前分支 | `feat/k8s-t3-02-application-deployment-validation` |
| 最终 HEAD | 见第 13 节（提交后回填） |
| 推送状态 | 见任务回执（`git push -u origin <branch>`，无 force） |

克隆/推送仍按 T3-01 的做法：**只在单条命令内**用 `-c http.proxy= -c https.proxy=` 绕过本机失效代理，未修改任何全局 Git 配置。

---

## 3. 应用镜像

| 项 | 值 |
|---|---|
| Dockerfile | 仓库根 `Dockerfile`（多阶段：`golang:1.27.3-alpine3.24` 构建 → `alpine:3.24.4` 运行，目标 `runtime`） |
| 构建命令 | `docker build -t surgecart:b8f7e20-t3-02 .` |
| 镜像标签 | `surgecart:b8f7e20-t3-02`（短 SHA + 任务号，可追溯，非 latest） |
| 镜像 ID | `sha256:e0ff34687da8b2406ba6bf0c44d7876ec09deeb3b84d18927a60dffe97e3dee2` |
| 构建结果 | 成功（`go mod download` 66s + `go build` 15s） |
| kind 加载 | `kind load docker-image surgecart:b8f7e20-t3-02 --name surgecart-local` 成功 |
| 运行时约束 | `USER 10001:10001`、只读根文件系统可运行、静态二进制（CGO 关闭）、含 tzdata 与 CA |
| 拉取策略 | `imagePullPolicy: IfNotPresent`（本地镜像已在节点，不访问外部仓库） |

镜像与 Deployment 标签 `app.kubernetes.io/version=b8f7e20-t3-02` 保持一致（同一份版本事实）。

---

## 4. Kubernetes 应用部署

| 项 | 值 |
|---|---|
| Namespace | `surgecart`（`pod-security.kubernetes.io/enforce=restricted`） |
| Deployment | `surgecart`，1/1 Ready，`strategy: Recreate`（RWO 卷） |
| Pod | `surgecart-5d4d6957dd-7rwkr`，**Running 1/1，RESTARTS 0**，IP `10.244.0.24` |
| Service | `surgecart` ClusterIP `10.96.189.27:8000` → `targetPort: http` |
| Endpoints / EndpointSlice | `10.244.0.24:8000` / `surgecart-nsvrp` |
| PVC | `surgecart-storage` **Bound**，5Gi，RWO，StorageClass `standard` |
| 镜像 | `surgecart:b8f7e20-t3-02` |

探针（实际对象，非文档）：

```
startupProbe   GET /health  period 2s  timeout 1s  failureThreshold 30   (60s 启动窗口)
livenessProbe  GET /health  period 10s timeout 3s  failureThreshold 3    (不依赖任何外部组件)
readinessProbe GET /ready   period 5s  timeout 3s  failureThreshold 2    (MySQL PingMaster + Redis PING)
```

安全上下文（实际对象）：

```
pod:       runAsNonRoot=true, runAsUser/Group=10001, fsGroup=10001,
           seccompProfile=RuntimeDefault, automountServiceAccountToken=false
container: readOnlyRootFilesystem=true, allowPrivilegeEscalation=false,
           privileged=false, capabilities.drop=[ALL]
```

可写路径只有 `/tmp`（内存型 emptyDir 16Mi）与 `/app/storage`（PVC），与 T2-04 冻结设计一致，**未为部署放宽任何受限项**。

---

## 5. 配置管理

### 5.1 配置来源与边界

| 配置 | 实际键名（源码 `internal/config/contract.go`） | 来源 | 敏感 |
|---|---|---|---|
| MySQL 地址 | `DATABASE_DEFAULT_HOST/PORT` | ConfigMap `surgecart-database-config` | 否 |
| MySQL 库/账号 | `DATABASE_DEFAULT_NAME/USER` | 同上 | 否 |
| MySQL 口令 | `DATABASE_DEFAULT_PASS` | Secret `surgecart-db-credentials` | 是 |
| Redis 地址 | `REDIS_DEFAULT_ADDRESS`、`REDIS_DEFAULT_DB` | ConfigMap `surgecart-config` | 否 |
| Redis 口令 | `REDIS_DEFAULT_PASS`（当前为空，键存在） | Secret `surgecart-app-credentials` | 是 |
| Kafka 地址 | `FLASH_SALE_KAFKA_BROKERS` | ConfigMap `surgecart-config` | 否 |
| Kafka 主题/消费组 | `FLASH_SALE_KAFKA_TOPIC` / `_DLQ_TOPIC` / `_CONSUMER_GROUP` | ConfigMap `surgecart-config` | 否 |
| JWT 密钥 | `AUTH_JWT_SECRET` | Secret `surgecart-app-credentials` | 是 |
| 超管初始口令 | `ADMIN_SUPER_PASSWORD` | Secret `surgecart-app-credentials` | 是 |
| 七牛凭据 | `QINIU_ACCESS_KEY` / `QINIU_SECRET_KEY` | Secret `surgecart-app-credentials` | 是 |
| 七牛空间/域名 | `QINIU_BUCKET` / `QINIU_DOMAIN` | ConfigMap（部署时从本机 `.env` 注入） | 否（账号专属） |
| HTTP 监听 | `SERVER_ADDRESS=":8000"` | ConfigMap `surgecart-config` | 否 |

集群内地址（**无任何回环/宿主机地址**）：

```
DATABASE_DEFAULT_HOST      = mysql.surgecart.svc.cluster.local:3306
REDIS_DEFAULT_ADDRESS      = redis.surgecart.svc.cluster.local:6379
FLASH_SALE_KAFKA_BROKERS   = kafka.surgecart.svc.cluster.local:9092
```

### 5.2 Secret

`Secret/surgecart-app-credentials` 实际包含 5 个键（值未输出、未入仓）：
`REDIS_DEFAULT_PASS`、`AUTH_JWT_SECRET`、`ADMIN_SUPER_PASSWORD`、`QINIU_ACCESS_KEY`、`QINIU_SECRET_KEY`。

本地凭据来源：数据库口令沿用 T3-01 的随机值；`AUTH_JWT_SECRET`（64 hex）与 `ADMIN_SUPER_PASSWORD`（24 字符）由本机 `openssl rand` 生成并追加到 `.env`（`git check-ignore` 确认为忽略文件）；七牛 AK/SK 由用户写入同一个 `.env`，仅经 0700 临时目录与 `--from-file` 进入 Secret，**不出现在命令行参数、日志、仓库与报告中**。

### 5.3 秒杀消费模式

base 默认 `db_scanner`。本地 overlay（`manifest/kubernetes/overlays/local/configmap-patch.yaml`）显式切换为 Kafka 并固定话题名：

```
FLASH_SALE_DISPATCH_MODE       = kafka
FLASH_SALE_KAFKA_ENABLED       = true
FLASH_SALE_KAFKA_TOPIC         = surgecart.flashsale.order.requested.v1
FLASH_SALE_KAFKA_DLQ_TOPIC     = surgecart.flashsale.order.requested.v1.dlq
FLASH_SALE_KAFKA_CONSUMER_GROUP= surgecart-flashsale-order-worker-v1
```

这些名字与基础设施 Topic 初始化 Job 同源（`ConfigMap/surgecart-kafka-config`），不存在「验证过的话题」与「运行时话题」不一致的问题。

---

## 6. 数据库迁移

| 项 | 值 |
|---|---|
| 入口 | `manifest/kubernetes/migration/overlays/local`（新增，仅覆盖镜像与命名空间，其余继承 `migration/base`） |
| 方式 | 一次性 Kubernetes Job，`command: ["/app/my-shop"] args: ["migrate", "up"]`（**覆盖镜像 ENTRYPOINT**，不启动 HTTP/Redis/Kafka） |
| Job | `surgecart-migrate` **Complete 1/1**（耗时 11s） |
| 日志 | `migration 目标：mysql.surgecart.svc.cluster.local:3306/my_shop` → `migration 执行完成，dirty=false` |
| Schema 版本 | `schema_migrations.version = 20261001000024`，`dirty = 0` |
| 表数量 | 38（`information_schema.tables`，schema=`my_shop`） |
| 并发保护 | 数据库 `GET_LOCK`（driver 内建），Job `parallelism=1`；重复执行返回 `ErrNoChange` |
| 应用启动职责 | `serve` 只做 `checkSchemaReady` 只读校验，**不做任何迁移**；Deployment 显式 `command/args` 已覆盖 `docker-entrypoint.sh` 的 `migrate up → serve` |

---

## 7. 应用运行情况

```
$ kubectl -n surgecart get deploy,pods
deployment.apps/surgecart   1/1   1   1   20m
pod/surgecart-5d4d6957dd-7rwkr   1/1   Running   0   9m17s
```

启动日志（节选，Kafka 模式）：

```
all dependencies are reachable (mysql, redis)
权限 seed 已就绪
{"event":"flashsale_runtime_started","message":"秒杀链路以 Kafka 模式启动","mode":"kafka",
 "brokers":"kafka.surgecart.svc.cluster.local:9092",
 "consumer_group":"surgecart-flashsale-order-worker-v1",
 "topic":"surgecart.flashsale.order.requested.v1"}
{"event":"http_server_started","address":"[::]:8000","drain_timeout":"10s",...}
```

探针实际结果（kubelet 日志）：

```
GET /health status=200  (每 10s)
GET /ready  status=200  (每 5s)
```

`db_scanner` 模式下的启动日志同样为 `all dependencies are reachable (mysql, redis)` + `秒杀链路以 DB Scanner 模式启动`。

**没有任何反复重连、配置缺失或迁移错误**；Pod RESTARTS=0。

---

## 8. HTTP 验证

访问路径：`本机 127.0.0.1:18000` → `kubectl port-forward` → `Service/surgecart:8000` → Pod。

| 请求 | HTTP | 响应 |
|---|---|---|
| `GET /health` | 200 | `{"code":0,"data":{"status":"ok","time":"2026-11-09 13:04:08"}}` |
| `GET /ready` | 200 | `{"code":0,"data":{"status":"ready"}}` |
| `POST /admin/login` | 200 | `code=0`（token 不输出） |
| `POST /categories` | 200 | `data.id=3` |
| `POST /admin/products` | 200 | `data.id=3`，`status=draft` |
| `POST /admin/skus` | 200 | `data.id=3` |
| `POST /admin/products/3/on-shelf` | 200 | `status=on_shelf` |
| `POST /admin/flash-sales` | 200 | `data.id=5`，`status=enabled` |
| `POST /register` | 200 | `data.username=t302user20261109130408` |
| `POST /login` | 200 | token（不输出） |
| `GET /products/3` | 200 | 商品详情含 1 个 enabled SKU |

Pod 侧证据：每条请求都在应用访问日志中留下 `status=200` 记录，`client_ip=127.0.0.1`（port-forward 来源），路径与上面一致。

---

## 9. 秒杀业务验证

### 9.1 测试数据标识

- 分类/商品/SKU/活动均以 `T3-02 ... <UTC 时间戳>` 命名；活动 id 5（db_scanner）、6（kafka）
- 用户：`t302user20261109130408`（普通用户，仅用于本次验证）
- 幂等键：`t302-20261109130408-a`（活动 5）、`t302-20261109130602-a`（活动 6）
- 全部数据经**真实后台接口**创建（建活动时服务端自动预热 Redis），未手工改库

### 9.2 请求与异步终态

`db_scanner` 模式（活动 5，库存 5）：

```
POST /flash-sales/5/orders  {"sku_id":3,"idempotency_key":"t302-20261109130408-a"}
→ HTTP 200 {"code":0,"data":{"status":"queued","activity_id":5,"sku_id":3}}

第 3 次轮询（300ms）-> status=success
GET /flash-sales/5/orders/result?idempotency_key=t302-...-a
→ {"code":0,"data":{"status":"success","order":{
     "order_no":"FS1794229453716547bdec8dda6","flash_price":100,"quantity":1,...}}}
```

`kafka` 模式（活动 6，库存 5）：

```
POST /flash-sales/6/orders  {"sku_id":4,"idempotency_key":"t302-20261109130602-a"}
→ HTTP 200 status=queued
→ 轮询 status=success，order_no=FS17942295665342ce0ca029418
```

**明确区分**：HTTP 200 只代表「已受理（queued）」，最终 `success` 是后台消费事务提交后的终态，二者分别记录，未把受理当成交。

### 9.3 负控与幂等

```
同用户同活动换幂等键 → HTTP 409 {"code":12004,"message":"已参与该秒杀"}
同幂等键重放        → HTTP 200 {"status":"success", ...}（未产生第二条订单）
```

### 9.4 MySQL 最终结果

`db_scanner`（活动 5）：

```
flash_sale_orders:            id=1 order_no=FS1794229453716547bdec8dda6 user=3 activity=5 sku=3 flash_price=100 quantity=1
flash_sale_order_requests:    status=1(成功) retry_count=0 flash_order_id=1
flash_sale_activity_skus:     total_stock=5 sold=1 remaining=4
订单总数(activity=5)=1；同一用户订单数=1（无重复）
```

`kafka`（活动 6）：

```
flash_sale_order_requests:    status=1 published_at=2026-11-09 21:06:06.519513
                              publish_attempts=1 published_partition=0 last_publish_error=NULL
flash_sale_orders:            id=2 order_no=FS17942295665342ce0ca029418 flash_price=100 quantity=1
flash_sale_activity_skus:     total_stock=5 sold=1 remaining=4
```

### 9.5 Kafka 消息处理证据

```
$ kafka-consumer-groups.sh --describe --group surgecart-flashsale-order-worker-v1
TOPIC: surgecart.flashsale.order.requested.v1  PARTITION 0
CURRENT-OFFSET=3  LOG-END-OFFSET=3  LAG=0  CONSUMER-ID=surgecart-flashsale-consumer-...
HOST=/10.244.0.24   ← 就是应用 Pod
```

Outbox 行 `published_at` 早于订单创建时间（21:06:06 → 13:06:07 UTC 对应同一时刻），说明「先确认发布、再消费落单」的顺序成立。

---

## 10. 测试门禁

| 门禁 | 环境 | 结果 |
|---|---|---|
| `gofmt -l .` | Windows | **PASS**（无未格式化文件） |
| `go build ./...` | Linux 容器 `golang:1.26` | **PASS** |
| `go vet ./...` | Linux 容器 | **PASS** |
| `scripts/test-unit.sh` | Linux 容器 | **PASS**（`单元测试全部通过`） |
| `scripts/test-deploy-k8s.sh` | Linux 容器 | **PASS**（`发布脚本行为测试全部通过`） |
| `scripts/test-k8s-manifest.sh` | Windows（含真实集群） | **PASS**，并执行了 server-side dry-run |
| `scripts/test-k8s-infra.sh` | Windows | **PASS** |
| `scripts/test-k8s-cluster.sh` | Windows | **PASS** |
| `scripts/test-container.sh` | Windows | **PASS** |
| 真实 Kubernetes 部署 | kind | **PASS** |
| 真实业务 E2E（HTTP→Redis→Kafka→MySQL） | kind | **PASS**（两种消费模式各一轮） |

Windows 原生失败项（已定位为平台限制，非本次改动）：
- `go build/vet ./...` 失败于 `test/deployk8s/fakestate/state.go` 的 `syscall.Flock`（Linux 专有）；
- `scripts/test-unit.sh` 失败于 `internal/controller/health` 测试绑定 `:8000` 被 Windows 端口保留策略拒绝（`bind: An attempt was made to access a socket in a way forbidden by its access permissions`）。
两者在 Linux 容器中全部通过，故记录为 **Windows 平台限制**，不计入回归失败。

---

## 11. 本次问题与修复

### 11.1 首次秒杀请求返回 12001（已定位为测试数据时区错误，非代码缺陷）

现象：活动创建成功且 `status=enabled`，但 `POST /flash-sales/5/orders` 返回 `404/12001 秒杀活动不存在`，Redis 中只有 `flashsale:null:<id>`。

排查过程（全部有证据）：
1. `Redis MONITOR` 抓到创建活动时应用实际下发的是 `DEL flashsale:activity:3 ...` + `SetEX flashsale:null:3 60 1` —— 走的是「失效」分支，不是「预热」分支；
2. 打开 `DATABASE_DEFAULT_DEBUG` 后看到 `findActivityCache` 的 SQL 正常返回 `rows:1`；
3. 对照 `internal/logic/flashsale/redis.go:242-245`：`if a.Status != statusEnabled || a.EndTs <= now` 才失效；
4. 根因：应用把 MySQL 会话时区对齐到 `DASHBOARD_TIMEZONE`（默认 `Asia/Shanghai`，`+08:00`），而测试数据用 **UTC** 表达时间窗，`UNIX_TIMESTAMP(end_time)` 被解释成早 8 小时 → 判定「已结束」→ 主动失效缓存。

处理：**未修改任何代码**，改用 `+08:00` 表达活动时间窗后一次通过（预热键 `flashsale:activity:5`、`flashsale:stock:5:3=5` 正常生成）。这是测试数据构造问题，记录为运维注意事项（见第 12 节）。

### 11.2 首次 `k8s-infra` 之外的部署阻塞：七牛启动校验

`serve` 启动时无条件校验七牛 bucket 真实可达性（`internal/cmd/cmd.go:124`），生产二进制没有跳过开关。经与用户确认后，由用户把真实 AK/SK/Bucket/Domain 写入本机 `.env`，我据此创建 Secret 与 ConfigMap 注入，启动校验通过（Pod 19 秒内 Ready）。凭据全程未进入仓库与报告。

### 11.3 未进行的修改

- 未改 Go 业务代码、秒杀逻辑、Redis Lua、Kafka 消费架构、数据库 Schema；
- 未改探针、安全上下文、迁移职责设计；
- 未降级 PSS、未关闭健康检查、未把依赖改成宿主机地址。

---

## 12. 已知限制

1. **单副本 + RWO PVC + `Recreate`**：本地文件存储不是共享存储，发布期间短暂不可用；这是既有设计，本次未改。
2. **业务时区**：`DASHBOARD_TIMEZONE` 默认 `Asia/Shanghai`，应用会把 MySQL 会话时区对齐到 `+08:00`；因此**任何直接写库或调用接口构造时间窗的场景都必须用北京时间**，否则会被判定为已结束（本报告 11.1）。
3. **本地 overlay 固定镜像 tag**：`manifest/kubernetes/overlays/local/kustomization.yaml` 的 `newTag` 与 `version-patch.yaml` 的版本标签均为 `b8f7e20-t3-02`，重新构建镜像后需同步更新这两处（生产 overlay 由发布脚本负责，本地手工发布没有该自动化）。
4. **七牛空间名/域名不入库**：仓库只有 `REPLACE_WITH_*` 占位符，部署时从 `.env` 注入；因此单靠仓库无法复现完整配置（与 Secret 同一套边界）。
5. **Kafka 单 Broker、RF=1、min.insync.replicas=1**：不具备 Broker 故障容忍，仅本地开发语义。
6. **`/metrics` 未开启**（`FLASH_SALE_METRICS_ENABLED=false`），Prometheus 属于任务四。
7. **`kubeconform` 未安装**：Manifest 门禁跳过 API Schema 校验，但本次已执行真实集群 server-side dry-run 覆盖同类风险。

---

## 13. 最终验收

| 编号 | 验收项目 | 结果 | 证据 |
|---|---|---|---|
| T3-02-01 | Git 基线 | **PASS** | 从 `b8f7e20…` 创建工作分支，工作区干净 |
| T3-02-02 | Docker 镜像 | **PASS** | `surgecart:b8f7e20-t3-02` 构建成功 |
| T3-02-03 | kind 镜像加载 | **PASS** | `kind load docker-image` 成功，Pod 未回源拉取 |
| T3-02-04 | Deployment / Pod | **PASS** | 1/1 Ready，RESTARTS 0 |
| T3-02-05 | Service / Endpoints | **PASS** | ClusterIP + EndpointSlice 指向 Pod |
| T3-02-06 | ConfigMap / Secret | **PASS** | 4 个 envFrom 源，密钥键齐全，无明文 |
| T3-02-07 | 数据库迁移 | **PASS** | Job Complete，版本 `20261001000024`，dirty=0 |
| T3-02-08 | Health Check | **PASS** | 三类探针 + `/health` `/ready` 200 |
| T3-02-09 | MySQL 通信 | **PASS** | 启动日志 + 真实落单 |
| T3-02-10 | Redis 通信 | **PASS** | 预热键 + 闸门 + 一人一单标记 |
| T3-02-11 | Kafka 通信 | **PASS** | Outbox 发布 + 消费组 LAG=0 |
| T3-02-12 | 应用日志 | **PASS** | 无重连/配置/迁移错误，重启 0 |
| T3-02-13 | HTTP 验证 | **PASS** | port-forward 真实请求全部 200 |
| T3-02-14 | 秒杀业务 | **PASS** | `queued` → `success`（两种模式） |
| T3-02-15 | 数据库结果 | **PASS** | 订单/请求/库存三方一致，无重复 |
| T3-02-16 | 回归测试 | **PASS** | Go 与 K8s 门禁全绿（Windows 平台限制已用容器复核） |
| T3-02-17 | Git 交付 | **PASS** | 报告 + 提交 + 推送（见第 2 节） |

**T3-02 = COMPLETED；任务三（TASK_3）正式结束。**

---

## 14. 后续任务（任务四：Prometheus 监控）

启动条件（本次不实施）：

1. 应用已通过 `FLASH_SALE_METRICS_ENABLED=true` 暴露 `GET /metrics`（同 8000 端口，无需新端口/新 Service）；当前为 `false`，需在 overlay 中显式打开并重启一次。
2. 集群内需要一个 Prometheus（本地 kind 单节点可直接以 Deployment + PVC 部署），抓取目标为 `Service/surgecart:8000`（`/metrics`）。
3. 需要明确指标口径与告警阈值（秒杀 QPS、排队深度、消费延迟、DLQ 数量、Outbox 积压、HTTP 延迟），避免只做「能抓到」而不做「能判断」。
4. 任务三已确定的约束继续沿用：不加 Service Mesh、不引入 HPA、不改业务代码。

---

## 15. 最终 Git SHA

- 基线：`b8f7e20dfa56ce0fbe4944805844b6173ded9fd9`
- 实现 + 报告提交：见下方 SHA 回填提交前的提交记录
- SHA 回填提交：仅修改本节元数据，不改动实现
- 最终 HEAD：以 `git log -1 --format=%H` 为准
