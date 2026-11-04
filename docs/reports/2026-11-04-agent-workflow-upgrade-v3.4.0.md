# Agent Workflow 3.2.0 → 3.4.0 升级报告

- 执行日期：2026-11-04
- 上游：`https://cnb.cool/framework-station/agent-workflow`
- 升级前：`3.2.0`（`ccd95f8c6e2191b4f4a13019ce15bd7dfa251e01`，`refs/tags/v3.2.0`）
- 升级后：`3.4.0`（`263194ba83e72c1fc67f177fb22e9bffeff0780e`，`refs/tags/v3.4.0`）

## 1. 升级前的实际安装状态（核查结论）

标准 `upgrade` 流程在核查阶段即被工具拒绝，原因不是内容冲突，而是宿主 provenance 不符合工具契约：

| 检查项 | 升级前实际状态 | 工具契约要求 |
| --- | --- | --- |
| `.agent/upstream.yaml` 结构 | 文档式记录（`repository` / `commit` / `previous` / `history` / `migration` / `divergence` / `compat`，含 YAML 列表） | 机器式 `source` / `installed` / `snapshot` 映射，解析器对 YAML 列表 fail closed（`YAML_UNSUPPORTED`，第 24 行） |
| B 快照 `.git/workflow-adoption/snapshots/<commit>/` | 不存在（目录下只有 `reports/`） | 升级必须读取 B；缺失即 `BASE_UNKNOWN`，禁止用远端 HEAD 代替 |
| `.agent/bin/workflow-check` | 不存在 | `verify` 需要 binary 与其 SHA256 记录 |

结论：本次安装是**手工/文档方式**落地 3.2.0 的，未经 adoption 工具事务，因此工具无法证明上次安装的确切内容。

## 2. 基线核验（用精确 commit 还原 B，未使用远端 HEAD）

- 从上游 commit `ccd95f8c6e2191b4f4a13019ce15bd7dfa251e01`（= `refs/tags/v3.2.0` peel 后同一提交）重建源码分发包：
  - `agent-workflow-src-ccd95f8c6e21.tar.gz`
  - `sha256 = 00a0578749525d142688f762903009124c7808e226d13280d772541ae3d9dffc`
  - `framework_version = 3.2.0`，managed 文件 53 个
- 宿主 `.agent` 与上述 B 逐字节比对（排除 `tasks/`、`registry/`、`bin/`、`workflow.yaml`、`upstream.yaml`）：**无任何差异**，即受管文件上 L = B，不存在本地修改，升级不会产生 CONFLICT。
- 上游 `v3.2.0 → v3.4.0` 在 `.agent` 下**没有删除任何文件**，因此无需 `--allow-deletes`。
- 保护对象不变：`.agent/workflow.yaml`、`.agent/tasks/**`、`.agent/registry/**`、根 `AGENTS.md`（不存在）均不参与写入。

## 3. 本次处置

1. 按精确 commit 重建 B 快照（由工具自身 `write_snapshot` 生成，格式与运行时一致）：
   - 路径：`.git/workflow-adoption/snapshots/ccd95f8c6e2191b4f4a13019ce15bd7dfa251e01/`
   - `manifest_sha256 = 3f797edb82d76f855279b3556b1ef3ef5951e2015e368ce676118337ebc5ab02`
   - 文件数 53
2. 将 `.agent/upstream.yaml` 规范化为工具 schema（`source` / `installed` / `snapshot` / `runtime` / `compatibility`），原文档式内容在本报告第 4 节逐字归档。
3. 执行 `workflow-adoption upgrade`（Dry-run → `--execute`），由事务完成写入、备份、真实 Gate 校验与 provenance 更新。
4. 执行 `workflow-adoption verify` 校验落盘结果。

## 4. 原 `.agent/upstream.yaml`（升级前逐字归档）

```yaml
schema_version: 1
repository: https://cnb.cool/framework-station/agent-workflow
commit: ccd95f8c6e2191b4f4a13019ce15bd7dfa251e01
version: "3.2.0"
subdirectory: .agent

# 本次采用的远端引用：refs/tags/v3.2.0（附注 tag）peel 后指向 ccd95f8c，
# 与 refs/heads/main、远端 HEAD 为同一提交。
# 核验方式：git ls-remote --tags / git merge-base，2026-10-29。
ref: refs/tags/v3.2.0
tag: v3.2.0
tag_object: 2ef026da5a22f1955ecc732af53e569bcb3a86d6

# 上一版本地基线：上游 4.x 线提交 229edfd（"release: finalize Agent Workflow v4.3.0"）。
# 重要事实：该提交在上游没有 tag，也不被任何远端分支包含；上游发布线（main / 全部 tag）
# 停在 v3.2.0。3.2.0 与 4.3.0 互不为祖先，共同祖先为 7727e0a（= v3.0.0）。
# 因此本次不是版本回退，而是切换世代：3.x 发布线 ↔ 4.x 未发布线。
previous:
  commit: 229edfd18e3b6e770f803db01e5010b215ba623f
  version: "4.3.0-unreleased"
  verified_by: git merge-base ccd95f8c 229edfd = 7727e0a；is-ancestor 双向均为 false

history:
  - commit: 229edfd18e3b6e770f803db01e5010b215ba623f
    version: "4.3.0-unreleased"
  - commit: 0c62d1c07bc9a98120c7a11b9cd3bbeae104a2f0
    version: "4.3.0-dev"
  - commit: 1fc5c6dc88fc7f417374e37e7354a3a6c4e07a65
    version: "4.2.1"
  - commit: 7727e0a64f47bc33b6f042f27421e1889224e7f3
    version: "3.0.0"

# 本次迁移范围：框架资产整体替换为 v3.2.0，宿主运行数据迁移。
migration:
  framework_replace:
    - 删除 4.x 框架资产：engine/（六 CLI）、roles/{complex,design,legacy}、mastery/、index/、
      specs/TaskLayoutExtension.md、AGENTS.md、assistant-context.md、LICENSE
    - 写入 v3.2.0 全量资产：engine/（workflow-check 单 CLI）、roles/（七角色平铺）、
      specs/、templates/、adoption/、bin/.gitignore、README.md、VERSION
  task_migration:
    - Task 目录由 <category>/<id>/ 拍平为 .agent/tasks/<id>/（v3.2.0 路径约定）
    - 51 个 Task → 29 个可被 v3.2.0 引擎解析并保留；22 个移入 .agent/tasks/_archive/
      其中 19 个无 state.yaml（仅文档证据），3 个 state schema_version=0（不可解析）
    - 分类索引 layout.yaml / layout-journal.yaml 移入 .agent/tasks/_archive/_layout/ 作为历史记录
  preserved_host_assets:
    - .agent/workflow.yaml（schema 1，integration_branch=develop，两个 Registry kind）
    - .agent/registry/migrations.md、.agent/registry/error-codes.md
    - .agent/tasks/（运行数据与证据，逐字节保留）

# 保留的项目本地定制与项目拥有资产（不随框架更新覆盖）：
local_changes: |-
  workflow.yaml：项目机器配置（integration_branch=develop、migration_version 与 error_code_domain 两个 Registry）。
  registry/、tasks/：项目运行数据与共享资源事实源。
  根 AGENTS.md 已于 2026-10-18 按 Owner 指令删除，业务工程规范位于 docs/design/engineering-guidelines.md。

# 与上游 3.2.0 的差异（不得伪称为上游功能）：
divergence:
  - 上游 3.2.0 无 Task 分类目录机制；本项目的分类信息仅保留在 _archive/_layout 历史记录中，不再参与任何校验。
  - 22 个归档 Task 不具备 v3.2.0 可校验的 state.yaml，不得对其调用 workflow-check gate。

# 未解决的兼容限制：
compat:
  - 29 个保留 Task 均无 state.route 事实（4.x 迁移前未记录），Engine 按 Legacy 处理：
    workflow-check route <task> 需显式 --allow-legacy；不补造 route，避免 R-3 与 task.md 不一致。
  - 3.2.0 缺少 workflow-deliver / workflow-event / workflow-git / workflow-program / workflow-status，
    任务查询与交付由文档与人工流程承担。
  - 3.2.0 上游自述 NOT_RELEASED，且 Cross-project Adoption 为 NOT_PERFORMED。
```

## 5. 升级结果

`upgrade --execute` 结果：

| 项 | 值 |
| --- | --- |
| STATUS | `UPGRADED` |
| 事务 / 备份 | `20261104T082133Z-d613866f` |
| 报告 | `.git/workflow-adoption/reports/20261104T082132Z-35422cff.json` |
| 源分发包 | `agent-workflow-src-263194ba83e7.tar.gz`（`d8c41775…e2be5`） |
| 写入 | 19 个 `update`（roles / specs / adoption / README / VERSION / git-lifecycle-check），无删除、无 conflict |
| `workflow_check` | 由分发包内 Engine 本地构建（`provenance=local-build`），`version` 报告 `3.4.0` / `build_source=source` |
| 事务内校验 | `preflight_gate` / `files` / `upstream` / `snapshot` / `gate` 全部 `PASS`，`protected_unchanged=True` |

升级后独立校验 `workflow-adoption verify`：

```text
STATUS: VERIFIED
source_commit: 263194ba83e72c1fc67f177fb22e9bffeff0780e
framework_version: 3.4.0
verify.files: PASS
verify.binary: PASS
verify.gate: PASS
verify.config_schema: 1
verify.state_schema_preserved: 2,3
```

回归检查：`GOWORK=off go -C .agent/engine test ./...` → `cmd/workflow-check`、`internal/workflow` 均 `ok`。

新 provenance 要点：`source.commit = 263194ba83e72c1fc67f177fb22e9bffeff0780e`，
`snapshot.ref = snapshots/263194ba83e72c1fc67f177fb22e9bffeff0780e`（53 个受管文件），
`last_upgrade.from_commit = ccd95f8c…`。

## 6. 保留事项与边界

- `.agent/workflow.yaml`、`.agent/tasks/**`、`.agent/registry/**` 未被修改（事务 `protected_unchanged=True`）。
- 29 个保留 Task 仍无 `state.route` 事实，`workflow-check route <task>` 仍需 `--allow-legacy`；本次升级不补造 route。
- 其他文档中 `3.2.0` 的表述：仅“当前版本”类陈述已同步为 `3.4.0`（根 `README.md`、`docs/design/agent-workflow.md`、`docs/task-management/README.md` 首段）；带日期的历史叙述与行为性说明保持原样，未改写。
- 升级结果（19 个框架文件 + `upstream.yaml`）在本次操作结束时尚未提交，按项目 Git 流程由 Owner 决定提交时机。
