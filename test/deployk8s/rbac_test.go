package deployk8s

import (
	"strings"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1.3 / F-01：**权限预检必须覆盖脚本真正需要的资源**，
// 并且能区分「明确被拒绝（Forbidden）」与「无法确认（查询失败）」。
//
// 审计事实：T2-06.1.2 交付的 manifest/kubernetes/examples/deploy-lock/rbac.yaml 漏了
// core API 的 pods get/list，而脚本在发布前要按 job-name 读取 Pod、按 component 标签
// 扫描残留迁移 Pod。按示例授权的身份首次发布会在 Pod 门禁处失败。
//
// 两件事必须分开证明：
//   1) 权限预检在**迁移之前**发现缺失（退出码 3，不做任何资源变更）；
//   2) 即使绕过预检，Pod 门禁也会 fail-closed（退出码 7），不会退化成「放行」。
// 第 2 类用例在 podquery_test.go；RBAC 示例本身与脚本的一致性由 test/manifest 断言。

// 权限预检不允许在失败前创建/修改任何集群资源。
// （kubectl 的参数以 `--request-timeout=...` 开头，因此判断「资源操作」必须看子命令，
// 而不是行首；`auth can-i` 是预检自身，不在此列。）
func requireNoResourceMutations(t *testing.T, f *fixture) {
	t.Helper()
	for _, call := range f.calls() {
		if strings.Contains(call, " auth can-i ") {
			continue
		}
		for _, verb := range []string{" create ", " replace ", " apply ", " delete ", " get lease ", " get jobs", " get pods"} {
			if strings.Contains(" "+call+" ", verb) {
				t.Fatalf("权限预检失败时不得发起资源操作（%s），实际调用: %s", verb, call)
			}
		}
	}
	if lease := f.state().Lease; lease != nil {
		t.Fatalf("权限预检失败时不得创建 Lease，实际: %+v", lease)
	}
}

// TestPodsGetPermissionDeniedFailsBeforeMigration 覆盖「pods get 权限不足」：
// T2-06.1.2 的 RBAC 示例正是缺这一项。
func TestPodsGetPermissionDeniedFailsBeforeMigration(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		// 只拒绝 pods：leases/jobs 仍然有权限，证明检查是**按资源**生效的。
		st.Permissions.Denied = []string{"get pods"}
	})

	res := runDeploy(t, f)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t,
		"缺少 pods 的 get 权限",
		"Forbidden",
		"权限不足，无法建立发布互斥或核验迁移生命周期",
	)
	res.requireNotContains(t, "无法确认")
	f.requireNoEvent(t, "apply.migration")
	f.requireNoEvent(t, "apply.application")
	requireNoResourceMutations(t, f)
}

// TestPodsListPermissionDeniedFailsBeforeMigration 覆盖「pods list 权限不足」：
// 残留 Pod 扫描（component 标签）用的是 list，与 get 是两个独立动词。
func TestPodsListPermissionDeniedFailsBeforeMigration(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) { st.Permissions.Denied = []string{"list pods"} })

	res := runDeploy(t, f)

	res.requireCode(t, 3)
	res.requireContains(t, "缺少 pods 的 list 权限", "Forbidden")
	f.requireNoEvent(t, "apply.migration")
	requireNoResourceMutations(t, f)
}

// TestJobsListPermissionDeniedFailsBeforeMigration 覆盖既有要求（T2-06.1.1）：
// jobs 的 list 权限缺失同样必须在迁移之前失败（本轮不允许退化）。
func TestJobsListPermissionDeniedFailsBeforeMigration(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) { st.Permissions.Denied = []string{"list jobs"} })

	res := runDeploy(t, f)

	res.requireCode(t, 3)
	res.requireContains(t, "缺少 jobs.batch 的 list 权限", "Forbidden")
	f.requireNoEvent(t, "apply.migration")
	requireNoResourceMutations(t, f)
}

// TestPermissionPreflightDistinguishesForbiddenFromUnknown 覆盖 F-01 的判定质量：
// `auth can-i` 本身失败（SubjectAccessReview 不可用）时，不能报成「权限被拒绝」。
func TestPermissionPreflightDistinguishesForbiddenFromUnknown(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		st.AuthCanIError = `Error from server (ServiceUnavailable): the server is currently unable to handle the request`
	})

	res := runDeploy(t, f)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "无法确认当前身份", "不是", "被拒绝", "ServiceUnavailable")
	res.requireNotContains(t, "缺少 pods 的", "缺少 leases.coordination.k8s.io")
	f.requireNoEvent(t, "apply.migration")
	requireNoResourceMutations(t, f)
}

// TestPermissionPreflightCoversEveryRequiredPair 断言预检表本身覆盖了四类资源：
// 表是 F-01 契约的单一口径，缺一项就意味着门禁失去意义（静态断言在 test/manifest）。
func TestPermissionPreflightCoversEveryRequiredPair(t *testing.T) {
	t.Parallel()

	// 逐个资源单独拒绝：只有预检表里真的列了这一对，才会退出 3 并给出对应文案。
	// denied 用资源短名（fake 的 auth can-i 把 group 归一掉）；fragment 是脚本输出。
	cases := []struct {
		denied   string
		fragment string
	}{
		{denied: "get leases", fragment: "leases.coordination.k8s.io 的 get"},
		{denied: "create leases", fragment: "leases.coordination.k8s.io 的 create"},
		{denied: "update leases", fragment: "leases.coordination.k8s.io 的 update"},
		{denied: "get jobs", fragment: "jobs.batch 的 get"},
		{denied: "list jobs", fragment: "jobs.batch 的 list"},
		{denied: "get pods", fragment: "pods 的 get"},
		{denied: "list pods", fragment: "pods 的 list"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.denied, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, fixtureOptions{})
			f.updateState(func(st *fakestate.State) { st.Permissions.Denied = []string{tc.denied} })

			res := runDeploy(t, f)
			res.requireCode(t, 3)
			res.requireContains(t, "缺少 "+tc.fragment, "Forbidden")
			f.requireNoEvent(t, "apply.migration")
		})
	}
}
