package deployk8s

import (
	"strings"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1.1 / AUD-01：**Migration Job 生命周期与 Lease 生命周期绑定**。
//
// 关键不变量：只要上一轮迁移 Job 尚未进入终态，任何 Runner 都不能接管发布锁并开始
// 新一轮发布。注解记录只是「上一轮 Runner 的陈述」，接管方必须用真实 Job 对象核验。
//
// 断言对象是脚本行为：真实执行 deploy-k8s.sh，观察 fake kubectl 记录的事件、
// Lease 注解与退出码。

// runningMigration 构造「本轮迁移仍在运行」的 Job 快照（名字与 uid 与 fixture 一致）。
func runningMigration(f *fixture) fakestate.Job {
	tag := f.shas["target"]
	return fakestate.Job{
		Name:   "surgecart-migrate-" + tag,
		UID:    "job-uid-" + tag,
		Exists: true,
		Pods: []fakestate.Pod{{
			Name: "surgecart-migrate-abc1234-p9x2z", Phase: "Running",
		}},
	}
}

// expireLease 让锁在「ago」之前就停止续租，模拟持有者崩溃/退出后租期已过。
func (f *fixture) expireLease(ago time.Duration) {
	f.t.Helper()
	f.updateState(func(st *fakestate.State) {
		if st.Lease == nil {
			f.t.Fatal("expireLease: Lease 不存在")
		}
		st.Lease.RenewTime = time.Now().Add(-ago).UTC().Format(time.RFC3339)
	})
}

// setJobState 只改「按名称读取」的 Job 对象状态：模拟本轮发布即将 apply 的 Job。
// 刻意不把它放进 Job 列表 —— apply 之前它不应该在集群里存在（否则前置扫描会正确地拦截）。
func (f *fixture) setJobState(job fakestate.Job) {
	f.t.Helper()
	f.updateState(func(st *fakestate.State) {
		st.Job = job
		st.JobScript = nil
		if !job.Exists {
			st.Jobs = nil
		}
	})
}

// setClusterJobState 同时改列表：模拟「集群里已经存在的 Job」（上一轮遗留）。
func (f *fixture) setClusterJobState(job fakestate.Job) {
	f.t.Helper()
	f.updateState(func(st *fakestate.State) {
		st.Job = job
		st.JobScript = nil
		if job.Exists {
			st.Jobs = []fakestate.Job{job}
		} else {
			st.Jobs = nil
		}
	})
}

// TestMigrationTimeoutKeepsLeaseAssociation 覆盖 6.1-1：
// 超时后 Job 仍在运行 → 锁不释放、关联记录（state/job/uid）完整保留。
func TestMigrationTimeoutKeepsLeaseAssociation(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	running := runningMigration(f)
	f.setJobState(running)

	res := runDeploy(t, f, "--migration-timeout", "6")

	res.requireCode(t, 6)
	res.requireContains(t, "迁移等待超时", "发布锁**未释放**")
	f.requireNoEvent(t, "lease.release")

	lease := f.state().Lease
	if lease == nil || lease.HolderIdentity == "" {
		t.Fatalf("迁移未终态时不得释放锁，实际: %+v", lease)
	}
	state, job, uid := f.leaseRecord()
	if state != "MIGRATION_RUNNING" {
		t.Fatalf("超时后的迁移状态应为 MIGRATION_RUNNING，实际 %q", state)
	}
	if job != running.Name || uid != running.UID {
		t.Fatalf("必须保留 Job 关联（job=%q uid=%q），实际 job=%q uid=%q", running.Name, running.UID, job, uid)
	}
}

// TestMigrationTimeoutBlocksNextRunnerWithActiveJob 覆盖 6.1-2：
// 锁过期后第二个 Runner 仍然不能接管 —— 上一轮 Job 还在跑。
func TestMigrationTimeoutBlocksNextRunnerWithActiveJob(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.setJobState(runningMigration(f))

	runDeploy(t, f, "--migration-timeout", "6").requireCode(t, 6)
	f.expireLease(10 * time.Minute)

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")

	res.requireCode(t, 7)
	res.requireNoSuccess(t)
	res.requireContains(t, "拒绝接管", "未终态")
	if got := f.countEvents("apply.migration"); got != 1 {
		t.Fatalf("第二个 Runner 不得启动新的迁移，实际 apply.migration = %d 次", got)
	}
	if got := f.countEvents("lease.acquire"); got != 1 {
		t.Fatalf("第二个 Runner 不得接管发布锁，实际获取 %d 次", got)
	}
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity == "" {
		t.Fatalf("被拒绝接管时不得改动锁记录，实际: %+v", lease)
	}
}

// TestTakeoverAllowedAfterJobComplete 覆盖 6.1-3：
// 上一轮 Job 已 Complete → 第二个 Runner 可以正常接管并完成发布。
func TestTakeoverAllowedAfterJobComplete(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	running := runningMigration(f)
	f.setJobState(running)

	runDeploy(t, f, "--migration-timeout", "6").requireCode(t, 6)

	complete := running
	complete.Complete = true
	complete.Pods = nil
	f.setClusterJobState(complete)
	f.expireLease(10 * time.Minute)

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "已终态（complete）且 Pod 已终止：允许接管")
	if got := f.countEvents("apply.migration"); got != 2 {
		t.Fatalf("接管后应重新执行迁移（apply 两次），实际 %d 次", got)
	}
}

// TestTakeoverAllowedAfterJobFailedWithoutForce 覆盖 6.1-4：
// 上一轮 Job 已 Failed **且 Pod 已终止** → 允许接管，但脚本绝不 force、绝不自动删除 Job。
//
// T2-06.1.2 / AUD-D3 之后，「Failed 且 Pod 仍在 Running」不再被允许接管（见
// recovery_test.go 的 Pod 状态矩阵）。
func TestTakeoverAllowedAfterJobFailedWithoutForce(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	failed := runningMigration(f)
	failed.Failed = true
	failed.Reason = "BackoffLimitExceeded"
	failed.Message = "Job has reached the specified backoff limit"
	failed.Pods = []fakestate.Pod{{
		Name: "surgecart-migrate-abc1234-p9x2z", Phase: "Failed",
	}}
	// 上一轮已经超时并保留了 RUNNING 记录；现在把它推进到 Failed。
	f.setJobState(runningMigration(f))
	runDeploy(t, f, "--migration-timeout", "6").requireCode(t, 6)

	f.setClusterJobState(failed)
	f.expireLease(10 * time.Minute)

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")

	res.requireCode(t, 2)
	res.requireContains(t, "已终态（failed）且 Pod 已终止：允许接管", "迁移失败", "脚本不会自动 force，也不会自动删除 Job")
	for _, call := range f.calls() {
		if strings.HasPrefix(call, "delete ") {
			t.Fatalf("脚本不得自动删除 Job，实际调用: %s", call)
		}
		if strings.Contains(call, "migrate force") {
			t.Fatalf("脚本不得调用 migrate force，实际调用: %s", call)
		}
	}
	f.requireNoEvent(t, "apply.application")
}

// TestTakeoverRefusedWhenJobQueryFails 覆盖 6.1-5：
// 上一轮 Job 状态查询失败 → 状态未知 → 拒绝接管。
func TestTakeoverRefusedWhenJobQueryFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.setJobState(runningMigration(f))
	runDeploy(t, f, "--migration-timeout", "6").requireCode(t, 6)

	f.updateState(func(st *fakestate.State) { st.JobGetFailures = 1 })
	f.expireLease(10 * time.Minute)

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")

	res.requireCode(t, 7)
	res.requireContains(t, "无法读取上一轮 Job", "状态未知，拒绝接管")
	if got := f.countEvents("lease.acquire"); got != 1 {
		t.Fatalf("查询失败时不得接管发布锁，实际获取 %d 次", got)
	}
}

// TestTakeoverRefusedWhenJobNotFoundButRecorded 覆盖 6.1-6：
// 记录指向一个已不存在的 Job → 必须进入人工恢复路径，不得假定迁移已完成。
func TestTakeoverRefusedWhenJobNotFoundButRecorded(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.setJobState(runningMigration(f))
	runDeploy(t, f, "--migration-timeout", "6").requireCode(t, 6)

	f.setClusterJobState(fakestate.Job{Exists: false})
	f.expireLease(10 * time.Minute)

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")

	res.requireCode(t, 7)
	res.requireContains(t,
		"但该 Job 不存在",
		"不得据此假定迁移已经完成",
		"人工恢复步骤",
		"migration-recovery-evidence",
	)
	res.requireNotContains(t, "DEPLOY_K8S_SUCCESS")
	if got := f.countEvents("apply.migration"); got != 1 {
		t.Fatalf("记录与 Job 不一致时不得开始新一轮迁移，实际 apply.migration = %d 次", got)
	}
}

// TestCrashAfterJobCreatedIsDetectedByNextRunner 覆盖 6.1-7：
// Runner 在创建 Job 之后崩溃（SIGKILL，来不及写清理），第二个 Runner 必须能从
// Lease 注解 + 真实 Job 识别出「上一轮迁移仍在跑」并拒绝接管。
func TestCrashAfterJobCreatedIsDetectedByNextRunner(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	slowMigration(f)

	p := f.start("--migration-timeout", "120", "--rollout-timeout", "30")
	f.waitForEvent(t, "apply.migration", 60*time.Second)
	p.kill()

	f.updateState(func(st *fakestate.State) {
		if st.Lease == nil {
			t.Fatal("崩溃后锁记录必须仍然存在")
		}
		st.JobScript = nil
	})
	f.expireLease(10 * time.Minute)

	// 第一个 Runner 崩溃时 Job 仍在运行：第二个 Runner 必须被拦住。
	// Job 名/uid 取自崩溃留下的记录（取决于崩溃发生在 UID 回写之前还是之后）。
	recordedState, recordedJob, recordedUID := f.leaseRecord()
	if recordedJob == "" {
		t.Fatalf("崩溃后必须留下 Job 名，实际记录 state=%q job=%q", recordedState, recordedJob)
	}
	running := fakestate.Job{
		Name: recordedJob, UID: recordedUID, Exists: true,
		Pods: []fakestate.Pod{{Name: "surgecart-migrate-crash-pod", Phase: "Running"}},
	}
	f.setClusterJobState(running)

	blocked := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")
	blocked.requireCode(t, 7)
	blocked.requireContains(t, "拒绝接管")

	// Job 进入终态后，接管放行。
	complete := running
	complete.Complete = true
	complete.Pods = nil
	f.setClusterJobState(complete)

	allowed := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10", "--rollout-timeout", "10")
	allowed.requireSuccess(t)
}

// TestApplyErrorButJobCreatedIsNotMisjudged 覆盖 6.1-8：
// apply 返回错误但 API Server 实际已创建 Job → 不得误判为「未创建」。
func TestApplyErrorButJobCreatedIsNotMisjudged(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		st.ApplyErrorsApplied = map[string]bool{"migration": true}
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "不能据此断定 Job 未创建")
	f.requireEvent(t, "apply.error-but-created migration")
	// 只 apply 一次：脚本不是靠重试 apply 来「碰运气」。
	if got := f.countEvents("apply.error-but-created migration"); got != 1 {
		t.Fatalf("迁移 apply 次数 = %d，期望 1", got)
	}
}

// TestApplicationApplyFailureReleasesLeaseAndKeepsRecord 覆盖 6.1-9：
// 迁移已完成、Application apply 失败 → 退出码 1，锁可以释放，完成记录保留。
func TestApplicationApplyFailureReleasesLeaseAndKeepsRecord(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) { st.ApplyFailures["application"] = true })

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 1)
	res.requireNoSuccess(t)
	res.requireContains(t, "Application 清单应用失败")
	f.requireEvent(t, "lease.release")
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "" {
		t.Fatalf("迁移已终态时 Application 失败应释放锁，实际: %+v", lease)
	}
	state, job, uid := f.leaseRecord()
	if state != "MIGRATION_COMPLETE" || job == "" || uid == "" {
		t.Fatalf("完成记录必须保留（state/job/uid），实际 state=%q job=%q uid=%q", state, job, uid)
	}
}

// TestScanBlocksActiveMigrationWithoutRecord 覆盖「注解丢了也不能放过」：
// 锁上没有迁移记录，但集群里有一个未终结的迁移 Job → 拒绝接管。
func TestScanBlocksActiveMigrationWithoutRecord(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		// 上一轮 Runner 是升级前的老版本（不写注解），或者注解写入失败。
		st.Lease = &fakestate.Lease{
			ResourceVersion: "5",
			UID:             "lease-uid-legacy",
			HolderIdentity:  "deploy-legacy-runner-1",
			AcquireTime:     rfc3339(time.Now().Add(-30 * time.Minute)),
			RenewTime:       rfc3339(time.Now().Add(-20 * time.Minute)),
			DurationSeconds: 30,
			Transitions:     1,
		}
		st.Jobs = []fakestate.Job{{
			Name:   "surgecart-migrate-legacy",
			UID:    "uid-legacy-job",
			Exists: true,
		}}
	})

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")

	res.requireCode(t, 7)
	res.requireContains(t, "发现未终结的迁移 Job", "上一轮没有迁移记录，但集群里存在未终结的迁移 Job")
	if got := f.countEvents("apply.migration"); got != 0 {
		t.Fatalf("存在未终结迁移时不得 apply 迁移清单，实际 %d 次", got)
	}
}

// TestUnknownMigrationStateFailsClosed 覆盖「记录里的状态无法识别」：
// 不猜测语义，直接拒绝接管。
func TestUnknownMigrationStateFailsClosed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		st.Lease = &fakestate.Lease{
			ResourceVersion: "9",
			UID:             "lease-uid-odd",
			HolderIdentity:  "deploy-odd-runner",
			AcquireTime:     rfc3339(time.Now().Add(-30 * time.Minute)),
			RenewTime:       rfc3339(time.Now().Add(-20 * time.Minute)),
			DurationSeconds: 30,
			Annotations: map[string]string{
				annMigrationState: "MIGRATION_HALF_DONE",
				annMigrationJob:   "surgecart-migrate-somebody",
				annMigrationUID:   "uid-somebody",
			},
		}
	})

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")

	res.requireCode(t, 7)
	res.requireContains(t, "无法识别：拒绝接管")
}
