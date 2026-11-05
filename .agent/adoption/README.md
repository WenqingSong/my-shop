# Adoption 工具（V4）

`.agent/adoption/workflow-adoption` 是 V4 的安装 / 分发 / 升级 / 回滚 / 校验入口。Python 3.10+，无第三方依赖。

## 子命令

| 命令 | 作用 | 写文件 |
| --- | --- | --- |
| `dist` | 从干净 commit 构建确定性源码包 | 只写输出目录 |
| `inspect` | 校验包 SHA256、成员哈希与 `MANIFEST.json` | 否 |
| `install` | 首次安装 `managed` 文件 | 是（`--execute`） |
| `upgrade` | B/L/N 哈希三方比较升级 | 是（`--execute`） |
| `rollback` | 回滚事务 | 是（`--execute`） |
| `verify` | 校验宿主安装完整性 | 否 |
| `check` | 仓库一致性静态检查 | 否 |

## 用法

```bash
# 构建源码包（源仓库必须干净）
.agent/adoption/workflow-adoption dist --source . --output dist/

# 查看包内容
.agent/adoption/workflow-adoption inspect \
  --package dist/agent-workflow-src-4.0.0.tar.gz \
  --expected-sha256 <sha256>

# 首次安装（默认 Dry-run）
.agent/adoption/workflow-adoption install \
  --target /path/to/host \
  --package dist/agent-workflow-src-4.0.0.tar.gz \
  --distribution-sha256 <sha256> \
  --source-commit <40-hex> \
  --integration-branch main \
  --owner alice --owner-authorized --execute

# 升级（默认 Dry-run）
.agent/adoption/workflow-adoption upgrade \
  --target /path/to/host \
  --package /path/to/new.tar.gz \
  --distribution-sha256 <sha256> \
  --source-commit <40-hex> \
  --owner alice --owner-authorized --execute

# 冲突时保留本地版本（仅限确认过的路径）
.agent/adoption/workflow-adoption upgrade ... --keep-local .agent/specs/WorkflowAdoption.md --execute

# 回滚最近一次事务
.agent/adoption/workflow-adoption rollback --target /path/to/host --execute

# 校验
.agent/adoption/workflow-adoption verify --target /path/to/host
.agent/adoption/workflow-adoption check --repo .
```

## 文件分类

- `managed`：安装到宿主 `.agent/`，参与升级与校验；
- `package_only`：只随包分发，不安装；
- `generated`：`.agent/upstream.json`，只由成功事务写入；
- `protected`：宿主业务文件、`AGENTS.md`、`.agent/tasks/**`、`.agent/registry/**`，永不覆盖。

## 事务与备份

每次写操作创建 `.agent/.transactions/<txn-id>/`：

```text
plan.json        计划、每个文件的原状态、事务状态
backup/          被覆盖或删除文件的原始副本
```

失败自动回滚。宿主的 `.gitignore` 应包含 `.agent/.transactions/`。

## 升级语义

| 宿主状态 | 上游状态 | 结果 |
| --- | --- | --- |
| 未修改（== B） | 有更新 | 覆盖 |
| 未修改（== B） | 已删除 | 删除 |
| 本地修改 | 未变 | 保留本地，记录 `kept_local` |
| 本地修改 | 也更新 | `CONFLICT`，整体失败 |
| 文件缺失 | 未变 | 保持删除 |
| 文件缺失 | 有更新 | `CONFLICT` |

`upstream.json` 缺失或损坏 → `BASE_UNKNOWN`，拒绝升级。

## `check` 的边界

`check` 只做静态检查：

- 7 份规范、7 个角色 Prompt、6 份记录模板是否存在且非空；
- 是否残留 V3 机制文件（`engine/`、`workflow.yaml`、`state.yaml`、`bin/`）；
- 规范与角色中是否出现 V3 术语（历史说明行除外）；
- `.agent/tools/git-facts` 是否可执行；
- Dogfood 任务记录是否具备必需文件与 `Next Action`。

它**不解析任务状态、不判断阶段、不调度角色**，因此不是工作流引擎。

## 已知边界

- 校验的是文件与哈希事实，不能证明 Agent 遵守规范；
- 不做自动合并、不做发布、不改宿主 Git 历史；
- Windows 原生未验证，使用 WSL2 / Linux。
