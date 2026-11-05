# `.agent/` — Workflow V4 运行时命名空间

`.agent/` 存放行为规范、角色能力、工作记录与工程工具。V4 没有工作流引擎：工作流由 **Lead Agent** 组织，事实写在 Markdown 与 Git 里。

## 版本

`.agent/VERSION` = `4.0.0`

V4.0.0 相对 V3.4.0 的变化：

- 删除 Workflow Engine、`state.yaml`、Schema 解析、五个 Gate、Stage 推导、Route 选择、固定交接矩阵与为它们服务的二进制；
- 删除 `HANDOFF_PROMPT`，交接固定 4 个字段 + 可选证据行；
- 角色从「流水线工位」改为「能力边界」，由 Lead 按需调用，可并行、可多轮、可跳过；
- 工作记录改为六份普通 Markdown：`task.md`、`plan.md`、`handoff.md`、`findings.md`、`delivery.md`、`decisions.md`；
- 失败处理简化为「判断原因 → 最多两次有依据的尝试 → 升级 Lead → 必要时升级 Owner」；
- 发布只产出源码资产与发布记录，不再构建 Engine 二进制。

V3.4.0 的规范与证据归档在 `docs/history/v3.4.0/`，实现保存在 Git 历史中。

## 入口

| 用途 | 路径 |
| --- | --- |
| 唯一入口 | `specs/WorkflowAgentDirective.md` |
| 协作原则 | `specs/AgentCollaborationSpecification.md` |
| 权责边界 | `specs/Five-AgentResponsibilityBoundary.md` |
| 交接格式 | `specs/WorkflowHandoffContract.md` |
| Git 纪律 | `specs/WorkflowGitLifecycle.md` |
| 失败处理 | `specs/WorkflowLoopCircuitBreaker.md` |
| 安装/升级 | `specs/WorkflowAdoption.md` |
| 角色 Prompt | `roles/`（7 个） |
| 记录模板 | `templates/task/` |
| 安装工具 | `adoption/workflow-adoption` |
| 只读 Git 事实 | `tools/git-facts` |

## 目录

```text
.agent/
  VERSION                 框架版本
  README.md               本文件
  specs/                  7 份行为规范
  roles/                  7 个角色 Prompt
  templates/              任务记录模板、接入模板、provenance 模板
  adoption/               安装 / 升级 / 回滚 / 校验 / 一致性检查
  tools/git-facts         只读 Git 事实（分支、HEAD、上游、领先落后）
  tasks/                  宿主任务记录（安装时不创建、不覆盖）
  upstream.json           安装来源与文件哈希（只由成功事务写入）
```

`registry/` 在 V4 中不存在。V3 的共享资源分配机制随 Engine 一并删除；如宿主需要资源登记，用普通 Markdown 记录，不引入机器 schema。

## 角色调用

```text
以 CODER 角色执行任务：
.agent/tasks/<task-slug>/
```

角色读取入口规范与任务目录全部记录，输出开工声明，结束时落盘并给出 4 字段交接块。

## 安装与升级

```bash
.agent/adoption/workflow-adoption dist    --source . --output dist/
.agent/adoption/workflow-adoption install --target /path/to/host --package <tgz> \
  --distribution-sha256 <sha> --source-commit <40-hex> \
  --integration-branch <branch> --owner <owner> --owner-authorized --execute
.agent/adoption/workflow-adoption upgrade --target /path/to/host --package <tgz> \
  --distribution-sha256 <sha> --source-commit <40-hex> --owner <owner> --owner-authorized --execute
.agent/adoption/workflow-adoption rollback --target /path/to/host --execute
.agent/adoption/workflow-adoption verify   --target /path/to/host
.agent/adoption/workflow-adoption check    --repo .
```

`check` 是静态一致性检查（文件集合、规范与角色完整性、禁止术语、记录必需文件），不解析任务状态，不是工作流引擎。

## 重要

- 历史任务记录是证据，不是当前规范；
- 不得恢复 Engine / Gate / Stage / `state.yaml`；
- 不得把「流程跑完」当作「验证通过」。
