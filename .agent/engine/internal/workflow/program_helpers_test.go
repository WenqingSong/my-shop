package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// 本文件提供 Program 相关测试的共享 helper。

func mustParseProgram(t *testing.T, yml string) ProgramState {
	t.Helper()
	ps, err := ParseProgramState([]byte(yml))
	if err != nil {
		t.Fatalf("解析 program-state 应成功，实际 %v", err)
	}
	return ps
}

func mustParseProgramError(t *testing.T, yml, wantSub string) {
	t.Helper()
	_, err := ParseProgramState([]byte(yml))
	if err == nil {
		t.Fatalf("期望错误含 %q，实际 nil", wantSub)
	}
	if wantSub != "" && !strings.Contains(err.Error(), wantSub) {
		t.Fatalf("期望错误含 %q，实际 %v", wantSub, err)
	}
}

// shaLike 用输入字符串派生一个确定性 40 位十六进制串（形如 Commit SHA）。
func shaLike(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:20])
}

// testSHA 返回第 n 个确定性 SHA 串。
func testSHA(n int) string {
	return shaLike(strings.Repeat("x", n+1))
}

// validDelivery 返回一个结构合法的交付记录（review CLEAN + owner ACCEPT）。
func validDelivery(taskID string) RegisteredDelivery {
	return RegisteredDelivery{
		TaskID:       taskID,
		Branch:       "feature/" + taskID,
		DeliverySHA:  shaLike("delivery:" + taskID),
		ReviewStatus: ReviewClean,
		ReviewTarget: shaLike("review:" + taskID),
		OwnerStatus:  OwnerAccepted,
		OwnerTarget:  shaLike("review:" + taskID),
		RegisteredBy: "CollectorAgent",
		RegisteredAt: "2026-10-16T00:00:00Z",
	}
}

// programWithTasks 构造一个最小合法 ProgramState。
func programWithTasks(programID, parentBranch, baseCommit string, tasks []ProgramTask) ProgramState {
	return ProgramState{
		SchemaVersion: ProgramSchemaV1,
		ProgramID:     programID,
		ParentBranch:  parentBranch,
		BaseCommit:    baseCommit,
		Tasks:         tasks,
		FinalIntegration: ProgramFinalIntegration{
			Status:   "PASS",
			SHA:      shaLike("final:" + programID),
			Evidence: "final-integration.md",
		},
		Review: ProgramReview{Status: ReviewClean, Target: shaLike("review:" + programID)},
		Owner:  ProgramOwner{Status: OwnerAccepted, Target: shaLike("review:" + programID)},
	}
}

// expectIssueContains 断言结果为 FAIL 且任一 issue 的完整描述包含 substr。
func expectIssueContains(t *testing.T, res Result, substr string) {
	t.Helper()
	if res.Status != StatusFail {
		t.Fatalf("期望 FAIL，实际 %s error=%q", res.Status, res.Error)
	}
	for _, iss := range res.Issues {
		if strings.Contains(iss.String(), substr) {
			return
		}
	}
	t.Fatalf("期望 issue 含 %q，实际 %v", substr, res.Issues)
}

// collectorTaskState 构造一个「已完成独立验收」的子 Task state（供 Collector 测试注入）。
func collectorTaskState(programID, branch, reviewSHA, featureSHA string) State {
	return State{
		SchemaVersion: SchemaV4,
		TaskID:        "t1",
		TaskType:      TaskTypeNormal,
		Git:           &GitBaseline{ParentBranch: "develop", BaseCommit: testSHA(1), TaskBranch: branch},
		Program:       &ProgramRef{ID: programID, Version: 1},
		Contract:      Contract{Status: ContractNotRequired},
		Resources:     Resources{Reservations: map[string][]string{}},
		Review:        Review{Status: ReviewClean, Target: reviewSHA},
		Owner:         Owner{Status: OwnerAccepted, ReviewTarget: reviewSHA},
		Delivery:      Delivery{Status: DeliveryPass, ReviewTarget: reviewSHA, FeatureHead: featureSHA, DevelopBase: testSHA(2)},
		Blocked:       Blocked{Active: false},
	}
}

// fakeGit 是只读 Git 接口的可注入实现。
type fakeGit struct {
	commits    map[string]bool
	ancestorFn func(a, d string) (bool, error)
	revParseFn func(ref string) (string, error)
}

func newFakeGit() *fakeGit {
	return &fakeGit{commits: map[string]bool{}}
}

func (g *fakeGit) DiffNameOnly(string) ([]string, error)              { return nil, nil }
func (g *fakeGit) DiffNameOnlyRange(string, string) ([]string, error) { return nil, nil }
func (g *fakeGit) UntrackedFiles() ([]string, error)                  { return nil, nil }
func (g *fakeGit) ShowFile(string, string) (string, error)            { return "", nil }
func (g *fakeGit) PathExistsAt(string, string) (bool, error)          { return false, nil }
func (g *fakeGit) RevParse(ref string) (string, error) {
	if g.revParseFn != nil {
		return g.revParseFn(ref)
	}
	return ref, nil
}
func (g *fakeGit) CommitExists(sha string) (bool, error) {
	if g.commits == nil {
		return true, nil
	}
	return g.commits[sha], nil
}
func (g *fakeGit) IsAncestor(a, d string) (bool, error) {
	if g.ancestorFn != nil {
		return g.ancestorFn(a, d)
	}
	return true, nil
}
func (g *fakeGit) StatusPorcelain() ([]string, error) { return nil, nil }
func (g *fakeGit) CurrentBranch() (string, error)     { return "main", nil }
func (g *fakeGit) BranchExists(string) (bool, error)  { return true, nil }
func (g *fakeGit) WorktreeExists(string) (bool, error) { return true, nil }

// taskStateYAML 生成一条最小合法 V4 子 Task state（NORMAL、无资源、已验收），
// 供跨 Task 契约校验加载（delivery.feature_head 与已登记 delivery_sha 一致）。
func taskStateYAML(taskID, programID, branch, deliverySHA string) string {
	return taskStateYAMLRes(taskID, programID, branch, deliverySHA, "")
}

// taskStateYAMLRes 同上，但可注入 resources 块原文（如冲突资源）。resources 需包含完整的
// `resources:` 块（含缩进），用于替换默认的空 reservations。
func taskStateYAMLRes(taskID, programID, branch, deliverySHA, resources string) string {
	yml := `schema_version: 4
task_id: ` + taskID + `
task_type: NORMAL
git:
  parent_branch: develop
  base_commit: ` + deliverySHA + `
  task_branch: ` + branch + `
program:
  id: ` + programID + `
  version: 1
contract:
  status: NOT_REQUIRED
  target: ""
resources:
  reservations: {}
review:
  status: CLEAN
  target: ` + deliverySHA + `
owner:
  status: ACCEPTED
  review_target: ` + deliverySHA + `
delivery:
  status: PASS
  review_target: ` + deliverySHA + `
  feature_head: ` + deliverySHA + `
  develop_base: ` + deliverySHA + `
`
	if resources != "" {
		yml = strings.Replace(yml, "resources:\n  reservations: {}\n", resources, 1)
	}
	return yml
}
