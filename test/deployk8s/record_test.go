package deployk8s

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1.2 / AUD-D2 与 AUD-D5-A/B：**Migration 记录完整性**。
//
// 背景（独立 FINAL-AUDIT 的结论）：lease_takeover 用内存里的 MIG_*（此时还是
// NO_MIGRATION）整体回写注解，把上一轮的 COMPLETE/FAILED + job + uid + detail 清空；
// 接管方随后可能因为版本降级保护退出，证据永久丢失。
//
// 断言方式：真实执行 scripts/deploy-k8s.sh，逐次检查 fake kubectl 记录的
// `lease.write` 事件（每次 CAS 写入携带了什么记录）+ 最终注解，而不是只看最终状态。

// eventFields 把 `lease.write kind=... state=... job=...` 解析成 map。
// 事件值里不含空格，因此按空格切分是安全的。
func eventFields(line string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Fields(line) {
		if key, value, ok := strings.Cut(part, "="); ok {
			out[key] = value
		}
	}
	return out
}

// oldJob 返回一个与本次目标版本不同的上一轮 Job 名。
func (f *fixture) oldJob() string { return "surgecart-migrate-" + f.shas["c1"] }

// previousTerminalJob 返回上一轮终态 Job 的快照。
func previousTerminalJob(job, uid, state string) fakestate.Job {
	return fakestate.Job{
		Name:     job,
		UID:      uid,
		Exists:   true,
		Complete: state == "MIGRATION_COMPLETE",
		Failed:   state == "MIGRATION_FAILED",
		Reason:   "BackoffLimitExceeded",
		Message:  "Job has reached the specified backoff limit",
		Pods: []fakestate.Pod{{
			Name: "surgecart-migrate-old-pod", Phase: "Failed",
		}},
	}
}

// scriptNewMigration 追加「本轮迁移」的 Job 快照序列：一次运行 - 一次完成。
// 第一个快照（接管核验用）由调用方提供。
func (f *fixture) scriptNewMigration(first fakestate.Job, clusterJobs []fakestate.Job) {
	f.t.Helper()
	tag := f.shas["target"]
	running := fakestate.Job{Name: "surgecart-migrate-" + tag, UID: "job-uid-" + tag, Exists: true}
	done := running
	done.Complete = true
	f.updateState(func(st *fakestate.State) {
		st.JobScript = []fakestate.Job{first, running, done}
		st.Jobs = clusterJobs
		// 重新开始一个 Job 读取序列：多次 runDeploy 共用同一份状态时必须从头读。
		st.JobGets = 0
	})
}

// seedTTLPreviousTerminal 构造「上一轮已终态、锁已过期、Job 已被 TTL 清理」的现场。
func (f *fixture) seedTTLPreviousTerminal(state string) (string, string) {
	f.t.Helper()
	job := f.oldJob()
	uid := "job-uid-previous-round"
	f.seedExpiredLease(state, job, uid)
	f.updateState(func(st *fakestate.State) {
		st.Job = fakestate.Job{Exists: false}
		st.JobPods = map[string][]fakestate.Pod{}
	})
	f.scriptNewMigration(fakestate.Job{Exists: false}, nil)
	return job, uid
}

// seedExistingPreviousTerminal 构造「上一轮已终态、Job 仍存在、锁已过期」的现场。
func (f *fixture) seedExistingPreviousTerminal(state string) (string, string) {
	f.t.Helper()
	job := f.oldJob()
	uid := "job-uid-previous-round"
	f.seedExpiredLease(state, job, uid)
	previous := previousTerminalJob(job, uid, state)
	f.updateState(func(st *fakestate.State) {
		st.Job = previous
		st.JobPods = map[string][]fakestate.Pod{job: previous.Pods}
	})
	f.scriptNewMigration(previous, []fakestate.Job{previous})
	return job, uid
}

// TestTTLCompleteRecordAllowsTakeover 覆盖 AUD-D5-A1：
// COMPLETE + Job NotFound（TTL 清理）→ 允许接管；记录在新迁移开始时归档到 previous-*。
func TestTTLCompleteRecordAllowsTakeover(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	job, uid := f.seedTTLPreviousTerminal("MIGRATION_COMPLETE")

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	// 接管写入本身必须携带上一轮记录（旧实现会写 NO_MIGRATION + 空 job）。
	f.requireEventContaining(t, "lease.write", "kind=lease-takeover.yaml", "state=MIGRATION_COMPLETE",
		"job="+job, "uid="+uid)
	// 开始新一轮迁移时把上一轮归档，而不是用默认值覆盖。
	f.requireEventContaining(t, "lease.write", "kind=lease-record.yaml", "state=MIGRATION_PENDING",
		"prev=MIGRATION_COMPLETE")
	anns := f.leaseAnnotations()
	if anns[annPrevState] != "MIGRATION_COMPLETE" || anns[annPrevJob] != job {
		t.Fatalf("上一轮 COMPLETE 记录必须归档（state/job），实际 prevState=%q prevJob=%q",
			anns[annPrevState], anns[annPrevJob])
	}
	if anns[annPrevUID] != uid || anns[annPrevTerminalAt] == "" {
		t.Fatalf("归档必须保留 uid 与 terminal-at，实际 prevUid=%q prevTerminalAt=%q",
			anns[annPrevUID], anns[annPrevTerminalAt])
	}
	if anns[annMigrationState] != "MIGRATION_COMPLETE" {
		t.Fatalf("新迁移完成后当前记录应为 COMPLETE，实际 %q", anns[annMigrationState])
	}
	if got := f.countEvents("apply.migration"); got != 1 {
		t.Fatalf("本轮只应创建一次迁移 Job，实际 apply.migration=%d", got)
	}
}

// TestTTLFailedRecordAllowsTakeover 覆盖 AUD-D5-A2：
// FAILED + Job NotFound → 允许接管；失败证据归档保留。
func TestTTLFailedRecordAllowsTakeover(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	job, uid := f.seedTTLPreviousTerminal("MIGRATION_FAILED")

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	f.requireEventContaining(t, "lease.write", "kind=lease-takeover.yaml", "state=MIGRATION_FAILED",
		"job="+job, "uid="+uid)
	f.requireEventContaining(t, "lease.write", "kind=lease-record.yaml", "state=MIGRATION_PENDING",
		"prev=MIGRATION_FAILED")
	anns := f.leaseAnnotations()
	if anns[annPrevState] != "MIGRATION_FAILED" || anns[annPrevJob] != job || anns[annPrevUID] != uid {
		t.Fatalf("失败证据必须完整归档（state/job/uid），实际 %q/%q/%q",
			anns[annPrevState], anns[annPrevJob], anns[annPrevUID])
	}
	if anns[annPrevDetail] == "" {
		t.Fatal("归档必须保留上一轮的 detail 证据")
	}
}

// TestTTLInflightRecordWithoutEvidenceFailsClosed 覆盖 AUD-D5-A3（PENDING）：
// PENDING + Job NotFound 且没有数据库证据 → 拒绝接管，不做任何变更。
func TestTTLInflightRecordWithoutEvidenceFailsClosed(t *testing.T) {
	t.Parallel()
	runTTLInflightRejected(t, "MIGRATION_PENDING")
}

// TestTTLRunningRecordWithoutEvidenceFailsClosed 覆盖 AUD-D5-A4（RUNNING）。
func TestTTLRunningRecordWithoutEvidenceFailsClosed(t *testing.T) {
	t.Parallel()
	runTTLInflightRejected(t, "MIGRATION_RUNNING")
}

func runTTLInflightRejected(t *testing.T, state string) {
	t.Helper()
	f := newFixture(t, fixtureOptions{})
	job := f.oldJob()
	f.seedExpiredLease(state, job, "") // uid 为空：崩溃在「apply 后、回写 UID 前」
	f.updateState(func(st *fakestate.State) {
		st.Job = fakestate.Job{Exists: false}
		st.JobScript = []fakestate.Job{{Exists: false}}
		st.Jobs = nil
	})

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")

	res.requireCode(t, 7)
	res.requireNoSuccess(t)
	res.requireContains(t, "但该 Job 不存在", "人工恢复步骤", "migration-recovery-evidence")
	f.requireNoEvent(t, "apply.migration")
	f.requireNoEvent(t, "apply.application")
	f.requireNoEvent(t, "lease.acquire")

	// 记录与持有者都必须保持原样：拒绝接管不能顺手改写证据。
	anns := f.leaseAnnotations()
	if anns[annMigrationState] != state || anns[annMigrationJob] != job {
		t.Fatalf("拒绝接管时不得改写记录，实际 state=%q job=%q", anns[annMigrationState], anns[annMigrationJob])
	}
	if anns[annMigrationDetail] == "" {
		t.Fatal("拒绝接管时不得清空 detail")
	}
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "deploy-previous-runner-7777" {
		t.Fatalf("拒绝接管时不得改动持有者，实际: %+v", lease)
	}
}

// TestTakeoverKeepsPreviousRecordWhenVersionGuardRejects 覆盖 AUD-D5-B3：
// 接管成功但版本降级保护拒绝（退出码 4）→ 上一轮记录必须完整保留。
func TestTakeoverKeepsPreviousRecordWhenVersionGuardRejects(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	job, uid := f.seedExistingPreviousTerminal("MIGRATION_FAILED")
	before := f.leaseAnnotations()
	// 目标版本是当前版本的祖先 → 降级，必须在锁内被拒绝。
	f.retag(f.shas["c1"])

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")

	res.requireCode(t, 4)
	res.requireNoSuccess(t)
	f.requireNoEvent(t, "apply.migration")
	f.requireNoEvent(t, "apply.application")

	after := f.leaseAnnotations()
	for _, key := range []string{annMigrationState, annMigrationJob, annMigrationUID, annMigrationDetail} {
		if after[key] != before[key] {
			t.Fatalf("接管后版本检查失败不得改动记录：%s 期望 %q，实际 %q", key, before[key], after[key])
		}
	}
	if after[annMigrationState] != "MIGRATION_FAILED" {
		t.Fatalf("FAILED 记录必须完整保留，实际 state=%q", after[annMigrationState])
	}
	if after[annMigrationJob] != job || after[annMigrationUID] != uid {
		t.Fatalf("FAILED 记录必须保留 job/uid，实际 job=%q uid=%q", after[annMigrationJob], after[annMigrationUID])
	}
	// 每一次 Lease 写入都必须携带上一轮记录：不允许出现「state 空 / job 空」的写入。
	for _, line := range f.findEvents("lease.write") {
		fields := eventFields(line)
		if fields["state"] != "MIGRATION_FAILED" {
			t.Fatalf("写入事件改写了上一轮 state: %s", line)
		}
		if fields["job"] != job || fields["uid"] != uid {
			t.Fatalf("写入事件丢失了上一轮 job/uid: %s", line)
		}
	}
}

// TestCrashAfterTakeoverKeepsPreviousRecord 覆盖 AUD-D2 的关键时序：
// FAILED → 锁释放 → 新 Runner 接管 → **尚未开始新 Migration** → Runner SIGKILL
// → 原 FAILED 记录必须仍然存在（接管不得清空，也不能靠 cleanup 恢复）。
func TestCrashAfterTakeoverKeepsPreviousRecord(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{secretDelay: 3 * time.Second})
	job, uid := f.seedExistingPreviousTerminal("MIGRATION_FAILED")

	p := f.start("--lock-wait-timeout", "10", "--migration-timeout", "10", "--rollout-timeout", "10")
	// 锁内复检（get secret）被延迟：此时接管已经完成，但新迁移还没有开始。
	f.waitForFile(t, filepath.Join(f.stateDir, "inflight-secret-check"), 60*time.Second)
	p.kill()

	if got := f.countEvents("apply.migration"); got != 0 {
		t.Fatalf("崩溃发生在开始迁移之前，不得创建迁移 Job，实际 %d", got)
	}
	f.requireEventContaining(t, "lease.write", "kind=lease-takeover.yaml", "state=MIGRATION_FAILED",
		"job="+job, "uid="+uid)

	anns := f.leaseAnnotations()
	if anns[annMigrationState] != "MIGRATION_FAILED" || anns[annMigrationJob] != job || anns[annMigrationUID] != uid {
		t.Fatalf("接管后崩溃必须保留原 FAILED 记录，实际 %q/%q/%q",
			anns[annMigrationState], anns[annMigrationJob], anns[annMigrationUID])
	}
	if anns[annMigrationTerminalAt] == "" || anns[annMigrationDetail] == "" {
		t.Fatalf("terminal-at / detail 不得被清空，实际 %q/%q",
			anns[annMigrationTerminalAt], anns[annMigrationDetail])
	}
	if anns[annPrevState] != "" {
		t.Fatalf("尚未开始新迁移时不应归档，实际 prev-state=%q", anns[annPrevState])
	}
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity == "" {
		t.Fatalf("SIGKILL 后锁应保持被持有（无法清理），实际: %+v", lease)
	}
}

// TestRenewPreservesRecordAnnotations 覆盖 AUD-D5-B4：
// 续租只刷新租期，不得把迁移记录写成默认值。
func TestRenewPreservesRecordAnnotations(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.seedReleasedCompleteLease()

	res := runDeploy(t, f, append([]string{
		"--migration-timeout", "60",
		"--rollout-timeout", "30",
	}, leaseArgs()...)...)
	res.requireSuccess(t)

	renews := f.findEvents("lease.write", "kind=lease-renew.yaml")
	if len(renews) < 2 {
		t.Fatalf("续租写入 = %d 次，期望 >= 2（否则无法证明续租保留记录）；事件:\n%s",
			len(renews), strings.Join(f.events(), "\n"))
	}
	for _, line := range renews {
		fields := eventFields(line)
		if fields["state"] == "" || fields["state"] == "NO_MIGRATION" {
			t.Fatalf("续租把迁移状态写成默认值/空: %s", line)
		}
		if fields["job"] == "" {
			t.Fatalf("续租丢失了迁移 Job 名: %s", line)
		}
	}
}

// seedReleasedCompleteLease 构造「已释放 + 已有 COMPLETE 记录」的锁：
// 续租发生在 takeover 之后、begin_migration 归档之前，因此能观察续租是否保留记录。
func (f *fixture) seedReleasedCompleteLease() string {
	f.t.Helper()
	job := f.oldJob()
	// 上一轮 Job 已终结（空 Pod 列表），因此接管被允许；本轮迁移持续几秒以便观察续租。
	f.updateState(func(st *fakestate.State) {
		previous := fakestate.Job{Name: job, UID: "job-uid-previous-round", Exists: true, Complete: true}
		slow := fakestate.Job{Name: "surgecart-migrate-" + f.shas["target"], UID: "job-uid-" + f.shas["target"], Exists: true}
		done := slow
		done.Complete = true
		st.Job = previous
		st.JobScript = []fakestate.Job{previous, slow, slow, done}
		st.Jobs = []fakestate.Job{previous}
		st.Lease = &fakestate.Lease{
			ResourceVersion: "5",
			UID:             "lease-uid-previous",
			HolderIdentity:  "",
			AcquireTime:     rfc3339(time.Now().Add(-40 * time.Minute)),
			RenewTime:       rfc3339(time.Now().Add(-30 * time.Minute)),
			DurationSeconds: 30,
			Transitions:     1,
			Annotations: map[string]string{
				annMigrationState:      "MIGRATION_COMPLETE",
				annMigrationJob:        job,
				annMigrationUID:        "job-uid-previous-round",
				annMigrationUpdatedAt:  rfc3339(time.Now().Add(-40 * time.Minute)),
				annMigrationTerminalAt: rfc3339(time.Now().Add(-40 * time.Minute)),
			},
		}
	})
	return job
}

// TestReleasePreservesRecordAnnotations 覆盖 AUD-D5-B5 与 D6：
// 释放只清 holder；记录（终态、uid、terminal-at）完整保留，租期字段保持配置值。
func TestReleasePreservesRecordAnnotations(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	anns := f.leaseAnnotations()
	if anns[annMigrationState] != "MIGRATION_COMPLETE" || anns[annMigrationJob] == "" || anns[annMigrationUID] == "" {
		t.Fatalf("释放后记录必须完整（state/job/uid），实际 %q/%q/%q",
			anns[annMigrationState], anns[annMigrationJob], anns[annMigrationUID])
	}
	if anns[annMigrationTerminalAt] == "" {
		t.Fatal("释放后必须保留 terminal-at")
	}
	// 释放写入本身必须携带同一份记录（不是事后被别的写入补上的）。
	f.requireEventContaining(t, "lease.write", "kind=lease-release.yaml", "state=MIGRATION_COMPLETE",
		"job="+anns[annMigrationJob], "uid="+anns[annMigrationUID])
	// D6：释放不改变 leaseDurationSeconds 的语义值（旧实现写 1）。
	// T2-06.1.3：默认租期改为 45s（F-02 预算重估），断言必须跟进默认值。
	if lease := f.state().Lease; lease == nil || lease.DurationSeconds != 45 {
		t.Fatalf("释放后 leaseDurationSeconds 应保持配置值 45，实际: %+v", lease)
	}
}

// TestFailedMigrationReleaseKeepsFailureEvidence 覆盖 B5 的失败分支：
// 迁移失败后释放锁，失败证据（state/job/uid/terminal-at）保留在释放写入里。
func TestFailedMigrationReleaseKeepsFailureEvidence(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	jobName := "surgecart-migrate-" + f.shas["target"]
	uid := "job-uid-" + f.shas["target"]
	f.updateState(func(st *fakestate.State) {
		failed := fakestate.Job{
			Name: jobName, UID: uid, Exists: true, Failed: true,
			Reason: "BackoffLimitExceeded", Message: "Job has reached the specified backoff limit",
			Pods: []fakestate.Pod{{Name: "surgecart-migrate-pod-failed", Phase: "Failed"}},
		}
		st.Job = failed
		st.JobScript = []fakestate.Job{failed}
	})

	res := runDeploy(t, f, "--migration-timeout", "10")

	res.requireCode(t, 2)
	anns := f.leaseAnnotations()
	if anns[annMigrationState] != "MIGRATION_FAILED" || anns[annMigrationUID] != uid {
		t.Fatalf("失败证据必须保留，实际 %q/%q", anns[annMigrationState], anns[annMigrationUID])
	}
	if anns[annMigrationTerminalAt] == "" {
		t.Fatal("Failed 且 Pod 已终止时必须记录 terminal-at")
	}
	f.requireEventContaining(t, "lease.write", "kind=lease-release.yaml", "state=MIGRATION_FAILED",
		"job="+jobName, "uid="+uid)
}

// TestNewMigrationArchivesPreviousRecordOnce 覆盖 AUD-D5-B6：
// 新一轮迁移开始时归档上一轮记录；CAS 重试不得把新记录自己归档掉。
func TestNewMigrationArchivesPreviousRecordOnce(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	job, uid := f.seedExistingPreviousTerminal("MIGRATION_COMPLETE")
	// 迁移记录写入第一次即确定性 409：脚本必须重读并把「上一轮」而不是「自己」归档。
	f.updateState(func(st *fakestate.State) { st.LeaseRecordConflicts = 1 })

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	f.requireEvent(t, "lease.record-conflict")
	anns := f.leaseAnnotations()
	if anns[annPrevState] != "MIGRATION_COMPLETE" || anns[annPrevJob] != job || anns[annPrevUID] != uid {
		t.Fatalf("上一轮记录必须被归档一次（state/job/uid），实际 %q/%q/%q",
			anns[annPrevState], anns[annPrevJob], anns[annPrevUID])
	}
	for _, line := range f.findEvents("lease.write", "state=MIGRATION_PENDING", "prev=MIGRATION_PENDING") {
		t.Fatalf("重试时把本轮记录归档成了上一轮: %s", line)
	}
}

// TestRecordWriteRefusedWhenLeaseExpired 覆盖 T2-06.1.4 / B-3（审计点名的缺陷）：
//
//	Runner 持锁 → 租约在服务端过期（续租链还没跑到）→ 主进程要写 COMPLETE 记录
//
// 旧实现只比 holderIdentity：holder 仍是自己，于是照写，并把 renewTime 推到 now ——
// 等于给一把「别的 Runner 此刻就能合法接管」的锁续命一个租期。
// 修复后：写记录路径必须先判定过期，过期即 fail-closed（exit 3），且**不写** Lease。
func TestRecordWriteRefusedWhenLeaseExpired(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	longMigration(f)

	// 续租间隔取 40s：保证「注入过期」之后不会有续租抢先改写 renewTime，
	// 从而把「记录写入路径自己的过期判定」隔离出来（预算 40+3×2+1+5=52 < 60）。
	args := []string{
		"--lease-duration", "60", "--lease-renew-interval", "40", "--lease-renew-timeout", "1",
		"--migration-timeout", "30", "--rollout-timeout", "20",
	}
	p := f.start(args...)
	// MIGRATION_RUNNING 写入之后，主进程在等待循环里不再读/写 Lease，
	// 下一处 Lease 写入是终态（COMPLETE）记录。
	f.waitForEvent(t, "lease.write kind=lease-record.yaml state=MIGRATION_RUNNING", 60*time.Second)
	f.expireLease(2 * time.Minute)
	expired := f.state().Lease.RenewTime

	res := p.wait(t, 120*time.Second)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "拒绝写迁移记录", "租约已过期", "leaseDurationSeconds=", "fail-closed")
	// COMPLETE 记录必须没有写出去（事件层面证明，而不是只看最终注解）。
	for _, line := range f.findEvents("lease.write", "state=MIGRATION_COMPLETE") {
		t.Fatalf("租约过期后不得写入 COMPLETE 记录: %s", line)
	}
	res.requireContains(t, "LEASE_WINDOW stage=record-write-", "verdict=expired")
	// 过期锁不得被续命/释放：renewTime 保持注入值，holder 仍是本 Runner（等自然过期）。
	lease := f.state().Lease
	if lease == nil {
		t.Fatal("Lease 不应被删除")
	}
	if lease.RenewTime != expired {
		t.Fatalf("过期锁的 renewTime 被改写了（等于给过期锁续命）：期望 %q，实际 %q",
			expired, lease.RenewTime)
	}
	if lease.HolderIdentity == "" {
		t.Fatal("过期锁不得被释放路径清空 holder（B-4：不为过期锁续命、也不写他人的锁）")
	}
}
