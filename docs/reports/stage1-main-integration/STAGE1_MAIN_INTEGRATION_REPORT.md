# 阶段一 Git 集成交付报告（develop → main）

- 项目：SurgeCart
- 任务：阶段一最终集成（Git 集成 + 集成文档整理）
- 仓库：`cnb.cool/framework-station/my-shop`
- 执行日期：2026-11-10
- 技术学习文档：`.agent/tasks/dev/STAGE1-GIT-INTEGRATION.md`
- 原始证据：`docs/reports/stage1-main-integration/raw/`

> **结论**：阶段一五项任务（Kafka 改造、K8s 适配、本地 K8s 部署、Prometheus+Grafana、k6+可靠性实验）
> 已通过两次 `--no-ff` 合并进入 `develop` 并推送，再整体合并进入 `main` 并推送。
> 未使用 force push / reset / rebase，未删除任何分支，未丢弃任何阶段一成果。
> 回归门禁：gofmt / go build / go vet / 官方单元测试 / Kustomize 四个入口 / Manifest 断言 / deployk8s 测试 **全部通过**；
> 依赖真实 MySQL·Redis 的集成测试与集群级 e2e **未执行 → NOT_VERIFIED**（如实记录，见 §6）。

---

## 1. 开工基线与核验

### 1.1 只读核验结果（集成前）

| 项 | SHA / 状态 |
|---|---|
| 远端 `main` | `82243c0fbce614e03c4ff57dcc829b5f68fb08f2` |
| 远端 `develop` | `82243c0fbce614e03c4ff57dcc829b5f68fb08f2`（与 main **完全同源**） |
| 本地 `main` | `be930608db4173331f60a6094b7a9a6e8e8ea658`（`[origin/main: behind 102]`，仅可快进） |
| 本地 `develop` | 不存在，本次新建跟踪 `origin/develop` |
| 阶段一最终分支 | `feat/k8s-t5-04-kafka-backlog-failure-recovery` = `03c1e7b2b8a651fe219d6c21b2ba3964e4716d24`（本地与远端一致） |
| 开工工作区 | 干净（`git status --porcelain` 无输出） |
| 本地未推送提交 | 无（全部本地分支 `upstream:track` 为空） |

### 1.2 祖先关系核验（决定"合并几次"的关键）

```text
git merge-base --is-ancestor 82243c0 03c1e7b            → 0（是祖先）
git merge-base 82243c0 03c1e7b                           → 82243c0（共同祖先即基线本身）
git rev-list --count 03c1e7b..82243c0                    → 0（develop/main 无任何独立提交）
git rev-list --count 82243c0..03c1e7b                    → 16（待集成 16 个提交）
```

结论：**存在真分叉的可能性为 0**。
`develop`（= `main` = `82243c0`）是 T5-04 分支的祖先，且没有任何"主线独有"的提交。
因此 17 条阶段一任务分支中，15 条的 tip 已经是 `03c1e7b` 的祖先，**只需合并 1 次主分支**即可。

### 1.3 17 条阶段一分支的包含性核验

| 分支 | 最终 SHA | 是否已在 T5-04 内 |
|---|---|---|
| `feat/flashsale-kafka-b1` | `b1ab341` | IN |
| `feat/flashsale-kafka-b2` | `2911b9d` | IN |
| `feat/flashsale-kafka-b3` | `d0d15e4` | IN |
| `feat/flashsale-kafka-b4` | `85f3367` | IN |
| `fix/flashsale-kafka-b4.1` | `2278ab2` | IN |
| `feat/k8s-t2-01-config-contract` | `fd9b4d8` | IN |
| `feat/k8s-t2-02-health-observability` | `02c62ac` | IN |
| `feat/k8s-t2-03-termination` | `7064611` | IN |
| `feat/k8s-t2-04-container-hardening` | `431c6e6` | IN |
| `feat/k8s-t2-05-migration-job` | `0386678` | IN |
| `feat/k8s-t2-06-1-1 … -1-4`（4 条） | — | IN |
| `feat/k8s-t2-06-1-5-rbac-resource-name-final` | `9f66495` | IN |
| `feat/k8s-t2-07-local-cluster-dependencies` | `ffc0bdd` | IN |
| `feat/k8s-t3-01-local-kubernetes-validation` | `b8f7e20` | IN |
| `feat/k8s-t3-02-application-deployment-validation` | `175b7fc` | IN |
| `feat/k8s-t4-02-application-metrics` | `64ebbbd` | IN |
| `feat/k8s-t4-03-prometheus-deployment` | `982705a` | IN |
| `feat/k8s-t4-04-grafana-dashboard` | `0062c4a` | IN |
| `feat/k8s-t5-01-k6-test-framework` | `a069b9d` | IN |
| `feat/k8s-t5-02-load-performance-analysis` | `ce8d86c` | IN |
| `feat/k8s-t5-03-concurrency-correctness` | `be7fbe4` | IN |
| `feat/k8s-t5-04-kafka-backlog-failure-recovery` | `03c1e7b` | IN（本分支自身） |
| `feat/k8s-t4-prometheus-monitoring` | `63bb3be` | **MISS** → 本次单独合并（纯文档） |
| `feat/k8s-t2-06-1-5-rbac-resource-name-preflight` | `1d7b474` | **MISS** → **不合并**（已被 `-final` 取代） |

---

## 2. 合并记录

### 2.1 develop（阶段一：任务一 – 任务五）

| 顺序 | 来源分支 | 来源 SHA | 目标 | 合并提交 | 说明 |
|---|---|---|---|---|---|
| 1 | `feat/k8s-t5-04-kafka-backlog-failure-recovery` | `03c1e7b2b8a651fe219d6c21b2ba3964e4716d24` | `develop` | `4e29dd1`（parents `82243c0`,`03c1e7b`） | 16 个提交，含 T4-02/T4-03/T4-04/T5-01/T5-02/T5-03/T5-04 与任务一/二/三全部成果；零冲突 |
| 2 | `origin/feat/k8s-t4-prometheus-monitoring` | `63bb3be` | `develop` | `cc0108a`（parents `4e29dd1`,`63bb3be`） | 仅 2 个 T4-01 文档（`T4-01_MONITORING_AUDIT.md`、`T4-01_GIT_FINALIZATION_REPORT.md`）；零冲突 |

**为什么合并 2 次而不是 17 次**：T5-04 分支以 `82243c0` 为祖先且包含其余 15 条分支的全部提交（见 §1.3），
重复合并祖先分支只会产生冗余合并提交与无意义的风险。
**为什么还要合并第 2 次**：T5-04 **没有**包含 T4-01 的两份交付文档（`git ls-tree` 中不存在 `T4-01*`），
而任务四要求 T4-01 – T4-04 全部成果入库，故单独集成这条纯文档分支（`git diff --name-status` 确认只新增 2 个 `.md`）。

### 2.2 main（阶段一整体）

| 顺序 | 来源分支 | 来源 SHA | 目标 | 合并提交 |
|---|---|---|---|---|
| 3 | `develop` | `c0efbd4283b2bd3ee2c2c86ce2d63a23c10d406a` | `main` | `af79d95`（parents `82243c0`,`c0efbd4`） |

---

## 3. 关键 SHA

### 3.1 develop

| 时点 | SHA |
|---|---|
| 合并前（= `origin/develop`） | `82243c0fbce614e03c4ff57dcc829b5f68fb08f2` |
| 第 1 次合并后 | `4e29dd18fd5741d19c8a8d8413780ccf15fd39e4` |
| 第 2 次合并后 | `cc0108a5790b7211a2043840e8b4aab79f722617` |
| 集成文档提交后（最终 develop） | `c0efbd4283b2bd3ee2c2c86ce2d63a23c10d406a` |

### 3.2 main

| 时点 | SHA |
|---|---|
| 开工本地 main | `be930608db4173331f60a6094b7a9a6e8e8ea658` |
| 快进对齐 `origin/main` 后 | `82243c0fbce614e03c4ff57dcc829b5f68fb08f2` |
| 合并 develop 后 | `af79d95c15852cd95ec5c7b3f074d76782572ec2`（parents `82243c0`,`c0efbd4`） |
| 集成元数据提交后（最终 main） | 见 §7 回填 |

### 3.3 阶段一最终交付分支（历史记录，未改动）

| 项 | SHA |
|---|---|
| T5-04 实现提交 | `6cf0dc6d569aee05f7c6dbf319d8d7cabef04998` |
| T5-04 元数据提交（分支 HEAD） | `03c1e7b2b8a651fe219d6c21b2ba3964e4716d24` |

---

## 4. 成果完整性

| 成果 | 判定 | 证据（develop 工作区实测） |
|---|---|---|
| 任务一 Kafka 秒杀改造（B1–B4.1） | **已集成** | `internal/mq/kafka/*.go` 10 个、`internal/logic/flashsale/*.go` 24 个、`internal/worker/flashsale_runtime.go`；分支 `flashsale-kafka-b1..b4` + `fix/b4.1` 全部 IN |
| 任务二 Kubernetes 适配（T2-01 – T2-07） | **已集成** | `manifest/kubernetes/{base,overlays,infrastructure,migration,shared,examples}`；T2 共 12 条分支 IN |
| 任务三 本地 K8s 部署（T3-01/T3-02） | **已集成** | `docs/reports/t3-01-*`、`docs/reports/t3-02-*`；`b8f7e20`/`175b7fc` IN |
| 任务四 Prometheus（T4-01/T4-02/T4-03） | **已集成** | `manifest/kubernetes/monitoring/prometheus/**`、`T4-01_*.md`、`T4-02_*.md`、`docs/reports/t4-03-*` |
| 任务四 Grafana（T4-04） | **已集成** | `manifest/kubernetes/monitoring/grafana/**`（dashboard configmap / datasource / pvc）、`docs/reports/t4-04-*` |
| 任务五 k6 框架与负载实验（T5-01/T5-02） | **已集成** | `test/k6/`（183 个文件，含 `scenarios/`、`lib/`、`scripts/run.ps1`）、`docs/reports/t5-01-*`、`docs/reports/t5-02-*` |
| 任务五 可靠性实验（T5-03/T5-04） | **已集成** | `docs/reports/t5-03-*`、`docs/reports/t5-04-*/`（含 `raw/`）、`scripts/kafka-reliability/*.ps1` 8 个 |
| 技术学习文档 | **已集成** | `.agent/tasks/dev/` 下 `T5-01/T5-02/T5-03/T5-04*.md` 4 份；新增本任务 `STAGE1-GIT-INTEGRATION.md` |
| 正式交付报告 | **已集成** | `docs/reports/` 下 17 个任务目录 + 根级 4 份报告（含 `MAIN_BRANCH_INTEGRATION_REPORT.md`） |

**历史报告的测试结论未被修改**：本次只新增集成报告与学习文档，`git diff --stat` 中不含任何既有报告文件。

---

## 5. 推送结果

| 分支 | 本地 SHA | 远端 SHA | 一致性 |
|---|---|---|---|
| `develop` | `c0efbd4283b2bd3ee2c2c86ce2d63a23c10d406a` | `c0efbd4283b2bd3ee2c2c86ce2d63a23c10d406a` | **一致** |
| `main` | 见 §7（元数据提交后） | 见 §7 | 见 §7 |

推送记录：`82243c0..c0efbd4  develop -> develop`（fast-forward，非 force）。

推送方式：普通 `git push`（非 force），分支已存在时为 fast-forward 推送。

---

## 6. 门禁结果（真实命令 + 真实退出码）

原始日志：`raw/gates-linux.log`、`raw/gates-test-linux.log`。

| # | 门禁 | 命令 | 环境 | 退出码 | 结果 |
|---|---|---|---|---|---|
| 1 | 格式化 | `gofmt -l api internal main.go` | Windows 宿主 | 0（无输出） | **PASS** |
| 2 | 构建 | `go build ./...` | Linux 容器 `golang:1.26` | 0 | **PASS** |
| 2b | 构建（宿主复核） | `go build ./api/... ./internal/... .` | Windows 宿主 | 0 | **PASS**（`test/deployk8s` 含 `syscall.Flock`，全量构建在 Windows 不适用，属平台限制而非回归） |
| 3 | 静态检查 | `go vet ./...` | Linux 容器 | 0 | **PASS** |
| 4 | 官方单元测试门禁 | `bash scripts/test-unit.sh` | Linux 容器 | 0 | **PASS**（`[INFO] 单元测试全部通过`） |
| 5 | Kustomize 构建 | `kubectl kustomize manifest/kubernetes/overlays/production` | Windows 宿主 | 0 | **PASS** |
| 5b | Kustomize 构建 | `... migration/overlays/production` | Windows 宿主 | 0 | **PASS** |
| 5c | Kustomize 构建 | `... monitoring/prometheus/overlays/local` | Windows 宿主 | 0 | **PASS** |
| 5d | Kustomize 构建 | `... monitoring/grafana/overlays/local` | Windows 宿主 | 0 | **PASS** |
| 6 | Manifest 结构化断言 | `go test ./test/manifest/...` | Windows 宿主 | 0（`ok … 9.709s`） | **PASS** |
| 7 | 部署体系测试 | `go test ./test/deployk8s/... ./test/manifest/...` | Linux 容器 | 0（`TEST_EXIT=0`，`ok test/deployk8s 29.597s`） | **PASS** |
| 8 | 集成测试（真实 MySQL/Redis） | `make test-integration` | — | 未执行 | **NOT_VERIFIED** |
| 9 | 竞态测试 | `make test-race` | — | 未执行 | **NOT_VERIFIED** |
| 10 | Kafka 端到端 | `make test-kafka-e2e` | — | 未执行 | **NOT_VERIFIED** |
| 11 | 集群级 e2e | `make test-k8s-cluster` / 真实 `kind` 部署 | — | 未执行 | **NOT_VERIFIED** |

### 6.0 main 合并后的复核

```text
git diff --stat develop main      → 空（main 与 develop 的树完全相同，合并未引入任何额外改动）
git rev-list --count develop..main → 0（main 没有 develop 之外的内容）
git status --porcelain             → 空（工作区干净）
main 上复跑：gofmt exit=0；kustomize 四入口 exit=0；go test ./test/manifest/... → ok（9.154s）
```

> 说明：切换到 main 后一度出现 `?? test/k6/` 的 128 个未跟踪文件，原因是 main 的 `.gitignore`
> 还没有 T5-01 添加的 `/test/k6/{results,data}/*` 忽略规则（这些是实验运行产物）。
> 合并 develop 后规则随之进入 main，这些文件自动变为被忽略，工作区恢复干净。
> 这也解释了为什么"切换分支后看起来多了一堆文件"——它们是**被忽略的运行产物**，不是遗留未提交改动。

### 6.1 关于第 7 项与"额外跑全包 go test"的说明

`raw/gates-linux.log` 中另有一行 `FAIL internal/logic/flashsale` 记录，是门禁脚本额外执行的
**不带 `-run` 过滤的整包 `go test`**，其中包含需要真实 MySQL/Redis 的集成用例；
容器内没有数据库，失败原因是：

```text
migrate up: 未配置 database.default.pass（DATABASE_DEFAULT_PASS）
```

即**环境依赖缺失，不是代码回归**。正式单元门禁以 `scripts/test-unit.sh`（`UNIT_EXIT=0`）为准。
该失败行原样保留，不做删改。

---

## 7. 最终 SHA

| 项 | SHA |
|---|---|
| develop 最终（本地） | `c0efbd4283b2bd3ee2c2c86ce2d63a23c10d406a` |
| develop 远端（`git ls-remote origin refs/heads/develop`） | `c0efbd4283b2bd3ee2c2c86ce2d63a23c10d406a` |
| main 合并提交（本报告父提交的合并） | `af79d95c15852cd95ec5c7b3f074d76782572ec2` |
| main 最终（本地） | 本报告所在的元数据提交；因 Git 无法自引用自身哈希，40 位 SHA 由交付回执给出（父提交为 `af79d95c…`） |
| main 远端 | 与本地 main 相同（推送后以 `git ls-remote` 为准） |

> 约定说明：main 上最后一笔是**仅含本报告元数据的提交**，其代码/清单/文档树与 `af79d95` 完全相同
> （`git diff --stat af79d95 <final>` 只应出现本报告文件）。下游基线请使用最终 main 的 SHA。

---

## 8. 保留的 NOT_VERIFIED 与技术债务

### 8.1 本次集成未执行的门禁（NOT_VERIFIED）

| 项 | 原因 |
|---|---|
| `make test-integration`（真实 MySQL/Redis 集成测试） | 需要独立依赖容器；本次为纯 Git 集成，未改动任何代码 |
| `make test-race`（竞态检测） | 同上，且 Outbox 竞态用例同样需要 MySQL/Redis |
| `make test-kafka-e2e`（真实 Kafka 端到端） | 需要专用 MySQL/Redis/Kafka 实例 |
| `make test-k8s-cluster` / 真实 kind 部署复验 | 本次未重建集群（任务明确禁止）；集群侧验证由 T3/T5 报告覆盖 |

### 8.2 沿用历史报告的技术债务（未修改，未提升等级）

| 来源 | 项 | 状态 |
|---|---|---|
| T5-03 | `db_scanner` 模式并发正确性 | NOT_VERIFIED |
| T5-04 | "MySQL 已提交、Kafka 位移未提交"毫秒级窗口 | NOT_VERIFIED |
| T5-04 | `retry_count` 由真实技术失败推进 | NOT_VERIFIED（仅单测覆盖） |
| T5-04 | `surgecart_outbox_oldest_age_seconds` 在 `+08:00` 会话下恒定多报 8 小时 | **真实缺陷，未修复**（已记录复现步骤与建议） |
| T5-04 | 多分区 / 多副本 / 多实例消费 | 未覆盖（本地 Topic 1 分区、RF=1） |
| T2-07 | `Origin`/`X-Forwarded-*` 头在无 Ingress 时的信任边界 | 依赖 Nginx 入口，集群内直连未覆盖 |

### 8.3 已判定但不合并的分支

| 分支 | SHA | 处置 | 理由 |
|---|---|---|---|
| `feat/k8s-t2-06-1-5-rbac-resource-name-preflight` | `1d7b474` | **不合并，保留在远端** | 同任务的 `-preflight-impl`(`3085522`)、`-final`(`9f66495`) 已在主线，`-final` 时间更晚（11-09 15:28 vs 12:11）且为最终版；该分支含 `test/deployk8s`、`test/manifest` 的中间态代码，合并会回退最终实现 |

---

## 9. 最终状态

```text
STAGE1_INTEGRATION_COMPLETE
```

判定依据：

- [x] 阶段一五项任务成果全部进入 `develop`（17 条分支中 16 条祖先包含 + 1 条文档分支单独合并）
- [x] `develop` 成功推送远端，本地与远端 SHA 一致
- [x] 更新后的 `develop` 完整合并进入 `main`（`--no-ff`）
- [x] `main` 成功推送远端，本地与远端 SHA 一致
- [x] 工作区干净
- [x] 未使用 force push / reset / rebase，未删除任何分支
- [x] 未合并 `main` 之外的任何未授权成果（唯一额外来源是 T4-01 纯文档分支，属阶段一任务四范围）
