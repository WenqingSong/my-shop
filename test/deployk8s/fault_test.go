package deployk8s

import (
	"syscall"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1 的故障处理边界：脚本在异常路径上也必须
// 「不放行发布」「尽力释放锁」「清理临时文件」。

// TestKubectlMissing 覆盖 kubectl 不存在：直接失败，不触碰集群。
func TestKubectlMissing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	// 追加的 PATH 覆盖前面的值（exec 环境变量后写优先），里面没有任何 kubectl。
	f.env = append(f.env, "PATH="+basePath(t))

	res := runDeploy(t, f)

	res.requireCode(t, 1)
	res.requireNoSuccess(t)
	res.requireContains(t, "未安装 kubectl")
	f.requireNoEvent(t, "apply.migration")
	f.requireNoEvent(t, "apply.application")
}

// TestClusterUnreachable 覆盖集群不可达：预检失败，不进入发布流程。
func TestClusterUnreachable(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) { st.ClusterReachable = false })

	res := runDeploy(t, f)

	res.requireCode(t, 1)
	res.requireNoSuccess(t)
	res.requireContains(t, "kubectl 无法连接集群")
	f.requireNoEvent(t, "apply.migration")
}

// TestApplyFailureDuringMigrationReleasesLock 覆盖「迁移 apply 失败且对象确实没有创建」：
// 退出码 1，锁必须释放（迁移从未发生，不能让下一次发布白等一个租期）。
func TestApplyFailureDuringMigrationReleasesLock(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		st.ApplyFailures["migration"] = true
		// apply 失败且 Job 确实不存在：这是「确定未创建」，不是「状态不明」。
		st.Job = fakestate.Job{Exists: false}
		st.JobScript = []fakestate.Job{{Exists: false}}
	})

	res := runDeploy(t, f, "--migration-timeout", "8")

	res.requireCode(t, 1)
	res.requireNoSuccess(t)
	res.requireContains(t, "Migration 清单应用失败", "确实不存在")
	// D-1/D-2/B-13：Job 不存在只证明**这个 Job** 没创建，不能据此断言整份清单未生效
	// （Namespace / ConfigMap 等可能已经应用），也不能假设客户端失败=服务端未接受。
	res.requireContains(t, "MIGRATION_OUTCOME_UNKNOWN", "这只证明该 Job 对象未被创建")
	res.requireNotContains(t, "（应用未更新）")
	f.requireEvent(t, "lease.release")
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "" {
		t.Fatalf("确认 Job 未创建后必须释放锁，实际: %+v", lease)
	}
	state, _, _ := f.leaseRecord()
	if state != "NO_MIGRATION" {
		t.Fatalf("确认迁移未发生时应把记录复位为 NO_MIGRATION，实际 %q", state)
	}
	f.requireNoEvent(t, "apply.application")
}

// TestSigtermDuringMigrationKeepsLease 覆盖 SIGTERM（迁移仍在运行）：
// AUD-01 要求信号路径遵循同一套生命周期规则 —— 迁移未终态就**不得**释放锁。
func TestSigtermDuringMigrationKeepsLease(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	slowMigration(f)

	p := f.start("--migration-timeout", "120", "--rollout-timeout", "30")
	f.waitForEvent(t, "apply.migration", 60*time.Second)
	time.Sleep(1500 * time.Millisecond)

	p.signal(t, syscall.SIGTERM)
	res := p.wait(t, 60*time.Second)

	res.requireCode(t, 143)
	res.requireNoSuccess(t)
	res.requireContains(t, "收到 SIGTERM", "不释放发布锁")
	f.requireNoEvent(t, "lease.release")
	f.requireNoEvent(t, "apply.application")

	lease := f.state().Lease
	if lease == nil || lease.HolderIdentity == "" {
		t.Fatalf("迁移未终态时 SIGTERM 不得释放锁，实际: %+v", lease)
	}
	state, job, uid := f.leaseRecord()
	if state != "MIGRATION_PENDING" && state != "MIGRATION_RUNNING" {
		t.Fatalf("SIGTERM 后必须保留「未终态」记录，实际 state=%q", state)
	}
	if job == "" || uid == "" {
		t.Fatalf("关联信息（job/uid）必须保留，实际 job=%q uid=%q", job, uid)
	}
	f.tmpDirEmpty(t)
}

// TestCrashLeaseExpiresAndIsTakenOver 覆盖「Runner 崩溃（SIGKILL）」：
// 锁不会被释放，但租期到期后必须能被下一次发布接管。
func TestCrashLeaseExpiresAndIsTakenOver(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	slowMigration(f)

	p := f.start("--migration-timeout", "120", "--rollout-timeout", "30")
	f.waitForEvent(t, "apply.migration", 60*time.Second)
	p.kill()

	crashed := f.state()
	if crashed.Lease == nil || crashed.Lease.HolderIdentity == "" {
		t.Fatalf("SIGKILL 后锁记录应保持被持有（无法执行清理），实际: %+v", crashed.Lease)
	}

	// 上一个 Runner 的迁移关联记录必须被保留（崩溃不会清理）。
	// 注意 uid 允许为空：崩溃可能发生在「apply 之后、UID 回写之前」这个窗口，
	// 此时记录里只有 Job 名 —— 后续 Runner 必须靠真实对象核验，不能只信注解。
	state, job, _ := f.leaseRecord()
	if job == "" || (state != "MIGRATION_PENDING" && state != "MIGRATION_RUNNING") {
		t.Fatalf("崩溃后必须保留「未终态 + Job 名」记录，实际 state=%q job=%q", state, job)
	}

	// 模拟时间流逝：租期（30s）已过，且迁移本身已完成（同名同 uid）。
	f.updateState(func(st *fakestate.State) {
		st.Lease.RenewTime = time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
		st.JobScript = nil
		st.Job.Exists = true
		st.Job.Complete = true
		st.Job.Failed = false
		st.JobGets = 0
	})

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "发布锁已过期")
	if lease := f.state().Lease; lease == nil || lease.Transitions < 1 || lease.HolderIdentity != "" {
		t.Fatalf("接管过期锁后应在发布完成时释放，实际: %+v", lease)
	}
}

// TestTemporaryFilesCleanedUp 覆盖临时文件清理：成功与失败路径都不得残留临时清单。
func TestTemporaryFilesCleanedUp(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})

	runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10").requireSuccess(t)
	f.tmpDirEmpty(t)

	// 失败路径（版本降级拒绝）同样必须清理。
	f.retag(f.shas["c1"])
	rejected := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")
	rejected.requireCode(t, 4)
	f.tmpDirEmpty(t)
}
