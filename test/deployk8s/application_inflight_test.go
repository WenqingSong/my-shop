package deployk8s

import (
	"strings"
	"syscall"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1.2 / AUD-D1（AUD-02 / AUD-05 的边界）与 AUD-D5-C：
// **Application apply 的在途行为**。
//
// 必须如实理解这组测试证明了什么、没证明什么：
//   - 证明：失锁之后脚本不再发起新的写请求；已发出的请求会留在集群里；
//     客户端返回非零**不会**被当成「资源没变」；结果不确定时输出
//     APPLICATION_OUTCOME_UNKNOWN 并明确提示「可能已被 API Server 接受并生效」。
//   - 不证明：已经发出的请求能被撤销。当前架构（Kubernetes Lease + 普通 apply）
//     不具备这种能力；真正的强 fencing 需要 Admission Webhook 或专用发布控制器。
//
// 所有用例都真实执行 scripts/deploy-k8s.sh；「在途」由 fake kubectl 之外的
// 外部延迟包装制造（见 harness_test.go 的 applyDelay）。

// TestLockLostBeforeApplicationApplyIssuesNothing 覆盖 AUD-D5-C1「apply 发出前失锁」：
// 服务端复核发现锁已不属于自己 → 退出码 3，且**没有**发出 Application apply。
func TestLockLostBeforeApplicationApplyIssuesNothing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		// 迁移完成记录写入成功后，锁立刻被另一个 Runner 接管。
		st.LeaseStealAfterState = "MIGRATION_COMPLETE"
		st.LeaseStealHolder = "deploy-new-runner-9101"
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "服务端复核发现发布锁已不属于本 Runner", "本次尚未发出新的写请求")
	f.requireNoEvent(t, "apply.application")
	f.requireNoEvent(t, "lease.release")
	if f.state().Applied {
		t.Fatal("apply 发出前失锁时不得修改 Deployment")
	}
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "deploy-new-runner-9101" {
		t.Fatalf("锁必须仍由新持有者持有，实际: %+v", lease)
	}
}

// TestLockLostDuringApplicationApplyReportsUnknown 覆盖 AUD-D5-C2/C5：
// 请求在途时失锁 → 请求已经生效（不可撤销）、退出码 3、输出 APPLICATION_OUTCOME_UNKNOWN，
// 并且**在途请求完成后不再发出任何新的操作**。
func TestLockLostDuringApplicationApplyReportsUnknown(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{applyDelay: 1500 * time.Millisecond})

	p := f.start("--migration-timeout", "20", "--rollout-timeout", "20")
	f.waitForFile(t, f.inflightMarker(), 60*time.Second)
	// 请求在途期间锁被接管（模拟租期过期/被其他 Runner 抢走）。
	f.stealLease("deploy-new-runner-9102")

	res := p.wait(t, 120*time.Second)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t,
		"APPLICATION_OUTCOME_UNKNOWN",
		"失去发布锁",
		"可能已被 API Server 接受并生效",
		"无法撤销",
	)
	// 请求确实被服务端接受：不能声称「资源没有被修改」。
	if !f.state().Applied {
		t.Fatal("apply 已经发出且 fake 已接受，状态必须记录为已应用（否则测试替身失真）")
	}
	// 在途请求完成后不再发出新的操作。
	if got := f.countEvents("apply.application"); got != 1 {
		t.Fatalf("Application apply 次数 = %d，期望 1（失锁后不得重试/重复 apply）", got)
	}
	if got := f.countEvents("apply.migration"); got != 1 {
		t.Fatalf("迁移 apply 次数 = %d，期望 1", got)
	}
	for _, call := range f.calls() {
		if strings.HasPrefix(call, "delete ") {
			t.Fatalf("失锁后不得删除任何资源，实际调用: %s", call)
		}
	}
	f.requireNoEvent(t, "lease.release")
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "deploy-new-runner-9102" {
		t.Fatalf("旧持有者不得清除新持有者的锁，实际: %+v", lease)
	}
}

// TestApplicationApplyAcceptedButClientTimesOut 覆盖 AUD-D5-C3：
// 服务端已接受请求、客户端却返回超时 → 不得判定「资源没有变」；
// 输出 APPLICATION_OUTCOME_UNKNOWN，并且不打印成功标记。
func TestApplicationApplyAcceptedButClientTimesOut(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		st.ApplyErrorsApplied["application"] = true
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 1)
	res.requireNoSuccess(t)
	res.requireContains(t,
		"Application 清单应用失败",
		"不能",
		"资源没有被修改",
		"APPLICATION_OUTCOME_UNKNOWN",
		"很可能已被 API Server 接受并生效",
	)
	f.requireEvent(t, "apply.error-but-created application")
	// 关键断言：Deployment **确实**被修改了 —— 客户端非零 ≠ 没有变更。
	if !f.state().Applied {
		t.Fatal("客户端超时时对象已被服务端创建，测试替身必须如实记录")
	}
	if got := f.countEvents("apply.application"); got != 0 {
		t.Fatalf("不重复 apply：期望 0 次成功 apply 事件，实际 %d", got)
	}
	// 迁移已终态：锁可以释放，完成记录保留。
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "" {
		t.Fatalf("迁移终态时 Application 结果不确定应释放锁，实际: %+v", lease)
	}
	state, _, uid := f.leaseRecord()
	if state != "MIGRATION_COMPLETE" || uid == "" {
		t.Fatalf("完成记录必须保留，实际 state=%q uid=%q", state, uid)
	}
}

// TestPausedRunnerResumesAfterLockLoss 覆盖 AUD-D5-C4「旧 Runner 恢复执行」：
// Runner 在 apply 期间被 SIGSTOP，锁被接管；恢复后必须停止并如实报告，
// 而不是继续验证、更不是打印成功。
func TestPausedRunnerResumesAfterLockLoss(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{applyDelay: 1500 * time.Millisecond})

	p := f.start("--migration-timeout", "20", "--rollout-timeout", "20")
	f.waitForFile(t, f.inflightMarker(), 60*time.Second)

	// 冻结 Runner（模拟长 GC / 挂起 / 网络分区期间进程被暂停）。
	if err := p.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatalf("SIGSTOP: %v", err)
	}
	f.stealLease("deploy-new-runner-9103")
	time.Sleep(200 * time.Millisecond)
	if err := p.cmd.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatalf("SIGCONT: %v", err)
	}

	res := p.wait(t, 120*time.Second)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "APPLICATION_OUTCOME_UNKNOWN", "可能已被 API Server 接受并生效")
	if !f.state().Applied {
		t.Fatal("恢复执行后必须如实反映「请求已经生效」，不能假装没有发生")
	}
	f.requireNoEvent(t, "lease.release")
	if got := f.countEvents("apply.application"); got != 1 {
		t.Fatalf("恢复后不得重试 apply，实际 %d 次", got)
	}
}
