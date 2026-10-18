package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupInitialTask(t *testing.T, typ TaskType) (*testRepo, State) {
	t.Helper()
	r, base := setupTaskBuilderRepo(t)
	s := v4NormalState(base)
	s.TaskType = typ
	s.Intake = &IntakeRef{ID: "my-intake"}
	if typ == TaskTypeComplex {
		s.Contract.Status = ContractPending
	}
	if typ == TaskTypeDesign {
		s.Design = &Design{Status: DesignPending}
	}
	for _, p := range []string{ownerDecisionPath("my-intake"), taskBuilderHandoffPath("my-intake")} {
		r.write(p, strings.ReplaceAll(r.git("show", "HEAD:"+p), "task_type: NORMAL", "task_type: "+typ)+"\n")
	}
	r.writeState(s)
	r.write(".agent/tasks/demo/task.md", "# TEST FIXTURE approved initial "+typ+"\n")
	r.commit("TEST FIXTURE approved initial " + typ)
	r.syncOrigin("feature/agent-workflow-v2")
	return r, s
}

func initialTaskHandoff(r *testRepo, s State, role string) *Handoff {
	return &Handoff{Prepared: true, Scope: HandoffScopeTask, ScopeID: s.TaskID,
		Workflow: s.TaskType, NextRole: role, NextAction: "TEST FIXTURE initial route",
		SourceCommit: r.git("rev-parse", "HEAD"),
		Evidence:     &EvidenceSnapshot{Version: 1, Items: []EvidenceItem{{Kind: "task", SHA: r.git("rev-parse", "HEAD"), Path: ".agent/tasks/demo/task.md"}}}}
}

func TestTaskBuilderInitialPolicyRejectsWrongState(t *testing.T) {
	for _, tc := range []struct {
		name, typ string
		mutate    func(*testRepo, *State)
	}{
		{"normal-contract", TaskTypeNormal, func(_ *testRepo, s *State) { s.Contract.Status = ContractPending }},
		{"complex-contract", TaskTypeComplex, func(_ *testRepo, s *State) { s.Contract.Status = ContractNotRequired }},
		{"design-contract", TaskTypeDesign, func(_ *testRepo, s *State) { s.Contract.Status = ContractPending }},
		{"design-missing", TaskTypeDesign, func(_ *testRepo, s *State) { s.Design = nil }},
		{"design-not-required", TaskTypeDesign, func(_ *testRepo, s *State) { s.Design.Status = DesignNotRequired }},
		{"inherited-contract", TaskTypeComplex, func(r *testRepo, s *State) { s.Contract.Target = r.git("rev-parse", "HEAD") }},
		{"inherited-clean", TaskTypeNormal, func(r *testRepo, s *State) {
			s.Review = Review{Status: ReviewClean, Target: r.git("rev-parse", "HEAD")}
		}},
		{"inherited-owner", TaskTypeNormal, func(r *testRepo, s *State) {
			s.Owner = Owner{Status: OwnerAccepted, ReviewTarget: r.git("rev-parse", "HEAD")}
		}},
		{"inherited-delivery", TaskTypeNormal, func(r *testRepo, s *State) {
			sha := r.git("rev-parse", "HEAD")
			s.Delivery = Delivery{Status: DeliveryPass, ReviewTarget: sha, FeatureHead: sha, DevelopBase: s.Git.BaseCommit}
		}},
		{"blocked", TaskTypeNormal, func(_ *testRepo, s *State) {
			s.Blocked = Blocked{Active: true, By: RoleTaskBuilder, Reason: "TEST FIXTURE"}
		}},
		{"premature-reservation", TaskTypeNormal, func(_ *testRepo, s *State) { s.Resources.Reservations = map[string][]string{"code": {"unassigned"}} }},
		{"wrong-role", TaskTypeNormal, func(r *testRepo, s *State) { s.Handoff = initialTaskHandoff(r, *s, RoleMerger) }},
		{"wrong-workflow", TaskTypeComplex, func(r *testRepo, s *State) {
			s.Handoff = initialTaskHandoff(r, *s, RoleAnalyst)
			s.Handoff.Workflow = TaskTypeNormal
		}},
		{"wrong-worktree", TaskTypeNormal, func(r *testRepo, s *State) {
			s.Git.TaskBranch = "feature/other"
			r.git("worktree", "add", "-b", s.Git.TaskBranch, filepath.Join(t.TempDir(), "other"), s.Git.BaseCommit)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, s := setupInitialTask(t, tc.typ)
			tc.mutate(r, &s)
			r.writeState(s)
			r.commit("TEST FIXTURE invalid initialization " + tc.name)
			r.syncOrigin("feature/agent-workflow-v2")
			expectStatus(t, r.validator().GateTaskBuilderInit(".agent/tasks/demo"), StatusFail)
		})
	}
}

func TestTaskBuilderInitializationRetryIsReadOnly(t *testing.T) {
	for _, tc := range []struct{ typ, role string }{{TaskTypeNormal, RoleCoder}, {TaskTypeComplex, RoleAnalyst}, {TaskTypeDesign, RoleArchitect}} {
		t.Run(tc.typ, func(t *testing.T) {
			r, s := setupInitialTask(t, tc.typ)
			// A crash after commit but before Handoff can safely recheck; the Gate
			// never creates a branch, rewrites approval or fabricates an ACK.
			before := r.git("rev-parse", "HEAD")
			for i := 0; i < 2; i++ {
				expectStatus(t, r.validator().GateTaskBuilderInit(".agent/tasks/demo"), StatusPass)
			}
			if r.git("rev-parse", "HEAD") != before || r.git("status", "--porcelain") != "" {
				t.Fatal("initialization recheck changed Git or files")
			}
			s.Handoff = initialTaskHandoff(r, s, tc.role)
			r.writeState(s)
			r.commit("TEST FIXTURE prepared initial handoff")
			r.syncOrigin("feature/agent-workflow-v2")
			expectStatus(t, r.validator().GateTaskBuilderInit(".agent/tasks/demo"), StatusPass)
		})
	}
}

func TestTaskBuilderRejectsBaseOutsideTaskHistory(t *testing.T) {
	r, s := setupInitialTask(t, TaskTypeNormal)
	parent := filepath.Join(t.TempDir(), "parent")
	r.git("worktree", "add", parent, "develop")
	p := &testRepo{dir: parent, t: t}
	p.write("README.md", "TEST FIXTURE parent-only change\n")
	newBase := p.commit("TEST FIXTURE newly approved parent base")
	// All approval mirrors agree, and newBase exists on the parent, but the
	// existing Task branch was created from the old Base and did not inherit it.
	for _, path := range []string{intakeYAMLPath("my-intake"), ownerDecisionPath("my-intake"), taskBuilderHandoffPath("my-intake")} {
		r.write(path, strings.ReplaceAll(r.git("show", "HEAD:"+path), s.Git.BaseCommit, newBase)+"\n")
	}
	s.Git.BaseCommit = newBase
	r.writeState(s)
	r.commit("TEST FIXTURE mutually matching but non-ancestral Base")
	r.syncOrigin("feature/agent-workflow-v2")
	expectStatus(t, r.validator().GateTaskBuilderInit(".agent/tasks/demo"), StatusFail)
}

func TestTaskBuilderLegacyReadDoesNotUpgrade(t *testing.T) {
	for _, version := range []int{SchemaV2, SchemaV3} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			r := newTestRepo(t)
			r.write("README.md", "TEST FIXTURE historical task\n")
			resources := "  reservations: {}\n"
			if version == SchemaV2 {
				resources = "  migrations: []\n  error_code_domains: []\n"
			}
			r.write(".agent/tasks/legacy/state.yaml", fmt.Sprintf("schema_version: %d\ntask_id: legacy\nresources:\n%s", version, resources))
			sha := r.commit("TEST FIXTURE immutable legacy schema")
			path := filepath.Join(r.dir, ".agent/tasks/legacy/state.yaml")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			v := r.validator()
			s, res := v.loadStateFile(".agent/tasks/legacy")
			expectStatus(t, res, StatusPass)
			if s.SchemaVersion != version {
				t.Fatal("legacy read changed schema")
			}
			expectStatus(t, v.GateTaskBuilderInit(".agent/tasks/legacy"), StatusError)
			expectStatus(t, v.GateHandoffEntry(".agent/tasks/legacy"), StatusError)
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) || r.git("rev-parse", "HEAD") != sha || r.git("status", "--porcelain") != "" {
				t.Fatal("read/rejected initialization changed historical evidence")
			}
		})
	}
}
