package workflow

import (
	"fmt"
	"strings"
	"testing"
)

// The public Discovery starts from Intake facts only, before any task_type or
// Task State exists. No Prompt path is a machine input to this Gate.
func TestUnifiedDiscoveryBeforeWorkflowChoice(t *testing.T) {
	r, _ := setupIntakeRepo(t)
	v := r.validator()
	expectStatus(t, v.GateDiscoveryStart(".agent/intake/my-intake"), StatusPass)
	if res := v.GateDiscoveryDecision(".agent/intake/my-intake"); res.Status == StatusPass {
		t.Fatal("investigation must not imply Owner approval")
	}
	if res := v.GateDiscoveryHandoff(".agent/intake/my-intake"); res.Status == StatusPass {
		t.Fatal("no approved Task type means no TaskBuilder handoff")
	}
}

// All three Owner-approved types use the same Intake/TaskBuilder machine
// protocol. Each temporary real Git repo has no Role files at all: machine
// validation binds identifiers/evidence, not the removed workflow Prompt paths.
func TestUnifiedTaskBuilderApprovedRoutes(t *testing.T) {
	for _, tc := range []struct {
		typ, role, contract string
	}{
		{TaskTypeNormal, RoleCoder, ContractNotRequired},
		{TaskTypeComplex, RoleAnalyst, ContractPending},
		{TaskTypeDesign, RoleArchitect, ContractNotRequired},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			r, base := setupTaskBuilderRepo(t)
			s := v4NormalState(base)
			s.TaskType = tc.typ
			s.Contract = Contract{Status: tc.contract}
			s.Intake = &IntakeRef{ID: "my-intake"}
			if tc.typ == TaskTypeDesign {
				s.Design = &Design{Status: DesignPending}
			}
			r.writeState(s)
			r.write(ownerDecisionPath("my-intake"), fmt.Sprintf("---\ndecision: REJECTED\nintake_id: my-intake\ntask_id: demo\ntask_type: %s\nparent_branch: develop\nbase_commit: %s\n---\nTEST FIXTURE ONLY: no real Owner authorization\n", tc.typ, base))
			r.write(taskBuilderHandoffPath("my-intake"), fmt.Sprintf("---\nintake_id: my-intake\nowner_decision: APPROVED\nparent_branch: develop\nbase_commit: %s\ntask_type: %s\ntask_id: demo\n---\nTEST FIXTURE handoff mirror is not approval\n", base, tc.typ))
			r.commit("TEST FIXTURE route without Owner approval")
			r.syncOrigin("feature/agent-workflow-v2")
			if res := r.validator().GateTaskBuilderInit(".agent/tasks/demo"); res.Status == StatusPass {
				t.Fatal("Handoff mirror must not bypass original Owner rejection")
			}
			// Explicit test-only approval, bound to this type and unchanged Base.
			raw := r.git("show", "HEAD:"+ownerDecisionPath("my-intake"))
			r.write(ownerDecisionPath("my-intake"), strings.Replace(raw, "decision: REJECTED", "decision: APPROVED", 1)+"\n")
			r.commit("TEST FIXTURE bound Owner approval")
			r.syncOrigin("feature/agent-workflow-v2")
			expectStatus(t, r.validator().GateTaskBuilderInit(".agent/tasks/demo"), StatusPass)

			// Type drift in State cannot be routed with the previous approval.
			s.TaskType = TaskTypeNormal
			if tc.typ == TaskTypeNormal {
				s.TaskType = TaskTypeComplex
			}
			r.writeState(s)
			r.commit("TEST FIXTURE conflicting task type")
			r.syncOrigin("feature/agent-workflow-v2")
			if res := r.validator().GateTaskBuilderInit(".agent/tasks/demo"); res.Status == StatusPass {
				t.Fatal("TaskBuilder must not change the approved type")
			}
			s.TaskType = tc.typ
			r.writeState(s)
			evidence := r.commit("TEST FIXTURE restore approved route")
			s.Handoff = &Handoff{Prepared: true, Scope: HandoffScopeTask, ScopeID: "demo", Workflow: tc.typ,
				NextRole: tc.role, NextAction: "TEST FIXTURE initial route", SourceCommit: evidence,
				Evidence: &EvidenceSnapshot{Version: 1, Items: []EvidenceItem{{Kind: "task", SHA: evidence, Path: ".agent/tasks/demo/task.md"}}}}
			r.writeState(s)
			r.commit("TEST FIXTURE public TaskBuilder handoff")
			r.syncOrigin("feature/agent-workflow-v2")
			expectStatus(t, r.validator().GateHandoffPrepared(".agent/tasks/demo"), StatusPass)
			expectStatus(t, r.validator().GateHandoffEntry(".agent/tasks/demo"), StatusPass)
			s.Handoff.Ack = &HandoffAck{Version: 1, AckedBy: tc.role, Target: evidence}
			r.writeState(s)
			r.commit("TEST FIXTURE receiver ACK")
			r.syncOrigin("feature/agent-workflow-v2")
			switch tc.typ {
			case TaskTypeNormal:
				expectStatus(t, r.validator().GateCoderStart(".agent/tasks/demo"), StatusPass)
			case TaskTypeComplex:
				if res := r.validator().GateCoderStart(".agent/tasks/demo"); res.Status == StatusPass {
					t.Fatal("Analyst route still requires fresh Contract approval before Coder")
				}
			case TaskTypeDesign:
				expectStatus(t, r.validator().GateDesignArchitectStart(".agent/tasks/demo"), StatusPass)
				if res := r.validator().GateDesignDocsSpecWriterStart(".agent/tasks/demo"); res.Status == StatusPass {
					t.Fatal("new DESIGN route must not inherit architecture approval")
				}
			}
		})
	}
}

func TestUnifiedEntryDoesNotInitializeProgramAsTask(t *testing.T) {
	for _, typ := range []string{WorkflowCoordinated, "PROGRAM"} {
		_, err := parseTaskBuilderHandoff([]byte(fmt.Sprintf("intake_id: valid\nowner_decision: APPROVED\nparent_branch: main\nbase_commit: %s\ntask_type: %s\ntask_id: demo\n", strings.Repeat("a", 40), typ)))
		if err == nil || !strings.Contains(err.Error(), "task_type") {
			t.Fatalf("Program must stay outside Task initialization: %s: %v", typ, err)
		}
	}
}
