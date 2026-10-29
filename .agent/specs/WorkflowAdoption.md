# Workflow V3 项目 Adoption / 安装 / 升级规范

本文定义 Workflow V3 的 **Project Adoption Procedure**、**首次安装协议** 与 **安全升级协议**。它是迁移 / 安装 / 升级协议，不是新增的第七个长期 Agent Role。

正常六角色运行（`TaskBuilder → Analyst → Coder → Cleaner → OwnerGate → Deliverer → Owner Integration`）不依赖本文，也不依赖临时文件 `PROJECT_ADAPTATION.md`。本文只在 Adoption / 安装 / 升级时被读取。

## 1. 定位与事实源

- **Workflow Core**（Roles、Authority、Evidence Snapshot、Handoff、Gate、Completion Contract、Git safety model、Registry coordination concept）跨项目稳定，可迁移。
- **Project Adoption** 负责把 Workflow Core 绑定到某个具体项目的差异（框架、目录、build/test、migration、design mapping、shared resource applicability、项目工程约束）。
- **Task Contract** 负责当前具体功能（AC、selected design、resource requirements、task-specific risk）。
- **Source Distribution** 是框架内容的版本化输入，不是宿主现场。
- **Installed Host** 是宿主仓库中被实际安装的框架文件、真实配置、来源记录与二进制。
- **Protected Host** 是宿主业务文件、既有证据与宿主配置，任何安装 / 升级都不得改写。

三者的关系：

```text
Workflow Core
+
Project Materialized Policy
+
Current Task Contract
=
Agent 当前执行环境
```

正常运行阶段的 Agent 不依赖临时 `PROJECT_ADAPTATION.md`，因为 Adoption 完成后它已被删除。

### 1.1 核心原则：不创造第二个长期事实源

`PROJECT_ADAPTATION.md` 是**临时工作单**：

- 不是长期项目配置；
- 不是新的事实源；
- 不是正常 Task Artifact；
- 不是第七个 Agent 的状态文件。

Adoption 中确认出来的信息，最终必须写入它们真正所属的长期权威位置：

| 确认信息 | 长期权威位置 |
| --- | --- |
| 框架安装来源 / 精确 Commit | `.agent/upstream.yaml` |
| 项目架构 | 项目已有 architecture / design 文档 |
| Build / Test 命令 | README / Makefile / 项目工程规范等真实权威位置 |
| Migration policy | 项目数据库设计 / 工程规范 + Registry |
| Shared Resource policy | 对应 Registry / 项目设计 |
| Branch / Workflow authority | Workflow / 项目工程规范 |
| Validator 必要配置 | `.agent/workflow.yaml` 或 Engine 的正式配置位置 |

不能形成「README 说 A、`PROJECT_ADAPTATION.md` 说 B、实际代码是 C」这样的长期三套事实源。Adoption 完成后，`PROJECT_ADAPTATION.md` 必须可以安全删除。

## 2. 三种对象

安装与升级必须先把以下三类对象分开。混淆它们是覆盖宿主文件与伪造来源记录的主要来源。

### 2.1 A. Source Distribution

Source Distribution（源码分发包）只包含框架本身的版本化内容：

| 类别 | 内容 | 安装到宿主 |
| --- | --- | --- |
| `managed` | `.agent/roles/**`、`.agent/specs/**`、`.agent/templates/**`、`.agent/engine/**`、`.agent/adoption/**`、`.agent/README.md`、`.agent/VERSION`、`.agent/bin/.gitignore` | 是；升级时参与 B/L/N |
| `package_only` | 根 `README.md`、`PACKAGE_MANIFEST.md`、`.cnb.yml`、`docs/design/agent-workflow.md`、`.agent/adoption/tests/**` | 否；仅用于阅读、发布校验与设计追溯 |
| `generated` | `.agent/workflow.yaml`、`.agent/upstream.yaml`、`.agent/bin/workflow-check` | 由安装 / 升级事务按规范生成，不来自包内复制 |

分发包**绝不能**夹带真实宿主数据：

- `.agent/workflow.yaml`
- `.agent/upstream.yaml`
- `.agent/tasks/**`
- `.agent/registry/**`
- `.agent/bin/workflow-check`
- 根 `.git/**`
- 根 `AGENTS.md`
- 任何真实宿主业务文件、CI 配置、业务 Design 或 secrets

发布包可以包含框架 README、分发清单、CI 与 Design 作为 `package_only` 内容，但**不代表这些文件应复制到宿主根目录**。尤其不得用发布包中的 README、CI、Design 或根 `AGENTS.md` 覆盖宿主原有文件。

Source Distribution 必须由干净的精确 Commit 构建，并包含 `MANIFEST.json`（文件路径、SHA256、分类、兼容性、Commit）。归档不得包含绝对路径、`..` 目录穿越、符号链接、硬链接或特殊设备文件。

### 2.2 B. 安装后的 Host

正确完成首次安装后，应具有：

- `.agent/workflow.yaml`：使用 Owner 确认的真实配置，`schema_version: 1`，`git.integration_branch` 为真实集成分支；
- `.agent/upstream.yaml`：记录精确上游 Commit、分发包校验、B 快照位置与最近一次成功升级；
- `.agent/bin/workflow-check`：来自可信、可追溯的匹配构建或经过校验的二进制资产；
- 已安装框架的 Role / Spec / Template / Engine / Adoption 内容；
- `.agent/bin/.gitignore`，保证本地二进制不进入 Git。

Registry 与 Tasks 目录**按需创建**：

- 默认 `resources: {}`，不创建 `.agent/registry/`；
- 默认不创建 `.agent/tasks/`；
- 新 Task 初始资源保持 `resources: {}`；
- 只有项目确实存在需要全局分配冲突控制的共享资源时，才显式启用 Registry。

### 2.3 C. 宿主保护对象

以下对象属于宿主，安装 / 升级不得创建、覆盖、删除、清空或批量重写：

- 根 `AGENTS.md`
- 宿主业务代码、业务测试、构建脚本
- `docs/design/**` 中的宿主业务设计
- 现有根 `README.md`、`PACKAGE_MANIFEST.md`、`.cnb.yml` 与 CI
- `.agent/workflow.yaml`
- `.agent/tasks/**`（含 state、contract、findings、core-logic、owner-decision、delivery、blocked 历史）
- `.agent/registry/**`（含已有分配行与资源预留）
- 已有业务自定义文件和证据
- 未在 `managed` 清单中的宿主文件

`.agent/upstream.yaml` 属于安装来源元数据，不是发布包直接覆盖的文件；只允许在经过授权且成功的安装 / 升级事务中按规范创建或更新，并保留原始记录与恢复信息。

二进制可以根据版本升级，但必须先验证来源、目标版本与兼容性，不能让未知来源的可执行文件覆盖现有版本。首次安装遇到来源不明的既有 binary 时，工具必须 fail closed。

## 3. 首次安装协议

首次安装必须走独立决策路径，不能复用升级的 B/L/N 逻辑。安装入口：

```bash
.agent/adoption/workflow-adoption install \
  --target /path/to/host-repo \
  --package /path/to/agent-workflow-src-<commit>.tar.gz \
  --distribution-sha256 <sha256> \
  --source-commit <full-40-hex> \
  --integration-branch <owner-confirmed-branch> \
  --owner <owner-name> \
  --owner-authorized \
  --execute
```

没有 `--execute` 时只输出 Dry-run Plan，不写宿主内容。安装步骤：

1. **确认目标是正确的宿主 Git 仓库**：目标必须是仓库根，不能是子目录；拒绝 detached HEAD。
2. **确认 Owner 授权接入 Workflow**：必须提供 `--owner` 与 `--owner-authorized`。
3. **核对安装源**：精确 40 位 Commit、分发包 SHA256、`MANIFEST.json`、文件 SHA256 与兼容性（Config Schema 1、State Schema 2/3）。
4. **检查目标路径与已有文件，生成 Install Plan**：列出 create、keep-identical、protected、conflict；不覆盖宿主已有业务文件、配置与 Evidence。
5. **验证 Owner 指定的集成分支真实存在**：必须存在 `origin/<integration_branch>`；不得假设分支永远叫 `develop` 或 `main`。
6. **按 Manifest 安装框架受管理的内容**：只写 `managed` 文件；`package_only` 文件不落宿主。
7. **生成真实 `.agent/workflow.yaml`**：`schema_version: 1`，写入 Owner 分支；默认 `resources: {}`；Registry 仅在显式 `--resource KIND=PATH` 时创建**空表**。
8. **生成精确的 `.agent/upstream.yaml`**：记录来源、Commit、分发包校验、B 快照位置、binary 校验；`release` 必须为空，直到 P08 正式发布。
9. **安装或构建匹配的 `workflow-check`**：默认从分发包内同一 Commit 的 Engine 构建；外部 binary 必须同时提供完整 SHA256 与匹配 Commit，并在替换前通过真实 Gate。
10. **使用真实 Engine Gate 与最小 Task Fixture 验证**：复制配置到临时 Git 仓库，构造最小 `state.yaml`，运行 `workflow-check gate coder-start`；不得调用不存在的 `workflow-check config`。
11. **输出已安装文件清单和验证报告**：报告写入 `.git/workflow-adoption/reports/<txn>.json`，包含 protected 文件前后 SHA256。

安装约束：

- 不得静默修改业务项目的 Git Branch、Remote、CI 或根 `AGENTS.md`；
- 不得创建根 `AGENTS.md`：原本没有，安装后仍然没有；
- 原本已有根 `AGENTS.md`：安装前后 SHA256 必须一致；
- 不得覆盖宿主 `README.md`、`.cnb.yml`、`docs/design/**`；
- 安装前必须输出明确变更计划；失败时保留宿主现场，不能出现无报告的部分覆盖；
- 所有写入先备份到 `.git/workflow-adoption/backups/<txn>/`，失败自动回滚；即使回滚失败也保留备份与报告。

安装后的 E2E 验证至少覆盖：

```text
空宿主无根 AGENTS          → 安装后仍无
已有根 AGENTS              → 前后 SHA256 一致
已有 README / CI / Design  → 不覆盖
workflow.yaml / upstream.yaml → 真实存在且可解析
Owner 指定集成分支          → 写入真实值并通过 Gate
Engine 最小 Task Fixture   → 真实 Gate PASS
Registry                   → 默认不存在；显式启用时为空表
```

## 4. 升级协议：B/L/N

三方含义必须明确：

- **B：Base**，上次实际安装的上游版本内容。
- **L：Local**，当前宿主文件内容。
- **N：New**，本次希望升级到的上游版本内容。

B 必须来自可验证的精确来源快照。**仅知道 B 的 Commit SHA，并不等于已经取得 B 文件的实际内容。** 如果无法读取上次上游的真实文件，属于 `BASE_UNKNOWN`，不得假称完成三方比较，也不得静默覆盖。

B 快照存放在宿主 Git 目录：

```text
<git-dir>/workflow-adoption/snapshots/<B-commit>/
  manifest.json
  files/<managed-path>...
```

`manifest.json` 记录每文件 SHA256 与来源分发包校验；`.agent/upstream.yaml` 的 `snapshot.ref` / `snapshot.manifest_sha256` 指向它。快照缺失或内容损坏时，只能先恢复精确 B 快照或按 B 重新安装，再升级；**不得把远端当前 HEAD 当作上次安装版本**。

### 4.1 三方决策表

| 条件 | 处理 |
| --- | --- |
| L=B，N 已变化 | 可计划更新为 N |
| L=N | 无需修改 |
| B=N，L 有改动 | 保留 L |
| B/L/N 三者不同 | `CONFLICT`，保留 L，等待 Owner |
| B 不可用，L 与 N 不同 | `UNKNOWN_BASE`，不自动覆盖 |
| 上游删除文件，L=B | 可提出删除计划；执行前需 `--allow-deletes` 与备份 |
| 上游删除文件，L 已被修改 | 保留 L，标记冲突 |
| 文件是宿主保护对象 | 保护优先，不因三方比较而自动覆盖 |

宿主特有文件不得因为 N 不包含就被删除。`managed` 路径上存在 B/N 都不包含的本地文件时，同样保留并报告，不按“上游不存在”删除。

### 4.2 升级流程

```bash
.agent/adoption/workflow-adoption upgrade \
  --target /path/to/host-repo \
  --package /path/to/new-source.tar.gz \
  --distribution-sha256 <sha256> \
  --source-commit <new-full-40-hex> \
  --owner <owner-name> \
  --owner-authorized \
  --execute
```

升级必须具备：

1. **变更预览**：逐文件 B/L/N 摘要与决策；Dry-run 不写宿主。
2. **所有权分类**：managed / package_only / protected / generated；protected 优先。
3. **SHA256 记录**：B 快照、L、N、写入结果与 binary 全部记录。
4. **冲突报告**：CONFLICT / UNKNOWN_BASE / 删除未授权必须单独列出。
5. **备份或可验证恢复机制**：写入前备份到 `.git/workflow-adoption/backups/<txn>/`，并提供 `restore` 命令。
6. **经 Owner 授权的实际写入**：必须 `--owner` + `--owner-authorized`。
7. **写入后的校验**：文件 SHA256、`workflow.yaml` 真实 Gate、binary、upstream、快照、protected 不变性。
8. **成功后更新 Provenance**：只有全部验证通过才更新 `.agent/upstream.yaml`。

存在冲突时不得自动覆盖，也不得默默跳过后声称完整升级成功。工具默认 fail closed：整次升级不写入任何框架文件，只输出报告。上游删除必须显式 `--allow-deletes`；否则返回授权错误，不执行部分删除。

## 5. Provenance 与版本兼容

`.agent/upstream.yaml` 至少表达：

- 上游来源（repository / distribution kind / name）；
- 上次安装的精确 Commit；
- 对应的 Framework 版本（`.agent/VERSION` 内容版本）；
- 上次安装使用的分发包或内容校验信息；
- 上次成功升级的记录（`last_upgrade`）；
- 用于定位 B 快照的来源（`snapshot.store` / `ref` / `manifest_sha256`）；
- binary SHA256 与 provenance（`local-build` / `external-verified` / `preserved`）；
- 兼容性（Config Schema 1、State Schema 2/3）。

示例见 `.agent/templates/upstream.yaml.template`。关键原则：

- `.agent/VERSION` 表示框架**内容版本**，由分发包携带；
- `.agent/upstream.yaml` 表示**本宿主实际安装来源**，是来源追溯的唯一权威；
- **不新增 `.agent/engine.version`**：Engine 版本由 upstream Commit、分发包校验与 `.agent/VERSION` 共同确定。再增加一个版本文件会产生两个可能互相冲突的版本结论，收益不足。
- `source.release` 在 P08 创建正式 Tag 前必须为空；不得把远端 HEAD、开发分支或未发布提交描述成正式 Release。

升级必须保留：

- State Schema 2/3；
- Config Schema 1；
- 旧 Task 历史与 state.yaml 字节；
- Contract / Review / Owner / Delivery Evidence；
- Registry 已有行与资源分配；
- blocked / DANGER 历史。

不允许自动批量重写历史 SHA 或历史 Task 状态。

## 6. F-03：历史非法 Evidence 的升级处置

P02 已建立新 Evidence 写入完整 40 位 SHA 的规则。Engine 允许合法的 **7～40 位十六进制 SHA**，以兼容历史短 SHA；但下列旧证据在 V3.1.0 可能被拒绝：

- `HEAD`、`HEAD~1`；
- 分支名；
- 可移动 Tag；
- `origin/*`；
- 十六进制字面值但不存在、有歧义或指向非 Commit 对象；
- 其他非合法 Commit SHA 字面形式。

这是有意的安全收紧，不是兼容性回归。

如果旧 Task 包含非法 Evidence，**不得**在升级时自动将其转换成新 SHA 并宣称原审查仍然有效。正确做法：

1. 识别风险并报告（文件、Task、字段、原值、分类）；
2. 保留原始证据和 Git 历史，不改写 `state.yaml`；
3. 在升级报告中标记相关 Task 需要重新核验（`task_reverification_required`）；
4. 按对应角色与 Owner 决策流程重新建立有效 Evidence；
5. 不能靠改写 state 字符串让旧 `CLEAN`、Owner Acceptance 或 Delivery PASS 直接复活。

兼容矩阵：

| Evidence 字面形式 | Engine 行为 | 升级处置 |
| --- | --- | --- |
| 完整 40 位十六进制 SHA | 接受 | 无需处理 |
| 7～39 位十六进制短 SHA | 接受并解析为完整 SHA | 报告 `legacy_short_sha`；新写入必须用完整 SHA；不自动改写 |
| 十六进制字面值但不存在 / 有歧义 / 指向非 Commit 对象 | 拒绝（无法唯一解析为真实 Commit） | 报告 `invalid_commit_sha`；Task 标记重新核验；保留原证据 |
| `HEAD` / `HEAD~1` | 拒绝 | 报告 `invalid_immutable_ref`；Task 标记重新核验；保留原证据 |
| 分支名 | 拒绝 | 同上 |
| 可移动 Tag | 拒绝 | 同上 |
| `origin/<branch>` | 拒绝 | 同上 |
| 其他非 SHA 字面形式 | 拒绝 | 同上 |

短 SHA 兼容不构成新任务使用短 SHA 的许可。所有新证据必须先解析并核验真实身份，绑定比较统一使用完整 SHA。

## 7. Registry 按需启用

默认安装与升级：

- 不自动初始化有真实分配行的 Registry；
- 不创建 `.agent/registry/`；
- `.agent/workflow.yaml` 的 `resources` 保持 `{}`；
- Task 初始资源保持 `resources: {}`；
- 不改变 Registry 现有行协议；
- 不重写已有预留、历史记录或状态列。

只有项目确实存在需要跨任务协调的共享资源时，才通过显式参数启用：

```bash
--resource migration_version=.agent/registry/migrations.md
```

工具只创建**空表** Registry，写入配置映射；不会生成 `RESERVED` / `ACTIVE` 真实分配行。真实分配仍由 Analyst Registry-only authority 按协作规范落盘。

若真实宿主已经启用 Registry，升级必须保护它：Registry 文件属于 protected 对象，B/L/N 比较不得导致覆盖、删除或重排。

## 8. 工具与操作边界

安装 / 升级工具位于 `.agent/adoption/`，是轻量 Python 3 标准库脚本，不替代 Go Engine，不重造包管理器、远端更新服务或跨平台安装平台。Go Engine 仍是唯一 Gate 实现。

支持命令：

```text
workflow-adoption dist      从干净 Commit 构建确定性 Source Distribution
workflow-adoption inspect   校验分发包成员、Manifest、SHA256 与安全路径
workflow-adoption install   首次安装（Dry-run 默认）
workflow-adoption upgrade   B/L/N 升级（Dry-run 默认）
workflow-adoption verify    校验已安装文件、来源、binary 与真实 Gate
workflow-adoption restore   从事务备份恢复
```

工具保证：

- 首装与升级模式区分；
- 明确目标宿主目录与精确 Commit；
- Dry-run 计划；
- 保护文件清单与前后 SHA256；
- 冲突 / BASE_UNKNOWN / 删除未授权 fail closed；
- 成功后才写 `.agent/upstream.yaml`；
- 失败自动回滚并保留可检查报告与备份；
- 压缩包路径穿越、绝对路径、符号链接、硬链接与特殊文件拒绝解析；
- 外部 binary 必须固定 SHA256 与匹配 Commit，并通过真实 Gate。

退出码：

| code | 含义 |
| --- | --- |
| 0 | 成功 / 计划 |
| 1 | 失败 / 内部错误 |
| 2 | 前置条件、来源或完整性错误 |
| 3 | CONFLICT |
| 4 | BASE_UNKNOWN |
| 5 | 未授权（Owner 授权或删除授权） |

平台边界（诚实声明）：

- Linux + Python 3.11 + Git 2.x + Go 1.23+ 已完成真实 E2E 验证；
- macOS 使用相同 POSIX / Python / Go 代码路径，但本任务未在 macOS 实机验证；
- Windows 原生环境**未验证**：文件模式、符号链接语义与 binary 名称（`workflow-check.exe`）不同。Windows 宿主必须使用 WSL2 或 Linux 容器执行同一流程，或由 Owner 按本文手工步骤安装并在 Linux 环境完成 Gate 验证。不得声称已在 Windows 验证。

## 9. PROJECT_ADAPTATION.md 生命周期

```text
PROJECT_ADAPTATION.template.md
        ↓
创建临时 PROJECT_ADAPTATION.md
        ↓
DISCOVER
        ↓
RESOLVE
        ↓
MATERIALIZE
        ↓
VERIFY
        ↓
CLEANUP
        ↓
删除 PROJECT_ADAPTATION.md
        ↓
ADOPTION READY
```

只有 Adoption 真正完成后才能删除。如果仍有：未确认 Owner 决策、未落地的项目规则、未初始化的 Registry、Validator 无法工作、项目规则冲突、BLOCKER，则 `PROJECT_ADAPTATION.md` 不得删除。

## 10. 五阶段

### 10.1 DISCOVER

Agent 自动扫描项目，不要求 Owner 先填表。Agent 能从事实可靠推断的，直接形成 Proposed Adaptation；无法可靠推断、或属于 Owner Authority 的，才进入 §10.2 的 Owner Decision。

至少检查：

**Repository**

- project name
- repository model
- monorepo / single service
- language(s)
- Go module
- primary branches
- current Git state
- existing branch conventions

**Architecture**

- framework
- service layout
- application layers
- database
- cache
- MQ / stream
- deployment model
- docs/design structure

**Build / Validation**

- build commands
- unit test commands
- integration test commands
- race test
- lint / vet
- migration tests
- Makefile / scripts
- CI pipeline

**Database Migration**

- 是否启用 migration
- migration tool
- migration path
- migration version format
- 是否存在全局版本空间
- 是否已经存在 migration collision control

**Shared Resources**

检查当前项目是否存在需要跨任务协调的：migration version、error code domain，以及其他项目已经明确存在的共享资源。

注意：不要凭空把 port、NATS subject、Kafka topic、metric namespace 全部变成 Registry。只有当前项目确实存在并需要全局分配冲突控制时，才提出。

**Workflow Existing State**

检查 `.agent`、registry、templates、workflow-check、design docs，哪些已经存在，哪些需要初始化；是否存在 `.agent/upstream.yaml`，以判断是首装还是升级。

### 10.2 RESOLVE

DISCOVER 后形成：

- Detected Facts
- Proposed Adaptation
- Unresolved Decisions

Agent 应尽量减少 Owner 提问。只有「无法可靠推断」或「属于 Owner Authority」才问 Owner。例如：

- main 和 develop 都存在，到底谁是 Shared Integration Branch；
- 多服务是共享 migration namespace 还是独立；
- error code 是否需要全局 Registry；
- 某历史习惯是不是正式项目政策；
- 哪个 Design 文件应成为某领域 authority。

每个 Owner Decision 必须说明：

- 检测到什么；
- 为什么不能自己决定；
- 有哪些合理方案；
- 推荐方案；
- 各方案影响。

状态语义：`WAITING_FOR_OWNER_DECISION`。Owner 回答后，继续当前 Adoption Session，**不是 Handoff**。

集成分支必须由 Owner 指定真实值，并在安装前验证 `origin/<branch>` 存在。Agent 不得用 `develop` / `main` 默认值替代。

### 10.3 MATERIALIZE

Owner 确认后，把最终结果写入真正的项目权威位置。禁止「全部留在 `PROJECT_ADAPTATION.md`」。

每个 Adaptation Item 必须有：

- Source
- Decision
- Authoritative Destination
- Materialized Status

示例：

```text
Shared Integration Branch
→ release/integration
→ .agent/workflow.yaml (git.integration_branch)
→ MATERIALIZED

Migration Tool
→ golang-migrate
→ database/project engineering design
→ MATERIALIZED

Migration Registry
→ enabled
→ .agent/registry/migrations.md
→ INITIALIZED (empty table)

Build Command
→ go build ./...
→ existing engineering docs / Makefile
→ VERIFIED
```

具体落点必须结合项目已有结构判断，优先复用现有权威文件，不为 Adoption 新建大量重复 Project Policy 文件。

Adoption 在 Owner Decision 确认后，必须生成 / 更新长期机器配置 `.agent/workflow.yaml`，至少 materialize：

- shared integration branch（`git.integration_branch`）；
- 项目实际声明的 Shared Resource Kinds 的 Registry 路径（`resources.<kind>.registry`，仅项目确实启用对应资源时声明；kind 名必须是合法 `lower_snake_case`，如 `migration_version`、`im_migration_version`）。

`.agent/workflow.yaml` 是长期机器配置，**不是** temporary；`PROJECT_ADAPTATION.md` 仍是 temporary，完成后删除。

#### 宿主根 `AGENTS.md` 保全

Workflow 接入与升级**不得**把 Workflow 内部指令写入宿主根 `AGENTS.md`，也不得创建、覆盖、删除或清空该文件：

- 宿主原本没有根 `AGENTS.md`：接入后仍然没有，**不创建**；
- 宿主原本已有根 `AGENTS.md`：**保留原有内容**，接入前后文件 SHA256 必须一致；
- 旧版本 Workflow 可能已把 `AGENTS.template.md` 的内容写入宿主根 `AGENTS.md`：先分析内容差异，只输出待迁移项（可迁移的 Workflow 指令应迁往 `.agent/specs/WorkflowAgentDirective.md`），**不得自行删除**宿主业务规则；
- 对已有根文件的清理、改写或迁移必须取得 Owner 明确授权，并记录在 `PROJECT_ADAPTATION.md` 的宿主根 `AGENTS.md` 保全记录中；
- 不得通过覆盖整个文件来完成升级。

Workflow Role 在该文件存在时读取其中的宿主业务规范；不存在时不阻断、不补建。本次框架变更不对任何实际宿主项目执行迁移。

### 10.4 VERIFY

必须验证迁移后的 Workflow 真正可以工作。

**Workflow structure**

- 七个 Role Prompt 存在（`TaskBuilderPrompt.md`、`AnalystAgent.md`、`ArchitectAgent.md`、`CoderAgent.md`、`CleanerAgent.md`、`OwnerGateAgent.md`、`DelivererAgent.md`）；
- `.agent/specs/WorkflowAgentDirective.md` 存在，且七个 Role Prompt 都显式要求读取它；
- 不存在指向已删除模板 `AGENTS.template.md` 的有效入口；
- Workflow Design 存在（宿主可按 Owner 决策保留或引用）；
- task/template 结构完整；
- Registry 状态合法（未启用时不要求存在）；
- 宿主根 `AGENTS.md` 的存在性与 SHA256 与接入前一致（或差异已获 Owner 授权并记录）。

**Git model**

- Shared Integration Branch 明确且 `origin/<branch>` 存在；
- Owner branch lifecycle authority 明确；
- Analyst Registry-only exception 能够映射到当前项目。

**Workflow Engine（`.agent/engine/`，独立 Module）**

P3 起 Workflow Engine 是 source-vendored 独立 Go Module（module `workflow-v2-engine`），不依赖宿主业务 Go Module。VERIFY 至少检查：

- `.agent/engine/go.mod` 存在，`module workflow-v2-engine`；
- Engine 能独立 build：`go -C .agent/engine build ./...`；
- 本地 binary 能生成：`go -C .agent/engine build -o ../bin/workflow-check ./cmd/workflow-check`；
- build 后 Git working tree 不因 binary 变 dirty（`.agent/bin/` 被 ignore）；
- `.agent/bin/workflow-check` 能读取 materialized `.agent/workflow.yaml` 并执行基础 Gate；
- Engine 不依赖宿主业务 Go Module（无业务 module import、无 `go.work`、无 root `replace`）。

**Machine Configuration**

验证 `.agent/workflow.yaml`：

- 可解析、`schema_version: 1`；
- integration branch 可用（`origin/<integration_branch>` 存在）；
- 声明的 Registry 可在 integration branch 上读取；
- `workflow-check` 能基于该配置工作。

**Install / Upgrade verification**

- `.agent/upstream.yaml` 的 Commit 与分发包精确一致；
- B 快照存在且每文件 SHA256 匹配；
- 安装 / 升级报告记录 protected 前后 SHA256；
- 真实 Gate Fixture PASS（不得调用不存在的 `workflow-check config`）；
- 旧 Task / Registry / Evidence 字节保持不变；
- 非法历史 Evidence 已报告且未改写。

**Project validation**

使用项目实际定义的验证方式，而不是硬编码当前 my-shop 的 `go test ./...`。如果项目实际是 `make test`、`go test ./services/...`、`helm lint` 等，则使用真实项目规则。

### 10.5 CLEANUP

只有以下全部满足才能 Cleanup：

- 所有 Required Adaptation 已确定；
- Owner Decisions 已解决；
- 所有长期信息已 Materialize；
- Registry 已初始化 / 确认（或明确未启用）；
- Validator 可运行或已明确 NOT_APPLICABLE / 有正式处理；
- Verification PASS；
- 无 BLOCKER；
- `PROJECT_ADAPTATION.md` 中没有只存在于临时文件而未落地的重要事实。

然后删除临时 `PROJECT_ADAPTATION.md`，保留 `PROJECT_ADAPTATION.template.md` 与本文档。

```text
ADOPTION RESULT: READY
PROJECT_ADAPTATION.md: REMOVED
```

失败则：

```text
ADOPTION RESULT: NOT_READY
PROJECT_ADAPTATION.md: RETAINED
```

## 11. Agent 调查 vs Owner 决策分工

不要做成「Owner 先填写几十项表格」。正确模式：

1. Agent 先主动扫描项目，把能从事实可靠推断的直接形成 Proposed Adaptation（Language、Go module、framework、repository layout、database、migration tool / directory、Makefile、CI commands、existing tests、current branches、docs/design structure、Registry existing state 等）。
2. Agent 无法可靠推断、或属于 Owner Authority 的，才进入 `WAITING_FOR_OWNER_DECISION`。
3. Owner 在当前 Adoption Session 中回答，原 Session 继续，这不是 Handoff。

## 12. Migration / Registry 关键抽象

当前 my-shop 使用 `golang-migrate` + 全局 migration version Registry + error code domain Registry，但**这些不是所有项目都必须有**。不要把 Workflow V3 Core 写死成「所有项目都必须有 migrations.md 和 error-codes.md」。

Adoption 必须逐项判断：

### Migration

`ENABLED / DISABLED / NOT_APPLICABLE`。如果 ENABLED，确定：

- tool
- path
- identity / version strategy
- namespace
- Registry 是否需要

多服务 Monorepo 必须允许 Owner 决定：

- A. 整个 repo 共用 migration version namespace；
- B. 每个 service 独立 namespace。

不能由 Agent 擅自决定。

### Error Code Domain

`ENABLED / DISABLED / NOT_APPLICABLE`。有些项目根本没有全局 numeric error domain，此时 `NOT_APPLICABLE`，不要为了 Workflow 一致性强行创建。

## 13. 当前 Validator 已知项目耦合（源项目）

本节记录**当前 my-shop 源项目**里 `.agent/engine/`（`workflow-check` / `internal/workflow`）的已知耦合。前三项已由 P1 / P2 / P3 解决；其余保留，本轮不重构。

| 位置 | 当前耦合 | 状态 |
| --- | --- | --- |
| integration branch | 原硬编码 `origin/develop` | 已解决（P1）：由 `.agent/workflow.yaml` 的 `git.integration_branch` 声明 |
| Registry 路径 | 原硬编码 `.agent/registry/migrations.md` / `error-codes.md` | 已解决（P1）：由 `.agent/workflow.yaml` 的 `resources.*.registry` 声明 |
| `.agent/engine/internal/workflow/state.go` `Resources` | 硬编码 `Migrations` / `ErrorCodeDomains` 两个字段 | 已解决（P2）：泛化为 `Resources.Reservations map[string][]string`，schema v2 legacy 由兼容层 normalize |
| `.agent/engine/internal/workflow/paths.go` | 硬编码 `.agent/tasks/<task>/` 根路径 | 保留（Core convention，本轮不配置化） |
| `scripts/check-registry.sh` | 硬编码 `internal/codes/codes.go`、`internal/migrations/sql`、错误码 `CodeOK == 0`、14 位数字 migration version | 保留（Project Policy，非 Engine runtime） |
| Go module path | 原 `cnb.cool/go-cloud-devops/my-shop`（import path） | 已解决（P3）：Engine 独立 module `workflow-v2-engine`，不再依赖 my-shop module |

上述未解决项**不在本轮重构**，留待后续正式打包设计时单独决定。

## 14. Release Consumer Contract（给 P08）

本节是 P07 交给 P08 的最小发布契约；真正的 `v3.1.0` Tag、CI 构建、校验和与 Release 资产由 P08 负责。**本任务不创建 Tag、Release 或正式二进制资产，也不引用尚不存在的 `v3.1.0` Release 地址。**

### 14.1 Source Distribution 必须包含

- `MANIFEST.json`（format、kind、framework_version、source.commit、tree、compatibility、files/SHA256/install 分类、release 占位）；
- `.agent/roles/**`、`.agent/specs/**`、`.agent/templates/**`、`.agent/engine/**`、`.agent/adoption/**`（`managed`）；
- `.agent/README.md`、`.agent/VERSION`、`.agent/bin/.gitignore`；
- 根 `README.md`、`PACKAGE_MANIFEST.md`、`.cnb.yml`、`docs/design/agent-workflow.md`、`.agent/adoption/tests/**`（`package_only`）。

### 14.2 绝不能打进发布包

- `.agent/workflow.yaml`
- `.agent/upstream.yaml`
- `.agent/tasks/**`
- `.agent/registry/**`
- `.agent/bin/workflow-check`
- 根 `.git/**`
- 根 `AGENTS.md`
- 宿主业务代码、业务 CI、业务 Design、secrets

### 14.3 Release Binary 与位置

- 宿主位置：`.agent/bin/workflow-check`，模式 `0755`；`.agent/bin/.gitignore` 已保证不进入 Git。
- Release 资产应由与 `source.commit` 相同的 Engine 源码构建；P08 应发布 binary 的 SHA256。
- 外部 binary 安装必须传：

```bash
--workflow-check /path/to/workflow-check \
--workflow-check-sha256 <sha256> \
--workflow-check-commit <full-40-hex-equal-to-source-commit>
```

- 工具会在写入前运行真实 Gate fixture；不匹配、无固定 SHA256 或 Gate 失败的 binary 不得覆盖宿主现有版本。

### 14.4 如何核验匹配版本与 Commit

```bash
.agent/adoption/workflow-adoption inspect \
  --package <archive> \
  --expected-sha256 <published-sha256>

GOWORK=off go -C .agent/engine test -count=1 ./...
GOWORK=off go -C .agent/engine vet ./...
git diff --check
```

- `MANIFEST.json.source.commit` 必须等于 Owner 指定的完整 40 位 Commit；
- `framework_version` 来自 `.agent/VERSION`；当前源码版本为 `3.1.0`，正式 Tag 由发布流水线创建，源码版本标记不等于已发布；
- `compatibility` 必须为 Config Schema 1、State Schema 2/3；
- 归档成员必须与 Manifest 完全一致，且不含宿主数据。

### 14.5 如何记录安装来源

只允许由成功的 install / upgrade 事务写入 `.agent/upstream.yaml`。字段见 `.agent/templates/upstream.yaml.template`。不得手工把远端 HEAD 写入 `source.commit`。`source.release` 在正式 Tag 前为空。

### 14.6 何时允许更新 `.agent/upstream.yaml`

- 首次安装事务成功并通过全部校验；
- 升级事务无 CONFLICT / BASE_UNKNOWN / 未授权删除，且写入后校验全部通过；
- 不得在 Dry-run、失败回滚、冲突或仅预览时更新。

### 14.7 如何验证 Source Distribution 与 Host 最终状态

```bash
# 1. 分发包洁净与分类
.agent/adoption/workflow-adoption inspect --package <archive> --expected-sha256 <sha>

# 2. 宿主安装状态（文件、binary、真实 Gate）
.agent/adoption/workflow-adoption verify --target <host>

# 3. Engine 自身验证
GOWORK=off go -C .agent/engine test -count=1 ./...
GOWORK=off go -C .agent/engine vet ./...
```

`verify` 必须检查：B 快照每文件 SHA256、binary SHA256、`workflow.yaml` 真实 Gate、历史非法 Evidence 报告。任何一项失败不得报告安装 / 升级成功。

## 15. 禁止事项

- 不创建 `v3.1.0` GA Tag、Release 或正式二进制资产；不改 `.agent/VERSION` 冒充已发布；
- 不把远端当前 HEAD 当作上次安装的 B；
- 不制作包含宿主 `.agent/workflow.yaml` / `.agent/upstream.yaml` / tasks / registry / binary 的发布包；
- 不用发布包 README / CI / Design / 根 `AGENTS.md` 覆盖宿主文件；
- 不执行当前 my-shop 的 live Adoption、不创建 live `PROJECT_ADAPTATION.md`；
- 不新增第七 Agent；
- 不创建、不覆盖、不删除、不清空宿主根 `AGENTS.md`，不把内部 Workflow 指令写入宿主业务规范（Owner 明确授权的一次性迁移除外）；
- 不修改正常 Workflow state schema（不新增 Adoption 状态到 state.yaml）；
- 不修改 INV-1 ~ INV-7、五个 Gate、Evidence Snapshot、Neutral Tail、Registry Authority；
- 不自动批量重写历史 Evidence SHA、Task 状态、Registry 行或 blocked / DANGER 历史；
- 不修改业务代码、历史 Task Artifact、当前 live Registry；
- 不修改 Go Engine 使其承担文件安装器职责。

## 16. 模板

- `.agent/templates/PROJECT_ADAPTATION.template.md`：Adoption 工作记录、Owner Decision Checkpoint、Materialization checklist、Verification checklist。
- `.agent/templates/workflow.yaml.template`：机器配置模板（Config Schema 1）。
- `.agent/templates/upstream.yaml.template`：安装来源与 B 快照字段说明。
- `.agent/templates/registry/registry.template.md`：按需启用的空 Registry 模板。

`PROJECT_ADAPTATION.template.md` 不是「Owner 手填完的表」，而是 Agent 工作记录与 Owner 决策检查点。
