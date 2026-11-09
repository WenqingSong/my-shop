package deployk8s

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1.1 / AUD-03、T2-06.1.2 / AUD-D7 与 T2-06.1.3 / F-02：Lease 续租参数安全。
//
// 一次续租 attempt 的真实执行路径（以 scripts/deploy-k8s.sh 的 lease_renew 为准）：
//
//	GET Lease（≤ get_timeout）→ 校验 holder/uid/是否已过期 → PUT Lease（CAS，≤ replace_timeout）
//
// 失败后最多重试 attempts 次，期间 (attempts-1) 次退避。因此不变量是：
//
//	renewal_budget = renew_interval
//	               + attempts × (get_timeout + replace_timeout)
//	               + total_backoff
//	               + safety_margin
//	               < lease_duration
//
// 默认：10 + 3×(3+3) + 1 + 5 = 34 < 45。
//
// 旧公式只计「单次请求超时」（attempts × timeout），漏掉了每次 attempt 的 GET：
// 默认参数下算出来是 25 < 30，看似安全，真实最坏值却是 34 > 30 —— 一次 API 抖动就能
// 让续租链在锁过期之后才结束。T2-06.1.3 的审计（F-02）正是基于这一点。

// TestLeaseParameterInvariantRejectsIllegalCombinations 覆盖非法组合：
// 退出码 1，且**不产生任何 kubectl 调用**（连锁都没碰过）。
func TestLeaseParameterInvariantRejectsIllegalCombinations(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
	}{
		{
			name: "预算恰好等于租期（相等也不行：没有留给下一次续租的窗口）",
			args: []string{"--lease-duration", "34", "--lease-renew-interval", "10", "--lease-renew-timeout", "3"},
		},
		{
			name: "预算超过租期（T2-06.1.2 的默认 30s 租期在新公式下已不合法）",
			args: []string{"--lease-duration", "30", "--lease-renew-interval", "10", "--lease-renew-timeout", "3"},
		},
		{
			name: "T2-06.1 旧默认值（续租请求比租期还长）",
			args: []string{"--lease-duration", "30", "--lease-renew-interval", "10", "--lease-renew-timeout", "30"},
		},
		{
			name: "续租间隔过大",
			args: []string{"--lease-duration", "45", "--lease-renew-interval", "25", "--lease-renew-timeout", "3"},
		},
		{
			name: "续租请求超时过大（GET+REPLACE 各一次）",
			args: []string{"--lease-duration", "45", "--lease-renew-interval", "1", "--lease-renew-timeout", "10"},
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
			// 报错必须点名 GET 与 REPLACE 两段预算，避免读者误以为只算一次请求。
			res.requireContains(t, "租期参数不合法", "get_timeout", "replace_timeout", "safety_margin")
			if calls := f.calls(); len(calls) != 0 {
				t.Fatalf("非法参数必须在任何资源变更之前拒绝，实际调用:\n%s", strings.Join(calls, "\n"))
			}
			if lease := f.state().Lease; lease != nil {
				t.Fatalf("非法参数不得创建/修改 Lease，实际: %+v", lease)
			}
		})
	}
}

// TestLeaseDefaultParametersPassInvariant 覆盖「默认配置通过」：
// 10 + 3×(3+3) + 1 + 5 = 34 < 45，且日志把两段超时分别列出。
func TestLeaseDefaultParametersPassInvariant(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t,
		"10 + 3×(3+3) + 1 + 5 = 34 < 45",
		"单次 attempt 最坏 6s",
		"续租链最坏 29s",
	)
	if lease := f.state().Lease; lease == nil || lease.DurationSeconds != 45 {
		t.Fatalf("Lease 的 leaseDurationSeconds 必须是默认租期 45，实际: %+v", lease)
	}
}

// TestLeaseParameterInvariantAcceptsBoundary 覆盖临界合法组合：
// 10 + 3×(3+3) + 1 + 5 = 34 < 35 是默认参数下「刚好留出 1s 余量」的最小合法配置。
func TestLeaseParameterInvariantAcceptsBoundary(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})

	res := runDeploy(t, f,
		"--lease-duration", "35",
		"--lease-renew-interval", "10",
		"--lease-renew-timeout", "3",
		"--migration-timeout", "10",
		"--rollout-timeout", "10",
	)

	res.requireSuccess(t)
	res.requireContains(t, "10 + 3×(3+3) + 1 + 5 = 34 < 35")
}

// TestHelpDefaultsMatchCode 覆盖 F-03：`--help` 里的默认值必须与代码默认值一致。
//
// 只比对「同一份源码里的两处文字」是不够的（两处可以一起写错），因此这里同时断言
// 运行时打印的租期/间隔/超时，三者必须完全一致。
func TestHelpDefaultsMatchCode(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})

	help := f.run("--help")
	help.requireCode(t, 0)
	help.requireContains(t,
		"--lease-duration <sec>        发布锁租期（默认 45",
		"--lease-renew-interval <sec>  发布锁续租间隔（默认 10）",
		"--lease-renew-timeout <sec>   单次续租请求的超时（默认 3；GET 与 REPLACE 各算一次）",
		"默认值：10 + 3×(3+3) + 1 + 5 = 34 < 45",
	)
	// --help 不得触发任何集群操作。
	if calls := f.calls(); len(calls) != 0 {
		t.Fatalf("--help 不得调用 kubectl，实际调用:\n%s", strings.Join(calls, "\n"))
	}

	// 运行时日志必须与 help 中的默认值一致（3/10/45）。
	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")
	res.requireSuccess(t)
	res.requireContains(t, "租期 45s，续租间隔 10s，续租请求超时 3s")
}

// TestRenewUsesDedicatedRequestTimeout 覆盖「续租专用请求超时可控」：
// 只有续租（lease-renew.yaml）与锁读取使用 --lease-renew-timeout，其他调用仍用默认 30s。
func TestRenewUsesDedicatedRequestTimeout(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	longMigration(f)

	res := runDeploy(t, f,
		"--migration-timeout", "60",
		"--rollout-timeout", "30",
		"--lease-duration", "60",
		"--lease-renew-interval", "1",
		"--lease-renew-timeout", "7",
	)

	res.requireSuccess(t)

	renewCalls := 0
	for _, call := range f.calls() {
		if !strings.Contains(call, "lease-renew.yaml") &&
			!strings.Contains(call, " get lease ") {
			continue
		}
		renewCalls++
		if !strings.Contains(call, "--request-timeout=7s") {
			t.Fatalf("锁读取/续租必须使用专用请求超时 7s，实际调用: %s", call)
		}
	}
	if renewCalls < 2 {
		t.Fatalf("续租相关调用 = %d 次，期望 >= 2（否则无法证明超时参数被真正使用）", renewCalls)
	}
}

// TestRenewReplaceTimeoutAbortsDeployment 覆盖续租 REPLACE 超时 → 判定失锁 → 退出码 3。
func TestRenewReplaceTimeoutAbortsDeployment(t *testing.T) {
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

// TestRenewGetTimeoutAbortsDeployment 覆盖续租 GET 超时 → 判定失锁 → 退出码 3。
//
// 为什么需要单独一条：GET 与 REPLACE 是两个独立请求，GET 失败时 lease_read 直接返回
// 非零，不会进入 CAS；旧实现只把「一次请求」计入预算，正是因为默认「GET 不会失败」。
//
// 注入时机必须确定：迁移进入 RUNNING 之后，主进程在等待循环里不会读 Lease（下一次读是
// 终态写入），此后的第一次 get lease 只可能来自后台续租。迁移被设置为永远不会 Complete，
// 因此不存在「主进程读消耗掉注入」的竞争窗口。
func TestRenewGetTimeoutAbortsDeployment(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		st.JobScript = []fakestate.Job{{Exists: true}, {Exists: true}, {Exists: true}}
	})

	p := f.start(append([]string{
		"--migration-timeout", "12",
		"--rollout-timeout", "10",
	}, leaseArgs()...)...)
	// 记录写入 RUNNING 之后：主进程只剩「等待 Job」，不会再读 Lease。
	f.waitForEvent(t, "lease.write kind=lease-record.yaml state=MIGRATION_RUNNING", 30*time.Second)
	f.updateState(func(st *fakestate.State) { st.LeaseGetTimeouts = 1 })

	res := p.wait(t, 120*time.Second)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "发布锁续租失败")
	f.requireEvent(t, "lease.get-timeout")
	f.requireNoEvent(t, "apply.application")
}

// TestRenewAPI500AbortsDeployment 覆盖续租 API 500 → 失锁 → 退出码 3。
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

// TestRenewThreeCASConflictsExhaustAttempts 覆盖「连续三次 CAS 冲突」：
// attempts 用尽后判定续租失败，且每次冲突都重新读取 Lease（不会用旧 rv 盲写）。
func TestRenewThreeCASConflictsExhaustAttempts(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	longMigration(f)
	f.updateState(func(st *fakestate.State) { st.LeaseRenewConflicts = 1 })

	res := runDeploy(t, f, append([]string{
		"--migration-timeout", "60",
		"--rollout-timeout", "30",
	}, leaseArgs()...)...)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "续租失败")

	if got := f.countEvents("lease.renew-conflict"); got != 3 {
		t.Fatalf("续租冲突 = %d 次，期望 3（attempts=3 用尽才判定失锁）；事件:\n%s",
			got, strings.Join(f.events(), "\n"))
	}
	// 三次 attempt 各读一次 Lease：审计 F-02 的核心就是「attempt 里真的有 GET」。
	// 判据是「每个 attempt 窗口（上一次续租 replace 之后）内至少有一次 get lease」，
	// 而不是「紧邻上一行是 get lease」：全局调用日志里主线程的
	// `replace -f lease-record.yaml` 会与续租 attempt 正常交错（假耦合，见
	// renewAttemptsStartingWithGet 的注释）。
	attempts, problems := renewAttemptsStartingWithGet(f)
	if attempts != 3 {
		t.Fatalf("续租 replace = %d 次，期望 3；调用:\n%s", attempts, strings.Join(f.calls(), "\n"))
	}
	if len(problems) > 0 {
		t.Fatalf("每个续租 attempt 都必须先 get lease 再 PUT（不允许用陈旧 rv 盲写）：\n%s\n调用:\n%s",
			strings.Join(problems, "\n"), strings.Join(f.calls(), "\n"))
	}
	// 冲突不是「写入成功」：不得出现成功续租的写入事件。
	f.requireNoEvent(t, "lease.write kind=lease-renew.yaml")
	f.requireNoEvent(t, "apply.application")
}

// TestRenewAbortsWhenLeaseExpiresDuringRetries 覆盖 F-02.4：
// 重试链比租期还长、服务器上的租约在重试期间过期 → 不得继续盲写，直接判定失锁。
//
// 这是审计点名的场景：上一次续租已经失效、新 Runner 已经可以接管，而旧 Runner 仍在
// 执行 GET/REPLACE。旧实现会「立即续租」把租约复活（等于在接管窗口里继续写入）。
func TestRenewAbortsWhenLeaseExpiresDuringRetries(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	longMigration(f)
	f.updateState(func(st *fakestate.State) {
		st.LeaseRenewConflicts = 1
		// 第一次冲突之后租约在服务端过期（模拟重试链耗尽租期）。
		st.LeaseExpireAfterRenewConflicts = 1
	})

	res := runDeploy(t, f, append([]string{
		"--migration-timeout", "60",
		"--rollout-timeout", "30",
	}, leaseArgs()...)...)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t,
		"租约已过期",
		"其他 Runner 已可合法接管",
		"不再写入 Lease",
		"不通过「立即续租」复活",
	)
	f.requireEvent(t, "lease.expired")
	f.requireNoEvent(t, "lease.write kind=lease-renew.yaml")
	f.requireNoEvent(t, "apply.application")

	// 过期之后不得再有任何 Lease 写入（释放也不行：那不是本 Runner 的锁了）。
	events := f.events()
	expiredAt := -1
	for i, line := range events {
		if strings.HasPrefix(line, "lease.expired") {
			expiredAt = i
		}
	}
	if expiredAt < 0 {
		t.Fatalf("缺少 lease.expired 事件；事件:\n%s", strings.Join(events, "\n"))
	}
	for _, line := range events[expiredAt+1:] {
		if strings.HasPrefix(line, "lease.write") || strings.HasPrefix(line, "lease.release") {
			t.Fatalf("租约过期后不得再写 Lease，实际事件: %s\n事件序列:\n%s", line, strings.Join(events, "\n"))
		}
	}
}

// TestRenewChainStaysWithinBudget 覆盖「不能只验证参数计算正确，还要验证实际执行耗时」：
// 让 GET 与 REPLACE 都接近超时上限（各 0.9s / timeout 1s），三次 attempt 全部失败，
// 用外部包装记录的时间戳测量真实续租链耗时，断言它不超过公式给出的上界。
//
// 断言三件事：
//  1. 链里确实有 3 次 GET + 3 次 REPLACE（attempt 是「两个请求」，不是「一个」）；
//  2. 实测耗时 ≥ 3×(get+replace)：证明测试真的制造了慢请求，不是空转；
//  3. 实测耗时 ≤ attempts×(get+replace)+backoff，且 < lease_duration。
func TestRenewChainStaysWithinBudget(t *testing.T) {
	t.Parallel()

	const (
		leaseTimeout = 1 * time.Second
		requestDelay = 900 * time.Millisecond // 接近超时上限（90%）
		attempts     = 3
		backoffBudge = 1 * time.Second
		leaseLength  = 25 * time.Second
	)

	f := newFixture(t, fixtureOptions{
		leaseGetDelay:   requestDelay,
		leaseRenewDelay: requestDelay,
	})
	// 迁移持续时间必须长于续租链，否则主进程先结束会 stop 掉续租进程。
	f.updateState(func(st *fakestate.State) {
		st.JobScript = []fakestate.Job{
			{Exists: true}, {Exists: true}, {Exists: true}, {Exists: true},
			{Exists: true}, {Exists: true}, {Exists: true, Complete: true},
		}
		st.LeaseRenewFailures = 1
	})

	res := runDeploy(t, f,
		"--lease-duration", fmt.Sprintf("%d", int(leaseLength.Seconds())),
		"--lease-renew-interval", "1",
		"--lease-renew-timeout", fmt.Sprintf("%d", int(leaseTimeout.Seconds())),
		"--migration-timeout", "60",
		"--rollout-timeout", "30",
	)

	res.requireCode(t, 3)
	res.requireContains(t, "续租失败")

	timings := f.timings()
	if len(timings) == 0 {
		t.Fatal("timing.log 为空：外部包装未生效，无法测量实际耗时")
	}

	var renews []int
	for i, entry := range timings {
		if isRenewReplace(entry.args) {
			renews = append(renews, i)
		}
	}
	if len(renews) != attempts {
		t.Fatalf("续租 replace = %d 次，期望 %d（attempts 用尽才判定失锁）\n完整时间线:\n%s",
			len(renews), attempts, formatTimings(timings))
	}

	// 相邻两次续租 replace 的间隔 = GET 耗时 + REPLACE 耗时 + 退避。
	// 若 attempt 里没有 GET（例如只重发 PUT），间隔只有 REPLACE 耗时 + 退避，
	// 因此这条断言直接证明「一次 attempt 真的是两个请求」。
	minSpacing := requestDelay + requestDelay
	for i := 1; i < len(renews); i++ {
		spacing := timings[renews[i]].at.Sub(timings[renews[i-1]].at)
		if spacing < minSpacing {
			t.Fatalf("第 %d→%d 次续租间隔 %s < %s（GET+REPLACE）：attempt 里少了 GET？\n完整时间线:\n%s",
				i, i+1, spacing, minSpacing, formatTimings(timings))
		}
	}

	// wrapper 在 sleep **之前**记录时间戳：第一次 attempt 的 GET 时刻 ≈ 首次 replace 时刻 - GET 延迟。
	chainStart := timings[renews[0]].at.Add(-requestDelay)
	chainEnd := timings[renews[len(renews)-1]].at.Add(requestDelay)
	elapsed := chainEnd.Sub(chainStart)
	// 下界只算注入的请求延迟（不含真实退避），保证断言不会因噪声变成空转。
	slowFloor := time.Duration(attempts) * 2 * requestDelay
	budgetChain := time.Duration(attempts)*2*leaseTimeout + backoffBudge
	t.Logf("续租链实测 %s；公式上界 %s（attempts×(get+replace)+backoff）；租期 %s",
		elapsed, budgetChain, leaseLength)

	if elapsed < slowFloor {
		t.Fatalf("续租链实测 %s 小于 %s：测试没有真正制造慢请求（断言会变成空转）", elapsed, slowFloor)
	}
	if elapsed > budgetChain {
		t.Fatalf("续租链实测 %s 超出预算 %s：参数不变量没有覆盖真实执行路径（GET 是否被漏算？）",
			elapsed, budgetChain)
	}
	if elapsed >= leaseLength {
		t.Fatalf("续租链实测 %s >= 租期 %s：锁会在续租完成之前过期", elapsed, leaseLength)
	}
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

// isLeaseRead 判断一次 kubectl 调用是否是读取锁对象（`get lease <name>`）。
func isLeaseRead(args string) bool {
	return strings.Contains(" "+args+" ", " get lease ")
}

// isRenewReplace 判断一次 kubectl 调用是否是续租写入（`replace -f .../lease-renew.yaml`）。
func isRenewReplace(args string) bool {
	return strings.Contains(args, "replace") && strings.Contains(args, "lease-renew.yaml")
}

// renewAttemptsStartingWithGet 返回续租 replace 的次数，并逐次检查「本次 attempt 的
// replace 之前，上一次续租 replace 之后，至少有一次 get lease」。
//
// 这是「一次 attempt = GET + REPLACE」在调用序列上的直接证据（F-02 的核心：
// 每次都重读 rv，绝不拿陈旧 rv 盲写）。**不断言「紧邻上一次调用是 get lease」** ——
// 那是一条虚假耦合（T2-06.1.4 追加修复）：
//
//   - f.calls() 是**所有进程**共享的全局调用日志：后台续租子 shell 与主线程会并发写它；
//   - 主线程的 migration_record_write 会插入 `get lease` + `replace -f lease-record.yaml`，
//     完全可能落在「续租的 GET」与「续租的 PUT」之间，序列形如：
//     get lease（续租）→ replace -f lease-record.yaml（主线程）→ replace -f lease-renew.yaml（续租）
//     旧断言只看到 replace 的前一行是 lease-record.yaml，就判失败 —— 但被测语义
//     （attempt 内部先 GET 后 PUT）其实完全成立。
//   - 实测：该失败只在「全量并发 + 机器高负载」下偶发（C 的 fake 改成 flock 串行 +
//     全局 rv 后时序变化把它暴露出来），单测 5 轮、基线 3 轮全量均无法复现。
//
// 强度没有降低：窗口内必须有 get lease，因此「漏掉 GET 直接 PUT」仍然会被抓住
// （TestRenewChainStaysWithinBudget 断言的正是没有 record 写入干扰的纯续租链；
// 如果 attempt 少了 GET，相邻 replace 的间隔会小于 GET+REPLACE 之和）。
func renewAttemptsStartingWithGet(f *fixture) (int, []string) {
	return classifyRenewAttempts(f.calls())
}

// classifyRenewAttempts 是 renewAttemptsStartingWithGet 的纯函数内核（便于用合成
// 调用序列做确定性回归，不依赖并发时序）：
//
//	每个续租 replace 都必须在「上一次续租 replace 之后」见过至少一次 get lease。
func classifyRenewAttempts(calls []string) (int, []string) {
	attempts := 0
	var problems []string
	// sawGet：自上一次续租 replace 以来，是否见过 get lease。
	sawGet := false
	for _, call := range calls {
		switch {
		case isLeaseRead(call):
			sawGet = true
		case isRenewReplace(call):
			attempts++
			if !sawGet {
				problems = append(problems, fmt.Sprintf(
					"第 %d 次续租 replace 之前（上一次续租之后）没有任何 get lease：attempt 用陈旧 rv 盲写", attempts))
			}
			// 本次 attempt 的窗口到此结束；下一次 attempt 必须重新 GET。
			sawGet = false
		}
	}
	return attempts, problems
}

func formatTimings(entries []timingEntry) string {
	var builder strings.Builder
	base := entries[0].at
	for _, entry := range entries {
		fmt.Fprintf(&builder, "+%6dms  %s\n", entry.at.Sub(base).Milliseconds(), entry.args)
	}
	return builder.String()
}

// TestRenewBackoffBudgetIsSingleSourced 覆盖 T2-06.1.4 / B-5：
//
// 审计发现 LOCK_RENEW_BACKOFF_MS 是**死常量** —— 校验用 (attempts-1)×200ms=400ms
// 的预算，真实实现却对每次失败（含最后一次）都 sleep 0.2s，共 600ms，比预算多 200ms。
//
// 这条契约测试直接检查源码：常量、预算、以及 lease_renew 里的真实 sleep 必须同源。
// 它比"测量耗时"更稳定（不会因 CI 抖动变成空转），而真实耗时由
// TestRenewChainStaysWithinBudget 单独测量。
func TestRenewBackoffBudgetIsSingleSourced(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join(repoRootPath, "scripts", "deploy-k8s.sh"))
	if err != nil {
		t.Fatalf("读取 scripts/deploy-k8s.sh: %v", err)
	}
	script := string(raw)

	constInt := func(name string) int {
		t.Helper()
		re := regexp.MustCompile(`(?m)^` + name + `=([0-9]+)$`)
		m := re.FindStringSubmatch(script)
		if m == nil {
			t.Fatalf("脚本里找不到整数常量 %s", name)
		}
		value, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("解析 %s: %v", name, err)
		}
		return value
	}
	attempts := constInt("LOCK_RENEW_MAX_ATTEMPTS")
	backoffMS := constInt("LOCK_RENEW_BACKOFF_MS")
	budget := constInt("LOCK_RENEW_BACKOFF_BUDGET")

	// 真实退避 = 只在 attempt 之间退避 (attempts-1) 次，预算必须是它的向上取整。
	wantMS := (attempts - 1) * backoffMS
	wantBudget := (wantMS + 999) / 1000
	if budget != wantBudget {
		t.Fatalf("退避预算 %ds 与真实退避 (%d-1)×%dms=%dms（向上取整 %ds）不一致：预算与实现必须同源",
			budget, attempts, backoffMS, wantMS, wantBudget)
	}

	body := shellFunctionBody(t, script, "lease_renew")
	// 真实 sleep 必须由常量换算（而不是硬编码 0.2s）。
	if !strings.Contains(body, `sleep "$(sleep_ms_arg "${LOCK_RENEW_BACKOFF_MS}")"`) {
		t.Fatalf("lease_renew 必须用 sleep_ms_arg 从 LOCK_RENEW_BACKOFF_MS 换算退避；实际函数体:\n%s", body)
	}
	if regexp.MustCompile(`(?m)^\s*sleep [0-9]`).MatchString(body) {
		t.Fatalf("lease_renew 里仍有硬编码 sleep：退避时长必须只有一个数值来源\n%s", body)
	}
	// 最后一次失败后不得再 sleep（否则 (attempts-1) 次退避会变成 attempts 次）。
	if !strings.Contains(body, "attempt < LOCK_RENEW_MAX_ATTEMPTS") {
		t.Fatalf("退避必须在 attempt 之间进行（attempt < LOCK_RENEW_MAX_ATTEMPTS 守卫）；实际:\n%s", body)
	}
	// usage 文本里对外公布的 backoff 数值必须与预算一致（避免文档与实现漂移）。
	if !strings.Contains(script, fmt.Sprintf("backoff(%d)", budget)) {
		t.Fatalf("usage/日志里公布的 backoff(%d) 与实际预算 %ds 不一致", budget, budget)
	}
}

// shellFunctionBody 返回 `name() {` 到对应收尾 `}` 之间的函数体（含首行）。
func shellFunctionBody(t *testing.T, script, name string) string {
	t.Helper()
	start := strings.Index(script, "\n"+name+"() {")
	if start < 0 {
		t.Fatalf("脚本里找不到函数 %s", name)
	}
	rest := script[start+1:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("函数 %s 没有找到收尾", name)
	}
	return rest[:end+2]
}

// TestRenewAttemptAdjacencyClassifier 用**合成调用序列**证明修复既消除了虚假耦合、
// 又没有放宽断言强度（F-02：每个 attempt 必须先 GET 再 PUT）。
//
// 这些序列都是真实日志里出现过的形态（时间线来自全量并发跑出来的失败样本）。
func TestRenewAttemptAdjacencyClassifier(t *testing.T) {
	t.Parallel()

	const (
		readLease = `-n surgecart get lease surgecart-deploy-lock -o go-template=... --request-timeout=3s`
		putRenew  = `-n surgecart replace -f /tmp/lock/lease-renew.yaml --request-timeout=3s`
		putRecord = `-n surgecart replace -f /tmp/lock/lease-record.yaml`
		putApply  = `-n surgecart apply -f manifest/kubernetes/overlays/production`
	)

	cases := []struct {
		name     string
		calls    []string
		attempts int
		problems int
	}{
		{
			name: "理想序列：GET→PUT 交替",
			calls: []string{
				readLease, putRenew,
				readLease, putRenew,
				readLease, putRenew,
			},
			attempts: 3, problems: 0,
		},
		{
			// 这就是当初失败的样本：主线程的 record 写入插在续租 GET 与 PUT 之间。
			// 续租 attempt 内部仍然是「先 GET 后 PUT」，旧断言却因"前一行不是 get lease"而误报。
			name: "主线程 record 写入插在续租 GET 与 PUT 之间（旧断言的假阳性）",
			calls: []string{
				readLease,
				putRecord,
				putRenew,
				readLease,
				putRenew,
			},
			attempts: 2, problems: 0,
		},
		{
			name: "主线程的 get lease + record 写入都插在中间",
			calls: []string{
				readLease,
				readLease,
				putRecord,
				putRenew,
				readLease,
				putRenew,
			},
			attempts: 2, problems: 0,
		},
		{
			// 强度证明：attempt 之间没有 GET 就是盲写（陈旧 rv），必须被抓住。
			name: "第 2 次 attempt 没有 GET（盲写）",
			calls: []string{
				readLease, putRenew,
				putRenew,
			},
			attempts: 2, problems: 1,
		},
		{
			// 连一次 GET 都没有：任何 replace 都是盲写。
			name: "整条链没有 GET",
			calls: []string{
				putRenew,
				putRenew,
			},
			attempts: 2, problems: 2,
		},
		{
			name: "无关调用（apply）穿插不影响判定",
			calls: []string{
				putApply,
				readLease, putRenew,
				putApply,
				readLease, putRenew,
			},
			attempts: 2, problems: 0,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			attempts, problems := classifyRenewAttempts(tc.calls)
			if attempts != tc.attempts {
				t.Fatalf("续租 replace 计数 = %d，期望 %d", attempts, tc.attempts)
			}
			if len(problems) != tc.problems {
				t.Fatalf("问题数 = %d，期望 %d（问题: %v）", len(problems), tc.problems, problems)
			}
		})
	}
}
