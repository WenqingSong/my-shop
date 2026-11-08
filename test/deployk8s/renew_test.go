package deployk8s

import (
	"strings"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1.1 / AUD-03 与 T2-06.1.2 / AUD-D7：Lease 续租参数安全。
//
// 不变量（重试链的最坏耗时也计入预算，AUD-D7）：
//   renew_interval + attempts(3) × renew_request_timeout + backoff(1) + safety_margin(5)
//     < lease_duration
// 旧实现只计一次请求超时：10 + 5 + 5 = 20 < 30 看似安全，但一次失败后的重试链最坏
// 需要 3×5=15s，加上间隔后足以跨过租期 —— 那时续租成功也已经太晚。

// TestLeaseParameterInvariantRejectsIllegalCombinations 覆盖非法组合：
// 退出码 1，且**不产生任何 kubectl 调用**（连锁都没碰过）。
func TestLeaseParameterInvariantRejectsIllegalCombinations(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
	}{
		{
			name: "T2-06.1 旧默认值（续租请求比租期还长）",
			args: []string{"--lease-duration", "30", "--lease-renew-interval", "10", "--lease-renew-timeout", "30"},
		},
		{
			name: "只算单次请求（旧公式 10+5+5=20，重试链 10+15+1+5=31 > 30）",
			args: []string{"--lease-duration", "30", "--lease-renew-interval", "10", "--lease-renew-timeout", "5"},
		},
		{
			name: "恰好相等（没有余量）",
			args: []string{"--lease-duration", "25", "--lease-renew-interval", "10", "--lease-renew-timeout", "3"},
		},
		{
			name: "续租间隔过大",
			args: []string{"--lease-duration", "30", "--lease-renew-interval", "20", "--lease-renew-timeout", "3"},
		},
		{
			name: "续租请求超时过大",
			args: []string{"--lease-duration", "30", "--lease-renew-interval", "1", "--lease-renew-timeout", "10"},
		},
		{
			name: "租期过短",
			args: []string{"--lease-duration", "10", "--lease-renew-interval", "1", "--lease-renew-timeout", "3"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, fixtureOptions{})
			res := runDeploy(t, f, tc.args...)

			res.requireCode(t, 1)
			res.requireNoSuccess(t)
			res.requireContains(t, "租期参数不合法", "safety_margin")
			if calls := f.calls(); len(calls) != 0 {
				t.Fatalf("非法参数必须在任何资源变更之前拒绝，实际调用:\n%s", strings.Join(calls, "\n"))
			}
		})
	}
}

// TestLeaseParameterInvariantAcceptsBoundary 覆盖临界合法组合：
// 10 + 3×3 + 1 + 5 = 25 < 26 是「刚好留出 1s 余量」的最小合法配置。
func TestLeaseParameterInvariantAcceptsBoundary(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})

	res := runDeploy(t, f,
		"--lease-duration", "26",
		"--lease-renew-interval", "10",
		"--lease-renew-timeout", "3",
		"--migration-timeout", "10",
		"--rollout-timeout", "10",
	)

	res.requireSuccess(t)
	res.requireContains(t, "10 + 3×3 + 1 + 5 = 25 < 26")
}

// TestRenewUsesDedicatedRequestTimeout 覆盖「续租专用请求超时可控」：
// 只有续租（lease-renew.yaml）使用 --lease-renew-timeout，其他调用仍用默认 30s。
func TestRenewUsesDedicatedRequestTimeout(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	longMigration(f)

	res := runDeploy(t, f,
		"--migration-timeout", "60",
		"--rollout-timeout", "30",
		"--lease-duration", "30",
		"--lease-renew-interval", "1",
		"--lease-renew-timeout", "7",
	)

	res.requireSuccess(t)

	renewCalls := 0
	for _, call := range f.calls() {
		if !strings.Contains(call, "lease-renew.yaml") {
			continue
		}
		renewCalls++
		if !strings.Contains(call, "--request-timeout=7s") {
			t.Fatalf("续租必须使用专用请求超时 7s，实际调用: %s", call)
		}
	}
	if renewCalls < 2 {
		t.Fatalf("续租调用 = %d 次，期望 >= 2（否则无法证明超时参数被真正使用）", renewCalls)
	}
}

// TestRenewRequestTimeoutAbortsDeployment 覆盖 6.2-11：续租请求超时 → 判定失锁 → 退出码 3。
func TestRenewRequestTimeoutAbortsDeployment(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	longMigration(f)
	f.updateState(func(st *fakestate.State) { st.LeaseRenewTimeouts = 1 })

	res := runDeploy(t, f, append([]string{
		"--migration-timeout", "60",
		"--rollout-timeout", "30",
	}, leaseArgs()...)...)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "续租失败")
	f.requireEvent(t, "lease.renew-timeout")
	f.requireNoEvent(t, "apply.application")
}

// TestRenewAPI500AbortsDeployment 覆盖 6.2-12：续租 API 500 → 失锁 → 退出码 3。
func TestRenewAPI500AbortsDeployment(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	longMigration(f)
	f.updateState(func(st *fakestate.State) { st.LeaseRenewFailures = 1 })

	res := runDeploy(t, f, append([]string{
		"--migration-timeout", "60",
		"--rollout-timeout", "30",
	}, leaseArgs()...)...)

	res.requireCode(t, 3)
	res.requireContains(t, "续租失败")
	f.requireEvent(t, "lease.renew-error")
	f.requireNoEvent(t, "apply.application")
}

// TestRenewCASConflictAbortsDeployment 覆盖 6.2-13：resourceVersion 冲突（409）→ 失锁。
func TestRenewCASConflictAbortsDeployment(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	longMigration(f)
	f.updateState(func(st *fakestate.State) { st.LeaseRenewConflicts = 1 })

	res := runDeploy(t, f, append([]string{
		"--migration-timeout", "60",
		"--rollout-timeout", "30",
	}, leaseArgs()...)...)

	res.requireCode(t, 3)
	res.requireContains(t, "续租失败")
	f.requireEvent(t, "lease.renew-conflict")
	f.requireNoEvent(t, "apply.application")
}

// TestRenewUIDMismatchAbortsDeployment 覆盖 6.2-14：Lease uid 不一致（对象被删除重建）→ 失锁。
func TestRenewUIDMismatchAbortsDeployment(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	longMigration(f)
	f.updateState(func(st *fakestate.State) { st.LeaseRenewUIDConflicts = 1 })

	res := runDeploy(t, f, append([]string{
		"--migration-timeout", "60",
		"--rollout-timeout", "30",
	}, leaseArgs()...)...)

	res.requireCode(t, 3)
	res.requireContains(t, "续租失败")
	f.requireEvent(t, "lease.renew-uid-conflict")
	f.requireNoEvent(t, "apply.application")
}

// TestReleaseDoesNotRaceWithRenewer 覆盖 6.2-18：后台续租与 cleanup 并发。
//
// 断言：发布成功后 holder 一定被清空，且释放之后不再出现续租写入
// （释放后仍续租会让下一次发布多等一个租期，甚至误判锁仍被占用）。
func TestReleaseDoesNotRaceWithRenewer(t *testing.T) {
	t.Parallel()

	for round := 0; round < 3; round++ {
		f := newFixture(t, fixtureOptions{})
		slowMigration(f)

		res := runDeploy(t, f, append([]string{
			"--migration-timeout", "60",
			"--rollout-timeout", "30",
		}, leaseArgs()...)...)
		res.requireSuccess(t)

		events := f.events()
		lastRelease := -1
		for i, line := range events {
			if strings.HasPrefix(line, "lease.release") {
				lastRelease = i
			}
		}
		if lastRelease < 0 {
			t.Fatalf("第 %d 轮：缺少 lease.release；事件:\n%s", round, strings.Join(events, "\n"))
		}
		for i := lastRelease + 1; i < len(events); i++ {
			if strings.HasPrefix(events[i], "lease.renew") {
				t.Fatalf("第 %d 轮：释放之后仍出现续租（%s）；事件:\n%s", round, events[i], strings.Join(events, "\n"))
			}
		}
		if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "" {
			t.Fatalf("第 %d 轮：发布结束后 holder 必须为空，实际: %+v", round, lease)
		}
	}
}
