# Owner Decision

## Review Target
`60a28775c52754f6d7ad69bf54eeabafd1753d90`（Cleaner 已 `CLEAN` 的 immutable snapshot；`review.target` 之后仅有 review-neutral 产物 `core-logic.md`/`findings.md`/`state.yaml`，无生产代码或测试实质变化）

## Core Logic
- CL-001：auth_epoch 版本比对兜底——禁用即时失效 + 重新启用不复活旧凭证（`users.auth_epoch` 单调递增，凭证绑定签发时版本，鉴权/刷新要求版本一致，否则 fail-closed 401）。
- CL-002：状态更新事务原子性 + `SELECT ... FOR UPDATE` 三态判定——禁用/启用与审计、refresh family 撤销在单事务内原子完成，幂等 no-op 不重复副作用。

## Owner Decision
ACCEPTED

## Decision Evidence
- Owner 在 OwnerGate Session 中审阅核心机制说明后明确表达 `ACCEPT`。
- 接受对象：Cleaner 已 `CLEAN` 的 `review.target = 60a28775c52754f6d7ad69bf54eeabafd1753d90` 这一具体 snapshot，而非抽象接受任务。
- 适用范围：本任务（IAM V5 用户账号状态管理与禁用会话控制）范围内；不新增架构组件、不扩大功能范围。
- 记录者：OwnerGate（File Writer）；Decision Authority：Owner。
