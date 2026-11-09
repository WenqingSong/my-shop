package deployk8s

import (
	"strings"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1.2 / AUD-D3 与 AUD-D5-D：
// **Job Failed=True 不等于 Pod 已经停止执行**。
//
// 每个用例都断言四件事（任务要求的可观察结果）：
//   1) 最终退出码；
//   2) 是否创建新 Job（apply.migration 事件）；
//   3) 是否执行 Application apply（apply.application 事件）；
//   4) Lease 最终状态（持有者 + 迁移记录 + terminal-at）。

// failedMigrationJob 返回「上一轮 Failed」的 Job 快照。
func failedMigrationJob(name, uid string, pods []fakestate.Pod) fakestate.Job {
	return fakestate.Job{
		Name:    name,
		UID:     uid,
		Exists:  true,
		Failed:  true,
		Reason:  "BackoffLimitExceeded",
		Message: "Job has reached the specified backoff limit",
		Pods:    pods,
	}
}

// TestFailedJobPodGateMatrix 覆盖 AUD-D5-D：Failed Job + 各种 Pod 状态。
func TestFailedJobPodGateMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		pods        []fakestate.Pod
		podFailures int
		wantCode    int
		wantApply   bool
		wantMessage string
	}{
		{
			name:        "Running Pod",
			pods:        []fakestate.Pod{{Name: "migrate-pod-running", Phase: "Running"}},
			wantCode:    7,
			wantMessage: "仍为 Running",
		},
		{
			name:        "Pending Pod",
			pods:        []fakestate.Pod{{Name: "migrate-pod-pending", Phase: "Pending"}},
			wantCode:    7,
			wantMessage: "仍为 Pending",
		},
		{
			name:        "Terminating Pod",
			pods:        []fakestate.Pod{{Name: "migrate-pod-terminating", Phase: "Running", Deleting: true}},
			wantCode:    7,
			wantMessage: "正在终止",
		},
		{
			name:      "Succeeded Pod",
			pods:      []fakestate.Pod{{Name: "migrate-pod-succeeded", Phase: "Succeeded"}},
			wantCode:  0,
			wantApply: true,
		},
		{
			name:      "Failed Pod",
			pods:      []fakestate.Pod{{Name: "migrate-pod-failed", Phase: "Failed"}},
			wantCode:  0,
			wantApply: true,
		},
		{
			name:        "Pod 查询失败",
			pods:        []fakestate.Pod{{Name: "migrate-pod-unknown", Phase: "Failed"}},
			podFailures: 1,
			wantCode:    7,
			wantMessage: "Pod 查询失败",
		},
		{
			name: "Pod 的 ownerReferences 指向另一个 Job",
			pods: []fakestate.Pod{{
				Name: "migrate-pod-foreign", Phase: "Failed", OwnerJobUID: "job-uid-somebody-else",
			}},
			wantCode:    7,
			wantMessage: "不一致",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, fixtureOptions{})
			jobName := "surgecart-migrate-" + f.shas["target"]
			uid := "job-uid-" + f.shas["target"]
			failed := failedMigrationJob(jobName, uid, tc.pods)
			f.seedExpiredLease("MIGRATION_FAILED", jobName, uid)
			f.updateState(func(st *fakestate.State) {
				st.Job = failed
				st.JobPods = map[string][]fakestate.Pod{jobName: tc.pods}
				if tc.podFailures > 0 {
					st.PodsGetFailures = tc.podFailures
				}
			})
			f.scriptNewMigration(failed, []fakestate.Job{failed})

			res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10", "--rollout-timeout", "10")

			res.requireCode(t, tc.wantCode)
			if tc.wantCode != 0 {
				res.requireNoSuccess(t)
				res.requireContains(t, tc.wantMessage)
				if got := f.countEvents("apply.migration"); got != 0 {
					t.Fatalf("Pod 未确认终止时不得创建新 Job，实际 apply.migration=%d", got)
				}
				f.requireNoEvent(t, "apply.application")
				f.requireNoEvent(t, "lease.acquire")
				lease := f.state().Lease
				if lease == nil || lease.HolderIdentity != "deploy-previous-runner-7777" {
					t.Fatalf("拒绝接管时不得改动持有者，实际: %+v", lease)
				}
				state, job, gotUID := f.leaseRecord()
				if state != "MIGRATION_FAILED" || job != jobName || gotUID != uid {
					t.Fatalf("拒绝接管时不得改写记录，实际 state=%q job=%q uid=%q", state, job, gotUID)
				}
				return
			}

			// 允许重试：接管后重新创建迁移 Job 并完成发布。
			res.requireSuccess(t)
			if got := f.countEvents("apply.migration"); got != 1 {
				t.Fatalf("允许重试时必须恰好创建一次迁移 Job，实际 %d", got)
			}
			if got := f.countEvents("apply.application"); got != 1 {
				t.Fatalf("迁移确认成功后必须 apply 一次 Application，实际 %d", got)
			}
			anns := f.leaseAnnotations()
			if anns[annPrevState] != "MIGRATION_FAILED" {
				t.Fatalf("上一轮 FAILED 记录必须归档，实际 prev=%q", anns[annPrevState])
			}
			if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "" {
				t.Fatalf("发布结束后锁必须已释放，实际: %+v", lease)
			}
		})
	}
}

// TestFailedJobPodBecomesTerminalAllowsRetry 覆盖 AUD-D5-D8「Pod 全部终态后允许重试」：
// 同一现场先被拒绝，Pod 终态后再跑一次即可正常发布。
func TestFailedJobPodBecomesTerminalAllowsRetry(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	jobName := "surgecart-migrate-" + f.shas["target"]
	uid := "job-uid-" + f.shas["target"]
	running := []fakestate.Pod{{Name: "migrate-pod-running", Phase: "Running"}}
	failed := failedMigrationJob(jobName, uid, running)
	f.seedExpiredLease("MIGRATION_FAILED", jobName, uid)
	f.updateState(func(st *fakestate.State) {
		st.Job = failed
		st.JobPods = map[string][]fakestate.Pod{jobName: running}
	})
	f.scriptNewMigration(failed, []fakestate.Job{failed})

	blocked := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")
	blocked.requireCode(t, 7)
	blocked.requireContains(t, "Pod 未确认终止", "不得据此假定 DDL 已经停止")
	if got := f.countEvents("apply.migration"); got != 0 {
		t.Fatalf("Pod 仍在运行时不得创建新 Job，实际 %d", got)
	}

	// 时间推进：Pod 进入终态。锁仍是上一轮持有者（未被改动），再次过期。
	terminal := []fakestate.Pod{{Name: "migrate-pod-running", Phase: "Failed"}}
	f.updateState(func(st *fakestate.State) {
		failed.Pods = terminal
		st.Job = failed
		st.JobPods = map[string][]fakestate.Pod{jobName: terminal}
		st.PodsGetFailures = 0
	})
	f.scriptNewMigration(failed, []fakestate.Job{failed})

	allowed := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10", "--rollout-timeout", "10")
	allowed.requireSuccess(t)
	if got := f.countEvents("apply.migration"); got != 1 {
		t.Fatalf("Pod 终态后应允许一次新迁移，实际 %d", got)
	}
	if got := f.countEvents("apply.application"); got != 1 {
		t.Fatalf("Pod 终态后应允许一次 Application apply，实际 %d", got)
	}
}

// TestOwnMigrationFailedWithRunningPodExits7 覆盖 AUD-D3 的本轮路径：
// 本轮 Job Failed=True 但 Pod 仍在 Running → 不更新应用、不放锁、记录不声称终态。
func TestOwnMigrationFailedWithRunningPodExits7(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	jobName := "surgecart-migrate-" + f.shas["target"]
	uid := "job-uid-" + f.shas["target"]
	failed := failedMigrationJob(jobName, uid, []fakestate.Pod{{Name: "migrate-pod-running", Phase: "Running"}})
	f.updateState(func(st *fakestate.State) {
		st.Job = failed
		st.JobPods = map[string][]fakestate.Pod{jobName: failed.Pods}
		st.JobScript = []fakestate.Job{failed}
	})

	res := runDeploy(t, f, "--migration-timeout", "10")

	res.requireCode(t, 7)
	res.requireNoSuccess(t)
	res.requireContains(t,
		"迁移生命周期未确认",
		"不等价于",
		"不创建新 Job",
		"migrate-pod-running",
		"Running",
	)
	f.requireNoEvent(t, "apply.application")
	f.requireNoEvent(t, "lease.release")

	// 记录：state=FAILED 但 terminal-at 必须为空（没有确认终结）。
	anns := f.leaseAnnotations()
	if anns[annMigrationState] != "MIGRATION_FAILED" {
		t.Fatalf("应记录 Job 的 Failed 事实，实际 %q", anns[annMigrationState])
	}
	if anns[annMigrationTerminalAt] != "" {
		t.Fatalf("Pod 未终止时不得写 terminal-at，实际 %q", anns[annMigrationTerminalAt])
	}
	if !strings.Contains(anns[annMigrationDetail], "Pod") {
		t.Fatalf("detail 必须记录 Pod 门禁结论，实际 %q", anns[annMigrationDetail])
	}
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity == "" {
		t.Fatalf("迁移未确认终止时不得释放锁，实际: %+v", lease)
	}
	for _, call := range f.calls() {
		if strings.HasPrefix(call, "delete ") {
			t.Fatalf("不得自动删除 Job/Pod，实际调用: %s", call)
		}
		if strings.Contains(call, "migrate force") {
			t.Fatalf("不得调用 migrate force，实际调用: %s", call)
		}
	}
}
