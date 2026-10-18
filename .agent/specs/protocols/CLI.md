# CLI 手册

CLI 参数参考；Gate 的适用对象和拒绝语义以本目录 GateCatalogue.md 与真实源码为准。

需要 Go 1.23+、Git；构建六个 direct binary（仓库根）：

```bash
for cli in workflow-check workflow-git workflow-status workflow-event workflow-program workflow-deliver; do
  GOWORK=off go -C .agent/engine build -o "../bin/$cli" "./cmd/$cli" || exit
done
```

各命令支持 --root 指定仓库根；workflow-check 使用完整仓库相对 Task/Intake 路径，workflow-status 另支持 Task ID 简写。相对 Task 路径按仓库根解析。正式 Gate 避免 go run，因为它包装子进程退出码。

| CLI | 命令与副作用 |
|---|---|
| workflow-check | gate <gate> <task或intake>；17 个 Task/Intake Gate、config 只读配置校验（不是新增 Gate）；0 PASS/1 FAIL/2 ERROR |
| workflow-git | verify-base --parent B --base SHA；branch-exists B；worktree-exists B；create-branch B --parent B --base SHA；create-worktree PATH B。后两项写 Git，拒绝覆盖；0 成功/1 拒绝/2 错误 |
| workflow-status | inspect TASK；recovery-check TASK；index [--format tasks/programs/meta]。只读；inspect/index 为0产出/2错误；recovery-check 另有1不可继续 |
| workflow-event | append --scope-kind task或program --scope-id ID --role ROLE --event TYPE [--sha SHA] [--reason TEXT] [--timestamp RFC3339Nano]；追加 events.md；0成功/1校验失败/2错误 |
| workflow-program | gate <assembler-start或program-cleaner或final-delivery> ID；plan ID（只读）；collect ID --task ID --expected-revision N（Registry CAS写）；assemble ID --source-branch NEW -- COMMAND ARG...（复用Merger并创建新本地C0分支，不更新父分支）；0通过/1 Gate拒绝/2错误或受控写入拒绝 |
| workflow-deliver | plan 支持 task/program/design，只读；verify task/design 隔离测试并写交付证据（DESIGN 用专属批准链）；execute 支持三类对象，隔离重测与受控推送；0通过/1拒绝/2错误 |

## 调用示例

```bash
.agent/bin/workflow-check gate taskbuilder-init .agent/tasks/<task-id>
.agent/bin/workflow-status inspect <task-id>
.agent/bin/workflow-status recovery-check <task-id>
.agent/bin/workflow-status index
.agent/bin/workflow-event append --scope-kind task --scope-id <task-id> --role Coder --event BLOCKED --reason "<真实原因>"
.agent/bin/workflow-program plan <program-id>
.agent/bin/workflow-deliver plan design <task-id> --test "<真实测试命令>"
.agent/bin/workflow-deliver plan program <program-id> --source-branch <集成分支> --test "<真实测试命令>"
.agent/bin/workflow-deliver plan task <task-id> --test "<真实测试命令>"
.agent/bin/workflow-deliver verify task <task-id> --test "<真实测试命令>"
```

`task` 适用于已完成 Merger Handoff/ACK 的 schema 4 NORMAL/COMPLEX。verify 不需要更新父分支授权，也不更新父分支；仅成功后写 Task 直属的 delivery.md 与 state.yaml 的 delivery 字段。随后明确 stage 这两个文件并 commit/push，运行 `workflow-check gate merge-ready .agent/tasks/<task-id>`。Owner 授权后执行：

```bash
.agent/bin/workflow-deliver execute task <task-id> --source-sha <来源tip完整SHA> --parent-branch <目标父分支> --test "<真实测试命令>"
```

Task 授权绑定包含 delivery metadata 的实际来源 tip；`review.target` 仍是被审快照，`delivery.feature_head` 仍是受验快照。两者不能替代实际来源授权。父分支前进、来源实质变化或非 delivery-neutral tail 均需重新验证相应条件。Program 子 Task 可以 verify 后登记，但不能用 execute task 绕过 Program 整体交付。原 DESIGN/Program 的 --source-sha 继续绑定其 final_target/C0，不改变既有语义。

自动 scratch 使用跨进程随机分支名；正常结束（含测试失败）清理本次 worktree 和仍指向预期提交的分支。清理失败会报告 warning 并保留诊断，不删除旧 scratch 分支或已被其他操作改变的引用。显式指定 ScratchDir 的底层调用保留工作区供调用者检查。进程被强制终止可能留下资源，但不会阻断后续调用。

execute 必填 --source-sha 与 --parent-branch；Program 另需 --source-branch。明确的人类授权须绑定同一对象、来源 SHA 与父分支；没有 --yes。测试命令按字段拆分 argv，不通过 shell 执行，需复杂脚本时把脚本作为独立可执行文件。
plan 读取的 origin ref 是本地缓存，不把缓存当作远端绝对新鲜；实际执行仍做 fetch 与父分支漂移校验。详见 [流程规范](../workflows/)。


## Program 写入口与证据

Collector 使用 collect，从准确远端 Task tip读取状态；Registry.delivery_sha仍是经过 verify 的snapshot。集成前证明该snapshot到metadata tip仅限delivery-neutral路径且State只变Delivery，保留审批/Review/资源绑定。冲突、重复或错误revision拒绝，不覆盖记录。

Assembler在已提交推送、clean的独立协调分支调用assemble，提供新来源分支与真实测试argv。成功创建本地C0分支并输出原始JSON收据，Agent普通push后冻结来源；C1后才在独立证据分支保存集成metadata。失败不会创建成功分支/State PASS，不更新父分支。没有单独的Program审批写CLI；OwnerGate按已明确的人类决定机械记录，参数和Gate不证明Owner身份。

DESIGN Program子Task使用verify design并提交delivery metadata后交Collector；execute design/task都会拒绝Program子Task直接更新父分支。DESIGN验证命令应针对真实设计AC，不默认要求生产代码。
