# T3-01 本地 Kubernetes 集群与基础设施部署验证报告

- 项目：SurgeCart（`cnb.cool/framework-station/my-shop`）
- 任务：T3-01 — 本地 Kubernetes 集群与基础设施部署、验证
- 分支：`feat/k8s-t3-01-local-kubernetes-validation`
- 基线：`ffc0bddf805b670bc34ef39209dd0cf73469dbb5`（`feat/k8s-t2-07-local-cluster-dependencies` 的 HEAD，与历史交付记录一致）
- 结论：**T3-01 = COMPLETED**（全部验收项在真实 kind 集群上通过）

---

## 1. 执行摘要

| 项 | 实际结果 |
|---|---|
| 集群方案 | kind v0.33.0（单节点 control-plane，无端口映射） |
| 集群名 / context | `surgecart-local` / `kind-surgecart-local` |
| Kubernetes 版本 | **v1.37.0**（节点镜像 `kindest/node:v1.37.0`，containerd 2.3.4） |
| Node 状态 | `surgecart-local-control-plane` **Ready**（1/1） |
| MySQL | Deployment 1/1 Ready，PVC `Bound`，`SELECT 1` 返回 `1`，`MYSQL_VERIFY_OK` |
| Redis | Deployment 1/1 Ready，`PING` 返回 `PONG`，`REDIS_VERIFY_OK` |
| Kafka | Deployment 1/1 Ready，KRaft 单节点（Leader=1），生产→消费回读一致，`KAFKA_VERIFY_OK` |
| Topic 初始化 Job | `kafka-topics-init` **Complete**，两个业务 Topic 实际存在 |
| Service / DNS / 网络 | 3 个 ClusterIP Service、Endpoints/EndpointSlices 正常；三个 FQDN 集群内可解析、端口可连接 |
| 最终验收 | 全部 14 项验收项通过（见第 12 节） |

本次在**真实 Kubernetes API Server / Pod / PVC / Service / CoreDNS** 上完成了 T2-07 遗留的 `NOT_VERIFIED` 部分：此前只有静态门禁与 Docker 等价运行，本轮首次获得真实集群运行证据。

期间发现并修复了 **2 个真实缺陷**（均为最小必要修复，且各自补充了回归断言）：

1. `Job/verify-kafka` 用 `grep -qx`（正则）比对含 `$` 的消息标记，导致「消息存在」被误判为「未消费到」；
2. `doctor` 的特权容器探测在 Windows/Git Bash 下被 MSYS 参数转换改写容器内路径 `/bin/true`，把「Docker 允许特权」误判为「特权不可用」。

另有一项 Windows 检出兼容修复：新增 `.gitattributes`（`text=auto eol=lf`），避免 `core.autocrlf=true` 把 shell 脚本与清单检出为 CRLF 后无法执行、并让 Manifest 门禁的正则断言假失败。

---

## 2. 环境信息

| 项 | 值 |
|---|---|
| 宿主机 | Windows 11 专业版 10.0.26300（x64，物理机） |
| Shell | PowerShell 5.1 + Git Bash（MSYS bash 5.2.37） |
| Docker | Client/Server 均为 **29.9.3**，Context `desktop-linux`，WSL2 后端，overlay2，cgroup v2 |
| Docker VM | 20 CPU / 约 15.4 GiB |
| kubectl | Client **v1.36.1**（Docker Desktop 自带）/ Server **v1.37.0** |
| kind | **v0.33.0**（安装于 `%LOCALAPPDATA%\Programs\kind\kind.exe`，SHA256 已与官方 `kind-windows-amd64.sha256sum` 校验一致） |
| Go | 宿主机 go1.24.4 windows/amd64；容器复核使用 `golang:1.26`（go1.26.10） |
| 集群名 | `surgecart-local` |
| 节点 | `surgecart-local-control-plane`，Ready，`Debian GNU/Linux 13 (trixie)`，kubelet v1.37.0 |
| API Server | `https://127.0.0.1:14342`（kind 自行分配的端口） |
| StorageClass | `standard`（`rancher.io/local-path`，默认，`WaitForFirstConsumer`） |

未修改 Docker Desktop 配置、未迁移数据目录、未改动其他项目容器（`code-im-*` 全程未触碰）。

---

## 3. Git 信息

| 项 | 值 |
|---|---|
| 仓库路径 | `D:\Wenqingsong\CodeProjects\codebuddy\k8s` |
| 基线 SHA | `ffc0bddf805b670bc34ef39209dd0cf73469dbb5` |
| 当前分支 | `feat/k8s-t3-01-local-kubernetes-validation`（基于历史分支 HEAD 创建，无分叉、无改写） |
| 最终 HEAD | 见第 13 节（由提交后回填） |
| 工作区状态 | 干净（`.env` 与临时诊断文件均在仓库外/被忽略） |

远端核验：`git ls-remote` 确认 `refs/heads/feat/k8s-t2-07-local-cluster-dependencies` 的 SHA 与历史记录完全一致（`ffc0bdd…`），且该分支**不是** `main` 的祖先（未合并），因此本次选择以它为基线，未从 `main` 另起一套。

克隆/推送时按任务要求**只在单条命令内**用 `-c http.proxy= -c https.proxy=` 绕过失效代理（`127.0.0.1:18081` 无监听），未修改任何全局 Git 配置。

---

## 4. 实现变更

| 文件 | 原因 | 变更 | 对应测试 |
|---|---|---|---|
| `manifest/kubernetes/infrastructure/verify/kafka-verify.yaml` | 真实集群暴露：`MARKER` 结尾为 `$$`，作为正则时尾部 `$` 被当作行尾锚点，`grep -qx` 对「已消费到」的消息判定失败（Job 变 Failed） | `grep -qx` → `grep -qxF`（固定字符串匹配），并加注释说明 | `test/manifest/infrastructure_test.go` 新增 2 条断言（必须用 `-qxF`、禁止 `-qx`）；`bash scripts/k8s-infra.sh verify` 真实通过 |
| `scripts/k8s-cluster.sh` | Windows/Git Bash 下 MSYS 把容器内 `/bin/true` 改写成 `D:/.../usr/bin/true`，`doctor` 误报「特权不可用」 | 特权探测调用加 `MSYS2_ARG_CONV_EXCL='*'`（仅作用于该条 docker 命令，Linux/macOS 无副作用） | `scripts/test-k8s-cluster.sh` 新增 2 个用例：断言隔离 MSYS 转换、断言「Docker 不允许特权时 doctor 必须失败」 |
| `scripts/test-k8s-cluster.sh` | 为上述修复补回归门禁 | 新增 2 个 doctor 用例（见上） | 自身即门禁 |
| `test/manifest/infrastructure_test.go` | 为 Kafka 验证断言补回归门禁 | 在 `TestVerificationJobsProveProtocols` 中新增 2 条断言 | `bash scripts/test-k8s-infra.sh` |
| `.gitattributes`（新增） | `core.autocrlf=true` 让所有文本文件检出为 CRLF：shell 脚本无法执行、Manifest 门禁正则假失败 | `* text=auto eol=lf`（不改变已提交内容，索引本来就是 LF） | 全仓 `git ls-files --eol` 无 `w/crlf`；三个门禁在 Windows 下通过 |

未修改：Go 业务代码、秒杀逻辑、数据库 Schema、Redis Lua、Kafka 消费架构、应用 Deployment、Secret/PSS 策略、迁移职责设计。

---

## 5. 集群创建证据

```
$ bash scripts/k8s-cluster.sh doctor
  docker    : 29.9.3
  kubectl   : v1.36.1
  kind      : kind v0.33.0 go1.26.7 windows/amd64
  kustomize : kubectl 内建（kubectl kustomize 可用）
  privileged: 可用（kind 控制面容器可以创建）
[INFO] 环境可用于创建本地集群

$ bash scripts/k8s-cluster.sh create
Creating cluster "surgecart-local" ...
 • Ensuring node image (kindest/node:v1.37.0) 🖼️  ...
 ✓ Ensuring node image (kindest/node:v1.37.0) 🖼️
 ✓ Preparing nodes 📦
 ✓ Writing configuration 📜
 ✓ Starting control-plane 🕹️
 ✓ Installing CNI 🔌
 ✓ Installing StorageClass 💾
 ✓ Waiting ≤ 5m0s for control-plane = Ready ⏳
 • Ready after 16s 💚
Set kubectl context to "kind-surgecart-local"

$ kubectl get nodes -o wide
NAME                            STATUS   ROLES           AGE   VERSION   INTERNAL-IP   CONTAINER-RUNTIME
surgecart-local-control-plane   Ready    control-plane   22s   v1.37.0   172.19.0.2    containerd://2.3.4
```

- `kubectl cluster-info` → `Kubernetes control plane is running at https://127.0.0.1:14342`，CoreDNS 可达。
- `kubectl config current-context` → `kind-surgecart-local`。
- 默认 StorageClass 存在：`standard (default) rancher.io/local-path Delete WaitForFirstConsumer`。
- `bash scripts/k8s-cluster.sh create` 重复执行只做状态检查（幂等，不重建）。

#### 5.1 部署过程中的一次真实波折（如实记录）

首次 `bash scripts/k8s-infra.sh create` 在 MySQL 等待窗口（420s）内超时失败：

```
[ERROR] Deployment/mysql 未在 420s 内就绪
[ERROR] MySQL 未就绪
```

原因经证据确认：**不是**配置或权限问题，而是首次拉取镜像耗时超过等待窗口。事件时间线显示 `mysql:8.0` 镜像拉取耗时 `6m39.909s`，容器在 420s 窗口之后才真正启动；MySQL 启动日志本身正常（`ready for connections ... port: 3306`），随后 Pod 稳定 1/1 Ready，重启次数 0。镜像就绪后重跑 `create` 立即通过。

结论：这是等待预算与冷启动拉取时间的匹配问题（本地网络），不影响清单正确性；本轮**未**因此修改任何超时参数，避免掩盖真实回归。

---

## 6. MySQL 验证

| 项 | 结果 |
|---|---|
| Pod | `mysql-87f8b688f-t854q` **1/1 Running**，RESTARTS 0 |
| PVC | `mysql-data` **Bound**（`pvc-201fd1b3…`，2Gi，RWO，SC `standard`） |
| Service | `mysql` ClusterIP `10.96.239.142:3306`，Endpoints `10.244.0.9:3306` |
| EndpointSlice | `mysql-pqb8r`，IPv4，端口 3306 |
| 真实 SQL | 集群内 `verify-mysql` Job 通过 Service DNS + 应用账号执行 |

`Job/verify-mysql` 日志（真实输出）：

```
[verify-mysql] 目标: surgecart@mysql.surgecart.svc.cluster.local:3306/my_shop
[verify-mysql] DNS 解析: 10.96.239.142   mysql.surgecart.svc.cluster.local
mysql_select_1
1
current_database
my_shop
MYSQL_VERIFY_OK
```

安全上下文（实际对象）：`runAsNonRoot=true, runAsUser/Group=999, fsGroup=999, seccompProfile=RuntimeDefault`；容器 `readOnlyRootFilesystem=true, allowPrivilegeEscalation=false, capabilities.drop=[ALL]`。

---

## 7. Redis 验证

| 项 | 结果 |
|---|---|
| Pod | `redis-5c44b5d69d-f66q6` **1/1 Running**，RESTARTS 0 |
| PVC | `redis-data` **Bound**（1Gi） |
| Service | `redis` ClusterIP `10.96.238.116:6379`，Endpoints `10.244.0.11:6379` |
| PING | `PONG` |

`Job/verify-redis` 日志：

```
[verify-redis] 目标: redis.surgecart.svc.cluster.local:6379 (db=0)
[verify-redis] PING -> PONG
REDIS_VERIFY_OK
```

Redis 保持无认证（与 `docker-compose.yml` 及 `config.yaml` 默认一致），访问面由 ClusterIP 限制在集群内——本轮未为部署方便而改变认证语义。

---

## 8. Kafka 验证

### 8.1 Broker 与 KRaft

```
ClusterId:              a6SsLG7YBxBAeqa-3gKwgg
LeaderId:               1
LeaderEpoch:            1
HighWatermark:          1158
MaxFollowerLag:         0
CurrentVoters:          [{"id": 1, "directoryId": null, "endpoints": ["CONTROLLER://127.0.0.1:9093"]}]
CurrentObservers:       []
```

- 进程角色 `broker,controller` 合一，`CLUSTER_ID` 与 ConfigMap 一致，**无 ZooKeeper**。
- 监听器：`PLAINTEXT://0.0.0.0:9092`（集群内）、`CONTROLLER://0.0.0.0:9093`（进程内自连）、`EXTERNAL://0.0.0.0:29092`（port-forward 用）。
- 广播：`PLAINTEXT://kafka.surgecart.svc.cluster.local:9092`、`EXTERNAL://127.0.0.1:29092`。
- Pod `kafka-6cc98894b9-h5gvc` **1/1 Running**，RESTARTS 0；`blocked`/`OOMKilled` 均为 0（`KAFKA_HEAP_OPTS=-Xms256m -Xmx512m` 与 1Gi limit 匹配）。
- PVC `kafka-data` **Bound**（2Gi）。

### 8.2 Topic 初始化 Job

```
NAME                STATUS     COMPLETIONS   DURATION   AGE
kafka-topics-init   Complete   1/1           7m44s      15m
```

Job 日志（真实输出摘录）：

```
[topics] 等待 Broker 可应答: kafka.surgecart.svc.cluster.local:9092
[topics] 创建（若不存在）: surgecart.flashsale.order.requested.v1
[topics] 创建（若不存在）: surgecart.flashsale.order.requested.v1.dlq
[topics] 当前集群 Topic：
  surgecart.flashsale.order.requested.v1
  surgecart.flashsale.order.requested.v1.dlq
Topic: surgecart.flashsale.order.requested.v1  PartitionCount: 1  ReplicationFactor: 1  Configs: min.insync.replicas=1
[topics] 完成
```

幂等性得到实际验证：重复 `create` 未产生第二个 Topic/Job（`--create --if-not-exists`，Job 清单未变则不重跑）。

### 8.3 生产与消费

`Job/verify-kafka`（修复后重跑）：

```
[verify-kafka] bootstrap: kafka.surgecart.svc.cluster.local:9092
[verify-kafka] 1/4 broker 元数据
kafka.surgecart.svc.cluster.local:9092 (id: 1 rack: null) -> (
[verify-kafka] 2/4 Topic 存在性           # 断言分区/副本 = 1/1
[verify-kafka] 3/4 生产: verify-kafka-1794225290-$   # MARKER 结尾的 $$ 不被展开
[verify-kafka] 4/4 消费（回读并断言标记一致）
[verify-kafka] 已消费 2 条
KAFKA_VERIFY_OK
```

关键点：`1/4` 步断言 **broker 广播地址 == Service DNS**。这证明「拿到元数据后客户端仍能连回 Broker」，而不是只证明「9092 端口可连」。

修复前的失败证据（保留以便追溯）：同一条消息内容为 `verify-kafka-1794224941-$$`，`grep -qx` 因尾部 `$` 被解释为行尾锚点而漏判；Job 报 `BackoffLimitExceeded`，Topic 内消息实际存在（用 `cat -A` 直接读取确认）。修复后同一场景通过。

---

## 9. Kubernetes 网络验证

```
$ kubectl get svc -n surgecart
NAME    TYPE        CLUSTER-IP      PORT(S)              SELECTOR
kafka   ClusterIP   10.96.242.148   9092/TCP,29092/TCP   app.kubernetes.io/name=kafka
mysql   ClusterIP   10.96.239.142   3306/TCP             app.kubernetes.io/name=mysql
redis   ClusterIP   10.96.238.116   6379/TCP             app.kubernetes.io/name=redis

$ kubectl get endpoints -n surgecart
kafka   10.244.0.10:9092,10.244.0.10:29092
mysql   10.244.0.9:3306
redis   10.244.0.11:6379

$ kubectl get endpointslices -n surgecart
kafka-x2vm2   IPv4   29092,9092   10.244.0.10
mysql-pqb8r   IPv4   3306         10.244.0.9
redis-xrp5l   IPv4   6379         10.244.0.11
```

临时诊断 Pod `t3-dns-probe`（`busybox:1.36`，restricted 兼容：非 root、只读根文件系统、capabilities 全丢）在集群内执行，**验证后已删除**：

```
=== 集群内 DNS 解析（FQDN）===
mysql.surgecart.svc.cluster.local  -> 10.96.239.142
redis.surgecart.svc.cluster.local  -> 10.96.238.116
kafka.surgecart.svc.cluster.local  -> 10.96.242.148
--- 短名（同命名空间）---
mysql -> 10.96.239.142   redis -> 10.96.238.116   kafka -> 10.96.242.148

=== Service 端口连通性 ===
CONNECT OK   mysql.surgecart.svc.cluster.local:3306
CONNECT OK   redis.surgecart.svc.cluster.local:6379
CONNECT OK   kafka.surgecart.svc.cluster.local:9092
```

解析结果与 Service ClusterIP 完全一致；连通性由 Pod 内部发起，**不是**宿主机 localhost / Docker 网络测试。

---

## 10. 测试门禁

| 门禁 | 命令 | 结果 | 说明 |
|---|---|---|---|
| Manifest 体系 | `bash scripts/test-k8s-manifest.sh` | **PASS** | Go 结构化断言全绿；检测到集群后执行了 **server-side dry-run**（真实 API Server 校验，不创建资源） |
| 基础设施清单 | `bash scripts/test-k8s-infra.sh` | **PASS** | 含新增的 Kafka 断言；server-side dry-run 通过；脚本行为用例通过 |
| 集群脚本行为 | `bash scripts/test-k8s-cluster.sh` | **PASS** | 含新增的 MSYS 隔离断言与「不允许特权时 doctor 必须失败」用例 |
| 发布脚本行为 | `bash scripts/test-deploy-k8s.sh` | Windows **FAIL（环境限制）** / Linux 容器 **PASS** | Windows 下测试桩使用 `syscall.Flock`（Linux 专有），编译失败；在 `golang:1.26` 容器内 `bash scripts/test-deploy-k8s.sh` 全通过（`发布脚本行为测试全部通过`） |
| Docker 等价运行 | `bash scripts/test-k8s-infra-runtime.sh` | **NOT_RUN** | 本次已用真实 Kubernetes 覆盖其验证意图；未额外执行（避免与既有 `code-im-*` 容器抢端口） |
| 真实 Kubernetes | 本报告第 5–9 节 | **PASS** | 首次获得真实集群证据 |

分级说明（严格区分）：

- **静态验证**：PASS（含 server-side dry-run）。
- **Docker 等价验证**：本轮未执行（历史 T2-07 曾通过，不代表本轮）。
- **真实 Kubernetes 验证**：PASS（Pod/PVC/Service/DNS/协议级全部实测）。

---

## 11. 已知问题

1. **首次部署等待预算偏紧**：`MYSQL_TIMEOUT=420s` 在冷启动（镜像拉取 6m40s）时不够。本轮未改参数；若后续重复搭建更慢的网络环境，可考虑首次执行前预拉取镜像，而不是放宽超时。
2. **`test-deploy-k8s` 在 Windows 无法运行**：`test/deployk8s/fakestate/state.go` 使用 `syscall.Flock`。已在 Linux 容器中复核通过；该限制属于测试桩的可移植性，与本次改动无关，未擅自修改（属 T2-06 范围）。
3. **`kubeconform` 未安装**：`test-k8s-manifest.sh` 跳过 API Schema 校验（脚本明确标记 `NOT_VERIFIED`）。本轮通过 server-side dry-run 由真实 API Server 完成了同等强度的校验。
4. **`kubectl` 客户端 1.36.1 / 服务端 1.37.0**：仍在 ±1 minor 支持窗口内；`kubectl` 为 Docker Desktop 自带版本，未额外安装。
5. **等待窗口期间的 Warning 事件**：`Readiness probe failed: ERROR 2003`（MySQL 首次初始化期间）与 `BackoffLimitExceeded`（修复前的 verify-kafka）。二者均为历史事件，当前 Pod 全部 Ready、RESTARTS 0。
6. **`.env` 中的本地凭据**：由本机随机生成（32 位），已确认被 `.gitignore` 忽略；`scripts/k8s-infra.sh secrets` 只把值写入集群 Secret，不回显、不落盘到仓库。

---

## 12. 最终验收

| 编号 | 验收项目 | 结果 | 证据 |
|---|---|---|---|
| T3-01-01 | Git 基线 | **PASS** | 基线 `ffc0bdd…` 与实际远端分支 HEAD 一致，任务分支已建 |
| T3-01-02 | 本地集群（API Server） | **PASS** | `cluster-info` → `https://127.0.0.1:14342` |
| T3-01-03 | Node Ready | **PASS** | 1/1 Ready，v1.37.0 |
| T3-01-04 | MySQL | **PASS** | Pod Ready、PVC Bound、`SELECT 1` = 1 |
| T3-01-05 | Redis | **PASS** | Pod Ready、`PING` → `PONG` |
| T3-01-06 | Kafka / KRaft | **PASS** | Leader=1，voters `1@CONTROLLER://127.0.0.1:9093`，无 ZK |
| T3-01-07 | Topic 初始化 Job | **PASS** | `kafka-topics-init` Complete 1/1 |
| T3-01-08 | Kafka 生产/消费 | **PASS** | `KAFKA_VERIFY_OK`，回读断言一致 |
| T3-01-09 | Service | **PASS** | 三个 ClusterIP + Endpoints/EndpointSlices |
| T3-01-10 | 集群内 DNS | **PASS** | 三个 FQDN + 短名均解析到 ClusterIP |
| T3-01-11 | 端口连通 | **PASS** | 三处 `CONNECT OK` |
| T3-01-12 | 安全策略未放宽 | **PASS** | 命名空间仍 `pod-security.kubernetes.io/enforce=restricted`；三个工作负载仍为只读根文件系统/全丢 capabilities/非 root |
| T3-01-13 | 数据持久化 | **PASS** | mysql/redis/kafka 三个 PVC 全部 Bound |
| T3-01-14 | 回归测试 | **PASS** | manifest / infra / cluster 通过；deploy-k8s 在 Linux 容器通过 |

**T3-01 = COMPLETED。可以进入 T3-02。**

T3-02 的前置条件已具备：集群可用、三个依赖协议级可用、Service DNS 已在集群内验证、应用侧的 `surgecart-database-config` 中 `DATABASE_DEFAULT_HOST` 已指向 `mysql.surgecart.svc.cluster.local`。T3-02 仍需自行完成：镜像准备、Secret/ConfigMap 校验、迁移 Job 执行、应用 Deployment 发布与秒杀链路验证（本轮未部署应用）。

---

## 13. 最终 Git SHA

- 基线：`ffc0bddf805b670bc34ef39209dd0cf73469dbb5`
- 本次提交：`feat(k8s): validate local cluster infrastructure`
- 最终 HEAD：提交后由 `git rev-parse HEAD` 回填（见任务回执与下方"最终 HEAD"行）

最终 HEAD：`__FINAL_HEAD_SHA__`
