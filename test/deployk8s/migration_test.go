package deployk8s

import (
	"strings"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1-04 / T2-06.1.1-AUD-04：
//   - Migration Failed 与 Timeout 分离；
//   - 达到脚本等待上限后必须**重新读取** Job 状态再判定（不允许 Job 已 Complete 却报超时）；
//   - 「迁移未成功时 Application 绝不更新」这条不变量。

// TestMigrationCompleteProceedsToApplication 覆盖 Job Complete：应用才会被更新。
func TestMigrationCompleteProceedsToApplication(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	f.requireEventOrder(t, "apply.migration", "apply.application")
}

// TestMigrationJobNeverAppearsFailsClosed 覆盖「apply 之后 Job 始终不可见」：
// 状态不一致时不得继续等待到超时再猜，必须 fail-closed（退出码 7），
// 并保留锁与关联记录。
func TestMigrationJobNeverAppearsFailsClosed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		st.Job = fakestate.Job{Exists: false}
		st.JobScript = []fakestate.Job{{Exists: false}}
	})

	res := runDeploy(t, f, "--migration-timeout", "8")

	res.requireCode(t, 7)
	res.requireNoSuccess(t)
	res.requireContains(t, "不可见", "fail-closed")
	f.requireNoEvent(t, "apply.application")

	state, job, _ := f.leaseRecord()
	if state != "MIGRATION_PENDING" || job == "" {
		t.Fatalf("生命周期记录必须保留（state=MIGRATION_PENDING + job 名），实际 state=%q job=%q", state, job)
	}
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity == "" {
		t.Fatalf("迁移未确认终态时不得释放发布锁，实际: %+v", lease)
	}
}

// TestMigrationFailedBlocksApplicationApply 覆盖 Job Failed=True：
// 退出码 2、打印日志与 Pod 状态、应用不更新、且脚本不删除 Job。
func TestMigrationFailedBlocksApplicationApply(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		failed := fakestate.Job{
			Name:    "surgecart-migrate-" + f.shas["target"],
			UID:     "job-uid-" + f.shas["target"],
			Exists:  true,
			Failed:  true,
			Reason:  "BackoffLimitExceeded",
			Message: "Job has reached the specified backoff limit",
			Logs:    "migrate: ERROR 1064 (42000): You have an error in your SQL syntax\n",
			Pods: []fakestate.Pod{{
				Name: "surgecart-migrate-abc1234-x7k2p", Phase: "Failed",
			}},
		}
		st.Job = failed
		st.JobScript = []fakestate.Job{failed}
	})

	res := runDeploy(t, f, "--migration-timeout", "10")

	res.requireCode(t, 2)
	res.requireNoSuccess(t)
	res.requireContains(t,
		"迁移失败",
		"BackoffLimitExceeded",
		"ERROR 1064",
		"不要用 migrate force 绕过",
		"脚本不会自动 force，也不会自动删除 Job",
		"state=MIGRATION_FAILED",
	)
	f.requireNoEvent(t, "apply.application")

	for _, call := range f.calls() {
		if strings.HasPrefix(call, "delete ") {
			t.Fatalf("迁移失败时脚本不得自动删除 Job，实际调用: %s", call)
		}
	}

	// Failed 是终态：锁可以释放，但失败证据必须保留在注解上。
	f.requireEvent(t, "lease.release")
	state, job, uid := f.leaseRecord()
	if state != "MIGRATION_FAILED" || job == "" || uid == "" {
		t.Fatalf("失败证据必须保留（state/job/uid），实际 state=%q job=%q uid=%q", state, job, uid)
	}
}

// TestMigrationRunningThenComplete 覆盖 Job 仍在运行：持续等待，完成后才更新应用。
func TestMigrationRunningThenComplete(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		st.JobScript = []fakestate.Job{
			{Exists: true},
			{Exists: true},
			{Exists: true, Complete: true},
		}
	})

	res := runDeploy(t, f, "--migration-timeout", "30", "--rollout-timeout", "10")

	res.requireSuccess(t)
	if got := f.state().JobGets; got < 3 {
		t.Fatalf("迁移等待只读取了 %d 次 Job 状态，期望 >= 3（运行中必须继续等待）", got)
	}
	state, _, uid := f.leaseRecord()
	if state != "MIGRATION_COMPLETE" || uid == "" {
		t.Fatalf("完成后必须记录 MIGRATION_COMPLETE 与 Job uid，实际 state=%q uid=%q", state, uid)
	}
}

// TestMigrationTimeoutDoesNotClaimFailure 覆盖「等待超时」：
// 明确不是失败、给出继续等待与检查命令、应用不更新、不为超时输出失败恢复提示。
func TestMigrationTimeoutDoesNotClaimFailure(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		running := fakestate.Job{
			Name:   "surgecart-migrate-" + f.shas["target"],
			UID:    "job-uid-" + f.shas["target"],
			Exists: true,
			Logs:   "migrate: applying 20261107_add_index ...\n",
			Pods: []fakestate.Pod{{
				Name: "surgecart-migrate-abc1234-p9x2z", Phase: "Running",
			}},
		}
		st.Job = running
		st.JobScript = []fakestate.Job{running}
	})

	res := runDeploy(t, f, "--migration-timeout", "6")

	res.requireCode(t, 6)
	res.requireNoSuccess(t)
	res.requireContains(t,
		"迁移等待超时",
		"超时不等同于失败",
		"禁止删除 Job",
		"wait --for=condition=complete",
		"发布锁**未释放**",
	)
	// 超时不是失败：不得输出「迁移失败」的措辞，也不得给出「删除 Job 重跑」的失败恢复指引。
	res.requireNotContains(t, "迁移失败：Job/", "delete job")
	f.requireNoEvent(t, "apply.application")
}

// TestMigrationTimeoutWithTerminatingPod 覆盖「Pod 正在终止」：
// 仍然禁止删除 Job（DDL 可能未结束），并明确输出禁止项。
func TestMigrationTimeoutWithTerminatingPod(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		terminating := fakestate.Job{
			Name:   "surgecart-migrate-" + f.shas["target"],
			UID:    "job-uid-" + f.shas["target"],
			Exists: true,
			Pods: []fakestate.Pod{{
				Name: "surgecart-migrate-abc1234-q1w2e", Phase: "Running", Deleting: true,
			}},
		}
		st.Job = terminating
		st.JobScript = []fakestate.Job{terminating}
	})

	res := runDeploy(t, f, "--migration-timeout", "6")

	res.requireCode(t, 6)
	res.requireContains(t, "禁止删除 Job", "仍在运行/终止中")
	f.requireNoEvent(t, "apply.application")
}

// TestMigrationTimeoutWithoutPodsWarnsBeforeDeletion 覆盖「没有 Pod 但 Job 未收敛」：
// 仍要求先确认数据库侧状态，而不是直接删 Job。
func TestMigrationTimeoutWithoutPodsWarnsBeforeDeletion(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		pending := fakestate.Job{
			Name:   "surgecart-migrate-" + f.shas["target"],
			UID:    "job-uid-" + f.shas["target"],
			Exists: true,
			Reason: "PodInitializing",
		}
		st.Job = pending
		st.JobScript = []fakestate.Job{pending}
	})

	res := runDeploy(t, f, "--migration-timeout", "6")

	res.requireCode(t, 6)
	res.requireContains(t, "请先确认数据库侧是否有残留 DDL")
	res.requireNoSuccess(t)
	f.requireNoEvent(t, "apply.application")
}

// TestMigrationTimeoutThenCompleteContinues 覆盖 AUD-04：等待上限到达时 Job 恰好已 Complete。
// 脚本必须重新读取 Job 并继续发布，不能报告超时（旧实现会在这里错误地返回 6）。
func TestMigrationTimeoutThenCompleteContinues(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		// 1 次 apply 后确认对象存在 + 4 次等待轮询都是「运行中」，第 6 次（二次判定）才 Complete。
		st.JobScript = []fakestate.Job{
			{Exists: true},
			{Exists: true},
			{Exists: true},
			{Exists: true},
			{Exists: true},
			{Exists: true, Complete: true},
		}
	})

	res := runDeploy(t, f, "--migration-timeout", "6", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "二次判定", "已 Complete", "超时不等于失败")
	res.requireNotContains(t, "迁移等待超时（")
	f.requireEventOrder(t, "apply.migration", "apply.application")
}

// TestMigrationTimeoutThenFailedExits2 覆盖 AUD-04：等待上限到达时 Job 已 Failed=True。
// 必须走失败分支（退出码 2），而不是超时分支。
func TestMigrationTimeoutThenFailedExits2(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		failed := fakestate.Job{
			Name:   "surgecart-migrate-" + f.shas["target"],
			Exists: true, Failed: true, Reason: "DeadlineExceeded",
		}
		st.Job = failed
		st.JobScript = []fakestate.Job{
			{Exists: true},
			{Exists: true},
			{Exists: true},
			{Exists: true},
			{Exists: true},
			failed,
		}
	})

	res := runDeploy(t, f, "--migration-timeout", "6")

	res.requireCode(t, 2)
	res.requireContains(t, "迁移失败", "DeadlineExceeded")
	f.requireNoEvent(t, "apply.application")
}

// TestMigrationTimeoutThenQueryFailureExits6 覆盖 AUD-04：
// 等待上限到达后重新读取失败 → 状态未知 → 退出码 6，不得假定 Job 已结束。
func TestMigrationTimeoutThenQueryFailureExits6(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		running := fakestate.Job{
			Name:   "surgecart-migrate-" + f.shas["target"],
			UID:    "job-uid-" + f.shas["target"],
			Exists: true,
		}
		st.Job = running
		st.JobScript = []fakestate.Job{running}
		// 1 次「确认对象存在」+ 4 次等待轮询成功，第 6 次（二次判定）读取失败。
		st.JobGetFailAt = 6
	})

	res := runDeploy(t, f, "--migration-timeout", "6")

	res.requireCode(t, 6)
	res.requireContains(t, "状态未知", "按超时处理")
	f.requireNoEvent(t, "apply.application")
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity == "" {
		t.Fatalf("状态未知时不得释放发布锁，实际: %+v", lease)
	}
}

// TestMigrationJobVanishesDuringWaitFailsClosed 覆盖「等待过程中 Job 消失」：
// 记录指向一个已经不存在的 Job，必须 fail-closed，而不是当成「迁移已完成」。
func TestMigrationJobVanishesDuringWaitFailsClosed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		st.JobScript = []fakestate.Job{
			{Exists: true},
			{Exists: false},
		}
	})

	res := runDeploy(t, f, "--migration-timeout", "20")

	res.requireCode(t, 7)
	res.requireContains(t, "在等待过程中消失", "fail-closed")
	f.requireNoEvent(t, "apply.application")
	state, _, _ := f.leaseRecord()
	if state == "MIGRATION_COMPLETE" {
		t.Fatalf("Job 消失时不得把状态写成 MIGRATION_COMPLETE")
	}
}

// TestMigrationTimeoutMustExceedJobDeadline 覆盖 AUD-04/06：
// 脚本等待上限必须大于 Job 自己的 activeDeadlineSeconds，否则「脚本超时」与
// 「Job 被 kubelet 终止」会同时发生，结果取决于 API 传播速度。
func TestMigrationTimeoutMustExceedJobDeadline(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.setJobDeadline(900)

	res := runDeploy(t, f, "--migration-timeout", "900")

	res.requireCode(t, 1)
	res.requireNoSuccess(t)
	res.requireContains(t, "--migration-timeout (900s) 必须大于 Job activeDeadlineSeconds (900s)", "边界竞态")
	f.requireNoEvent(t, "lease.acquire")
	f.requireNoEvent(t, "apply.migration")
}

// TestMigrationTimeoutMarginSatisfied 覆盖 AUD-04/06 的正向关系：
// 900 + 180 = 1080，脚本记录「Job deadline + 余量 = 脚本上限」。
func TestMigrationTimeoutMarginSatisfied(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.setJobDeadline(900)

	res := runDeploy(t, f, "--migration-timeout", "1080", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "Job activeDeadlineSeconds=900s + 余量 180s = 脚本上限 1080s")
}

// TestMigrationTimeoutMarginTooSmallWarns 覆盖 AUD-04/06：
// 用户自定义上限虽然大于 deadline，但余量不足时必须明确告警（不静默）。
func TestMigrationTimeoutMarginTooSmallWarns(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.setJobDeadline(900)

	res := runDeploy(t, f, "--migration-timeout", "901", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "迁移等待余量 1s 小于建议的 180s")
}
