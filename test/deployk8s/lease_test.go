package deployk8s

import (
	"strings"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1-01：发布级互斥（coordination.k8s.io/v1 Lease）。
//
// 断言对象是**脚本行为**：真实执行 deploy-k8s.sh，观察 fake kubectl 记录的事件与
// Lease 状态。fake 的 create/replace 用文件锁串行化，因此「只有一个 Runner 拿到锁」
// 是由真实进程竞争产生的，不是预设结论。

func rfc3339(at time.Time) string { return at.UTC().Format(time.RFC3339) }

// slowMigration 让迁移等待循环至少跑几轮（用于观察续租、并发与信号行为）。
func slowMigration(f *fixture) {
	f.updateState(func(st *fakestate.State) {
		st.JobScript = []fakestate.Job{
			{Exists: true},
			{Exists: true},
			{Exists: true},
			{Exists: true},
			{Exists: true, Complete: true},
		}
	})
}

// TestLeaseFirstDeployment 覆盖「首次部署」：Deployment 不存在也必须有锁，
// 且锁覆盖完整生命周期（迁移 → 应用 → 验证 → 释放）。
func TestLeaseFirstDeployment(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{firstDeploy: true})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	f.requireEventOrder(t,
		"lease.acquire",
		"apply.migration",
		"apply.application",
		"lease.release",
	)
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "" {
		t.Fatalf("发布结束后锁必须已释放（holderIdentity 为空），实际: %+v", lease)
	}
	res.requireContains(t, "首次部署")
	f.tmpDirEmpty(t)
}

// TestLeaseConcurrentRunnersOnlyOneDeploys 覆盖「两个 Runner 同时申请锁，只有一个成功」：
// 失败方在等待窗口内拿不到锁即退出，绝不进入发布阶段。
func TestLeaseConcurrentRunnersOnlyOneDeploys(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	slowMigration(f)

	first := f.start("--migration-timeout", "60", "--rollout-timeout", "30", "--lock-wait-timeout", "1")
	second := f.start("--migration-timeout", "60", "--rollout-timeout", "30", "--lock-wait-timeout", "1")

	firstResult := first.wait(t, 120*time.Second)
	secondResult := second.wait(t, 120*time.Second)

	codes := []int{firstResult.code, secondResult.code}
	successCount, lockFailureCount := 0, 0
	for _, code := range codes {
		switch code {
		case 0:
			successCount++
		case 3:
			lockFailureCount++
		}
	}
	if successCount != 1 || lockFailureCount != 1 {
		t.Fatalf("并发发布必须恰好一个成功、一个因锁失败（退出码 3），实际退出码 %v\n--- A ---\n%s\n--- B ---\n%s",
			codes, firstResult.output(), secondResult.output())
	}

	// 只有一个 Runner 真正进入发布阶段。
	if got := f.countEvents("apply.migration"); got != 1 {
		t.Fatalf("迁移清单被 apply %d 次，期望 1 次（第二个 Runner 必须在进入发布前失败）", got)
	}
	if got := f.countEvents("apply.application"); got != 1 {
		t.Fatalf("应用清单被 apply %d 次，期望 1 次", got)
	}
	if got := f.countEvents("lease.acquire"); got != 1 {
		t.Fatalf("成功获取锁 %d 次，期望 1 次（create 必须原子，接管必须 CAS）", got)
	}

	// 失败方必须给出可操作的原因，而不是静默退出。
	failedOutput := secondResult.output()
	if secondResult.code == 0 {
		failedOutput = firstResult.output()
	}
	if !strings.Contains(failedOutput, "未能在 1s 内获取发布锁") &&
		!strings.Contains(failedOutput, "另一个发布正在持有锁") {
		t.Fatalf("因锁失败的一方必须给出明确原因；实际输出:\n%s", failedOutput)
	}
}

// TestLeaseSerializesTwoPipelines 覆盖「第二个 Runner 在第一个释放前不能进入发布阶段」：
// 第二个 Runner 等待锁，拿到（接管已释放的 Lease）之后才迁移与更新应用，
// 因此两次「迁移 → 应用」严格串行，绝不交错。
func TestLeaseSerializesTwoPipelines(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	slowMigration(f)

	first := f.start("--migration-timeout", "60", "--rollout-timeout", "30", "--lock-wait-timeout", "60")
	f.waitForEvent(t, "apply.migration", 60*time.Second)

	second := f.start("--migration-timeout", "60", "--rollout-timeout", "30", "--lock-wait-timeout", "60")

	firstResult := first.wait(t, 120*time.Second)
	secondResult := second.wait(t, 120*time.Second)

	firstResult.requireSuccess(t)
	secondResult.requireSuccess(t)
	secondResult.requireContains(t, "另一个发布正在持有锁")

	// 事件顺序证明串行：A 的迁移 → A 的应用 → B 的迁移 → B 的应用。
	f.requireEventOrder(t,
		"apply.migration",
		"apply.application",
		"apply.migration",
		"apply.application",
	)
	if got := f.countEvents("lease.acquire"); got != 2 {
		t.Fatalf("两次发布应各自获取锁一次（第二次接管已释放的 Lease），实际 %d 次", got)
	}
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "" {
		t.Fatalf("两次发布结束后锁必须已释放，实际: %+v", lease)
	}
}

// TestLeaseOccupiedByOtherRunner 覆盖「锁已被占用」：等待超时后失败，且不产生任何资源变更。
func TestLeaseOccupiedByOtherRunner(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	now := time.Now().UTC()
	f.updateState(func(st *fakestate.State) {
		st.Lease = &fakestate.Lease{
			ResourceVersion: "7",
			UID:             "lease-uid-other",
			HolderIdentity:  "deploy-other-runner-9999",
			AcquireTime:     rfc3339(now.Add(-30 * time.Second)),
			RenewTime:       rfc3339(now),
			DurationSeconds: 300,
			Transitions:     2,
		}
	})

	res := runDeploy(t, f, "--lock-wait-timeout", "1")

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "另一个发布正在持有锁", "deploy-other-runner-9999")
	f.requireNoEvent(t, "apply.migration")
	f.requireNoEvent(t, "apply.application")
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "deploy-other-runner-9999" {
		t.Fatalf("锁被别人持有时不得改动其持有者，实际: %+v", lease)
	}
}

// TestLeaseExpiredIsTakenOver 覆盖「锁过期后其他 Runner 可以接管」。
func TestLeaseExpiredIsTakenOver(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		st.Lease = &fakestate.Lease{
			ResourceVersion: "11",
			UID:             "lease-uid-dead",
			HolderIdentity:  "deploy-crashed-runner-1234",
			AcquireTime:     rfc3339(time.Now().Add(-20 * time.Minute)),
			RenewTime:       rfc3339(time.Now().Add(-15 * time.Minute)),
			DurationSeconds: 30,
			Transitions:     1,
		}
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "发布锁已过期")
	f.requireEventOrder(t, "lease.acquire", "apply.migration")
	f.requireEvent(t, "lease.release")
	if lease := f.state().Lease; lease == nil || lease.Transitions != 2 {
		t.Fatalf("接管过期锁必须递增 leaseTransitions（期望 2），实际: %+v", lease)
	}
}

// TestLeaseRenewedDuringDeploy 覆盖「锁持有者可以正常续租」。
func TestLeaseRenewedDuringDeploy(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	slowMigration(f)

	res := runDeploy(t, f,
		"--migration-timeout", "60",
		"--rollout-timeout", "30",
		"--lease-duration", "5",
		"--lease-renew-interval", "1",
	)

	res.requireSuccess(t)
	if got := f.countEvents("lease.renew"); got < 2 {
		t.Fatalf("续租次数 = %d，期望 >= 2（发布期间必须持续续租）；事件:\n%s",
			got, strings.Join(f.events(), "\n"))
	}
	f.requireEvent(t, "lease.release")
}

// TestLeaseRenewFailureStopsApplicationApply 覆盖「续租失败时不允许继续执行 Application apply」。
func TestLeaseRenewFailureStopsApplicationApply(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	slowMigration(f)
	f.updateState(func(st *fakestate.State) { st.LeaseReplaceFailures = 1 })

	res := runDeploy(t, f,
		"--migration-timeout", "60",
		"--rollout-timeout", "30",
		"--lease-duration", "5",
		"--lease-renew-interval", "1",
	)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "续租失败")
	f.requireNoEvent(t, "apply.application")
}

// TestLeaseTakenOverCannotBeReleased 覆盖「旧持有者不能释放新持有者的锁」：
// 锁被接管后，原持有者的续租失败并中止发布，且不会清空新持有者的记录。
func TestLeaseTakenOverCannotBeReleased(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	slowMigration(f)
	f.updateState(func(st *fakestate.State) {
		st.LeaseStealAfterGets = 1
		st.LeaseStealHolder = "deploy-new-runner-5678"
	})

	res := runDeploy(t, f,
		"--migration-timeout", "60",
		"--rollout-timeout", "30",
		"--lease-duration", "5",
		"--lease-renew-interval", "1",
	)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	f.requireEvent(t, "lease.stolen")
	f.requireNoEvent(t, "lease.release")
	f.requireNoEvent(t, "apply.application")
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "deploy-new-runner-5678" {
		t.Fatalf("旧持有者不得释放新持有者的锁，实际: %+v", lease)
	}
}

// TestLeasePermissionDeniedFailsBeforeMigration 覆盖「权限不足必须在迁移之前失败」。
func TestLeasePermissionDeniedFailsBeforeMigration(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) { st.Permissions.Update = false })

	res := runDeploy(t, f)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "缺少 leases 的 update 权限")
	f.requireNoEvent(t, "apply.migration")
	f.requireNoEvent(t, "apply.application")
	if lease := f.state().Lease; lease != nil {
		t.Fatalf("权限预检失败时不得创建锁资源，实际: %+v", lease)
	}
}

// TestLeaseReleaseUsesCAS 覆盖「正常释放」：释放走 CAS（resourceVersion 递增），
// 且释放后锁记录保留（不删除对象），以便后续 Runner 直接接管。
func TestLeaseReleaseUsesCAS(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	lease := f.state().Lease
	if lease == nil {
		t.Fatal("释放锁不应删除 Lease 对象（删除会与接管者产生 create/replace 竞态）")
	}
	if lease.HolderIdentity != "" {
		t.Fatalf("释放后 holderIdentity 必须为空，实际 %q", lease.HolderIdentity)
	}
	if lease.ResourceVersion == "1" {
		t.Fatalf("释放必须是一次 CAS 写入（resourceVersion 应已递增），实际 %q", lease.ResourceVersion)
	}
	f.requireNoEvent(t, "lease.conflict")
}
