# COORDINATED Program 工作流

规范性文件。已实现、文档补齐；Program 无机器 Intake 锚点，由 Owner 直接规划。子 Task 独立经过 Discovery、初始化、实施与验收，task_type 仍为 NORMAL/COMPLEX/DESIGN。

| 时序 | 角色 | 操作 / Gate | 失败下一步 |
|---|---|---|---|
| 规划 | Owner | program-state.yaml 固定父分支、Base、Task 与依赖 | Owner 修正规划；不得伪造 Intake |
| 登记 | Collector | 核对交付凭证并更新 Program Registry，不合并业务代码 | 子 Task 补齐独立交付证据 |
| 集成开始 | Assembler | workflow-program gate assembler-start | Collector 补登记或 Owner 修正依赖 |
| 集成 | Assembler | 隔离合并、真实测试，final_integration.status=PASS，固定 C0 | 集成失败返回责任 Task 或 Owner |
| 独立审查 | Cleaner | 无 start Gate；入口为 Assembler 交接与 PASS 的 C0 | 未满足入口则回 Assembler |
| 报告 | Cleaner | 审查 C0 后首次新增 cleaner-review.yaml，提交 C1 | 不通过则修复后重新集成和审查 |
| 登记审查 | Cleaner | 后续 metadata 提交登记 review.target=C0、evidence、evidence_commit=C1 | 不得让 C1 自引用 |
| OwnerGate 入口 | OwnerGate | workflow-program gate program-cleaner | Cleaner 修正报告证据 |
| 最终决定 | Owner | 明确接受 C0；OwnerGate 写 owner.status=ACCEPTED、owner.target=C0 | 不自行批准 |
| 交付前 | Merger | workflow-program gate final-delivery | 回责任角色，父分支不变 |
| 最终交付 | Merger | workflow-deliver plan/execute program | 先计划；人类授权后 execute；绑定 SHA 与父分支 |

## C0/C1 强约束

C0 必须不含 `.agent/programs/<program-id>/cleaner-review.yaml`；该文件必须由 C1 首次新增，C1 是 C0 严格后代。报告四字段为 program_id、kind=program-cleaner-review、target=C0、status=CLEAN。C0..C1 只允许该报告变化；不是用普通 README 或工作树文件替代 Git Evidence。重审既有 Program 时如 C0 已含旧报告，先在新集成 C0 中去除旧报告，固定新目标后重新审查，再新增新报告；不重写旧 commit。

Program Registry 是独立交付清单权威，索引不能替代它。Collector 不合并，Assembler 不最终交付，Cleaner 不代 Owner 接受，OwnerGate 不伪造独立审查，Merger 只受控交付。Program 不支持嵌套 Program。
Program 交接不调用 Task 级 handoff Gate。完整 Gate 目录见 `.agent/specs/protocols/GateCatalogue.md`。

## 来源分支与审查证据分支

最终交付来源分支的远端 tip 必须保持 C0（delivery.go 的 PlanProgram 检查 origin/sourceBranch==finalSHA）。Assembler 产生 C0 后推送并冻结该来源；Cleaner 在从 C0 派生的独立证据分支/Worktree 审查和保存 C1，后续 Program State 与 Owner 决定也保存在证据分支，不能推进来源分支到 C1/C2。

C1 必须只 stage 新增审查报告；final_integration.sha=C0 等不能在 C0 内自引用，也不能在 C0..C1 提前提交 State metadata。Assembler 的 C0/测试交接与待登记字段可在证据工作区准备，报告 C1 形成后才把 final_integration/review 与 C1 引用提交为 C2。OwnerGate 从已提交的证据分支读取状态，Merger 的 --source-branch 显式指向仍停在 C0 的集成来源分支。角色边界不允许由 Cleaner 替 Assembler 创造集成 PASS，或替 Owner 接受。


## 正式登记与集成操作

`workflow-program gate/plan` 保持只读。Collector 使用 `.agent/bin/workflow-program collect <program-id> --task <task-id> --expected-revision <N>`，机械读取远端 Task Evidence并原子CAS登记。Registry.delivery_sha 保持实际受验 snapshot；包含交付metadata的来源tip须证明 delivery-neutral tail，State仅 Delivery 字段可不同，批准/Review/Owner/资源/基线不变。非法 SHA、实质变化、审批/父基线漂移拒绝。

Assembler 使用 `.agent/bin/workflow-program assemble <program-id> --source-branch <新来源分支> -- <真实测试命令及参数>`。前提是独立协调分支已提交推送且clean；命令整合所有拓扑批次和已提交规划/Registry，复用Merger隔离测试，保留精确来源tip及测试前后防漂移。任何非MERGE-L1结果均拒绝。成功输出真实JSON收据并创建新的本地C0分支，不推送父分支、不代写Owner或Cleaner状态。Agent普通push后冻结远端C0。收据先交接暂存，C1后由Assembler在独立证据分支保存并登记final_integration，避免C0自引用或污染C0..C1。

子Task NORMAL/COMPLEX使用verify task；DESIGN子Task通过设计最终批准后使用verify design并保存delivery metadata。所有Program子Task不得execute更新父分支。更换已登记版本不能删除或覆盖旧Registry记录，须由Owner确认新的Program规划，保留原历史。
