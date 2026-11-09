package deployk8s

import (
	"fmt"
	"strings"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1.2 / AUD-D4 与 AUD-D5-E：
// **PENDING + UID 缺失 + Job NotFound 的恢复路径必须安全且可执行**。
//
// 旧状态是死路：runbook 让人把 state 改成 COMPLETE，但脚本仍因 uid 为空拒绝；
// 直接复位 NO_MIGRATION 又会丢掉「到底执行到哪」的证据。
//
// 新契约：人工把**只读事实**（`my-shop migrate version` 的输出）写成
//   migration-recovery-method=database-evidence
//   migration-recovery-evidence=version=<N> dirty=<bool> latest=<M>
// 脚本据此决定「上一轮算完成」还是「上一轮未完成」，并且不会跳过任何迁移。

const (
	// 14 位时间戳，与迁移文件名格式一致（YYYYMMDDHHMMSS）。
	evidenceLatest = "20261001000024"
	evidenceBehind = "20261001000010"
	evidenceFuture = "20261001000030"
)

func evidenceString(version string, dirty bool, latest string) string {
	return fmt.Sprintf("version=%s dirty=%t latest=%s", version, dirty, latest)
}

// seedInflightWithAbsentJob 构造 AUD-D4 的核心现场：
// 锁记录为 PENDING/RUNNING（uid 可空），Job 已不存在，数据库证据可选。
func (f *fixture) seedInflightWithAbsentJob(state, uid string, evidence string) (string, string) {
	f.t.Helper()
	job := f.oldJob()
	f.seedExpiredLease(state, job, uid)
	if evidence != "" {
		f.setLeaseAnnotations(map[string]string{
			annRecoveryMethod:   "database-evidence",
			annRecoveryEvidence: evidence,
		})
	}
	f.updateState(func(st *fakestate.State) {
		st.Job = fakestate.Job{Exists: false}
		st.JobPods = map[string][]fakestate.Pod{}
		st.MigrationPods = nil
	})
	f.scriptNewMigration(fakestate.Job{Exists: false}, nil)
	return job, uid
}

// TestRecoveryEvidenceCompleteAllowsResume 覆盖 AUD-D5-E1：
// 证据显示已到最新且 dirty=false → 上一轮按 COMPLETE 记录（不伪造 UID），继续本轮迁移。
func TestRecoveryEvidenceCompleteAllowsResume(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	evidence := evidenceString(evidenceLatest, false, evidenceLatest)
	job, _ := f.seedInflightWithAbsentJob("MIGRATION_PENDING", "", evidence)

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "读取到数据库证据", "不伪造 Job uid")
	// 收敛写入必须带 recovery-method，且 uid 为空（不得伪造）。
	f.requireEventContaining(t, "lease.write", "kind=lease-record.yaml", "state=MIGRATION_COMPLETE", "job="+job)
	var completeWrites []string
	for _, line := range f.findEvents("lease.write", "state=MIGRATION_COMPLETE", "job="+job) {
		fields := eventFields(line)
		if fields["uid"] != "" {
			t.Fatalf("恢复路径不得伪造 Job uid，实际: %s", line)
		}
		completeWrites = append(completeWrites, line)
	}
	if len(completeWrites) == 0 {
		t.Fatalf("缺少按证据收敛 COMPLETE 的写入；事件:\n%s", strings.Join(f.events(), "\n"))
	}
	// 证据来源必须被归档保留（当前记录会被新迁移覆盖）。
	anns := f.leaseAnnotations()
	if anns[annPrevState] != "MIGRATION_COMPLETE" || anns[annPrevJob] != job {
		t.Fatalf("上一轮应归档为 COMPLETE，实际 prev=%q job=%q", anns[annPrevState], anns[annPrevJob])
	}
	if !strings.Contains(anns[annPrevDetail], evidenceLatest) {
		t.Fatalf("归档必须保留数据库证据来源，实际 prev-detail=%q", anns[annPrevDetail])
	}
	if got := f.countEvents("apply.migration"); got != 1 {
		t.Fatalf("恢复后仍必须执行一次迁移（不得跳过），实际 %d", got)
	}
	if got := f.countEvents("apply.application"); got != 1 {
		t.Fatalf("恢复后应正常更新 Application，实际 %d", got)
	}
}

// TestRecoveryEvidenceDirtyRejected 覆盖 AUD-D5-E2：dirty=true → 拒绝自动恢复。
func TestRecoveryEvidenceDirtyRejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	job, uid := f.seedInflightWithAbsentJob("MIGRATION_RUNNING", "",
		evidenceString(evidenceLatest, true, evidenceLatest))

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")

	res.requireCode(t, 7)
	res.requireNoSuccess(t)
	res.requireContains(t, "dirty=true", "拒绝自动恢复", "不会 force")
	f.requireNoEvent(t, "apply.migration")
	f.requireNoEvent(t, "apply.application")
	anns := f.leaseAnnotations()
	if anns[annMigrationState] != "MIGRATION_RUNNING" || anns[annMigrationJob] != job || anns[annMigrationUID] != uid {
		t.Fatalf("拒绝恢复时不得改写记录，实际 %q/%q/%q",
			anns[annMigrationState], anns[annMigrationJob], anns[annMigrationUID])
	}
}

// TestRecoveryEvidenceBehindResetsRecord 覆盖 AUD-D5-E3：
// 账本干净但落后 → 上一轮没有完成：归档并复位，由新 Job 继续执行 migrate up。
func TestRecoveryEvidenceBehindResetsRecord(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	job, _ := f.seedInflightWithAbsentJob("MIGRATION_PENDING", "",
		evidenceString(evidenceBehind, false, evidenceLatest))

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "上一轮没有完成迁移", "归档该记录后复位")
	f.requireEventContaining(t, "lease.write", "kind=lease-record.yaml", "state=NO_MIGRATION", "prev=MIGRATION_PENDING")
	anns := f.leaseAnnotations()
	if anns[annPrevState] != "MIGRATION_PENDING" || anns[annPrevJob] != job {
		t.Fatalf("上一轮 PENDING 记录必须归档，实际 prev=%q job=%q", anns[annPrevState], anns[annPrevJob])
	}
	if !strings.Contains(anns[annPrevDetail], "恢复判定") {
		t.Fatalf("归档必须记录恢复判定与证据来源，实际 %q", anns[annPrevDetail])
	}
	// 仍然执行了一次迁移：复位只影响记录，不影响实际执行。
	if got := f.countEvents("apply.migration"); got != 1 {
		t.Fatalf("复位后必须执行迁移，实际 %d", got)
	}
}

// TestRecoveryEvidenceZeroRejected 覆盖 AUD-D4「schema_migrations 不存在」：
// version=0 无法区分全新空库与未纳管的既有库 → 拒绝自动恢复。
func TestRecoveryEvidenceZeroRejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.seedInflightWithAbsentJob("MIGRATION_PENDING", "",
		"version=00000000000000 dirty=false latest="+evidenceLatest)

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")

	res.requireCode(t, 7)
	res.requireContains(t, "version=0", "无法区分")
	f.requireNoEvent(t, "apply.migration")
}

// TestRecoveryEvidenceMalformedRejected 覆盖「证据不可解析」：
// 不接受自由文本，避免把「我觉得已经完成」当成证据。
func TestRecoveryEvidenceMalformedRejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.seedInflightWithAbsentJob("MIGRATION_PENDING", "", "已经确认迁移完成，应该没问题")

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")

	res.requireCode(t, 7)
	res.requireContains(t, "格式无法解析", "期望格式")
	f.requireNoEvent(t, "apply.migration")
}

// TestRecoveryEvidenceFutureRejected 覆盖「数据库比本次构建更新」：
// 说明方向不一致（可能是降级），拒绝自动恢复。
func TestRecoveryEvidenceFutureRejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.seedInflightWithAbsentJob("MIGRATION_PENDING", "",
		evidenceString(evidenceFuture, false, evidenceLatest))

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")

	res.requireCode(t, 7)
	res.requireContains(t, "比本次构建的最新迁移", "方向不一致")
	f.requireNoEvent(t, "apply.migration")
}

// TestRecoveryRejectedWhenMigrationPodStillRuns 覆盖 AUD-D4 的 Pod 兜底：
// Job 已被删除，但仍有迁移 Pod 在 Running → 拒绝（Job 不存在 ≠ Pod 已停止）。
func TestRecoveryRejectedWhenMigrationPodStillRuns(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.seedInflightWithAbsentJob("MIGRATION_PENDING", "",
		evidenceString(evidenceLatest, false, evidenceLatest))
	f.updateState(func(st *fakestate.State) {
		st.MigrationPods = []fakestate.Pod{{
			Name: "surgecart-migrate-orphan-pod", Phase: "Running", OwnerJobUID: "job-uid-previous-round",
		}}
	})

	res := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")

	res.requireCode(t, 7)
	// F-07：查询成功且存在活动 Pod → 措辞是「确实存在」，不能被说成「无法确认」。
	res.requireContains(t, "**确实存在**仍在执行/终止中的迁移 Pod", "surgecart-migrate-orphan-pod")
	res.requireNotContains(t, "无法确认迁移 Pod 状态")
	f.requireNoEvent(t, "apply.migration")
	f.requireNoEvent(t, "lease.acquire")
}

// TestTerminalRecordWithEmptyUIDNeedsEvidenceThenRecovers 覆盖 AUD-D4 的死路修复：
// 旧 runbook「把 state 改成 COMPLETE」在 uid 为空时仍然被拒；补上数据库证据后可恢复。
func TestTerminalRecordWithEmptyUIDNeedsEvidenceThenRecovers(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	job, _ := f.seedInflightWithAbsentJob("MIGRATION_COMPLETE", "", "")

	blocked := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10")
	blocked.requireCode(t, 7)
	blocked.requireContains(t,
		"终态记录不完整",
		"需要数据库证据",
		"人工恢复步骤",
		"不要用 migrate force 绕过",
	)
	if got := f.countEvents("apply.migration"); got != 0 {
		t.Fatalf("缺少证据时不得开始迁移，实际 %d", got)
	}

	// 人工写入证据（只加两个注解，不伪造 uid、不改 state）。
	f.setLeaseAnnotations(map[string]string{
		annRecoveryMethod:   "database-evidence",
		annRecoveryEvidence: evidenceString(evidenceLatest, false, evidenceLatest),
	})

	allowed := runDeploy(t, f, "--lock-wait-timeout", "10", "--migration-timeout", "10", "--rollout-timeout", "10")
	allowed.requireSuccess(t)
	anns := f.leaseAnnotations()
	if anns[annPrevState] != "MIGRATION_COMPLETE" || anns[annPrevJob] != job {
		t.Fatalf("恢复后应把上一轮 COMPLETE 记录归档，实际 prev=%q job=%q", anns[annPrevState], anns[annPrevJob])
	}
	if anns[annPrevUID] != "" {
		t.Fatalf("恢复路径不得伪造 Job uid，实际 prev-uid=%q", anns[annPrevUID])
	}
}
