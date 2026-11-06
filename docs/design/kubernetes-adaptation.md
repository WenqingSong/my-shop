# SurgeCart Kubernetes 适配设计（任务二）

## 基线与范围

基线为 `fix/flashsale-kafka-b4.1` 的 `2278ab2de3cd6aed0708003c7632f91e4e2fcd6a`。本文件依据该提交的实际启动、容器和探针代码制定。首批仅收口镜像启动语义与探针 HTTP 契约；不创建含猜测参数的 Kubernetes 清单，不改变秒杀状态机、Kafka 消息契约，也不部署集群。

## 当前运行链路与可复用部分

- `Dockerfile` 已用 Go 静态编译、多阶段镜像、固定 UID/GID `10001`、时区与 CA 证书；镜像内配置文件的敏感默认值为空。`.dockerignore` 排除 `.env` 与运行数据。
- `main.go` 进入 `cmd.Main`；`serve` 依次执行 `boot.Bootstrap`（MySQL/Redis 连通性、schema 版本、管理员与权限 seed）、七牛配置及 bucket 校验、FlashSaleRuntime 装配、HTTP 监听。`serve` 不执行迁移。
- `FlashSaleRuntime.Shutdown` 已按 Publisher、Consumer、Producer、Scanner 顺序关闭。GoFrame HTTP graceful drain 默认 10 秒，Runtime 默认预算 30 秒；B4.1 的进程级 SIGTERM 测试覆盖实际服务关闭顺序。
- `/health` 是独立于外部依赖的存活探针。`/ready` 对 MySQL 和 Redis 做有超时、有缓存的探测，失败经生产响应中间件返回 HTTP 503。Kafka Broker 故障不会导致 `/health` 失败；Kafka 消费健康应另用指标与告警观察。
- Compose 提供本地 MySQL、Redis、单 Broker Kafka 与应用；`scripts/docker-entrypoint.sh` 在本地先 `migrate up` 再 `exec serve`。现有 `test-container.sh`、`test-unit.sh`、`test-race.sh`、`test-kafka-e2e.sh`、`test-serve-lifecycle.sh` 可继续复用。

## 第一批：镜像、启动与探针契约

生产镜像默认 `ENTRYPOINT ["/app/my-shop"]`、`CMD ["serve"]`。应用直接作为 PID 1 接收 SIGTERM；镜像默认启动不会在每个副本执行 DDL。Compose 显式覆盖为原有入口脚本，保持本地开发自动迁移行为。多副本发布前必须独立运行一次 `my-shop migrate up`，成功后再启动应用；`serve` 的 schema 检查作为最后防线。迁移步骤失败时不得继续发布。

探针使用应用端口 8000 的 `GET /health`（liveness）和 `GET /ready`（readiness）。`/ready` 失败不应触发进程重启。启动期存在依赖重试、schema/seed 和七牛校验，后续集群清单应使用 startupProbe 或等价的启动保护，并通过实测确定阈值。就绪结果默认缓存 3 秒，摘流传播与优雅关闭时序需在集群滚动更新中验证。不能把 `/ready` 理解为 Kafka 消费进度或所有外部服务正常。

## 配置与 Secret 边界

运行时沿用 GoFrame 的环境变量覆盖机制。普通配置适合 ConfigMap：`SERVER_ADDRESS`、`SERVER_GRACEFUL_TIMEOUT`、数据库主机/端口/用户名/库名、Redis 地址/DB、七牛 bucket/domain/region、存储路径、秒杀模式及 Kafka brokers/topic/DLQ/group、探针超时/缓存 TTL。敏感值由 Secret 注入：`DATABASE_DEFAULT_PASS`、`REDIS_DEFAULT_PASS`（若 Redis 启用认证）、`AUTH_JWT_SECRET`、`ADMIN_SUPER_PASSWORD`（仅初始化时需要）、`QINIU_SECRET_KEY`；`QINIU_ACCESS_KEY` 虽是标识，也建议与七牛凭据一同管理。不得将真实值写进镜像、清单、ConfigMap、命令行或日志。缺少 DB/JWT/七牛必需配置时现有启动逻辑会失败。

上述名称是现有配置键，不代表已经选定集群 Secret 名称或具体命名空间。环境变量方式需要重启 Pod 才能读取新值。Secret 的注入、轮换、RBAC 和镜像仓库凭据由部署环境确定。

## 分阶段任务

1. **基础契约（本批）**：生产镜像直接启动 `serve`、保留 Compose 本地迁移行为，验证非 root、信号入口和 `/health`、`/ready` 的 HTTP 语义；写明配置边界与测试缺口。
2. **单副本编排**：确认目标集群、镜像仓库、依赖地址和持久化方案后，编写 migration Job、Deployment、Service、ConfigMap/Secret 引用、探针、安全上下文与资源配置。探针周期、资源 request/limit、终止宽限期由冷启动和 SIGTERM 实测确定；现有默认关闭预算约 40 秒只是下界依据，不是经集群验证的参数。
3. **多副本与滚动更新**：验证管理员 seed 并发、Outbox 领取互斥、Kafka Consumer Group 分区分配、滚动摘流与在途请求、重启收敛。`/app/storage` 是本地文件目录，跨 Pod 不共享；在选定对象存储或共享存储策略前，不应把会写入该目录的功能当作多副本安全能力。
4. **生产门禁**：隔离环境做真实镜像启动、容器 PID 1 SIGTERM、镜像内容与 UID 检查、集群滚动更新和故障注入；核对订单、库存、Outbox、消费位移与数据一致性，并测量关闭耗时、消费 Lag 与容量。修复已记录的 F-09/F-10 正确性风险后再考虑生产流量。

## 部署前风险与未验证事实

- 当前 `docker-compose.yml` 的 Kafka 为单节点开发配置，不具备 Broker 故障容忍能力。目标集群的 Broker、Topic 副本、认证与网络策略尚未确定。
- B4.1 验证的是应用进程 SIGTERM，不等于容器 PID 1 或 Kubernetes 滚动更新已验证。新镜像默认入口需要真实 Docker 构建与信号测试。
- `/ready` 只覆盖 MySQL 与 Redis；七牛在启动时校验，Kafka 运行期以恢复循环处理。需要单独监测 Kafka 发布失败、消费延迟与积压。
- `seedSuperAdmin` 与权限 seed 虽按现有代码运行，仍需在多副本并发启动中实测。初始化密码的交付与删除时机需部署流程定义。
- 本地存储目录和对外 `/storage/banners` 在多 Pod 下可能产生不同内容；选定持久化策略前不可宣称多副本可用。
