# Agent Workflow 3.4.0 → 4.0.0 升级报告

- 执行日期：2026-11-05
- 上游：`https://cnb.cool/framework-station/agent-workflow`
- 升级前：`3.4.0`（`263194ba83e72c1fc67f177fb22e9bffeff0780e`，`refs/tags/v3.4.0`）
- 升级后：`4.0.0`（`304bdb26b4c46f428e14e6067159f33505e14f7d`，`refs/tags/v4.0.0`）
- 世代变化：V4 是结构性重写，删除 Workflow Engine、`state.yaml`、五个 Gate、Stage 推导、Route 选择与 Registry 机器机制。

## 1. 上游版本核查

| 引用 | Commit | 结论 |
| --- | --- | --- |
| `refs/tags/v4.0.0`（附注 tag `28836ff`） | `304bdb26b4c46f428e14e6067159f33505e14f7d` | 新增发布，`VERSION` = `4.0.0` |
| `refs/heads/main` | `239450d24665a85e4ea2d466e906edfd66bf287d` | 仍在 `v3.4.0`（`263194b`）之后 1 个提交，仅改 `.cnb.yml`（CI 镜像推送） |
| `refs/tags/v3.4.0` | `263194ba83e72c1fc67f177fb22e9bffeff0780e` | 升级前安装版本 |

`git merge-base --is-ancestor 263194b 304bdb2` = true，两版本为直接演进关系，不存在分叉世代。

## 2. 基线与包构建（精确 commit，未使用远端 HEAD）

- 升级前安装由 V3 工具完成，provenance 在 `.agent/upstream.yaml` + `.git/workflow-adoption/snapshots/<commit>/`；宿主 Git 目录重建后快照目录缺失，`workflow-adoption verify` 报 `找不到上次安装的 B 快照`，V4 工具则要求 `.agent/upstream.json`。两种形式的 B 都不可读，因此先重建 B。
- 用 v3.4.0 自带 `dist` 从精确 commit `263194b` 重建源码分发包：
  - `agent-workflow-src-263194ba83e7.tar.gz`
  - `sha256 = d8c417758e0e4078691fff7b398f4e27af8763e610c5619adf2f58a1901e2be5`（与 `.agent/upstream.yaml` 记录**完全一致**，确定性构建得证）
  - managed 文件 53 个
- 逐字节比对宿主 53 个受管文件与 B：**53/53 完全一致**（无本地修改，升级不会产生 `CONFLICT`）。
- 按 V4 `upstream.json` schema（`format: 1` + `files{path: sha256}`）写入重建的 B，并附 `reconstruction` 字段说明来源；该文件随后被升级事务整体覆盖。
- 用 v4.0.0 自带 `dist` 从精确 commit `304bdb2` 构建目标包：
  - `agent-workflow-src-4.0.0.tar.gz`
  - `sha256 = 87b68de528b473099bebf1e39c4c6459abc8b0cf842fdcebc890ec398788f221`
  - 成员 90 个，其中 managed 30 个

## 3. 升级计划（Dry-run 结果，与实执行一致）

| 类别 | 数量 | 内容 |
| --- | ---: | --- |
| create | 9 | `roles/README.md`、`templates/task/*.md`（6）、`templates/upstream.json.template`、`tools/git-facts` |
| overwrite | 19 | `README.md`、`VERSION`、`adoption/{README.md,workflow_adoption.py}`、7 个角色 Prompt、7 份规范、`templates/PROJECT_ADAPTATION.template.md` |
| delete | 32 | `.agent/engine/**`（25）、`bin/.gitignore`、`specs/WorkflowRouteSelection.md`、`specs/tools/git-lifecycle-check`、`templates/registry/registry.template.md`、`templates/upstream.yaml.template`、`templates/workflow.yaml.template` |
| conflict / kept_local | 0 | 无本地修改，无需 Owner 裁决 |

未使用 `--keep-local`；工具无 `--allow-deletes` 参数，删除随 B/L 三方比较自动执行（`host == B` 且上游删除）。

## 4. 执行

1. 执行 `workflow-adoption upgrade --execute`（使用分发包内 v4.0.0 工具，`--integration-branch develop`、`--owner owner`）：
   - 事务：`.agent/.transactions/20261105T065537Z-upgrade/`（`plan.json` + 53 个备份文件）
   - 结果：写入 28 个文件、删除 32 个文件，`previous_version = 3.4.0`
   - 生成 `.agent/upstream.json`：`framework_version=4.0.0`、`source_commit=304bdb2…`、`distribution_sha256=87b68de5…`、30 个受管文件哈希
2. 事务按文件删除，不回收目录，手工清理空目录：`.agent/engine/**`、`.agent/bin`、`.agent/specs/tools`、`.agent/templates/registry`。
3. 删除 V3 机制文件（宿主拥有，不在事务范围内）：`.agent/workflow.yaml`、`.agent/upstream.yaml`；内容在第 6 节逐字归档。
4. `.gitignore` 增加 `/.agent/.transactions/`（V4 约定：事务备份为本机审计材料，不入库）。

## 5. 校验

```text
$ .agent/adoption/workflow-adoption verify --target .
target: /root/projects/my-shop
version: 4.0.0
source_commit: 304bdb26b4c46f428e14e6067159f33505e14f7d
files: 30
status: PASS
```

`verify` 同时检查 V3 机制残留（`.agent/engine`、`.agent/bin`、`.agent/workflow.yaml`、`specs/WorkflowRouteSelection.md`、`specs/tools`、`templates/workflow.yaml.template`、`templates/registry`）——全部不存在。

## 6. 被删除的宿主文件（逐字归档）

`.agent/workflow.yaml`（V3 项目配置，其 `integration_branch: develop` 已由 `--integration-branch` 记入 `upstream.json`）：

```yaml
schema_version: 1

git:
  integration_branch: develop

resources:
  migration_version:
    registry: .agent/registry/migrations.md

  error_code_domain:
    registry: .agent/registry/error-codes.md
```

`upstream.yaml`（V3 provenance，已由 `upstream.json` 取代；`3.2.0` 之前的历史记录见 `docs/reports/2026-11-04-agent-workflow-upgrade-v3.4.0.md` 第 4 节）：

```yaml
schema_version: 1
source:
  repository: "https://cnb.cool/framework-station/agent-workflow"
  kind: "source-archive"
  name: "agent-workflow-src-263194ba83e7.tar.gz"
  sha256: "d8c417758e0e4078691fff7b398f4e27af8763e610c5619adf2f58a1901e2be5"
  framework_version: "3.4.0"
  commit: "263194ba83e72c1fc67f177fb22e9bffeff0780e"
installed:
  action: "upgrade"
  at: "2026-11-04T08:21:33Z"
  owner: "owner"
  branch: "develop"
  integration_branch: "develop"
  previous_commit: "ccd95f8c6e2191b4f4a13019ce15bd7dfa251e01"
snapshot:
  store: "git-dir:workflow-adoption"
  ref: "snapshots/263194ba83e72c1fc67f177fb22e9bffeff0780e"
  manifest_sha256: "d9848b371d24f9401d0bc7d0ee3de46c15bbbe3b594a35335c5dc006da2074f7"
  file_count: 53
runtime:
  workflow_check: ".agent/bin/workflow-check"
  workflow_check_sha256: "41935fc1adae379c4450501e9ae01dec711b8a4adf15a113db38036bfd51aaa3"
  provenance: "local-build"
compatibility:
  config_schema: 1
  state_schema: "2,3"
last_upgrade:
  from_commit: "ccd95f8c6e2191b4f4a13019ce15bd7dfa251e01"
  to_commit: "263194ba83e72c1fc67f177fb22e9bffeff0780e"
  at: "2026-11-04T08:21:33Z"
  distribution_sha256: "d8c417758e0e4078691fff7b398f4e27af8763e610c5619adf2f58a1901e2be5"
```

## 7. 保留事项与边界

- **未触碰**：`.agent/tasks/**`（29 个在用 + 22 个归档，V3 记录作为冻结证据）、`.agent/registry/*.md`（降级为普通 Markdown，不再有机器 schema）、业务代码与 CI。
- **文档同步**：`README.md` §Task 管理、`docs/design/agent-workflow.md` 归档头部、`docs/task-management/README.md` §1/§2/§4/§6/§7/§8 已改为 V4 语义；带日期的历史报告与历史叙述保持原样。
- **已知陈旧物**：`.agent/adoption/tests/` 仍是 V3.2.0 时代的测试（V4 的 `tests/` 是 package_only，不安装、不校验）。V4 未接管该目录，是否删除由 Owner 决定。
- **B 的来源**：`.agent/upstream.json` 的初始 B 是按 v3.4.0 精确 commit 重建的分发包哈希（已逐字节验证与宿主一致），不是 V3 工具留下的事务记录；升级后该文件由事务整体重写为 V4 provenance。
- **未提交**：本次所有变更仍在 `develop` 工作区，按项目 Git 流程由 Owner 决定提交时机。
