# Plan — SurgeCart 秒杀链路工程基线审计

## Current Status

PLANNING

> 取值：`PLANNING` | `IN_PROGRESS` | `BLOCKED` | `REVIEW` | `DELIVERY` | `DONE`
> 只由 Lead 维护。

## Steps

| # | 步骤 | 负责角色 | 状态 | 证据 |
| --- | --- | --- | --- | --- |
| 1 | 撤销 V2 初始化、建立正式任务记录与 `task/surgecart-baseline-audit` 分支 | TASK-BUILDER | DONE | commit 见 `handoff.md` |
| 2 | 版本锚定：复核 HEAD、工作区与基线，写入 D1 首页 | 待 Lead 指派 | PENDING | — |
| 3 | 测绘调用链与源码入口地图（D1） | 待 Lead 指派 | PENDING | — |
| 4 | 梳理数据层定位清单与不变量（D2） | 待 Lead 指派 | PENDING | — |
| 5 | 判定 MySQL 请求表职责与消费者领取 / 恢复机制（D2） | 待 Lead 指派 | PENDING | — |
| 6 | 静态核实测试、门禁、Dockerfile、CNB 流水线、Prometheus 配置 | 待 Lead 指派 | PENDING | — |
| 7 | 实际运行基线测试并记录命令 / 退出码 / 输出（D3） | 待 Lead 指派 | PENDING | — |
| 8 | 事实分区与风险登记（D4），汇总 T02 / T03 审查输入（D5） | 待 Lead 指派 | PENDING | — |
| 9 | 五份产物落盘并自检 AC-1 ~ AC-9 | 待 Lead 指派 | PENDING | — |
| 10 | 独立审查（审查者不得是报告作者） | CLEANER | PENDING | — |
| 11 | 交付与验收核对 | DELIVERER / LEAD | PENDING | — |

> 状态取值：`PENDING` | `IN_PROGRESS` | `DONE` | `SKIPPED` | `BLOCKED`
> 跳过的步骤必须写原因。

## Next Action

LEAD 读取本目录 `task.md` 与 `plan.md`，为步骤 2 ~ 11 指定负责人与写入边界，然后指派角色执行步骤 2：先 `git rev-parse HEAD` 确认基线仍是 `473d6f5253e3abcb98ece9d79a5a8059563a3e8d`，若已变化先在 `task.md` 的 `## Review Baseline` 更新基线并在 `handoff.md` 说明，再开始采集证据。

## Blockers

- 无。

## Notes

- 任务从 Owner 的原始需求重新定义，未继承已撤销 `surgecart-baseline-audit-v2` 的任务定义、计划或决策。
- 报告落盘固定为 `docs/reports/surgecart-baseline-audit/`；如执行中发现该路径不可用，先回报 Lead，不自行换目录。
- 步骤 10 的独立性按 `AgentCollaborationSpecification.md` §4 执行：报告作者不得出具 CLEAN 结论。
- `docs/reports/surgecart-baseline-audit-v1/` 属于旧基线交付，只能作为对照，不得复制其结论。
