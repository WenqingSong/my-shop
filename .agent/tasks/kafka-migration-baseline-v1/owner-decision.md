# Owner Decision

## Review Target

a2d68709af9dd7d927930707c219637e3d476c0a

## Core Logic

- CL-001：防超卖——Redis Lua 闸门限量（快速失败）+ MySQL 条件扣减兜底；不变量为「成功订单数 ≤ 初始库存、库存 ≥ 0」。
- CL-002：一人一单 / 幂等 / 失败不建单——唯一约束兜底 + 消费单事务；不变量为「一人一单、重复请求不重复扣减、失败不建单」。

## Owner Decision

ACCEPTED

## Decision Evidence

Owner 在 OwnerGate Session 中明确回复「ACCEPT」，接受 Cleaner 已 CLEAN 的 snapshot（`review.target = a2d68709af9dd7d927930707c219637e3d476c0a`）作为 Kafka 改造前基线。

接受范围：以该 snapshot 所记录的秒杀核心真实调用链、五个业务不变量（库存 ≥ 0、成功订单数 ≤ 初始库存、一人一单、失败不建单、重复请求不重复扣减）与回归验证入口，作为后续 Kafka 改造任务「既有秒杀能力未被破坏」的对比基准。

本任务不新增/修改任何生产代码、配置、数据模型、错误码或公开接口；相对 Base Commit `8fd62c0` 的变更仅限本任务目录文档与证据。NOT_VERIFIED 项（promtool recording rules 语法校验、nginx `nginx -t`）因环境不具备镜像，不阻塞基线结论。
