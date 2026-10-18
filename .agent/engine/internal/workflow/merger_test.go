package workflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScratchCleanupPreservesChangedAndForeignRefs(t *testing.T) {
	r, base, src := setupDeliverable(t)
	branch := "scratch/merger-test-owned"
	gitAt(t, r.host, "branch", branch, src)
	g := &ExecGitOps{Root: r.host}
	if err := g.DeleteScratchBranch(branch, base); err == nil {
		t.Fatal("cleanup must reject a ref changed from expected SHA")
	}
	if got := gitAt(t, r.host, "rev-parse", branch); got != src {
		t.Fatal("changed scratch ref was removed")
	}
	if err := g.DeleteScratchBranch("feature/x", src); err == nil {
		t.Fatal("cleanup must reject foreign branch")
	}
}

func TestScratchNamesConcurrent(t *testing.T) {
	type allocation struct {
		name string
		err  error
	}
	results := make(chan allocation, 32)
	for i := 0; i < cap(results); i++ {
		go func() { name, err := nextScratchBranch("same-base"); results <- allocation{name, err} }()
	}
	seen := map[string]bool{}
	for i := 0; i < cap(results); i++ {
		a := <-results
		if a.err != nil {
			t.Fatal(a.err)
		}
		if seen[a.name] {
			t.Fatalf("duplicate scratch name: %s", a.name)
		}
		seen[a.name] = true
	}
}

// 本文件覆盖 Task 09 Merger 的受控隔离 Git 集成行为（真实临时仓库 + 真实 bare origin）。
//
// 覆盖：无冲突合并、目标漂移、未批准交付拒绝、无效来源 SHA、L2/L3 冲突分级、
// 合并结果测试失败父分支不变、fast-forward-only 禁强推。

// mRepo 是 merger 集成测试的临时仓库：宿主 repo + 真实 bare origin。
type mRepo struct {
	t      *testing.T
	host   string // 宿主仓库
	origin string // bare origin 路径
}

func newMRepo(t *testing.T) *mRepo {
	t.Helper()
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	host := filepath.Join(base, "host")
	gitAt(t, base, "init", "--bare", "-q", "origin.git")
	if err := os.MkdirAll(host, 0o755); err != nil {
		t.Fatalf("mkdir host: %v", err)
	}
	gitAt(t, host, "init", "-q")
	gitAt(t, host, "config", "user.name", "test")
	gitAt(t, host, "config", "user.email", "test@example.com")
	gitAt(t, host, "config", "commit.gpgsign", "false")
	gitAt(t, host, "remote", "add", "origin", origin)
	return &mRepo{t: t, host: host, origin: origin}
}

func gitAt(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (dir=%s): %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *mRepo) write(path, content string) {
	r.t.Helper()
	full := filepath.Join(r.host, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatalf("write: %v", err)
	}
}

func (r *mRepo) commit(msg string) string {
	r.t.Helper()
	gitAt(r.t, r.host, "add", "-A")
	gitAt(r.t, r.host, "commit", "-q", "-m", msg)
	return gitAt(r.t, r.host, "rev-parse", "HEAD")
}

func (r *mRepo) checkout(branch string, startPoint ...string) {
	r.t.Helper()
	args := []string{"checkout", "-q", "-b", branch}
	args = append(args, startPoint...)
	gitAt(r.t, r.host, args...)
}

func (r *mRepo) switchTo(branch string) {
	r.t.Helper()
	gitAt(r.t, r.host, "checkout", "-q", branch)
}

func (r *mRepo) push(branch string) {
	r.t.Helper()
	gitAt(r.t, r.host, "push", "-q", "origin", branch+":refs/heads/"+branch)
}

func (r *mRepo) merger() *Merger {
	return &Merger{Root: r.host, Git: &ExecGitOps{Root: r.host}}
}

// remoteSHA 通过 ls-remote 读取 origin 上分支的当前真实 SHA（不依赖本地 refs/remotes 缓存）。
func (r *mRepo) remoteSHA(branch string) string {
	r.t.Helper()
	out := gitAt(r.t, r.host, "ls-remote", "origin", "refs/heads/"+branch)
	fields := strings.Fields(out)
	if len(fields) == 0 {
		r.t.Fatalf("ls-remote %s 无输出", branch)
	}
	return fields[0]
}

// setupDeliverable 构建「父分支 develop 基线 + feature/x 后代」的可交付场景。
// 返回 base（develop 基线 SHA）与 src（feature SHA），并切回 develop。
func setupDeliverable(t *testing.T) (*mRepo, string, string) {
	r := newMRepo(t)
	r.checkout("develop")
	r.write("README.md", "# base\n")
	base := r.commit("baseline")
	r.push("develop")

	r.checkout("feature/x")
	r.write("internal/foo.go", "package foo\n")
	src := r.commit("feature implementation")
	r.push("feature/x")

	r.switchTo("develop")
	return r, base, src
}

func TestMergerNoConflictDelivers(t *testing.T) {
	r, base, src := setupDeliverable(t)

	req := MergeRequest{
		ParentBranch:    "develop",
		ParentBaseSHA:   base,
		Sources:         []MergeSource{{Branch: "feature/x", SHA: src}},
		OwnerAuthorized: true,
		TestCommand:     []string{"sh", "-c", "test -f internal/foo.go"},
	}
	rep, err := r.merger().Integrate(req)
	if err != nil {
		t.Fatalf("Integrate error: %v", err)
	}
	if rep.Level != MergeL1 {
		t.Fatalf("期望 L1，实际 %s", rep.Level)
	}
	if !rep.Merged || !rep.TestPassed || !rep.ParentUpdated {
		t.Fatalf("期望 Merged/TestPassed/ParentUpdated 全为 true，实际 %+v", rep)
	}
	if got := r.remoteSHA("develop"); got == base {
		t.Fatalf("origin/develop 未更新，仍为基线 %s", base)
	}
	// 合并结果应包含 feature 内容。
	if !strings.Contains(gitAt(t, r.host, "show", r.remoteSHA("develop")+":internal/foo.go"), "package foo") {
		t.Fatal("合并结果缺少 feature 内容")
	}
}

func TestMergerTargetDriftStops(t *testing.T) {
	r, base, src := setupDeliverable(t)

	// 父分支前进：origin/develop 漂移，固定基线 base 失效。
	r.write("README.md", "# base\n// advanced\n")
	r.commit("develop advance")
	r.push("develop")

	req := MergeRequest{
		ParentBranch:    "develop",
		ParentBaseSHA:   base,
		Sources:         []MergeSource{{Branch: "feature/x", SHA: src}},
		OwnerAuthorized: true,
		TestCommand:     []string{"true"},
	}
	rep, err := r.merger().Integrate(req)
	if err != nil {
		t.Fatalf("Integrate error: %v", err)
	}
	if rep.ParentUpdated {
		t.Fatal("目标漂移时不得更新父分支")
	}
	if !strings.Contains(rep.Reason, "漂移") {
		t.Fatalf("期望漂移原因，实际 %q", rep.Reason)
	}
}

func TestMergerNotAuthorizedRefusesDelivery(t *testing.T) {
	r, base, src := setupDeliverable(t)

	req := MergeRequest{
		ParentBranch:    "develop",
		ParentBaseSHA:   base,
		Sources:         []MergeSource{{Branch: "feature/x", SHA: src}},
		OwnerAuthorized: false,
		TestCommand:     []string{"true"},
	}
	rep, err := r.merger().Integrate(req)
	if err != nil {
		t.Fatalf("Integrate error: %v", err)
	}
	if !rep.Merged || !rep.TestPassed {
		t.Fatalf("隔离合并应已完成（Merged=%t TestPassed=%t），实际 %+v", rep.Merged, rep.TestPassed, rep)
	}
	if rep.ParentUpdated {
		t.Fatal("Owner 未授权时不得更新父分支")
	}
	if !strings.Contains(rep.Reason, "未明确授权") {
		t.Fatalf("期望未授权原因，实际 %q", rep.Reason)
	}
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("origin/develop 不应变化，实际 %s", got)
	}
}

func TestMergerInvalidSourceSHA(t *testing.T) {
	r, base, _ := setupDeliverable(t)

	req := MergeRequest{
		ParentBranch:    "develop",
		ParentBaseSHA:   base,
		Sources:         []MergeSource{{Branch: "feature/x", SHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}},
		OwnerAuthorized: true,
		TestCommand:     []string{"true"},
	}
	if _, err := r.merger().Integrate(req); err == nil {
		t.Fatal("无效来源 SHA 应返回 error")
	}
}

func TestMergerL2ConflictStops(t *testing.T) {
	r := newMRepo(t)
	r.checkout("develop")
	r.write("internal/foo.go", "base line\n")
	base := r.commit("baseline")
	r.push("develop")

	// 父分支前进：修改同一文件。
	r.write("internal/foo.go", "develop line\n")
	p1 := r.commit("develop change")
	r.push("develop")

	// feature 从 base 分叉，冲突式修改同一文件。
	r.checkout("feature/x", base)
	r.write("internal/foo.go", "feature line\n")
	src := r.commit("feature change")
	r.push("feature/x")
	r.switchTo("develop")

	req := MergeRequest{
		ParentBranch:    "develop",
		ParentBaseSHA:   p1,
		Sources:         []MergeSource{{Branch: "feature/x", SHA: src}},
		OwnerAuthorized: true,
		TestCommand:     []string{"true"},
	}
	rep, err := r.merger().Integrate(req)
	if err != nil {
		t.Fatalf("Integrate error: %v", err)
	}
	if rep.Level != MergeL2 {
		t.Fatalf("局部实现冲突期望 L2，实际 %s", rep.Level)
	}
	if rep.Merged || rep.ParentUpdated {
		t.Fatalf("L2 冲突应停止且不更新父分支，实际 Merged=%t ParentUpdated=%t", rep.Merged, rep.ParentUpdated)
	}
	if got := r.remoteSHA("develop"); got != p1 {
		t.Fatalf("origin/develop 不应变化，实际 %s", got)
	}
}

func TestMergerL3ConflictStopsNoAutoFix(t *testing.T) {
	r := newMRepo(t)
	r.checkout("develop")
	r.write("docs/design/arch.md", "# architecture\nbase\n")
	base := r.commit("baseline")
	r.push("develop")

	r.write("docs/design/arch.md", "# architecture\ndevelop\n")
	p1 := r.commit("develop architecture change")
	r.push("develop")

	r.checkout("feature/x", base)
	r.write("docs/design/arch.md", "# architecture\nfeature\n")
	src := r.commit("feature architecture change")
	r.push("feature/x")
	r.switchTo("develop")

	req := MergeRequest{
		ParentBranch:    "develop",
		ParentBaseSHA:   p1,
		Sources:         []MergeSource{{Branch: "feature/x", SHA: src}},
		OwnerAuthorized: true,
		TestCommand:     []string{"true"},
	}
	rep, err := r.merger().Integrate(req)
	if err != nil {
		t.Fatalf("Integrate error: %v", err)
	}
	if rep.Level != MergeL3 {
		t.Fatalf("架构语义冲突期望 L3，实际 %s", rep.Level)
	}
	if rep.Merged || rep.ParentUpdated {
		t.Fatalf("L3 冲突应停止且不得自动修复，实际 Merged=%t ParentUpdated=%t", rep.Merged, rep.ParentUpdated)
	}
	// 冲突文件不得被 Merger 删除或改写（parent 保持原样）。
	if got := gitAt(t, r.host, "show", r.remoteSHA("develop")+":docs/design/arch.md"); !strings.Contains(got, "develop") {
		t.Fatalf("父分支架构文件被意外改写: %q", got)
	}
}

func TestMergerTestFailureParentUnchanged(t *testing.T) {
	r, base, src := setupDeliverable(t)

	req := MergeRequest{
		ParentBranch:    "develop",
		ParentBaseSHA:   base,
		Sources:         []MergeSource{{Branch: "feature/x", SHA: src}},
		OwnerAuthorized: true,
		TestCommand:     []string{"sh", "-c", "exit 1"}, // 真实测试命令，必然失败
	}
	rep, err := r.merger().Integrate(req)
	if err != nil {
		t.Fatalf("Integrate error: %v", err)
	}
	if !rep.Merged {
		t.Fatal("隔离合并应已执行（Merged 应为 true）")
	}
	if rep.TestPassed || rep.ParentUpdated {
		t.Fatalf("测试失败时不得更新父分支，实际 TestPassed=%t ParentUpdated=%t", rep.TestPassed, rep.ParentUpdated)
	}
	if got := r.remoteSHA("develop"); got != base {
		t.Fatalf("测试失败时 origin/develop 不应变化，实际 %s", got)
	}
}

func TestMergerPushFastForwardOnly(t *testing.T) {
	r, base, src := setupDeliverable(t)

	// 先成功交付一次，使 origin/develop 前进到 src。
	req := MergeRequest{
		ParentBranch:    "develop",
		ParentBaseSHA:   base,
		Sources:         []MergeSource{{Branch: "feature/x", SHA: src}},
		OwnerAuthorized: true,
		TestCommand:     []string{"true"},
	}
	if _, err := r.merger().Integrate(req); err != nil {
		t.Fatalf("首次 Integrate error: %v", err)
	}

	// 现在 origin/develop 已在 src。尝试把旧基线 base（祖先）推回 develop → 非快进必须被拒绝。
	if err := r.merger().Git.PushFastForward("origin", base, "develop"); err == nil {
		t.Fatal("非快进推送应被拒绝（禁止 force push）")
	}
	if got := r.remoteSHA("develop"); got != src {
		t.Fatalf("非快进推送失败后 origin/develop 应保持 %s，实际 %s", src, got)
	}
}

func TestClassifyConflict(t *testing.T) {
	cases := []struct {
		name string
		path string
		want MergeConflictLevel
	}{
		{"本地实现", "internal/foo.go", MergeL2},
		{"任务契约", ".agent/tasks/demo/contract.md", MergeL3},
		{"共享资源权威", ".agent/registry/migrations.md", MergeL3},
		{"长期架构", "docs/design/arch.md", MergeL3},
		{"协作规范", ".agent/specs/AgentCollaborationSpecification.md", MergeL3},
		{"机器配置", ".agent/workflow.yaml", MergeL3},
		{"依赖契约", "go.mod", MergeL3},
		{"根依赖校验和", "go.sum", MergeL3},
		{"引擎依赖契约", ".agent/engine/go.mod", MergeL3},
		{"引擎依赖校验和", ".agent/engine/go.sum", MergeL3},
		{"业务子模块依赖", "services/orders/go.mod", MergeL3},
		{"业务子模块校验和", "services/orders/go.sum", MergeL3},
		{"嵌套备份文件", "services/orders/go.mod.backup", MergeL2},
		{"相似文件名", "services/orders/notgo.sum", MergeL2},
		{"普通代码", "pkg/service.go", MergeL2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classifyConflict([]string{c.path}); got != c.want {
				t.Fatalf("classifyConflict(%q) = %s，期望 %s", c.path, got, c.want)
			}
		})
	}
	if classifyConflict([]string{}) != MergeL1 {
		t.Fatal("无冲突应返回 L1（由调用方保证；此处防御性断言默认路径不误报）")
	}
}

// OD-4: real nested dependency conflicts must stop before tests or parent writes.
func TestMergerNestedGoDependencyConflictStopsAtL3(t *testing.T) {
	for _, name := range []string{".agent/engine/go.mod", ".agent/engine/go.sum"} {
		t.Run(filepath.Base(name), func(t *testing.T) {
			r := newMRepo(t)
			r.checkout("develop")
			r.write(name, "baseline\n")
			base := r.commit("dependency baseline")
			r.push("develop")
			r.write(name, "parent dependency\n")
			parent := r.commit("parent dependency change")
			r.push("develop")
			r.checkout("feature/dependency", base)
			r.write(name, "feature dependency\n")
			source := r.commit("feature dependency change")
			r.push("feature/dependency")
			r.switchTo("develop")
			marker := filepath.Join(t.TempDir(), "test-executed")
			rep, err := r.merger().Integrate(MergeRequest{
				ParentBranch: "develop", ParentBaseSHA: parent,
				Sources:         []MergeSource{{Branch: "feature/dependency", SHA: source}},
				OwnerAuthorized: true, TestCommand: []string{"sh", "-c", `touch "$1"`, "_", marker},
			})
			if err != nil {
				t.Fatal(err)
			}
			if rep.Level != MergeL3 || rep.Merged || rep.TestPassed || rep.ParentUpdated {
				t.Fatalf("dependency conflict must stop at L3: %+v", rep)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("post-merge test unexpectedly ran: %v", err)
			}
			if got := r.remoteSHA("develop"); got != parent {
				t.Fatalf("parent moved: %s", got)
			}
			if got := gitAt(t, r.host, "status", "--porcelain"); got != "" {
				t.Fatalf("host polluted: %s", got)
			}
			if got := gitAt(t, r.host, "show", parent+":"+name); got != "parent dependency" {
				t.Fatal(got)
			}
		})
	}
}
