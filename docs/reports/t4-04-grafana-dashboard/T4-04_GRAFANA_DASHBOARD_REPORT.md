# T4-04 Grafana Kubernetes Deployment and Monitoring Dashboard — 交付报告

> 项目：SurgeCart
> 顶层任务：任务四 —— Prometheus 基础监控
> 子任务：T4-04（Grafana Kubernetes Deployment and Monitoring Dashboard）
> 执行模型：DeepSeek-V4.1-Flash（单 Agent 串行）
> 环境：Windows 10/11 + Docker 29.9.3 + kind 集群 `surgecart-local`（k8s v1.37.0）+ kubectl v1.36.1
> 报告生成时间：2026-11-10

## 1. 结论摘要

Grafana `11.2.2` 已在 kind 集群 `monitoring` 命名空间中**真实运行**，通过 Provisioning 自动接入 T4-03 部署的 Prometheus，
并自动加载 `SurgeCart Overview` 看板（14 个面板，覆盖 HTTP / Go Runtime / Kafka / Outbox 四个维度）。
所有面板的 PromQL 均通过 Grafana 查询接口（`POST /api/ds/query`）返回 Prometheus 中的真实序列；
Pod 删除重建后 Data Source、Dashboard 与查询能力全部保留（PVC 未删除）。

| 验收项 | 结果 | 证据位置 |
| --- | --- | --- |
| Git 基线继承 T4-03 | **PASS** | §2 |
| Grafana 真实 Kubernetes 运行 | **PASS** | §5、§8 |
| Grafana Pod Running / Ready | **PASS** | §8 |
| Grafana PVC Bound | **PASS** | §5.4 |
| Restricted PSS 保持启用（未放宽） | **PASS** | §5.3 |
| Grafana Service 为 ClusterIP | **PASS** | §5.4 |
| Grafana 健康检查 | **PASS** | §9.1 |
| Prometheus Data Source 连接成功 | **PASS** | §9.2 |
| Dashboard Provisioning 成功 | **PASS** | §9.3 |
| HTTP 面板真实数据 | **PASS** | §7.2、§9.4 |
| Go Runtime 面板真实数据 | **PASS** | §7.2 |
| Kafka 面板真实数据 | **PARTIAL**（链路与标签真实，但无压测流量 → lag 恒为 0，非零积压曲线未观测） | §7.2、§11 |
| Outbox 面板真实数据 | **PASS**（真实 0 积压 + sampler_up=1；采样失败路径未做故障注入） | §7.2、§11 |
| Pod 重建后配置保留 | **PASS** | §9.5 |
| Git 提交 / 推送 / 工作区干净 | **PASS** | §12 |
| 交付报告完整可复核 | **PASS** | 本文件 |
| UI 视觉渲染（浏览器截图） | **NOT_VERIFIED**（仅 API 层验证） | §11 |

## 2. Git 基线

| 项 | 值 |
| --- | --- |
| 基线提交（T4-03 最终） | `982705a95b184082c4a41f1d76f44ae222309a14` |
| 基线分支 | `feat/k8s-t4-03-prometheus-deployment`（与 `origin/` 同步） |
| 基线核验 | `git cat-file -t` → `commit`；`origin/feat/k8s-t4-03-prometheus-deployment` 包含该提交 |
| T4-04 分支 | `feat/k8s-t4-04-grafana-dashboard`（从基线创建，未合并 main） |
| 开始前工作区 | 干净（`git status --porcelain` 无输出） |
| Prometheus 清单完整性 | 已核验 7 个文件（base 6 + overlay 2，含 deployment-patch） |

## 3. 环境与前置核验

### 3.1 集群与既有监控栈

| 检查项 | 实测结果 |
| --- | --- |
| kubectl context | `kind-surgecart-local` |
| Node | `surgecart-local-control-plane` Ready，v1.37.0 |
| Namespace `monitoring` | Active，PSS `enforce=restricted`（audit/warn 同值） |
| Prometheus Pod | `prometheus-7766899868-gq2vm` 1/1 Running |
| Prometheus Service | `prometheus` ClusterIP `10.96.142.39:9090` |
| Prometheus PVC | `prometheus-data` Bound 5Gi（未触碰） |
| SurgeCart 应用 Pod | `surgecart-7d4d84b88-xn4tb` 1/1 Running（surgecart 命名空间） |
| Prometheus Target | `prometheus` = up，`surgecart` = up（`/api/v1/targets` 实测） |

T4-03 的部署状态被重新核验而非常量假设：Pod / Service / PVC / Target 全部为本次任务开始时的实时查询结果。

### 3.2 指标名称与标签（以实际 Prometheus 数据为准）

从运行中的 Prometheus 读取 `/api/v1/label/__name__/values` 与 `/api/v1/series`，确认 T4-02 暴露的指标如下
（**注意**：任务书线索中的 `surgecart_http_in_flight` 实际为 `surgecart_http_requests_in_flight`，以源码/实际序列为准）：

| 指标 | 实际标签 | 说明 |
| --- | --- | --- |
| `surgecart_http_requests_total` | `method, route, status` | `route` 为 Gin 路由模板（如 `/products/:id`） |
| `surgecart_http_request_duration_seconds_bucket/_sum/_count` | `le, method, route, status` | Histogram |
| `surgecart_http_requests_in_flight` | 无业务标签 | Gauge |
| `surgecart_kafka_consumer_lag` | `consumer_group, topic, partition` | 实测值存在 `surgecart.flashsale.order.requested.v1` / partition `0` |
| `flashsale_outbox_backlog_pending` | 无业务标签 | Gauge |
| `surgecart_outbox_oldest_age_seconds` | 无业务标签 | Gauge |
| `surgecart_sampler_up` | `sampler`（`kafka_lag`、`outbox`） | 采样器健康 |
| `go_goroutines` / `go_memstats_heap_alloc_bytes` / `go_gc_duration_seconds_count` / `process_cpu_seconds_total` | 标准 Go/Process Collector | 未重复开发 |

## 4. 交付物

```text
manifest/kubernetes/monitoring/grafana/
├── base/
│   ├── kustomization.yaml                 # 3 ConfigMap + PVC + Deployment + Service，namespace=monitoring
│   ├── configmap-datasource.yaml          # datasources/datasource.yaml
│   ├── configmap-dashboard-provider.yaml  # dashboards/dashboard-provider.yaml
│   ├── configmap-dashboard.yaml           # surgecart-overview.json（14 面板）
│   ├── pvc.yaml                           # grafana-data 2Gi RWO
│   ├── deployment.yaml                    # grafana/grafana:11.2.2，单副本 Recreate
│   └── service.yaml                       # ClusterIP:3000
└── overlays/local/
    └── kustomization.yaml                 # 本地入口（不重复声明 Namespace）

test/manifest/grafana_test.go              # 5 组静态契约断言
docs/development/grafana.md                # 部署与变更流程说明
docs/reports/t4-04-grafana-dashboard/T4-04_GRAFANA_DASHBOARD_REPORT.md
```

未新增/未修改任何 Go 业务代码；未修改 Prometheus 清单；未改 main。

## 5. Grafana 部署

### 5.1 版本与镜像

| 项 | 值 |
| --- | --- |
| Grafana 版本 | `11.2.2`（Grafana OSS，固定 tag，非 latest） |
| 镜像 | `grafana/grafana:11.2.2` |
| 镜像 digest（pull 输出） | `sha256:d5133220d770aba5cb655147b619fa8770b90f41d8489a821d33b1cd34d16f89` |
| Pod 内镜像 ID | `sha256:845ad8362c493f9cf990cb360c8a0b617d771e1ae1a2aad5d020827942c877c9` |
| 镜像导入方式 | `docker save` → `docker cp` 到节点 → `ctr -n k8s.io images import`（本机无 `kind` CLI，与 T4-03 同一机制） |
| 节点内镜像 | `docker.io/grafana/grafana:11.2.2`（crictl 可见，`imagePullPolicy: IfNotPresent` 未回源拉取） |
| Grafana commit | `dbf571744d7dd2276e3198b54330e5a561b83953`（`/api/health` 返回） |

> 导入细节：kind 节点内 `/tmp` 是 `tmpfs`，`docker cp` 写入不落盘（cp 返回 0 但文件不存在），
> 改为拷贝到节点根目录 `/grafana-t4-04.tar` 后导入，导入成功后立即删除临时文件。

### 5.2 部署结构

| 资源 | 名称 | 关键参数 |
| --- | --- | --- |
| Deployment | `grafana` | replicas 1，strategy Recreate，progressDeadline 300s |
| Service | `grafana` | ClusterIP，`3000/TCP`，targetPort `http` |
| PVC | `grafana-data` | 2Gi，ReadWriteOnce，默认 StorageClass（`standard` / local-path） |
| ConfigMap | `grafana-datasources` | Data Source Provisioning |
| ConfigMap | `grafana-dashboard-provider` | Dashboard Provider（扫描 `/var/lib/grafana/dashboards`） |
| ConfigMap | `grafana-dashboard-surgecart` | `surgecart-overview.json` |

命名空间不重复声明（`monitoring` 由 Prometheus 清单创建），避免同一 Namespace 出现两处 PSS 标签来源。
未创建 NodePort / LoadBalancer / Ingress / ServiceAccount / Role / Secret。

### 5.3 安全上下文（实取自运行中的 Pod）

| 项 | 实测值 |
| --- | --- |
| `runAsNonRoot` | `true` |
| `runAsUser:runAsGroup` | `472:472`（镜像默认 grafana 用户，显式固定） |
| `fsGroup` | `472`（`OnRootMismatch`） |
| `seccompProfile.type` | `RuntimeDefault` |
| `readOnlyRootFilesystem` | `true` |
| `capabilities.drop` | `[ALL]` |
| `allowPrivilegeEscalation` / `privileged` | `false` / `false` |
| `automountServiceAccountToken` | `false`（Grafana 不访问 API Server，无需 RBAC） |
| requests / limits | `100m / 256Mi` — `500m / 512Mi` |
| 探针 | startup/liveness/readiness 均为 `GET /api/health` |

只读根文件系统下保留的可写目录（缺一即启动失败，全部有静态断言）：

| 路径 | 卷 | 用途 |
| --- | --- | --- |
| `/var/lib/grafana` | PVC `grafana-data` | `grafana.db`（1,093,632 B，owner `grafana:472`）、plugins |
| `/var/lib/grafana/dashboards` | ConfigMap（只读） | Provider 扫描目录 |
| `/etc/grafana/provisioning/datasources` | ConfigMap（只读） | 数据源 |
| `/etc/grafana/provisioning/dashboards` | ConfigMap（只读） | Provider 配置 |
| `/var/log/grafana` | emptyDir 64Mi | 日志兜底（实际日志走 stdout） |
| `/tmp` | emptyDir（内存 32Mi） | 临时文件兜底 |

命名空间 `restricted` 未放宽：Pod 能在 `enforce=restricted` 下 Ready 本身即证明其通过 PSS 准入。

### 5.4 PVC 与 Service

```text
persistentvolumeclaim/grafana-data   Bound   pvc-e6bba51c-c70e-4661-b366-478937d9a476   2Gi   RWO   standard
service/grafana                      ClusterIP   10.96.154.186   <none>   3000/TCP
```

`prometheus-data`（5Gi）未被删除或修改，Prometheus 数据完好（Target 仍 up，历史查询正常）。

## 6. Prometheus Data Source 集成

`configmap-datasource.yaml`（挂载为 `/etc/grafana/provisioning/datasources/datasource.yaml`）：

```yaml
apiVersion: 1
datasources:
  - name: Prometheus
    uid: prometheus
    type: prometheus
    access: proxy
    url: http://prometheus.monitoring.svc.cluster.local:9090
    isDefault: true
    editable: false
    jsonData:
      timeInterval: 15s
      httpMethod: POST
      prometheusType: Prometheus
      prometheusVersion: 2.53.0
```

设计要点：

- 集群内 Service DNS（非 localhost / 宿主机 IP / NodePort），静态断言禁止这三类地址；
- uid 固定，Dashboard 面板显式引用同一 uid，不依赖「默认数据源」隐式行为；
- `timeInterval: 15s` 对齐 Prometheus `scrape_interval`，避免 Grafana 用更细步长重复取样；
- `editable: false`：数据源以 Git 为准；
- 集群内 Prometheus 未开鉴权，因此**不写入任何凭据**（无 `secureJsonData` / basicAuth）。

验证（不是「配置文件存在」）：`GET /api/datasources/uid/prometheus/health`

```json
{"status":"OK","message":"Successfully queried the Prometheus API.","details":{"application":"Prometheus"}}
```

首次启动日志：`logger=provisioning.datasources msg="inserting datasource from configuration" name=Prometheus uid=prometheus`

## 7. SurgeCart Dashboard

### 7.1 设计要点

| 项 | 值 |
| --- | --- |
| 标题 / UID | `SurgeCart Overview` / `surgecart-overview`（固定，重复 apply 不产生副本） |
| 目录（folder） | `SurgeCart` |
| 面板数 | 14（9 timeseries + 5 stat） |
| 默认时间范围 / 刷新 | `now-1h` → `now` / `30s` |
| 数据源 | 全部面板显式 `uid=prometheus` |
| 写入方式 | ConfigMap Provisioning；`allowUiUpdates=false`、`disableDeletion=true` |
| 更新机制 | Provider 每 30s 扫描 + ConfigMap 卷同步（实测 100s 内自动生效，version 1 → 2，未重启 Pod） |

细节处理：

- 请求维度只用规范化路由 `route`（Gin 路由模板），不使用原始 URL；
- 错误率拆成 5xx（服务端）与 4xx（客户端）两条曲线，分母为全部状态码速率；
  无对应状态码时用 `or vector(0)` 显式补 0，避免「没有曲线」被误读；
- 延迟用 `histogram_quantile` 对每个 route 的 `le` 桶求和，不用 `_sum/_count` 的均值冒充；
- Go Runtime 面板全部限定 `job="surgecart"`，避免把 Prometheus 自身的 `go_*` 混进业务面板；
- Outbox 面板与 `surgecart_sampler_up` 并列展示，明确区分「真实 0 积压」与「采样失败 / 指标未产生」。

### 7.2 面板清单、PromQL 与实测结果

实测方式：`POST http://127.0.0.1:3000/api/ds/query`（Grafana 内部查询链路，与面板渲染同一路径），
`from=now-5m`、`to=now`、instant 查询。下表数值为 2026-11-10 03:1x UTC 的**真实返回值**。

| # | 面板 | 类型 | PromQL | 实测结果 |
| --- | --- | --- | --- | --- |
| 1 | HTTP Request Rate | timeseries | `sum by (route) (rate(surgecart_http_requests_total{job="surgecart"}[$__rate_interval]))` | `/health` 0.111、`/ready` 0.200、`/products/:id` 0、`/products` 0 req/s（静息） |
| 2 | HTTP Error Ratio | timeseries | 5xx：`(sum(rate(surgecart_http_requests_total{job="surgecart",status=~"5.."}[$__rate_interval])) or vector(0)) / clamp_min(sum(rate(surgecart_http_requests_total{job="surgecart"}[$__rate_interval])), 0.000001)`；4xx 同构（`status=~"4.."`） | 5xx = 0；4xx = 0（静息）；流量注入后 4xx = 0.281 |
| 3 | HTTP In-Flight | stat | `sum(surgecart_http_requests_in_flight{job="surgecart"})` | 0 |
| 4 | HTTP Latency (P95) | timeseries | `histogram_quantile(0.95, sum by (le, route) (rate(surgecart_http_request_duration_seconds_bucket{job="surgecart"}[$__rate_interval])))` | `/ready` 3.87 ms、`/health` 0.48 ms；无流量路由 NaN（见 §11） |
| 5 | Goroutines | timeseries | `go_goroutines{job="surgecart"}` | 37 |
| 6 | Heap Alloc | timeseries | `go_memstats_heap_alloc_bytes{job="surgecart"}` | 6,359,720 B（≈6.1 MiB） |
| 7 | GC Cycles Rate | timeseries | `rate(go_gc_duration_seconds_count{job="surgecart"}[$__rate_interval])` | 0.0444 次/s |
| 8 | Process CPU | timeseries | `rate(process_cpu_seconds_total{job="surgecart"}[$__rate_interval])` | 0.0089（核） |
| 9 | Kafka Consumer Lag (Total) | stat | `sum(surgecart_kafka_consumer_lag{job="surgecart"})` | 0 |
| 10 | Kafka Lag by Partition | timeseries | `sum by (topic, partition) (surgecart_kafka_consumer_lag{job="surgecart"})` | 0 `{topic=surgecart.flashsale.order.requested.v1, partition=0}` |
| 11 | Kafka Lag Trend (15m Delta) | timeseries | `sum(surgecart_kafka_consumer_lag{job="surgecart"}) - sum(surgecart_kafka_consumer_lag{job="surgecart"} offset 15m)` | 0 |
| 12 | Outbox Pending Backlog | stat | `flashsale_outbox_backlog_pending{job="surgecart"}` | 0（sampler_up=1，属真实零积压） |
| 13 | Outbox Oldest Event Age | stat | `surgecart_outbox_oldest_age_seconds{job="surgecart"}` | 0 s |
| 14 | Sampler Health | stat | `surgecart_sampler_up{job="surgecart"}` | `kafka_lag` = 1、`outbox` = 1（UP） |

## 8. 真实部署过程

```bash
# 1) 构建与静态校验
kubectl kustomize manifest/kubernetes/monitoring/grafana/overlays/local      # 6 个资源
kubectl apply -k .../overlays/local --dry-run=server                         # 全部 created (server dry run)

# 2) 镜像导入（见 §5.1）
docker save -o grafana-img.tar grafana/grafana:11.2.2
docker cp grafana-img.tar surgecart-local-control-plane:/grafana-t4-04.tar
docker exec surgecart-local-control-plane ctr -n k8s.io images import /grafana-t4-04.tar
docker exec surgecart-local-control-plane rm -f /grafana-t4-04.tar

# 3) 部署
kubectl apply -k manifest/kubernetes/monitoring/grafana/overlays/local
```

过程记录：

| 时间点 | 事件 |
| --- | --- |
| apply 后 0s | 6 个资源 created；PVC Pending（WaitForFirstConsumer，预期） |
| +21s | PVC Bound（`pvc-e6bba51c…`，2Gi，local-path） |
| +19s | Pod 调度成功，事件 `Pulled … already present on machine`（未回源拉取） |
| +12s | startupProbe 首次失败（connection refused，容器尚未监听 3000），属正常启动窗口 |
| +~30s | Pod 1/1 Running；日志出现 `HTTP Server Listen`、`inserting datasource from configuration name=Prometheus uid=prometheus`、`finished to provision dashboards` |

最终状态：

```text
pod/grafana-6dc754bdff-j59dt        1/1   Running   0
pod/prometheus-7766899868-gq2vm     1/1   Running   0
service/grafana                     ClusterIP   10.96.154.186   3000/TCP
persistentvolumeclaim/grafana-data  Bound       2Gi   RWO
```

## 9. 真实集成验证

### 9.1 Grafana 健康检查

```text
GET /api/health  → 200
{"database":"ok","version":"11.2.2","commit":"dbf571744d7dd2276e3198b54330e5a561b83953"}
```

### 9.2 Data Source 验证

| 检查 | 结果 |
| --- | --- |
| `GET /api/datasources` | 1 条：`Prometheus`，`uid=prometheus`，`type=prometheus`，`isDefault=true`，`url=http://prometheus.monitoring.svc.cluster.local:9090` |
| `GET /api/datasources/uid/prometheus/health` | `status=OK`，`Successfully queried the Prometheus API.` |

数据源数量为 1，说明重启未产生重复数据源（uid 固定生效）。

### 9.3 Dashboard Provisioning 验证

| 检查 | 结果 |
| --- | --- |
| `GET /api/search?query=%` | `SurgeCart`（dash-folder）、`SurgeCart Overview`（dash-db，folder=SurgeCart） |
| `GET /api/dashboards/uid/surgecart-overview` | title=`SurgeCart Overview`，uid=`surgecart-overview`，panels=14，version=2 |
| 面板数据源 | 14/14 面板 `datasource.uid=prometheus` |
| 面板 PromQL | 与 §7.2 表格逐条一致（由 API 读回校验） |

自动重载验证：修改 Dashboard JSON 后仅 `kubectl apply -k`（不重启 Pod），100s 后 API 读回的 version 由 1 变为 2，
表达式已包含新版本内容 —— 证明 Provider 扫描 + ConfigMap 卷同步链路真实工作。

### 9.4 真实 HTTP 样本

从 `monitoring` 命名空间的 Prometheus Pod 向业务 Service 发起真实请求（不引入 k6、不伪造数据）：

```bash
for i in $(seq 1 40); do
  wget http://surgecart.surgecart.svc.cluster.local:8000/health          # 200
  wget http://surgecart.surgecart.svc.cluster.local:8000/products        # 200
  wget http://surgecart.surgecart.svc.cluster.local:8000/products/999999 # 404
done
```

观察结果（1 分钟窗口，通过 Grafana 查询接口读取）：

| 指标 | 流量前 | 流量后 |
| --- | --- | --- |
| HTTP QPS（total） | 0.302 | **2.694** |
| HTTP 4xx 比例 | 0 | **0.281**（40/142 请求为 404，符合预期） |
| HTTP 5xx 比例 | 0 | 0（无 5xx 请求） |
| HTTP P95 延迟 | 2.44 ms | 2.49 ms |
| 按路由 QPS | — | `/health` 1.00、`/products` 0.742、`/products/:id` 0.758、`/ready` 0.20 |

曲线确实随真实流量变化，不存在 Fake 数据。

### 9.5 持久化验证（Pod 重建）

删除 Pod（**未删除 PVC、未触碰 Prometheus**）：

| 项 | 重建前 | 重建后 |
| --- | --- | --- |
| Pod 名称 | `grafana-6dc754bdff-hzznh` | `grafana-6dc754bdff-j59dt` |
| Pod UID | `6b4f866a-ff40-4707-af46-fbcd4f5e3aa6` | `b9b73d77-2b5b-4f91-b398-2473a7a2aa1d` |
| Ready | 1/1 | 1/1（`kubectl wait` condition met） |
| PVC | `pvc-e6bba51c-c70e-4661-b366-478937d9a476` | 同一卷，仍 Bound |
| `grafana.db` | 存在 | 存在，1,093,632 B，owner `grafana:472` |

重建后复核：

| 检查 | 结果 |
| --- | --- |
| `/api/health` | `database=ok`，version 11.2.2 |
| Data Source | `Prometheus` / uid `prometheus` / url 集群 DNS / default=true |
| Data Source health | `OK` |
| Dashboard | `SurgeCart Overview`，14 面板，**version 仍为 2**（未被重置 → 数据库保留） |
| 真实查询 | QPS 查询返回 0.723（有数据） |

## 10. 测试门禁

| # | 命令 | 环境 | 结果 |
| --- | --- | --- | --- |
| 1 | `gofmt -l .` | Windows | PASS（无输出） |
| 2 | `go build ./...` | Linux 容器 `golang:1.26` | PASS（退出码 0） |
| 3 | `go vet ./...` | Linux 容器 | PASS（退出码 0） |
| 4 | `bash scripts/test-unit.sh`（官方单元测试） | Linux 容器 | PASS（auth/config/metrics/health/dashboard/controller/mq.kafka/termination/worker/iam/flashsale/legacymigrate/migrations/middleware/cmd 全绿） |
| 5 | `K8S_MANIFEST_REQUIRED=1 go test -count=1 ./test/manifest/` | Windows（kubectl v1.36.1） | PASS（11.9s，含新增 5 组 Grafana 断言） |
| 6 | `go test -run "TestGrafana" -v ./test/manifest/` | Windows | PASS（5/5） |
| 7 | `kubectl kustomize …/grafana/overlays/local` | Windows | PASS（ConfigMap×3 + Deployment + Service + PVC） |
| 8 | `kubectl kustomize …/prometheus/overlays/local`（未改动，回归） | Windows | PASS |
| 9 | `kubectl apply -k …/grafana/overlays/local --dry-run=server` | 真实 API Server | PASS（6 资源全部 `created (server dry run)`） |
| 10 | Dashboard JSON 解析（`ConvertFrom-Json` + Go `json.Unmarshal`） | Windows | PASS（panels=14，uid/title 正确） |
| 11 | Provisioning 配置解析（Go `yaml.Unmarshal` + 运行时日志） | Windows / 集群 | PASS |
| 12 | `promtool check config /etc/prometheus/prometheus.yml` | 集群内 Pod | PASS（`SUCCESS … valid prometheus config file syntax`） |
| 13 | 变异测试：数据源改为 `http://localhost:9090` | Windows | 断言按预期失败（`期望集群内 Service DNS`），已还原并通过 |
| 14 | `kubectl -n monitoring rollout status` / Pod Ready | 集群 | PASS |

未执行（如实记录）：

| 门禁 | 状态 | 原因 / 替代 |
| --- | --- | --- |
| Windows 全量 `go build ./...` / `go vet ./...` / `go test ./...` | **NOT_RUN（预期失败）** | 仓库既有 `test/deployk8s/fakestate` 使用 `syscall.Flock`（Linux 专有），与本任务无关；已用 Linux 容器 `golang:1.26` 完整执行 |
| `bash scripts/test-k8s-manifest.sh`（原样） | **NOT_RUN** | 本机 `bash` 指向 WSL，WSL 内无 `kubectl`/`go`/`kustomize`；已逐步等价执行其全部步骤（Kustomize 构建 + `K8S_MANIFEST_REQUIRED=1 go test ./test/manifest/` + server-side dry-run） |
| `kubeconform` API Schema 离线校验 | **NOT_VERIFIED** | 本机未安装（脚本会 warn 跳过）；已用真实 API Server `--dry-run=server` 覆盖同一批资源 |
| 集成测试 / 压测 / race | **NOT_APPLICABLE** | 本任务无业务代码变更；压测属于任务五，明确禁止开始 |

## 11. 已知限制

1. **管理员凭据**：未注入 Secret，使用镜像内置默认管理员（`admin/admin`）。集群为本地 kind、Grafana 仅 ClusterIP、
   无 NodePort/Ingress，暴露面限于集群内；生产环境必须通过 Secret 覆盖 `GF_SECURITY_ADMIN_PASSWORD`（本任务禁止引入无关 Secret）。
2. **Kafka 面板为 PARTIAL**：lag 面板的查询链路、标签（`topic`/`partition`）与返回值均为真实数据，
   但本任务不做压测（禁止开始任务五），无秒杀订单流量，因此 lag 恒为 0，**未观测到非零积压曲线**。
3. **Outbox 采样失败路径未做故障注入**：当前 `sampler_up=1` 且积压真实为 0；
   面板已通过 `surgecart_sampler_up` 并列展示 + `noValue: "NO DATA (sampler?)"` 区分「真实 0」与「采样失败」，
   但未在真实集群中制造采样失败来验证渲染效果（属设计层面保证）。
4. **P95 无流量时断点**：`histogram_quantile` 在窗口内无观测值时返回 NaN，曲线出现缺口。
   这是 Prometheus 语义而非采集故障，未做平滑或 `or vector(0)`（会伪造「0 延迟」）。
5. **UI 视觉未验证**：所有验证通过 Grafana API 完成，未做浏览器截图渲染核对（NOT_VERIFIED）。
6. **单副本 + SQLite**：不支持 HA；Grafana 自身状态以 PVC 为单点。
7. **良性日志噪音**：Grafana 11.2.2 启动日志存在 `plugin xychart is already registered`（上游插件重复注册），
   不影响启动、查询与看板（已实测）。
8. **kubeconform 缺口**：见 §10。

## 12. Git 交付

| 项 | 值 |
| --- | --- |
| 分支 | `feat/k8s-t4-04-grafana-dashboard` |
| 基线 | `982705a95b184082c4a41f1d76f44ae222309a14`（未合并 main，未重写历史，未 force push） |
| 提交 1（实现） | `cf3f761863f83238490f4d210ac768c16f867a50` — feat(monitoring): T4-04 在 Kubernetes 内部署 Grafana 并完成 SurgeCart 监控看板 |
| 提交 2（报告） | `eb91393d94b05c5006544da0ada61b399d1e8947` — docs(monitoring): T4-04 Grafana 部署与看板交付报告 |
| 提交 3（元数据） | 仅回填本表与本文件 `FINAL_HEAD_SHA` 字段；该提交 SHA 无法写入自身内容，以远端分支 HEAD 为准 |
| 改动范围 | 仅新增 `manifest/kubernetes/monitoring/grafana/**`、`test/manifest/grafana_test.go`、`docs/development/grafana.md`、本报告 |
| 凭据检查 | 未包含任何真实凭据（无 Secret、无密码、无 token） |
| 业务代码 | 未修改任何 Go 业务代码 |
| 工作区 | 提交后干净 |
| 推送 | 已推送 `origin/feat/k8s-t4-04-grafana-dashboard`，远端 SHA 与本地一致 |

## 13. 最终状态

```ini
PROJECT=SurgeCart
TASK=T4-04

BASELINE_SHA=982705a95b184082c4a41f1d76f44ae222309a14
FINAL_BRANCH=feat/k8s-t4-04-grafana-dashboard
FINAL_HEAD_SHA=eb91393d94b05c5006544da0ada61b399d1e8947（内容提交；其后仅有一个回填本字段的元数据提交，分支 HEAD 以远端为准）

GRAFANA_VERSION=11.2.2
GRAFANA_DEPLOYMENT_STATUS=PASS（deployment/grafana 1/1 Ready，Running）
GRAFANA_PVC_STATUS=PASS（grafana-data Bound 2Gi RWO）
GRAFANA_SERVICE=ClusterIP grafana.monitoring:3000

PROMETHEUS_DATASOURCE_STATUS=PASS（/api/datasources/uid/prometheus/health = OK）
DASHBOARD_PROVISIONING_STATUS=PASS（uid=surgecart-overview，folder=SurgeCart）
DASHBOARD_PANEL_COUNT=14

HTTP_DASHBOARD_STATUS=PASS（QPS/错误率/延迟/In-Flight 均返回真实数据，真实流量可观测变化）
GO_RUNTIME_DASHBOARD_STATUS=PASS（Goroutines/Heap/GC/CPU 真实数据）
KAFKA_DASHBOARD_STATUS=PARTIAL（链路与标签真实，lag=0，无非零积压样本：未压测）
OUTBOX_DASHBOARD_STATUS=PASS（backlog=0 且 sampler_up=1；采样失败路径未故障注入）

POD_RECREATION_VALIDATION=PASS（Pod 重建后 DataSource/Dashboard/查询/数据库全部保留，PVC 未删）

KUBERNETES_RUNTIME_VALIDATION=PASS（kind surgecart-local，restricted PSS 未放宽）
STATIC_TESTS=PASS（gofmt + Linux 容器 build/vet）
UNIT_TESTS=PASS（Linux 容器 golang:1.26，官方 test-unit.sh）
MANIFEST_TESTS=PASS（含新增 5 组 Grafana 断言 + 变异测试）

PUSH_STATUS=PASS
WORKTREE_STATUS=CLEAN

REPORT=docs/reports/t4-04-grafana-dashboard/T4-04_GRAFANA_DASHBOARD_REPORT.md

T4_04_ACCEPTANCE=PASS（Kafka 面板 PARTIAL、UI 视觉 NOT_VERIFIED，已在 §11 披露）
TASK_4_COMPLETION=READY_FOR_ACCEPTANCE

T5_STARTED=NO

BLOCKERS=NONE
```
