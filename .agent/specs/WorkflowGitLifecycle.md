# Workflow Git Lifecycle（V4）

> Git 是唯一可信的代码事实来源。记录中的结论必须能绑定到 commit。

## 1. 分支

| 分支 | 用途 | 谁创建 | 规则 |
| --- | --- | --- | --- |
| `main` | 稳定主线 | Owner | Agent 不得直接提交或强推 |
| `task/<task-slug>` | 单个任务 | Lead 或 TaskBuilder | 从当前 HEAD 创建，一个任务一个分支 |
| `release/v<version>` | 发布准备 | Lead（Owner 授权） | 只做版本与发布资产修订 |
| 其他既有分支 | 历史开发 | — | 不删除、不改写 |

创建规则：

```bash
git switch -c task/<task-slug>          # 从当前检出分支的当前 HEAD 创建
git rev-parse HEAD                      # 记录基线 SHA 到 task.md 的 Review Baseline
```

- 默认以当前检出分支与当前 HEAD 为基线，不擅自切换到 `main`/`develop`，不把远端 SHA 当作起点。
- 不 `rebase` 已推送分支，不 `amend` 已推送提交，不 `push --force`（除非 Owner 明确授权且非 `main`）。
- 不为「整理历史」重写分支。

## 2. 提交

- 一个提交只做一件可描述的事；提交信息用 `<type>(<scope>): <summary>`。
- 不提交构建产物、临时文件、凭据、宿主私有配置。
- 提交前检查：`git status` 与 `git diff --check`。
- 工作区必须干净才能交付；有意的未跟踪文件（如本地产物）应加入 `.gitignore` 或删除。
- 提交信息不得声称未验证的结果。

## 3. 推送

- 任务分支在每个可验证里程碑后推送，避免成果只存在于本地容器。
- 推送后核对：`git ls-remote origin refs/heads/<branch>` 与本地 HEAD 一致。
- 推送失败属于环境类失败：按 `WorkflowLoopCircuitBreaker.md` 处理，不得口头声称已推送。

## 4. 审查绑定

1. 送审前，Lead 必须把被审代码提交，记录完整 SHA 到 `handoff.md` 与 `findings.md` 的 `Review Target`。
2. Cleaner 只针对该 SHA 审查。审查期间不得修改被审代码。
3. 审查后任何**代码**变更都会使审查失效：必须重新审查，并在 `findings.md` 记录新的 `Review Target`。
4. 只改记录（`findings.md` 状态、`handoff.md`、`delivery.md`）不使审查失效，但不得借机修改代码。
5. `task.md` 的验收标准变化会使既有审查与交付结论失效，必须重新确认。

## 5. 交付绑定

`delivery.md` 必须包含：

- 交付 commit（完整 SHA）与分支；
- 逐条 Acceptance Criteria 的结论（PASS / FAIL / 未验证）与证据；
- 实际运行的验证命令与真实结果；
- 审查对象 SHA 与审查结论；
- 已知限制与回滚方式。

`delivery.md` 引用的 SHA 必须存在：`git cat-file -t <sha>` 返回 `commit`。

## 6. 发布

- 正式版本由 annotated tag 标记：`git tag -a v<version> -m "..."`。
- Tag 只能打在已经通过验证的 commit 上，且 `.agent/VERSION` 与 tag 一致。
- Tag 一旦推送即视为正式事实：不得移动、不得覆盖、不得删除后重建。
- 发布资产由 `scripts/release/release.py` 从干净 commit 构建，只包含源码资产与发布记录。
- 发布结果必须以远端可核验事实为准：分支、Tag、Release 与附件 SHA256。

## 7. 各角色 Git 权限

| 角色 | 分支 | 提交 | 推送 | Tag |
| --- | --- | --- | --- | --- |
| Lead | 创建任务/发布分支 | 是 | 是 | 是（Owner 授权后） |
| TaskBuilder | 创建任务分支 | 仅任务目录 | 是（任务分支） | 否 |
| Architect / Coder | 否 | 是（任务分支） | 是（任务分支） | 否 |
| Cleaner | 否 | 否 | 否 | 否 |
| Analyst / OwnerGate / Deliverer | 否 | 否 | 否 | 否 |
| Owner | 是 | 是 | 是 | 是 |

例外：Deliverer 在 Lead 明确授权时可以提交 `delivery.md`。

## 8. 历史保护

- 不删除他人分支，不覆盖已发布 Tag，不改写 `main`。
- 历史任务记录是证据，不是当前规范；保留在 `docs/history/` 或原任务目录，不因版本升级删除。
- 升级框架文件不得删除宿主业务文件与既有任务记录。

## 9. 交付前检查清单

```bash
git status --short                # 必须为空
git log --oneline -10             # 提交信息可读
git diff --check                  # 无空白错误
git cat-file -t <delivery-sha>    # 交付 SHA 存在
git ls-remote origin refs/heads/<branch>   # 远端已同步
```
