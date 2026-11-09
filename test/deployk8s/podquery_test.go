package deployk8s

import (
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1.3 / F-07：**Pod 查询结果必须分成四种，错误信息不得混淆**。
//
//	1. 查询成功，Pod 不存在/空列表        → 允许
//	2. 查询成功，Pod 已全部终态           → 允许
//	3. 查询成功，存在活动 Pod             → 拒绝（措辞：「确实存在」「仍为 Running」）
//	4. 查询失败，无法确认                 → 拒绝（措辞：「无法确认」「Pod 查询失败」）
//
// 第 4 种必须 fail-closed，但**不能**把 403/500/超时说成「Pod 确实还在运行」——
// 那会让值班人员去杀 Pod，而真正的原因是权限或 API 故障。
//
// 同一套判定覆盖三个入口：
//   - migration_pod_gate（按 job-name 读取某个 Job 的 Pod）；
//   - scan_migration_pods（按 component 标签扫描残留 Pod）；
//   - verify_previous_migration（接管前的核验，包含上面两条路径）。

// seedTerminalRecordWithPods 构造「上一轮 COMPLETE + Job 仍在 + 指定 Pod 集合」的现场。
func (f *fixture) seedTerminalRecordWithPods(pods []fakestate.Pod) {
	f.t.Helper()
	job := "surgecart-migrate-" + f.shas["c1"]
	uid := "job-uid-previous-round"
	f.seedExpiredLease("MIGRATION_COMPLETE", job, uid)
	f.updateState(func(st *fakestate.State) {
		previous := fakestate.Job{Name: job, UID: uid, Exists: true, Complete: true, Pods: pods}
		st.Job = previous
		st.Jobs = []fakestate.Job{previous}
		st.JobPods = map[string][]fakestate.Pod{job: pods}
	})
}

// TestPodGateMatrixForTerminalRecord 覆盖「按 job-name 读取 Pod」的七种结果。
func TestPodGateMatrixForTerminalRecord(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		pods []fakestate.Pod
		// inject 在租赁过期现场上追加故障注入。
		inject func(*fakestate.State)
		// wantCode 0 表示允许接管并完成发布。
		wantCode int
		// mustContain / mustNotContain 检查措辞是否与事实一致。
		mustContain    []string
		mustNotContain []string
	}{
		{
			name:        "查询成功且返回空列表",
			pods:        nil,
			wantCode:    0,
			mustContain: []string{"上一轮 Job/"},
		},
		{
			name:        "查询成功且 Pod 全部终态",
			pods:        []fakestate.Pod{{Name: "migrate-pod-done", Phase: "Succeeded"}},
			wantCode:    0,
			mustContain: []string{"且 Pod 已终止：允许接管"},
		},
		{
			name:        "查询成功且存在活动 Pod",
			pods:        []fakestate.Pod{{Name: "migrate-pod-running", Phase: "Running"}},
			wantCode:    7,
			mustContain: []string{"Pod 未确认终止", "仍为 Running", "migrate-pod-running"},
			// 观察到活动 Pod 时不能说「无法确认」。
			mustNotContain: []string{"无法确认 Pod 状态"},
		},
		{
			name:           "403 Forbidden（pods 权限不足）",
			pods:           []fakestate.Pod{{Name: "migrate-pod-running", Phase: "Running"}},
			inject:         func(st *fakestate.State) { st.PodsGetForbidden = 1 },
			wantCode:       7,
			mustContain:    []string{"无法确认 Pod 状态", "Pod 查询失败", "Forbidden"},
			mustNotContain: []string{"Pod 未确认终止", "仍为 Running", "确实存在"},
		},
		{
			name:           "API Server 500",
			pods:           []fakestate.Pod{{Name: "migrate-pod-running", Phase: "Running"}},
			inject:         func(st *fakestate.State) { st.PodsGetFailures = 1 },
			wantCode:       7,
			mustContain:    []string{"无法确认 Pod 状态", "Pod 查询失败", "InternalError"},
			mustNotContain: []string{"Pod 未确认终止", "仍为 Running"},
		},
		{
			name:           "请求超时",
			pods:           []fakestate.Pod{{Name: "migrate-pod-running", Phase: "Running"}},
			inject:         func(st *fakestate.State) { st.PodsGetTimeouts = 1 },
			wantCode:       7,
			mustContain:    []string{"无法确认 Pod 状态", "Pod 查询失败", "Timeout"},
			mustNotContain: []string{"Pod 未确认终止", "仍为 Running"},
		},
		{
			name:           "Pod 不属于该 Job（归属无法证明）",
			pods:           []fakestate.Pod{{Name: "migrate-pod-foreign", Phase: "Failed", OwnerJobUID: "job-uid-other"}},
			wantCode:       7,
			mustContain:    []string{"无法确认 Pod 状态", "无法确认 Pod 归属", "不一致"},
			mustNotContain: []string{"Pod 未确认终止", "确实存在"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, fixtureOptions{})
			f.seedTerminalRecordWithPods(tc.pods)
			if tc.inject != nil {
				f.updateState(tc.inject)
			}
			// 上一轮 Job 的名字/uid 已在 seed 里；本轮迁移由 scriptNewMigration 提供。
			f.scriptNewMigration(fakestate.Job{
				Name: "surgecart-migrate-" + f.shas["c1"], UID: "job-uid-previous-round",
				Exists: true, Complete: true, Pods: tc.pods,
			}, []fakestate.Job{{
				Name: "surgecart-migrate-" + f.shas["c1"], UID: "job-uid-previous-round",
				Exists: true, Complete: true,
			}})

			res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10", "--rollout-timeout", "10")

			res.requireCode(t, tc.wantCode)
			res.requireContains(t, tc.mustContain...)
			res.requireNotContains(t, tc.mustNotContain...)
			if tc.wantCode != 0 {
				res.requireNoSuccess(t)
				f.requireNoEvent(t, "apply.migration")
				f.requireNoEvent(t, "apply.application")
			}
		})
	}
}

// TestMigrationPodScanMatrix 覆盖「按 component 标签扫描残留 Pod」的四种结果
// （上一轮没有迁移记录，因此走 scan_migration_pods 这条路径）。
func TestMigrationPodScanMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		pods           []fakestate.Pod
		inject         func(*fakestate.State)
		wantCode       int
		mustContain    []string
		mustNotContain []string
	}{
		{
			name:     "查询成功且没有残留 Pod",
			wantCode: 0,
		},
		{
			name:        "查询成功且存在活动 Pod",
			pods:        []fakestate.Pod{{Name: "orphan-pod", Phase: "Running", OwnerJobUID: "job-uid-old"}},
			wantCode:    7,
			mustContain: []string{"**确实存在**仍在执行/终止中的迁移 Pod", "orphan-pod"},
			// 观察到活动 Pod 时不能说「无法确认」。
			mustNotContain: []string{"无法确认迁移 Pod 状态", "无法确认**迁移 Pod 状态"},
		},
		{
			name:           "403 Forbidden",
			inject:         func(st *fakestate.State) { st.MigrationPodsGetForbidden = 1 },
			wantCode:       7,
			mustContain:    []string{"**无法确认**迁移 Pod 状态", "Forbidden", "不得据此认为 Pod 已终止"},
			mustNotContain: []string{"**确实存在**"},
		},
		{
			name:           "API Server 500",
			inject:         func(st *fakestate.State) { st.MigrationPodsGetFailures = 1 },
			wantCode:       7,
			mustContain:    []string{"**无法确认**迁移 Pod 状态", "InternalError"},
			mustNotContain: []string{"**确实存在**"},
		},
		{
			name:           "请求超时",
			inject:         func(st *fakestate.State) { st.MigrationPodsGetTimeouts = 1 },
			wantCode:       7,
			mustContain:    []string{"**无法确认**迁移 Pod 状态", "Timeout"},
			mustNotContain: []string{"**确实存在**"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, fixtureOptions{})
			// 上一轮没有任何迁移记录（升级前的老 Runner / 注解丢失）。
			f.seedExpiredLease("NO_MIGRATION", "", "")
			if len(tc.pods) > 0 {
				f.updateState(func(st *fakestate.State) { st.MigrationPods = tc.pods })
			}
			if tc.inject != nil {
				f.updateState(tc.inject)
			}
			slowMigration(f)

			res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10", "--rollout-timeout", "10")

			res.requireCode(t, tc.wantCode)
			res.requireContains(t, tc.mustContain...)
			res.requireNotContains(t, tc.mustNotContain...)
			if tc.wantCode != 0 {
				res.requireNoSuccess(t)
				f.requireNoEvent(t, "apply.migration")
				f.requireNoEvent(t, "apply.application")
				// 拒绝接管时不得改动持有者。
				if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "deploy-previous-runner-7777" {
					t.Fatalf("拒绝接管时不得改动持有者，实际: %+v", lease)
				}
			}
		})
	}
}

// TestOwnMigrationPodQueryFailureExits7 覆盖「本轮 Job 已 Failed，但该 Job 的 Pod
// 查询失败」：必须是「无法确认」，并且不放锁、不更新应用。
func TestOwnMigrationPodQueryFailureExits7(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	jobName := "surgecart-migrate-" + f.shas["target"]
	uid := "job-uid-" + f.shas["target"]
	failed := failedMigrationJob(jobName, uid, nil)
	f.updateState(func(st *fakestate.State) {
		st.Job = failed
		st.JobScript = []fakestate.Job{failed}
		st.PodsGetForbidden = 1
	})

	res := runDeploy(t, f, "--migration-timeout", "10")

	res.requireCode(t, 7)
	res.requireNoSuccess(t)
	res.requireContains(t, "无法确认 Pod 状态", "Pod 查询失败", "Forbidden")
	res.requireNotContains(t, "仍为 Running", "**确实存在**")
	f.requireNoEvent(t, "apply.application")
	f.requireNoEvent(t, "lease.release")
}
