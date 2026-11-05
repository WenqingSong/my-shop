# Workflow Adoption（V4）

> 把 V4 框架安装到宿主 Git 仓库，并在后续版本中安全升级、回滚与校验。
> 工具：`.agent/adoption/workflow-adoption`（Python 3.10+，无第三方依赖）。

## 1. 分发形态

V4 是 **Source Distribution**：只分发源码与规范，不含编译产物，不含 Engine 二进制。

```text
agent-workflow-src-<version>.tar.gz
agent-workflow-src-<version>.tar.gz.sha256
archive top-level: agent-workflow-src-<version>/
  MANIFEST.json
  README.md
  PACKAGE_MANIFEST.md
  .agent/**
  docs/**
  scripts/release/**
```

`MANIFEST.json` 至少包含：

- `format` / `kind` / `framework_version`
- `source.repository` / `source.commit` / `source.tree`
- `files[]`：`path` / `sha256` / `mode` / `install`（`managed` 或 `package_only`）
- `excluded[]`
- `release.tag` / `release.url`（构建时为空，发布工具填写）

## 2. 文件分类

| 分类 | 内容 | 进包 | 安装到宿主 |
| --- | --- | --- | --- |
| `managed` | `.agent/specs/**`、`.agent/roles/**`、`.agent/templates/**`、`.agent/tools/**`、`.agent/adoption/**`（`tests/` 除外）、`.agent/README.md`、`.agent/VERSION` | 是 | 是，参与升级 |
| `package_only` | 根 `README.md`、`PACKAGE_MANIFEST.md`、`.cnb.yml`、`docs/**`、`scripts/release/**`、`.agent/adoption/tests/**` | 是 | 否 |
| `generated` | `.agent/upstream.json` | 否 | 仅由成功事务写入 |
| `protected` | 宿主 `AGENTS.md`、宿主 README / CI / 业务代码、`.agent/tasks/**`、`.agent/registry/**`、宿主 `.git/**` | 否 | 永不覆盖或删除 |

## 3. 接入流程

```text
DISCOVER → RESOLVE → MATERIALIZE → VERIFY → CLEANUP
```

1. **DISCOVER**：读取宿主已有 `.agent/`、任务目录、`AGENTS.md`、CI 配置、构建命令；确认是否已有旧版本。
2. **RESOLVE**：与 Owner 确认集成分支、验证命令、受保护路径。有歧义时不得自行假定。
3. **MATERIALIZE**：安装 `managed` 文件；不创建 `tasks/`，不创建 registry，不改宿主 CI 与 README。
4. **VERIFY**：`workflow-adoption verify --target <host>` 通过，且 `git status` 可解释。
5. **CLEANUP**：删除临时文件与适配草稿；把接入事实写进宿主 `decisions.md` 或等价记录。

## 4. 安装

```bash
.agent/adoption/workflow-adoption install \
  --target /path/to/host \
  --package dist/agent-workflow-src-4.0.0.tar.gz \
  --distribution-sha256 <sha256> \
  --source-commit <40-hex> \
  --integration-branch <owner-confirmed-branch> \
  --owner <owner> --owner-authorized \
  --execute
```

行为：

- 默认 Dry-run，输出计划；`--execute` 才写文件。
- 校验包 SHA256、`MANIFEST.json`、`source_commit` 与包内一致性。
- 已存在同名 `managed` 文件且内容不同 → 失败（`ALREADY_INSTALLED`），提示改用 `upgrade`。
- 成功后写 `.agent/upstream.json`：`framework_version`、`source_commit`、`distribution_sha256`、`installed_at`、`files{path: sha256}`。
- 不创建、不覆盖、不删除宿主 `AGENTS.md`；不修改宿主业务文件。

## 5. 升级（B / L / N）

B = 上次安装记录的快照哈希；L = 新包；N = 当前宿主。

| 情况 | 判定 | 动作 |
| --- | --- | --- |
| 宿主文件 == B，新包有该文件 | 未本地修改 | 覆盖为 L |
| 宿主文件 == B，新包无该文件 | 上游删除 | 删除（记录为 `removed`） |
| 宿主文件缺失，新包有该文件 | 新文件 | 创建 |
| 宿主文件 != B，新包同路径内容 == B | 仅本地修改，上游未变 | 保留本地，报告 `kept_local` |
| 宿主文件 != B，新包内容 != B 且 != 宿主 | 真实冲突 | **整体失败**（`CONFLICT`），任何文件都不写入 |
| 宿主文件 != B，新包无该文件 | 上游删除但本地已改 | **整体失败**（`CONFLICT`） |

要求：

- `upstream.json` 缺失或损坏 → `BASE_UNKNOWN`，拒绝升级，不得用远端 HEAD 或猜测代替 B。
- 事务性：先把所有要写的文件暂存并做备份，全部校验通过后一次性替换；任一步失败自动回滚。
- 冲突解决只能由 Owner 决定：保留本地（`--keep-local <path>`，会从新包中排除该文件）或采用上游（先手工备份本地修改）。
- 成功后更新 `upstream.json`。

## 6. 回滚

```bash
.agent/adoption/workflow-adoption rollback --target <host> --execute
.agent/adoption/workflow-adoption rollback --target <host> --transaction <id> --execute
```

- 备份位于 `<host>/.agent/.transactions/<txn-id>/`，包含被覆盖与删除文件的原始副本及 `plan.json`。
- 回滚按 `plan.json` 逆序恢复，并恢复 `upstream.json`。
- 只允许回滚最近一次成功事务，或显式指定存在的 `txn-id`；不得跨版本跳跃猜测。

## 7. 校验

```bash
.agent/adoption/workflow-adoption verify --target <host>
.agent/adoption/workflow-adoption inspect --package <archive> --expected-sha256 <sha>
.agent/adoption/workflow-adoption check --repo <framework-repo>
```

`verify` 检查：

1. `upstream.json` 存在且可解析；
2. 所有 `managed` 文件存在，哈希与记录一致；
3. `.agent/VERSION` 与记录一致；
4. 没有遗留的 V3 机制文件（`engine/`、`workflow.yaml`、`state.yaml`、`bin/workflow-check`）。

`check` 是**仓库一致性静态检查**：文件集合、规范与角色完整性、禁止术语、任务记录必需文件、历史归档存在性。它是文本与文件检查，不解析任务状态，不构成新的工作流引擎。

## 8. 宿主责任

宿主负责：`AGENTS.md`、业务代码与测试、CI、任务记录内容、集成分支与发布。

框架负责：规范与角色 Prompt 的正确性、工具的安装/升级/回滚/校验能力、发布资产的可核验性。

## 9. 已知边界

- 工具验证的是文件与哈希事实，不能证明 Agent 遵守了规范。
- 不校验 Agent 身份与 Session 独立性。
- 不做自动合并、不做自动发布、不修改宿主 Git 历史。
- Windows 原生未验证；使用 WSL2 / Linux。
