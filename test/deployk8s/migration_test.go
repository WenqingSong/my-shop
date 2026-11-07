package deployk8s

import (
	"strings"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1-04：Migration Failed 与 Timeout 分离，以及
// 「迁移未成功时 Application 绝不更新」这条不变量。

// TestMigrationCompleteProceedsToApplication 覆盖 Job Complete：应用才会被更新。
func TestMigrationCompleteProceedsToApplication(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	f.requireEventOrder(t, "apply.migration", "apply.application")
}

// TestMigrationJobMissingKeepsApplication 覆盖「Job 不存在」：
// apply 之后读不到 Job 时不猜测成功，等待到超时并按「未确认」退出。
func TestMigrationJobMissingKeepsApplication(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		st.JobScript = []fakestate.Job{{Exists: false}}
	})

	res := runDeploy(t, f, "--migration-timeout", "1")

	res.requireCode(t, 6)
	res.requireNoSuccess(t)
	res.requireContains(t, "迁移等待超时", "超时不等同于失败")
	f.requireNoEvent(t, "apply.application")
}

// TestMigrationFailedBlocksApplicationApply 覆盖 Job Failed=True：
// 退出码 2、打印日志与 Pod 状态、应用不更新、且脚本不删除 Job。
func TestMigrationFailedBlocksApplicationApply(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		failed := fakestate.Job{
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

	res := runDeploy(t, f, "--migration-timeout", "5")

	res.requireCode(t, 2)
	res.requireNoSuccess(t)
	res.requireContains(t,
		"迁移失败",
		"BackoffLimitExceeded",
		"ERROR 1064",
		"不要用 migrate force 绕过",
		"脚本不会自动 force，也不会自动删除 Job",
	)
	f.requireNoEvent(t, "apply.application")

	for _, call := range f.calls() {
		if strings.HasPrefix(call, "delete ") {
			t.Fatalf("迁移失败时脚本不得自动删除 Job，实际调用: %s", call)
		}
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
}

// TestMigrationTimeoutDoesNotClaimFailure 覆盖「等待超时」：
// 明确不是失败、给出继续等待与检查命令、应用不更新、不为超时输出失败恢复提示。
func TestMigrationTimeoutDoesNotClaimFailure(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		running := fakestate.Job{
			Exists: true,
			Logs:   "migrate: applying 20261107_add_index ...\n",
			Pods: []fakestate.Pod{{
				Name: "surgecart-migrate-abc1234-p9x2z", Phase: "Running",
			}},
		}
		st.Job = running
		st.JobScript = []fakestate.Job{running}
	})

	res := runDeploy(t, f, "--migration-timeout", "1")

	res.requireCode(t, 6)
	res.requireNoSuccess(t)
	res.requireContains(t,
		"迁移等待超时",
		"超时不等同于失败",
		"禁止删除 Job",
		"wait --for=condition=complete",
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
			Exists: true,
			Pods: []fakestate.Pod{{
				Name: "surgecart-migrate-abc1234-q1w2e", Phase: "Running", Deleting: true,
			}},
		}
		st.Job = terminating
		st.JobScript = []fakestate.Job{terminating}
	})

	res := runDeploy(t, f, "--migration-timeout", "1")

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
			Exists: true,
			Reason: "PodInitializing",
		}
		st.Job = pending
		st.JobScript = []fakestate.Job{pending}
	})

	res := runDeploy(t, f, "--migration-timeout", "1")

	res.requireCode(t, 6)
	res.requireContains(t, "请先确认数据库侧是否有残留 DDL")
	res.requireNoSuccess(t)
	f.requireNoEvent(t, "apply.application")
}
