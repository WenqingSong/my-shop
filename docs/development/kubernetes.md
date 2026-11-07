# Kubernetes 部署体系（Kustomize，T2-06 / T2-06.1 / T2-06.1.1）

本文是 SurgeCart 在 Kubernetes 上部署的唯一操作入口：目录结构、配置与 Secret 边界、
发布顺序与发布锁、版本降级保护、发布成功判定、探针与终止预算、存储模型、失败恢复与
验证分级。事实来源是 `manifest/kubernetes/` 下的 Kustomize 资源、
`scripts/deploy-k8s.sh`、`scripts/test-k8s-manifest.sh`、`scripts/test-deploy-k8s.sh`、
`test/manifest/` 与 `test/deployk8s/`。

设计背景见 `docs/design/deployment.md`；发布锁与串行化的设计、CAS 细节与剩余风险见
`docs/design/deploy-lock.md`；迁移机制见 `docs/development/migrations.md`；
容器加固见 `docs/development/container-security.md`。

## 1. 范围与边界

- 本体系只编排**本仓库的后端服务**：应用 Deployment/Service、配置与凭据引用、持久化 PVC、
  以及独立的数据库迁移 Job。MySQL、Redis、Kafka、Ingress Controller、监控栈属于环境能力，
  不在本仓库内定义，也不写死任何云厂商配置。
- 不虚构生产入口：默认只提供 `ClusterIP` Service。没有真实域名、Ingress Controller 与 TLS
  方案之前不提供 Ingress 资源（叠加方法见 §6.3）。
- 发布是**串行**的：任何通过 `scripts/deploy-k8s.sh` 执行的发布都在完整生命周期内持有
  `Lease/surgecart-deploy-lock`（§9、`docs/design/deploy-lock.md`）；直接 `kubectl apply`
  的人工操作不受该锁保护。
- 未经显式 `--allow-downgrade` 不得把 Deployment 降级到更旧的 commit（§9.5）。
- 真实集群行为（调度、滚动、PVC 绑定、信号时序、Lease 的真实并发语义）未验证的部分
  集中在 §14，不以静态校验或 fake kubectl 代替。

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
│       ├── version-patch.yaml                # app.kubernetes.io/version（发布版本标记）
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
└── examples/
    ├── secrets/
    │   ├── database-secret.example.yaml      # Secret/surgecart-db-credentials（模板）
    │   └── application-secret.example.yaml   # Secret/surgecart-app-credentials（模板）
    └── deploy-lock/
        └── rbac.yaml                         # 发布锁最小权限（Role/RoleBinding，人工授予）
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

发布锁的 Lease **不在**这两个入口里（`examples/deploy-lock/` 只有 RBAC 示例）：Lease 一旦
进入部署清单，每次 `kubectl apply` 都会把 `holderIdentity` 覆盖成清单值，等于发布流程自己
拆掉互斥。锁由脚本在需要时创建/接管/释放。

不同发布入口的职责边界（不要混用）：

| 入口 | 做什么 | 提供什么保护 |
|---|---|---|
| `bash scripts/deploy-k8s.sh` | 锁 → 降级保护 → 迁移 → 应用 → 验证 → 释放锁 | 发布互斥 + 降级保护 + 发布成功判定 |
| `kubectl apply -k migration/overlays/production` | 只创建迁移 Job | 数据库 `GET_LOCK`（仅保护迁移本身） |
| `kubectl apply -k overlays/production` | 只更新应用资源 | 无（不会先迁移、不会检查版本、不会占用发布锁） |
| `bash scripts/test-k8s-manifest.sh` | 校验清单构建产物（无需集群） | 结构/契约断言 |
| `bash scripts/test-deploy-k8s.sh` | 校验脚本行为（fake kubectl，无需集群） | 锁/降级/判定/故障语义 |

**生产发布只应使用第一个入口**；其余入口用于构建校验、排障或环境初始化。直接执行第三个
入口会绕过本任务建立的全部不变量（互斥、降级保护、成功判定）。

## 3. Base 与 Overlay 的职责

| 层 | 只放什么 | 明确不放什么 |
|---|---|---|
| `shared/` | 两个入口必须一致的集群级事实：Namespace、数据库连接非敏感配置 | 应用专属配置、任何 Secret |
| `base/` | 与环境无关的资源结构：探针、安全上下文、终止预算、卷挂载、Service 端口 | 镜像版本、副本数、资源规格、外部地址 |
| `overlays/production/` | 环境差异：不可变镜像版本、版本标记、副本数、资源规格、外部 DB/Redis 地址、七牛空间 | 资源结构（探针/安全上下文不得在 overlay 里重写） |
| `migration/base/` | 迁移 Job 的执行语义（命令、重试、超时、TTL、加固） | Deployment/Service，以及任何 Redis/JWT/七牛依赖 |
| `migration/overlays/production/` | 发布版本化 Job 名称、迁移镜像版本、外部数据库地址 | 应用资源 |

`base` 中的镜像 tag 是 `unreleased`、`app.kubernetes.io/version` 是 `unreleased`，属于
「不可直接发布」的占位；只有 overlay 才能给出可发布版本。这样
`kubectl apply -k manifest/kubernetes/base` 不会意外把未打版本的对象推上线。

三处版本必须同值（由 `test/manifest` 从构建产物断言，发布脚本也会在发布前拦截）：

1. `overlays/production/kustomization.yaml` 的 `images.newTag`；
2. `overlays/production/version-patch.yaml` 的 `app.kubernetes.io/version`（Deployment 与
   Pod 模板各一处）；
3. `migration/overlays/production/job/kustomization.yaml` 的 `nameSuffix` 与 `images.newTag`。

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

### 8.3 迁移失败与等待超时（两种不同结局）

- **失败**：`backoffLimit` 用尽后 Job 进入 `Failed=True`。应用 Deployment **不会被更新**，
  脚本退出码 `2`，打印 Job 条件、Pod 状态与日志，不 `force`、不删除 Job（恢复步骤见 §10.1）。
  失败证据同时写入 Lease 注解（`migration-state=MIGRATION_FAILED` + job/uid）。
- **超时**：`--migration-timeout`（默认 1080，必须大于 `activeDeadlineSeconds=900`）用尽时
  Job 既未完成也未失败。这**不等于失败**：Pod 可能仍在执行 DDL，脚本退出码 `6`，
  并且只要还有 Running/终止中的 Pod（或 Pod 状态查询失败）就明确禁止删除 Job
  （恢复步骤见 §10.2）。此时脚本**不释放发布锁**，关联记录保留，后续 Runner 会先核验 Job。
- 达到等待上限后脚本会**重新读取** Job：已 `Complete` 则继续发布，已 `Failed` 则退出码 `2`，
  只有仍运行/查询失败才是 `6`；
- 迁移内容失败会留下 dirty 标记，重试不会绕过，也不会自动 `force`；
- 重跑同一个版本前必须先删除失败 Job（同名 Job 不会自动重跑）。

`migrate force` 只修改版本记录、不执行 SQL，属于人工风险操作，脚本永远不会调用它。

## 9. 发布流程与发布锁

### 9.1 一次发布的完整步骤

`scripts/deploy-k8s.sh` 的执行顺序（任一步失败立即非零退出，后续步骤不执行）：

```text
1  预检：kubectl / 集群可达 / 构建两个清单 / REPLACE_WITH_* / 镜像与版本标记一致性 /
   迁移等待上限与 Job activeDeadlineSeconds 的时间关系
2  命名空间引导：缺失时按 shared/namespace.yaml 创建（幂等，锁的前置条件）
3  权限预检：auth can-i get|create|update leases + get|list jobs -n surgecart
4  获取发布锁：Lease/surgecart-deploy-lock（原子 create；已释放/已过期则先核验上一轮
   Migration Job，再用 resourceVersion CAS 接管）
5  启动后台续租（默认每 10s，单次请求超时 5s）
6  锁内复检：Secret 就绪 + 版本降级保护（当前 Deployment 版本 vs 本次目标版本）
7  Migration：①记录预期 Job 名 → ②apply 迁移清单 → ③读真实 Job UID 回写 → ④轮询
   （Failed 与 Timeout 分开处理，超时后二次判定）
8  Application：apply 应用清单 → 轮询 Deployment 收敛
9  发布成功判定：镜像 / app.kubernetes.io/version / generation / 副本 / 实际 Pod
10 释放发布锁（迁移记录保留）→ 打印 DEPLOY_K8S_SUCCESS
```

设计动机、CAS 细节与剩余风险见 `docs/design/deploy-lock.md`。核心不变量：

- **锁覆盖到「验证通过」为止**，不在迁移完成后提前释放；
- **迁移未确认终态时不释放锁**（AUD-01）：Job 仍在运行时，锁按租期自然过期，
  之后接管的 Runner 会先核验该 Job，不会直接开始新一轮发布；
- **只有全部验证项通过才打印 `DEPLOY_K8S_SUCCESS`**。

### 9.1.1 迁移生命周期记录（Lease 注解）

锁上会记录本轮迁移的关联，用于让「迁移还在跑」这件事跨进程、跨 Runner 可见：

| 注解 | 含义 |
|---|---|
| `deploy-lock.surgecart.io/migration-state` | `NO_MIGRATION` / `MIGRATION_PENDING` / `MIGRATION_RUNNING` / `MIGRATION_COMPLETE` / `MIGRATION_FAILED` |
| `deploy-lock.surgecart.io/migration-job` | 本轮 Job 名称 |
| `deploy-lock.surgecart.io/migration-job-uid` | 真实 Job `metadata.uid` |
| `deploy-lock.surgecart.io/migration-detail` | 失败原因（单行，≤200 字符） |

```bash
kubectl -n surgecart get lease surgecart-deploy-lock \
  -o jsonpath='{.metadata.annotations}{"\n"}'
```

注解只是「上一轮 Runner 的陈述」，接管方必须用真实 Job 对象交叉核验（见 §10.8）。

### 9.2 命令行参数

```bash
bash scripts/deploy-k8s.sh [选项]

  --context <name>              kubeconfig context（默认当前 context）
  --migration-timeout <sec>     等待迁移 Job 进入终态的上限（默认 1080）
                                必须大于 Job 的 activeDeadlineSeconds（默认 900）
  --rollout-timeout <sec>       等待 Deployment 收敛的上限（默认 600）
  --lock-wait-timeout <sec>     等待其他发布释放锁的上限（默认 60）
  --lease-duration <sec>        发布锁租期（默认 30；崩溃后最多这么久即可被接管）
  --lease-renew-interval <sec>  续租间隔（默认 10）
  --lease-renew-timeout <sec>   单次续租请求的超时（默认 5）
  --allow-downgrade             允许降级到更旧的 commit（默认禁止，见 §9.5）
  -h, --help                    显示帮助
```

租期参数必须满足（AUD-03）：

```text
renew_interval + renew_request_timeout + safety_margin(5) < lease_duration
默认：10 + 5 + 5 = 20 < 30
```

- 旧默认值 `10 + 30 > 30`：续租请求本身比租期还长，锁会在请求返回之前过期；
- 非法组合在**任何资源变更之前**拒绝（退出码 1，连 kubectl 都不会调用）；
- 该不变量只降低「正常网络延迟下锁提前过期」的概率，不能消除 Runner 暂停、
  网络分区与缺少 fencing 的问题（见 §9.7 与 `docs/design/deploy-lock.md` §10）。

迁移等待上限与 Job 截止时间的关系（AUD-04/06）：

```text
Job activeDeadlineSeconds = 900
脚本 --migration-timeout  = 1080（余量 180）
```

`--migration-timeout` 小于等于 900 时脚本拒绝执行；余量小于 180 时打印显式告警。
不同时调整这两处会让「脚本超时」与「Job 被 kubelet 终止」重新撞在一起。

典型的 CI 调用（不要携带 `--allow-downgrade`）：

```bash
bash scripts/deploy-k8s.sh --context production
```

### 9.3 发布成功判定

`rollout status` 只能证明「某个 ReplicaSet 可用」，无法证明「可用的是本次发布的版本」：
另一个流水线刚推上去的 Deployment 也会让它返回成功。因此脚本自己判定，逐项比对：

| # | 判据 | 失败含义 |
|---|---|---|
| 1 | `Deployment/surgecart` 存在 | apply 未生效或已被删除 |
| 2 | Pod 模板镜像 == 本次目标镜像 | 实际运行的不是本次构建的产物 |
| 3 | Pod 模板 `app.kubernetes.io/version` == 本次目标 commit | 版本标记与镜像不一致（清单被半更新） |
| 4 | `status.observedGeneration == metadata.generation` | controller 还没处理完本次变更 |
| 5 | `status.replicas/updatedReplicas/availableReplicas == spec.replicas`，`unavailableReplicas == 0` | 副本未收敛 |
| 6 | 每个非终止 Pod：`Running` + 容器 Ready + 镜像与版本标记匹配 | Pod 未就绪 / 跑的是旧版本 |

全部满足才打印：

```text
DEPLOY_K8S_SUCCESS
```

任一不满足：退出码 5，输出「实际值 vs 期望值」，**不打印成功标记**。
这就是「不把其他 Runner 的发布结果冒充成本次成功」的落地方式。

### 9.4 退出码

| 退出码 | 含义 | 应用是否已更新 |
|---|---|---|
| `0` | 发布成功且已验证（打印 `DEPLOY_K8S_SUCCESS`） | 是（且已验证为目标版本） |
| `1` | 参数 / 预检 / 清单构建 / Application apply 失败 | 否 |
| `2` | 迁移失败（Job `Failed=True`） | 否 |
| `3` | 发布锁失败（权限不足 / 获取超时 / 续租失败 / 服务端复核发现失锁） | 否（或未完成） |
| `4` | 版本降级保护拒绝（降级 / 分叉 / 无法验证版本顺序） | 否 |
| `5` | 发布后验证失败（镜像/版本/副本不一致，或未在超时内收敛） | 不确定，需按输出排查 |
| `6` | 迁移等待超时（Job 未确认终态，Pod 可能仍在执行 DDL；**锁与关联记录保留**） | 否 |
| `7` | 迁移生命周期未确认/未终结（拒绝接管、拒绝开始新一轮迁移、Job 与记录不一致） | 否 |
| `143` | 收到 SIGTERM（迁移已终态则释放锁；否则保留锁与记录） | 否（或未完成） |

`2` 与 `6` 的区别、`6` 与 `7` 的区别见 §10.1–§10.2 与 §10.8：

- `2` = 迁移**确定失败**（Job `Failed=True`，有原因、有日志）；
- `6` = 等待上限到了但 Job **没有终态**（可能仍在跑 DDL，禁止删除 Job）；
- `7` = 迁移生命周期**无法确认**（Job 找不到、查询失败、记录与实际不一致），
  或另一个 Runner 的迁移 Job 尚未终结，因此**拒绝接管**。

### 9.5 版本降级保护与人工降级

判定规则（`C` = 线上版本，`T` = 本次目标版本，均取自 Git commit）：

| 情况 | 处理 |
|---|---|
| Deployment 不存在（首发布） | 允许 |
| `C == T` | 允许（幂等重发） |
| `C` 是 `T` 的祖先 | 允许（升级） |
| `T` 是 `C` 的祖先 | 拒绝（退出码 4） |
| `C` 与 `T` 分叉 | 拒绝（退出码 4） |
| `C` 或 `T` 无法在本地 Git 历史中解析 | 拒绝（fail-closed，退出码 4） |

线上版本的事实来源是 Deployment Pod 模板标签 `app.kubernetes.io/version`（与镜像 tag 同值，
由 `overlays/production/version-patch.yaml` 写入、`test/manifest` 断言）。
浅克隆会让祖先关系不可判定，此时**必须**先补历史：

```bash
git fetch --unshallow        # 或 git fetch --depth=<足够深>
```

人工降级（应急回滚）流程：

```bash
# 1) 确认要回滚到的 commit 与镜像 tag 都在 Git 历史与镜像仓库里
git log --oneline -5
docker manifest inspect docker.cnb.cool/framework-station/my-shop:<短SHA> >/dev/null

# 2) 把三处版本同步改为目标短 SHA（三者必须一致，否则脚本拒绝发布）
#    manifest/kubernetes/overlays/production/kustomization.yaml       images.newTag
#    manifest/kubernetes/overlays/production/version-patch.yaml       app.kubernetes.io/version
#    manifest/kubernetes/migration/overlays/production/job/kustomization.yaml
#        nameSuffix 与 images.newTag
bash scripts/test-k8s-manifest.sh

# 3) 由人工显式授权降级（默认禁止）
bash scripts/deploy-k8s.sh --allow-downgrade
```

风险（脚本会在授权时重复打印）：

- **schema 不会回滚。** 迁移只向前；旧版本代码必须能读新 schema，否则会在运行期报错
  （缺列/类型变化/新约束）。
- 旧版本可能写坏新版本依赖的数据（例如新列未写、状态机回退），这类不一致**不会**因为
  再次升级而自动修复，需要人工核对。
- `--allow-downgrade` 在 CI 环境下必须额外设置 `DEPLOY_K8S_ALLOW_DOWNGRADE_IN_CI=1`；
  禁止把该参数写进流水线默认命令。

### 9.6 不经过脚本时的边界

纯 `kubectl` 手工流程**没有**发布锁与版本保护，必须自己保证顺序与互斥
（`docs/design/deploy-lock.md` §6 已说明锁只保护脚本路径）：

```bash
TAG=<短SHA>

# 迁移
kubectl kustomize manifest/kubernetes/migration/overlays/production > /tmp/migration.yaml
kubectl apply -f /tmp/migration.yaml
kubectl -n surgecart wait --for=condition=complete job/surgecart-migrate-${TAG} --timeout=900s

# 应用
kubectl kustomize manifest/kubernetes/overlays/production > /tmp/application.yaml
kubectl apply -f /tmp/application.yaml

# 自己判定成功（不能只看 rollout status）
kubectl -n surgecart get deploy surgecart \
  -o go-template='{{index .spec.template.metadata.labels "app.kubernetes.io/version"}}{{"\n"}}'
kubectl -n surgecart get pods -l app.kubernetes.io/name=surgecart -o wide
```

### 9.7 发布互斥的真实边界（不是强 fencing）

当前实现是 **cooperative / advisory mutual exclusion**，不是发布级 fencing
（T2-06.1.1 / AUD-02，**未彻底修复**）：

| 已经做到的 | 没有做到的 |
|---|---|
| 获取/接管/续租/释放全部走 `resourceVersion` CAS | API Server 不会校验「写 Deployment 的人是否持有锁」 |
| 每次资源变更前检查本地 `lost` 标记 | 本地标记存在传播延迟（续租间隔量级） |
| 变更前服务端复核 `holderIdentity` + `uid` | 复核与 apply 之间仍有 TOCTOU 窗口 |
| 迁移未终态不放锁、接管前核验 Job | 被挂起/分区的旧持有者**仍可能成功写入一次** |
| 发布成功判定会比对目标版本 | 已经发出的 `kubectl apply` **不能被撤销** |

残余窗口：`0 ≤ 窗口 ≤ 续租间隔 + 单次 kubectl 调用时长`（默认约 10s + 30s）。
缩小窗口的手段不能消除它；真正的 fencing 需要写入侧校验令牌（Admission Webhook
或专用发布控制器），属于后续架构决策，见 `docs/design/deploy-lock.md` §10.1。

因此：

- 不要用 `GET Lease → 检查 holder → kubectl apply` 的方式自行扩展发布流程并声称安全；
- 不要在文档/评审里把本机制描述成「强 Fencing」或「绝对不会并发发布」。

## 10. 失败恢复

### 10.1 迁移失败（Job `Failed=True`）

脚本行为：打印 Job 状态、Pod 状态、Job 日志（最后 200 行）、Pod describe，退出码 `2`；
**不** force、**不**删除 Job、**不**更新应用。

```bash
# 1) 看根因（脚本已经打印，仍可复查）
kubectl -n surgecart logs job/surgecart-migrate-<短SHA> --tail=200
kubectl -n surgecart describe pods -l job-name=surgecart-migrate-<短SHA>

# 2) 确认没有 Pod 仍在运行（必须先确认）
kubectl -n surgecart get pods -l job-name=surgecart-migrate-<短SHA>

# 3) 修好根因后删除失败 Job 再重跑（同名 Job 不会自动重跑）
kubectl -n surgecart delete job surgecart-migrate-<短SHA>
bash scripts/deploy-k8s.sh
```

`migrate force` 只修改版本记录、不执行 SQL，属于人工风险操作，脚本永远不会调用。
版本记录 dirty 的处理见 `docs/development/migrations.md` §7。

### 10.2 迁移等待超时（**不是失败**）

`--migration-timeout` 到期时 Job 可能仍然在跑，Pod 可能正在执行 DDL。

- 脚本退出码是 `6`，**不是** `2`；
- **达到上限后脚本会重新读取 Job 最新状态**：若此刻已 `Complete=True`，脚本继续发布
  （不会误报超时）；若已 `Failed=True`，走退出码 `2`；若仍运行或查询失败，才是 `6`；
- 脚本明确输出「超时不等同于失败」，并且：
  - 查询 Job 当前状态（conditions）与 Job UID；
  - 查询迁移 Pod 的 phase 与 `deletionTimestamp`（**查询失败会明确说「状态未知」**，
    不会被当成「没有 Pod」）；
  - 只要还有 `Running`/`Pending`/终止中的 Pod，或 Pod 状态未知，就明确**禁止删除 Job**；
  - 不 force、不中断 DDL。

超时之后 Lease 与 Job 分别处于什么状态（这是 T2-06.1.1 的核心变化）：

| 对象 | 状态 |
|---|---|
| Migration Job | **保持不变**，脚本不删除、不 patch、不重试 |
| Lease `surgecart-deploy-lock` | **不释放**：`holderIdentity` 仍是本 Runner，`renewTime` 停止更新，租期（默认 30s）后自然过期 |
| Lease 注解 | 保留 `migration-state=MIGRATION_RUNNING`、`migration-job`、`migration-job-uid` |

因此超时之后的下一个发布**不会**直接开始新一轮迁移：

```text
Runner A 超时退出（锁未释放）
  → 30s 后锁过期
  → Runner B 读取记录 + 读取真实 Job
      ├─ Job 仍 Running/Pending/Terminating → 退出码 7（拒绝接管）
      ├─ Job 已 Complete                    → 允许接管，继续发布
      ├─ Job 已 Failed                      → 允许接管，但不 force、不删 Job
      └─ Job NotFound / 查询失败            → 退出码 7（人工恢复，见 §10.8）
```

正确做法（继续等，而不是杀掉）：

```bash
# 先确认锁上的迁移记录
kubectl -n surgecart get lease surgecart-deploy-lock -o jsonpath='{.metadata.annotations}{"\n"}'

kubectl -n surgecart get job surgecart-migrate-<短SHA> -o wide
kubectl -n surgecart get pods -l job-name=surgecart-migrate-<短SHA> -o wide
kubectl -n surgecart logs -f job/surgecart-migrate-<短SHA> --tail=100

# 继续等待完成（不杀 Pod）
kubectl -n surgecart wait --for=condition=complete job/surgecart-migrate-<短SHA> --timeout=900s

# Job 收敛后再确认数据库侧没有残留 DDL（检查 information_schema / 迁移版本表）
```

只有确认 Pod 已退出、数据库无残留 DDL 之后，才允许删除 Job 重跑。
把超时当失败直接删 Job，可能中断迁移并留下 dirty 状态。

> 不要为了让流程「立刻重跑」而手工清空 Lease 上的迁移记录：记录一旦被清空，
> 下一个 Runner 就只能看到「没有上一轮迁移」，会在旧 DDL 仍在执行时开始新一轮发布。

### 10.3 发布锁相关失败（退出码 3）

| 现象 | 原因 | 处理 |
|---|---|---|
| `缺少 leases 的 get/create/update 权限` | 发布身份没有 RBAC | 按 `manifest/kubernetes/examples/deploy-lock/rbac.yaml` 授权后重跑 |
| `缺少 jobs 的 get/list 权限` | 迁移生命周期核验需要的只读权限缺失 | 同上（RBAC 示例已包含 `batch/jobs get,list`） |
| `另一个发布正在持有锁（holder=…）` 直到超时 | 另一个流水线正在发布 | 等它结束；不要提高 `--lock-wait-timeout` 去「抢」 |
| `发布锁续租失败` / 突然中止 | 锁被接管、API Server 抖动、网络分区 | 查看 `get lease` 记录；确认没有并行发布后重跑 |
| `发布锁已过期…尝试接管` | 上一个 Runner 崩溃 | 若上一轮迁移已终态则自动接管；否则退出码 7（§10.8） |
| `拒绝接管发布锁：上一轮迁移的生命周期不允许接管` | 上一轮迁移未终态/状态未知 | **等待**迁移进入终态后重跑，不要手工清记录（§10.8） |

### 10.4 Lease 卡住时的人工处理

先判断「持有者是否还活着」：

```bash
kubectl -n surgecart get lease surgecart-deploy-lock -o yaml
# 关注：holderIdentity / renewTime / leaseDurationSeconds / leaseTransitions
# 以及迁移记录：/migration-state、/migration-job、/migration-job-uid
```

- `renewTime` 在最近 30s 内持续变化 → 有 Runner 正在发布：**等待**，不要动锁；
- `renewTime` 长时间不更新且超过租期 → 死锁，下一次发布会自动接管（但会先核验 `migration-*` 注解）；
- 确认 Runner 已被强制终止（CI 任务取消、Pod 消失）但仍不愿等待租期时，才人工释放
  （**保留迁移注解**，只清 holder）：

```bash
# 人工释放：清空持有者。这里故意不带 resourceVersion —— 人工操作需要能覆盖
# 「反复续租的僵尸 Runner」，因此必须先确认没有任何存活的发布进程。
kubectl -n surgecart patch lease surgecart-deploy-lock --type=merge \
  -p "{\"spec\":{\"holderIdentity\":\"\",\"renewTime\":\"$(date -u +%Y-%m-%dT%H:%M:%SZ)\",\"leaseDurationSeconds\":1}}"
```

上面的 `--type=merge` 只改 `spec`，**不会**清掉 `metadata.annotations` 上的迁移记录——
这是刻意的：清空记录等于告诉下一个 Runner「没有上一轮迁移」。

禁止事项：

- **不要 `kubectl delete lease`**：删除与「他人 create/接管」之间存在竞态，
  可能删掉刚被其他 Runner 获取的锁；
- 不要在 Runner 仍存活时手工释放：两个发布并行会破坏本任务要建立的不变量；
- **不要为了让流水线通过而删除/清空 `migration-*` 注解**：先按 §10.8 核对真实 Job
  与数据库状态。

### 10.5 发布验证失败（退出码 5）

脚本会打印每一项 `实际值 vs 期望值`。按输出分流：

| 输出 | 处理 |
|---|---|
| `Pod 模板镜像 = …，期望 = …` | 清单/overlay 版本不对，或有其他流水线在并发发布 |
| `app.kubernetes.io/version = …` | 三处版本未同步（§9.5 第 2 步） |
| `observedGeneration=… 未追上 generation=…` | controller 未处理完：确认 Deployment 事件、配额、PVC |
| `副本未对齐` | 查看 `kubectl -n surgecart describe deploy surgecart` 与 Pod 事件 |
| `Pod/… 容器未 Ready` | `/ready` 依赖（MySQL/Redis）不通，见 §5.1 |
| `Pod 数量 = 0，期望 = 1` | Pod 未创建或被驱逐，见 `describe` / `get events` |

### 10.6 其他常见失败

| 场景 | 现象 | 处理 |
|---|---|---|
| 应用未就绪（`/ready` 503） | 收敛超时 | `kubectl -n surgecart describe pod`、确认 MySQL/Redis 连通性 |
| 应用启动失败（配置） | `CrashLoopBackOff`，日志指出环境变量名 | 修正 ConfigMap/Secret；`DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS=true` 会精确指出缺失项 |
| PVC 一直 `Pending` | Pod `Pending` | 集群无默认 StorageClass 或容量不足；按 §7 补 patch |
| 误发布占位版本 | 不应发生 | 脚本拒绝 `REPLACE_WITH_*`、`latest`、`unreleased`、`0000000` |

### 10.7 并发发布行为

- 第二个发布在锁上等待（默认 60s），拿不到就退出码 3，**不做任何变更**；
- 第一个发布结束（释放锁）后，第二个可接管并继续发布；若目标版本已相同则为幂等发布；
- **接管前会核验上一轮迁移 Job**：未终态则退出码 7（不做任何变更），已终态才继续；
- 迁移未终态时锁不会被释放（见 §10.2），因此「Job 还在跑」时不存在第二个 Runner
  拿到锁的窗口；
- 不同版本的迁移 Job 名称不同，可并存；数据库 `GET_LOCK` 仍保证同一时刻只有一个迁移在执行；
- 不要删除别的流水线正在运行的 Job / Pod。

### 10.8 迁移生命周期不一致（退出码 7，人工恢复）

退出码 `7` 的含义是「脚本无法证明上一轮迁移已经结束」，因此**拒绝接管或拒绝开始新一轮迁移**。
它出现的四种情况与处置：

#### 情况 A：上一轮 Job 仍运行 / 待调度 / 正在终止

```text
上一轮迁移 Job/… 仍为 active（uid=…，未终态）：拒绝接管
```

处置：**等待**。Job 进入 `Complete`/`Failed` 后重跑发布即可，不需要任何手工动作。

```bash
kubectl -n surgecart get job -l app.kubernetes.io/component=database-migration -o wide
kubectl -n surgecart wait --for=condition=complete job/<job-name> --timeout=900s
```

#### 情况 B：锁记录指向的 Job 不存在（`NotFound`）

```text
锁记录指向 Job/…，但该 Job 不存在。不得据此假定迁移已经完成。
```

这是最危险的一种：Job 可能被手工删除，也可能 DDL 在数据库侧留下了痕迹。
**不要直接清记录**，按顺序核实：

```bash
# 1) 看清记录（state / job / uid）
kubectl -n surgecart get lease surgecart-deploy-lock -o yaml

# 2) 核实数据库侧：迁移版本表 + 是否存在残留 DDL
#    （SHOW PROCESSLIST 看是否有长事务；information_schema 看表结构是否已变更）
kubectl -n surgecart logs job/<job-name> --tail=200 || true   # Job 没了就看历史日志/CI 日志

# 3) 二选一，并把结论写进注解
# 3a) 确认迁移已完成（schema 已是目标版本、无残留 DDL）→ 标记 COMPLETE
kubectl -n surgecart annotate lease surgecart-deploy-lock --overwrite \
  deploy-lock.surgecart.io/migration-state=MIGRATION_COMPLETE

# 3b) 确认迁移没有产生任何变更 → 复位为 NO_MIGRATION
kubectl -n surgecart annotate lease surgecart-deploy-lock --overwrite \
  deploy-lock.surgecart.io/migration-state=NO_MIGRATION \
  deploy-lock.surgecart.io/migration-job= \
  deploy-lock.surgecart.io/migration-job-uid=
```

注意：3a 之后脚本仍会用记录里的 job/uid 去核验，如果对象不存在则以「TTL 清理」放行；
3b 之后脚本会重新扫描 Job 列表，仍要求没有未终结的迁移 Job。

#### 情况 C：Job 查询失败（API 5xx / 超时 / 权限不足）

```text
无法读取上一轮 Job/…（…）：状态未知，拒绝接管
```

处置：确认 API Server 与 RBAC（`batch/jobs get,list`）正常后重跑。**不要**把「查不到」
当成「没有迁移」。

#### 情况 D：记录与事实不一致（uid 变化 / 未知状态 / 记录不完整）

```text
上一轮 Job/… 的 uid=… 与锁记录 … 不一致：同名 Job 被删除重建，拒绝接管
锁上的迁移状态 … 无法识别：拒绝接管
```

同名 Job 被删除重建意味着「记录说的那一轮」已经不可追踪。处置同情况 B：
先核实数据库状态，再决定标记 `COMPLETE` 还是复位 `NO_MIGRATION`。

> 所有情况下，真正的判据都是**数据库侧的事实**，不是 Lease 注解，也不是 Job 是否存在。

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

# 发布锁
kubectl -n surgecart get lease surgecart-deploy-lock -o yaml
kubectl -n surgecart get lease surgecart-deploy-lock \
  -o go-template='{{index .spec "holderIdentity"}} {{index .spec "renewTime"}} {{index .metadata "resourceVersion"}}{{"\n"}}'
kubectl auth can-i get leases.coordination.k8s.io -n surgecart
kubectl auth can-i create leases.coordination.k8s.io -n surgecart
kubectl auth can-i update leases.coordination.k8s.io -n surgecart

# 迁移生命周期记录（锁上的注解）+ 是否有未终结的迁移 Job
kubectl -n surgecart get lease surgecart-deploy-lock \
  -o go-template='{{range $k, $v := .metadata.annotations}}{{$k}}={{$v}}{{"\n"}}{{end}}'
kubectl -n surgecart get jobs -l app.kubernetes.io/component=database-migration -o wide
kubectl auth can-i list jobs.batch -n surgecart

# 线上版本（发布判定用同一组字段）
kubectl -n surgecart get deploy surgecart \
  -o go-template='{{index .spec.template.metadata.labels "app.kubernetes.io/version"}}{{"\n"}}'
kubectl -n surgecart get deploy surgecart \
  -o go-template='{{(index .spec.template.spec.containers 0).image}}{{"\n"}}'

# 健康
kubectl -n surgecart exec deploy/surgecart -- wget -qO- http://127.0.0.1:8000/health
kubectl -n surgecart exec deploy/surgecart -- wget -qO- http://127.0.0.1:8000/ready

# 配置变更后重启（环境变量只在启动时注入）
kubectl -n surgecart rollout restart deployment/surgecart
kubectl -n surgecart rollout status deployment/surgecart

# 清理历史 Job（TTL 之外的人工清理；不要删除正在运行的 Job）
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
3. `go test ./test/manifest/` 解析构建产物做结构化断言（13 组，含版本标记与发布锁契约）；
4. 安装 `kubeconform` 时追加 Kubernetes API Schema 校验；
5. 集群可达时追加 `kubectl apply --dry-run=server`（不创建资源）。

## 13. 在真实集群部署

前置条件：

- 集群版本 >= 1.23（Pod Security Admission 标签才生效；Lease 是 1.14+ 的稳定 API）；
- 已配置默认 StorageClass（或按 §7 指定）；
- 已创建两个 Secret（§4.2）；
- **发布身份具备 leases 的 get/create/update 权限（§13.1）**；
- 节点可拉取 `docker.cnb.cool/framework-station/my-shop`（私有仓库需 `imagePullSecrets`，
  由环境在 Pod spec 中叠加，仓库不内置凭据）；
- 集群内可达外部 MySQL 与 Redis（NetworkPolicy / 安全组需放通）；
- 发布 Runner 的时钟与集群一致（NTP），否则锁的过期判定会失真。

### 13.1 授予发布锁权限

```bash
# 1) 把 subjects 改成真实的发布身份（ServiceAccount / User / Group）
vi manifest/kubernetes/examples/deploy-lock/rbac.yaml

# 2) 首次部署时命名空间可能还不存在：先建命名空间，再授权
kubectl apply -f manifest/kubernetes/shared/namespace.yaml
kubectl apply -f manifest/kubernetes/examples/deploy-lock/rbac.yaml

# 3) 自检（三条都必须是 yes）
kubectl auth can-i get leases.coordination.k8s.io -n surgecart
kubectl auth can-i create leases.coordination.k8s.io -n surgecart
kubectl auth can-i update leases.coordination.k8s.io -n surgecart
```

首次部署的顺序与后续发布一致（先迁移、后应用），额外只需要「命名空间 + RBAC」这两步：

```bash
kubectl config use-context <目标 context>
kubectl version
kubectl apply -f manifest/kubernetes/shared/namespace.yaml
kubectl apply -f manifest/kubernetes/examples/deploy-lock/rbac.yaml
kubectl -n surgecart get secret                            # 两个 Secret 必须先存在

bash scripts/test-k8s-manifest.sh
bash scripts/test-deploy-k8s.sh                            # 发布脚本行为门禁（不需要集群）
bash scripts/deploy-k8s.sh                                 # 锁 → 迁移 → 应用 → 验证

kubectl -n surgecart get deploy,svc,pod,pvc
kubectl -n surgecart get endpoints surgecart               # 必须出现 Pod IP:8000
kubectl -n surgecart get lease surgecart-deploy-lock -o yaml  # holderIdentity 应为空
```

脚本会在命名空间缺失时自动创建它（`shared/namespace.yaml`，幂等），但**不会**自动创建 RBAC：
授权属于集群管理动作，不应由发布流程自我扩权。权限缺失时脚本在迁移之前失败（退出码 3）。

## 14. 验证分级

| 层级 | 手段 | 结论 |
|---|---|---|
| Kustomize 构建 | `kubectl kustomize` / `kustomize build` 真实退出码 | 已验证（本仓库环境） |
| 结构化静态断言 | `test/manifest` 解析构建产物（含版本标记、锁契约、租期预算、超时关系） | 已验证 |
| API Schema | `kubeconform -strict`（Kubernetes 1.31 schema） | 已验证 |
| 发布脚本行为 | `go test ./test/deployk8s/`：真实执行 `deploy-k8s.sh` + fake kubectl（锁/迁移生命周期/版本/判定/故障/续租参数） | 已验证（脚本语义） |
| 两 Runner 并发（脚本层） | 两个真实 bash 进程竞争同一 fake 状态目录 | 已验证（脚本互斥逻辑） |
| 迁移超时后「Job 仍在跑 → 拒绝接管」 | fake 状态 + 真实脚本进程 | 已验证（脚本语义） |
| 续租参数不变量（合法/临界/非法） | 真实脚本退出码 + 调用记录（非法组合零 kubectl 调用） | 已验证 |
| preStop 命令在运行镜像可用 | `docker run --entrypoint /bin/sh my-shop-app:t204 -c '... sleep ...'`（`test-container-runtime` PASS[4]） | 已验证（本地构建镜像） |
| server-side dry-run | `kubectl apply --dry-run=server` | 无集群，NOT_VERIFIED |
| 真实集群部署 | 迁移 Job、Deployment Available、Service、探针 | 无集群，NOT_VERIFIED |
| **Lease 在真实 API Server 上的并发语义** | 真实集群 / etcd 事务 / 多 apiserver | 无集群，**NOT_VERIFIED** |
| **RBAC 最小权限的实际效果（含 `jobs get,list`）** | 真实 RoleBinding + `auth can-i` | 无集群，**NOT_VERIFIED** |
| **Job 被 `DeadlineExceeded` 终止后的条件传播时延** | 真实集群 + 真实 DDL 负载 | 无集群，**NOT_VERIFIED** |
| **fencing 窗口实测** | 真实暂停 Runner + 网络分区 | 无集群，**NOT_VERIFIED** |
| 滚动更新 / SIGTERM 时序 | 集群 preStop + 60s 宽限期实测 | 无集群，NOT_VERIFIED |
| 迁移失败阻断应用升级（真实集群） | 集群内制造失败迁移并观察 | 无集群，NOT_VERIFIED |

明确区分：「清单可构建、字段合法」与「脚本在给定状态序列下行为正确」都不等于
「已在真实集群成功运行」。fake kubectl 无法冒充 API Server 的并发保证。

**fake kubectl 已验证的场景**（T2-06.1.1 新增/扩展）：

- Lease `create` 原子性；`replace` 的 `resourceVersion` 与 `uid` 校验；确定性 409；
- 续租请求超时、续租 API 500、续租 CAS 冲突、续租 UID 冲突 → 判定失锁并中止；
- Job 状态序列（不存在 / 运行中 / 删除中 / Complete / Failed）、Job UID 变化、
  Job 列表扫描（未终结迁移阻断接管）；
- 「apply 返回错误但对象已创建」「等待中 Job 消失」「记录指向的 Job NotFound」；
- 发布超时后锁与注解保留、第二个 Runner 被拒绝、Job 终态后接管放行。

**仍需真实集群验证的场景**：etcd 事务下的真实 CAS 语义、watch 缓存可见性延迟、
RBAC 实际效果、网络分区/时钟偏移下的接管行为、fencing 窗口大小、
kubelet 终止迁移 Pod 后的条件传播时延。

## 15. 变更约束

- 不得修改订单业务逻辑、Kafka 契约、Outbox 模型、Consumer 状态机；
- 不得修改 T2-03 终止预算（5/10/30/60 与 15s 余量）、T2-04 容器安全策略、
  T2-05 迁移并发锁语义、迁移 SQL 与数据模型；
- 不得把 Migration 与 Application 合并成一个无序部署入口；
- 不得把 Lease 放进 Kustomize 入口（apply 会覆盖 holderIdentity，等于拆掉互斥）；
- 不得把发布锁退化为本地 `flock` 或「先 GET 再无条件写」；
- 不得在迁移完成后提前释放发布锁，也不得在没有验证的情况下打印 `DEPLOY_K8S_SUCCESS`；
- **不得在迁移未确认终态时释放发布锁**（含 SIGTERM/cleanup 路径、续租失败路径）；
- **不得清空 `deploy-lock.surgecart.io/migration-*` 注解来绕过生命周期检查**；
- **不得只信 Lease 注解就接管**：必须用真实 Job 对象核验 state/uid/终态；
- **不得声称实现了强 fencing**：当前是 cooperative/advisory 互斥，AUD-02 未彻底修复；
- 不得把 `--migration-timeout` 降到 `activeDeadlineSeconds` 或以下；
- 不得默认开启 `--allow-downgrade`，也不得把它写进流水线默认命令；
- 不得提交真实 Secret，不得把 Secret 写进 ConfigMap、镜像或日志；
- 不得为了「目录整齐」重写资源结构：`shared` / `base` / `overlay` 的职责边界是断言对象。
