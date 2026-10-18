package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestV421InternalInstructionsAreL3(t *testing.T) {
	if classifyConflict([]string{".agent/AGENTS.md"}) != MergeL3 {
		t.Fatal("internal instructions must retain contract conflict protection")
	}
}

// Real Git + local bare remote; no business project or real Owner approvals.
func TestV421MergerSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		pass          bool
	}{
		{"unchanged", "test -f internal/foo.go", true},
		{"external-output", `printf output > "$WORKFLOW_TEST_OUTPUT_DIR/report"`, true},
		{"tracked", "printf changed >> internal/foo.go", false},
		{"index", "printf changed >> internal/foo.go; git add internal/foo.go", false},
		{"commit", "printf changed >> internal/foo.go; git add internal/foo.go; git commit -qm changed", false},
		{"untracked", "printf changed > internal/new.go", false},
		{"ignored", "printf changed > ignored-output", false},
		{"assume-unchanged", "git update-index --assume-unchanged internal/foo.go; printf changed >> internal/foo.go", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, base, _ := setupDeliverable(t)
			r.switchTo("feature/x")
			r.write(".gitignore", "ignored-output\n")
			src := r.commit("TEST FIXTURE output policy")
			r.push("feature/x")
			r.switchTo("develop")
			rep, err := r.merger().Integrate(MergeRequest{
				ParentBranch: "develop", ParentBaseSHA: base,
				Sources:         []MergeSource{{Branch: "feature/x", SHA: src}},
				OwnerAuthorized: true, TestCommand: []string{"sh", "-ec", tc.command},
				ScratchDir: filepath.Join(t.TempDir(), "scratch"),
			})
			if err != nil {
				t.Fatal(err)
			}
			if rep.TestPassed != tc.pass || rep.ParentUpdated != tc.pass {
				t.Fatalf("unexpected result: %+v", rep)
			}
			want := base
			if tc.pass {
				want = rep.MergeCommit
			} else if !strings.Contains(rep.Reason, "快照不一致") {
				t.Fatalf("missing snapshot rejection: %+v", rep)
			}
			if r.remoteSHA("develop") != want {
				t.Fatal("remote parent differs from the validated result")
			}
		})
	}
}

func TestV421TaskBuilderApprovalRecheck(t *testing.T) {
	for _, fault := range []string{"none", "missing-owner", "rejected-owner", "handoff-id", "record-id", "owner-task", "owner-base", "legacy-unbound"} {
		t.Run(fault, func(t *testing.T) {
			r, base := setupTaskBuilderRepo(t)
			switch fault {
			case "missing-owner":
				if err := os.Remove(filepath.Join(r.dir, ownerDecisionPath("my-intake"))); err != nil {
					t.Fatal(err)
				}
			case "rejected-owner":
				writeOwnerDecision(t, r, OwnerDecisionRejected)
			case "handoff-id":
				p := filepath.Join(r.dir, taskBuilderHandoffPath("my-intake"))
				b, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				r.write(taskBuilderHandoffPath("my-intake"), strings.Replace(string(b), "intake_id: my-intake", "intake_id: other", 1))
			case "owner-task", "owner-base":
				p := filepath.Join(r.dir, ownerDecisionPath("my-intake"))
				b, err := os.ReadFile(p)
				if err != nil {
					t.Fatal(err)
				}
				old, replacement := "task_id: demo", "task_id: other"
				if fault == "owner-base" {
					old, replacement = "base_commit: "+base, "base_commit: "+strings.Repeat("a", 40)
				}
				r.write(ownerDecisionPath("my-intake"), strings.Replace(string(b), old, replacement, 1))
			case "legacy-unbound":
				r.write(ownerDecisionPath("my-intake"), "---\ndecision: APPROVED\n---\nTEST FIXTURE legacy unbound decision\n")
			case "record-id":
				r.write(intakeYAMLPath("my-intake"), fmt.Sprintf("intake_id: other\nparent_branch: develop\nbase_commit: %s\n", base))
			}
			if fault != "none" {
				r.commit("TEST FIXTURE conflicting approval")
				r.syncOrigin("feature/agent-workflow-v2")
			}
			res := r.validator().GateTaskBuilderInit(".agent/tasks/demo")
			if (res.Status == StatusPass) != (fault == "none") {
				t.Fatalf("unexpected approval result: %+v", res)
			}
		})
	}
}

// Architecture revision is a new Owner-approved DESIGN task, not an in-place
// clearing of old approvals. Existing schema/Gates express the complete path.
func TestV421DesignRevisionSuccessor(t *testing.T) {
	r, old := setupDesignFinalApproved(t)
	oldPath := filepath.Join(r.dir, ".agent/tasks/demo/state.yaml")
	original, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	if res := r.validator().GateDesignArchitectStart(".agent/tasks/demo"); res.Status == StatusPass {
		t.Fatal("must not reopen the approved original architecture")
	}
	const id = "demo-revision"
	const branch = "feature/design-revision"
	base := old["base"]
	oldRepo := r
	newDir := filepath.Join(t.TempDir(), "revision")
	gitAt(t, r.dir, "worktree", "add", "-b", branch, newDir, base)
	r = &testRepo{dir: newDir, t: t}
	r.write(intakeYAMLPath(id), fmt.Sprintf("intake_id: %s\nparent_branch: develop\nbase_commit: %s\n", id, base))
	r.write(ownerDecisionPath(id), "---\ndecision: REJECTED\n---\nTEST FIXTURE: architecture revision needs explicit Owner approval\n")
	r.write(taskBuilderHandoffPath(id), fmt.Sprintf("---\nintake_id: %s\nowner_decision: APPROVED\nparent_branch: develop\nbase_commit: %s\ntask_type: DESIGN\ntask_id: %s\n---\n", id, base, id))
	s := designState(base)
	s.TaskID = id
	s.Git.TaskBranch = branch
	s.Intake = &IntakeRef{ID: id}
	s.Review = Review{Status: ReviewNotRequested}
	s.Owner = Owner{Status: OwnerPending}
	s.Delivery = Delivery{Status: DeliveryNotRun}
	b, err := yaml.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	dir := ".agent/tasks/" + id
	r.write(dir+"/state.yaml", string(b))
	r.write(dir+"/task.md", "# TEST FIXTURE architecture revision\nPredecessor: .agent/tasks/demo; approved architecture SHA: "+old["A1"]+"\n")
	r.commit("TEST FIXTURE revision pending Owner")
	r.syncOrigin(branch)
	if res := r.validator().GateTaskBuilderInit(dir); res.Status == StatusPass {
		t.Fatal("revision without Owner approval must fail initialization")
	}
	r.write(ownerDecisionPath(id), fmt.Sprintf("---\ndecision: APPROVED\nintake_id: %s\ntask_id: %s\ntask_type: DESIGN\nparent_branch: develop\nbase_commit: %s\n---\nTEST FIXTURE ONLY: Owner authorizes new DESIGN revision scope\n", id, id, base))
	r.commit("TEST FIXTURE authorize revision")
	r.syncOrigin(branch)
	expectStatus(t, r.validator().GateTaskBuilderInit(dir), StatusPass)
	initSHA := r.git("rev-parse", "HEAD")
	s.Handoff = &Handoff{Prepared: true, Scope: HandoffScopeTask, ScopeID: id,
		Workflow: TaskTypeDesign, NextRole: RoleArchitect, NextAction: "TEST FIXTURE architecture revision",
		SourceCommit: initSHA, Evidence: &EvidenceSnapshot{Version: 1, Items: []EvidenceItem{{Kind: "task", SHA: initSHA, Path: dir + "/task.md"}}}}
	b, err = yaml.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	r.write(dir+"/state.yaml", string(b))
	r.commit("TEST FIXTURE prepare revision handoff")
	r.syncOrigin(branch)
	expectStatus(t, r.validator().GateHandoffPrepared(dir), StatusPass)
	expectStatus(t, r.validator().GateHandoffEntry(dir), StatusPass)
	s.Handoff.Ack = &HandoffAck{Version: 1, AckedBy: RoleArchitect, Target: initSHA}
	b, err = yaml.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	r.write(dir+"/state.yaml", string(b))
	r.commit("TEST FIXTURE Architect ACK")
	r.syncOrigin(branch)
	expectStatus(t, r.validator().GateDesignArchitectStart(dir), StatusPass)
	if res := r.validator().GateDesignDocsSpecWriterStart(dir); res.Status == StatusPass {
		t.Fatal("old architecture approval must not authorize revised design")
	}
	r.write(dir+"/architecture.md", "# revised architecture\n")
	a1 := r.commit("TEST FIXTURE revision architecture")
	s.Design = &Design{Status: DesignApproved, ApprovedVersion: 1, ApprovedTarget: a1}
	b, err = yaml.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	r.write(dir+"/state.yaml", string(b))
	r.commit("TEST FIXTURE new architecture approval")
	r.syncOrigin(branch)
	expectStatus(t, r.validator().GateDesignDocsSpecWriterStart(dir), StatusPass)
	if res := r.validator().GateDesignFinalApproval(dir); res.Status == StatusPass {
		t.Fatal("old CLEAN/final approval must not carry into successor")
	}
	after, err := os.ReadFile(filepath.Join(oldRepo.dir, ".agent/tasks/demo/state.yaml"))
	if err != nil || string(after) != string(original) {
		t.Fatal("historical state changed")
	}
}
