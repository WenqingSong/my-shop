# Grafana 看板栈（Kubernetes）

> 任务：T4-04（Grafana Kubernetes Deployment and Monitoring Dashboard）。
> 上游：T4-03 已在 `monitoring` 命名空间部署 Prometheus（`prom/prometheus:v2.53.0`，Service `prometheus:9090`）。
> 本文只描述**集群内** Grafana 的部署、Provisioning 与查询语义；压测（T5）与告警不在范围内。

## 1. 范围与边界

已交付：

- Grafana 单实例 Deployment（`grafana/grafana:11.2.2`）+ ClusterIP Service + 数据卷 PVC；
- Data Source Provisioning：`Prometheus`（固定 uid `prometheus`，指向集群内 Service DNS）；
- Dashboard Provisioning：`SurgeCart Overview`（固定 uid `surgecart-overview`，14 个面板，四个维度）；
- restricted PSS 下的安全上下文（非 root、只读根文件系统、drop ALL、seccomp RuntimeDefault）；
- 结构化静态断言：`test/manifest/grafana_test.go`。

明确未交付（不要按「应该也有」推断）：

- 没有 Alertmanager / 告警规则 / 通知渠道；
- 没有 Prometheus Operator / ServiceMonitor / Grafana Operator / 任何 RBAC；
- 没有 Ingress / NodePort / LoadBalancer：Grafana 只在集群内可达，本机查看用 port-forward；
- 没有 HA（单副本 + SQLite）、没有外部数据库、没有镜像渲染（image renderer）插件；
- 没有在 UI 中手工维护的 Dashboard（`allowUiUpdates: false`，ConfigMap 是唯一事实来源）。

## 2. 目录结构

```text
manifest/kubernetes/monitoring/grafana/
├── base/
│   ├── kustomization.yaml              # 资源清单 + namespace + 公共标签
│   ├── configmap-datasource.yaml       # datasources/datasource.yaml（Prometheus）
│   ├── configmap-dashboard-provider.yaml # dashboards/dashboard-provider.yaml（自动导入）
│   ├── configmap-dashboard.yaml        # surgecart-overview.json（14 面板）
│   ├── pvc.yaml                        # grafana-data（2Gi RWO）
│   ├── deployment.yaml                 # grafana（11.2.2，单副本，Recreate）
│   └── service.yaml                    # ClusterIP:3000
└── overlays/local/
    └── kustomization.yaml              # 入口（不重复声明 Namespace）
```

部署入口：

```bash
# 前置：monitoring 命名空间由 prometheus overlay 创建
kubectl kustomize manifest/kubernetes/monitoring/grafana/overlays/local
kubectl apply -k manifest/kubernetes/monitoring/grafana/overlays/local
kubectl -n monitoring rollout status deployment/grafana
```

本机访问（仅人工查看，不是数据源路径）：

```bash
kubectl -n monitoring port-forward svc/grafana 3000:3000
# 打开 http://127.0.0.1:3000 ，默认管理员为镜像内置 admin/admin（本地专用，生产必须覆盖）
```

## 3. 安全上下文（restricted PSS）

| 项 | 值 | 说明 |
| --- | --- | --- |
| `runAsNonRoot` / `runAsUser` / `runAsGroup` | `true` / `472` / `472` | 官方镜像默认用户，显式固定避免镜像漂移 |
| `fsGroup` | `472` | PVC 授权给 grafana 用户（`grafana.db` 必须可写） |
| `seccompProfile` | `RuntimeDefault` | |
| `readOnlyRootFilesystem` | `true` | |
| `capabilities.drop` | `[ALL]` | |
| `allowPrivilegeEscalation` / `privileged` | `false` / `false` | |
| `automountServiceAccountToken` | `false` | Grafana 不访问 API Server，不需要 RBAC |

只读根文件系统下仍保留的可写目录（缺一个就会启动失败）：

| 路径 | 卷类型 | 用途 |
| --- | --- | --- |
| `/var/lib/grafana` | PVC `grafana-data` | `grafana.db`、插件、导出文件 |
| `/var/lib/grafana/dashboards` | ConfigMap（只读） | Dashboard Provider 扫描目录 |
| `/etc/grafana/provisioning/datasources` | ConfigMap（只读） | 数据源 Provisioning |
| `/etc/grafana/provisioning/dashboards` | ConfigMap（只读） | Provider 配置 |
| `/var/log/grafana` | emptyDir（64Mi） | 日志兜底（日志实际走 stdout，`GF_LOG_MODE=console`） |
| `/tmp` | emptyDir（内存 32Mi） | 临时文件兜底 |

## 4. 数据源与看板的可复现性

- 数据源 `url: http://prometheus.monitoring.svc.cluster.local:9090`：`localhost` 在 Pod 内指向 Grafana 自身，
  宿主机 IP / NodePort 绕过了 Service 语义，三者都被静态断言禁止。
- 数据源 uid 固定为 `prometheus`，Dashboard 面板内直接按 uid 引用；数据源与看板都不依赖「默认数据源」的隐式行为。
- Dashboard 通过 Provider 从 ConfigMap 挂载目录导入，`allowUiUpdates: false` + `disableDeletion: true`：
  UI 改动不会覆盖 Git，也无法删除 Provision 的看板。
- PVC 保存 Grafana 自身状态（管理员、组织、偏好）。**两层都要有**：
  只有 ConfigMap 无法恢复 UI 侧状态，只有 PVC 无法保证配置可复现。

## 5. 变更流程

```bash
# 改 ConfigMap（数据源 / Provider / Dashboard JSON）后：
kubectl apply -k manifest/kubernetes/monitoring/grafana/overlays/local
# Dashboard 文件更新会在 Provider 扫描周期（30s）+ ConfigMap 卷同步（~60s）内自动生效；
# 若改了 datasource/provider 结构，按需重启：
kubectl -n monitoring rollout restart deployment/grafana
```

## 6. 已知行为（不是缺陷）

- 无流量时 `histogram_quantile` 返回 NaN，P95 曲线出现断点：这是 Prometheus 的正常语义
  （窗口内没有观测值），不代表采集失败。
- `Kafka Lag Trend` 使用 `offset 15m`：刚启动的 Prometheus 在 15 分钟内没有对比样本，
  该面板会显示 `No data`。
- Kafka lag 在无秒杀流量时恒为 0；「积压扩大」的负向场景需压测（T5）才能产生，本任务不做压测。
