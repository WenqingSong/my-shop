# PROJECT ADAPTATION

Status: TEMPORARY

Purpose:
Workflow V2 Project Adoption Workspace

Lifecycle:
DISCOVER → RESOLVE → MATERIALIZE → VERIFY → CLEANUP

Deletion Rule:
Only remove this file after ADOPTION READY.

---

## 1. Adoption Metadata

- Project:
- Repository:
- Date:
- Current Branch:
- Workflow Version:
- Installation Mode: FIRST_INSTALL / UPGRADE
- Source Distribution / Commit:
- Distribution SHA256:
- Existing `.agent/upstream.yaml`: YES / NO
- B Snapshot Available (upgrade): YES / NO / NOT_APPLICABLE

Adoption Status:

- DISCOVER
- WAITING_FOR_OWNER_DECISION
- MATERIALIZING
- VERIFYING
- READY
- BLOCKED

> 这些状态只属于 Adoption 临时工作区。不要把它们加入正常 Workflow V2 state schema。

---

## 2. Detected Project Facts

由 Agent 在 DISCOVER 阶段填写。只记录真实检测到的事实，不臆造。

- Language:
- Go Module:
- Repository Model:
- Framework:
- Database:
- Cache:
- Messaging:
- Deployment:

Branch Candidates:

Build System:
Test System:

Migration Tool:
Migration Path:
Migration Version Strategy:

Existing Design Root:

Existing Workflow Assets:

---

## 3. Proposed Workflow Mapping

由 Agent 在 DISCOVER 后形成 Proposed Adaptation。可靠推断直接写死，需 Owner 决策的进 §4。

- Shared Integration Branch:
- Feature Branch Convention:
- Design Authority Root:
- Workflow Task Root:
- Registry Root:
- Machine Config (.agent/workflow.yaml): integration_branch + registry 映射

Migration Registry: ENABLED / DISABLED / NOT_APPLICABLE

Error Code Registry: ENABLED / DISABLED / NOT_APPLICABLE

Validation Commands:

---

## 4. Owner Decisions

只记录真正需要 Owner 决策的内容。每项使用：

- Decision ID:
- Question:
- Detected Evidence:
- Options:
- Recommendation:
- Owner Decision:
- Status:

> 禁止预生成几十个空 Decision。有问题才增加。

---

## 5. Materialization Plan

| Item | Final Decision | Authoritative Destination | Action | Status |

> 最终每一个重要 Adaptation Fact 都必须找到长期权威 Destination。
> PROJECT_ADAPTATION.md 本身不能作为长期 Destination。
> 必含长期机器配置 `.agent/workflow.yaml`：至少 materialize integration branch 与适用的 Registry 路径（它长期存在，不是 temporary）。

---

## 6. Shared Resource Kinds 与 Registry Initialization

先确定当前项目到底有哪些 Shared Resource Kinds（例如 `migration_version`、`error_code_domain`、`im_migration_version`、`idp_migration_version`，或 `NONE`），再逐个初始化对应 Registry：

| Resource Kind | Registry Path | Namespace | Initial State | Authority |
| --- | --- | --- | --- | --- |
| <kind> | .agent/registry/<...>.md | ... | ... | Analyst Registry-only |

> kind 名必须是合法 `lower_snake_case`（`^[a-z][a-z0-9_]*$`）；每个 kind 的 registry 路径必须严格位于 `.agent/registry/` 下，且不能 absolute、不能 `..` traversal。
> 不要写死所有项目都有 migration / error code Registry——只有项目确实存在并需要全局分配冲突控制时，才声明对应 kind。

---

## 7. Validator Adaptation

workflow-check（`.agent/engine/`，独立 Module `workflow-v2-engine`）:

Compatibility:
- READY
- NEEDS_PROJECT_ADAPTATION
- NOT_APPLICABLE
- BLOCKED

Project Coupling Found:

Adaptation Applied:

Validation:
- `.agent/engine/go.mod` 存在（`module workflow-v2-engine`）
- `go -C .agent/engine build ./...` PASS
- `go -C .agent/engine build -o ../bin/workflow-check ./cmd/workflow-check` PASS
- build 后 `git status --short` 不因 binary 变 dirty（`.agent/bin/` 被 ignore）
- `.agent/bin/workflow-check` 能读取 materialized `.agent/workflow.yaml` 并执行基础 Gate
- Engine 不依赖宿主业务 Go Module（无业务 module import / 无 `go.work` / 无 root `replace`）

---

## 8. Verification

- Workflow Design
- Workflow Agent Directive（`.agent/specs/WorkflowAgentDirective.md` 存在，七个 Role Prompt（含 `ArchitectAgent.md`）显式读取）
- Legacy directive entry（不存在指向已删除 `AGENTS.template.md` 的有效入口）
- Agent Prompts
- Task templates
- Git authority
- Registry
- Validator
- Machine Config (.agent/workflow.yaml 可解析 / integration branch 可用 / registry 可读取)
- Project build/test mapping
- Owner decisions
- Materialization completeness
- Host root `AGENTS.md` preservation（见 §8.1）

### 8.1 宿主根 `AGENTS.md` 保全记录

Workflow 指令入口是 `.agent/specs/WorkflowAgentDirective.md`；宿主根 `AGENTS.md` 属于宿主项目，只读取，不由 Workflow 创建或改写。

| Check | Result | Evidence |
| --- | --- | --- |
| 接入前是否存在宿主根 `AGENTS.md` | YES / NO | |
| 接入前 SHA256 | | |
| 接入后 SHA256 | | |
| 是否创建 / 覆盖 / 删除 / 清空 | NO（必须） | |
| 旧 Workflow 内容差异 | NONE / REPORTED（附差异清单） | |
| 清理或迁移的 Owner 授权 | NOT_REQUIRED / <Decision ID> | |

判定：

- 接入前 NO → 接入后必须仍为 NO（不创建）；
- 接入前 YES → 接入后 SHA256 必须与接入前一致；
- 发现宿主根文件含旧 Workflow 内容时，只输出待迁移差异，不自行删除；清理或迁移需 Owner 明确授权。

### 8.2 宿主保护对象基线

安装 / 升级前后必须记录并比较；任何未授权差异都视为失败：

| Protected Object | Before SHA256 | After SHA256 | Result |
| --- | --- | --- | --- |
| Root `README.md` | | | UNCHANGED / N/A |
| Root `PACKAGE_MANIFEST.md` | | | UNCHANGED / N/A |
| `.cnb.yml` / host CI | | | UNCHANGED / N/A |
| `docs/design/**` | | | UNCHANGED / N/A |
| `.agent/workflow.yaml` | | | UNCHANGED / CREATED_BY_INSTALL |
| `.agent/tasks/**` | | | UNCHANGED |
| `.agent/registry/**` | | | UNCHANGED / NOT_ENABLED |
| Historical Evidence / blocked / DANGER | | | UNCHANGED |

> Upgrade 报告中必须列出 B/L/N 决策与冲突；不得在存在 CONFLICT / BASE_UNKNOWN
> 时声称升级成功。历史非法 Evidence 只报告并标记重新核验，不改写 state。

---

## 9. Cleanup Check

- [ ] All required facts resolved
- [ ] Owner decisions resolved
- [ ] Facts materialized
- [ ] Registry initialized
- [ ] Validator verified
- [ ] No blocker
- [ ] No critical information exists only in this temporary file

只有全部通过：PROJECT_ADAPTATION.md may be deleted.

---

## 10. Adoption Result

RESULT: READY / NOT_READY

Remaining Issues:

Cleanup:

PROJECT_ADAPTATION.md: REMOVED / RETAINED
