# 本地 Kubernetes 基础设施（T2-07：集群 + MySQL / Redis / Kafka）

本文是「在本机跑起一个真实 Kubernetes 集群，并让三个依赖在集群内可用」的操作入口。
事实来源：`scripts/k8s-cluster.sh`、`scripts/k8s-infra.sh`、
`manifest/kubernetes/infrastructure/`、`test/manifest/infrastructure_test.go`、
`scripts/test-k8s-infra.sh`、`scripts/test-k8s-cluster.sh`。

应用自身的发布流程仍然只看 `docs/development/kubernetes.md`（发布锁、迁移顺序、降级保护）。
本文只覆盖**依赖**，不覆盖应用发布。

## 1. 范围与边界

- 只面向**本地开发**：单节点、单副本、无 HA。MySQL 5.x/8.x 的复制、Kafka 多 Broker、
  Redis 哨兵/集群都不在本仓库内定义。
- 生产环境的 MySQL/Redis/Kafka 属于环境能力（托管数据库/缓存/消息），
  继续通过 `manifest/kubernetes/overlays/production/` 覆盖为外部地址。
- 不引入 Helm、Operator、Service Mesh、GitOps：本阶段的目标是「能真实运行并可以被验证」，
  不是「再建一套平台」。
- 应用部署、业务链路验证属于 T2-08；监控属于 T2-09；压测属于 T2-10。

## 2. 集群方案：kind（唯一方案）

选择依据（先看本机已有什么，再决定装什么）：

| 能力 | 是否具备 | 结论 |
|---|---|---|
| Docker daemon | 必需 | 已具备（镜像构建、compose 联调都依赖它） |
| kind | 本方案采用 | 唯一新增工具：一个二进制，用 Docker 容器承载控制面 |
| minikube / k3d | 不引入 | 与 kind 解决同一问题；同时装两个等于让「本地集群」出现多个事实来源 |

kind 的硬前提是 **Docker 必须允许特权容器**（控制面容器需要挂载 sysfs/cgroup、
创建网络命名空间）。受限的嵌套容器（无 `CAP_SYS_ADMIN`、禁止 mount/unshare）无法运行
kind、minikube-docker、k3d 中的任何一个。

先检查环境：

```bash
bash scripts/k8s-cluster.sh doctor
```

- 全部满足 → 退出码 0，可以创建集群；
- 缺工具或不允许特权容器 → 非零退出，并说明缺什么、为什么这是硬前提。

## 3. 集群生命周期

```bash
bash scripts/k8s-cluster.sh create     # 创建（已存在则跳过，不重建）
bash scripts/k8s-cluster.sh status     # 节点 Ready / 系统组件 / StorageClass / 命名空间内资源
bash scripts/k8s-cluster.sh info       # context、API 地址、集群内 DNS、三个 port-forward 命令
bash scripts/k8s-cluster.sh destroy    # 删除（会丢数据，见 §9）
```

幂等语义：

- `create` 先 `kind get clusters` 判断；已存在时只做状态检查，**不会**重建或改动集群；
- `destroy` 在集群不存在时成功返回（已经是目标状态）；
- 两条命令都不会“部分成功”：失败即非零退出，不会留下「以为建好了」的中间态。

集群名默认 `surgecart-local`，可用 `K8S_CLUSTER_NAME` 覆盖；
kubectl context 固定为 `kind-<集群名>`。

## 4. 依赖的部署与验证

```bash
bash scripts/k8s-infra.sh secrets    # 从 .env 创建 Secret/surgecart-db-credentials
bash scripts/k8s-infra.sh create     # 应用清单 + 等待三个 Deployment 与 Topic 初始化完成
bash scripts/k8s-infra.sh status     # Deployment 可用性 / Job 完成状态
bash scripts/k8s-infra.sh verify     # 集群内真实验证（SELECT 1 / PING / 生产+消费）
bash scripts/k8s-infra.sh delete     # 删除依赖（会丢 PVC 数据）
```

`secrets` 需要的两个键（写入仓库根目录的 `.env`，该文件已被 `.gitignore` 忽略）：

```dotenv
MYSQL_ROOT_PASSWORD=<管理员口令>          # 只在首次初始化 PVC 时使用
DATABASE_DEFAULT_PASS=<应用账号口令>      # 应用账号 surgecart 的口令
```

缺值时脚本直接失败并指出缺哪个键，**不会**生成默认口令。凭据只经过 0700 临时目录与管道，
不打印、不写入仓库；命名空间内的 Secret 用 `replace` 更新（`apply` 会把内容再写进
`last-applied-configuration` 注解，等于同一份口令存两遍）。

## 5. 目录结构

```text
manifest/kubernetes/
├── shared/                                  # 三个入口共用（Namespace + 数据库连接配置）
└── infrastructure/
    ├── base/                                # 本地依赖基座（无 production overlay）
    │   ├── kustomization.yaml
    │   ├── configmap.yaml                   # surgecart-kafka-config（Broker 属性 + Topic 名称）
    │   ├── mysql.yaml                       # PVC + Service + Deployment
    │   ├── redis.yaml
    │   ├── kafka.yaml
    │   └── kafka-topics-job.yaml            # Topic 初始化（幂等 --if-not-exists）
    └── verify/                              # 一次性验证 Job（verify 命令专用）
        ├── mysql-verify.yaml
        ├── redis-verify.yaml
        └── kafka-verify.yaml
```

为什么 `infrastructure/` 没有 production overlay：本地依赖与生产依赖是**不同的事实**
（本地是容器化的单实例，生产是托管服务）。给本地依赖加 overlay 只会制造
「看起来也能上生产」的错觉。生产地址仍在 `overlays/production/` 里覆盖。

## 6. 三个依赖的部署事实

| 项 | MySQL | Redis | Kafka |
|---|---|---|---|
| 镜像 | `mysql:8.0` | `redis:7-alpine` | `apache/kafka:3.9.1` |
| 来源 | 与 `docker-compose.yml` 完全一致，不升级 | 同上 | 同上（KRaft，无 ZooKeeper） |
| Service | `mysql`（3306） | `redis`（6379） | `kafka`（9092 / 29092） |
| 集群内地址 | `mysql.surgecart.svc.cluster.local:3306` | `redis.surgecart.svc.cluster.local:6379` | `kafka.surgecart.svc.cluster.local:9092` |
| 持久化 | PVC `mysql-data` 2Gi → `/var/lib/mysql` | PVC `redis-data` 1Gi → `/data`（默认 RDB） | PVC `kafka-data` 2Gi → `/var/lib/kafka/data` |
| 就绪判定 | 应用账号执行 `SELECT 1` | `PING` 返回 `PONG` | `kafka-broker-api-versions.sh` 成功 |
| 账号 | 管理员（Secret）+ 应用账号 `surgecart`（ConfigMap 的 `DATABASE_DEFAULT_USER`） | 无认证（与现有项目一致） | PLAINTEXT，无认证（本地） |

MySQL 的库名与账号不写死在清单里，而是引用 `ConfigMap/surgecart-database-config` 的
`DATABASE_DEFAULT_NAME` / `DATABASE_DEFAULT_USER` —— 与 Application、Migration 同一份事实，
不可能出现「迁移连 A 库、应用连 B 库、MySQL 创建了 C 库」。

`mysqladmin ping` 在口令错误时仍返回 0（实测），只能证明「服务端在说话」，
所以就绪探针用真正的查询，而不是 `ping`。

## 7. Kafka 的地址模型（最容易配错的地方）

`manifest/config/config.yaml` 的默认 Broker 是 `127.0.0.1:29092` —— 那是**宿主机**视角，
在 Pod 里 `127.0.0.1` 指向 Pod 自己。四个地址必须分开：

| 用途 | 值 | 说明 |
|---|---|---|
| 容器内监听（bind） | `0.0.0.0:9092 / :9093 / :29092` | 只决定从哪块网卡接受连接 |
| 集群内广播（advertised, PLAINTEXT） | `kafka.surgecart.svc.cluster.local:9092` | 客户端拿到元数据后仍能连回 |
| 宿主机访问（advertised, EXTERNAL） | `127.0.0.1:29092` | 只在 `port-forward svc/kafka 29092:29092` 时有效 |
| 控制器 quorum | `1@127.0.0.1:9093` | 单进程自连，不经过 Service（避免 hairpin NAT） |

宿主机访问：

```bash
kubectl -n surgecart port-forward svc/kafka 29092:29092
# 另一个终端：客户端连 127.0.0.1:29092（与 config.yaml 默认值一致）
```

Topic 由 `kafka-topics-init` Job 显式创建（`--if-not-exists`，1 分区、RF=1），
Broker 的 `KAFKA_AUTO_CREATE_TOPICS_ENABLE=false` 保证分区/副本数不会被客户端决定。

**残余风险（真实集群上必须复核）**：单 Broker + ClusterIP 时，Kafka 会把自己广播成
Service FQDN，客户端经 ClusterIP 回到同一个 Pod。这在标准 CNI 上可行，但依赖
hairpin/回环路径；若某集群不允许，现象是 `broker-api-versions` 成功而 produce/consume 超时。
`scripts/k8s-infra.sh verify` 的第 4 步（生产+消费）就是用来暴露这个问题的。
届时可选方案：改为 headless Service + `kafka-0.kafka.surgecart.svc.cluster.local` 广播
（需要 StatefulSet），或改用 Pod IP 广播（牺牲稳定性）。

## 8. 安全策略：restricted Pod Security

命名空间 `surgecart` 使用 `pod-security.kubernetes.io/enforce=restricted`。
三个依赖**没有**因此放宽命名空间，而是让镜像自身满足要求：

| 镜像 | 内置运行用户 | 只读根文件系统下的可写目录 | 结果 |
|---|---|---|---|
| `mysql:8.0` | `mysql` 999:999 | `/var/lib/mysql`（PVC）、`/var/run/mysqld`（emptyDir）、`/tmp`（emptyDir） | 满足 |
| `redis:7-alpine` | `redis` 999:999 | `/data`（PVC）、`/tmp`（emptyDir） | 满足 |
| `apache/kafka:3.9.1` | `appuser` 1000:1000 | `/var/lib/kafka/data`（PVC）、`/opt/kafka/config`（emptyDir）、`/opt/kafka/logs`（emptyDir）、`/tmp`（emptyDir） | 满足 |

Kafka 的两个 emptyDir 不是可选优化：镜像启动时会用环境变量生成
`/opt/kafka/config/server.properties`，并把 GC 日志写到 `/opt/kafka/logs`；
只读根文件系统下分别报 `file not writable` 与 `Read-only file system`（实测）。
它们不承载业务数据，进程重启即重建。

其余加固与 T2-04 应用容器同级：`allowPrivilegeEscalation: false`、`privileged: false`、
`capabilities.drop: [ALL]`、`seccompProfile: RuntimeDefault`、`automountServiceAccountToken: false`、
不使用 `hostPath` / `hostNetwork` / `hostPID` / `hostPort`。
`fsGroup` 与运行用户一致，保证 PVC 挂载点可写。

**没有采用的做法**：把命名空间改成 `privileged`/`baseline`、给容器加 `CAP_CHOWN`、
用 initContainer 以 root 改属主行为。这些都能让 Pod 起来，但掩盖了「镜像本身不需要 root」
这一事实。

## 9. 数据与销毁

- 三个 PVC 都由集群默认 StorageClass（kind 下为 `standard` / local-path）绑定；
  清单里不写死 `storageClassName`，换集群时不需要改文件。
- 容器重启、Pod 重建不丢数据；`bash scripts/k8s-infra.sh delete` 会删除 PVC，
  `bash scripts/k8s-cluster.sh destroy` 会删除整个集群（连带 PVC 与节点容器内的数据目录）。
- 两条删除命令都会先打印「哪些数据会丢、不可恢复」，并要求 `--yes` 或交互确认；
  非交互环境不加 `--yes` 会被拒绝（退出码非零）。
- `delete` 不会删除 Namespace 与 `ConfigMap/surgecart-database-config`：
  它们与应用、迁移入口共用，删掉等于顺手清掉应用资源。

## 10. 验证分级（不要把前两级说成第三级）

| 级别 | 命令 | 证明什么 | 不能证明什么 |
|---|---|---|---|
| 静态契约 | `bash scripts/test-k8s-infra.sh` | 清单可构建、Service/端口/安全上下文/Kafka 地址边界/迁移职责正确 | Pod 能不能跑起来 |
| 脚本行为 | `bash scripts/test-k8s-cluster.sh` | 创建幂等、状态判定、销毁确认、doctor 的失败语义 | 真实 kind 能否创建 |
| 容器级等价 | `bash scripts/test-k8s-infra-runtime.sh` | 三个镜像在 restricted 等价约束下可运行、协议交互成功 | 调度、PVC 绑定、PSS 准入、Service 路由、探针时序 |
| 真实集群 | `bash scripts/k8s-infra.sh verify` | 上述全部，含 Service DNS 与集群内生产/消费 | 业务链路（T2-08） |

只有第四级能关闭 T2-07 的验收标准 1-6。若本机不具备运行 kind 的能力，
前三级的结论仍然成立，但必须如实标记第四级为 `NOT_VERIFIED`。

## 11. 已知限制

- 单副本：没有故障切换语义，Pod 重建期间依赖不可用（应用侧靠重试与 readiness 摘流）。
- 资源规格是本地开发的保守下界（MySQL 512Mi/1Gi、Redis 128Mi/256Mi、Kafka 512Mi/1Gi），
  不是容量规划结论。Kafka 的 `KAFKA_HEAP_OPTS` 同时下调到 256m/512m，否则 JVM 默认
  1G 堆会在 1Gi limit 下被 OOMKill。
- kind 的镜像需要 `kind load docker-image` 或可访问的仓库才能被节点拉取；
  应用镜像的加载属于 T2-08。
- 生产环境的迁移、发布锁、降级保护等语义完全不受本文影响，仍以
  `docs/development/kubernetes.md` 为准。
