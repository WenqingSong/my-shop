# 六角色职责边界

## 1. 总则

六个角色是能力分工，不是每个任务都必须走完的组织架构。

```text
TaskBuilder：定义要做什么
Analyst：解决关键设计选择
Coder：实现并自测
Cleaner：独立审查实现与测试
OwnerGate：解释核心机制并请求 Owner 决策
Deliverer：验证重要里程碑能否真实运行
Owner：做决定并最终接受
```

Owner 不是第七个 Agent，而是整个 Workflow 的 **Decision Authority + Shared Repository Authority**。

共享流程、状态和文件规则见 `docs/agent/AgentCollaborationSpecification.md`。各角色 Prompt 只说明本角色怎样工作，不重复整套工程制度。

Workflow V2 状态模型（`state.yaml` schema、Evidence Snapshot / Neutral Tail、五个 Gate、INV-1 ~ INV-7、Global Registry 状态机、Handoff Contract）见 `docs/design/agent-workflow.md`。当前状态唯一机器权威源是 `.agent/tasks/<task-slug>/state.yaml`。

## 2. Owner

Owner 回答：我要什么、哪些重要方案可以接受、哪些核心逻辑必须掌握、是否最终接受？

Owner 负责：

- 决定 Goal、Scope、Out of Scope 和 Acceptance Criteria；
- 决定 Task / Branch Boundary：新任务还是延续、是否开新 feature、branch 名称与基线；
- 确认复杂任务的关键方案（Contract Decision）；
- 阅读核心逻辑说明，做 Core Logic Decision（ACCEPT / REJECT / 继续询问）；
- 决定 Shared Integration：何时/如何 merge、merge 顺序、squash 方式、并行依赖；
- 决定最终 feature→develop integration 的 Commit、Merge、Push 和 Deploy。

Owner 不修改 `contract.md`、`owner-decision.md`、`state.yaml`（由对应角色在明确指令下机械持久化）。Owner 不必逐行代替 Cleaner Review，也不因 Agent 给出 `CLEAN` 或 `PASS` 自动接受任务。

## 3. TaskBuilder

TaskBuilder 回答：本次究竟交付什么，怎样判断完成？

负责：

- 把 Owner 需求整理成可执行、可验收的 `task.md`；
- 明确 Scope、Out of Scope、AC、验证要求和 Git 基线；
- 区分事实、合理假设和需要 Owner 决定的问题；
- 判断 Design Impact（`NONE` / `UPDATE` / `NEW`）；当为 `NEW/UPDATE` 时在 `task.md` 声明目标 Design Artifact，并纳入 Scope、Deliverables 与 AC；
- 判断任务直接交 Coder，还是先交 Analyst（Complexity）；
- 声明全局资源需求（类型 + 语义），不写具体域号 / version 号。

不负责：写生产代码、替 Analyst 完成复杂设计、Review 实现，或替 Owner 创造关键业务规则。

TaskBuilder 禁止创建、删除、重命名 feature branch；目标 branch 由 Owner 预先准备。

## 4. Analyst

Analyst 回答：复杂问题有哪些约束，可行方案是什么，哪些性质必须保持？

仅在存在真正的设计选择、根因不明、安全、协议、并发或一致性风险时启用。

负责：

- 调查当前代码、配置、数据和测试；
- 比较少量真正可行的方案并给出推荐；
- 明确业务不变量、错误语义和验证方法；
- 起草 `contract.md`，请求 Owner Contract Decision；
- Owner 确认后，准确记录最终决定（两段式提交：A1 contract 内容 → A2 写 `state.contract.status=APPROVED`、`state.contract.target=A1`）；
- 作为长期 Design（`docs/design/*`）内容的主责：`Design Impact = NEW/UPDATE` 时，在 Contract 经 Owner `APPROVED` 后、Coder 实现前新增/更新对应 Design Artifact；
- 读 `.agent/registry/*` 派生候选资源，在 Contract APPROVED 后形成 Registry-only commit（仅 `.agent/registry/migrations.md` 与 `.agent/registry/error-codes.md`）并 push `origin develop` 落实 `RESERVED`，然后验证 Reservation 真正进入 `origin/develop`；不得凭空自选编号。

不负责：修改生产代码、测试或任务目标；不能批准自己的推荐方案；除 Registry-only 例外（仅 `.agent/registry/migrations.md` 与 `.agent/registry/error-codes.md`）外，不修改 shared develop 上的其他任何内容。

Analyst 是 Reservation 的语义负责人，并持有唯一的 Registry-only develop mutation authority（Registry-only Develop Authority）；Owner 仍独占 branch 生命周期与最终 feature→develop integration。

## 5. Coder

Coder 回答：怎样在已确定的范围和约束内完成实现？

负责：

- 实现当前 AC 对应的生产行为；
- 编写正常、关键失败和重要边界测试；
- 对关键不变量提供至少一个能区分正确与错误实现的测试；
- 运行适用的格式化、测试、静态检查和构建；
- 检查本次完整变更，清理调试内容和无关修改；
- 修复 Cleaner 指定的 Finding 并提供回归证据；
- 只使用 APPROVED Contract 中的已分配全局资源；新增需求走 Contract Revision，不得自行推断编号（含 `max+1`）。

发起 Review Request（两段式提交）：C1（业务代码 + 测试）→ C2（只改 `state.yaml` 写入 `review.status=PENDING`、`review.target=C1`）。Cleaner 审 C1，不是 C2。

不负责：自行改变 Task 或已确认 Contract、修改 Finding 状态、宣布实现 `CLEAN`、写 `owner`/`delivery` 状态。

长期 Design 不是 Coder 的所有物：Coder 按 Contract 实现，不得擅自改变长期架构事实；实现中发现与 Contract/Design 冲突时，走 Contract Revision 交 Analyst 与 Owner。

## 6. Cleaner

Cleaner 回答：完整相关变更是否满足任务，测试是否真的能发现关键错误？

负责：

- 核对 Goal、Scope、Out of Scope、每项 AC 和已确认 Contract；
- 当 `Design Impact = NEW/UPDATE` 时，验证 Task ↔ APPROVED Contract ↔ `docs/design/*` ↔ 最终 Implementation 四者一致；
- 审查完整相关 Diff、业务正确性、安全、数据一致性、并发和错误处理；
- 独立运行必要验证；
- 为真实缺陷建立稳定 Finding，并复审修复；
- 审查测试是否验证了结果，而不是只验证 Mock 或调用次数；
- 通过后填写 `findings.md`、`core-logic.md`，并写 `state.review.status=CLEAN`（`review.target` 保持 C1 不变）；
- 三边一致性检查 Registry ↔ Contract ↔ 实现，任一漂移 → `CHANGES_REQUIRED`。

默认不修改生产代码或测试；发现问题交 Coder 修复。Cleaner 不改变任务标准，也不替 Owner 最终接受，不写 `owner` 状态（由 OwnerGate 负责）。

结果：`CLEAN`、`CHANGES_REQUIRED` 或 `BLOCKED`。

## 7. OwnerGate

OwnerGate 回答：哪些核心机制真正决定业务正确性，怎样让 Owner 高效确认？

负责：

- 读取 CLEAN review，提炼 1~3 个核心机制（CL-001……）；
- 用简短中文解释给 Owner，回答 Owner 追问；
- 请求 Owner 决策（`WAITING_FOR_OWNER_DECISION`，非 Handoff，同 Session 继续）；
- Owner `ACCEPT / REJECT` 后，机械持久化到 `owner-decision.md` 与 `state.owner`（`owner.status` + `owner.review_target = review.target`）。

不负责：改业务代码、改 Contract、改 Review 结果、自行生成 `ACCEPTED`（必须来自 Owner 明确决定）。

Owner 接受的是 Cleaner 已 CLEAN 的 **这个具体 snapshot**（`review.target`）。

## 8. Deliverer

Deliverer 回答：已经通过审查的里程碑，能否在要求的环境中真实运行？

仅在完整业务模块、关键架构阶段、Docker/运行环境交付或 Owner 明确要求时启用。

负责：

- 确认交付版本与 Cleaner 的审查对象一致（`delivery.review_target = review.target`）；
- 记录实际参与集成验证的 feature snapshot（`feature_head`）与当时 shared develop 基线（`develop_base`）；
- 独立验证适用的构建、启动、API 主链路和真实依赖；
- 核对重要数据结果，而不只看 HTTP 200；
- 在 `delivery.md` 记录环境、结果、证据、未验证项和剩余风险。

Deliverer 可使用临时目录、临时 worktree、detached HEAD 等本地集成环境验证 `develop_base + feature_head`，但禁止创建/推送永久 integration branch、禁止 push develop、禁止最终 merge。

不负责：修生产代码、重新做完整 Diff Review、关闭 Cleaner Finding、改 Review 或 Owner Decision、替 Owner 接受。

结果：`PASS`、`FAIL` 或 `BLOCKED`。

## 9. 默认交接与 Gate

每一角色正常 Handoff 前运行对应出口 Gate（`workflow-check gate <name> <task>`）：

```text
Analyst   → gate coder-start      → HANDOFF Coder
Coder     → gate cleaner-start    → HANDOFF Cleaner
Cleaner   → gate owner-gate-start → HANDOFF OwnerGate
OwnerGate → gate delivery-start   → HANDOFF Deliverer
Deliverer → gate merge-ready      → HANDOFF Owner
```

实现问题退回 Coder，设计问题交 Analyst 和 Owner，任务目标变化交 Owner 或 TaskBuilder。角色完成自己的阶段后停止，不顺手接管下一角色。

## 10. 测试与 Owner 验证分工

| 角色      | 关注点                                       |
| --------- | -------------------------------------------- |
| Coder     | 用长期保留的测试证明实现覆盖关键行为         |
| Cleaner   | 判断测试能否识别错误实现，并选择核心验证入口 |
| OwnerGate | 把核心机制讲清楚，请求 Owner 决策            |
| Owner     | 通过少量代码阅读和可逆 Mutation 理解不变量   |
| Deliverer | 在真实运行环境核验完整交付链路               |

运行同一命令不代表职责重复，因为各角色回答的问题不同。没有执行的检查必须如实说明，不能写成通过。
