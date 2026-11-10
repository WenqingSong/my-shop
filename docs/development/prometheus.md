# Prometheus 监控栈（Kubernetes）

> 任务：T4-03（Prometheus Kubernetes Deployment and Real Metrics Scrape Validation）。
> 上游：T4-02 已实现应用侧 `/metrics`（HTTP / Kafka Lag / Outbox / Go Runtime）。
> 本文只描述**集群内** Prometheus 的部署与抓取语义；Grafana（T4-04）与压测（T5）不在范围内。

## 1. 范围与边界

已交付：

- Prometheus 单实例 Deployment（`prom/prometheus:v2.53.0`）+ ClusterIP Service + TSDB PVC；
- 静态抓取配置：业务应用 `surgecart`（Service DNS）+ Prometheus 自身；
- restricted PSS 下的安全上下文（非 root、只读根文件系统、drop ALL、seccomp RuntimeDefault）；
- 结构化静态断言：`test/manifest/monitoring_test.go`。

明确未交付（不要按「应该也有」推断）：

- 没有 Alertmanager、没有告警规则；`up == 0` 不会被自动通知；
- 没有 Grafana、没有 Dashboard（T4-04）；
- 没有 ServiceMonitor / `kubernetes_sd_configs` / Operator / RBAC；
- 没有 HA（单副本）、没有远程存储（Thanos/VictoriaMetrics）；
- 没有 Ingress / NodePort：Prometheus UI 只在集群内可达，本机查看用 port-forward。

## 2. 目录结构

```text
manifest/kubernetes/monitoring/prometheus/
├── base/
│   ├── kustomization.yaml     # 资源清单 + namespace + 公共标签
│   ├── namespace.yaml         # monitoring，PSS enforce=restricted
│   ├── configmap.yaml         # prometheus.yml（抓取配置）
│   ├── pvc.yaml               # prometheus-data（TSDB，5Gi RWO）
│   ├── deployment.yaml        # prometheus（v2.53.0，单副本，Recreate）
│   └── service.yaml           # ClusterIP:9090
└── overlays/local/
    ├── kustomization.yaml
    └── deployment-patch.yaml  # 本地保留期 15d → 7d
```

部署入口：

```bash
kubectl kustomize manifest/kubernetes/monitoring/prometheus/overlays/local
kubectl apply -k manifest/kubernetes/monitoring/prometheus/overlays/local
```

与 Application/Migration 入口的关系：完全独立。监控栈不参与发布顺序（`scripts/deploy-k8s.sh`），
也不消费 `surgecart` 命名空间的 ConfigMap/Secret；它只通过网络读取业务 Service 的 `/metrics`。

## 3. 抓取配置

`base/configmap.yaml` 的 `prometheus.yml`：

```yaml
global:
  scrape_interval: 15s
  scrape_timeout: 10s
scrape_configs:
  - job_name: surgecart
    metrics_path: /metrics
    scrape_interval: 15s
    scrape_timeout: 10s
    static_configs:
      - targets:
          - surgecart.surgecart.svc.cluster.local:8000
  - job_name: prometheus
    static_configs:
      - targets: ["127.0.0.1:9090"]
```

关键约束（均有静态断言）：

1. **目标必须是集群内 Service DNS**：`<service>.<namespace>.svc.cluster.local:<port>`。
   禁止 `localhost` / `127.0.0.1`（Prometheus 自己的回环）、宿主机 IP、NodePort。
2. **`scrape_timeout` < `scrape_interval`**，否则抓取会堆积。
3. 15s 与 T4-02 的采样语义配套：HTTP 指标是同步累加，任意间隔都能看到增量；
   Kafka Lag（10s）与 Outbox（5s）是后台采样的 Gauge（覆盖语义），15s 抓取不会重复计数。
4. **静态配置的代价**：应用改名或副本数变化必须同步改本文件；副本 >1 时需要重新评估
   （要么每个 Pod 单独作为 target，要么引入服务发现与相应 RBAC）。当前是单副本，静态配置足够。

## 4. 安全与存储

| 项 | 取值 | 原因 |
| --- | --- | --- |
| 运行身份 | `65534:65534`（nobody），`runAsNonRoot` | 官方镜像默认用户，显式固定避免漂移 |
| 根文件系统 | `readOnlyRootFilesystem: true` | 可写路径只有 `/prometheus`（PVC）与 `/tmp`（内存 emptyDir） |
| capabilities | `drop: [ALL]`，`allowPrivilegeEscalation: false` | restricted PSS |
| seccomp | `RuntimeDefault` | 同上 |
| ServiceAccount | `automountServiceAccountToken: false` | 静态配置不访问 API Server，因此**不需要任何 RBAC** |
| 存储 | PVC `prometheus-data` 5Gi RWO | TSDB 跨 Pod 重建保留时间线 |
| 保留期 | base 15d；local overlay 7d | 本地 5Gi local-path 卷容量有限 |
| 副本 | 1（`Recreate`） | TSDB 单写者；RWO + 多副本会 Multi-Attach |

## 5. 部署与验证命令

```bash
# 1) 部署
kubectl apply -k manifest/kubernetes/monitoring/prometheus/overlays/local
kubectl -n monitoring rollout status deployment/prometheus
kubectl -n monitoring get pods,pvc,svc

# 2) 健康
kubectl -n monitoring exec deploy/prometheus -- wget -q -O - http://127.0.0.1:9090/-/healthy
kubectl -n monitoring exec deploy/prometheus -- wget -q -O - http://127.0.0.1:9090/-/ready

# 3) Target 状态（health 必须为 up，lastError 为空）
kubectl -n monitoring exec deploy/prometheus -- \
  wget -q -O - "http://127.0.0.1:9090/api/v1/targets?state=active"

# 4) 本机查看 UI / 用 PromQL（port-forward 只用于人工查看，不是抓取路径）
kubectl -n monitoring port-forward svc/prometheus 9090:9090
curl -s 'http://127.0.0.1:9090/api/v1/query?query=up{job="surgecart"}'

# 5) 配置语法自检
kubectl -n monitoring exec deploy/prometheus -- promtool check config /etc/prometheus/prometheus.yml
```

修改抓取配置后的生效方式（Prometheus 不会热加载挂载的 ConfigMap）：

```bash
kubectl apply -k manifest/kubernetes/monitoring/prometheus/overlays/local
kubectl -n monitoring rollout restart deployment/prometheus
```

## 6. 可查询的指标（来自 T4-02）

| 指标 | 类型 | 关键标签 | 说明 |
| --- | --- | --- | --- |
| `up{job="surgecart"}` | Gauge（Prometheus 自生成） | `instance`,`job` | 抓取是否成功 |
| `surgecart_http_requests_total` | Counter | `method`,`route`,`status` | `route` 是路由模板（如 `/products/:id`），不是原始 URL |
| `surgecart_http_request_duration_seconds` | Histogram | `method`,`route`,`status` | 秒 |
| `surgecart_http_requests_in_flight` | Gauge | 无 | 并发度；注意任务书写作 `..._in_flight`，实际名称以 T4-02 源码为准 |
| `surgecart_kafka_consumer_lag` | Gauge | `consumer_group`,`topic`,`partition` | messages；`dispatch_mode=kafka` 才有 |
| `flashsale_outbox_backlog_pending` | Gauge | 无 | 既有指标，复用同一 Backlog 口径 |
| `surgecart_outbox_oldest_age_seconds` | Gauge | 无 | 最老待发布事件年龄 |
| `surgecart_sampler_up` | Gauge | `sampler` | 1=最近一次采样成功，0=失败（业务指标保留上次值） |
| `go_*` / `process_*` | 多种 | 无 | 默认 Registry 自带，不重复实现 |

判读要点：

- **`surgecart_sampler_up == 0` 时，`lag` / `outbox` 的值是「上一次有效值」**，
  不能据此认定积压为零或消费停滞；
- 指标为 0 有两种含义：真实零值（`sampler_up=1`）与「启用但无业务样本」，
  必须结合 `sampler_up` 与是否有对应标签序列区分；
- `/metrics` 抓取不会计入 `surgecart_http_requests_total`（T4-02 已保证，T4-03 复核为空序列）。

常用查询：

```promql
up{job="surgecart"}
sum by (route, status) (rate(surgecart_http_requests_total{job="surgecart"}[5m]))
histogram_quantile(0.95, sum by (le, route) (rate(surgecart_http_request_duration_seconds_bucket{job="surgecart"}[5m])))
surgecart_http_requests_in_flight{job="surgecart"}
surgecart_kafka_consumer_lag{job="surgecart"}
flashsale_outbox_backlog_pending{job="surgecart"}
surgecart_outbox_oldest_age_seconds{job="surgecart"}
surgecart_sampler_up{job="surgecart"}
go_goroutines{job="surgecart"}
```

## 7. 已知限制

1. **单实例、无 HA**：Pod 重建期间（秒级到十几秒）没有采集，时间线上表现为断点。
2. **无告警**：只有指标，没有 Alertmanager 与规则；`up == 0` 需要人工或 T4-04 之后的告警链。
3. **静态目标**：应用改名/扩容不会自动被发现（见 §3）。
4. **本地保留期 7d**、5Gi：超出后按时间分块删除，历史查询会变空区间。
5. **只在 local overlay 打开应用指标开关**（`FLASH_SALE_METRICS_ENABLED=true`）；
   production overlay 是否开启属于后续发布决策，本任务未扩大范围。
6. **UI 不对外**：只有 ClusterIP，本机需 port-forward；这是有意的暴露面控制。
