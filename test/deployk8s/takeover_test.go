package deployk8s

import (
	"strings"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1.3 / F-06：**接管语义与真实 Kubernetes Lease 更新一致**。
//
// 审计发现：测试替身的 stealLease 只改 holderIdentity 与 renewTime，没有推进
// resourceVersion。真实 API Server 的成功写入一定会推进它，因此旧持有者用旧
// resourceVersion 的 PUT 必须得到 409；替身不推进 rv 会让「旧 Runner 覆盖新 Runner」
// 这个缺陷在测试里根本不可能出现（假阴性）。
//
// 本文件区分两个层次，报告里不能混为一谈：
//   - 替身语义（fake）：resourceVersion 推进、CAS 409、holder 不被覆盖；
//   - 真实 API Server 的并发语义：仍然是 NOT_VERIFIED（需要真实集群）。

// TestStealLeaseAdvancesResourceVersion 是替身规则本身的回归测试（F-06 的第 1/2/3 条）：
// 接管 = 一次成功写入 → rv 必须推进，UID 必须保持不变。
func TestStealLeaseAdvancesResourceVersion(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.seedReleasedCompleteLease() // rv=5, uid=lease-uid-previous

	before := f.state().Lease
	if before == nil {
		t.Fatal("seed 失败：Lease 不存在")
	}
	f.stealLease("deploy-new-runner-6000")

	after := f.state().Lease
	if after.UID != before.UID {
		t.Fatalf("接管不得改变 Lease 的 uid（对象没有被删除重建）：before=%q after=%q", before.UID, after.UID)
	}
	if after.ResourceVersion == before.ResourceVersion {
		t.Fatalf("接管必须推进 resourceVersion（真实 API Server 的成功写入一定推进）：仍为 %q", after.ResourceVersion)
	}
	if after.HolderIdentity != "deploy-new-runner-6000" {
		t.Fatalf("接管后持有者必须是新 Runner，实际 %q", after.HolderIdentity)
	}
}

// TestStaleResourceVersionCannotOverwriteTakeover 覆盖 F-06 的端到端回归：
//
//	Runner A GET Lease (rv=10)
//	    → Runner B 接管并更新 Lease (rv=11)
//	    → Runner A 用 rv=10 REPLACE
//	    → HTTP 409 Conflict（A 的写入被拒绝，B 的 holder 不被覆盖）
//
// 注入点刻意放在「续租 replace 执行 CAS 之前」：如果替身不推进 rv，A 的 PUT 会成功，
// 断言里的「无续租写入事件」就会失败 —— 这正是审计要求的回归保护。
func TestStaleResourceVersionCannotOverwriteTakeover(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	longMigration(f)
	f.updateState(func(st *fakestate.State) {
		st.LeaseStealBeforeRenewReplace = 1
		st.LeaseStealHolder = "deploy-new-runner-6001"
	})

	res := runDeploy(t, f, append([]string{
		"--migration-timeout", "60",
		"--rollout-timeout", "30",
	}, leaseArgs()...)...)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "续租失败")

	// 1) B 的接管确实发生了（rv 推进 + holder 改写）。
	f.requireEvent(t, "lease.stolen holder=deploy-new-runner-6001")
	// 2) A 的 PUT 被 CAS 拒绝（409），不是「成功了但脚本没注意」。
	f.requireEvent(t, "lease.conflict")
	// 3) A 从未成功写入续租：没有 lease-renew.yaml 的写入事件。
	f.requireNoEvent(t, "lease.write kind=lease-renew.yaml")
	// 4) 最终 holder 仍是 B —— 旧 Runner 的陈旧 rv 不能覆盖新持有者。
	lease := f.state().Lease
	if lease == nil || lease.HolderIdentity != "deploy-new-runner-6001" {
		t.Fatalf("旧持有者不得覆盖新持有者的 holder，实际: %+v", lease)
	}
	// 5) 失锁后不得继续发布。
	f.requireNoEvent(t, "apply.application")
	f.requireNoEvent(t, "lease.release")
}

// TestTakeoverAfterStealKeepsUIDSemantics 覆盖 F-06 的第 3 条：
// 「测试明确模拟对象重建」时才允许 UID 变化；普通接管的 UID 必须保持不变，
// 否则脚本的「同名 Lease 被删除重建」判定会被替身自己制造出来。
func TestTakeoverAfterStealKeepsUIDSemantics(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.seedExpiredLease("NO_MIGRATION", "", "")

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10", "--rollout-timeout", "10")
	res.requireSuccess(t)

	events := f.events()
	if len(events) == 0 {
		t.Fatal("没有事件：接管路径没有执行")
	}
	for _, line := range events {
		if strings.Contains(line, "lease.uid-conflict") {
			t.Fatalf("普通接管不得产生 UID 冲突（UID 只在对象被删除重建时变化）：%s", line)
		}
	}
	// 接管走的是 replace + CAS，不是 delete+create。
	f.requireEvent(t, "lease.acquire")
}
