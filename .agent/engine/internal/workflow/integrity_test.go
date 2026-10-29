package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 本文件是 V3.1.0 Engine Integrity Hardening 的回归测试：
//
//	E-1   Review Target 必须是当前 feature HEAD 的祖先（孤儿化 target 即使 Tree 相同也必须拒绝）；
//	E-2   只能接受不可变 Commit 引用，且长短 SHA 必须归一化后比较；
//	E-3   blocked.active 对五个 Gate 一律生效；
//	E-4   Git rename detection 不得把实质变更折叠成 neutral 目标路径；
//	E-6a/b loop-ledger.md / danger-report.md 白名单必须 task-scoped。
//
// 所有 Git 历史模拟（孤儿 Commit、可移动 Tag、十六进制样式引用名、rename、非法 SHA）
// 都在 testRepo 临时夹具仓库内完成，不触碰正式源仓库的运行状态。

const taskDirDemo = ".agent/tasks/demo"

// featureBranchName 与既有夹具使用的 feature 分支名保持一致。
const featureBranchName = "feature/agent-workflow-v2"

// commitState 写入 state.yaml 并提交，便于构造多步 history。
func (r *testRepo) commitState(s State, msg string) string {
	r.writeState(s)
	return r.commit(msg)
}

// pushFeature 把当前 HEAD 标记为 feature 远端（模拟 push）。
func (r *testRepo) pushFeature() {
	r.syncOrigin(featureBranchName)
}

// mkdir 在夹具仓库内创建目录（用于构造 rename 目标位置等）。
func (r *testRepo) mkdir(rel string) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Join(r.dir, rel), 0o755); err != nil {
		r.t.Fatalf("创建目录失败: %v", err)
	}
}

// lastLine 取出 git 输出的最后一行（stderr 上的 ambiguity warning 会被一并返回）。
func lastLine(out string) string {
	parts := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(parts[len(parts)-1])
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

// reviewedCleanFixture 构建：develop 基线 → feature C1 实现 → C2 PENDING → C3 CLEAN，全部已同步。
func reviewedCleanFixture(t *testing.T) (*testRepo, map[string]string) {
	t.Helper()
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	shas := map[string]string{"D12": r.git("rev-parse", "HEAD")}
	r.syncOrigin("develop")

	r.checkout(featureBranchName)
	r.write("internal/foo.go", "package foo\n")
	shas["C1"] = r.commit("implementation")

	s := baseState()
	s.Review = Review{Status: ReviewPending, Target: shas["C1"]}
	shas["C2"] = r.commitState(s, "review request")

	s.Review = Review{Status: ReviewClean, Target: shas["C1"]}
	shas["C3"] = r.commitState(s, "clean review")
	r.pushFeature()
	return r, shas
}

// fullStateFromMergeReady 返回 setupMergeReady 对应的 owner ACCEPTED + delivery PASS 状态。
func fullStateFromMergeReady(shas map[string]string) State {
	s := baseState()
	s.Review = Review{Status: ReviewClean, Target: shas["C1"]}
	s.Owner = Owner{Status: OwnerAccepted, ReviewTarget: shas["C1"]}
	s.Delivery = Delivery{Status: DeliveryPass, ReviewTarget: shas["C1"], FeatureHead: shas["F8"], DevelopBase: shas["D12"]}
	return s
}

// --- 正向：NORMAL 五 Gate 完整成功路径（含 delivery-start）---

func TestFiveGateNormalSequence(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	developBase := r.git("rev-parse", "HEAD")
	r.syncOrigin("develop")

	r.checkout(featureBranchName)

	// coder-start：contract NOT_REQUIRED、无资源、尚未实现。
	s := baseState()
	r.commitState(s, "task state")
	r.pushFeature()
	expectStatus(t, r.validator().GateCoderStart(taskDirDemo), StatusPass)

	// cleaner-start：C1 实现 + C2 PENDING 元数据。
	r.write("internal/foo.go", "package foo\n")
	c1 := r.commit("implementation")
	s.Review = Review{Status: ReviewPending, Target: c1}
	r.commitState(s, "review request")
	r.pushFeature()
	expectStatus(t, r.validator().GateCleanerStart(taskDirDemo), StatusPass)

	// owner-gate-start：C3 CLEAN + review-neutral artifacts。
	r.write(".agent/tasks/demo/findings.md", "# findings\n")
	r.write(".agent/tasks/demo/core-logic.md", "# core logic\n")
	s.Review = Review{Status: ReviewClean, Target: c1}
	r.commitState(s, "clean review")
	r.pushFeature()
	expectStatus(t, r.validator().GateOwnerGateStart(taskDirDemo), StatusPass)

	// delivery-start：owner ACCEPTED 绑定同一 review.target。
	r.write(".agent/tasks/demo/owner-decision.md", "# decision\n")
	s.Owner = Owner{Status: OwnerAccepted, ReviewTarget: c1}
	featureHead := r.commitState(s, "owner acceptance")
	r.pushFeature()
	expectStatus(t, r.validator().GateDeliveryStart(taskDirDemo), StatusPass)

	// merge-ready：delivery PASS 绑定 feature_head / develop_base。
	r.write(".agent/tasks/demo/delivery.md", "# delivery\n")
	s.Delivery = Delivery{Status: DeliveryPass, ReviewTarget: c1, FeatureHead: featureHead, DevelopBase: developBase}
	r.commitState(s, "delivery pass")
	r.pushFeature()
	expectStatus(t, r.validator().GateMergeReady(taskDirDemo), StatusPass)
}

// --- 正向：历史合法短 SHA 与完整 SHA 归一化比较 ---

// TestShortSHABindingsAccepted 覆盖 review / owner / delivery 全部 SHA 绑定使用历史短 SHA，
// 且 developer 书写时混用长短 SHA（指向同一 Commit）仍应判为同一绑定。
func TestShortSHABindingsAccepted(t *testing.T) {
	for _, tc := range []struct {
		name         string
		reviewTarget func(full string) string
		ownerTarget  func(full string) string
		deliveryRT   func(full string) string
		featureHead  func(full string) string
		developBase  func(full string) string
	}{
		{name: "全短 SHA", reviewTarget: short7, ownerTarget: short7, deliveryRT: short7, featureHead: short7, developBase: short7},
		{name: "全完整 SHA", reviewTarget: id, ownerTarget: id, deliveryRT: id, featureHead: id, developBase: id},
		{name: "长短混用", reviewTarget: id, ownerTarget: short7, deliveryRT: short7, featureHead: id, developBase: short7},
		{name: "长短混用反向", reviewTarget: short7, ownerTarget: id, deliveryRT: id, featureHead: short7, developBase: id},
		{name: "12 位缩写", reviewTarget: short12, ownerTarget: short12, deliveryRT: short12, featureHead: short12, developBase: short12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, shas := setupMergeReady(t)
			// 先制造一个 review / delivery 双中性的新提交，保证 state 变更一定产生新 Commit。
			r.write(".agent/tasks/demo/loop-ledger.md", "# loop ledger\n"+tc.name)
			s := baseState()
			s.Review = Review{Status: ReviewClean, Target: tc.reviewTarget(shas["C1"])}
			s.Owner = Owner{Status: OwnerAccepted, ReviewTarget: tc.ownerTarget(shas["C1"])}
			s.Delivery = Delivery{
				Status:       DeliveryPass,
				ReviewTarget: tc.deliveryRT(shas["C1"]),
				FeatureHead:  tc.featureHead(shas["F8"]),
				DevelopBase:  tc.developBase(shas["D12"]),
			}
			r.commitState(s, "short sha evidence")
			r.pushFeature()

			v := r.validator()
			expectStatus(t, v.GateOwnerGateStart(taskDirDemo), StatusPass)
			expectStatus(t, v.GateDeliveryStart(taskDirDemo), StatusPass)
			expectStatus(t, v.GateMergeReady(taskDirDemo), StatusPass)
		})
	}
}

func id(full string) string      { return full }
func short7(full string) string  { return full[:7] }
func short12(full string) string { return full[:12] }

// --- 正向：Schema 2 兼容 + 短 SHA ---

func TestLegacyV2StateShortSHATargetAccepted(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	r.syncOrigin("develop")

	r.checkout(featureBranchName)
	r.write("internal/foo.go", "package foo\n")
	c1 := r.commit("implementation")

	// review.target 用短 SHA，owner.review_target 用完整 SHA：混用也必须判为同一绑定。
	v2State := `schema_version: 2
task_id: demo
contract:
  status: NOT_REQUIRED
  target: ""
resources:
  migrations: []
  error_code_domains: []
review:
  status: CLEAN
  target: "` + c1[:7] + `"
owner:
  status: ACCEPTED
  review_target: "` + c1 + `"
delivery:
  status: NOT_RUN
  review_target: ""
  feature_head: ""
  develop_base: ""
blocked:
  active: false
  by: ""
  reason: ""
`
	r.write(".agent/tasks/demo/state.yaml", v2State)
	r.write(".agent/tasks/demo/findings.md", "# findings\n")
	r.commit("legacy v2 clean state")
	r.pushFeature()

	expectStatus(t, r.validator().GateOwnerGateStart(taskDirDemo), StatusPass)
}

// --- 正向：CLEAN / Delivery tail 允许 DANGER 证据文件 ---

func TestCleanTailAllowsDangerEvidenceFiles(t *testing.T) {
	r, shas := reviewedCleanFixture(t)
	s := baseState()
	s.Review = Review{Status: ReviewClean, Target: shas["C1"]}

	r.write(".agent/tasks/demo/loop-ledger.md", "# loop ledger\n")
	r.write(".agent/tasks/demo/danger-report.md", "# danger report\n")
	r.write(".agent/tasks/demo/findings.md", "# findings\n")
	r.writeState(s)
	r.commit("post clean danger evidence")
	r.pushFeature()

	expectStatus(t, r.validator().GateOwnerGateStart(taskDirDemo), StatusPass)
}

func TestDeliveryTailAllowsDangerEvidenceFiles(t *testing.T) {
	r, shas := setupMergeReady(t)
	s := fullStateFromMergeReady(shas)

	r.write(".agent/tasks/demo/loop-ledger.md", "# loop ledger\n")
	r.write(".agent/tasks/demo/danger-report.md", "# danger report\n")
	r.writeState(s)
	r.commit("post delivery danger evidence")
	r.pushFeature()

	expectStatus(t, r.validator().GateMergeReady(taskDirDemo), StatusPass)
}

// --- E-4 正向机制：rename 必须同时暴露源路径与目标路径 ---

func TestDiffNameOnlyRangeKeepsBothSidesOfRename(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.mkdir(".agent/tasks/demo")
	r.write("internal/keep.go", "package keep\n")
	base := r.commit("baseline")

	r.git("mv", "internal/keep.go", ".agent/tasks/demo/findings.md")
	head := r.commit("move production code into neutral filename")

	g := &ExecGit{Root: r.dir}
	got, err := g.DiffNameOnlyRange(base, head)
	if err != nil {
		t.Fatalf("DiffNameOnlyRange: %v", err)
	}
	// rename detection 会把两者折叠为一条目标路径，从而让生产源文件「消失」。
	if !containsPath(got, "internal/keep.go") {
		t.Fatalf("rename 的源路径缺失，生产代码变更不可见: %v", got)
	}
	if !containsPath(got, ".agent/tasks/demo/findings.md") {
		t.Fatalf("rename 的目标路径缺失: %v", got)
	}
}

// --- E-2 负向：可变 / 非法 Commit 引用 ---

func TestReviewTargetMutableRefsRejected(t *testing.T) {
	cases := []struct{ name, target string }{
		{"HEAD", "HEAD"},
		{"revision 表达式", "HEAD~1"},
		{"feature 分支名", featureBranchName},
		{"集成分支名", "develop"},
		{"远端追踪引用", "origin/" + featureBranchName},
		{"可变 Tag 名", "v-evidence"},
		{"非法占位符", "latest"},
		{"空值以外非法形态", "../../../HEAD"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := setupCleanerStart(t)
			s := baseState()
			s.Review = Review{Status: ReviewPending, Target: tc.target}
			r.commitState(s, "bind mutable ref as review.target")
			r.pushFeature()

			// cleaner-start / owner-gate-start 必须给出一致结论（共享 reviewTailValid）。
			expectCheck(t, r.validator().GateCleanerStart(taskDirDemo), "Review Tail")

			cs := s
			cs.Review = Review{Status: ReviewClean, Target: tc.target}
			cs.Owner = Owner{Status: OwnerAccepted, ReviewTarget: tc.target}
			r.commitState(cs, "clean + owner accepted on mutable ref")
			r.pushFeature()
			expectCheck(t, r.validator().GateOwnerGateStart(taskDirDemo), "Review Tail")
		})
	}
}

func TestContractTargetMutableRefsRejected(t *testing.T) {
	cases := []struct{ name, target string }{
		{"HEAD", "HEAD"},
		{"revision 表达式", "HEAD^"},
		{"feature 分支名", featureBranchName},
		{"集成分支名", "develop"},
		{"远端追踪引用", "origin/" + featureBranchName},
		{"可变 Tag 名", "v-contract"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := setupCoderStartApproved(t)
			s := baseState()
			s.Contract = Contract{Status: ContractApproved, Target: tc.target}
			r.commitState(s, "bind mutable ref as contract.target")
			r.pushFeature()

			expectCheck(t, r.validator().GateCoderStart(taskDirDemo), "Contract (INV-2)")
		})
	}
}

// TestReviewTargetMovingTagRejected 覆盖「可移动 Tag」：
// 即便 Tag 此刻恰好指向正确 Commit，也不得作为不可变 Evidence 被接受。
func TestReviewTargetMovingTagRejected(t *testing.T) {
	t.Run("tag 当前不指向 target", func(t *testing.T) {
		r := newTestRepo(t)
		r.write("README.md", "# base\n")
		r.commit("baseline")
		r.syncOrigin("develop")
		r.checkout(featureBranchName)

		r.write("internal/foo.go", "package foo\n")
		c1 := r.commit("implementation")
		r.git("tag", "v-evidence", c1)

		s := baseState()
		s.Review = Review{Status: ReviewPending, Target: c1}
		c2 := r.commitState(s, "review request")
		r.git("tag", "-f", "v-evidence", c2) // Tag 被移动

		s.Review = Review{Status: ReviewPending, Target: "v-evidence"}
		r.commitState(s, "bind tag as review.target")
		r.pushFeature()
		expectCheck(t, r.validator().GateCleanerStart(taskDirDemo), "Review Tail")
	})

	t.Run("tag 当前恰好指向 target", func(t *testing.T) {
		r := newTestRepo(t)
		r.write("README.md", "# base\n")
		r.commit("baseline")
		r.syncOrigin("develop")
		r.checkout(featureBranchName)

		r.write("internal/foo.go", "package foo\n")
		c1 := r.commit("implementation")
		r.git("tag", "v-evidence", c1)

		s := baseState()
		s.Review = Review{Status: ReviewPending, Target: "v-evidence"}
		r.commitState(s, "bind tag as review.target")
		r.pushFeature()

		// 前提校验：Tag 此刻确实指向 C1（排除「Tag 不存在」这类无关失败原因）。
		if got := r.git("rev-parse", "v-evidence^{commit}"); got != c1 {
			t.Fatalf("fixture 前提不成立: v-evidence=%s 期望 %s", got, c1)
		}
		expectCheck(t, r.validator().GateCleanerStart(taskDirDemo), "Review Tail")
	})
}

// TestReviewTargetHexNamedRefRejected 覆盖「十六进制样式的引用名称」：
// 引用名长得像 SHA，但 Git 解析的是引用而非对象，形成可被移动的别名。
func TestReviewTargetHexNamedRefRejected(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	r.syncOrigin("develop")
	r.checkout(featureBranchName)

	r.write("internal/foo.go", "package foo\n")
	c1 := r.commit("implementation")
	r.write("internal/foo.go", "package foo\n// v2\n")
	c2 := r.commit("more work")

	// 用 C1 的 7 位前缀作分支名，但让该引用指向另一个 Commit。
	alias := c1[:7]
	r.git("branch", alias, c2)

	// 前提校验：Git 对该字面值的解析确实优先命中引用（别名生效），显式固定这一 Git 事实。
	if got := lastLine(r.git("rev-parse", alias+"^{commit}")); got != c2 {
		t.Fatalf("fixture 前提不成立: %s 解析为 %s，期望 %s（Git 未优先解析引用）", alias, got, c2)
	}

	s := baseState()
	s.Review = Review{Status: ReviewPending, Target: alias}
	r.commitState(s, "bind hex-named branch as review.target")
	r.pushFeature()

	expectCheck(t, r.validator().GateCleanerStart(taskDirDemo), "Review Tail")
}

// TestEvidenceTargetNotACommitRejected 覆盖不存在、过短、歧义与非十六进制形态。
func TestEvidenceTargetNotACommitRejected(t *testing.T) {
	// 用一个真实 Commit 的 SHA 派生非法形态，确保「存在于仓库的对象」也会被形态规则拒绝。
	_, probeShas := reviewedCleanFixture(t)
	c1 := probeShas["C1"]

	cases := []struct{ name, target string }{
		{"不存在的完整 SHA", "1234567890123456789012345678901234567890"},
		{"不存在的短 SHA", "aaaaaaa"},
		{"过短缩写", c1[:6]},      // 真实对象但不足 7 位
		{"超长十六进制", c1 + "aa"}, // 41 位
		{"带空白", " " + c1},
		{"非十六进制", "zzzzzzz"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr, _ := reviewedCleanFixture(t)
			s := baseState()
			s.Review = Review{Status: ReviewClean, Target: tc.target}
			rr.commitState(s, "invalid review.target")
			rr.pushFeature()

			res := rr.validator().GateOwnerGateStart(taskDirDemo)
			if res.Status == StatusPass {
				t.Fatalf("%s 不得 PASS：target=%q", tc.name, tc.target)
			}
			if res.Status == StatusError && res.Error == "" {
				t.Fatalf("ERROR 必须携带可定位信息")
			}
			if res.Status == StatusFail && !issueCheck(t, res.Issues, "Review Tail") {
				t.Fatalf("FAIL 必须落在 Review Tail 检查项: %v", res.Issues)
			}
		})
	}
}

// --- E-1：孤儿化 Review Target（Tree 相同）---

// orphanRewrite 用完全相同的 Tree 但全新 Root Commit 改写 feature 历史，使给定 Commit 被孤儿化。
func orphanRewrite(t *testing.T, r *testRepo, orphaned string) string {
	t.Helper()
	oldHead := r.git("rev-parse", "HEAD")
	r.git("checkout", "-q", "--orphan", "history-rewrite-scratch")
	newHead := r.commit("rewrite history with identical tree")
	r.git("branch", "-M", featureBranchName)
	r.pushFeature()

	// 夹具前提：改写前后 Tree 完全一致（纯 Tree Diff 无法发现问题）+ SHA 确实变化。
	if diff := r.git("diff", "--name-only", oldHead, newHead); diff != "" {
		t.Fatalf("fixture 前提不成立：改写前后 Tree diff 非空: %q", diff)
	}
	if oldHead == newHead {
		t.Fatal("fixture 前提不成立：历史改写未产生新 SHA")
	}
	return newHead
}

func TestReviewTargetOrphanedSameTreeRejected(t *testing.T) {
	r, shas := reviewedCleanFixture(t)
	orphanRewrite(t, r, shas["C1"])

	// Tree Diff 为空不能证明 target 属于当前 feature 历史。
	expectCheck(t, r.validator().GateOwnerGateStart(taskDirDemo), "Review Tail")
}

// TestOrphanedTargetRejectedByEveryGate 证明四个依赖 CLEAN 有效性的 Gate 语义一致。
func TestOrphanedTargetRejectedByEveryGate(t *testing.T) {
	r, shas := setupMergeReady(t)

	// 只通过 state.yaml（review / delivery 双中性）推进到最终 Evidence 状态，
	// 保证 old/new 两个实现的 tail 内容一致：拒绝原因只能来自 target 孤儿化本身。
	s := fullStateFromMergeReady(shas)
	intermediate := s
	intermediate.Delivery = Delivery{Status: DeliveryNotRun}
	r.commitState(intermediate, "delivery evidence not yet recorded")
	r.commitState(s, "baseline delivery evidence")
	r.pushFeature()

	newHead := orphanRewrite(t, r, shas["C1"])

	// 把 feature_head 绑到改写后的 HEAD，使 deliveryFreshness 在两种实现下都成立，
	// 从而把 merge-ready 的拒绝原因隔离到 CLEAN 有效性。
	s.Delivery.FeatureHead = newHead
	r.commitState(s, "rebind feature head after rewrite")
	r.pushFeature()

	v := r.validator()
	expectCheck(t, v.GateOwnerGateStart(taskDirDemo), "Review Tail")
	expectCheck(t, v.GateDeliveryStart(taskDirDemo), "Review Tail")
	expectCheck(t, v.GateMergeReady(taskDirDemo), "Review Tail")
}

// --- E-4：rename 绕过 Review / Delivery tail ---

func TestReviewTailRenameProductionToNeutralRejected(t *testing.T) {
	r, _ := reviewedCleanFixture(t)
	// 前提：CLEAN 后尚未变更时应通过。
	expectStatus(t, r.validator().GateOwnerGateStart(taskDirDemo), StatusPass)

	// 把生产代码搬到 review-neutral 文件名：rename detection 会把它折叠成目标路径。
	r.git("mv", "internal/foo.go", ".agent/tasks/demo/findings.md")
	r.commit("move production code into neutral filename")
	r.pushFeature()

	expectCheck(t, r.validator().GateOwnerGateStart(taskDirDemo), "Review Tail")
}

func TestReviewTailRenameNeutralToProductionRejected(t *testing.T) {
	r, shas := reviewedCleanFixture(t)
	s := baseState()
	s.Review = Review{Status: ReviewClean, Target: shas["C1"]}

	r.write(".agent/tasks/demo/findings.md", "# findings\n")
	r.commitState(s, "neutral artifact")
	r.pushFeature()
	expectStatus(t, r.validator().GateOwnerGateStart(taskDirDemo), StatusPass)

	// 中性文件被搬出去变成生产代码，同样必须识别。
	r.git("mv", ".agent/tasks/demo/findings.md", "internal/smuggled.go")
	r.commit("move neutral artifact into production path")
	r.pushFeature()

	expectCheck(t, r.validator().GateOwnerGateStart(taskDirDemo), "Review Tail")
}

// TestDeliveryTailRenameProductionToNeutralRejected 针对 delivery tail：
// feature_head 之后把生产文件搬到 delivery-neutral 的 delivery.md。
func TestDeliveryTailRenameProductionToNeutralRejected(t *testing.T) {
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	developBase := r.git("rev-parse", "HEAD")
	r.syncOrigin("develop")

	r.checkout(featureBranchName)
	// C1 实现
	r.write("internal/foo.go", "package foo\n")
	c1 := r.commit("implementation")
	// owner ACCEPTED（不创建 delivery.md）
	s := baseState()
	s.Review = Review{Status: ReviewClean, Target: c1}
	s.Owner = Owner{Status: OwnerAccepted, ReviewTarget: c1}
	featureHead := r.commitState(s, "clean + owner accepted")

	// Delivery Evidence 建立后才搬运生产代码；delivery.md 是 delivery-neutral 文件名。
	r.git("mv", "internal/foo.go", ".agent/tasks/demo/delivery.md")
	renamed := r.commit("move production code into delivery.md")

	// 重新 CLEAN 到搬运后的 Commit，使 review tail 不再是拒绝原因，隔离出 delivery tail。
	s.Review = Review{Status: ReviewClean, Target: renamed}
	s.Owner = Owner{Status: OwnerAccepted, ReviewTarget: renamed}
	s.Delivery = Delivery{Status: DeliveryPass, ReviewTarget: renamed, FeatureHead: featureHead, DevelopBase: developBase}
	r.commitState(s, "delivery evidence after rename")
	r.pushFeature()

	expectCheck(t, r.validator().GateMergeReady(taskDirDemo), "Delivery Freshness")
}

// --- E-3：blocked.active 必须拦住五个 Gate ---

func TestBlockedActiveBlocksAllFiveGates(t *testing.T) {
	t.Run("coder-start", func(t *testing.T) {
		r, shas := setupCoderStartApproved(t)
		s := baseState()
		s.Contract = Contract{Status: ContractApproved, Target: shas["A1"]}
		s.Blocked = Blocked{Active: true, By: "Owner", Reason: "danger"}
		r.commitState(s, "blocked")
		r.pushFeature()
		expectCheck(t, r.validator().GateCoderStart(taskDirDemo), "Coder Start")
	})

	t.Run("cleaner-start", func(t *testing.T) {
		r, shas := setupCleanerStart(t)
		s := baseState()
		s.Review = Review{Status: ReviewPending, Target: shas["C1"]}
		s.Blocked = Blocked{Active: true, By: "Owner", Reason: "danger"}
		r.commitState(s, "blocked")
		r.pushFeature()
		expectCheck(t, r.validator().GateCleanerStart(taskDirDemo), "Cleaner Start")
	})

	t.Run("owner-gate-start", func(t *testing.T) {
		r, shas := reviewedCleanFixture(t)
		s := baseState()
		s.Review = Review{Status: ReviewClean, Target: shas["C1"]}
		s.Blocked = Blocked{Active: true, By: "Owner", Reason: "danger"}
		r.commitState(s, "blocked")
		r.pushFeature()
		expectCheck(t, r.validator().GateOwnerGateStart(taskDirDemo), "Owner Gate Start")
	})

	t.Run("delivery-start", func(t *testing.T) {
		r, shas := reviewedCleanFixture(t)
		s := baseState()
		s.Review = Review{Status: ReviewClean, Target: shas["C1"]}
		s.Owner = Owner{Status: OwnerAccepted, ReviewTarget: shas["C1"]}
		s.Blocked = Blocked{Active: true, By: "Owner", Reason: "danger"}
		r.commitState(s, "blocked")
		r.pushFeature()
		expectCheck(t, r.validator().GateDeliveryStart(taskDirDemo), "Delivery Start")
	})

	t.Run("merge-ready", func(t *testing.T) {
		r, shas := setupMergeReady(t)
		s := fullStateFromMergeReady(shas)
		s.Blocked = Blocked{Active: true, By: "Owner", Reason: "danger"}
		r.commitState(s, "blocked")
		r.pushFeature()
		expectCheck(t, r.validator().GateMergeReady(taskDirDemo), "Merge Ready")
	})
}

// --- E-6a/6b：CLEAN / Delivery tail 的实质变更必须被拒绝 ---

func TestCleanTailSubstantiveFilesRejected(t *testing.T) {
	cases := []struct{ name, path, initial, changed string }{
		{"非中性 architecture.md", ".agent/tasks/demo/architecture.md", "# arch\n", "# arch v2\n"},
		{"生产 Go 源码", "internal/service.go", "package service\n", "package service\n// rework\n"},
		{"生产 SQL", "db/V1__baseline.sql", "SELECT 1;\n", "SELECT 2;\n"},
		{"Design 文档", "docs/design/foo.md", "# design\n", "# design v2\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRepo(t)
			r.write("README.md", "# base\n")
			r.commit("baseline")
			r.syncOrigin("develop")
			r.checkout(featureBranchName)

			r.write("internal/foo.go", "package foo\n")
			r.write(tc.path, tc.initial)
			c1 := r.commit("implementation")

			s := baseState()
			s.Review = Review{Status: ReviewClean, Target: c1}
			r.commitState(s, "clean review")
			r.pushFeature()
			expectStatus(t, r.validator().GateOwnerGateStart(taskDirDemo), StatusPass)

			r.write(tc.path, tc.changed)
			r.commit("post clean substantive change")
			r.pushFeature()
			expectCheck(t, r.validator().GateOwnerGateStart(taskDirDemo), "Review Tail")
		})
	}
}

// TestDangerEvidenceFilesMustBeTaskScoped 覆盖其他 Task 同名文件与当前 Task 子目录同名文件。
func TestDangerEvidenceFilesMustBeTaskScoped(t *testing.T) {
	paths := []string{
		".agent/tasks/other/loop-ledger.md",
		".agent/tasks/other/danger-report.md",
		".agent/tasks/demo/sub/loop-ledger.md",
		".agent/tasks/demo/sub/danger-report.md",
	}
	for _, p := range paths {
		t.Run("review tail "+p, func(t *testing.T) {
			r, _ := reviewedCleanFixture(t)
			r.write(p, "# ledger\n")
			r.commit("foreign ledger update")
			r.pushFeature()
			expectCheck(t, r.validator().GateOwnerGateStart(taskDirDemo), "Review Tail")
		})
		t.Run("delivery tail "+p, func(t *testing.T) {
			r, shas := setupMergeReady(t)
			// 先让外来账本进入历史，再把 Evidence 重新绑定到该 Commit，
			// 使 review tail 为空，从而把拒绝原因隔离到 delivery tail。
			r.write(p, "# ledger\n")
			rebindingPoint := r.commit("foreign ledger update")

			s := fullStateFromMergeReady(shas)
			s.Review.Target = rebindingPoint
			s.Owner.ReviewTarget = rebindingPoint
			s.Delivery.ReviewTarget = rebindingPoint
			r.commitState(s, "rebind evidence after ledger")
			r.pushFeature()

			expectCheck(t, r.validator().GateMergeReady(taskDirDemo), "Delivery Freshness")
		})
	}
}

// TestDangerRecoveryOnlyEvidenceFilesPasses 覆盖 DANGER 触发后经 Owner 授权恢复的正常闭环。
func TestDangerRecoveryOnlyEvidenceFilesPasses(t *testing.T) {
	r, shas := setupMergeReady(t)

	// DANGER 触发：blocked.active=true。
	s := fullStateFromMergeReady(shas)
	s.Blocked = Blocked{Active: true, By: "Owner", Reason: "danger"}
	r.commitState(s, "danger raised")
	r.pushFeature()
	expectCheck(t, r.validator().GateMergeReady(taskDirDemo), "Merge Ready")

	// Owner 授权恢复：只写入两个任务级证据文件。
	r.write(".agent/tasks/demo/loop-ledger.md", "# loop ledger\n")
	r.write(".agent/tasks/demo/danger-report.md", "# danger report\n")
	s.Blocked = Blocked{}
	r.commitState(s, "danger resolved")
	r.pushFeature()

	expectStatus(t, r.validator().GateMergeReady(taskDirDemo), StatusPass)
}

// TestDangerRecoveryWithProductionChangeRejected 证明新增白名单没有放宽 Delivery Evidence 强度：
// blocked 解除后若同时出现真实生产代码变更，旧的 Delivery Evidence 仍必须被拒绝。
func TestDangerRecoveryWithProductionChangeRejected(t *testing.T) {
	r, shas := setupMergeReady(t)

	s := fullStateFromMergeReady(shas)
	s.Blocked = Blocked{Active: true, By: "Owner", Reason: "danger"}
	r.commitState(s, "danger raised")
	r.pushFeature()

	r.write(".agent/tasks/demo/loop-ledger.md", "# loop ledger\n")
	r.write("internal/foo.go", "package foo\n// production rework\n")
	s.Blocked = Blocked{}
	r.commitState(s, "recovery with production change")
	r.pushFeature()

	expectCheck(t, r.validator().GateMergeReady(taskDirDemo), "Review Tail")
}

// TestDevelopAdvancedRejectsShortDevelopBase 证明 SHA 归一化没有削弱集成基线不变性检查。
func TestDevelopAdvancedRejectsShortDevelopBase(t *testing.T) {
	r, shas := setupMergeReady(t)
	s := fullStateFromMergeReady(shas)
	s.Delivery.DevelopBase = shas["D12"][:7] // 历史短 SHA 基线
	r.commitState(s, "short develop base evidence")
	r.pushFeature()
	expectStatus(t, r.validator().GateMergeReady(taskDirDemo), StatusPass)

	r.git("checkout", "-q", "develop")
	r.write("README.md", "# base\n// advanced\n")
	r.commit("develop advance")
	r.syncOrigin("develop")
	r.git("checkout", "-q", featureBranchName)

	expectCheck(t, r.validator().GateMergeReady(taskDirDemo), "Delivery Freshness")
}
