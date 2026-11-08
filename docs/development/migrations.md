# 数据库迁移（Migration）运维与设计（T2-05）

本文说明 SurgeCart 数据库迁移的架构、执行原理、配置要求、本地与 Kubernetes 执行方式、并发锁、失败恢复与 `migrate force` 风险。设计事实来源为 `internal/migrations`、`internal/cmd/cmd.go` 与 `manifest/kubernetes/migration/`（Kustomize，T2-06）；机制背景见 `docs/design/migration.md`，部署体系见 `docs/development/kubernetes.md`。

## 1. 架构

```text
                    ┌───────────────────────────────┐
发布新版本 ─────────►│ 构建并发布镜像（不可变短 SHA）│
                    └──────────────┬────────────────┘
                                   │
                    ┌──────────────▼────────────────┐
                    │ Job: /app/my-shop migrate up  │  ← 独立 Pod，非 root / 只读根文件系统
                    └──────────────┬────────────────┘
                        成功 │            │ 失败
                    ┌────────▼───┐   ┌───▼──────────────────────┐
                    │ 更新 Deployment │   │ 阻止升级；日志排障；人工处理 │
                    └────────────────┘   └──────────────────────────┘
```

职责边界：

| 组件 | 职责 | 明确不做 |
|---|---|---|
| `my-shop migrate up` | 前置配置校验 → 打开迁移专用连接 → 执行未安装迁移 → 关闭连接 | 不启动 HTTP/Redis/Kafka/Publisher/Consumer/Scanner；不做 seed |
| `my-shop migrate version` | 只读打印 `version` / `dirty` / 最新版本 | 不建表、不写 `schema_migrations` |
| `my-shop migrate force <version>` | 只改 `schema_migrations`（标记已应用、清 dirty） | 不执行 SQL、不校验、不修复 schema |
| `my-shop serve` | 只读 schema 就绪校验（非 dirty 且已到最新）→ seed → 监听 | 不执行 DDL、不自动迁移、不自动 force |
| Migration Job | 每次发布执行一次 `migrate up`，失败即阻止升级 | 不并发并行、不使用 `force` |
| Compose entrypoint | 本地开发便利：`migrate up` → `exec serve` | 生产镜像默认入口不是它（默认直接 `serve`） |

## 2. 执行原理

1. **迁移文件内嵌**：`internal/migrations/sql/*.sql` 经 `//go:embed` 打包，运行时用 `iofs` 挂载，不依赖工作目录或外部文件。
2. **版本号**：`{14 位时间戳}_{title}.up.sql`，数字升序即时间升序；只提供 `.up.sql`（无 down）。
3. **追踪表**：`schema_migrations(version bigint primary key, dirty boolean)`，由 golang-migrate 在首次执行时创建（创建过程本身也持锁）。
4. **幂等判定**：`Up()` 读取当前版本，只执行 `version > current` 的迁移；全部已应用时返回 `ErrNoChange`，被映射为成功（退出码 0）。
5. **连接生命周期**：每次命令用 `sql.Open` + DSN 建立独立连接池，迁移结束（成功或失败）在 `defer` 中关闭；`migration driver` 使用其中一条专用连接执行锁与 DDL。
6. **退出码**：成功 0；任何失败（配置、连接、锁超时、SQL、dirty）非零，错误信息只包含地址/库名/配置项名，不含密码。

### 2.1 事务边界与 MySQL DDL 隐式提交

迁移**不具备事务原子性**，不要假设失败会整体回滚：

- 迁移文件通过 `multiStatements=true` 一次性提交给驱动，文件内多条语句**逐条隐式提交**（MySQL DDL 会隐式 commit）。
- 版本记录在文件执行**成功后**写入；执行中途失败时，部分 DDL 可能已生效，同时 `dirty=true`。
- MySQL 8 的单条 DDL 是原子的（语句级），但**多语句迁移文件整体不是**。
- 因此正确表述是：「版本记录单调前进 + dirty 标记失败中间态」，不是「迁移事务回滚」。

失败后数据库可能处于的状态：**已全部生效但未记录**、**部分生效**、**完全未生效**，三者都可能出现。排障必须先看实际 schema，再决定 `force` 到哪个版本（见 §7）。

## 3. 配置要求

Migration 遵循 T2-01 统一配置契约（`internal/config`），只读取数据库相关项：

| 环境变量 | 必需 | 说明 |
|---|---|---|
| `DATABASE_DEFAULT_HOST` | 是 | 严格模式下必须由环境变量显式提供，且不得是回环地址 |
| `DATABASE_DEFAULT_PORT` | 否 | 默认 `3306` |
| `DATABASE_DEFAULT_USER` | 否 | 默认 `root`（生产应使用专用迁移账号） |
| `DATABASE_DEFAULT_PASS` | 是 | 无内置默认值，缺失或为空立即失败；只能来自 Secret |
| `DATABASE_DEFAULT_NAME` | 是 | 严格模式下必须由环境变量显式提供 |
| `DATABASE_DEFAULT_CHARSET` | 否 | 默认 `utf8mb4` |
| `DATABASE_DEFAULT_DEBUG` | 否 | 默认 `false`，非法布尔值直接失败 |
| `DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS` | 生产必需 | `true` 时启用严格模式校验 |

### 3.1 与应用严格模式的差异（T2-01 F4 修复）

`migrate up/force/version` 在建立任何连接之前调用 `migrations.Preflight` → `config.ValidateMigration`：

- 逐项严格解析上表配置（非法 bool 不静默回退）；
- 数据库密码必须显式注入；
- 严格模式下 `DATABASE_DEFAULT_HOST` / `DATABASE_DEFAULT_NAME` 必须由环境变量提供且非回环（`localhost`、`*.localhost`、`127.0.0.0/8`、`::1`）。

**唯一差异**：migration 不要求 `REDIS_DEFAULT_ADDRESS`，因为它不连接 Redis。应用启动的 `ValidateExternalEndpoints` 仍然要求三项（含 Redis），T2-01 冻结的应用严格模式没有被放宽（`internal/config/migration_test.go` 有对照断言）。

### 3.2 Secret 边界

- 密码只来自 Secret（K8s `secretRef` / Compose `.env`），禁止写入 Dockerfile、镜像、ConfigMap、Job 参数、命令行参数与日志。
- 错误信息不会回显密码；`DATABASE_DEFAULT_PASS` 在配置层被标记为敏感项，错误中统一显示为隐藏。
- 建议使用**独立的迁移账号**（DDL 权限），应用运行账号只保留 DML 权限。

## 4. 本地执行

```bash
# 1) 依赖与数据库（首次）
make init
make up            # 或仅起依赖：bash scripts/deps-start.sh

# 2) 迁移（凭证来自 .env，DATABASE_DEFAULT_PASS 回退 MYSQL_ROOT_PASSWORD）
make migrate-up
make migrate-version

# 3) 直接使用二进制（等价）
export DATABASE_DEFAULT_HOST=127.0.0.1
export DATABASE_DEFAULT_PORT=3306
export DATABASE_DEFAULT_USER=root
export DATABASE_DEFAULT_PASS="$DATABASE_DEFAULT_PASS"
export DATABASE_DEFAULT_NAME=my_shop
go run . migrate up
go run . migrate version

# 4) 容器内
docker run --rm --network my-shop_default \
  -e DATABASE_DEFAULT_HOST=mysql -e DATABASE_DEFAULT_PORT=3306 \
  -e DATABASE_DEFAULT_USER=root -e DATABASE_DEFAULT_NAME=my_shop \
  -e DATABASE_DEFAULT_PASS="$DATABASE_DEFAULT_PASS" \
  --entrypoint /app/my-shop <镜像>:<短SHA> migrate up
```

本地开发保持原有体验：严格模式默认关闭，`config.yaml` 的本机默认地址可用，只需注入密码。

## 5. Kubernetes Job 执行方式

T2-06 起，迁移 Job 由 Kustomize 管理，独立于应用部署入口：

| 路径 | 作用 |
|---|---|
| `manifest/kubernetes/migration/base/` | Job 基础层（命令、重试、超时、TTL、加固），不带发布版本 |
| `manifest/kubernetes/migration/overlays/production/` | 发布入口：版本化 Job 名称 + 迁移镜像版本 + 外部数据库地址 |
| `manifest/kubernetes/shared/database-configmap.yaml` | 与 Application 共用的数据库连接配置（同一份） |
| `manifest/kubernetes/examples/secrets/database-secret.example.yaml` | `surgecart-db-credentials` 模板（只有占位符） |

推荐用发布脚本（顺序由脚本强制，见 `docs/development/kubernetes.md` §9）：

```bash
bash scripts/deploy-k8s.sh
```

等价的 kubectl 流程：

```bash
TAG=<短SHA>          # 必须与 manifest/kubernetes/overlays/production 的镜像版本一致

# 1) 构建（含 Namespace、数据库 ConfigMap、版本化 Job）
kubectl kustomize manifest/kubernetes/migration/overlays/production > /tmp/migration.yaml

# 2) 应用（Job 名形如 surgecart-migrate-<短SHA>，每个版本一个新 Job）
kubectl apply -f /tmp/migration.yaml

# 3) 等待结果（成功才能继续发布应用）
kubectl -n surgecart wait --for=condition=complete \
  job/surgecart-migrate-${TAG} --timeout=900s
```

`surgecart-db-credentials` 必须先存在（创建方式见 `docs/development/kubernetes.md` §4.2）；
`scripts/deploy-k8s.sh` 会在迁移之前检查，缺失即失败，避免 Job 反复认证失败消耗 `backoffLimit`。

**Job 名称为什么带版本后缀**：Kubernetes Job 的 `spec.template` 不可变，同一名称的下一次发布
无法原地更新镜像。版本化名称（`nameSuffix: -<短SHA>`）让每个版本是一个新 Job，`kubectl apply`
永不撞上不可变字段；历史 Job 由 `ttlSecondsAfterFinished` 清理。跨 Job 的并发互斥仍由数据库
`GET_LOCK` 保证，`parallelism: 1` 只是单 Job 内并行度。


Job 字段说明：

| 字段 | 值 | 理由 |
|---|---|---|
| `restartPolicy` | `Never` | 失败即退出，重试由 Job controller 起新 Pod，避免容器内循环掩盖问题 |
| `backoffLimit` | `4` | 锁等待超时（10s）与数据库暂不可达可通过重试收敛；有界，防止无限重试 |
| `activeDeadlineSeconds` | `900` | 全量建表/增量迁移的硬上限，避免 MDL 或网络问题挂死发布 |
| `ttlSecondsAfterFinished` | `86400` | 保留 24h 供 `kubectl logs` 排障后自动清理 |
| `parallelism` / `completions` | `1` / `1` | 并发保护不靠 Job 层；跨 Pod 互斥由数据库锁提供 |
| `securityContext` | `runAsNonRoot`、`10001:10001`、`seccompProfile: RuntimeDefault` | 与 T2-04 镜像一致，禁止 root |
| 容器 `securityContext` | `readOnlyRootFilesystem`、`allowPrivilegeEscalation: false`、`capabilities.drop: [ALL]`、`privileged: false` | 迁移不需要写镜像层或任何 capability |
| `resources` | requests `50m/64Mi`，limits `500m/256Mi` | 迁移是短时低负载任务，避免挤占业务 Pod |
| `/tmp` | `emptyDir{medium: Memory}` | 只读根文件系统下唯一可写路径 |
| `command` / `args` | `/app/my-shop` + `migrate up` | 覆盖镜像 ENTRYPOINT，绝不启动 `serve`，也绝不使用 `force` |
| `envFrom` | ConfigMap + Secret | 非敏感配置与密码分离 |
| `env.DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS` | `"true"` | 环境变量优先于 ConfigMap，防止漏配时退化为非严格模式 |
| `imagePullPolicy` | `IfNotPresent` | 版本用不可变短 SHA，不需要每次拉取 |

## 6. Job 生命周期与发布门禁

1. **何时创建**：镜像已推送、发布流程准备更新 Deployment **之前**。每个发布版本一个新 Job（名称 `surgecart-migrate-<短SHA>`），同名版本重跑前删除该 Job（`kubectl -n surgecart delete job surgecart-migrate-<短SHA>`）。
2. **如何判断成功**：`kubectl -n surgecart wait --for=condition=complete job/surgecart-migrate-<短SHA> --timeout=900s`；退出码 0 且日志出现 `migration 执行完成` 或 `没有待执行的 migration`。
3. **如何判断失败**：`kubectl -n surgecart get job surgecart-migrate-<短SHA> -o jsonpath='{.status.conditions}'` 出现 `Failed`，或 `kubectl -n surgecart get pods -l job-name=surgecart-migrate-<短SHA>` 中有 `Error`/`OOMKilled`。
   **注意「等待超时」不是「失败」**（T2-06.1）：超时可能只是 Job 仍在执行 DDL，此时删除 Job 会中断迁移并留下 dirty 状态；先看 Pod 是否仍在 `Running`/终止中，再决定下一步（见 `docs/development/kubernetes.md` §10.2）。
4. **失败后如何重试**：修正根因（网络、凭据、锁冲突、SQL 错误）后删除该 Job 重新构建并 apply；**不要**通过放宽 `backoffLimit` 或改成 `force` 来「让它过」。
5. **如何看日志**：`kubectl -n surgecart logs job/surgecart-migrate-<短SHA> --tail=200`（`ttlSecondsAfterFinished` 到期后日志随 Job 删除）。
6. **如何清理**：TTL 自动清理；提前清理用 `kubectl -n surgecart delete job -l app.kubernetes.io/component=database-migration`（或用具体名称）。
7. **如何阻止应用升级**：发布流程必须**串行**——Job 未 `Complete` 时不得更新应用清单；`scripts/deploy-k8s.sh` 在迁移失败时以退出码 `2`、等待超时时以退出码 `6`、迁移生命周期无法确认时以退出码 `7` 终止，三种情况都不 apply 应用。发布级的互斥由 `Lease/surgecart-deploy-lock` 提供（T2-06.1，见 `docs/design/deploy-lock.md`）：数据库 `GET_LOCK` 只保护迁移本身，不能替代发布锁。T2-06.1.1 起锁上还记录迁移生命周期（`migration-state` / `migration-job` / `migration-job-uid`）：迁移未确认终态时锁不释放，接管的 Runner 必须先用真实 Job 核验，因此「上一轮迁移仍在跑」不会变成「下一轮迁移已经开始」。T2-06.1.2 进一步要求：`Failed=True` 之后还要确认该 Job 的 Pod 已终止（`Failed` 只是 controller 的判定）；记录指向的 Job 已不存在时，必须提供 `my-shop migrate version` 得到的数据库证据（`version=<N> dirty=<bool> latest=<M>`）才能恢复，脚本不会 force、不会伪造 Job uid，也不会跳过任何迁移（见 `docs/development/kubernetes.md` §10.8）。T2-06.1.3 补齐了这条链路的两个前提：① 发布身份必须有 core `pods` 的 `get`/`list` 权限（RBAC 示例此前漏授权，按旧示例配置首次发布就会失败）；② Pod 查询失败（403/5xx/超时）与「Pod 确实仍在运行」在输出上必须可区分 —— 两种都 fail-closed，但只有后者才需要等待 Pod 终止（见 `docs/design/deploy-lock.md` §12）。T2-06.1.4 又收紧了两处：① `renewTime` 锚定在租约写入请求的**发出时刻**，create/takeover 返回后立即做窗口断言并再续租一次（窗口为负 → `LEASE_ANCHOR_INVALID`，退出码 3，不 apply 任何资源）；② 写迁移记录（含 `COMPLETE`/`FAILED`）之前先判定服务器上的租约是否过期，过期就**不写 Lease**、退出码 3 —— 不允许给一把「别人此刻就能合法接管」的锁续命（见 `docs/design/deploy-lock.md` §7.5）。最后的防线是 `serve` 的只读 schema 校验：数据库非 dirty 且版本已到最新才监听端口，否则进程非零退出（`checkSchemaReady`）。

示例（发布脚本中的顺序，任一步失败即退出，不使用 `&&` 掩盖失败）：

```bash
set -euo pipefail
TAG=<短SHA>
kubectl kustomize manifest/kubernetes/migration/overlays/production > /tmp/migration.yaml
kubectl apply -f /tmp/migration.yaml
kubectl -n surgecart wait --for=condition=complete \
  job/surgecart-migrate-${TAG} --timeout=900s
# 迁移成功后才应用应用清单（脚本内由退出码保证）
kubectl kustomize manifest/kubernetes/overlays/production > /tmp/application.yaml
kubectl apply -f /tmp/application.yaml
kubectl -n surgecart rollout status deployment/surgecart --timeout=600s
```

## 7. 并发锁机制

- 实现：golang-migrate v4.19.0 MySQL driver 在**专用连接**上执行 `SELECT GET_LOCK(<crc32(db:schema_migrations)>, 10)`，整个迁移（含 `schema_migrations` 首次创建）期间持有；结束时 `RELEASE_LOCK`。
- 锁是 MySQL **会话级**锁：连接断开（Pod 删除、进程被杀、网络中断）即自动释放，不会留下永久锁。
- 并发策略：**一个执行，其余等待最多 10s，超时明确失败**。失败不执行任何 DDL，可安全重试；K8s 由 `backoffLimit` 重试。
- 不使用进程内 Mutex：它无法跨 Pod 生效，不能作为跨实例并发保护。
- 未使用自定义锁表：数据库级 advisory lock 已满足互斥，额外锁表会引入新的清理与死锁面。

验证（真实 MySQL + 真实独立进程）：

```bash
bash scripts/test-migration.sh
```

覆盖：双进程并发、锁等待、10s 锁超时零 DDL、持锁会话断开、DDL 执行中被 SIGKILL、脏状态与恢复、严格校验顺序、错误密码/不可达不泄漏密码。

## 8. 重试语义

| 场景 | 行为 | 是否可重试 |
|---|---|---|
| 数据库不可达 | 非零退出，未执行迁移 | 是（依赖恢复后） |
| 凭据错误 | 非零退出，明确 `Access denied`，不泄漏密码 | 是（修正 Secret 后） |
| 锁被占用（>10s） | 非零退出，未执行任何 DDL | 是（等待前一个结束） |
| 迁移 SQL 失败 | 非零退出，`dirty=true`，部分 DDL 可能已生效 | **否**，需人工评估 |
| 进程被终止（SIGKILL） | 锁自动释放；可能留下 `dirty` 或未记录的已执行 DDL | 视状态，需人工确认 |
| 全部已应用 | 退出 0（幂等） | 是 |

**Job 重试不会绕过 dirty**：dirty 状态下 `migrate up` 直接拒绝，不会自动 `force`。

## 9. `migrate force <version>` 风险

`force` 的语义是**只修改 `schema_migrations`**：把版本写成指定值、`dirty` 置 `false`，不执行 SQL、不校验实际表结构。

正确用途：

- 已有环境首次接管（7 张基线表已存在、无 `schema_migrations`）：`force <baseline 版本>`；
- 迁移失败且**已人工核对实际 schema**，确认该版本对象已全部创建：`force <该版本>`，让后续迁移继续。

禁止用途：

- 作为 Migration Job 的启动命令；
- 失败后自动执行（本任务所有自动化路径都不会调用 `force`）；
- 在不检查 schema 的情况下「消掉 dirty」。

误用后果：迁移记录与真实结构不一致，后续迁移可能因对象已存在而失败，或漏建对象直到运行期才暴露。

## 10. 回滚与恢复策略

本项目**不提供** `migrate down`（无 `.down.sql`）。回滚策略分三层：

1. **应用层回滚**：把 Deployment 镜像切回上一个短 SHA；数据库保持前向兼容（新增迁移只增不删，禁止在迁移中 DROP 列/表）。
2. **迁移失败恢复**：
   ```bash
   # 1) 只读查看版本与 dirty（一次性 Pod，不写数据库）
   kubectl -n surgecart apply -f - <<'YAML'
   apiVersion: v1
   kind: Pod
   metadata:
     name: surgecart-migrate-version
   spec:
     restartPolicy: Never
     securityContext: {runAsNonRoot: true, runAsUser: 10001, runAsGroup: 10001}
     containers:
       - name: version
         image: docker.cnb.cool/framework-station/my-shop:<短SHA>
         command: ["/app/my-shop", "migrate", "version"]
         envFrom:
           - configMapRef: {name: surgecart-database-config}
           - secretRef: {name: surgecart-db-credentials}
         securityContext:
           readOnlyRootFilesystem: true
           allowPrivilegeEscalation: false
           capabilities: {drop: ["ALL"]}
   YAML
   kubectl -n surgecart logs -f surgecart-migrate-version
   kubectl -n surgecart delete pod surgecart-migrate-version

   # 2) 人工核对实际 schema（SHOW TABLES / SHOW CREATE TABLE），确认该版本对象是否已创建
   # 3) 确认后再决定 force 到哪个版本（风险操作，见 §9）
   ./my-shop migrate force <version>
   # 4) 重新执行迁移并确认 dirty=false
   ./my-shop migrate up
   ./my-shop migrate version
   ```
3. **灾难恢复**：以数据库备份 + 迁移记录为准重建；恢复后先 `migrate version` 确认版本，再决定是否需要 `force` 对齐记录。

## 11. 排障速查

| 现象 | 判断 | 处理 |
|---|---|---|
| 日志 `未获取到数据库级 migration 锁` | 另一个迁移正在执行，等待超时 | 等待/确认无其它 Job，再重试 |
| 日志 `数据库处于 dirty 状态（版本 …）` | 上次迁移失败留下中间态 | 核对 schema → 人工 `force` 或修复 |
| 日志 `Kubernetes 严格配置模式（migration）校验失败` | 环境变量漏配或指向回环地址 | 修 ConfigMap/Secret，不要关严格模式 |
| 日志 `Access denied for user` | 凭据错误 | 修 Secret；确认迁移账号权限 |
| 日志 `connection refused` / `no such host` | 数据库不可达/DNS 错误 | 检查 Service、NetworkPolicy、数据库状态 |
| Job `DeadlineExceeded` | 超过 `activeDeadlineSeconds` | 查 MDL 阻塞（`SHOW PROCESSLIST`）、长事务 |
| Job 反复失败且 `dirty=false` | 可能是锁竞争 | 降低并发发布，串行化发布流程 |

## 12. 测试与验证范围

```bash
# 配置校验（纯内存，无 DB）
go test -count=1 -run 'TestValidateMigration' ./internal/config/

# 命令层前置校验顺序（纯内存，无 DB）
go test -count=1 -run 'TestMigrate' ./internal/cmd/

# 真实 MySQL：锁、连接释放、幂等、dirty、并发
bash scripts/test-migration.sh

# Kustomize 构建 + 结构化断言 + API Schema 校验（无需集群）
bash scripts/test-k8s-manifest.sh
```

未验证项（NOT_VERIFIED）：

- 真实 Kubernetes 集群上的 Job 调度、`kubectl wait` 流转、TTL 清理与 NetworkPolicy 连通性（本仓库无目标集群）。
- 版本化 Job 名称在真实发布中的行为（新版本创建新 Job、旧 Job 未完成时的并发表现）只做了构建产物断言，未在集群实测。
- 镜像 `docker.cnb.cool/framework-station/my-shop` 的短 SHA 标签在集群内的拉取（验证时使用本地构建镜像）。
- MDL（metadata lock）长时间阻塞下的 `activeDeadlineSeconds` 实测。
- 多可用区数据库故障切换期间的迁移行为。
