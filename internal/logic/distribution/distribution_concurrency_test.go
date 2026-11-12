package distribution

// 推荐关系图并发成环回归测试（B1-04-FIX-01）。
//
// 目的：证明「并发成环检查 + 关系写入」由 MySQL 级全局锁串行化，而不是依赖进程内 mutex、
// 一致性读快照或唯一索引兜底。全部用例使用真实 MySQL（不使用 Mock），并通过多个数据库连接
// 与同步屏障制造真正的并发事务；断言对象是数据库中的最终关系图，而不是 HTTP/函数返回值。
//
// 与 `go test -race` 的关系：race detector 只检查 Go 内存数据竞争，无法替代本文件对
// InnoDB 行锁、事务隔离与提交顺序的验证；两者必须同时执行（见交付报告）。

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// bindOutcome 是一次 BindReferral 调用的结果（错误码 + 原始错误）。
type bindOutcome struct {
	code codes.Code
	err  error
}

// success 表示绑定成功（幂等返回也视为成功）。
func (o bindOutcome) success() bool { return o.err == nil }

// cycleRejected 表示被成环检查拒绝（20004）。
func (o bindOutcome) cycleRejected() bool { return o.code == codes.CodeDistributionCycle }

// conflictRejected 表示已被其他推荐人占用（20003，不可改绑）。
func (o bindOutcome) conflictRejected() bool { return o.code == codes.CodeDistributionAlreadyBound }

// bindEdge 执行一次绑定（供并发 goroutine 调用，不做 t.Fatalf）。
func bindEdge(userID, referrerID int64) bindOutcome {
	_, err := distService().BindReferral(context.Background(), userID, referrerID)
	return bindOutcome{code: codes.FromError(err), err: err}
}

// runConcurrentBinds 用同步屏障让所有绑定尽量同时进入数据库事务。
//
// 屏障只影响「开始时刻」，正确性不依赖线程调度：每个绑定各自独立连接、独立事务，
// 串行化由 referral_graph_guard 行锁保证。
func runConcurrentBinds(pairs [][2]int64) []bindOutcome {
	start := make(chan struct{})
	outcomes := make([]bindOutcome, len(pairs))
	var ready, done sync.WaitGroup
	ready.Add(len(pairs))
	done.Add(len(pairs))
	for i, pair := range pairs {
		go func(i int, userID, referrerID int64) {
			defer done.Done()
			ready.Done()
			<-start
			outcomes[i] = bindEdge(userID, referrerID)
		}(i, pair[0], pair[1])
	}
	ready.Wait()
	close(start)
	done.Wait()
	return outcomes
}

// loadReferralGraph 读取全表推荐关系（child → referrer）。
func loadReferralGraph(t *testing.T) map[int64]int64 {
	t.Helper()
	var rows []*referralRow
	if err := g.DB().Model("user_referrals").Ctx(context.Background()).Scan(&rows); err != nil {
		t.Fatalf("读取推荐关系图: %v", err)
	}
	graph := make(map[int64]int64, len(rows))
	for _, row := range rows {
		if _, exists := graph[row.UserId]; exists {
			t.Fatalf("用户 %d 存在多条推荐关系（违反 uk_user_id）", row.UserId)
		}
		graph[row.UserId] = row.ReferrerUserId
	}
	return graph
}

// assertAcyclicGraph 校验最终关系图无环、无自荐。
func assertAcyclicGraph(t *testing.T, graph map[int64]int64) {
	t.Helper()
	for child, referrer := range graph {
		if child == referrer {
			t.Fatalf("存在自荐关系：%d → %d", child, referrer)
		}
	}
	state := make(map[int64]int64, len(graph)) // 0=未访问 1=访问中 2=已完成
	var visit func(node int64) error
	visit = func(node int64) error {
		switch state[node] {
		case 1:
			return fmt.Errorf("推荐关系成环，节点 %d", node)
		case 2:
			return nil
		}
		state[node] = 1
		if next, ok := graph[node]; ok && next > 0 {
			if err := visit(next); err != nil {
				return err
			}
		}
		state[node] = 2
		return nil
	}
	for node := range graph {
		if err := visit(node); err != nil {
			t.Fatal(err)
		}
	}
}

// Test 一：双向并发互绑 A → B / B → A，最多一条成功，最终无环。
func TestBindConcurrentMutualCycleRejected(t *testing.T) {
	setupDistributionTest(t)
	a := insertDistUser(t, "ccmutuala", 1)
	b := insertDistUser(t, "ccmutualb", 1)

	outcomes := runConcurrentBinds([][2]int64{{a, b}, {b, a}})

	success, cycles := 0, 0
	for _, o := range outcomes {
		switch {
		case o.success():
			success++
		case o.cycleRejected():
			cycles++
		default:
			t.Fatalf("双向互绑出现非预期结果: code=%d err=%v", o.code, o.err)
		}
	}
	if success != 1 || cycles != 1 {
		t.Fatalf("双向互绑期望 1 成功 + 1 成环拒绝，实际 success=%d cycle=%d", success, cycles)
	}
	graph := loadReferralGraph(t)
	if len(graph) != 1 {
		t.Fatalf("双向互绑后关系数 = %d，期望 1", len(graph))
	}
	assertAcyclicGraph(t, graph)
}

// Test 二：三节点并发 A → B / B → C / C → A，最多两条关系落库，第三条成环被拒。
func TestBindConcurrentThreeNodeCycleRejected(t *testing.T) {
	setupDistributionTest(t)
	a := insertDistUser(t, "ccthreea", 1)
	b := insertDistUser(t, "ccthreeb", 1)
	c := insertDistUser(t, "ccthreec", 1)

	outcomes := runConcurrentBinds([][2]int64{{a, b}, {b, c}, {c, a}})

	success, cycles := 0, 0
	for _, o := range outcomes {
		switch {
		case o.success():
			success++
		case o.cycleRejected():
			cycles++
		default:
			t.Fatalf("三节点并发出现非预期结果: code=%d err=%v", o.code, o.err)
		}
	}
	if success != 2 || cycles != 1 {
		t.Fatalf("三节点成环期望 2 成功 + 1 成环拒绝，实际 success=%d cycle=%d", success, cycles)
	}
	graph := loadReferralGraph(t)
	if len(graph) != 2 {
		t.Fatalf("三节点成环后关系数 = %d，期望 2", len(graph))
	}
	assertAcyclicGraph(t, graph)
}

// Test 三：已有长链 A → B → C → D，反向绑定（成环）必须被拒绝且不影响既有关系。
func TestBindRejectsCycleOnLongChain(t *testing.T) {
	setupDistributionTest(t)
	a := insertDistUser(t, "cclonga", 1)
	b := insertDistUser(t, "cclongb", 1)
	c := insertDistUser(t, "cclongc", 1)
	d := insertDistUser(t, "cclongd", 1)

	for _, edge := range [][2]int64{{a, b}, {b, c}, {c, d}} {
		if o := bindEdge(edge[0], edge[1]); !o.success() {
			t.Fatalf("预置长链 %d→%d 失败: code=%d err=%v", edge[0], edge[1], o.code, o.err)
		}
	}

	// D → A 会闭合 A→B→C→D→A。
	if o := bindEdge(d, a); !o.cycleRejected() {
		t.Fatalf("长链反向绑定必须 20004，实际 code=%d err=%v", o.code, o.err)
	}
	// D → C：C 沿链可到达 D（child），同样成环。
	if o := bindEdge(d, c); !o.cycleRejected() {
		t.Fatalf("长链中段反向绑定必须 20004，实际 code=%d err=%v", o.code, o.err)
	}

	graph := loadReferralGraph(t)
	want := map[int64]int64{a: b, b: c, c: d}
	if len(graph) != len(want) {
		t.Fatalf("长链关系数 = %d，期望 %d（既有关系不应被改变）", len(graph), len(want))
	}
	for child, referrer := range want {
		if graph[child] != referrer {
			t.Fatalf("既有关系被改变：%d → %d，期望 %d", child, graph[child], referrer)
		}
	}
	assertAcyclicGraph(t, graph)
}

// Test 四：同一用户并发绑定不同推荐人，只能有一条落库，另一方按既有语义 20003。
func TestBindConcurrentSameUserDifferentReferrers(t *testing.T) {
	setupDistributionTest(t)
	a := insertDistUser(t, "ccsamea", 1)
	b := insertDistUser(t, "ccsameb", 1)
	c := insertDistUser(t, "ccsamec", 1)

	outcomes := runConcurrentBinds([][2]int64{{a, b}, {a, c}})

	success, conflicts := 0, 0
	for _, o := range outcomes {
		switch {
		case o.success():
			success++
		case o.conflictRejected():
			conflicts++
		default:
			t.Fatalf("同一用户并发绑定出现非预期结果: code=%d err=%v", o.code, o.err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("同一用户并发绑定期望 1 成功 + 1 冲突(20003)，实际 success=%d conflict=%d", success, conflicts)
	}
	if n := len(loadReferralGraph(t)); n != 1 {
		t.Fatalf("同一用户并发绑定后关系数 = %d，期望 1", n)
	}
}

// Test 五：相同关系并发重复提交，保持原幂等语义（都成功、只有一行）。
func TestBindConcurrentSameRelationIdempotent(t *testing.T) {
	setupDistributionTest(t)
	a := insertDistUser(t, "ccidema", 1)
	b := insertDistUser(t, "ccidemb", 1)

	outcomes := runConcurrentBinds([][2]int64{{a, b}, {a, b}, {a, b}})
	for i, o := range outcomes {
		if !o.success() {
			t.Fatalf("相同关系并发第 %d 个请求应幂等成功，实际 code=%d err=%v", i, o.code, o.err)
		}
	}
	graph := loadReferralGraph(t)
	if len(graph) != 1 || graph[a] != b {
		t.Fatalf("相同关系并发后关系图 = %v，期望唯一关系 %d→%d", graph, a, b)
	}
	assertAcyclicGraph(t, graph)
}

// Test 六之一：绑定事务确实等待全局锁（证明串行化来源是 referral_graph_guard 行锁）。
func TestBindReferralWaitsForGraphLock(t *testing.T) {
	setupDistributionTest(t)
	a := insertDistUser(t, "cclocka", 1)
	b := insertDistUser(t, "cclockb", 1)

	ctx := context.Background()
	acquired := make(chan struct{})
	release := make(chan struct{})
	holderErr := make(chan error, 1)
	go func() {
		holderErr <- g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
			if _, err := tx.Model("referral_graph_guard").Ctx(ctx).
				Where("id", referralGraphGuardID).LockUpdate().Value("id"); err != nil {
				return err
			}
			close(acquired)
			<-release
			return nil
		})
	}()
	<-acquired

	done := make(chan bindOutcome, 1)
	go func() { done <- bindEdge(a, b) }()

	select {
	case o := <-done:
		t.Fatalf("全局锁被占用时绑定不应完成（说明未使用数据库锁）: code=%d err=%v", o.code, o.err)
	case <-time.After(600 * time.Millisecond):
	}

	close(release)
	if err := <-holderErr; err != nil {
		t.Fatalf("锁持有事务提交失败: %v", err)
	}
	select {
	case o := <-done:
		if !o.success() {
			t.Fatalf("锁释放后绑定应成功，实际 code=%d err=%v", o.code, o.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("锁释放后绑定未在 10s 内完成（疑似锁泄漏）")
	}
	if graph := loadReferralGraph(t); len(graph) != 1 || graph[a] != b {
		t.Fatalf("锁释放后关系图 = %v，期望唯一关系 %d→%d", graph, a, b)
	}
}

// Test 六之二：事务失败后关系不残留、全局锁随事务回滚释放。
func TestBindReferralTxRollbackReleasesLockAndLeavesNoRow(t *testing.T) {
	setupDistributionTest(t)
	a := insertDistUser(t, "ccrollbacka", 1)
	b := insertDistUser(t, "ccrollbackb", 1)

	boom := errors.New("simulated failure after write")
	err := g.DB().Transaction(context.Background(), func(ctx context.Context, tx gdb.TX) error {
		// 复刻 BindReferral 的锁协议：先取全局锁，再写关系，然后主动失败。
		if _, err := tx.Model("referral_graph_guard").Ctx(ctx).
			Where("id", referralGraphGuardID).LockUpdate().Value("id"); err != nil {
			return err
		}
		if _, err := tx.Model("user_referrals").Ctx(ctx).Data(g.Map{
			"user_id":          a,
			"referrer_user_id": b,
		}).Insert(); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("期望模拟失败返回，实际 %v", err)
	}
	if n := len(loadReferralGraph(t)); n != 0 {
		t.Fatalf("事务回滚后不应残留推荐关系，实际 %d 行", n)
	}

	// 锁已随回滚释放：后续绑定应立即成功（同一连接池，若锁泄漏会阻塞直至超时）。
	start := time.Now()
	if o := bindEdge(a, b); !o.success() {
		t.Fatalf("回滚后绑定应成功，实际 code=%d err=%v", o.code, o.err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("回滚后绑定耗时 %s，疑似锁未释放", elapsed)
	}
}

// Test 六之三：锁记录缺失时 fail-closed（拒绝写入，绝不降级为无锁绑定）。
func TestBindReferralFailsClosedWhenGuardRowMissing(t *testing.T) {
	setupDistributionTest(t)
	a := insertDistUser(t, "ccnoguarda", 1)
	b := insertDistUser(t, "ccnoguardb", 1)

	ctx := context.Background()
	if _, err := g.DB().Exec(ctx, "DELETE FROM referral_graph_guard"); err != nil {
		t.Fatalf("删除锁记录: %v", err)
	}
	t.Cleanup(func() {
		if _, err := g.DB().Exec(context.Background(),
			"INSERT IGNORE INTO referral_graph_guard (id) VALUES (1)"); err != nil {
			t.Errorf("恢复锁记录失败: %v", err)
		}
	})

	o := bindEdge(a, b)
	if o.err == nil {
		t.Fatal("锁记录缺失时必须拒绝绑定")
	}
	if o.code != codes.CodeInternalError {
		t.Fatalf("锁记录缺失期望内部错误码，实际 code=%d err=%v", o.code, o.err)
	}
	if n := len(loadReferralGraph(t)); n != 0 {
		t.Fatalf("锁记录缺失时不应写入关系，实际 %d 行", n)
	}
}

// Test 七：多轮并发压力（双向互绑 100 轮），每轮核对数据库最终关系图。
func TestBindConcurrentMutualCycleStress(t *testing.T) {
	setupDistributionTest(t)

	const rounds = 100
	var successes, cycleRejections int
	var seq atomic.Int64
	for round := 0; round < rounds; round++ {
		a := insertDistUser(t, fmt.Sprintf("ccstressa%d", seq.Add(1)), 1)
		b := insertDistUser(t, fmt.Sprintf("ccstressb%d", seq.Add(1)), 1)

		outcomes := runConcurrentBinds([][2]int64{{a, b}, {b, a}})
		success, cycles := 0, 0
		for _, o := range outcomes {
			switch {
			case o.success():
				success++
			case o.cycleRejected():
				cycles++
			default:
				t.Fatalf("第 %d 轮出现非预期结果: code=%d err=%v", round, o.code, o.err)
			}
		}
		if success != 1 || cycles != 1 {
			t.Fatalf("第 %d 轮双向互绑期望 1 成功 + 1 成环拒绝，实际 success=%d cycle=%d", round, success, cycles)
		}
		graph := loadReferralGraph(t)
		if len(graph) != round+1 {
			t.Fatalf("第 %d 轮关系总数 = %d，期望 %d", round, len(graph), round+1)
		}
		assertAcyclicGraph(t, graph)
		successes += success
		cycleRejections += cycles
	}
	if successes != rounds || cycleRejections != rounds {
		t.Fatalf("压力结果统计异常：success=%d cycle=%d，期望各 %d", successes, cycleRejections, rounds)
	}
	t.Logf("双向互绑 %d 轮：成功 %d / 成环拒绝 %d / 最终关系数 %d / 无环",
		rounds, successes, cycleRejections, len(loadReferralGraph(t)))
}

// Test 七之二：三节点成环 100 轮压力，每轮核对最终关系图。
func TestBindConcurrentThreeNodeCycleStress(t *testing.T) {
	setupDistributionTest(t)

	const rounds = 100
	var successes, cycleRejections int
	var seq atomic.Int64
	for round := 0; round < rounds; round++ {
		a := insertDistUser(t, fmt.Sprintf("ccstress3a%d", seq.Add(1)), 1)
		b := insertDistUser(t, fmt.Sprintf("ccstress3b%d", seq.Add(1)), 1)
		c := insertDistUser(t, fmt.Sprintf("ccstress3c%d", seq.Add(1)), 1)

		outcomes := runConcurrentBinds([][2]int64{{a, b}, {b, c}, {c, a}})
		success, cycles := 0, 0
		for _, o := range outcomes {
			switch {
			case o.success():
				success++
			case o.cycleRejected():
				cycles++
			default:
				t.Fatalf("第 %d 轮出现非预期结果: code=%d err=%v", round, o.code, o.err)
			}
		}
		if success != 2 || cycles != 1 {
			t.Fatalf("第 %d 轮三节点期望 2 成功 + 1 成环拒绝，实际 success=%d cycle=%d", round, success, cycles)
		}
		graph := loadReferralGraph(t)
		if len(graph) != (round+1)*2 {
			t.Fatalf("第 %d 轮关系总数 = %d，期望 %d", round, len(graph), (round+1)*2)
		}
		assertAcyclicGraph(t, graph)
		successes += success
		cycleRejections += cycles
	}
	if successes != rounds*2 || cycleRejections != rounds {
		t.Fatalf("压力结果统计异常：success=%d cycle=%d，期望 %d/%d",
			successes, cycleRejections, rounds*2, rounds)
	}
	t.Logf("三节点成环 %d 轮：成功 %d / 成环拒绝 %d / 最终关系数 %d / 无环",
		rounds, successes, cycleRejections, len(loadReferralGraph(t)))
}
