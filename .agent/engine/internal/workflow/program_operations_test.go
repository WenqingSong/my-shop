package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// All approvals below are explicit TEST FIXTURES, never real Owner decisions.
func setupControlledProgram(t *testing.T) (*mRepo, ProgramState, []ProgramDeliveryProof) {
	t.Helper()
	r := newMRepo(t)
	r.checkout("develop")
	r.write(".gitignore", ".agent/programs/*/registry.yaml.lock\n")
	r.write(".agent/workflow.yaml", defaultWorkflowConfig)
	r.write("go.mod", "module controlled-program-fixture\n\ngo 1.23.0\n")
	r.write("README.md", "TEST FIXTURE ONLY: two independently reviewed/accepted Go tasks; run go test ./...\nAC: Add(2,3)=5; Double(3)=6; combined result Double(Add(2,3))=10.\n")
	base := r.commit("TEST FIXTURE base")
	r.push("develop")
	p := ProgramState{SchemaVersion: 1, ProgramID: "prog-1", ParentBranch: "develop", BaseCommit: base, Tasks: []ProgramTask{{TaskID: "a", TaskType: TaskTypeNormal, Branch: "feature/a"}, {TaskID: "b", TaskType: TaskTypeNormal, Branch: "feature/b"}}, FinalIntegration: ProgramFinalIntegration{Status: "NOT_RUN"}, Review: ProgramReview{Status: ReviewNotRequested}, Owner: ProgramOwner{Status: OwnerPending}}
	r.checkout("coordinator/prog-1")
	writeMProgramState(t, r, p)
	plan := r.commit("TEST FIXTURE Owner Program plan")
	r.push("coordinator/prog-1")
	var proofs []ProgramDeliveryProof
	for _, id := range []string{"a", "b"} {
		branch := "feature/" + id
		r.checkout(branch, plan)
		fn, expr, want := "Add(a,b int)", "a+b", "Add(2,3)!=5"
		if id == "b" {
			fn = "Double(a int)"
			expr = "a*2"
			want = "Double(3)!=6"
		}
		r.write(id+".go", "package calculation\nfunc "+fn+" int { return "+expr+" }\n")
		r.write(id+"_test.go", "package calculation\nimport \"testing\"\nfunc Test"+strings.ToUpper(id)+"(t *testing.T){if "+want+"{t.Fatal(\"wrong result\")}}\n")
		r.write(".agent/tasks/"+id+"/task.md", "# TEST FIXTURE Task "+id+"\nAC: "+want+" must be false.\n")
		target := r.commit("TEST FIXTURE implementation " + id)
		raw := fmt.Sprintf(`schema_version: 4
task_id: %s
task_type: NORMAL
git: {parent_branch: develop, base_commit: %s, task_branch: %s}
program: {id: prog-1, version: 1}
contract: {status: NOT_REQUIRED, target: ""}
resources: {reservations: {}}
review: {status: CLEAN, target: %s}
owner: {status: ACCEPTED, review_target: %s}
delivery: {status: NOT_RUN, review_target: "", feature_head: "", develop_base: ""}
blocked: {active: false, by: "", reason: ""}
handoff:
 prepared: true
 scope: task
 scope_id: %s
 workflow: NORMAL
 next_role: Merger
 source_commit: %s
 next_action: TEST FIXTURE verify only
 entry: delivery-start
 evidence:
  version: 1
  items: [{kind: task, sha: %s, path: .agent/tasks/%s/task.md}]
 ack: {version: 1, acked_by: Merger, target: %s}
`, id, base, branch, target, target, id, target, target, id, target)
		r.write(".agent/tasks/"+id+"/state.yaml", raw)
		r.commit("TEST FIXTURE child Cleaner/Owner/Handoff prerequisites")
		r.push(branch)
		dc := r.deliveryController("go", "test", "./...")
		rep, res := dc.VerifyTask(id)
		if res.Status != StatusPass || !rep.TestPassed {
			t.Fatalf("real child verify %s: %+v %+v", id, res, rep)
		}
		r.commit("TEST FIXTURE native verified delivery metadata")
		r.push(branch)
		proof, err := ResolveProgramDelivery(r.host, p, id)
		if err != nil {
			t.Fatal(err)
		}
		if proof.SourceTip == proof.Delivery.DeliverySHA {
			t.Fatal("fixture must reproduce distinct verified and metadata SHA")
		}
		proofs = append(proofs, proof)
	}
	r.switchTo("coordinator/prog-1")
	for i, id := range []string{"a", "b"} {
		if _, err := CollectProgram(r.host, p.ProgramID, id, i); err != nil {
			t.Fatal(err)
		}
		r.commit("TEST FIXTURE controlled Collector " + id)
		r.push("coordinator/prog-1")
	}
	return r, p, proofs
}

func TestProgramControlledFullChain(t *testing.T) {
	r, p, proofs := setupControlledProgram(t)
	if st, e := ProgramTaskStateLoader(r.host, p.ProgramID)("a"); e != nil || st.Delivery.FeatureHead != proofs[0].Delivery.DeliverySHA {
		t.Fatalf("coordinator snapshot loader: %+v %v", st, e)
	}
	out, err := AssembleProgram(r.host, p.ProgramID, "assembly/prog-1", []string{"go", "test", "./..."})
	if err != nil {
		t.Fatal(err)
	}
	if out.C0 == "" || out.Report.Level != MergeL1 || !out.Report.TestPassed || out.Report.ParentUpdated {
		t.Fatalf("invalid actual result %+v", out)
	}
	for _, pr := range proofs {
		if ok, _ := (&ExecGit{Root: r.host}).IsAncestor(pr.SourceTip, out.C0); !ok {
			t.Fatal("missing metadata source")
		}
	}
	r.push("assembly/prog-1")
	r.checkout("review/prog-1", out.C0)
	r.write(ProgramCleanerEvidencePath(p.ProgramID), cleanerReportYAML(p.ProgramID, out.C0))
	c1 := r.commit("TEST FIXTURE Cleaner report only")
	r.push("review/prog-1")
	p.FinalIntegration = ProgramFinalIntegration{Status: "PASS", SHA: out.C0, Evidence: ".agent/programs/prog-1/integration.json"}
	p.Review = ProgramReview{Status: ReviewClean, Target: out.C0, Evidence: ProgramCleanerEvidencePath(p.ProgramID), EvidenceCommit: c1}
	writeMProgramState(t, r, p)
	r.commit("TEST FIXTURE Assembler and Cleaner metadata")
	r.push("review/prog-1")
	pv := &ProgramValidator{Root: r.host, Git: &ExecGit{Root: r.host}, Store: &RegistryStore{Root: r.host}}
	if res := pv.GateProgramCleaner(p.ProgramID); res.Status != StatusPass {
		t.Fatalf("C0/C1 rejected: %+v", res)
	}
	if res := pv.GateFinalDelivery(p.ProgramID); res.Status != StatusFail {
		t.Fatal("missing Program Owner ACCEPT must fail")
	}
	p.Owner = ProgramOwner{Status: OwnerAccepted, Target: out.C0}
	writeMProgramState(t, r, p)
	r.commit("TEST FIXTURE Program Owner ACCEPT C0")
	r.push("review/prog-1")
	if res := pv.GateFinalDelivery(p.ProgramID); res.Status != StatusPass {
		t.Fatalf("final gate: %+v", res)
	}
	dc := r.deliveryController("go", "test", "./...")
	if _, res := dc.PlanProgram(p.ProgramID, "assembly/prog-1"); res.Status != StatusPass {
		t.Fatalf("delivery plan: %+v", res)
	}
	if r.remoteSHA("develop") != p.BaseCommit {
		t.Fatal("assembly/review changed parent")
	}
	t.Logf("verified=%s metadata=%s C0=%s C1=%s parent=%s", proofs[0].Delivery.DeliverySHA, proofs[0].SourceTip, out.C0, c1, p.BaseCommit)
}

func TestProgramSourceProofRejectsInvalidChanges(t *testing.T) {
	for _, kind := range []string{"substantive", "approval", "sha", "parent"} {
		t.Run(kind, func(t *testing.T) {
			r, p, _ := setupControlledProgram(t)
			if kind == "parent" {
				r.switchTo("develop")
				r.write("parent.txt", "drift")
				r.commit("TEST FIXTURE drift")
				r.push("develop")
			} else {
				r.switchTo("feature/a")
				st, err := LoadTaskStateFile(r.host, "a")
				if err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "substantive":
					r.write("a.go", "package calculation\nfunc Add(a,b int) int{return 0}\n")
				case "approval":
					st.Owner.Status = OwnerRejected
				case "sha":
					st.Delivery.FeatureHead = strings.Repeat("f", 40)
				}
				if kind != "substantive" {
					raw, _ := yaml.Marshal(st)
					r.write(".agent/tasks/a/state.yaml", string(raw))
				}
				r.commit("TEST FIXTURE invalid " + kind)
				r.push("feature/a")
			}
			if _, err := ResolveProgramDelivery(r.host, p, "a"); err == nil {
				t.Fatal("invalid proof accepted")
			}
			r.switchTo("coordinator/prog-1")
			if _, err := AssembleProgram(r.host, p.ProgramID, "assembly/rejected", []string{"go", "test", "./..."}); err == nil {
				t.Fatal("invalid assembly accepted")
			}
		})
	}
}

func TestAssemblerAdapterRejectsPostTestDrift(t *testing.T) {
	for _, branch := range []string{"feature/x", "develop"} {
		t.Run(branch, func(t *testing.T) {
			r, base, source := setupDeliverable(t)
			peer := filepath.Join(t.TempDir(), "peer")
			gitAt(t, filepath.Dir(peer), "clone", "-q", "--branch", branch, r.origin, peer)
			gitAt(t, peer, "config", "user.name", "TEST FIXTURE peer")
			gitAt(t, peer, "config", "user.email", "fixture@example.invalid")
			os.WriteFile(filepath.Join(peer, "drift.txt"), []byte("TEST FIXTURE concurrent change"), 0644)
			gitAt(t, peer, "add", "drift.txt")
			gitAt(t, peer, "commit", "-qm", "TEST FIXTURE concurrent drift")
			script := filepath.Join(t.TempDir(), "test.sh")
			os.WriteFile(script, []byte("#!/bin/sh\nset -eu\ngit -C '"+peer+"' push origin HEAD:refs/heads/"+branch+"\n"), 0755)
			adapter := &MergerMergeOps{Merger: r.merger(), ParentBranch: "develop", BranchOf: map[string]string{source: "feature/x"}, TestCommand: []string{"sh", script}}
			if sha, err := adapter.MergeSources(base, []string{source}); err == nil || sha != "" {
				t.Fatalf("MERGE-L2 accepted: %s %v", sha, err)
			}
		})
	}
}

func TestDesignProgramChildVerificationAndDirectDeliveryRefusal(t *testing.T) {
	r, base, d1 := setupDesignDelivery(t)
	st, err := LoadTaskStateFile(r.host, "demo")
	if err != nil {
		t.Fatal(err)
	}
	st.Program = &ProgramRef{ID: "prog-1", Version: 1}
	st.Handoff = &Handoff{Prepared: true, Scope: HandoffScopeTask, ScopeID: "demo", Workflow: WorkflowDesign, NextRole: RoleMerger, SourceCommit: d1, NextAction: "TEST FIXTURE DESIGN verify only", Entry: "design-final-approval", Evidence: &EvidenceSnapshot{Version: 1, Items: []EvidenceItem{{Kind: "design", SHA: d1, Path: ".agent/tasks/demo/design.md"}}}, Ack: &HandoffAck{Version: 1, AckedBy: RoleMerger, Target: d1}}
	writeMState(t, r, st)
	h0 := r.commit("TEST FIXTURE DESIGN child handoff")
	r.push("feature/design-v1")
	dc := r.deliveryController("test", "-f", ".agent/tasks/demo/design.md")
	rep, res := dc.VerifyDesign("demo")
	if res.Status != StatusPass || !rep.TestPassed {
		t.Fatalf("DESIGN verify: %+v %+v", res, rep)
	}
	r.commit("TEST FIXTURE verified DESIGN metadata")
	r.push("feature/design-v1")
	p := ProgramState{ProgramID: "prog-1", ParentBranch: "develop", BaseCommit: base, Tasks: []ProgramTask{{TaskID: "demo", TaskType: TaskTypeDesign, Branch: "feature/design-v1"}}}
	proof, err := ResolveProgramDelivery(r.host, p, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if proof.Delivery.DeliverySHA != h0 {
		t.Fatal("DESIGN verified SHA was replaced with metadata tip")
	}
	if _, res := dc.ExecuteDesign("demo", DeliveryAuthorization{SourceSHA: d1, ParentBranch: "develop"}); res.Status != StatusFail {
		t.Fatal("DESIGN child updated parent instead of Collector")
	}
	if r.remoteSHA("develop") != base {
		t.Fatal("DESIGN child moved parent")
	}
}

func TestAssemblerAdapterResolvesProgramDeliveryMetadata(t *testing.T) {
	r, p, proofs := setupControlledProgram(t)
	branches := map[string]string{}
	var shas []string
	for _, proof := range proofs {
		sha := proof.Delivery.DeliverySHA
		shas = append(shas, sha)
		branches[sha] = proof.Delivery.Branch
	}
	adapter := &MergerMergeOps{Merger: r.merger(), ParentBranch: p.ParentBranch, ProgramID: p.ProgramID, BranchOf: branches, TestCommand: []string{"go", "test", "./..."}}
	sha, err := adapter.MergeSources(p.BaseCommit, shas)
	if err != nil {
		t.Fatal(err)
	}
	for _, proof := range proofs {
		if ok, _ := (&ExecGit{Root: r.host}).IsAncestor(proof.SourceTip, sha); !ok {
			t.Fatal("adapter omitted proven metadata tip")
		}
	}
	if r.remoteSHA("develop") != p.BaseCommit {
		t.Fatal("phase adapter updated parent")
	}
}
