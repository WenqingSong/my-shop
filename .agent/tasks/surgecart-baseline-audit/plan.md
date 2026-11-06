# Plan — SurgeCart 秒杀链路工程基线审计

## Current Status

IN_PROGRESS

> 取值：`PLANNING` | `IN_PROGRESS` | `BLOCKED` | `REVIEW` | `DELIVERY` | `DONE`
> 只由 Lead 维护。

## Steps

| # | 步骤 | 负责角色 | 状态 | 证据 |
| --- | --- | --- | --- | --- |
| 1 | 撤销 V2 初始化、建立正式任务记录与 `task/surgecart-baseline-audit` 分支 | TASK-BUILDER | DONE | commit `e2949d1278f94a4851cd750f99c7b47e38f5b3a9` |
| 2 | 版本锚定：复核 HEAD、工作区与基线 | LEAD | DONE | HEAD `95c13910c7226b4e32487ed046702caf75d74fc6`；`git diff --stat 473d6f5..HEAD` 仅 6 个任务记录文件 |
| 3 | 测绘调用链与源码入口地图（D1） | LEAD | IN_PROGRESS | 进行中 |
| 4 | 梳理数据层定位清单与不变量（D2） | LEAD | PENDING | — |
| 5 | 判定 MySQL 请求表职责与消费者领取 / 恢复机制（D2） | LEAD | PENDING | — |
| 6 | 静态核实测试、门禁、Dockerfile、CNB 流水线、Prometheus 配置 | LEAD（含只读盘点） | PENDING | — |
| 7 | 实际运行基线测试并记录命令 / 退出码 / 输出（D3） | LEAD | PENDING | — |
| 8 | 事实分区与风险登记（D4），汇总 T02 / T03 审查输入（D5） | LEAD | PENDING | — |
| 9 | 五份产物落盘并自检 AC-1 ~ AC-9 | LEAD | PENDING | — |
| 10 | 独立审查（审查者不得是报告作者） | OWNER / 外部审查者 | BLOCKED | 单 Agent 会话内无独立审查者；见 `delivery.md` 独立性披露 |
| 11 | 交付与验收核对 | LEAD → OWNER | PENDING | — |

> 状态取值：`PENDING` | `IN_PROGRESS` | `DONE` | `SKIPPED` | `BLOCKED`
> 跳过的步骤必须写原因。

## Next Action

LEAD 按 `task.md` 的 D1 → D5 顺序采集证据并落盘到 `docs/reports/surgecart-baseline-audit/`：先完成 D1（路由 → Handler → Service → Lua → 请求表 → 消费者 → 订单），再 D2、D3、D4、D5。

## Blockers

- 步骤 10：当前 Session 内不存在独立审查者（报告作者即 LEAD），独立审查未满足。该阻塞不影响审计事实与交付材料的可用性，但不得声称已通过独立审查。

## Notes

- LEAD 于 2026-11-06 接管本任务；接管声明见 `handoff.md`。
- 任务 slug / 分支名与 Owner 本次指令中的 `-v1` 后缀存在冲突，裁决记录见 `decisions.md` D4：沿用既有 `task/surgecart-baseline-audit`，不覆盖历史 `-v1` 目录与报告。
- 报告目录沿用 `task.md` 已固定的 `docs/reports/surgecart-baseline-audit/`。
- `docs/reports/surgecart-baseline-audit-v1/` 属于旧基线交付，只能作为对照，不得复制其结论。
- 步骤 10 的独立性按 `AgentCollaborationSpecification.md` §4 执行：报告作者不得出具 CLEAN 结论。
