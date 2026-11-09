# T2-07 实施报告：本地 Kubernetes 运行基线与基础设施依赖就绪

- 任务：T2-07（本地 Kubernetes 集群 + MySQL / Redis / Kafka 集群内可用）
- 分支：`feat/k8s-t2-07-local-cluster-dependencies`
- 最终状态：**`T2-07_IMPLEMENTATION_BLOCKED`**
  （唯一阻断项：本机无法运行任何 Kubernetes 集群；静态与容器级自动化验证全部通过，
  真实集群项按任务要求标记 `NOT_VERIFIED`，未伪造）

---

## 1. Git 基线

核验命令与结果（改动前）：

```text
$ git rev-parse --abbrev-ref HEAD
feat/k8s-t2-06-1-5-rbac-resource-name-final

$ git rev-parse HEAD
9f66495576cabdd5f700edf863401f33285219c1

$ git rev-parse origin/feat/k8s-t2-06-1-5-rbac-resource-name-final
9f66495576cabdd5f700edf863401f33285219c1

$ git status --porcelain=v1 -b
## feat/k8s-t2-06-1-5-rbac-resource-name-final...origin/feat/k8s-t2-06-1-5-rbac-resource-name-final
（无文件变更 = 工作区干净）
```

与任务预期完全一致（分支、HEAD、远端 SHA 三者相同，工作区干净）。

前序成果存在性核验：

```text
$ git log --all --oneline --grep='T2-0' | wc -l
41
$ git log --all --oneline --grep='T2-02'
02c62ac feat(observability): 冻结健康检查语义并补齐 Kafka 异步状态指标（T2-02）
$ git log --all --oneline --grep='T2-05'
0386678 feat(migration): 独立数据库迁移 Job 与并发/失败恢复语义（T2-05）
$ git log --all --oneline --grep='T2-01'
fd9b4d8 feat(config): 统一配置读取契约与 Kubernetes 严格配置模式（T2-01）
```

- T2-06 / T2-06.1.1 ~ T2-06.1.5 的提交链（k8s 清单、发布锁、RBAC、资源名预检）均在
  从 HEAD 可达的历史中，41 条 T2 相关提交证明前序成果已集成；
- 分支从 `9f66495`（已核验的最终集成分支）创建，未使用 main/develop，
  未 force push，未重写历史，未修改 T2-06.1.5 分支。

新增分支：`feat/k8s-t2-07-local-cluster-dependencies`。

---

## 2. 集群技术选型及理由

### 2.1 本机实际能力（先测量，再选择）

| 工具 | 初始状态 |
|---|---|
| Docker Engine | 29.9.2-2，守护进程可用（compose v5.6.1） |
| kubectl | **未安装**（本次安装 v1.37.2，内建 kustomize v5.8.1） |
| kustomize（独立 CLI） | 未安装（GitHub Release 下载被网关 504 阻断；`kubectl kustomize` 已覆盖构建需求） |
| kind / minikube / k3d / helm | 均未安装 |

### 2.2 关键测量：本机是否可以运行集群

逐个实测，结论是**不可以**：

```text
# 特权容器（kind / minikube-docker / k3d 的硬前提）
$ docker run --rm --privileged --security-opt seccomp=unconfined alpine:3.20 sh -c 'echo ok'
docker: ... error during container init: error mounting "sysfs" to rootfs at "/sys":
mount src=sysfs ... flags=MS_NOSUID|MS_NODEV|MS_NOEXEC: operation not permitted

# 内核级能力（k3s / kubelet 直接运行的前提）
$ mount -t tmpfs tmpfs /tmp/mnttest        → permission denied
$ unshare -Urn true                        → Operation not permitted
$ unshare -n true                          → Operation not permitted
$ sysctl -w net.ipv4.ip_forward=1          → permission denied on key
$ ls /dev/fuse                             → No such file or directory
$ grep CapEff /proc/self/status            → 00000000a80c05fb（无 CAP_SYS_ADMIN / CAP_NET_ADMIN）
```

这是受限的嵌套容器环境（cgroup driver 为 `none`、无 `CAP_SYS_ADMIN`、禁止 mount/unshare）。
kind、minikube(--driver=docker)、k3d 都要求在特权容器内运行控制面，k3s 需要 mount 与
网络命名空间 —— 全部不可用。

### 2.3 选型

- **固定 kind 单节点**作为本仓库唯一的本地集群方案：一个二进制、复用已有 Docker、
  不引入 Helm/Operator，符合「不为了完成任务额外引入复杂度」。
- 不引入第二个集群工具：minikube/k3d 与 kind 解决同一问题，同时存在等于让
  「本地集群」出现多个事实来源。
- `scripts/k8s-cluster.sh doctor` 把上述前提做成**可执行的前置检查**：
  Docker 可用性、kubectl/kind 是否存在、`kubectl kustomize` 是否可用、
  Docker 是否允许特权容器。任一不满足即非零退出并说明原因，而不是等到
  `kind create` 中途报一个难以定位的 mount 错误。

> 说明：本机的 `doctor` 输出为「kind 未安装」，安装 kind 后会在本机停在
> 「privileged 不可用」这一项 —— 这正是该环境无法运行集群的事实来源。

---

## 3. 环境创建与销毁方式

脚本沿用 `scripts/` 既有体系（`lib.sh` 公共库 + 单入口子命令），没有新建平行的脚本树。

```bash
bash scripts/k8s-cluster.sh doctor     # 依赖工具 + 运行时前提检查（失败即非零退出）
bash scripts/k8s-cluster.sh create     # 创建集群（幂等：已存在则跳过，不重建）
bash scripts/k8s-cluster.sh status     # 节点 Ready / 系统组件 / StorageClass / 命名空间资源
bash scripts/k8s-cluster.sh info       # context、API、集群内 DNS、三个 port-forward 命令
bash scripts/k8s-cluster.sh destroy    # 删除集群（打印数据丢失清单 + 需要 --yes）
```

幂等与安全设计：

| 关注点 | 实现 |
|---|---|
| 重复 `create` | 先 `kind get clusters` 判断；已存在时只做状态检查，不重建、不改动 |
| 重复 `destroy` | 集群不存在时成功返回（已是目标状态） |
| 集群名 | 默认 `surgecart-local`，`K8S_CLUSTER_NAME` 可覆盖；context 固定 `kind-<name>` |
| 端口冲突 | 不指定 `apiServerPort`，由 kind 选空闲端口（避免与已有集群冲突） |
| 数据丢失告知 | `destroy` 先输出 PVC/PV 清单与「不可恢复」说明，再要求 `--yes` 或交互确认；非交互无 `--yes` → 退出码 2 |
| 销毁边界 | 只删集群；不删本地镜像、仓库文件、`.env` |

依赖的部署与销毁（独立脚本，避免把「依赖」和「应用发布」混在一起）：

```bash
bash scripts/k8s-infra.sh secrets   # 从 .env 创建 Secret（不回显、不落盘）
bash scripts/k8s-infra.sh create    # 应用清单 + 等待就绪 + Topic 初始化
bash scripts/k8s-infra.sh status    # Deployment/Job 就绪性
bash scripts/k8s-infra.sh verify    # 集群内真实验证（三类 Job）
bash scripts/k8s-infra.sh delete    # 删除依赖（PVC 数据丢失，需要 --yes）
```

`delete` 逐个点名资源而不是 `kubectl delete -k`：后者会连 Namespace 与
`ConfigMap/surgecart-database-config` 一起删掉（它们与应用/迁移入口共用）。

---

## 4. MySQL 部署与验证

**部署事实**（`manifest/kubernetes/infrastructure/base/mysql.yaml`）

| 项 | 值 | 依据 |
|---|---|---|
| 镜像 | `mysql:8.0` | 与 `docker-compose.yml` 完全一致，未升级 |
| 端口 | 3306，Service `mysql`（ClusterIP） | 与 compose 端口一致 |
| 库名 | `ConfigMap/surgecart-database-config` → `DATABASE_DEFAULT_NAME` | 与 Application/Migration 同一份事实 |
| 应用账号 | 同 ConfigMap → `DATABASE_DEFAULT_USER`（`surgecart`） | 与 K8s 既有清单一致，非 root |
| 口令 | Secret `surgecart-db-credentials`：`MYSQL_ROOT_PASSWORD`、`DATABASE_DEFAULT_PASS` | 不落 Git |
| 持久化 | PVC `mysql-data` 2Gi → `/var/lib/mysql`（RWO，Recreate） | 本地单副本 |
| 可写目录 | `/var/run/mysqld`（emptyDir）、`/tmp`（emptyDir） | 只读根文件系统 |
| 运行身份 | `runAsUser/Group 999`、`fsGroup 999` | 镜像内置 `mysql` 用户（实测 uid=999） |
| Readiness | 应用账号执行 `SELECT 1` | 见下 |
| 资源 | requests 100m/512Mi，limits 1/1Gi | 本地开发下界 |

**Readiness 选择依据（实测）**：

```text
mysqladmin ping --silent -h 127.0.0.1 -u root -pwrong   → exit 0（仍报「服务端在说话」）
```

因此 `mysqladmin ping` 不能判定「应用能连库」。Readiness 改为真实的
`mysql -e "SELECT 1"`（口令经 `MYSQL_PWD` 传入，不进 argv）；
Liveness 仍用 `mysqladmin ping`（只判定协议应答，避免凭据问题引发重启循环）。

**验证结果**

| 验证项 | 结果 | 证据 |
|---|---|---|
| 镜像在 restricted 约束下启动 | PASS | 容器级等价验证：uid 999、只读根文件系统、cap 全丢弃下 6s 就绪 |
| 应用账号被正确创建且可 DDL | PASS | `SHOW GRANTS` → `GRANT ALL PRIVILEGES ON my_shop.*`；`CREATE/DROP TABLE` 成功 |
| 迁移所需 `GET_LOCK` | PASS | `SELECT GET_LOCK("x",1)` → 1 |
| 集群内 `SELECT 1` | **NOT_VERIFIED** | 需真实集群 |
| PVC 绑定 / fsGroup 授权 | **NOT_VERIFIED** | 需真实集群（kubelet 行为） |

---

## 5. Redis 部署与验证

| 项 | 值 | 依据 |
|---|---|---|
| 镜像 | `redis:7-alpine` | 与 compose 一致 |
| 端口 | 6379，Service `redis`（ClusterIP） | 一致 |
| 认证 | **不启用**（与现有项目一致：`REDIS_DEFAULT_PASS` 为空） | 「不为 Kubernetes 改变原有 Redis 业务语义」 |
| 持久化 | PVC `redis-data` 1Gi → `/data`，默认 RDB | 与 compose 的 named volume 语义一致；不加 AOF |
| 运行身份 | 999:999，`fsGroup 999` | 镜像内置 `redis` 用户（实测 uid=999 gid=1000，以 999 作主组亦可） |
| 探针 | readiness/liveness 均断言 `PING == PONG` | 端口可连 ≠ 可用 |
| 资源 | requests 50m/128Mi，limits 500m/256Mi | |

**验证结果**

| 验证项 | 结果 | 证据 |
|---|---|---|
| restricted 约束下启动 | PASS | 容器级等价验证 0s 就绪 |
| `PING → PONG` | PASS | `[verify] PING -> PONG` |
| 集群内 PING | **NOT_VERIFIED** | 需真实集群 |

---

## 6. Kafka 部署与验证

| 项 | 值 |
|---|---|
| 镜像 | `apache/kafka:3.9.1`（与 compose 一致） |
| 模式 | KRaft 单节点，`KAFKA_PROCESS_ROLES=broker,controller`，**无 ZooKeeper** |
| Service | `kafka`：`plaintext` 9092、`external` 29092（ClusterIP） |
| 运行身份 | 1000:1000（镜像内置 `appuser`），`fsGroup 1000` |
| 可写目录 | `/var/lib/kafka/data`（PVC）、`/opt/kafka/config`、`/opt/kafka/logs`、`/tmp`（emptyDir） |
| Topic | `kafka-topics-init` Job 显式创建（`--if-not-exists`，1 分区 / RF=1） |
| 资源 | requests 200m/512Mi，limits 1/1Gi；`KAFKA_HEAP_OPTS=-Xms256m -Xmx512m` |

### 6.1 地址模型（本次最容易出错的地方）

`config.yaml` 默认 `127.0.0.1:29092` 是宿主机视角；Pod 内的 `127.0.0.1` 指向 Pod 自己。
四个地址显式分离（`ConfigMap/surgecart-kafka-config`）：

| 用途 | 值 |
|---|---|
| 容器内监听 | `PLAINTEXT://0.0.0.0:9092`、`CONTROLLER://0.0.0.0:9093`、`EXTERNAL://0.0.0.0:29092` |
| 集群内广播 | `PLAINTEXT://kafka.surgecart.svc.cluster.local:9092` |
| 宿主机广播 | `EXTERNAL://127.0.0.1:29092`（配合 `kubectl port-forward svc/kafka 29092:29092`） |
| 控制器 quorum | `1@127.0.0.1:9093`（单进程自连，不经过 Service） |

应用侧：`ConfigMap/surgecart-config` 新增 `FLASH_SALE_KAFKA_BROKERS=kafka.surgecart.svc.cluster.local:9092`，
不使用回环地址。当前 `FLASH_SALE_DISPATCH_MODE=db_scanner` + `FLASH_SALE_KAFKA_ENABLED=false`，
该地址不会被连接（打开开关时也不会连错地方）。

### 6.2 验证结果

| 验证项 | 结果 | 证据 |
|---|---|---|
| restricted 约束下启动（含两个可写 emptyDir） | PASS | 容器级等价验证就绪；`/opt/kafka/config` 与 `/opt/kafka/logs` 缺一即启动失败（实测） |
| Broker 元数据 | PASS | `kafka-broker-api-versions.sh` 返回 `kafka:9092 (id: 1 ...) -> (Produce(0): 0 to 11 ...)` |
| Topic 创建 | PASS | `surgecart.flashsale.order.requested.v1`（PartitionCount 1 / RF 1）、`.dlq` |
| 生产 + 消费 | PASS | 唯一标记 `verify-kafka-1794213189-1` 生产后原样消费回读 |
| 自动建 Topic 关闭 | PASS | `KAFKA_AUTO_CREATE_TOPICS_ENABLE=false`，探针 Topic 未被自动创建 |
| 集群内生产消费 / Service FQDN 广播 | **NOT_VERIFIED** | 需真实集群（见 §12 风险 R-1） |

---

## 7. Service DNS

三个 Service 与 Pod 标签、命名端口一一对应（由 `TestInfrastructureServicesResolveToPods` 断言）：

| 依赖 | Service | 端口（name → port / targetPort） | 集群内 FQDN |
|---|---|---|---|
| MySQL | `mysql` | `mysql` → 3306 / `mysql` | `mysql.surgecart.svc.cluster.local:3306` |
| Redis | `redis` | `redis` → 6379 / `redis` | `redis.surgecart.svc.cluster.local:6379` |
| Kafka | `kafka` | `plaintext` → 9092 / `plaintext`；`external` → 29092 / `external` | `kafka.surgecart.svc.cluster.local:9092` |

命名空间统一 `surgecart`（与 Application/Migration 共用 `shared/namespace.yaml`），
Service 名与 `docker-compose.yml` 的服务名一致，因此 `database-configmap.yaml` 中既有的
`mysql.surgecart.svc.cluster.local` 无需修改即成立。

宿主机访问一律经 `kubectl port-forward`（端口与 compose 一致：3306 / 6379 / 29092），
不使用 NodePort/LoadBalancer，避免把本地开发伪装成对外服务。

---

## 8. 安全策略（restricted PSS）

命名空间 `surgecart` 的 `pod-security.kubernetes.io/enforce=restricted` **未做任何放宽**。
逐个镜像检查实际运行用户后的结论：

| 镜像 | 内置用户（实测） | 是否需要 root | 只读根文件系统下的可写目录 | 结论 |
|---|---|---|---|---|
| `mysql:8.0` | `mysql` 999:999 | 否（直接以 999 启动，initdb 正常） | `/var/lib/mysql`、`/var/run/mysqld`、`/tmp` | 满足 |
| `redis:7-alpine` | `redis` 999:1000 | 否 | `/data`、`/tmp` | 满足 |
| `apache/kafka:3.9.1` | `appuser` 1000:1000 | 否 | `/var/lib/kafka/data`、`/opt/kafka/config`、`/opt/kafka/logs`、`/tmp` | 满足（需两个额外 emptyDir） |

逐项处理结果：

| 检查项 | MySQL | Redis | Kafka |
|---|---|---|---|
| `runAsNonRoot: true` | ✔ | ✔ | ✔ |
| `runAsUser/runAsGroup` | 999:999 | 999:999 | 1000:1000 |
| `allowPrivilegeEscalation` | false | false | false |
| `privileged` | false | false | false |
| `capabilities.drop` | ALL | ALL | ALL |
| `seccompProfile` | RuntimeDefault | RuntimeDefault | RuntimeDefault |
| `readOnlyRootFilesystem` | true | true | true |
| `fsGroup` / `fsGroupChangePolicy` | 999 / OnRootMismatch | 999 / OnRootMismatch | 1000 / OnRootMismatch |
| `hostPath` / `hostNetwork` / `hostPID` / `hostPort` | 无 | 无 | 无 |
| `automountServiceAccountToken` | false | false | false |

关键证据（Kafka 为什么需要两个 emptyDir，实测报错）：

```text
# 只读根文件系统、config 不可写
/opt/kafka/config/ file not writable
# 补上 config 后，GC 日志目录仍不可写
mkdir: cannot create directory '/opt/kafka/bin/../logs': Read-only file system
Error: Could not create the Java Virtual Machine.
```

两者都不承载业务数据（`server.properties` 每次启动由环境变量重新生成），
因此用 emptyDir 而不是关闭 `readOnlyRootFilesystem`。

**明确没有做的事**：把 Namespace 改成 `privileged`/`baseline`、给容器加 `CAP_CHOWN`、
用 root initContainer 改属主。这些都只是把真实问题藏起来。

---

## 9. 配置与 Secret 设计

| 资源 | 内容 | 为什么在这里 |
|---|---|---|
| `ConfigMap/surgecart-database-config`（shared，既有） | 类型/主机/端口/用户/库名/字符集 + 严格模式开关 | 三个入口共用，库事实唯一 |
| `ConfigMap/surgecart-config`（既有，本次新增 1 键） | 应用运行期配置 + `FLASH_SALE_KAFKA_BROKERS`（Service DNS） | 集群内地址属于部署差异 |
| `ConfigMap/surgecart-kafka-config`（新增） | Broker 属性（`CLUSTER_ID`/`LISTENERS`/`ADVERTISED_LISTENERS`/副本因子/堆）+ Topic 名称 + bootstrap | Kafka 容器与 Topic Job 同源 |
| `Secret/surgecart-db-credentials`（既有结构，本次补键） | `DATABASE_DEFAULT_PASS`、`MYSQL_ROOT_PASSWORD` | 唯一存放数据库口令的地方 |

设计要点：

- **主题名称不带 `KAFKA_` 前缀**：`apache/kafka` 镜像会把所有 `KAFKA_*` 环境变量写成
  broker 属性（实测 `KAFKA_FOO=bar` 会生成 `foo=bar`），Topic 名带前缀会污染
  `server.properties`。因此 `TOPIC_ORDER_REQUESTED*` 用无前缀键名。
- **不提交任何真实凭据**：`secrets` 子命令从 `.env`/环境变量取值，经 0700 临时目录与管道
  写入 Secret，不打印、不落仓库；缺值直接失败并指出缺哪个键，不生成默认口令。
- **更新 Secret 用 `replace` 而不是 `apply`**：`apply` 会把口令再写进
  `last-applied-configuration` 注解，等于同一份口令存两遍。
- **模板只含占位符**：`manifest/kubernetes/examples/secrets/database-secret.example.yaml`
  补齐 `MYSQL_ROOT_PASSWORD`，由 `TestInfrastructureConfigMapsCarryNoSecrets` 断言
  构建产物中不出现 `REPLACE_WITH`、ConfigMap 不含 `PASSWORD/SECRET/TOKEN/*_KEY`。
- `.env.example` 补充 `DATABASE_DEFAULT_PASS`（说明它只用于 Kubernetes 流程；
  本机 `.env` 尚未包含该键，首次执行 `secrets` 会明确报错提示补齐）。

MySQL 容器使用 `configMapKeyRef`/`secretKeyRef` 引用上述两份资源，因此
「MySQL 创建了哪个库/哪个账号」与「应用连哪个库/哪个账号」不可能不一致
（`TestInfrastructureMatchesApplicationConfig` 断言）。

---

## 10. 迁移职责处理

### 10.1 问题复核（事实链）

1. 镜像 `ENTRYPOINT=["/app/docker-entrypoint.sh"]`、`Cmd=null`（`docker image inspect` 实测）；
2. 该脚本语义是 `my-shop migrate up` → `exec my-shop serve`（镜像内实际内容已读取）；
3. 修改前的 `Deployment/surgecart` **没有覆盖 ENTRYPOINT**，因此每个应用 Pod 启动都会
   先执行一次 `migrate up` —— 与 T2-05「迁移只由独立 Job 执行」冲突。

### 10.2 修复

`manifest/kubernetes/base/deployment.yaml` 为应用容器显式声明：

```yaml
command: ["/app/my-shop"]
args: ["serve"]
```

Kubernetes 语义上 `command` 覆盖 ENTRYPOINT、`args` 覆盖 CMD，因此容器内不会出现
`docker-entrypoint.sh`，也不会执行 `migrate`。

### 10.3 证明（静态 + 动态）

| 证明方式 | 结果 |
|---|---|
| `TestApplicationWorkloadsNeverRunMigrations`（构建产物断言） | PASS：Application 容器 `command=["/app/my-shop"]`、`args=["serve"]`；四个入口的所有容器都不出现 `docker-entrypoint.sh`；只有 Migration Job 的 `args` 含 `migrate up`；Application 入口无 Job |
| 迁移 Job 契约（既有测试 + 本次交叉断言） | PASS：`command=["/app/my-shop"]`、`args=["migrate up"]`，无 `force` |
| 真实镜像语义（动态） | PASS：`--entrypoint /app/my-shop <img> arg-should-fail` → `ERROR: command "arg-should-fail" not found for command "my-shop"`，说明入口脚本（含 `migrate up`）被完全绕过 |
| 运行时门禁 `test-container-runtime` PASS[11] | PID 1 = `/app/my-shop serve`，无 shell 中间层、无 migrate 子进程 |

### 10.4 关于 `publish` / `consume` 角色的偏差说明（需 Owner 知悉）

任务第八节要求「`serve`、`publish`、`consume` 能通过同一镜像的不同启动参数运行」。
经代码核验，当前二进制**没有** `publish` / `consume` 子命令：

```text
internal/cmd/cmd.go: Usage: my-shop [serve|migrate <up|force|version>|qiniu <check>|admin <check>|legacy-migrate ...]
internal/worker/flashsale_runtime.go: StartFlashSaleRuntime → Publisher / Consumer / Scanner 由
  FLASH_SALE_DISPATCH_MODE（db_scanner | kafka）在 serve 进程内装配
docs/development/kubernetes.md: 无 publish/consume 角色的任何描述
```

因此现状是「单进程多角色」，而不是「三个可独立启动的角色」。新增这两个子命令需要把
worker 内部组件拆成独立入口，属于**运行架构变更**，与本次「不重构业务架构、
不修改与本任务无关的业务逻辑」冲突，本次**未实施**，按任务要求在此显式报告。

已完成的机制性保障：
- 所有工作负载都显式声明 `command`，不依赖镜像 ENTRYPOINT（同一镜像可用不同参数启动）；
- 迁移职责已与「启动哪个角色」解耦：任何角色都无法通过 ENTRYPOINT 触发迁移。

若 Owner 决定在 T2-08 之前引入独立角色，需要单独评估关闭顺序（T2-03 冻结的
Publisher → Consumer → Producer → Scanner 编排）与就绪/存活语义。

---

## 11. 修改文件清单

新增（19 个）：

```text
manifest/kubernetes/infrastructure/base/kustomization.yaml
manifest/kubernetes/infrastructure/base/configmap.yaml
manifest/kubernetes/infrastructure/base/mysql.yaml
manifest/kubernetes/infrastructure/base/redis.yaml
manifest/kubernetes/infrastructure/base/kafka.yaml
manifest/kubernetes/infrastructure/base/kafka-topics-job.yaml
manifest/kubernetes/infrastructure/verify/kustomization.yaml
manifest/kubernetes/infrastructure/verify/mysql-verify.yaml
manifest/kubernetes/infrastructure/verify/redis-verify.yaml
manifest/kubernetes/infrastructure/verify/kafka-verify.yaml
scripts/kind-config.yaml
scripts/k8s-cluster.sh
scripts/k8s-infra.sh
scripts/test-k8s-infra.sh
scripts/test-k8s-infra-runtime.sh
scripts/test-k8s-cluster.sh
test/manifest/infrastructure_test.go
docs/development/local-infrastructure.md
T2-07_IMPLEMENTATION_REPORT.md
```

修改（7 个）：

```text
manifest/kubernetes/base/deployment.yaml                      # command/args 覆盖 ENTRYPOINT（迁移职责）
manifest/kubernetes/base/configmap.yaml                       # FLASH_SALE_KAFKA_BROKERS（Service DNS）
manifest/kubernetes/examples/secrets/database-secret.example.yaml  # 补 MYSQL_ROOT_PASSWORD 占位
docs/development/kubernetes.md                                # 范围与目录结构指向本地依赖
scripts/test.sh                                               # 纳入两个 T2-07 门禁
Makefile                                                      # test-k8s-infra / test-k8s-cluster / test-k8s-infra-runtime
.env.example                                                  # DATABASE_DEFAULT_PASS 说明（仅为键名，无值）
```

未修改：任何 Go 业务代码、Dockerfile、docker-compose.yml、T2-06.1.x 发布锁与 RBAC。

---

## 12. 自动化测试结果（真实退出码）

| 门禁 | 命令 | 退出码 | 说明 |
|---|---|---|---|
| 格式 | `gofmt -l .` | 0（输出为空） | — |
| 构建 | `go build ./...` | 0 | — |
| 静态检查 | `go vet ./...` | 0 | — |
| 单元 | `bash scripts/test-unit.sh` | 0 | 全部通过 |
| 发布脚本行为 | `bash scripts/test-deploy-k8s.sh` | 0 | 未受本次清单改动影响 |
| 清单体系 | `bash scripts/test-k8s-manifest.sh` | 0 | 既有 28+ 用例 + 本次 9 个新用例 |
| 容器化静态 | `bash scripts/test-container.sh` | 0 | — |
| 容器运行时 | `bash scripts/test-container-runtime.sh` | 0 | 16 项断言全 PASS |
| **基础设施清单（新）** | `bash scripts/test-k8s-infra.sh` | 0 | 构建两个入口 + 9 个 Go 契约测试 + 3 项脚本行为断言 |
| **集群脚本行为（新）** | `bash scripts/test-k8s-cluster.sh` | 0 | 9 项断言（幂等/状态/销毁确认/doctor 失败语义） |
| **容器级等价（新）** | `bash scripts/test-k8s-infra-runtime.sh` | 0 | 三个镜像在 restricted 等价约束下运行并完成协议交互 |

新增静态契约测试（`test/manifest/infrastructure_test.go`，均作用于 `kubectl kustomize` 构建产物）：

| 用例 | 断言内容 |
|---|---|
| `TestInfrastructureEntrypointShape` | 资源种类/数量、不含应用资源、镜像版本固定、不含 Secret |
| `TestInfrastructureServicesResolveToPods` | selector 命中 Pod 标签、ClusterIP、命名端口与端口号 |
| `TestInfrastructurePodSecurityRestricted` | 依赖与验证 Pod 的 Pod/容器两级安全上下文、禁用面、requests/limits |
| `TestKafkaListenerContract` | KRaft 无 ZooKeeper、监听器/广播/控制器地址边界、自动建 Topic 关闭、日志目录与挂载一致 |
| `TestInfrastructureMatchesApplicationConfig` | 库名/账号/Redis 地址/Kafka bootstrap 跨入口同值 |
| `TestInfrastructureConfigMapsCarryNoSecrets` | ConfigMap 无敏感键、构建产物无占位符、Secret 模板完整 |
| `TestApplicationWorkloadsNeverRunMigrations` | ENTRYPOINT 事实链 + 应用容器不迁移 + 迁移 Job 唯一入口 |
| `TestVerificationJobsProveProtocols` | 验证脚本含协议命令、backoffLimit=0、restartPolicy=Never |
| `TestInfrastructureImageTagsArePinned` | 所有镜像 tag 非空且非 latest |

**未安装 kubeconform**：API Schema 校验仍为跳过（脚本打印 `NOT_VERIFIED`），
与基线一致，未降低任何断言。

---

## 13. 真实 Kubernetes 验证结果

```text
集群创建                    NOT_VERIFIED（本机 Docker 不允许特权容器，kind/k3d/minikube 均不可用）
kubectl 访问                NOT_VERIFIED
Namespace / Kustomize 构建   构建：PASS（kubectl kustomize 退出码 0）
                            server-side dry-run：NOT_VERIFIED（无集群）
MySQL Pod Ready / Service   NOT_VERIFIED
MySQL 集群内 SELECT 1        NOT_VERIFIED
Redis Pod Ready / Service   NOT_VERIFIED
Redis 集群内 PING            NOT_VERIFIED
Kafka Pod Ready / Service   NOT_VERIFIED
Kafka 元数据 / Topic / 生产消费  NOT_VERIFIED
Service DNS（集群内）        NOT_VERIFIED
PVC 绑定 / fsGroup 授权      NOT_VERIFIED
restricted PSS 准入          NOT_VERIFIED
探针时序                     NOT_VERIFIED
```

为把「未验证」压到最小，额外执行了**容器级等价验证**
（`scripts/test-k8s-infra-runtime.sh`，Docker 复刻同一组约束）：

```text
MySQL 已就绪（6s）→ SELECT 1 → MYSQL_VERIFY_OK
Redis 已就绪（0s）→ PING -> PONG → REDIS_VERIFY_OK
Kafka 已就绪（0s）→ 建 Topic → 生产 → 消费 → KAFKA_VERIFY_OK (verify-kafka-1794213189-1)
自动建 Topic 关闭 → 未出现未显式创建的 Topic
```

它证明：镜像在非 root / 只读根文件系统 / cap 全丢弃 / no-new-privileges 下可运行，
端口、账号、监听器、广播地址与客户端命令都是对的。
它**不能**替代：调度、PVC 绑定、PSS 准入、Service/Endpoint 路由、探针时序。

在具备特权 Docker 的机器上，关闭全部 `NOT_VERIFIED` 只需三步：

```bash
bash scripts/k8s-cluster.sh create
bash scripts/k8s-infra.sh secrets && bash scripts/k8s-infra.sh create
bash scripts/k8s-infra.sh verify
```

---

## 14. 剩余风险

| 编号 | 风险 | 影响 | 处理 |
|---|---|---|---|
| R-1 | 单 Broker + ClusterIP：广播 `kafka.surgecart.svc.cluster.local:9092`，客户端经 ClusterIP 回到同一 Pod，依赖 hairpin/回环路径 | 某些 CNI 下表现为 `broker-api-versions` 成功但 produce/consume 超时 | `verify` 第 4 步（生产+消费）就是探针；失败时改用 headless Service + `kafka-0.kafka...`（需 StatefulSet）或 Pod IP 广播 |
| R-2 | 基础设施清单的 PVC 不指定 `storageClassName`，依赖集群默认 StorageClass | 无默认 StorageClass 的集群会 Pending | `k8s-cluster.sh status` 打印 StorageClass；kind 自带 `standard` |
| R-3 | 本地依赖为单副本、无 HA | Pod 重建期间依赖不可用 | 应用侧由 readiness 摘流 + 启动重试兜底；本阶段不追求可用性 |
| R-4 | 容器级等价验证的 uid/挂载点与清单同源但**未自动校验**（脚本硬编码） | 清单改动后证据可能失真 | 清单侧由 `TestInfrastructurePodSecurityRestricted` 断言；如调整运行身份需同步脚本并重跑 |
| R-5 | 本机 `.env` 缺 `DATABASE_DEFAULT_PASS` | `k8s-infra.sh secrets` 首次执行会失败 | 脚本明确提示补齐；`.env.example` 已补充说明 |
| R-6 | `publish` / `consume` 不是独立入口（见 §10.4） | 与任务第八节的字面要求存在偏差 | 已在报告中披露；新增角色属于架构变更，未擅自实施 |
| R-7 | kubeconform 未安装 | API Schema 校验跳过 | 与基线一致，未降低断言；安装后脚本自动生效 |

---

## 15. Commit 与远端

| 项 | 值 |
|---|---|
| 基线分支 | `feat/k8s-t2-06-1-5-rbac-resource-name-final` |
| 基线 HEAD | `9f66495576cabdd5f700edf863401f33285219c1` |
| 工作分支 | `feat/k8s-t2-07-local-cluster-dependencies` |
| 实现提交 | `b6913208071a5038da8f830c1cc4bcb494c94d20`（`b691320`，25 个文件） |
| 报告提交 | `287d3d1ea95ad564bba1981dc76069369716dd7a`（`287d3d1`，本文件首次提交） |
| SHA 回填提交 | 见 `git log --oneline -1`（仅把上两行 SHA 写进报告，无代码变更） |
| 远端 SHA | 推送后与本地 HEAD 相同；核验：`git rev-parse origin/feat/k8s-t2-07-local-cluster-dependencies` |

推送后核验命令：

```bash
git rev-parse HEAD
git rev-parse origin/feat/k8s-t2-07-local-cluster-dependencies
git status --porcelain   # 空 = 工作区干净
```

---

## 16. 最终状态

**`T2-07_IMPLEMENTATION_BLOCKED`**

阻断原因只有一条，且不属于实现缺陷：**当前开发机是无 `CAP_SYS_ADMIN`、禁止 mount/unshare
的嵌套容器环境，无法运行任何 Kubernetes 集群（kind / minikube / k3d / k3s 全部不可用）**。

因此验收标准 3、4、5、6（集群内 MySQL/Redis/Kafka 可用与 Service DNS）无法在本机完成，
已如实标记 `NOT_VERIFIED`，未用静态校验或容器级验证冒充集群验证。

已经完成并可独立验收的部分：

1. 集群环境管理脚本（doctor/create/status/info/destroy）及其幂等性与数据丢失告知；
2. 三个依赖的 Kustomize 清单（固定版本、配置/Secret 分离、Service DNS、持久化、探针、资源）；
3. restricted PSS 兼容性（不改安全策略，逐镜像实测运行用户与可写目录）；
4. 迁移职责修复（应用工作负载不再执行 `migrate up`），静态 + 动态双证；
5. 全部门禁通过（gofmt / build / vet + 8 个脚本门禁；含 9 个新契约测试、
   2 个新脚本门禁与 1 个容器级等价验证）；
6. 文档与可复现的验证路径（在有特权 Docker 的机器上三条命令即可关闭全部 `NOT_VERIFIED`）。

T2-07 到此停止，不进入 T2-08。
