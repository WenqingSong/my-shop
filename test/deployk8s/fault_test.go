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

// TestApplyFailureDuringMigrationReleasesLock 覆盖「发布过程中 API Server 返回错误」：
// 退出非零，且已经获得的锁必须被释放（不能让下一次发布白等一个租期）。
func TestApplyFailureDuringMigrationReleasesLock(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) { st.ApplyFailures["migration"] = true })

	res := runDeploy(t, f)

	res.requireCode(t, 1)
	res.requireNoSuccess(t)
	res.requireContains(t, "Migration 清单应用失败")
	f.requireEvent(t, "lease.release")
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "" {
		t.Fatalf("apply 失败后必须释放锁，实际: %+v", lease)
	}
	f.requireNoEvent(t, "apply.application")
}

// TestSigtermReleasesLock 覆盖 SIGTERM：中止发布、释放锁、清理临时文件、不报告成功。
func TestSigtermReleasesLock(t *testing.T) {
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
	res.requireContains(t, "收到 SIGTERM")
	f.requireEvent(t, "lease.release")
	f.requireNoEvent(t, "apply.application")
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "" {
		t.Fatalf("SIGTERM 后锁必须被释放（无 SIGKILL 时应尽力释放），实际: %+v", lease)
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

	// 模拟时间流逝：租期（30s）已过，且迁移本身已完成。
	f.updateState(func(st *fakestate.State) {
		st.Lease.RenewTime = time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
		st.JobScript = nil
		st.Job = fakestate.Job{Exists: true, Complete: true}
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
