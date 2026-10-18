package workflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

// ProgramDeliveryProof separates the tested snapshot from the published metadata
// tip. The registry continues to bind the tested SHA, never an untested branch.
type ProgramDeliveryProof struct {
	Delivery  RegisteredDelivery `json:"delivery"`
	SourceTip string             `json:"source_tip"`
	State     State              `json:"-"`
}

type pinnedProgramGit struct {
	Git
	head string
}

func (g pinnedProgramGit) RevParse(ref string) (string, error) {
	if ref == "HEAD" {
		return g.head, nil
	}
	return g.Git.RevParse(ref)
}

func requireProgramResult(r Result) error {
	if r.Status == StatusPass {
		return nil
	}
	return fmt.Errorf("program evidence rejected: %s %v %s", r.Status, r.Issues, r.Error)
}

// ResolveProgramDelivery reads BOTH snapshots from Git, not the coordinator's
// possibly stale task directory. Only Delivery may differ in state.yaml after
// verification; approvals, review, handoff, resources and baseline stay bound.
func ResolveProgramDelivery(root string, p ProgramState, taskID string) (ProgramDeliveryProof, error) {
	var proof ProgramDeliveryProof
	if !ValidScopeID(taskID) {
		return proof, fmt.Errorf("invalid task ID")
	}
	plan, ok := findPlanTask(p, taskID)
	if !ok {
		return proof, fmt.Errorf("task outside Owner plan")
	}
	g := &ExecGit{Root: root}
	tip, err := g.RevParse("origin/" + plan.Branch)
	if err != nil {
		return proof, err
	}
	path := ".agent/tasks/" + taskID + "/state.yaml"
	raw, err := g.ShowFile(tip, path)
	if err != nil {
		return proof, err
	}
	st, err := parseState([]byte(raw))
	if err != nil {
		return proof, err
	}
	d := RegisteredDelivery{TaskID: taskID, Branch: plan.Branch, DeliverySHA: st.Delivery.FeatureHead, ReviewStatus: st.Review.Status, ReviewTarget: st.Review.Target, OwnerStatus: st.Owner.Status, OwnerTarget: st.Owner.ReviewTarget}
	if st.TaskID != taskID || st.SchemaVersion != SchemaV4 || st.TaskType != plan.TaskType || st.Blocked.Active {
		return proof, fmt.Errorf("task identity/type/blocked mismatch")
	}
	c := Collector{Git: g, LoadTaskState: func(string) (State, error) { return st, nil }}
	if err = requireProgramResult(c.Validate(p, d)); err != nil {
		return proof, err
	}
	if st.Delivery.ReviewTarget != st.Review.Target || st.Git.ParentBranch != p.ParentBranch || st.Delivery.DevelopBase != p.BaseCommit {
		return proof, fmt.Errorf("delivery review/parent/base binding mismatch")
	}
	cfg, err := LoadConfig(root)
	if err != nil {
		return proof, err
	}
	if cfg.Git.IntegrationBranch != p.ParentBranch {
		return proof, fmt.Errorf("configured parent mismatch")
	}
	parent, err := g.RevParse("origin/" + p.ParentBranch)
	if err != nil {
		return proof, err
	}
	v := &Validator{Root: root, Git: pinnedProgramGit{g, tip}, Config: cfg}
	if err = requireProgramResult(v.deliveryFreshness(taskID, st, tip, parent)); err != nil {
		return proof, err
	}
	for _, ancestor := range []string{st.Review.Target, st.Git.BaseCommit} {
		ok, err := g.IsAncestor(ancestor, st.Delivery.FeatureHead)
		if err != nil {
			return proof, err
		}
		if !ok {
			return proof, fmt.Errorf("review/baseline is not an ancestor of tested snapshot")
		}
	}
	if err = requireProgramResult(v.ownerAcceptanceValid(taskID, st, tip)); err != nil {
		return proof, err
	}
	if err = requireProgramResult(v.resourcesAuthorized(st)); err != nil {
		return proof, err
	}
	raw, err = g.ShowFile(d.DeliverySHA, path)
	if err != nil {
		return proof, err
	}
	tested, err := parseState([]byte(raw))
	if err != nil {
		return proof, err
	}
	tested.Delivery = st.Delivery
	if !reflect.DeepEqual(tested, st) {
		return proof, fmt.Errorf("non-delivery state changed after verification")
	}
	if st.TaskType == TaskTypeDesign {
		if st.Design == nil || st.Design.Status != DesignApproved || st.Design.ApprovedVersion <= 0 || st.Design.FinalVersion <= 0 || st.Design.FinalTarget != st.Review.Target || designReviewTarget(st) != st.Review.Target {
			return proof, fmt.Errorf("DESIGN final approval binding missing")
		}
		for target, path := range map[string]string{st.Design.ApprovedTarget: architecturePath(taskID), st.Design.FinalTarget: designSpecPath(taskID)} {
			ok, e := g.IsAncestor(target, d.DeliverySHA)
			if e != nil {
				return proof, e
			}
			if !ok {
				return proof, fmt.Errorf("DESIGN approved target outside tested history")
			}
			approved, e := g.ShowFile(target, path)
			if e != nil {
				return proof, e
			}
			current, e := g.ShowFile(tip, path)
			if e != nil {
				return proof, e
			}
			if approved != current {
				return proof, fmt.Errorf("DESIGN approval drift")
			}
		}
	}
	if st.TaskType == TaskTypeComplex && st.Contract.Status != ContractApproved {
		return proof, fmt.Errorf("COMPLEX requires approved Contract")
	}
	if st.Contract.Status == ContractApproved {
		ok, err := g.IsAncestor(st.Contract.Target, d.DeliverySHA)
		if err != nil {
			return proof, err
		}
		if !ok {
			return proof, fmt.Errorf("Contract approval outside tested history")
		}
		approved, err := g.ShowFile(st.Contract.Target, contractPath(taskID))
		if err != nil {
			return proof, err
		}
		current, err := g.ShowFile(tip, contractPath(taskID))
		if err != nil {
			return proof, err
		}
		if approved != current {
			return proof, fmt.Errorf("Contract differs from approved target")
		}
	} else if st.Contract.Status != ContractNotRequired {
		return proof, fmt.Errorf("Contract is not approved/not-required")
	}
	if err = requireProgramResult(v.contractRevisionValid(taskID, st)); err != nil {
		return proof, err
	}
	proof = ProgramDeliveryProof{Delivery: d, SourceTip: tip, State: st}
	return proof, nil
}

// CollectProgram uses the existing Registry CAS. It writes only registry.yaml;
// it never merges or changes a Task or Owner decision. Caller commits metadata.
func CollectProgram(root, programID, taskID string, expectedRevision int) (ProgramDeliveryProof, error) {
	var proof ProgramDeliveryProof
	if !ValidScopeID(programID) || !ValidScopeID(taskID) || expectedRevision < 0 {
		return proof, fmt.Errorf("invalid ID/revision")
	}
	ops := &ExecGitOps{Root: root}
	if err := ops.Fetch(); err != nil {
		return proof, err
	}
	p, err := LoadProgramState(root, programID)
	if err != nil {
		return proof, err
	}
	cfg, err := LoadConfig(root)
	if err != nil {
		return proof, err
	}
	g := &ExecGit{Root: root}
	v := &Validator{Root: root, Git: g, Config: cfg}
	if err = requireProgramResult(v.handoffReady()); err != nil {
		return proof, err
	}
	branch, err := g.CurrentBranch()
	if err != nil {
		return proof, err
	}
	if branch == p.ParentBranch {
		return proof, fmt.Errorf("collect on a dedicated coordinator branch")
	}
	for _, rel := range []string{ProgramStatePath(programID), ProgramRegistryPath(programID), ProgramRegistryPath(programID) + ".lock"} {
		current := root
		for _, part := range strings.Split(rel, string(filepath.Separator)) {
			current = filepath.Join(current, part)
			info, e := os.Lstat(current)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return proof, e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return proof, fmt.Errorf("unsafe program symlink: %s", rel)
			}
		}
	}

	proof, err = ResolveProgramDelivery(root, p, taskID)
	if err != nil {
		return proof, err
	}
	proof.Delivery.RegisteredBy = RoleCollector
	proof.Delivery.RegisteredAt = time.Now().UTC().Format(time.RFC3339)
	// Re-fetch immediately before write; do not silently accept a moved source.
	if err = ops.Fetch(); err != nil {
		return proof, err
	}
	current, err := ResolveProgramDelivery(root, p, taskID)
	if err != nil {
		return proof, err
	}
	if current.SourceTip != proof.SourceTip {
		return proof, fmt.Errorf("source drift before registration")
	}
	store := &RegistryStore{Root: root}
	return proof, store.RegisterExpected(programID, expectedRevision, proof.Delivery)
}

type ProgramAssembly struct {
	ProgramID        string                 `json:"program_id"`
	RegistryRevision int                    `json:"registry_revision"`
	Base             string                 `json:"base"`
	Coordinator      MergeSource            `json:"coordinator"`
	Sources          []ProgramDeliveryProof `json:"sources"`
	Plan             IntegrationPlan        `json:"plan"`
	C0               string                 `json:"c0"`
	SourceBranch     string                 `json:"source_branch"`
	TestCommand      []string               `json:"test_command"`
	Report           MergeReport            `json:"report"`
}

// AssembleProgram integrates ALL topological batches and the committed registry
// in one final isolation, using the existing Merger. It pins C0 in a NEW local
// branch only. No parent push, no Owner fields, no fabricated State PASS, and no
// circular C0 self-reference. Assembler persists the receipt after Cleaner C1.
func AssembleProgram(root, programID, sourceBranch string, test []string) (ProgramAssembly, error) {
	var out ProgramAssembly
	if !ValidScopeID(programID) || sourceBranch == "" || len(test) == 0 {
		return out, fmt.Errorf("program ID, new source branch and test command required")
	}
	if strings.HasPrefix(sourceBranch, "-") {
		return out, fmt.Errorf("invalid source branch")
	}
	if _, err := gitRun(root, "check-ref-format", "--branch", sourceBranch); err != nil {
		return out, err
	}
	g := &ExecGit{Root: root}
	ops := &ExecGitOps{Root: root}
	if err := ops.Fetch(); err != nil {
		return out, err
	}
	p, err := LoadProgramState(root, programID)
	if err != nil {
		return out, err
	}
	if sourceBranch == p.ParentBranch {
		return out, fmt.Errorf("source cannot be parent")
	}
	if _, err := g.RevParse("refs/heads/" + sourceBranch); err == nil {
		return out, fmt.Errorf("source branch already exists")
	}
	remote, err := gitRun(root, "ls-remote", "--heads", "origin", "refs/heads/"+sourceBranch)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(remote) != "" {
		return out, fmt.Errorf("remote source already exists")
	}
	cfg, err := LoadConfig(root)
	if err != nil {
		return out, err
	}
	v := &Validator{Root: root, Git: g, Config: cfg}
	if err = requireProgramResult(v.handoffReady()); err != nil {
		return out, err
	}
	coordinatorBranch, err := g.CurrentBranch()
	if err != nil {
		return out, err
	}
	if coordinatorBranch == p.ParentBranch {
		return out, fmt.Errorf("assemble from a dedicated coordinator branch")
	}
	head, err := g.RevParse("HEAD")
	if err != nil {
		return out, err
	}
	store := &RegistryStore{Root: root}
	reg, err := store.Load(programID)
	if err != nil {
		return out, err
	}
	proofs := map[string]ProgramDeliveryProof{}
	states := map[string]State{}
	for _, d := range reg.Deliveries {
		pr, err := ResolveProgramDelivery(root, p, d.TaskID)
		if err != nil {
			return out, err
		}
		if pr.Delivery.DeliverySHA != d.DeliverySHA || pr.Delivery.ReviewTarget != d.ReviewTarget || pr.Delivery.OwnerTarget != d.OwnerTarget || pr.Delivery.Branch != d.Branch {
			return out, fmt.Errorf("registered evidence changed: %s", d.TaskID)
		}
		proofs[d.TaskID] = pr
		states[d.TaskID] = pr.State
	}
	load := func(id string) (State, error) {
		s, ok := states[id]
		if !ok {
			return s, fmt.Errorf("missing registered task")
		}
		return s, nil
	}
	pv := &ProgramValidator{Root: root, Git: g, Store: store, LoadTaskState: load}
	if err = requireProgramResult(pv.GateAssemblerStart(programID)); err != nil {
		return out, err
	}
	asm := &Assembler{Store: store, Git: g, LoadTaskState: load}
	plan, res := asm.Plan(p)
	if err = requireProgramResult(res); err != nil {
		return out, err
	}
	sources := []MergeSource{{Branch: coordinatorBranch, SHA: head}}
	out = ProgramAssembly{ProgramID: programID, RegistryRevision: reg.Revision, Base: p.BaseCommit, Coordinator: sources[0], Plan: plan, SourceBranch: sourceBranch, TestCommand: test}
	for _, batch := range plan.Batches {
		for _, id := range batch.Tasks {
			pr := proofs[id]
			out.Sources = append(out.Sources, pr)
			sources = append(sources, MergeSource{Branch: pr.Delivery.Branch, SHA: pr.SourceTip})
		}
	}
	merger := &Merger{Root: root, Git: ops}
	report, err := merger.Integrate(MergeRequest{ParentBranch: p.ParentBranch, ParentBaseSHA: p.BaseCommit, Sources: sources, TestCommand: test, OwnerAuthorized: false})
	out.Report = report
	if err != nil {
		return out, err
	}
	if report.Level != MergeL1 || !report.Merged || !report.TestPassed || report.ParentUpdated || !validRequiredSHA(report.MergeCommit) {
		return out, fmt.Errorf("assembly rejected (%s): %s", report.Level, report.Reason)
	}
	// A coordinator change during tests must not be hidden by a clean remote ref.
	now, err := g.RevParse("HEAD")
	if err != nil {
		return out, err
	}
	dirty, err := g.StatusPorcelain()
	if err != nil {
		return out, err
	}
	if now != head || len(dirty) != 0 {
		return out, fmt.Errorf("coordinator changed during assembly")
	}
	// CAS creation, no existing branch can be replaced. This ref preserves C0
	// after Merger removes its private scratch ref/worktree.
	if _, err = gitRun(root, "update-ref", "refs/heads/"+sourceBranch, report.MergeCommit, strings.Repeat("0", len(report.MergeCommit))); err != nil {
		return out, err
	}
	out.C0 = report.MergeCommit
	return out, nil
}

// ProgramTaskStateLoader preserves historical read-only diagnostics on an
// already materialized task. A coordinator checkout need not contain every
// child's runtime directory: missing children are loaded from proven Git
// delivery snapshots. Controlled writes ALWAYS use ResolveProgramDelivery,
// regardless of the presence of a local artifact.
func ProgramTaskStateLoader(root, programID string) TaskStateLoader {
	return func(id string) (State, error) {
		if !ValidScopeID(programID) || !ValidScopeID(id) {
			return State{}, fmt.Errorf("invalid program/task ID")
		}
		st, err := LoadTaskStateFile(root, id)
		if err == nil {
			return st, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return State{}, err
		}
		p, err := LoadProgramState(root, programID)
		if err != nil {
			return State{}, err
		}
		proof, err := ResolveProgramDelivery(root, p, id)
		if err != nil {
			return State{}, err
		}
		reg, err := (&RegistryStore{Root: root}).Load(programID)
		if err != nil {
			return State{}, err
		}
		for _, d := range reg.Deliveries {
			if d.TaskID == id && d.DeliverySHA == proof.Delivery.DeliverySHA && d.ReviewTarget == proof.Delivery.ReviewTarget && d.OwnerTarget == proof.Delivery.OwnerTarget && d.Branch == proof.Delivery.Branch {
				return proof.State, nil
			}
		}
		return State{}, fmt.Errorf("task proof does not match registered delivery")
	}
}
