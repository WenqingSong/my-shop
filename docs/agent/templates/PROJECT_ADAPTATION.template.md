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

## 6. Registry Initialization

Migration Registry:
- Status
- Namespace
- Initial State
- Authority

Error Code Registry:
- Status
- Namespace
- Initial State
- Authority

Other Shared Resource Registry:
（只有项目确实需要时才增加）

> 不要写死所有项目都有 migration / error code。

---

## 7. Validator Adaptation

workflow-check:

Compatibility:
- READY
- NEEDS_PROJECT_ADAPTATION
- NOT_APPLICABLE
- BLOCKED

Project Coupling Found:

Adaptation Applied:

Validation:

---

## 8. Verification

- Workflow Design
- Agent Prompts
- Task templates
- Git authority
- Registry
- Validator
- Machine Config (.agent/workflow.yaml 可解析 / integration branch 可用 / registry 可读取)
- Project build/test mapping
- Owner decisions
- Materialization completeness

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
