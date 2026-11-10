# T4-03 Prometheus Kubernetes 部署与真实指标抓取验证 — 交付报告

## 1. 基本信息

| 项 | 值 |
| --- | --- |
| 项目 | SurgeCart（`https://cnb.cool/framework-station/my-shop`） |
| 任务 | T4-03（Prometheus Kubernetes Deployment and Real Metrics Scrape Validation） |
| 基线 | `64ebbbd4c1c53c087c0b30c4f2636f48da433c51`（T4-02 最终提交，独立开发分支，未合入 main） |
| main | `82243c0fbce614e03c4ff57dcc829b5f68fb08f2`（未修改、未合并） |
| 开发分支 | `feat/k8s-t4-03-prometheus-deployment`（从 `64ebbbd` 创建） |
| 实现提交 SHA | `53fba1475d5789cfac805b27974e63ece2d7615f` |
| 最终 HEAD SHA | `53fba1475d5789cfac805b27974e63ece2d7615f`（实现 + 报告）；其后仅追加一次「回填本报告元数据」的 docs 提交 |
| 推送状态 | 已推送：`origin/feat/k8s-t4-03-prometheus-deployment`（`git push -u` 成功，new branch） |
| 集群 | kind `surgecart-local`，节点 `surgecart-local-control-plane`（v1.37.0，Ready） |
| 监控命名空间 | `monitoring`（新建，PSS `enforce=restricted`） |

基线核验（开工时实测）：

```text
git branch --show-current  -> feat/k8s-t4-02-application-metrics
git rev-parse HEAD         -> 64ebbbd4c1c53c087c0b30c4f2636f48da433c51
git status --short --branch-> 工作区干净，与 origin/feat/k8s-t4-02-application-metrics 同步
git ls-remote origin       -> main=82243c0fbce614e03c4ff57dcc829b5f68fb08f2
                              feat/k8s-t4-02-application-metrics=64ebbbd4c1c53c087c0b30c4f2636f48da433c51
kubectl config current-context -> kind-surgecart-local
kubectl get ns             -> default/kube-*/local-path-storage/surgecart（此时无 monitoring）
kubectl get pods -n surgecart -> mysql/redis/kafka/surgecart 均 Running（1/1）
```

远端 T4-02 分支 SHA 与预期一致（无额外更新），因此按 `64ebbbd` 建立 T4-03 分支。

## 2. 实际部署架构

```text
SurgeCart Pod (surgecart/surgecart:64ebbbd-t4-03)
   │  GET /metrics  ← 抓取路径（集群内网络，15s 一次）
   ▼
Service/surgecart (ClusterIP 10.96.189.27:8000, Endpoints 10.244.0.25:8000)
   ▲
   │ surgecart.surgecart.svc.cluster.local:8000
   │
Prometheus Pod (monitoring/prom/prometheus:v2.53.0)
   │  TSDB
   ▼
PVC/prometheus-data (pvc-c2fd5c15-..., 5Gi, RWO, standard, Bound)
   │
   ▼  /api/v1/query（本机经 port-forward 只用于人工查看）
PromQL 真实查询结果
```

关键点：

- Prometheus 通过 **Kubernetes Service DNS** 抓取应用，不经过宿主机 port-forward、不使用 NodePort、
  不使用宿主机 IP、不存在 Fake Target；
- 应用侧无需任何改动即可被抓取（`/metrics` 在 ClusterIP Service 的同一 8000 端口上暴露）；
- Prometheus 自身无 RBAC（`automountServiceAccountToken: false`，静态配置不访问 API Server）。

## 3. Manifest 文件清单

新增：

| 文件 | 作用 |
| --- | --- |
| `manifest/kubernetes/monitoring/prometheus/base/kustomization.yaml` | 基础层组合（namespace + 5 个资源） |
| `manifest/kubernetes/monitoring/prometheus/base/namespace.yaml` | `Namespace/monitoring` + PSS restricted 标签 |
| `manifest/kubernetes/monitoring/prometheus/base/configmap.yaml` | `ConfigMap/prometheus-config`（`prometheus.yml`） |
| `manifest/kubernetes/monitoring/prometheus/base/pvc.yaml` | `PVC/prometheus-data`（5Gi RWO） |
| `manifest/kubernetes/monitoring/prometheus/base/deployment.yaml` | `Deployment/prometheus`（v2.53.0，单副本，Recreate） |
| `manifest/kubernetes/monitoring/prometheus/base/service.yaml` | `Service/prometheus`（ClusterIP:9090） |
| `manifest/kubernetes/monitoring/prometheus/overlays/local/kustomization.yaml` | 本地入口 |
| `manifest/kubernetes/monitoring/prometheus/overlays/local/deployment-patch.yaml` | 保留期 15d → 7d |
| `test/manifest/monitoring_test.go` | 5 个构建产物结构化断言（结构/安全/抓取/有状态性） |
| `docs/development/prometheus.md` | 监控栈运维文档（含 PromQL 与排障） |
| `docs/reports/t4-03-prometheus-deployment/T4-03_PROMETHEUS_DEPLOYMENT_REPORT.md` | 本报告 |

修改（应用侧只影响「部署差异」，不动业务代码）：

| 文件 | 变更 |
| --- | --- |
| `manifest/kubernetes/overlays/local/kustomization.yaml` | 镜像 tag → `64ebbbd-t4-03` |
| `manifest/kubernetes/overlays/local/version-patch.yaml` | 版本标签 → `64ebbbd-t4-03`（与镜像 tag 同值） |
| `manifest/kubernetes/overlays/local/configmap-patch.yaml` | 新增 `FLASH_SALE_METRICS_ENABLED: "true"` |
| `docs/development/kubernetes.md` | 目录结构补充监控入口，链接 `prometheus.md` |

未修改：Go 业务代码、数据库 Schema/迁移、`manifest/config/config.yaml`、prometheus/（compose 侧）、
`manifest/kubernetes/{base,overlays/production,migration,infrastructure,shared}`。

## 4. Prometheus 版本与镜像来源

| 项 | 值 |
| --- | --- |
| 镜像 | `prom/prometheus:v2.53.0`（与仓库既有 compose 配置同版本） |
| 来源 | Docker Hub，由 kind 节点 containerd 直接拉取（无代理、无私有仓库） |
| 构建信息（实测 `/api/v1/status/buildinfo`） | `version=2.53.0`，`revision=4c35b9250afefede41c5f5acd76191f90f625898`，`goVersion=go1.22.4` |
| 启动参数（`/api/v1/status/flags` 实测） | `config.file=/etc/prometheus/prometheus.yml`，`storage.tsdb.retention.time=1w`（=7d，overlay 覆盖生效） |

应用镜像：

| 项 | 值 |
| --- | --- |
| tag | `surgecart:64ebbbd-t4-03` |
| 构建 | `docker build -t surgecart:64ebbbd-t4-03 .`（本分支源码，应用代码即 T4-02 `64ebbbd`） |
| 导入 | `docker save` → `docker cp` 到节点 → `ctr -n k8s.io images import`（等价于 `kind load docker-image`；本机未安装 kind CLI） |
| 节点内引用 | `docker.io/library/surgecart:64ebbbd-t4-03` |
| 运行校验 | 启动日志路由表出现 `middleware.Metrics`；`/metrics` 返回 200 |

> 说明：T3-02 部署的旧镜像 `surgecart:b8f7e20-t3-02` 不包含 T4-02 指标实现，
> 因此本任务重建镜像并在集群内实测确认新镜像生效（见 §6、§8）。

## 5. 配置与开关

| 项 | 值 | 来源 |
| --- | --- | --- |
| `FLASH_SALE_METRICS_ENABLED` | `true` | `ConfigMap/surgecart-config`（local overlay 定义 + 集群内 patch 落地） |
| `FLASH_SALE_DISPATCH_MODE` | `kafka` | 同上（沿用 T3-02 的选择） |
| `FLASH_SALE_KAFKA_ENABLED` | `true` | 同上 |
| 抓取间隔 | 15s（timeout 10s） | `ConfigMap/prometheus-config` |

Secret 边界未变：`surgecart-db-credentials` / `surgecart-app-credentials` 仍由部署环境创建，
仓库与 monitor 清单均不含任何凭据；`QINIU_BUCKET` 等账号专属值在集群内保持真实值（未被 overlay 覆盖）。

## 6. Kubernetes 资源状态（实测）

```text
$ kubectl -n monitoring get deploy,pods,svc,pvc
deployment.apps/prometheus   1/1   1   1   85m   prometheus   prom/prometheus:v2.53.0
pod/prometheus-7766899868-gq2vm   1/1   Running   0   85m   10.244.0.27
service/prometheus   ClusterIP   10.96.142.39   <none>   9090/TCP
persistentvolumeclaim/prometheus-data   Bound   pvc-c2fd5c15-8d57-4575-a7a8-170e5688e571   5Gi   RWO   standard

$ kubectl -n surgecart get pods
surgecart-7d4d84b88-xn4tb   1/1   Running   0   11s   10.244.0.25
$ kubectl -n surgecart rollout status deployment/surgecart
deployment "surgecart" successfully rolled out   # 镜像 surgecart:64ebbbd-t4-03

$ kubectl -n surgecart get endpoints surgecart
surgecart   10.244.0.25:8000
```

启动日志（节选）证明新路由已挂载：

```text
2026-11-09T19:01:34.604Z [INFO] {"address":"[::]:8000","event":"http_server_started",...}
2026-11-09T19:01:34.611Z [INFO] {"consumer_group":"surgecart-flashsale-order-worker-v1","event":"flashsale_consumer_joined",...}
```

## 7. PVC 状态

```text
NAME              STATUS   VOLUME                                     CAPACITY   ACCESS MODES   STORAGECLASS
prometheus-data   Bound    pvc-c2fd5c15-8d57-4575-a7a8-170e5688e571   5Gi        RWO            standard
```

TSDB 实际写入 PVC（Pod 内 `ls -l /prometheus`）：

```text
drwxr-xr-x  chunks_head
-rw-r--r--  lock
-rw-r--r--  queries.active
drwxr-xr-x  wal
```

PromQL 侧证据：`prometheus_tsdb_head_series => 805`。

## 8. Target 状态（`GET /api/v1/targets`）

```text
scrapePool: surgecart
scrapeUrl : http://surgecart.surgecart.svc.cluster.local:8000/metrics
health    : up
lastError : ""
lastScrape: 2026-11-09T19:03:26.190630109Z
duration  : 0.00477614s
labels    : instance="surgecart.surgecart.svc.cluster.local:8000", job="surgecart"

scrapePool: prometheus
scrapeUrl : http://127.0.0.1:9090/metrics
health    : up
lastError : ""
lastScrape: 2026-11-09T19:03:28.007233209Z
```

Prometheus 自身健康：

```text
$ ... wget -q -O - http://127.0.0.1:9090/-/healthy  -> Prometheus Server is Healthy.
$ ... wget -q -O - http://127.0.0.1:9090/-/ready    -> Prometheus Server is Ready.
```

## 9. PromQL 查询结果（真实数据）

通过 `GET /api/v1/query`（本机经 `kubectl port-forward svc/prometheus 19090:9090`）实测：

| 查询 | 结果 |
| --- | --- |
| `up{job="surgecart"}` | `1` |
| `surgecart_http_requests_total` | `route=/health,status=200 → 201`；`route=/ready,status=200 → 401`；`route=/products/:id,status=404 → 2`；`route=/products,status=200 → 1` |
| `surgecart_http_requests_in_flight` | `0` |
| `go_goroutines` | `surgecart → 37`（`prometheus → 33`） |
| `go_memstats_heap_alloc_bytes` | `surgecart → 4185400` |
| `flashsale_outbox_backlog_pending` | `0` |
| `surgecart_outbox_oldest_age_seconds` | `0` |
| `surgecart_sampler_up` | `{sampler="kafka_lag"} → 1`；`{sampler="outbox"} → 1` |
| `surgecart_kafka_consumer_lag` | `{consumer_group="surgecart-flashsale-order-worker-v1",partition="0",topic="surgecart.flashsale.order.requested.v1"} → 0` |
| `surgecart_http_request_duration_seconds_count{route="/products/:id"}` | `2`（直方图已采集） |
| `surgecart_http_requests_total{route="/metrics"}` | **0 series**（抓取端不计入自身） |
| `surgecart_http_requests_total{route="unmatched"}` | **0 series**（未命中路由不进分组中间件，T4-02 已知边界） |

应用 `/metrics` 原始响应（Pod 内实测）：

```text
HTTP/1.1 200 OK
Content-Type: text/plain; version=0.0.4; charset=utf-8; escaping=values

# TYPE surgecart_http_requests_total counter
# TYPE surgecart_http_request_duration_seconds histogram
# TYPE surgecart_http_requests_in_flight gauge
# TYPE surgecart_kafka_consumer_lag gauge
# TYPE surgecart_outbox_oldest_age_seconds gauge
# TYPE surgecart_sampler_up gauge
# TYPE flashsale_outbox_backlog_pending gauge
# TYPE go_goroutines gauge
# TYPE go_memstats_heap_alloc_bytes gauge
# TYPE process_cpu_seconds_total counter
```

「没有样本」与「采样失败」的区分：

- Kafka Lag 与 Outbox 均为 **真实零值**（`sampler_up=1`，且存在标签序列）；
- `dispatch_mode=kafka` 下采样器运行中，不是「未启用」；
- 若将来 `sampler_up=0`，则 lag/age 为「上一次有效值」，不能解读为零积压。

## 10. 真实 HTTP 指标验证

从应用 Pod 内经 **Service DNS** 发起真实请求（不是 localhost）：

```text
GET /products/12345            -> HTTP/1.1 404 Not Found
GET /products/67890            -> HTTP/1.1 404 Not Found
GET /products                  -> HTTP/1.1 200 OK
GET /no-such-route-abc123      -> HTTP/1.1 404 Not Found
```

Prometheus 中对应的指标变化（等待 1 个抓取周期后查询）：

```text
surgecart_http_requests_total{method="GET",route="/products/:id",status="404"} = 2   ← 两个不同 ID 合并为同一序列
surgecart_http_requests_total{method="GET",route="/products",status="200"}     = 1
```

结论：

1. 请求总量随真实请求增长（计数器语义正确）；
2. `status` 标签反映真实响应码（404/200），不是进入中间件时的初始值；
3. `route` 使用路由模板 `/products/:id`，**两个不同商品 ID 没有产生两条序列**（无高基数泄漏）；
4. 未命中路径 `/no-such-route-abc123` **没有** 产生 `route="unmatched"` 或原始 URL 序列
   （与 T4-02 documented 行为一致，避免扫描路径污染标签）；
5. `/metrics` 抓取请求不被计入业务指标（序列数为 0）。

## 11. Kafka 与 Outbox 验证

| 项 | 结果 | 判读 |
| --- | --- | --- |
| `surgecart_kafka_consumer_lag` | 1 条序列，值 0 | 单分区 Topic 已消费到 LEO；`sampler_up=1` 说明是**真实零值**而不是采样失败 |
| `surgecart_sampler_up{sampler="kafka_lag"}` | 1 | Lag 采样链路健康（OffsetFetch + ListOffsets 成功） |
| `flashsale_outbox_backlog_pending` | 0 | 发布侧无积压（`status=queued AND published_at IS NULL`） |
| `surgecart_outbox_oldest_age_seconds` | 0 | 空 Outbox ⇒ 年龄真实为 0 |
| `surgecart_sampler_up{sampler="outbox"}` | 1 | 采样 SQL 成功执行（MySQL 可达） |

未做的事（明确记录）：**没有**人为制造 Kafka 积压或订单洪水来验证 lag > 0，
因为那会写入真实业务事件（秒杀请求/订单），超出本任务「不破坏现有业务数据」的约束。
lag > 0 的数值正确性由 T4-02 的真实 Kafka 集成测试覆盖（`TestLagReaderRealKafka`：
未提交=10 → 提交 4 后 6 → 追加 5 条 11 → 追平 0）。

## 12. 测试门禁（逐条命令与结果）

| # | 命令 | 环境 | 退出码 | 结果 |
| --- | --- | --- | --- | --- |
| 1 | `gofmt -l .` | Windows | 0 | PASS（无输出） |
| 2 | `go build ./...` | Linux 容器 `golang:1.26` | 0 | PASS |
| 3 | `go vet ./...` | Linux 容器 | 0 | PASS |
| 4 | `bash scripts/test-unit.sh` | Linux 容器 | 0 | PASS（全部纯内存单元测试通过） |
| 5 | `K8S_MANIFEST_REQUIRED=1 go test -count=1 ./test/manifest/` | Windows（kubectl v1.36.1 + kustomize v5.8.1） | 0 | PASS（28.8s，含新增 5 个监控断言） |
| 6 | `kubectl kustomize <5 个入口>` | Windows | 0 | PASS：application(6 docs)、migration(3)、infra-base(13)、infra-verify(3)、prometheus(5) |
| 7 | `kubectl apply --dry-run=server -f -`（application / migration / prometheus） | Windows（真实 API Server） | 0 | PASS（分别 `unchanged`/`configured`/`unchanged`，无创建） |
| 8 | `promtool check config /etc/prometheus/prometheus.yml` | 集群内 Pod | 0 | `SUCCESS: /etc/prometheus/prometheus.yml is valid prometheus config file syntax` |
| 9 | 变异测试（把抓取目标改成 `127.0.0.1:8000` 后重跑断言） | Windows | ≠0 | 断言按预期失败（`抓取目标="127.0.0.1:8000"，期望集群内 Service DNS`），已还原 |
| 10 | `kubectl -n surgecart rollout status` / `kubectl -n monitoring rollout status` | Windows | 0 | 两个 Deployment 均 successfully rolled out |

未执行（如实标记）：

| 门禁 | 状态 | 原因 / 替代 |
| --- | --- | --- |
| `bash scripts/test-k8s-manifest.sh`（原样执行） | **NOT_RUN** | 本机 `bash` 指向 WSL 且不可用；已逐步等价执行其全部步骤（kustomize 构建、`K8S_MANIFEST_REQUIRED=1 go test ./test/manifest/`、server-side dry-run） |
| `kubeconform`（API Schema 离线校验） | **NOT_VERIFIED** | 本机未安装；脚本内该项本身也是「未安装则跳过」 |
| `bash scripts/test-prometheus.sh` | **NOT_RUN** | 校验 compose 侧 Prometheus 配置，本次未修改 `prometheus/`；K8s 侧改为 `promtool check config`（见 #8） |
| `bash scripts/test-integration.sh` | **NOT_RUN** | 需要完整 MySQL/Redis/七牛栈；本任务未改 Go 代码 |
| Windows 全量 `go build ./...` | **NOT_RUN（预期失败）** | `test/deployk8s/fakestate` 使用 `syscall.Flock`（Linux 专有），已有环境限制；已用 Linux 容器等价执行 |

## 13. 已知限制

1. **单实例、无 HA**：Prometheus 重建期间（`Recreate` + RWO PVC）会出现采集断点；
   多副本需要处理 TSDB 写者唯一性与数据去重，本任务未做。
2. **无告警**：没有 Alertmanager 与规则；`up == 0` 不会自动通知（T4-04 之后另行授权）。
3. **静态抓取目标**：应用改名/扩容需同步改 `prometheus.yml` 并 `rollout restart`；
   未引入 `kubernetes_sd_configs`，因此也没有相应 RBAC。
4. **配置不热加载**：ConfigMap 变更不会自动生效，必须重启 Pod（文档已写明流程）。
5. **lag/outbox 只验证了「真实零值」**：未注入积压（保护业务数据），数值正确性依赖 T4-02 集成测试。
6. **生产 overlay 未打开指标开关**：仅 local overlay 为 `true`；生产是否开启属于后续发布决策。
7. **kubeconform 未执行**：API Schema 离线校验缺口（NOT_VERIFIED），但已用真实 API Server
   `--dry-run=server` 校验同一批资源。
8. **镜像导入方式**：本机无 `kind` CLI，使用 `ctr -n k8s.io images import`（kind 内部同一机制）；
   未验证 `kind load docker-image` 命令本身。

## 14. Git 交付信息

```text
FEATURE_BRANCH=feat/k8s-t4-03-prometheus-deployment
BASELINE_SHA=64ebbbd4c1c53c087c0b30c4f2636f48da433c51
IMPLEMENTATION_COMMIT=53fba1475d5789cfac805b27974e63ece2d7615f
FINAL_HEAD_SHA=53fba1475d5789cfac805b27974e63ece2d7615f
REMOTE_BRANCH_SHA=53fba1475d5789cfac805b27974e63ece2d7615f
WORKTREE_STATUS=clean（回填本段元数据时仅本文件变更，随后以 docs 提交推送）
```

最终状态：

```ini
PROJECT=SurgeCart
TASK=T4-03

BASELINE_SHA=64ebbbd4c1c53c087c0b30c4f2636f48da433c51
FINAL_BRANCH=feat/k8s-t4-03-prometheus-deployment
FINAL_HEAD_SHA=53fba1475d5789cfac805b27974e63ece2d7615f

APPLICATION_METRICS_ENDPOINT=http://surgecart.surgecart.svc.cluster.local:8000/metrics
APPLICATION_METRICS_ENABLED=true

PROMETHEUS_VERSION=v2.53.0
PROMETHEUS_DEPLOYMENT_STATUS=RUNNING_READY（1/1）
PROMETHEUS_PVC_STATUS=BOUND（5Gi RWO standard）
PROMETHEUS_SERVICE=ClusterIP prometheus.monitoring:9090

SURGECART_TARGET_STATUS=UP（lastError 为空）
HTTP_METRICS_STATUS=PASS（PromQL 有真实样本，含 200/404，路由模板无高基数）
GO_RUNTIME_METRICS_STATUS=PASS（go_goroutines / go_memstats_heap_alloc_bytes）
KAFKA_LAG_METRICS_STATUS=PASS（真实零值 + sampler_up=1；未注入积压）
OUTBOX_METRICS_STATUS=PASS（pending/oldest_age 均为真实 0 + sampler_up=1）

KUBERNETES_RUNTIME_VALIDATION=PASS（kind 集群内真实部署 + 真实抓取 + 真实 PromQL）
STATIC_TESTS=PASS（gofmt / kustomize build×5 / promtool / server-side dry-run）
UNIT_TESTS=PASS（Linux 容器 golang:1.26）
MANIFEST_TESTS=PASS（含新增 5 个监控断言 + 变异测试）

PUSH_STATUS=PUSHED（origin/feat/k8s-t4-03-prometheus-deployment）
WORKTREE_STATUS=CLEAN

REPORT=docs/reports/t4-03-prometheus-deployment/T4-03_PROMETHEUS_DEPLOYMENT_REPORT.md

T4_03_ACCEPTANCE=PASS
T4_04_STARTED=NO
T5_STARTED=NO

BLOCKERS=无阻断；缺口：kubeconform NOT_VERIFIED、scripts/test-k8s-manifest.sh 未原样执行（bash 不可用，已等价执行）
```
