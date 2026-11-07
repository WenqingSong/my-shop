# Kubernetes 部署体系（Kustomize，T2-06）

本文是 SurgeCart 在 Kubernetes 上部署的唯一操作入口：目录结构、配置与 Secret 边界、
发布顺序、探针与终止预算、存储模型、失败恢复与验证分级。事实来源是
`manifest/kubernetes/` 下的 Kustomize 资源、`scripts/deploy-k8s.sh`、
`scripts/test-k8s-manifest.sh` 与 `test/manifest/`。

设计背景见 `docs/design/deployment.md`；迁移机制见 `docs/development/migrations.md`；
容器加固见 `docs/development/container-security.md`。

## 1. 范围与边界

- 本体系只编排**本仓库的后端服务**：应用 Deployment/Service、配置与凭据引用、持久化 PVC、
  以及独立的数据库迁移 Job。MySQL、Redis、Kafka、Ingress Controller、监控栈属于环境能力，
  不在本仓库内定义，也不写死任何云厂商配置。
- 不虚构生产入口：默认只提供 `ClusterIP` Service。没有真实域名、Ingress Controller 与 TLS
  方案之前不提供 Ingress 资源（叠加方法见 §6.3）。
- 真实集群行为（调度、滚动、PVC 绑定、信号时序）未验证的部分集中在 §13，不以静态校验代替。

## 2. 目录结构与两个部署入口

```text
manifest/kubernetes/
├── shared/                                   # 两个入口共用（同一份资源）
│   ├── kustomization.yaml
│   ├── namespace.yaml                        # Namespace/surgecart（PSS restricted）
│   └── database-configmap.yaml               # ConfigMap/surgecart-database-config
├── base/                                     # Application 基础层（环境无关）
│   ├── kustomization.yaml
│   ├── configmap.yaml                        # ConfigMap/surgecart-config
│   ├── deployment.yaml                       # Deployment/surgecart
│   ├── service.yaml                          # Service/surgecart（ClusterIP）
│   └── storage.yaml                          # PVC/surgecart-storage（RWO 5Gi）
├── overlays/
│   └── production/                           # Application 发布入口
│       ├── kustomization.yaml                # 镜像版本 + patches 组合
│       ├── database-patch.yaml               # 外部数据库地址
│       ├── configmap-patch.yaml              # 外部 Redis、七牛 bucket/domain
│       ├── replicas-patch.yaml               # 副本数（1）
│       └── resources-patch.yaml              # requests/limits
├── migration/
│   ├── base/
│   │   ├── kustomization.yaml
│   │   └── migration-job.yaml                # Job/surgecart-migrate（无版本后缀）
│   └── overlays/production/
│       ├── kustomization.yaml                # 组合：版本化 Job + 共享资源
│       ├── database-patch.yaml               # 与 Application 同值的外部数据库地址
│       └── job/kustomization.yaml            # 发布版本后缀 + 迁移镜像版本
└── examples/secrets/
    ├── database-secret.example.yaml          # Secret/surgecart-db-credentials（模板）
    └── application-secret.example.yaml       # Secret/surgecart-app-credentials（模板）
```

两个入口必须互不重叠，且各自可独立构建：

```bash
# Application（Deployment/Service/ConfigMap/PVC/Namespace）
kubectl kustomize manifest/kubernetes/overlays/production

# Migration（Job/ConfigMap/Namespace）
kubectl kustomize manifest/kubernetes/migration/overlays/production
```

`scripts/test-k8s-manifest.sh` 会从构建产物断言：Application 清单中没有 Job，
Migration 清单中没有 Deployment/Service/PVC。

## 3. Base 与 Overlay 的职责

| 层 | 只放什么 | 明确不放什么 |
|---|---|---|
| `shared/` | 两个入口必须一致的集群级事实：Namespace、数据库连接非敏感配置 | 应用专属配置、任何 Secret |
| `base/` | 与环境无关的资源结构：探针、安全上下文、终止预算、卷挂载、Service 端口 | 镜像版本、副本数、资源规格、外部地址 |
| `overlays/production/` | 环境差异：不可变镜像版本、副本数、资源规格、外部 DB/Redis 地址、七牛空间 | 资源结构（探针/安全上下文不得在 overlay 里重写） |
| `migration/base/` | 迁移 Job 的执行语义（命令、重试、超时、TTL、加固） | Deployment/Service，以及任何 Redis/JWT/七牛依赖 |
| `migration/overlays/production/` | 发布版本化 Job 名称、迁移镜像版本、外部数据库地址 | 应用资源 |

`base` 中的镜像 tag 是 `unreleased`，属于「不可直接发布」的占位；只有 overlay 才能给出
可发布版本。这样 `kubectl apply -k manifest/kubernetes/base` 不会意外把未打版本的对象推上线。

## 4. ConfigMap 与 Secret

### 4.1 ConfigMap 的取舍

T2-01 的契约有 63 项配置，但 ConfigMap 只写**生产部署必须显式固定**的项。未列出的项沿用
`internal/config` 注册表中的代码默认值，避免「把默认值复制一遍再各自漂移」。

| ConfigMap | 承载 | 被谁引用 |
|---|---|---|
| `surgecart-database-config` | `DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS`、DB type/host/port/user/name/charset/debug | Application + Migration |
| `surgecart-config` | `SERVER_ADDRESS`、`SERVER_GRACEFUL_TIMEOUT`、`LIFECYCLE_*`、`READINESS_*`、`STARTUP_DEPENDENCY_TIMEOUT`、`REDIS_DEFAULT_ADDRESS/DB`、`STORAGE_LOCAL_ROOT`、`QINIU_REGION`、`FLASH_SALE_DISPATCH_MODE`、`FLASH_SALE_KAFKA_ENABLED`、`FLASH_SALE_SHUTDOWN_TIMEOUT` | 仅 Application |

所有键名都必须与 `internal/config/contract.go` 的环境变量名完全一致，否则环境变量不会生效
（T2-01 不使用 GoFrame 的「下划线 → 点号」推导）。`test/manifest` 会把构建产物里的每个
ConfigMap 键回查契约注册表，写错一个字母即失败。

迁移为什么共用同一份数据库 ConfigMap：两个入口如果各写一份，迟早出现「迁移连 A 库、
应用连 B 库」。`test/manifest` 断言两边 `DATABASE_DEFAULT_HOST` 完全相同。

### 4.2 Secret 边界

| Secret | 键 | 消费者 |
|---|---|---|
| `surgecart-db-credentials` | `DATABASE_DEFAULT_PASS` | Application + Migration |
| `surgecart-app-credentials` | `REDIS_DEFAULT_PASS`、`AUTH_JWT_SECRET`、`ADMIN_SUPER_PASSWORD`、`QINIU_ACCESS_KEY`、`QINIU_SECRET_KEY` | 仅 Application |

- Migration **只**注入 `surgecart-db-credentials`：不接触 JWT、超管与七牛凭据。
- 仓库只提交 `*.example.yaml` 模板，值全部是 `REPLACE_WITH_*` 占位符；
  `test/manifest` 断言模板中不存在可用凭据，并断言契约里所有敏感项都有对应 Secret 键。
- 构建产物中永远不出现 `kind: Secret`：Kustomize 不生成 Secret，Secret 由部署环境创建。

创建方式（值只存在于 shell 变量，不进入仓库与镜像）：

```bash
kubectl -n surgecart create secret generic surgecart-db-credentials \
  --from-literal=DATABASE_DEFAULT_PASS="$DATABASE_DEFAULT_PASS"

kubectl -n surgecart create secret generic surgecart-app-credentials \
  --from-literal=REDIS_DEFAULT_PASS="$REDIS_DEFAULT_PASS" \
  --from-literal=AUTH_JWT_SECRET="$AUTH_JWT_SECRET" \
  --from-literal=ADMIN_SUPER_PASSWORD="$ADMIN_SUPER_PASSWORD" \
  --from-literal=QINIU_ACCESS_KEY="$QINIU_ACCESS_KEY" \
  --from-literal=QINIU_SECRET_KEY="$QINIU_SECRET_KEY"
```

首次部署时命名空间还不存在，先创建它（与清单同源，`scripts/deploy-k8s.sh` 在缺失时也会自动执行这一步）：

```bash
kubectl apply -f manifest/kubernetes/shared/namespace.yaml
```

> **Base64 不是加密。** Kubernetes Secret 的 `data` 只是 base64 编码，任何能读取该命名空间
> Secret 的主体都能还原明文。生产环境必须结合集群静态加密（`EncryptionConfiguration` /
> KMS provider）、最小权限 RBAC（把 Secret 读取限制到工作负载的 ServiceAccount）或外部密钥
> 管理系统（External Secrets / Vault / Sealed Secrets）。轮换凭据后需要重启 Pod 才会读到新值
> （环境变量只在容器启动时注入）。

## 5. Application Deployment

| 项 | 值 | 依据 |
|---|---|---|
| 副本数 | `1` | 本地文件存储不是共享存储，见 §7 |
| 更新策略 | `Recreate` | RWO PVC 无法在新旧 Pod 间同时挂载；多节点会卡在 Multi-Attach |
| `progressDeadlineSeconds` | `600` | 启动含依赖重试与七牛校验，10 分钟内可诊断失败 |
| 镜像 | `docker.cnb.cool/framework-station/my-shop:<短SHA>` | 与 CNB 发布标签一致，禁止 `latest` |
| 端口 | 命名端口 `http` → `containerPort: 8000` | Service 与探针均引用命名端口 |

### 5.1 健康探针

| 探针 | 路径 | 语义 | 参数 | 选择依据 |
|---|---|---|---|---|
| `startupProbe` | `/health` | HTTP Server 是否已开始监听 | period 2s / timeout 1s / failure 30（60s） | 启动包含 MySQL/Redis 重试（`STARTUP_DEPENDENCY_TIMEOUT=30s`）、schema 校验、seed 与七牛 `GetBucketInfo`；60s 覆盖该上界。启动期内 liveness 不生效，慢启动不会变成重启循环 |
| `livenessProbe` | `/health` | 仅进程存活 | period 10s / timeout 3s / failure 3（30s） | `/health` 是进程内常量响应，不访问任何外部依赖；30s 容忍窗口避免 GC/调度抖动触发重启 |
| `readinessProbe` | `/ready` | MySQL PingMaster + Redis PING | period 5s / timeout 3s / failure 2（10s） | 应用内探测超时 1s、结果缓存 3s，3s 的探针超时留有余量；10s 摘流窗口与 preStop 5s 同量级 |

冻结语义（T2-02，禁止扩散）：

- Kafka 故障、Consumer 未加入 Group、Outbox 积压**不进入** `/health` 与 `/ready`。
  Kafka 只影响异步处理延迟，用指标（`flashsale_consumer_joined`、
  `flashsale_outbox_backlog_pending`）观测，不得升级为摘流或重启。
- `/ready` 失败只摘流，不触发进程重启；`livenessProbe` 不指向 `/ready`，
  这一点由 `test/manifest` 断言。

### 5.2 preStop 与终止预算

冻结预算（T2-03 / T2-03.1，未改变）：

| 阶段 | 值 | 落地位置 |
|---|---|---|
| preStop 摘流窗口 | 5s | `lifecycle.preStop.exec`：`/bin/sh -c "sleep 5"` |
| HTTP Drain | 10s | `SERVER_GRACEFUL_TIMEOUT=10`（进程内实现） |
| Runtime Shutdown | 30s | `FLASH_SALE_SHUTDOWN_TIMEOUT=30s`（进程内实现） |
| 安全余量 | 15s | 由 `terminationGracePeriodSeconds` 扣除前三段得到 |
| Kubernetes 终止宽限期 | 60s | `terminationGracePeriodSeconds: 60` |

```text
Pod Deletion
    ↓  Endpoint 异步摘除
preStop: /bin/sh -c "sleep 5"      ← 这 5s 计入 60s 宽限期，不是额外的 5s
    ↓  kubelet 发送 SIGTERM
SIGTERM → HTTP Drain（最多 10s，超时强制关闭连接）
    ↓
Runtime Shutdown（Publisher → Consumer → Producer → Scanner，最多 30s）
    ↓
Process Exit（剩余约 15s 为 SIGKILL 前的安全余量）
```

要点：

- preStop 用 `/bin/sh -c "sleep 5"`，`/bin/sh` 与 `sleep` 都由 Alpine busybox 提供（T2-04 运行镜像），
  不引入额外二进制、不绕过只读根文件系统。
- 进程侧不实现第二套关闭机制：SIGTERM 仍由 `internal/cmd` 的 drain + `Runtime.Shutdown` 编排。
- `LIFECYCLE_PRE_STOP_DELAY=5s` 与 `LIFECYCLE_TERMINATION_GRACE_PERIOD=60s` 与清单中的
  preStop / 宽限期必须一致；不一致时进程启动即 fail-fast（`internal/termination` 预算校验）。
- preStop 若失败（例如命令不存在），kubelet 会立即发送 SIGTERM，摘流窗口退化为 0，
  但关闭顺序仍由进程保证——这也是必须保持窗口与实现同源的原因。

### 5.3 安全上下文（继承 T2-04）

| 层级 | 字段 | 值 |
|---|---|---|
| Pod | `runAsNonRoot` / `runAsUser` / `runAsGroup` | `true` / `10001` / `10001` |
| Pod | `seccompProfile.type` | `RuntimeDefault` |
| Pod | `automountServiceAccountToken` | `false` |
| Pod | `fsGroup` / `fsGroupChangePolicy` | `10001` / `OnRootMismatch`（PVC 可写） |
| Container | `allowPrivilegeEscalation` | `false` |
| Container | `readOnlyRootFilesystem` | `true` |
| Container | `privileged` | `false` |
| Container | `capabilities.drop` | `[ALL]` |

禁止项（由测试断言）：`hostNetwork` / `hostPID` / `hostIPC`、`hostPath` 卷、Docker Socket、
额外 capabilities。写入问题只能通过显式可写挂载解决，不允许把 `readOnlyRootFilesystem` 关掉。

可写路径只有两个：

- `/tmp`：`emptyDir.medium: Memory`，`sizeLimit: 16Mi`（Go 运行期临时文件）；
- `/app/storage`：PVC（`STORAGE_LOCAL_ROOT=/app/storage`）。

## 6. Service 与入口

### 6.1 Service

`Service/surgecart`：`ClusterIP`、`port 8000`、`targetPort: http`（命名端口）、
`sessionAffinity: None`。selector 与 Pod 模板标签一致（测试断言），否则 Service 没有端点。

不暴露额外管理端口：`/metrics` 与应用同端口，是否开启由 `FLASH_SALE_METRICS_ENABLED` 决定。
开启后由集群内 Prometheus 通过该 Service 抓取，不需要第二个端口或第二个 Service。

### 6.2 为什么没有 Ingress

本仓库没有真实域名、Ingress Controller 与 TLS 证书方案。凭空写一个 Ingress 会制造
「看起来能对外、实际不可用」的假象，也会把证书与 DNS 假设固化进仓库。因此默认只提供
集群内 Service；对外暴露由环境按自身 Ingress 方案叠加。

### 6.3 环境叠加示例（不随仓库提交）

```yaml
# 环境私有 overlay：在 production 之上增加 Ingress（示意，需替换域名与 TLS Secret）
resources:
  - ../../../../overlays/production
patches:
  - target:
      kind: Ingress
      name: surgecart
    patch: |-
      - op: replace
        path: /spec/rules/0/host
        value: shop.example.com
```

## 7. 存储

代码事实（审计结论）：

- `serve` 启动时用 `storage.NewLocal(STORAGE_LOCAL_ROOT)` 初始化本地存储，并向该目录写入
  轮播图占位图；`/storage/banners` 是静态路由。
- 七牛云只承担**上传凭证签发**，文件本体由浏览器直传七牛，不经过本服务。
  因此七牛不能替代 `/app/storage` 的持久化职责。

三种可选模型与当前选择：

| 方案 | 状态 | 说明 |
|---|---|---|
| 单副本 + RWO PVC | ✅ 当前默认 | `replicas: 1` + `strategy: Recreate`；最简单且不虚构共享存储 |
| 共享 RWX 存储 | ⏸ 未采用 | 需要集群提供 RWX StorageClass（NFS/CephFS/EFS）；仓库不假设其存在 |
| 外部对象存储 | ⏸ 未采用 | 需要把 LocalStorage 实现替换为对象存储实现，属于业务改造 |

当前模型的能力边界（必须如实认知）：

- **不能扩到多副本**：RWO PVC 只保证单节点挂载，多 Pod 会各自拿到独立卷内容，
  轮播图在不同 Pod 上可能不一致。
- **发布有短暂不可用**：`Recreate` 先停旧 Pod 再起新 Pod，没有滚动更新的无缝切换。
- **PVC 依赖集群存储能力**：`base` 不写死 `storageClassName`，使用集群默认 StorageClass。
  集群没有默认 StorageClass 时 PVC 会一直 `Pending`，需要管理员提供名称后在
  `overlays/production/kustomization.yaml` 取消注释补一个 patch（脚本不会自动创建）。

扩容前置条件：先落地 RWX 共享存储或对象存储改造，再把 `replicas` 调大并改回滚动更新策略。

## 8. Migration Job

### 8.1 执行语义（沿用 T2-05）

| 字段 | 值 | 说明 |
|---|---|---|
| 命令 | `/app/my-shop migrate up` | 覆盖镜像 ENTRYPOINT，只迁移，不启动 HTTP/Redis/Kafka |
| `parallelism` / `completions` | `1` / `1` | 并发保护来自数据库 `GET_LOCK`，不靠并行度 |
| `backoffLimit` | `4` | 连接失败、锁等待超时可重试收敛 |
| `activeDeadlineSeconds` | `900` | 防止 MDL/网络问题让发布无限挂起 |
| `ttlSecondsAfterFinished` | `86400` | 保留 24h 排障窗口后自动清理 |
| `restartPolicy` | `Never` | 失败由 Job controller 以新 Pod 重试 |
| `terminationGracePeriodSeconds` | `30` | 迁移无长连接，连接关闭即释放锁 |

### 8.2 Job 的不可变字段与版本化名称

Kubernetes Job 的 `spec.template` 是不可变字段：同一个 Job 名称无法在下一次发布时原地更新
Pod 模板（镜像变了就 apply 失败）。本体系用**版本化 Job 名称**解决：

```text
migration/overlays/production/job/kustomization.yaml
  nameSuffix: "-<短SHA>"      →  Job/surgecart-migrate-<短SHA>
  images.newTag: "<短SHA>"
```

- 每个发布版本产生一个新名称的 Job，`kubectl apply` 永不撞上不可变字段；
- 多流水线并发时不会互相删除 Job；真正的互斥仍由数据库级迁移锁保证
  （`parallelism: 1` 只是单 Job 内并行度，不能替代跨 Job 互斥）；
- 历史 Job 由 TTL 清理，排障窗口内仍可 `kubectl logs`。

Job 名称后缀与两个入口的镜像 tag 必须完全一致，`test/manifest` 从构建产物断言这一点。
脚本按构建产物里的真实名称等待，不自己拼字符串。

### 8.3 迁移失败

- `backoffLimit` 用尽后 Job 进入 `Failed`，应用 Deployment **不会被更新**；
- 迁移内容失败会留下 dirty 标记，重试不会绕过，也不会自动 `force`；
- 重跑同一个版本前必须先删除失败 Job（同名 Job 不会自动重跑）：

```bash
kubectl -n surgecart delete job surgecart-migrate-<短SHA>
bash scripts/deploy-k8s.sh
```

`migrate force` 只修改版本记录、不执行 SQL，属于人工风险操作，脚本永远不会调用它。

## 9. 发布流程（完整命令）

```bash
# 0) 前置：Secret 已存在（见 §4.2），镜像已推送且 tag 是短 SHA

# 1) 更新版本号（两处必须同值，测试会校验）
#    manifest/kubernetes/overlays/production/kustomization.yaml
#      images[0].newTag: "<短SHA>"
#    manifest/kubernetes/migration/overlays/production/job/kustomization.yaml
#      nameSuffix: "-<短SHA>"  且 images[0].newTag: "<短SHA>"

# 2) 替换环境专属占位符（REPLACE_WITH_*）
#    overlays/production/database-patch.yaml          DATABASE_DEFAULT_HOST
#    overlays/production/configmap-patch.yaml         REDIS_DEFAULT_ADDRESS / QINIU_BUCKET / QINIU_DOMAIN
#    migration/overlays/production/database-patch.yaml（与上面同值）

# 3) 静态校验（构建 + 结构化断言 + API Schema）
bash scripts/test-k8s-manifest.sh

# 4) 发布：先迁移，成功后才更新应用
bash scripts/deploy-k8s.sh
```

等价的纯 kubectl 流程（不经过脚本时，必须自己保证顺序）：

```bash
TAG=<短SHA>

# 4.1 构建并应用迁移
kubectl kustomize manifest/kubernetes/migration/overlays/production > /tmp/migration.yaml
kubectl apply -f /tmp/migration.yaml
kubectl -n surgecart wait --for=condition=complete \
  job/surgecart-migrate-${TAG} --timeout=900s
kubectl -n surgecart get job surgecart-migrate-${TAG} -o wide

# 4.2 迁移成功后才应用应用清单
kubectl kustomize manifest/kubernetes/overlays/production > /tmp/application.yaml
kubectl apply -f /tmp/application.yaml

# 4.3 等待可用并验证就绪
kubectl -n surgecart rollout status deployment/surgecart --timeout=600s
kubectl -n surgecart get pods -l app.kubernetes.io/name=surgecart
kubectl -n surgecart get endpoints surgecart
```

`scripts/deploy-k8s.sh` 做的事：预检（kubectl、集群、Secret、占位符、镜像一致性）→ 构建两个
清单 → 应用迁移并轮询 `Complete`/`Failed` 条件 → 仅在迁移成功后应用应用清单 →
`rollout status` → 等待 Pod `Ready`。任一步失败立即非零退出；迁移失败退出码为 `2`。

## 10. 失败恢复

| 场景 | 现象 | 处理 |
|---|---|---|
| 迁移失败（SQL/dirty） | Job `Failed`，应用未更新 | 查看 `kubectl -n surgecart logs job/<name>`；按 `docs/development/migrations.md` §7 排障；修复后删除 Job 重跑 |
| 迁移反复失败（连接/权限） | Job 重试 4 次后 `Failed` | 检查 Secret、DB 网络策略、账号 DDL 权限；不要用 `parallelism` 或 `force` 绕过 |
| 应用未就绪（`/ready` 503） | `rollout status` 超时 | `kubectl -n surgecart describe pod`、查看 `/ready` 依赖（MySQL/Redis）连通性 |
| 应用启动失败（配置） | Pod `CrashLoopBackOff`，日志指出环境变量名 | 修正 ConfigMap/Secret；`DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS=true` 会精确指出缺失项 |
| PVC 一直 `Pending` | Pod `Pending` | 集群无默认 StorageClass 或容量不足；由管理员提供 StorageClass 后按 §7 补 patch |
| 需要回滚应用 | 新版本异常 | 把 overlay 的 `newTag` 改回上一个短 SHA，重新构建应用入口并 `kubectl apply`；**不要回滚迁移**（schema 向前兼容是发布前提） |
| 误发布占位版本 | 不应发生 | `scripts/deploy-k8s.sh` 会拒绝 `REPLACE_WITH_*`、`latest`、`unreleased`、`0000000` |

并发发布：不同版本的 Job 名称不同，可以并存；数据库迁移锁保证同一时刻只有一个迁移执行。
后到的迁移会在锁等待超时后失败并由 Job 重试，属于预期行为，不应删除别的流水线正在运行的 Job。

## 11. 常见运维命令

```bash
# 构建（不需要集群）
kubectl kustomize manifest/kubernetes/overlays/production
kubectl kustomize manifest/kubernetes/migration/overlays/production

# 查看
kubectl -n surgecart get deploy,rs,pod,svc,pvc
kubectl -n surgecart get job -o wide
kubectl -n surgecart describe pod -l app.kubernetes.io/name=surgecart
kubectl -n surgecart logs -l app.kubernetes.io/name=surgecart --tail=200
kubectl -n surgecart logs job/surgecart-migrate-<短SHA>

# 健康
kubectl -n surgecart exec deploy/surgecart -- wget -qO- http://127.0.0.1:8000/health
kubectl -n surgecart exec deploy/surgecart -- wget -qO- http://127.0.0.1:8000/ready

# 配置变更后重启（环境变量只在启动时注入）
kubectl -n surgecart rollout restart deployment/surgecart
kubectl -n surgecart rollout status deployment/surgecart

# 清理历史 Job（TTL 之外的人工清理）
kubectl -n surgecart delete job -l app.kubernetes.io/component=database-migration
```

## 12. 本地构建 Manifest（无集群）

```bash
# 首选 kubectl（自带 Kustomize）
kubectl kustomize manifest/kubernetes/overlays/production > /tmp/application.yaml

# 环境没有 kubectl 时使用独立 Kustomize CLI（v5+）
kustomize build manifest/kubernetes/overlays/production > /tmp/application.yaml

# 完整校验：构建 + 结构化断言 + API Schema（kubeconform 存在时）
bash scripts/test-k8s-manifest.sh
```

`scripts/test-k8s-manifest.sh` 的行为：

1. 没有 kubectl/kustomize 直接失败（不会用「源 YAML 能解析」冒充构建成功）；
2. 构建两个入口，打印资源清单；
3. `go test ./test/manifest/` 解析构建产物做结构化断言；
4. 安装 `kubeconform` 时追加 Kubernetes API Schema 校验；
5. 集群可达时追加 `kubectl apply --dry-run=server`（不创建资源）。

## 13. 在真实集群部署

前置条件：

- 集群版本 >= 1.23（Pod Security Admission 标签才生效）；
- 已配置默认 StorageClass（或按 §7 指定）；
- 已创建两个 Secret（§4.2）；
- 节点可拉取 `docker.cnb.cool/framework-station/my-shop`（私有仓库需 `imagePullSecrets`，
  由环境在 Pod spec 中叠加，仓库不内置凭据）；
- 集群内可达外部 MySQL 与 Redis（NetworkPolicy / 安全组需放通）。

步骤：

```bash
kubectl config use-context <目标 context>
kubectl version                       # 客户端与服务端都要连通

# 首次部署：命名空间由清单创建；脚本会在缺失时按 shared/namespace.yaml 自动创建，
# 手工流程等价于：
kubectl apply -f manifest/kubernetes/shared/namespace.yaml

# 两个 Secret 必须在迁移之前存在（§4.2），否则脚本在预检阶段失败
kubectl -n surgecart get secret

bash scripts/test-k8s-manifest.sh     # 本地构建 + Schema 校验
bash scripts/deploy-k8s.sh            # 迁移 → 应用 → 等待可用

kubectl -n surgecart get deploy,svc,pod,pvc
kubectl -n surgecart get endpoints surgecart    # 必须出现 Pod IP:8000
```

首次部署顺序与后续发布相同（先迁移，后应用）：迁移在空库上建表，应用启动时只做 schema
就绪校验与 seed，不执行 DDL。

## 14. 验证分级

| 层级 | 手段 | 结论 |
|---|---|---|
| Kustomize 构建 | `kubectl kustomize` / `kustomize build` 真实退出码 | 已验证（本仓库环境） |
| 结构化静态断言 | `test/manifest` 解析构建产物（11 组断言） | 已验证 |
| API Schema | `kubeconform -strict`（Kubernetes 1.31 schema） | 已验证 |
| preStop 命令在运行镜像可用 | `docker run --entrypoint /bin/sh my-shop-app:t204 -c '... sleep ...'`（`test-container-runtime` PASS[4]） | 已验证（本地构建镜像） |
| server-side dry-run | `kubectl apply --dry-run=server` | 无集群，NOT_VERIFIED |
| 真实集群部署 | 迁移 Job、Deployment Available、Service、探针 | 无集群，NOT_VERIFIED |
| 滚动更新 / SIGTERM 时序 | 集群 preStop + 60s 宽限期实测（镜像层 SIGTERM 已在 Docker 验证） | 无集群，NOT_VERIFIED |
| 迁移失败阻断应用升级 | 集群内制造失败迁移并观察 | 无集群，NOT_VERIFIED |

明确区分：「清单可构建、字段合法」不等于「已在集群成功运行」。

## 15. 变更约束

- 不得修改订单业务逻辑、Kafka 契约、Outbox 模型、Consumer 状态机；
- 不得修改 T2-03 终止预算（5/10/30/60 与 15s 余量）、T2-04 容器安全策略、
  T2-05 迁移并发锁语义；
- 不得把 Migration 与 Application 合并成一个无序部署入口；
- 不得提交真实 Secret，不得把 Secret 写进 ConfigMap、镜像或日志；
- 不得为了「目录整齐」重写资源结构：`shared` / `base` / `overlay` 的职责边界是断言对象。
