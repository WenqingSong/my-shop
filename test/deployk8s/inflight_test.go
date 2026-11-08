package deployk8s

import (
	"strings"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1.1 §6.3「在途操作」与 AUD-02 的真实边界：
//
//	Runner A 获取 Lease → A 开始操作 → 模拟失锁 → Runner B（真实独立进程）尝试接管
//	→ A 恢复执行
//
// 明确验证（也是明确承认局限）：
//   - 新操作不会在「本地失锁标记」被观察到之后继续发起；
//   - **已经发出的 kubectl 请求不能被假定为可撤销**（迁移 Job 仍在集群里）；
//   - 协作式锁不是 fencing：本测试证明的是「脚本会停下来」，不是「旧的写一定被拒绝」。

// TestInflightLockLossStopsNewOperations 覆盖「迁移记录写入成功后锁立即被接管」：
// A 必须停止后续操作、不碰新持有者的 Lease；B 在 Job 仍运行时不得开始新一轮迁移。
func TestInflightLockLossStopsNewOperations(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	slowMigration(f)
	f.updateState(func(st *fakestate.State) {
		// 确定性注入：MIGRATION_RUNNING 写入成功后，锁立刻被另一个 Runner 接管。
		st.LeaseStealAfterState = "MIGRATION_RUNNING"
		st.LeaseStealHolder = "deploy-new-runner-9001"
	})

	a := runDeploy(t, f, "--migration-timeout", "60", "--rollout-timeout", "30",
		"--lease-duration", "40", "--lease-renew-interval", "5")

	a.requireCode(t, 3)
	a.requireNoSuccess(t)
	// 两条失锁路径都合法：写迁移记录时发现 holder 变了，或后台续租先失败。
	if !strings.Contains(a.output(), "发布锁已不属于本 Runner") &&
		!strings.Contains(a.output(), "发布锁续租失败") {
		t.Fatalf("必须明确报告失去发布锁；实际输出:\n%s", a.output())
	}
	f.requireEvent(t, "lease.stolen-after-state")

	// 1) 失锁之后没有发起新的资源变更。
	f.requireNoEvent(t, "apply.application")
	// 2) 旧持有者不得清除新持有者的 Lease。
	f.requireNoEvent(t, "lease.release")
	lease := f.state().Lease
	if lease == nil || lease.HolderIdentity != "deploy-new-runner-9001" {
		t.Fatalf("锁必须仍由新持有者持有，实际: %+v", lease)
	}
	// 3) 已经发出的请求不可撤销：迁移 Job 依然在集群里。
	state := f.state()
	if !state.MigrationApplied || len(state.Jobs) == 0 {
		t.Fatalf("迁移请求已经发出，Job 必须仍然存在（不能假定可撤销），实际: applied=%v jobs=%d",
			state.MigrationApplied, len(state.Jobs))
	}

	// 4) 第二个 Runner（真实独立进程）：Job 仍在运行 → 拒绝接管。
	f.expireLease(10 * time.Minute)
	f.setClusterJobState(runningMigration(f))

	b := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")
	b.requireCode(t, 7)
	b.requireContains(t, "拒绝接管")
	if got := f.countEvents("apply.migration"); got != 1 {
		t.Fatalf("第二个 Runner 不得启动新的迁移，实际 apply.migration = %d 次", got)
	}

	// 5) Job 进入终态后，接管放行（生命周期互斥解除）。
	complete := runningMigration(f)
	complete.Complete = true
	complete.Pods = nil
	f.setClusterJobState(complete)

	c := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10", "--rollout-timeout", "10")
	c.requireSuccess(t)
	if got := f.countEvents("apply.application"); got != 1 {
		t.Fatalf("只允许接管后的那一次 Application apply，实际 %d 次", got)
	}
}

// TestInflightAbortDoesNotLeakLeaseWrites 覆盖「失锁后不再写 Lease」：
// 续租失败被判定为失锁后，脚本不得再对 Lease 做任何 replace（包括释放）。
func TestInflightAbortDoesNotLeaseWrites(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	slowMigration(f)
	f.updateState(func(st *fakestate.State) {
		st.LeaseStealAfterGets = 1
		st.LeaseStealHolder = "deploy-new-runner-9002"
	})

	res := runDeploy(t, f, append([]string{
		"--migration-timeout", "60",
		"--rollout-timeout", "30",
	}, leaseArgs()...)...)

	res.requireCode(t, 3)
	f.requireEvent(t, "lease.stolen")

	// 失锁之后只允许读 Lease，不允许写（replace）。
	seenSteal := false
	for _, line := range f.events() {
		if strings.HasPrefix(line, "lease.stolen") {
			seenSteal = true
			continue
		}
		if seenSteal && (strings.HasPrefix(line, "lease.release") ||
			strings.HasPrefix(line, "lease.renew") ||
			strings.HasPrefix(line, "lease.acquire")) {
			t.Fatalf("失锁后不得再写 Lease，实际事件: %s\n事件序列:\n%s", line, strings.Join(f.events(), "\n"))
		}
	}
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "deploy-new-runner-9002" {
		t.Fatalf("锁必须仍由新持有者持有，实际: %+v", lease)
	}
}
