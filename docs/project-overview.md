# SurgeCart 项目导览

这是一份面向初次阅读者的短导览。项目以 GoFrame v2 提供 HTTP API，MySQL 保存交易与身份数据，Redis 管理登录会话并承担秒杀准入层。HTTP 入口由 `api` 和 `controller` 定义，业务流程位于 `service` 与 `logic`；订单超时取消、秒杀异步消费与缓存对账由应用内后台扫描器执行。详细规则和已验证范围请沿文末链接阅读。

## 两条核心交易链路

**普通订单：**用户从购物车或直接购买发起下单 → 服务端重读 SKU 价格并保存商品、价格和地址快照 → 同一 MySQL 事务创建订单、订单项并条件扣减库存 → 通过订单状态机执行 Mock 支付、取消、发货、收货等操作。幂等键与数据库唯一约束防止重复建单，条件扣减和 `RowsAffected` 防止超卖，取消或退款只恢复一次库存。详见[普通订单设计](design/order.md)和[核心逻辑验证](../.agent/tasks/order-v1/core-logic.md)。

**秒杀：**V1 先用 MySQL 条件更新与唯一约束证明不超卖、一人一单和请求幂等；V2 增加 Redis + Lua 准入，提前拒绝无效请求；V3 将请求写入 MySQL 出队表，再由应用内消费者异步落单；V4 补充崩溃重投、幂等库存收敛、死信修复与审计；V5 增加限流、排队软上限、熔断、指标和逐级压测脚本。Redis 的预扣库存是准入闸门，MySQL 始终是订单与已售数量的最终事实来源。详见[秒杀设计](design/flash-sale.md)、[V4 核心逻辑验证](../.agent/tasks/flash-sale-v4/core-logic.md)和[V5 交付证据](../.agent/tasks/flash-sale-v5/delivery.md)。

## 值得追问的技术取舍

| 取舍 | 原因与可验证材料 |
| --- | --- |
| 先正确，后加速 | 秒杀按 MySQL → Redis + Lua → 异步落单 → 故障恢复 → 容量保护演进，每一步保留业务不变量；见[秒杀设计](design/flash-sale.md)。 |
| 用 MySQL 出队表而非外部 MQ | 当前规模下复用已有持久化与事务能力，并明确消费者重试、死信和恢复语义；见[秒杀 V3 任务](../.agent/tasks/flash-sale-v3/contract.md)。 |
| 区分前台用户与管理员 | 独立身份域、会话与鉴权中间件，后台写操作再做权限检查；见[IAM 设计](design/iam.md)与[RBAC 设计](design/rbac.md)。 |
| 保留设计与验收证据 | 项目级设计记录稳定规则，任务材料记录 Owner 决策、核心逻辑和实际验证；见[Agent Workflow 设计](design/agent-workflow.md)。 |

项目使用 Agent 协作开发。Owner 负责业务边界、关键取舍和验收判断；任务资料记录 Contract、核心逻辑验证与交付结果。阅读这些资料时，应把文档结论与当前实现及测试对应起来，而不是仅凭生成的说明推断功能已验证。

## 当前边界与阅读建议

普通订单的支付是 Mock，秒杀订单下单即成交；项目没有宣称真实支付资金链路或外部 MQ 能力。V5 的[压测脚本](../scripts/flashsale-loadtest/run.sh)与交付记录包含延迟分位数验证，但可用于简历的 P99/容量结论仍应在明确环境、负载与复测结果后单独发布。Kubernetes 部署也尚未形成已验证结论。

首次阅读建议从[根目录 README](../README.md)进入，挑一条交易链路读设计，再按 `core-logic.md → 实现 → 测试 → delivery.md` 核对。`docs/design/*` 是长期设计；`.agent/tasks/*` 是历史任务证据，不能仅凭旧任务清单判断当前功能状态。
