# Task: <task-id>

## Goal
<可观察交付结果>

## Scope
<本次包含的行为与文件边界>

## Out of Scope
<明确排除的相邻能力>

## Workflow 与 Complexity
选择并保留一行：
- NORMAL：contract NOT_REQUIRED，TaskBuilder → Coder。
- COMPLEX：contract PENDING，TaskBuilder → Analyst → Owner Contract Approval → Coder。
- DESIGN：contract NOT_REQUIRED，design PENDING，TaskBuilder → Architect → Owner 架构批准 → DocsSpecWriter。

## Acceptance Criteria
- [ ] AC-001：<成功行为与验证方式>
- [ ] AC-002：<拒绝/失败/边界行为与验证方式>

## Design Impact
<NEW / UPDATE / NONE；NEW/UPDATE 时指定 Design Artifact 并列入 Scope/AC>

## Review Baseline
- Parent branch: <Owner 确认的父分支>
- Base commit: <完整 SHA>
- Task branch: <独立任务分支>
- Intake: <已批准的 intake-id>
- 已有修改：<真实状态与归属>

## Verification
<真实命令、预期结果、所需环境；未执行不得标 PASS>

## Relevant Context
<已核实事实、假设、未决问题；禁止代 Owner 批准>

新任务先走公共 Discovery，再由 Owner 确认类型；公共 TaskBuilder 读取已批准 Intake 和 EntryWorkflow 后填本模板，类型不是用户启动调查的前置输入。Program 整体不使用 Task 模板，子 Task 独立批准。
