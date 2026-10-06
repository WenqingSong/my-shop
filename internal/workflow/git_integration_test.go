package workflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// testRepo 是用于集成测试的临时 git 仓库。
type testRepo struct {
	dir string
	t   *testing.T
}

const defaultWorkflowConfig = `schema_version: 1
git:
  integration_branch: develop
resources:
  migration_version:
    registry: .agent/registry/migrations.md
  error_code_domain:
    registry: .agent/registry/error-codes.md
`

func newTestRepo(t *testing.T) *testRepo {
	return newTestRepoWithConfig(t, defaultWorkflowConfig)
}

// newTestRepoWithConfig 与 newTestRepo 相同，但允许注入自定义机器配置内容。
func newTestRepoWithConfig(t *testing.T, configYAML string) *testRepo {
	t.Helper()
	dir := t.TempDir()
	r := &testRepo{dir: dir, t: t}
	r.git("init", "-q")
	r.git("checkout", "-q", "-b", "develop")
	r.git("config", "user.name", "test")
	r.git("config", "user.email", "test@example.com")
	r.git("config", "commit.gpgsign", "false")
	if configYAML != "" {
		r.write(".agent/workflow.yaml", configYAML)
	}
	return r
}

func (r *testRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v 失败: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *testRepo) write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatalf("创建目录失败: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatalf("写文件失败: %v", err)
	}
}

func (r *testRepo) commit(msg string) string {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

func (r *testRepo) checkout(branch string) {
	r.t.Helper()
	r.git("checkout", "-q", "-b", branch)
}

// syncOrigin 把当前 HEAD 标记为 shared 远端引用 refs/remotes/origin/<branch>。
func (r *testRepo) syncOrigin(branch string) {
	r.t.Helper()
	head := r.git("rev-parse", "HEAD")
	r.git("update-ref", "refs/remotes/origin/"+branch, head)
}

func (r *testRepo) validator() *Validator {
	cfg, err := LoadConfig(r.dir)
	if err != nil {
		r.t.Fatalf("load config: %v", err)
	}
	return &Validator{
		Root:   r.dir,
		Git:    &ExecGit{Root: r.dir},
		Config: cfg,
	}
}

func (r *testRepo) writeState(s State) {
	r.t.Helper()
	b, err := yaml.Marshal(s)
	if err != nil {
		r.t.Fatalf("marshal state: %v", err)
	}
	r.write(".agent/tasks/demo/state.yaml", string(b))
}

func baseState() State {
	return State{
		SchemaVersion: SchemaV2,
		TaskID:        "demo",
		Contract:      Contract{Status: ContractNotRequired, Target: ""},
		Resources:     Resources{},
		Review:        Review{Status: ReviewNotRequested, Target: ""},
		Owner:         Owner{Status: OwnerPending, ReviewTarget: ""},
		Delivery:      Delivery{Status: DeliveryNotRun, ReviewTarget: "", FeatureHead: "", DevelopBase: ""},
		Blocked:       Blocked{Active: false, By: "", Reason: ""},
	}
}

// setupCoderStartApproved 构建 coder-start 的 APPROVED 前置：A1 contract + A2 metadata，feature 已同步。
func setupCoderStartApproved(t *testing.T) (*testRepo, map[string]string) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	r.syncOrigin("develop")

	r.checkout("feature/agent-workflow-v2")
	r.write(".agent/tasks/demo/contract.md", "# contract v1\n")
	shas := map[string]string{"A1": r.commit("contract")}

	s := baseState()
	s.Contract = Contract{Status: ContractApproved, Target: shas["A1"]}
	r.writeState(s)
	shas["A2"] = r.commit("contract metadata")
	r.syncOrigin("feature/agent-workflow-v2")
	return r, shas
}

// setupCleanerStart 构建 cleaner-start 前置：C1 implementation + C2 PENDING metadata，feature 已同步。
func setupCleanerStart(t *testing.T) (*testRepo, map[string]string) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	r.syncOrigin("develop")

	r.checkout("feature/agent-workflow-v2")
	r.write("internal/foo.go", "package foo\n")
	shas := map[string]string{"C1": r.commit("implementation")}

	s := baseState()
	s.Review = Review{Status: ReviewPending, Target: shas["C1"]}
	r.writeState(s)
	shas["C2"] = r.commit("review metadata")
	r.syncOrigin("feature/agent-workflow-v2")
	return r, shas
}

// setupMergeReady 构建 merge-ready PASS 的完整 feature 历史（contract NOT_REQUIRED、无资源）。
func setupMergeReady(t *testing.T) (*testRepo, map[string]string) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	r.syncOrigin("develop")
	shas := map[string]string{"D12": r.git("rev-parse", "HEAD")}

	r.checkout("feature/agent-workflow-v2")

	// C1 implementation
	r.write("internal/foo.go", "package foo\n")
	shas["C1"] = r.commit("implementation")

	// C2 metadata: PENDING target C1
	s := baseState()
	s.Review = Review{Status: ReviewPending, Target: shas["C1"]}
	r.writeState(s)
	shas["C2"] = r.commit("review request metadata")

	// C3 clean + findings + core-logic
	r.write(".agent/tasks/demo/findings.md", "# findings\n")
	r.write(".agent/tasks/demo/core-logic.md", "# core logic\n")
	s.Review = Review{Status: ReviewClean, Target: shas["C1"]}
	r.writeState(s)
	shas["C3"] = r.commit("clean review")

	// C4 owner accepted + owner-decision
	r.write(".agent/tasks/demo/owner-decision.md", "# decision\n")
	s.Owner = Owner{Status: OwnerAccepted, ReviewTarget: shas["C1"]}
	r.writeState(s)
	shas["C4"] = r.commit("owner acceptance")
	shas["F8"] = shas["C4"]

	// F9 delivery pass + delivery.md
	r.write(".agent/tasks/demo/delivery.md", "# delivery\n")
	s.Delivery = Delivery{Status: DeliveryPass, ReviewTarget: shas["C1"], FeatureHead: shas["F8"], DevelopBase: shas["D12"]}
	r.writeState(s)
	shas["F9"] = r.commit("delivery pass")
	r.syncOrigin("feature/agent-workflow-v2")

	return r, shas
}

func expectStatus(t *testing.T, res Result, want StatusKind) {
	t.Helper()
	if res.Status != want {
		t.Fatalf("期望 status=%s，实际 status=%s issues=%v error=%q", want, res.Status, res.Issues, res.Error)
	}
}

func expectCheck(t *testing.T, res Result, checkSubstr string) {
	t.Helper()
	if res.Status != StatusFail || !issueCheck(t, res.Issues, checkSubstr) {
		t.Fatalf("期望 FAIL 且含 %q，实际 status=%s issues=%v error=%q", checkSubstr, res.Status, res.Issues, res.Error)
	}
}

// --- coder-start ---

func TestGateCoderStartContractApproved(t *testing.T) {
	r, _ := setupCoderStartApproved(t)
	expectStatus(t, r.validator().GateCoderStart(".agent/tasks/demo"), StatusPass)
}

func TestGateCoderStartContractChanged(t *testing.T) {
	r, _ := setupCoderStartApproved(t)
	r.write(".agent/tasks/demo/contract.md", "# contract v2 changed\n")
	r.commit("contract changed")
	r.syncOrigin("feature/agent-workflow-v2")
	expectCheck(t, r.validator().GateCoderStart(".agent/tasks/demo"), "Contract")
}

func TestGateCoderStartDesignNotBound(t *testing.T) {
	r, _ := setupCoderStartApproved(t)
	r.write("docs/design/foo.md", "# design\n")
	r.commit("design added")
	r.syncOrigin("feature/agent-workflow-v2")
	expectStatus(t, r.validator().GateCoderStart(".agent/tasks/demo"), StatusPass)
}

func TestGateCoderStartBlocked(t *testing.T) {
	r, _ := setupCoderStartApproved(t)
	s := baseState()
	s.Contract = Contract{Status: ContractApproved, Target: r.git("rev-parse", "HEAD~0")}
	s.Blocked = Blocked{Active: true, By: "Coder", Reason: "blocked"}
	r.writeState(s)
	r.commit("blocked state")
	r.syncOrigin("feature/agent-workflow-v2")
	expectCheck(t, r.validator().GateCoderStart(".agent/tasks/demo"), "Coder Start")
}

// --- cleaner-start ---

func TestGateCleanerStartPendingPushed(t *testing.T) {
	r, _ := setupCleanerStart(t)
	expectStatus(t, r.validator().GateCleanerStart(".agent/tasks/demo"), StatusPass)
}

func TestGateCleanerStartDirty(t *testing.T) {
	r, _ := setupCleanerStart(t)
	r.write("internal/foo.go", "package foo\n// dirty\n")
	expectCheck(t, r.validator().GateCleanerStart(".agent/tasks/demo"), "Handoff Ready")
}

func TestGateCleanerStartUnpushed(t *testing.T) {
	r, _ := setupCleanerStart(t)
	r.write(".agent/tasks/demo/findings.md", "# pre-findings\n")
	r.commit("local commit not pushed")
	// 不 sync，local HEAD != remote feature HEAD
	expectCheck(t, r.validator().GateCleanerStart(".agent/tasks/demo"), "Handoff Ready")
}

func TestGateCleanerStartSubstantiveTail(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	r.syncOrigin("develop")
	r.checkout("feature/agent-workflow-v2")

	r.write("internal/foo.go", "package foo\n")
	c1 := r.commit("implementation v1")
	r.write("internal/foo.go", "package foo\n// v2\n") // 实质变化
	s := baseState()
	s.Review = Review{Status: ReviewPending, Target: c1}
	r.writeState(s)
	r.commit("metadata + substantive")
	r.syncOrigin("feature/agent-workflow-v2")

	expectCheck(t, r.validator().GateCleanerStart(".agent/tasks/demo"), "Review Tail")
}

// --- owner-gate-start / CLEAN validity ---

func TestGateOwnerGateStartCleanValid(t *testing.T) {
	r, shas := setupCleanerStart(t)
	r.write(".agent/tasks/demo/findings.md", "# findings\n")
	r.write(".agent/tasks/demo/core-logic.md", "# core logic\n")
	s := baseState()
	s.Review = Review{Status: ReviewClean, Target: shas["C1"]}
	r.writeState(s)
	r.commit("clean review")
	r.syncOrigin("feature/agent-workflow-v2")

	expectStatus(t, r.validator().GateOwnerGateStart(".agent/tasks/demo"), StatusPass)
}

func TestCleanValiditySubstantiveComment(t *testing.T) {
	r, shas := setupCleanerStart(t)
	r.write(".agent/tasks/demo/findings.md", "# findings\n")
	s := baseState()
	s.Review = Review{Status: ReviewClean, Target: shas["C1"]}
	r.writeState(s)
	r.commit("clean review")

	// 只改 .go 注释，也属 substantive。
	r.write("internal/foo.go", "package foo\n// comment only\n")
	r.commit("comment change")
	r.syncOrigin("feature/agent-workflow-v2")

	expectCheck(t, r.validator().GateOwnerGateStart(".agent/tasks/demo"), "Review Tail")
}

// --- owner acceptance invariant ---

func TestOwnerAcceptanceInvalidated(t *testing.T) {
	r, _ := setupMergeReady(t)

	// 新实现 C6：review CLEAN C6，但 owner.review_target 仍是 C1。
	r.write("internal/foo.go", "package foo\n// reworked\n")
	c6 := r.commit("rework")
	s := baseState()
	s.Review = Review{Status: ReviewClean, Target: c6}
	r.writeState(s)
	r.commit("re-clean")
	r.syncOrigin("feature/agent-workflow-v2")

	expectCheck(t, r.validator().GateMergeReady(".agent/tasks/demo"), "Owner Acceptance")
}

// --- merge-ready ---

func TestGateMergeReadyHappyPath(t *testing.T) {
	r, _ := setupMergeReady(t)
	expectStatus(t, r.validator().GateMergeReady(".agent/tasks/demo"), StatusPass)
}

func TestGateMergeReadyDeliverySubstantive(t *testing.T) {
	r, _ := setupMergeReady(t)
	// core-logic.md 是 review-neutral 但非 delivery-neutral：验证 delivery tail 更窄白名单。
	r.write(".agent/tasks/demo/core-logic.md", "# core logic updated after delivery\n")
	r.commit("post-delivery review artifact")
	r.syncOrigin("feature/agent-workflow-v2")
	expectCheck(t, r.validator().GateMergeReady(".agent/tasks/demo"), "Delivery Freshness")
}

func TestGateMergeReadyDevelopMoved(t *testing.T) {
	r, _ := setupMergeReady(t)
	r.git("checkout", "-q", "develop")
	r.write("README.md", "# base\n// advanced\n")
	r.commit("develop advance")
	r.syncOrigin("develop")
	r.git("checkout", "-q", "feature/agent-workflow-v2")
	expectCheck(t, r.validator().GateMergeReady(".agent/tasks/demo"), "Delivery Freshness")
}

// --- handoffReady ---

func TestHandoffReadyDetached(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	r.git("checkout", "-q", "--detach")
	res := r.validator().handoffReady()
	if res.Status != StatusError {
		t.Fatalf("detached 期望 ERROR，实际 status=%s error=%q", res.Status, res.Error)
	}
}

// --- non-V2 task ---

func TestGateNonV2Error(t *testing.T) {
	r := newTestRepo(t)
	r.write(".agent/tasks/demo/state.yaml", "task_id: demo\nphase: NEW\n")
	r.commit("v1 state")
	res := r.validator().GateCoderStart(".agent/tasks/demo")
	if res.Status != StatusError {
		t.Fatalf("非 V2 task 期望 ERROR，实际 status=%s error=%q", res.Status, res.Error)
	}
}

// --- resource authority ---

const migrationsBase = `# migrations
| version | title | 拥有方（任务） | 状态 | 备注 |
| --- | --- | --- | --- | --- |
| 20261001000001 | baseline | db-migration | ACTIVE | 基线 |
`

const errorDomainsBase = `# error-codes
| 域区间 | 拥有方（任务/模块） | 状态 | 备注 |
| --- | --- | --- | --- |
| 1000-1999 | 通用 | ACTIVE | 基线 |
`

func TestGateCoderStartResourceAuthorized(t *testing.T) {
	r := newTestRepo(t)
	r.write(".agent/registry/migrations.md", migrationsBase)
	r.write(".agent/registry/error-codes.md", errorDomainsBase)
	r.commit("registry baseline")
	r.syncOrigin("develop")

	r.write(".agent/registry/migrations.md", migrationsBase+"| 20261001000009 | foo | demo | RESERVED | |\n")
	r.commit("develop reserved")
	r.syncOrigin("develop")

	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	s.Resources = Resources{Migrations: []string{"20261001000009"}}
	r.writeState(s)
	r.commit("feature state")
	r.syncOrigin("feature/agent-workflow-v2")

	expectStatus(t, r.validator().GateCoderStart(".agent/tasks/demo"), StatusPass)
}

func TestGateCoderStartResourceFeatureOnlyReserved(t *testing.T) {
	r := newTestRepo(t)
	r.write(".agent/registry/migrations.md", migrationsBase)
	r.write(".agent/registry/error-codes.md", errorDomainsBase)
	r.commit("registry baseline")
	r.syncOrigin("develop")

	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	s.Resources = Resources{Migrations: []string{"20261001000009"}}
	r.writeState(s)
	r.commit("feature state")
	r.syncOrigin("feature/agent-workflow-v2")

	expectCheck(t, r.validator().GateCoderStart(".agent/tasks/demo"), "Resource Authority")
}

// --- machine config：非 develop 集成分支 ---

const mainWorkflowConfig = `schema_version: 1
git:
  integration_branch: main
resources:
  migration_version:
    registry: .agent/registry/migrations.md
  error_code_domain:
    registry: .agent/registry/error-codes.md
`

func TestValidatorIntegrationBranchMain(t *testing.T) {
	r := newTestRepoWithConfig(t, mainWorkflowConfig)
	r.git("branch", "-m", "main") // 集成分支改为 main
	r.write(".agent/registry/migrations.md", migrationsBase)
	r.write(".agent/registry/error-codes.md", errorDomainsBase)
	r.commit("registry baseline")
	r.syncOrigin("main")

	// 在 main 上追加 RESERVED
	r.write(".agent/registry/migrations.md", migrationsBase+"| 20261001000009 | foo | demo | RESERVED | |\n")
	r.commit("reserve on main")
	r.syncOrigin("main")

	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	s.Resources = Resources{Migrations: []string{"20261001000009"}}
	r.writeState(s)
	r.commit("feature state")
	r.syncOrigin("feature/agent-workflow-v2")

	// Validator 应读取 origin/main（而非 origin/develop）上的 registry。
	expectStatus(t, r.validator().GateCoderStart(".agent/tasks/demo"), StatusPass)
}

// --- machine config：自定义 registry 路径 ---

const customRegistryConfig = `schema_version: 1
git:
  integration_branch: develop
resources:
  migration_version:
    registry: .agent/registry/custom-migrations.md
  error_code_domain:
    registry: .agent/registry/custom-error-codes.md
`

func TestValidatorCustomRegistryPaths(t *testing.T) {
	r := newTestRepoWithConfig(t, customRegistryConfig)
	r.write(".agent/registry/custom-migrations.md", migrationsBase)
	r.write(".agent/registry/custom-error-codes.md", errorDomainsBase)
	r.commit("registry baseline")
	r.syncOrigin("develop")

	r.write(".agent/registry/custom-migrations.md", migrationsBase+"| 20261001000009 | foo | demo | RESERVED | |\n")
	r.write(".agent/registry/custom-error-codes.md", errorDomainsBase+"| 10000-10999 | demo | RESERVED | |\n")
	r.commit("reserve")
	r.syncOrigin("develop")

	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	s.Resources = Resources{Migrations: []string{"20261001000009"}, ErrorCodeDomains: []string{"10000-10999"}}
	r.writeState(s)
	r.commit("feature state")
	r.syncOrigin("feature/agent-workflow-v2")

	expectStatus(t, r.validator().GateCoderStart(".agent/tasks/demo"), StatusPass)
}

// --- machine config 缺 registry 声明 → ERROR（exit 2），而非「resource not reserved」---

const noMigrationRegistryConfig = `schema_version: 1
git:
  integration_branch: develop
resources:
  migration_version:
    registry: ""
  error_code_domain:
    registry: .agent/registry/error-codes.md
`

func TestResourceNeedsMigrationButConfigMissingMigrationRegistry(t *testing.T) {
	r := newTestRepoWithConfig(t, noMigrationRegistryConfig)
	r.write(".agent/registry/error-codes.md", errorDomainsBase)
	r.commit("baseline")
	r.syncOrigin("develop")

	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	s.Resources = Resources{Migrations: []string{"20261001000009"}}
	r.writeState(s)
	r.commit("feature state")
	r.syncOrigin("feature/agent-workflow-v2")

	res := r.validator().GateCoderStart(".agent/tasks/demo")
	if res.Status != StatusError {
		t.Fatalf("期望 ERROR，实际 status=%s error=%q", res.Status, res.Error)
	}
}

const noErrorCodeRegistryConfig = `schema_version: 1
git:
  integration_branch: develop
resources:
  migration_version:
    registry: .agent/registry/migrations.md
  error_code_domain:
    registry: ""
`

func TestResourceNeedsErrorCodeButConfigMissingErrorCodeRegistry(t *testing.T) {
	r := newTestRepoWithConfig(t, noErrorCodeRegistryConfig)
	r.write(".agent/registry/migrations.md", migrationsBase)
	r.commit("baseline")
	r.syncOrigin("develop")

	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	s.Resources = Resources{ErrorCodeDomains: []string{"10000-10999"}}
	r.writeState(s)
	r.commit("feature state")
	r.syncOrigin("feature/agent-workflow-v2")

	res := r.validator().GateCoderStart(".agent/tasks/demo")
	if res.Status != StatusError {
		t.Fatalf("期望 ERROR，实际 status=%s error=%q", res.Status, res.Error)
	}
}

// --- config 声明的 registry 路径无法读取 → ERROR ---

func TestConfigRegistryPathNotFound(t *testing.T) {
	r := newTestRepoWithConfig(t, customRegistryConfig)
	r.write(".agent/registry/unrelated.md", "# not the configured path\n")
	r.commit("baseline")
	r.syncOrigin("develop")

	r.checkout("feature/agent-workflow-v2")
	s := baseState()
	s.Resources = Resources{Migrations: []string{"20261001000009"}}
	r.writeState(s)
	r.commit("feature state")
	r.syncOrigin("feature/agent-workflow-v2")

	res := r.validator().GateCoderStart(".agent/tasks/demo")
	if res.Status != StatusError {
		t.Fatalf("期望 ERROR，实际 status=%s error=%q", res.Status, res.Error)
	}
}
