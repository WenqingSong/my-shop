# COMPLEX 工作流

规范性文件。在 NORMAL 通用审查和受控交付链之前，增加 Analyst → Owner Contract Approval。TaskBuilder 初始化 contract.status=PENDING；Analyst 调查并产出 contract.md，Owner 明确批准后绑定 contract.target/revision/history；不得以 draft 进入 Coder。

## 公共 TaskBuilder 初始化

从 `.agent/roles/DiscoveryAgent.md` 调查，经 Owner 明确批准 COMPLEX 后使用 `.agent/roles/TaskBuilderAgent.md`；入口协议见 `.agent/specs/workflows/EntryWorkflow.md`。schema 4 Task 使用 task_type=COMPLEX、contract.status=PENDING（target 为空，不复制旧批准）、review=NOT_REQUESTED、owner=PENDING、delivery=NOT_RUN、blocked.active=false、resources.reservations={}，intake/git 绑定原批准。初始 Handoff next_role=Analyst、workflow=COMPLEX，交 complex/AnalystAgent.md；未经当前 Contract 批准不得进入 Coder。

| 阶段 | Gate | 失败下一步 |
|---|---|---|
| Discovery 与初始化 | discovery-start / discovery-decision / discovery-handoff / taskbuilder-init | Discovery/TaskBuilder 修正证据 |
| Analyst 完成与 Coder 接手 | coder-start | Analyst 修正 Contract、资源授权，必要时请求 Owner |
| Coder 到 Cleaner | cleaner-start | Coder 固定 review.target 并补测试 |
| Cleaner 到 OwnerGate | owner-gate-start | Cleaner 修正证据 |
| Owner 接受后交付验证 | delivery-start | OwnerGate 修正目标绑定 |
| 交付验证后 | merge-ready | Merger 或责任角色重新验证 |

Contract Revision 须记录历史并重新请求 Owner，不自行继承批准。共享资源以远端 Registry 分配为权威。
其余规则见 `.agent/specs/workflows/NormalWorkflow.md`、`.agent/specs/protocols/HandoffProtocol.md` 与 `.agent/specs/protocols/GateCatalogue.md`。
