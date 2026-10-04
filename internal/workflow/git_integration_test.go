package workflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// testRepo 是一个用于集成测试的临时 git 仓库。
type testRepo struct {
	dir string
	t   *testing.T
}

func newTestRepo(t *testing.T) *testRepo {
	t.Helper()
	dir := t.TempDir()
	r := &testRepo{dir: dir, t: t}
	r.git("init", "-q")
	r.git("checkout", "-q", "-b", "develop")
	r.git("config", "user.name", "test")
	r.git("config", "user.email", "test@example.com")
	r.git("config", "commit.gpgsign", "false")
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

func (r *testRepo) validator(cutover string) *Validator {
	return &Validator{
		Root:       r.dir,
		Git:        &ExecGit{Root: r.dir},
		DevelopRef: "origin/develop",
		Cutover:    cutover,
	}
}

// syncOriginDevelop 把当前 HEAD 标记为 shared 远端引用 origin/develop。
// 模拟「只改 Registry 的 commit 已落到 shared develop」。
func (r *testRepo) syncOriginDevelop() string {
	r.t.Helper()
	head := r.git("rev-parse", "HEAD")
	r.git("update-ref", "refs/remotes/origin/develop", head)
	return head
}

const cleanStateYAML = `task: demo-task
phase: WAITING_FOR_OWNER_ACCEPTANCE
review:
  status: CLEAN
  target_base: %s
  target_paths:
    - internal/foo.go
    - internal/foo_test.go
    - docs/design/foo.md
    - .agent/tasks/demo-task/contract.md
owner_verification:
  status: ACCEPTED
delivery:
  status: NONE
required_resources:
  error_code_domains: []
  migrations: []
blocked:
  is_blocked: false
  reason: ""
`

// TestCleanStaleProductionCode 覆盖测试要求 6：CLEAN 后生产代码实质变化 → 判 STALE。
func TestCleanStaleProductionCode(t *testing.T) {
	r := newTestRepo(t)
	r.write("internal/foo.go", "package foo\n")
	r.write("docs/design/foo.md", "# foo\n")
	r.write(".agent/tasks/demo-task/contract.md", "# contract\n")
	r.write(".agent/tasks/demo-task/task.md", "# task\n")
	base := r.commit("baseline")

	r.write(".agent/tasks/demo-task/state.yaml", strings.Replace(cleanStateYAML, "%s", base, 1))
	r.commit("clean")

	// CLEAN 后修改生产代码（工作区未提交）。
	r.write("internal/foo.go", "package foo\n// changed\n")

	res, err := r.validator("").ValidateTask(".agent/tasks/demo-task")
	if err != nil {
		t.Fatalf("不应返回运行错误: %v", err)
	}
	if res.Status != StatusFail {
		t.Fatalf("期望 FAIL，实际 %s，issues=%v", res.Status, res.Issues)
	}
	if !issueCheck(t, res.Issues, "Review Validity") {
		t.Fatalf("期望 Review Validity issue，实际 %v", res.Issues)
	}
}

// TestCleanReviewNeutralNoStale 覆盖测试要求 5：Review-neutral Artifact 变化不使 CLEAN 失效。
func TestCleanReviewNeutralNoStale(t *testing.T) {
	r := newTestRepo(t)
	r.write("internal/foo.go", "package foo\n")
	r.write(".agent/tasks/demo-task/task.md", "# task\n")
	base := r.commit("baseline")

	r.write(".agent/tasks/demo-task/state.yaml", strings.Replace(cleanStateYAML, "%s", base, 1))
	r.commit("clean")

	// 修改 Review-neutral 文件。
	r.write(".agent/tasks/demo-task/findings.md", "# findings updated\n")
	r.write(".agent/tasks/demo-task/state.yaml", strings.Replace(cleanStateYAML, "%s", base, 1)+"# comment\n")

	res, err := r.validator("").ValidateTask(".agent/tasks/demo-task")
	if err != nil {
		t.Fatalf("不应返回运行错误: %v", err)
	}
	if res.Status != StatusPass {
		t.Fatalf("期望 PASS，实际 %s，issues=%v", res.Status, res.Issues)
	}
}

// TestCleanStaleBusinessTest 覆盖测试要求 7：CLEAN 后业务测试变化 → 判 STALE。
func TestCleanStaleBusinessTest(t *testing.T) {
	r := newTestRepo(t)
	r.write("internal/foo.go", "package foo\n")
	r.write(".agent/tasks/demo-task/task.md", "# task\n")
	base := r.commit("baseline")
	r.write(".agent/tasks/demo-task/state.yaml", strings.Replace(cleanStateYAML, "%s", base, 1))
	r.commit("clean")

	r.write("internal/foo_test.go", "package foo\n")
	res, _ := r.validator("").ValidateTask(".agent/tasks/demo-task")
	if res.Status != StatusFail || !issueCheck(t, res.Issues, "Review Validity") {
		t.Fatalf("业务测试变化应判 STALE，实际 status=%s issues=%v", res.Status, res.Issues)
	}
}

// TestCleanStaleContractDesign 覆盖测试要求 8：Contract / Design 变化 → 判 STALE。
func TestCleanStaleContractDesign(t *testing.T) {
	r := newTestRepo(t)
	r.write("internal/foo.go", "package foo\n")
	r.write(".agent/tasks/demo-task/task.md", "# task\n")
	base := r.commit("baseline")
	r.write(".agent/tasks/demo-task/state.yaml", strings.Replace(cleanStateYAML, "%s", base, 1))
	r.commit("clean")

	r.write("docs/design/foo.md", "# foo changed\n")
	res, _ := r.validator("").ValidateTask(".agent/tasks/demo-task")
	if res.Status != StatusFail || !issueCheck(t, res.Issues, "Review Validity") {
		t.Fatalf("Design 变化应判 STALE，实际 status=%s issues=%v", res.Status, res.Issues)
	}
}

// TestCutoverLegacySkipAndNewTaskFail 覆盖测试要求 13/14：Legacy 跳过、新任务缺 state.yaml 判 FAIL。
func TestCutoverLegacySkipAndNewTaskFail(t *testing.T) {
	r := newTestRepo(t)
	r.write(".agent/tasks/legacy-task/task.md", "# legacy\n")
	r.write(".agent/tasks/legacy-task/findings.md", "# findings\n")
	cutover := r.commit("cutover")

	// cutover 之后新建任务，无 state.yaml。
	r.write(".agent/tasks/new-task/task.md", "# new\n")
	r.commit("new task")

	v := r.validator(cutover)

	legacyRes, err := v.ValidateTask(".agent/tasks/legacy-task")
	if err != nil {
		t.Fatalf("legacy 不应返回运行错误: %v", err)
	}
	if legacyRes.Status != StatusSkipped {
		t.Fatalf("legacy 期望 SKIPPED，实际 %s", legacyRes.Status)
	}

	newRes, err := v.ValidateTask(".agent/tasks/new-task")
	if err != nil {
		t.Fatalf("new 不应返回运行错误: %v", err)
	}
	if newRes.Status != StatusFail || !issueCheck(t, newRes.Issues, "Cutover Rule") {
		t.Fatalf("新任务缺 state.yaml 期望 FAIL，实际 %s issues=%v", newRes.Status, newRes.Issues)
	}
}

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

// TestResourceAuthorityFeatureBranchReservedInvalid 覆盖测试要求 11：
// Feature branch 自报 RESERVED，但 shared develop Registry 无记录 → FAIL。
func TestResourceAuthorityFeatureBranchReservedInvalid(t *testing.T) {
	r := newTestRepo(t)
	r.write(".agent/registry/migrations.md", migrationsBase)
	r.write(".agent/registry/error-codes.md", errorDomainsBase)
	r.commit("develop registry baseline")
	r.syncOriginDevelop()

	// feature 分支：私留 RESERVED 并声明资源。
	r.git("checkout", "-q", "-b", "feature")
	r.write(".agent/registry/migrations.md", migrationsBase+"| 20261001000009 | foo | demo-task | RESERVED | feature 私留 |\n")
	r.write(".agent/tasks/demo-task/state.yaml", `task: demo-task
phase: IMPLEMENTING
review:
  status: NONE
owner_verification:
  status: PENDING
delivery:
  status: NONE
required_resources:
  migrations:
    - "20261001000009"
blocked:
  is_blocked: false
  reason: ""
`)
	r.commit("feature reserved")

	res, err := r.validator("").ValidateTask(".agent/tasks/demo-task")
	if err != nil {
		t.Fatalf("不应返回运行错误: %v", err)
	}
	if res.Status != StatusFail || !issueCheck(t, res.Issues, "Resource Authority") {
		t.Fatalf("feature 私留 RESERVED 应 FAIL，实际 %s issues=%v", res.Status, res.Issues)
	}
}

// TestResourceAuthorityDevelopReservedValid 覆盖测试要求 12：
// shared develop Registry 有合法 Reservation → PASS。
func TestResourceAuthorityDevelopReservedValid(t *testing.T) {
	r := newTestRepo(t)
	r.write(".agent/registry/migrations.md", migrationsBase)
	r.write(".agent/registry/error-codes.md", errorDomainsBase)
	r.commit("develop registry baseline")

	// develop 上落 RESERVED，并同步到 shared origin/develop。
	r.write(".agent/registry/migrations.md", migrationsBase+"| 20261001000009 | foo | demo-task | RESERVED | 已落 develop |\n")
	r.commit("develop reserved")
	r.syncOriginDevelop()

	// feature 分支声明同一资源。
	r.git("checkout", "-q", "-b", "feature")
	r.write(".agent/tasks/demo-task/state.yaml", `task: demo-task
phase: IMPLEMENTING
review:
  status: NONE
owner_verification:
  status: PENDING
delivery:
  status: NONE
required_resources:
  migrations:
    - "20261001000009"
blocked:
  is_blocked: false
  reason: ""
`)
	r.commit("feature state")

	res, err := r.validator("").ValidateTask(".agent/tasks/demo-task")
	if err != nil {
		t.Fatalf("不应返回运行错误: %v", err)
	}
	if res.Status != StatusPass {
		t.Fatalf("develop 合法 Reservation 期望 PASS，实际 %s issues=%v", res.Status, res.Issues)
	}
}

// TestMissingMigrationForbiddenImplementing 覆盖测试要求 10：
// 缺 migration Reservation → 禁止 IMPLEMENTING。
func TestMissingMigrationForbiddenImplementing(t *testing.T) {
	r := newTestRepo(t)
	r.write(".agent/registry/migrations.md", migrationsBase)
	r.write(".agent/registry/error-codes.md", errorDomainsBase)
	r.commit("develop registry")
	r.syncOriginDevelop()

	r.write(".agent/tasks/demo-task/state.yaml", `task: demo-task
phase: IMPLEMENTING
review:
  status: NONE
owner_verification:
  status: PENDING
delivery:
  status: NONE
required_resources:
  migrations:
    - "20261001000077"
blocked:
  is_blocked: false
  reason: ""
`)
	r.commit("state")

	res, err := r.validator("").ValidateTask(".agent/tasks/demo-task")
	if err != nil {
		t.Fatalf("不应返回运行错误: %v", err)
	}
	if res.Status != StatusFail || !issueCheck(t, res.Issues, "Resource Authority") {
		t.Fatalf("缺 migration Reservation 应 FAIL，实际 %s issues=%v", res.Status, res.Issues)
	}
}

// TestResourceAuthorityLocalDevelopReservedInvalid 覆盖 CLEAN-004：
// local develop 分支私留 RESERVED，但 shared origin/develop 无记录 → FAIL。
// 验证 Validator 以 shared origin/develop 为权威，而不是本地 develop 分支。
func TestResourceAuthorityLocalDevelopReservedInvalid(t *testing.T) {
	r := newTestRepo(t)
	r.write(".agent/registry/migrations.md", migrationsBase)
	r.write(".agent/registry/error-codes.md", errorDomainsBase)
	r.commit("develop registry baseline")
	r.syncOriginDevelop() // origin/develop 停在 baseline（无 RESERVED）

	// 仅本地 develop 私留 RESERVED，不同步到 origin/develop。
	r.write(".agent/registry/migrations.md", migrationsBase+"| 20261001000009 | foo | demo-task | RESERVED | 本地 develop 私留 |\n")
	r.commit("local develop reserved")

	r.git("checkout", "-q", "-b", "feature")
	r.write(".agent/tasks/demo-task/state.yaml", `task: demo-task
phase: IMPLEMENTING
review:
  status: NONE
owner_verification:
  status: PENDING
delivery:
  status: NONE
required_resources:
  migrations:
    - "20261001000009"
blocked:
  is_blocked: false
  reason: ""
`)
	r.commit("feature state")

	res, err := r.validator("").ValidateTask(".agent/tasks/demo-task")
	if err != nil {
		t.Fatalf("不应返回运行错误: %v", err)
	}
	if res.Status != StatusFail || !issueCheck(t, res.Issues, "Resource Authority") {
		t.Fatalf("local develop 私留 RESERVED 应 FAIL（以 origin/develop 为权威），实际 %s issues=%v", res.Status, res.Issues)
	}
}

// TestMalformedYAMLExitError 覆盖测试要求 18：malformed YAML → 运行错误（exit 2 级）。
func TestMalformedYAMLExitError(t *testing.T) {
	r := newTestRepo(t)
	r.write(".agent/tasks/demo-task/state.yaml", "phase: [unclosed\n")
	r.commit("bad state")

	_, err := r.validator("").ValidateTask(".agent/tasks/demo-task")
	if err == nil {
		t.Fatal("malformed YAML 应返回运行错误")
	}
}
