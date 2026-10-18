# 派生索引

DERIVED — 不得作为审批证据。只转述当前工作树 State 与本地 Git，不 fetch、不改 State、不更新 Registry。生成期间请保持工作树稳定；source_sha 是本地 HEAD，不能证明未提交 State 已入库，也不证明远端新鲜或 Gate PASS。

在仓库根构建 workflow-status 后执行：

```bash
.agent/bin/workflow-status index > .agent/index/tasks.tsv
.agent/bin/workflow-status index --format programs > .agent/index/programs.md
.agent/bin/workflow-status index --format meta > .agent/index/index-meta.yaml
```

三次读取应在无并发修改时执行。删除三个生成物后重跑即恢复；内层 .gitignore 是唯一忽略来源，只有本说明及 .gitignore 入库。data 文件相同输入重复生成内容相同；meta 的 generated_at 每次变化。

| 字段 | 来源/含义 |
|---|---|
| task_id / path | State ID 与所在目录；不一致标 ghost |
| schema / task_type / workflow / program | 原 State；Program 子任务显示 COORDINATED(child) |
| contract / review / owner / delivery | 原 status，绝不推导批准有效性 |
| review_target / delivery_head / owner_target | 完整 SHA，未截断 |
| blocked | 原 blocked.active，true 的行优先 |
| branch / base / intake | 初始化 Git 基线及 Intake 引用 |
| last_commit / updated | 本地任务分支 tip 的完整 SHA 和 UTC 时间；无本地分支时读 origin 缓存 |
| flags / reason | ghost、v3、no-branch、orphan、corrupt、stale（超过180天），只提示 |
| generated_at / source_sha | meta 中的 UTC 生成时刻与 HEAD 完整 SHA |

无法读取/解析 State 的目录仍输出一行，ID 使用目录名，其余事实记 -，flags=corrupt 并保留原因；末尾输出 corrupt 总数。符号链接不跟随，标 corrupt。Program 损坏及反向归属不一致在 programs.md 明示，不自动修复；Program Registry 仍是交付登记权威。

索引不在 Task 目录，生成物不入库，不改变 CLEAN、Neutral Tail 或 handoffReady。规模验证见 V4.1 验收记录；索引不承诺目录快照隔离。
