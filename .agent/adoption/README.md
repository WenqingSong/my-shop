# Workflow Adoption Tool

`.agent/adoption/` 是 Workflow V3 的轻量安装 / 升级入口。它不替代 Go Engine，
不重造包管理器，只负责：

- 从干净 Commit 生成可校验的 Source Distribution；
- 首装时按 Manifest 写 `managed` 文件，生成真实 `workflow.yaml` /
  `upstream.yaml` / `workflow-check`；
- 升级时用 B/L/N 三方比较，保护宿主修改与历史证据；
- 在写入前用真实 Engine Gate + 最小 Task Fixture 验证；
- 事务备份、失败回滚与恢复。

规范见 `.agent/specs/WorkflowAdoption.md`，分发契约见 `PACKAGE_MANIFEST.md`。

## Requirements

- Python 3.10+（标准库即可，无第三方依赖）
- Git 2.x
- Go 1.23+（从分发包内 Engine 构建 `workflow-check` 时需要）

## Commands

```bash
# 1. 构建 Source Distribution（源必须干净）
.agent/adoption/workflow-adoption dist \
  --source . \
  --output dist/

# 2. 校验分发包
.agent/adoption/workflow-adoption inspect \
  --package dist/agent-workflow-src-<commit12>.tar.gz \
  --expected-sha256 <sha256>

# 3. 首次安装（默认 Dry-run；确认计划后加 --execute）
.agent/adoption/workflow-adoption install \
  --target /path/to/host \
  --package /path/to/source.tar.gz \
  --distribution-sha256 <sha256> \
  --source-commit <full-40-hex> \
  --integration-branch <owner-confirmed-branch> \
  --owner <owner> \
  --owner-authorized \
  --execute

# 4. 升级（默认 Dry-run）
.agent/adoption/workflow-adoption upgrade \
  --target /path/to/host \
  --package /path/to/new-source.tar.gz \
  --distribution-sha256 <sha256> \
  --source-commit <new-full-40-hex> \
  --owner <owner> \
  --owner-authorized \
  --execute

# 5. 校验已安装 Host
.agent/adoption/workflow-adoption verify --target /path/to/host

# 6. 从事务备份恢复
.agent/adoption/workflow-adoption restore \
  --target /path/to/host \
  --backup <transaction-id> \
  --execute
```

也可以 `--source <clean git repo>` 代替 `--package`；此时来源由精确 Commit
确定，内容校验记录为规范化 Manifest 的 SHA256。使用 `--package` 时必须提供
`--distribution-sha256`，拒绝未固定归档。

## Registry

默认不创建 Registry，`resources: {}`。只有确实需要共享资源时才显式启用：

```bash
--resource migration_version=.agent/registry/migrations.md
```

工具只创建空表，不生成 `RESERVED` / `ACTIVE` 真实分配行。

## External workflow-check

默认从分发包内同一 Commit 的 Engine 构建。构建时注入分发包自身的
`main.version` / `main.commit` 与 `buildSource=source`，因此安装后的
`.agent/bin/workflow-check version` 会报告真实来源版本与 Commit，
不会停留在 `dev` / `unknown`。发布资产使用 `buildSource=release` 区分。

若使用 P08 发布的 binary：

```bash
--workflow-check /path/to/workflow-check \
--workflow-check-sha256 <sha256> \
--workflow-check-commit <full-40-hex-equal-to-source-commit>
```

缺任一固定值、Commit 不匹配或真实 Gate 不通过时，工具拒绝替换宿主现有 binary。

升级不会无条件替换现有 binary：未提供通过校验的新 binary 时保留原 binary。因此
`workflow-check version` 报告的是**该 binary 的构建 Commit**，而“当前安装的是哪个
分发包”以 `.agent/upstream.yaml` 的 `source.commit` 为准；两者在只更新源码文件的
升级中可能不同，这是预期语义，不是漂移。

## Safety Model

- 只写 `managed` 清单与事务生成的 `workflow.yaml` / `upstream.yaml` / binary；
- `package_only`（根 README、CI、Design）不落宿主；
- 根 `AGENTS.md` 不创建、不覆盖、不删除；
- `.agent/workflow.yaml` 在升级中只读；
- `.agent/tasks/**`、`.agent/registry/**`、业务文件与证据只读；
- 升级冲突、B 快照缺失、删除未授权时整次 fail closed，不做部分写入；
- 写入前备份到 `.git/workflow-adoption/backups/<txn>/`；
- 报告写入 `.git/workflow-adoption/reports/<txn>.json`；
- 归档拒绝绝对路径、`..` 穿越、符号链接、硬链接与特殊文件；
- 安装 / 升级要求目标工作区无未提交 tracked 变更。

## Exit Codes

| code | 含义 |
| --- | --- |
| 0 | 成功 / Dry-run 计划 |
| 1 | 失败 / 内部错误 |
| 2 | 前置条件、来源或完整性错误 |
| 3 | CONFLICT |
| 4 | BASE_UNKNOWN |
| 5 | 未授权（Owner 授权或删除授权） |

## Failure and Recovery

失败时不会丢失既有文件：事务会按备份恢复所有被修改 / 删除的文件，删除本次
新建的文件，并在报告中记录 `rollback`。若回滚本身失败，备份仍保留：

```text
<git-dir>/workflow-adoption/backups/<txn>/
  manifest.json
  before/<path>...
```

可使用 `restore --backup <txn>` 恢复。建议在恢复后重新运行 `verify`。

## Tests

```bash
python3 .agent/adoption/tests/test_workflow_adoption.py -v
```

测试只在临时 Git 仓库中进行，覆盖：

- Source Distribution 洁净、分类、路径穿越与符号链接拒绝；
- 空宿主 / 已有 AGENTS / 已有 README / CI / Design 的安装保护；
- Owner 指定集成分支与真实 Gate；
- Registry 按需启用的空表；
- B=L/N 变化、B=N/L 修改、B/L/N 冲突、B 缺失；
- 旧 Task / Registry / blocked / illegal Evidence 字节保护与 F-03 报告；
- 上游删除 + 本地修改、授权删除与备份；
- 事务注入失败后的回滚与 `restore`；
- 校验和固定的归档安装。

测试钩子 `WORKFLOW_ADOPTION_TEST_FAIL_AFTER_WRITE=N` 仅用于验证事务回滚，
不属于发布接口。

## Platform Boundary

- Linux + Python 3.11 + Git 2.39 + Go 1.24 已完成真实 E2E 验证；
- macOS 使用相同 POSIX / Python / Go 代码路径，但本任务未在 macOS 实机验证；
- Windows 原生环境未验证：文件模式、符号链接语义与 `workflow-check.exe`
  命名不同。Windows 宿主必须使用 WSL2 或 Linux 容器执行同一流程，或按
  `WorkflowAdoption.md` 的手工步骤安装，并在 Linux 环境完成 Gate 验证。
  不得声称已在 Windows 验证。

## Version Boundary

当前 `.agent/VERSION` 是 `3.2.0`（Source Version）。`v3.2.0` Tag、Release、四平台
binary 资产、`SHA256SUMS` 与 `version.json` 由发布流水线产生，当前 Published Status
为 `NOT_RELEASED`；本目录不创建 Tag / Release，也不伪造正式发布地址。
