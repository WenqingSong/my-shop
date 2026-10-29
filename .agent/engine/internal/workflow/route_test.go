package workflow

import (
	"testing"
)

// 本文件是 V3.2.0「TaskBuilder 路线决策权」的 Engine 侧回归：
//
//	R-1  route 枚举合法；缺省 = 历史任务，不推断路线；
//	R-2  route ↔ contract.status（NORMAL 不得带独立 Contract；COMPLEX/ARCHITECT 不得 NOT_REQUIRED）；
//	R-3  route ↔ task.md `## Complexity`；
//	R-4  route ↔ Evidence（旧路线审批不得复用；结构快照不得串路线复用）。
//
// 所有夹具都在 testRepo 临时仓库内构造，不触碰正式仓库。

const routeTaskDir = ".agent/tasks/demo"

// taskMDFixture 生成一份带 `## Complexity` 的 task.md。
func taskMDFixture(complexity string, extraSections ...string) string {
	md := "# Task: demo\n\n## Goal\n交付可观察行为。\n\n## Complexity\n" + complexity + "\n\n原因：夹具。\n"
	for _, s := range extraSections {
		md += "\n" + s
	}
	return md
}

// setupRouteInit 构造 TaskBuilder 初始化现场：develop 基线 → feature 初始化 Commit → 已 push。
func setupRouteInit(t *testing.T, route Route, contract Contract, complexity string) (*testRepo, string) {
	t.Helper()
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	r.syncOrigin("develop")

	r.checkout(featureBranchName)
	r.write(routeTaskDir+"/task.md", taskMDFixture(complexity))
	s := baseState()
	s.Route = route
	s.Contract = contract
	init := r.commitState(s, "chore(task): 初始化任务 demo")
	r.pushFeature()
	return r, init
}

// approveContract 模拟 Analyst 的 A1 / A2：contract.md 内容 + APPROVED 元数据。
// A1 必须保留 init 时写入的 route，A2 只写 contract.status / target。
// 返回更新后的 State 与 A1 SHA，避免调用方用旧 State 覆盖审批事实。
func approveContract(t *testing.T, r *testRepo, s State) (State, string) {
	t.Helper()
	r.write(routeTaskDir+"/contract.md", "# contract v1\n")
	a1 := r.commit("A1 contract")
	s.Contract = Contract{Status: ContractApproved, Target: a1}
	r.writeState(s)
	r.commit("A2 approval metadata")
	r.pushFeature()
	return s, a1
}

// --- 1. NORMAL 正确初始化与交接 ---

func TestRouteNormalInitAndHandoff(t *testing.T) {
	r, init := setupRouteInit(t, RouteNormal, Contract{Status: ContractNotRequired}, RouteNormal)
	if init == "" {
		t.Fatal("初始化 commit 缺失")
	}

	rep := r.validator().CheckRoute(routeTaskDir, true)
	expectStatus(t, rep.Result, StatusPass)
	if rep.Route != RouteNormal || rep.Legacy {
		t.Fatalf("期望 route=NORMAL 非 legacy，实际 route=%q legacy=%v", rep.Route, rep.Legacy)
	}

	// NORMAL 的 Handoff 是 coder-start PASS。
	expectStatus(t, r.validator().GateCoderStart(routeTaskDir), StatusPass)
}

// --- 2. COMPLEX 正确初始化与 Contract Gate ---

func TestRouteComplexInitAndContractGate(t *testing.T) {
	r, _ := setupRouteInit(t, RouteComplex, Contract{Status: ContractPending}, RouteComplex)

	rep := r.validator().CheckRoute(routeTaskDir, true)
	expectStatus(t, rep.Result, StatusPass)
	if rep.Route != RouteComplex {
		t.Fatalf("期望 route=COMPLEX，实际 %q", rep.Route)
	}

	// COMPLEX 未获 Contract 批准前不得直接交 Coder。
	expectCheck(t, r.validator().GateCoderStart(routeTaskDir), "Contract")

	s := baseState()
	s.Route = RouteComplex
	_, _ = approveContract(t, r, s)

	expectStatus(t, r.validator().GateCoderStart(routeTaskDir), StatusPass)
	expectStatus(t, r.validator().CheckRoute(routeTaskDir, true).Result, StatusPass)
}

// --- 3. ARCHITECT 正确初始化与 Contract Gate ---

func TestRouteArchitectInitAndContractGate(t *testing.T) {
	r, _ := setupRouteInit(t, RouteArchitect, Contract{Status: ContractPending}, RouteArchitect)

	rep := r.validator().CheckRoute(routeTaskDir, true)
	expectStatus(t, rep.Result, StatusPass)
	if rep.Route != RouteArchitect {
		t.Fatalf("期望 route=ARCHITECT，实际 %q", rep.Route)
	}
	expectCheck(t, r.validator().GateCoderStart(routeTaskDir), "Contract")

	s := baseState()
	s.Route = RouteArchitect
	s, _ = approveContract(t, r, s)
	expectStatus(t, r.validator().GateCoderStart(routeTaskDir), StatusPass)

	// D1 结构快照 + D2 Review Metadata。
	r.write(routeTaskDir+"/architecture.md", "# architecture\n")
	r.write("internal/skeleton.go", "package skeleton\n")
	d1 := r.commit("D1 structural snapshot")
	s.Review = Review{Status: ReviewPending, Target: d1}
	r.writeState(s)
	r.commit("D2 review metadata")
	r.pushFeature()

	expectStatus(t, r.validator().GateCleanerStart(routeTaskDir), StatusPass)
}

// --- 4. TaskBuilder 自主选择 ARCHITECT（不依赖 Owner 指定标记）---

func TestRouteArchitectSelectedByTaskBuilder(t *testing.T) {
	// task.md 中没有任何「Owner 指定路线」的标记，Engine 也不要求这种标记；
	// 路线是 TaskBuilder 根据交付物形态写下的决策事实。
	md := taskMDFixture(RouteArchitect,
		"## Owner Constraints\n- 交付范围：模块边界与依赖方向\n- 技术限制：不得新增外部依赖\n")
	r := newTestRepo(t)
	r.write("README.md", "# base\n")
	r.commit("baseline")
	r.syncOrigin("develop")
	r.checkout(featureBranchName)
	r.write(routeTaskDir+"/task.md", md)
	s := baseState()
	s.Route = RouteArchitect
	s.Contract = Contract{Status: ContractPending}
	r.commitState(s, "task init")
	r.pushFeature()

	rep := r.validator().CheckRoute(routeTaskDir, true)
	expectStatus(t, rep.Result, StatusPass)
	if rep.Route != RouteArchitect {
		t.Fatalf("TaskBuilder 自主选择 ARCHITECT 应被接受，实际 route=%q", rep.Route)
	}
	// 路线校验通过，但未批准的 Contract 仍然阻止交 Coder：路线选择不等于免除审批。
	expectCheck(t, r.validator().GateCoderStart(routeTaskDir), "Contract")
}

// --- 5. Owner 约束与 TaskBuilder 路线选择不冲突 ---

func TestRouteOwnerConstraintsCompatible(t *testing.T) {
	cases := []struct {
		name       string
		route      Route
		contract   Contract
		complexity string
	}{
		{
			name:       "Owner 要求结构交付并记录 ARCHITECT",
			route:      RouteArchitect,
			contract:   Contract{Status: ContractPending},
			complexity: RouteArchitect,
		},
		{
			name:       "Owner 限制范围后仍是 COMPLEX",
			route:      RouteComplex,
			contract:   Contract{Status: ContractPending},
			complexity: RouteComplex,
		},
		{
			name:       "Owner 限定边界后仍是 NORMAL",
			route:      RouteNormal,
			contract:   Contract{Status: ContractNotRequired},
			complexity: RouteNormal,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := setupRouteInit(t, tt.route, tt.contract, tt.complexity)
			// Owner 约束写在 task.md 的另一章节：不参与路线事实，也不与之冲突。
			r.write(routeTaskDir+"/task.md", taskMDFixture(tt.complexity,
				"## Owner Constraints\n- 基线：integration/v3.1.0\n- 不得修改其他 Role Prompt\n- 路线要求："+tt.route+"\n"))
			r.commit("owner constraints")
			r.pushFeature()

			rep := r.validator().CheckRoute(routeTaskDir, true)
			expectStatus(t, rep.Result, StatusPass)
			if rep.Route != tt.route {
				t.Fatalf("期望 route=%s，实际 %q", tt.route, rep.Route)
			}
		})
	}
}

// --- 6. ARCHITECT + NOT_REQUIRED 必须拒绝 ---

func TestRouteArchitectNotRequiredRejected(t *testing.T) {
	r, _ := setupRouteInit(t, RouteArchitect, Contract{Status: ContractNotRequired}, RouteArchitect)

	expectCheck(t, r.validator().GateCoderStart(routeTaskDir), "Route Contract")
	expectCheck(t, r.validator().CheckRoute(routeTaskDir, true).Result, "Route Contract")
}

// COMPLEX 同样不得以 NOT_REQUIRED 绕过独立 Contract。
func TestRouteComplexNotRequiredRejected(t *testing.T) {
	r, _ := setupRouteInit(t, RouteComplex, Contract{Status: ContractNotRequired}, RouteComplex)
	expectCheck(t, r.validator().GateCoderStart(routeTaskDir), "Route Contract")
}

// NORMAL 不得被初始化为「需要独立审批但尚未完成」的任务。
func TestRouteNormalWithContractStateRejected(t *testing.T) {
	for _, status := range []ContractStatus{ContractPending, ContractApproved, ContractRejected} {
		t.Run(status, func(t *testing.T) {
			r, _ := setupRouteInit(t, RouteNormal, Contract{Status: status}, RouteNormal)
			expectCheck(t, r.validator().GateCoderStart(routeTaskDir), "Route Contract")
			expectCheck(t, r.validator().CheckRoute(routeTaskDir, true).Result, "Route Contract")
		})
	}
}

// --- 7. route 与 task.md 不一致必须拒绝 ---

func TestRouteTaskDocumentMismatchRejected(t *testing.T) {
	cases := []struct {
		name       string
		route      Route
		contract   Contract
		complexity string
	}{
		{"state COMPLEX / task.md NORMAL", RouteComplex, Contract{Status: ContractPending}, RouteNormal},
		{"state NORMAL / task.md COMPLEX", RouteNormal, Contract{Status: ContractNotRequired}, RouteComplex},
		{"保留模板占位", RouteComplex, Contract{Status: ContractPending}, "NORMAL / COMPLEX / ARCHITECT"},
		{"同时列出两条路线", RouteComplex, Contract{Status: ContractPending}, "COMPLEX / NORMAL"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := setupRouteInit(t, tt.route, tt.contract, tt.complexity)
			expectCheck(t, r.validator().CheckRoute(routeTaskDir, true).Result, "Route Task Document")
			expectCheck(t, r.validator().GateCoderStart(routeTaskDir), "Route Task Document")
		})
	}
}

// ParseComplexity 是 route ↔ task.md 判定的基础，单独覆盖常见书写形态。
func TestParseComplexity(t *testing.T) {
	cases := []struct {
		name string
		md   string
		want string
		ok   bool
	}{
		{"裸关键字", "## Complexity\nNORMAL\n\n原因：局部改动\n", RouteNormal, true},
		{"字段名写法", "## Complexity\nComplexity: COMPLEX\n", RouteComplex, true},
		{"列表与强调", "## Complexity\n- **ARCHITECT**（结构交付）\n", RouteArchitect, true},
		{"带括号理由", "## Complexity\nCOMPLEX（涉及数据模型选择）\n", RouteComplex, true},
		{"CRLF 换行", "## Complexity\r\nCOMPLEX\r\n", RouteComplex, true},
		{"模板占位", "## Complexity\nNORMAL / COMPLEX / ARCHITECT\n\n原因：……\n", "", false},
		{"只有解释句", "## Complexity\n\n原因：本项目按 ARCHITECT 交付\n", "", false},
		{"同时出现两条路线", "## Complexity\nCOMPLEX（不是 NORMAL）\n", "", false},
		{"章节缺失", "## Goal\nCOMPLEX\n", "", false},
		{"章节边界", "## Complexity\nCOMPLEX\n## Next\nNORMAL\n", RouteComplex, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseComplexity(tt.md)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("ParseComplexity = (%q,%v)，期望 (%q,%v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// --- 8. 非法路线与未授权降级 ---

func TestRouteIllegalValueRejected(t *testing.T) {
	for _, bad := range []Route{"ARCH", "complex", "FULL", "NORMAL,COMPLEX"} {
		t.Run(string(bad), func(t *testing.T) {
			r, _ := setupRouteInit(t, bad, Contract{Status: ContractPending}, RouteComplex)
			expectCheck(t, r.validator().GateCoderStart(routeTaskDir), "route enum")
			expectCheck(t, r.validator().CheckRoute(routeTaskDir, true).Result, "route enum")
		})
	}
}

// ARCHITECT → COMPLEX 的降级若只改 state.route，A1 里记录的路线仍是 ARCHITECT，
// Engine 必须拒绝复用旧审批证据（R-4a）。
func TestRouteDowngradeCannotReuseApproval(t *testing.T) {
	r, _ := setupRouteInit(t, RouteArchitect, Contract{Status: ContractPending}, RouteArchitect)
	s := baseState()
	s.Route = RouteArchitect
	var a1 string
	s, a1 = approveContract(t, r, s)
	expectStatus(t, r.validator().GateCoderStart(routeTaskDir), StatusPass)

	// 未授权降级：改 route 与 task.md，但不重新形成 Contract 审批证据。
	r.write(routeTaskDir+"/task.md", taskMDFixture(RouteComplex))
	s.Route = RouteComplex
	s.Contract = Contract{Status: ContractApproved, Target: a1}
	r.writeState(s)
	r.commit("downgrade route only")
	r.pushFeature()

	expectCheck(t, r.validator().GateCoderStart(routeTaskDir), "Route Contract Evidence")
	expectCheck(t, r.validator().CheckRoute(routeTaskDir, true).Result, "Route Contract Evidence")
}

// 结构快照（含 architecture.md）不得作为 COMPLEX / NORMAL 的 Review 证据（R-4b）。
func TestRouteStructuralSnapshotNotReusable(t *testing.T) {
	r, _ := setupRouteInit(t, RouteComplex, Contract{Status: ContractPending}, RouteComplex)
	s := baseState()
	s.Route = RouteComplex
	s, _ = approveContract(t, r, s)

	// 旧 ARCHITECT 时代的结构快照。
	r.write(routeTaskDir+"/architecture.md", "# architecture\n")
	r.write("internal/skeleton.go", "package skeleton\n")
	d1 := r.commit("structural snapshot")
	s.Review = Review{Status: ReviewPending, Target: d1}
	r.writeState(s)
	r.commit("reuse structural snapshot as pending review")
	r.pushFeature()

	expectCheck(t, r.validator().GateCleanerStart(routeTaskDir), "Route Review Evidence")
}

// 仅仅「树里含 architecture.md」不等于该快照是结构交付：合法路线变更后，
// 历史 architecture.md 仍在树中，COMPLEX 的实现快照不得因此被误拒绝。
func TestRouteComplexSnapshotInheritingArchitectureAllowed(t *testing.T) {
	r, _ := setupRouteInit(t, RouteComplex, Contract{Status: ContractPending}, RouteComplex)
	s := baseState()
	s.Route = RouteComplex
	s, _ = approveContract(t, r, s)

	// 早期 ARCHITECT 阶段留下的结构文件（路线变更后不删除）。
	r.write(routeTaskDir+"/architecture.md", "# legacy architecture\n")
	r.commit("legacy structural artifact")

	// C1 只写实现代码，没有改写 architecture.md。
	r.write("internal/impl.go", "package impl\n")
	c1 := r.commit("C1 implementation")
	s.Review = Review{Status: ReviewPending, Target: c1}
	r.writeState(s)
	r.commit("C2 review metadata")
	r.pushFeature()

	expectStatus(t, r.validator().GateCleanerStart(routeTaskDir), StatusPass)
	expectStatus(t, r.validator().CheckRoute(routeTaskDir, true).Result, StatusPass)
}

// 同一快照本身改写了 architecture.md 时仍必须拒绝：那是结构交付证据。
func TestRouteComplexSnapshotTouchingArchitectureRejected(t *testing.T) {
	r, _ := setupRouteInit(t, RouteComplex, Contract{Status: ContractPending}, RouteComplex)
	s := baseState()
	s.Route = RouteComplex
	s, _ = approveContract(t, r, s)

	r.write(routeTaskDir+"/architecture.md", "# architecture v2\n")
	r.write("internal/impl.go", "package impl\n")
	snapshot := r.commit("structural deliverable mixed into implementation")
	s.Review = Review{Status: ReviewPending, Target: snapshot}
	r.writeState(s)
	r.commit("review metadata")
	r.pushFeature()

	expectCheck(t, r.validator().GateCleanerStart(routeTaskDir), "Route Review Evidence")
}

// Owner 明确改变路线后，TaskBuilder 记录新路线并由 Analyst 重新形成 A1/A2，
// 新证据必须通过；这验证 R-4a 不会把「合法重新审批」误判为「复用旧审批」。
func TestRouteChangeWithReapprovalPasses(t *testing.T) {
	r, _ := setupRouteInit(t, RouteArchitect, Contract{Status: ContractPending}, RouteArchitect)

	// TaskBuilder 按 Owner 指令记录新路线（先改 route 与 task.md 并提交）。
	r.write(routeTaskDir+"/task.md", taskMDFixture(RouteComplex))
	changed := baseState()
	changed.Route = RouteComplex
	changed.Contract = Contract{Status: ContractPending}
	r.writeState(changed)
	r.commit("chore(task): 按 Owner 指令切换路线为 COMPLEX")
	r.pushFeature()

	// Analyst 重新形成 A1 / A2。
	reapproved := baseState()
	reapproved.Route = RouteComplex
	reapproved, _ = approveContract(t, r, reapproved)

	expectStatus(t, r.validator().GateCoderStart(routeTaskDir), StatusPass)
	expectStatus(t, r.validator().CheckRoute(routeTaskDir, true).Result, StatusPass)

	// 新的实现快照（不含 architecture.md）也必须能通过 R-4b。
	r.write("internal/impl.go", "package impl\n")
	c1 := r.commit("C1 implementation")
	reapproved.Review = Review{Status: ReviewPending, Target: c1}
	r.writeState(reapproved)
	r.commit("C2 review metadata")
	r.pushFeature()
	expectStatus(t, r.validator().GateCleanerStart(routeTaskDir), StatusPass)
}

// ARCHITECT 的 Review 证据必须是结构快照（D1 含 architecture.md）。
func TestRouteArchitectReviewRequiresStructuralSnapshot(t *testing.T) {
	r, _ := setupRouteInit(t, RouteArchitect, Contract{Status: ContractPending}, RouteArchitect)
	s := baseState()
	s.Route = RouteArchitect
	s, _ = approveContract(t, r, s)

	r.write("internal/impl.go", "package impl\n")
	c1 := r.commit("implementation without architecture.md")
	s.Review = Review{Status: ReviewPending, Target: c1}
	r.writeState(s)
	r.commit("review metadata")
	r.pushFeature()

	expectCheck(t, r.validator().GateCleanerStart(routeTaskDir), "Route Review Evidence")
}

// --- 9. 历史任务（无 route）兼容 ---

func TestRouteLegacyStateCompatible(t *testing.T) {
	t.Run("v3 无 route 的 Gate 行为不变", func(t *testing.T) {
		r, _ := setupRouteInit(t, "", Contract{Status: ContractNotRequired}, RouteNormal)
		expectStatus(t, r.validator().GateCoderStart(routeTaskDir), StatusPass)
		expectStatus(t, r.validator().GateCoderStart(routeTaskDir), StatusPass)

		// 历史任务不推断路线：严格自检必须显式声明这是 legacy。
		rep := r.validator().CheckRoute(routeTaskDir, true)
		expectCheck(t, rep.Result, "Route Authority")
		if !rep.Legacy || rep.Route != "" {
			t.Fatalf("期望 legacy=true route 为空，实际 legacy=%v route=%q", rep.Legacy, rep.Route)
		}

		allow := r.validator().CheckRoute(routeTaskDir, false)
		expectStatus(t, allow.Result, StatusPass)
		if !allow.Legacy {
			t.Fatal("允许 legacy 时应报告 legacy 事实")
		}
	})

	t.Run("v2 历史 state 仍可通过 Gate 与 legacy 自检", func(t *testing.T) {
		r := newTestRepo(t)
		r.write("README.md", "# base\n")
		r.commit("baseline")
		r.syncOrigin("develop")
		r.checkout(featureBranchName)
		r.write(routeTaskDir+"/state.yaml", `schema_version: 2
task_id: demo
contract:
  status: NOT_REQUIRED
  target: ""
resources:
  migrations: []
  error_code_domains: []
review:
  status: NOT_REQUESTED
  target: ""
owner:
  status: PENDING
  review_target: ""
delivery:
  status: NOT_RUN
  review_target: ""
  feature_head: ""
  develop_base: ""
blocked:
  active: false
  by: ""
  reason: ""
`)
		r.commit("legacy v2 state")
		r.pushFeature()

		expectStatus(t, r.validator().GateCoderStart(routeTaskDir), StatusPass)
		rep := r.validator().CheckRoute(routeTaskDir, false)
		expectStatus(t, rep.Result, StatusPass)
		if !rep.Legacy {
			t.Fatal("v2 历史 state 应报告 legacy")
		}
	})

	t.Run("无 route 时不推断 ARCHITECT 形态", func(t *testing.T) {
		// V3.1.0 的 ARCHITECT 任务没有 route 字段；结构快照同样必须继续通过 Gate。
		r := newTestRepo(t)
		r.write("README.md", "# base\n")
		r.commit("baseline")
		r.syncOrigin("develop")
		r.checkout(featureBranchName)
		r.write(routeTaskDir+"/contract.md", "# contract v1\n")
		a1 := r.commit("A1 contract")
		s := baseState()
		s.Contract = Contract{Status: ContractApproved, Target: a1}
		s.Review = Review{Status: ReviewPending, Target: a1}
		r.write(routeTaskDir+"/architecture.md", "# architecture\n")
		d1 := r.commit("D1 structural snapshot")
		s.Review = Review{Status: ReviewPending, Target: d1}
		r.commitState(s, "D2 review metadata")
		r.pushFeature()

		expectStatus(t, r.validator().GateCleanerStart(routeTaskDir), StatusPass)
	})
}

// --- 10. 原有 Gate 回归：ARCHITECT 路线带 route 的完整链路 ---

func TestRouteArchitectFullGateSequence(t *testing.T) {
	r, _ := setupRouteInit(t, RouteArchitect, Contract{Status: ContractPending}, RouteArchitect)
	developBase := r.git("rev-parse", "origin/develop")

	s := baseState()
	s.Route = RouteArchitect
	s, _ = approveContract(t, r, s)
	expectStatus(t, r.validator().GateCoderStart(routeTaskDir), StatusPass)

	// D1：结构骨架 + architecture.md；D2：PENDING。
	r.write(routeTaskDir+"/architecture.md", "# architecture\n")
	r.write("internal/skeleton.go", "package skeleton\n")
	d1 := r.commit("D1 structural snapshot")
	s.Review = Review{Status: ReviewPending, Target: d1}
	r.commitState(s, "D2 review metadata")
	r.pushFeature()
	expectStatus(t, r.validator().GateCleanerStart(routeTaskDir), StatusPass)

	// V1：独立结构验证（OwnerGate 新 Session）写入 CLEAN，target 保持 D1。
	r.write(routeTaskDir+"/findings.md", "# findings\n")
	r.write(routeTaskDir+"/core-logic.md", "# core logic\n")
	s.Review = Review{Status: ReviewClean, Target: d1}
	r.commitState(s, "V1 structural verification")
	r.pushFeature()
	expectStatus(t, r.validator().GateOwnerGateStart(routeTaskDir), StatusPass)

	// V2：Owner 决策绑定 D1。
	r.write(routeTaskDir+"/owner-decision.md", "# decision\n")
	s.Owner = Owner{Status: OwnerAccepted, ReviewTarget: d1}
	featureHead := r.commitState(s, "V2 owner acceptance")
	r.pushFeature()
	expectStatus(t, r.validator().GateDeliveryStart(routeTaskDir), StatusPass)

	// Deliverer：结构交付验收。
	r.write(routeTaskDir+"/delivery.md", "# delivery\n")
	s.Delivery = Delivery{Status: DeliveryPass, ReviewTarget: d1, FeatureHead: featureHead, DevelopBase: developBase}
	r.commitState(s, "delivery pass")
	r.pushFeature()
	expectStatus(t, r.validator().GateMergeReady(routeTaskDir), StatusPass)
}
