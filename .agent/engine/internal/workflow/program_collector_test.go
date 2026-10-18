package workflow

import (
	"testing"
)

// --- Collector 登记校验（Task 13）---

func collectorFor(t *testing.T, git Git, load TaskStateLoader) *Collector {
	return &Collector{
		Store:         &RegistryStore{Root: t.TempDir()},
		Git:           git,
		LoadTaskState: load,
	}
}

func planFor(programID string) ProgramState {
	return ProgramState{
		SchemaVersion: ProgramSchemaV1,
		ProgramID:     programID,
		ParentBranch:  "develop",
		BaseCommit:    testSHA(1),
		Tasks: []ProgramTask{
			{TaskID: "t1", TaskType: TaskTypeNormal, Branch: "feature/t1"},
		},
	}
}

// 合法登记通过。
func TestCollectorAcceptValid(t *testing.T) {
	reviewSHA := shaLike("review:t1")
	featureSHA := shaLike("delivery:t1")
	c := collectorFor(t, nil, func(id string) (State, error) {
		return collectorTaskState("prog-1", "feature/t1", reviewSHA, featureSHA), nil
	})
	d := RegisteredDelivery{
		TaskID:       "t1",
		Branch:       "feature/t1",
		DeliverySHA:  featureSHA,
		ReviewStatus: ReviewClean,
		ReviewTarget: reviewSHA,
		OwnerStatus:  OwnerAccepted,
		OwnerTarget:  reviewSHA,
		RegisteredBy: "CollectorAgent",
	}
	res := c.Collect(planFor("prog-1"), d)
	if res.Status != StatusPass {
		t.Fatalf("期望 PASS，实际 %s issues=%v error=%q", res.Status, res.Issues, res.Error)
	}
}

// 未规划的子 Task 拒绝。
func TestCollectorRejectNotInPlan(t *testing.T) {
	c := collectorFor(t, nil, func(id string) (State, error) {
		return collectorTaskState("prog-1", "feature/t1", testSHA(2), testSHA(3)), nil
	})
	res := c.Collect(planFor("prog-1"), validDelivery("ghost"))
	if res.Status != StatusFail {
		t.Fatalf("期望 FAIL，实际 %s", res.Status)
	}
}

// 未 Owner ACCEPT 不能登记。
func TestCollectorRejectNotAccepted(t *testing.T) {
	reviewSHA := shaLike("review:t1")
	c := collectorFor(t, nil, func(id string) (State, error) {
		st := collectorTaskState("prog-1", "feature/t1", reviewSHA, shaLike("delivery:t1"))
		st.Owner.Status = OwnerPending
		return st, nil
	})
	d := RegisteredDelivery{
		TaskID: "t1", Branch: "feature/t1", DeliverySHA: shaLike("delivery:t1"),
		ReviewStatus: ReviewClean, ReviewTarget: reviewSHA,
		OwnerStatus: OwnerAccepted, OwnerTarget: reviewSHA,
	}
	if res := c.Collect(planFor("prog-1"), d); res.Status != StatusFail {
		t.Fatalf("期望 FAIL（未 ACCEPT），实际 %s", res.Status)
	}
}

// 错误 delivery_sha 拒绝。
func TestCollectorRejectWrongDeliverySHA(t *testing.T) {
	reviewSHA := shaLike("review:t1")
	c := collectorFor(t, nil, func(id string) (State, error) {
		return collectorTaskState("prog-1", "feature/t1", reviewSHA, shaLike("delivery:t1")), nil
	})
	d := RegisteredDelivery{
		TaskID: "t1", Branch: "feature/t1", DeliverySHA: testSHA(999),
		ReviewStatus: ReviewClean, ReviewTarget: reviewSHA,
		OwnerStatus: OwnerAccepted, OwnerTarget: reviewSHA,
	}
	if res := c.Collect(planFor("prog-1"), d); res.Status != StatusFail {
		t.Fatalf("期望 FAIL（SHA 不一致），实际 %s", res.Status)
	}
}

// 错误分支拒绝。
func TestCollectorRejectWrongBranch(t *testing.T) {
	reviewSHA := shaLike("review:t1")
	featureSHA := shaLike("delivery:t1")
	c := collectorFor(t, nil, func(id string) (State, error) {
		return collectorTaskState("prog-1", "feature/t1", reviewSHA, featureSHA), nil
	})
	d := RegisteredDelivery{
		TaskID: "t1", Branch: "feature/other", DeliverySHA: featureSHA,
		ReviewStatus: ReviewClean, ReviewTarget: reviewSHA,
		OwnerStatus: OwnerAccepted, OwnerTarget: reviewSHA,
	}
	if res := c.Collect(planFor("prog-1"), d); res.Status != StatusFail {
		t.Fatalf("期望 FAIL（分支不一致），实际 %s", res.Status)
	}
}

// delivery_sha 非真实 commit 拒绝。
func TestCollectorRejectUnknownSHA(t *testing.T) {
	reviewSHA := shaLike("review:t1")
	featureSHA := shaLike("delivery:t1")
	g := newFakeGit()
	g.commits[featureSHA] = false // 不存在
	c := collectorFor(t, g, func(id string) (State, error) {
		return collectorTaskState("prog-1", "feature/t1", reviewSHA, featureSHA), nil
	})
	d := RegisteredDelivery{
		TaskID: "t1", Branch: "feature/t1", DeliverySHA: featureSHA,
		ReviewStatus: ReviewClean, ReviewTarget: reviewSHA,
		OwnerStatus: OwnerAccepted, OwnerTarget: reviewSHA,
	}
	if res := c.Collect(planFor("prog-1"), d); res.Status != StatusFail {
		t.Fatalf("期望 FAIL（SHA 非真实 commit），实际 %s", res.Status)
	}
}

// program.id 指向其它 Program 拒绝（F2）。
func TestCollectorRejectWrongProgramID(t *testing.T) {
	reviewSHA := shaLike("review:t1")
	featureSHA := shaLike("delivery:t1")
	c := collectorFor(t, nil, func(id string) (State, error) {
		st := collectorTaskState("other-program", "feature/t1", reviewSHA, featureSHA)
		return st, nil
	})
	d := RegisteredDelivery{
		TaskID: "t1", Branch: "feature/t1", DeliverySHA: featureSHA,
		ReviewStatus: ReviewClean, ReviewTarget: reviewSHA,
		OwnerStatus: OwnerAccepted, OwnerTarget: reviewSHA,
	}
	if res := c.Collect(planFor("prog-1"), d); res.Status != StatusFail {
		t.Fatalf("期望 FAIL（program.id 不一致），实际 %s", res.Status)
	}
}

// ProgramRef 缺失拒绝（F2）。
func TestCollectorRejectMissingProgramRef(t *testing.T) {
	reviewSHA := shaLike("review:t1")
	featureSHA := shaLike("delivery:t1")
	c := collectorFor(t, nil, func(id string) (State, error) {
		st := collectorTaskState("prog-1", "feature/t1", reviewSHA, featureSHA)
		st.Program = nil
		return st, nil
	})
	d := RegisteredDelivery{
		TaskID: "t1", Branch: "feature/t1", DeliverySHA: featureSHA,
		ReviewStatus: ReviewClean, ReviewTarget: reviewSHA,
		OwnerStatus: OwnerAccepted, OwnerTarget: reviewSHA,
	}
	if res := c.Collect(planFor("prog-1"), d); res.Status != StatusFail {
		t.Fatalf("期望 FAIL（ProgramRef 缺失），实际 %s", res.Status)
	}
}

// 重复登记拒绝。
func TestCollectorRejectDuplicate(t *testing.T) {
	reviewSHA := shaLike("review:t1")
	featureSHA := shaLike("delivery:t1")
	c := collectorFor(t, nil, func(id string) (State, error) {
		return collectorTaskState("prog-1", "feature/t1", reviewSHA, featureSHA), nil
	})
	d := RegisteredDelivery{
		TaskID: "t1", Branch: "feature/t1", DeliverySHA: featureSHA,
		ReviewStatus: ReviewClean, ReviewTarget: reviewSHA,
		OwnerStatus: OwnerAccepted, OwnerTarget: reviewSHA,
	}
	if res := c.Collect(planFor("prog-1"), d); res.Status != StatusPass {
		t.Fatalf("第一次应 PASS，实际 %s", res.Status)
	}
	if res := c.Collect(planFor("prog-1"), d); res.Status != StatusError {
		t.Fatalf("期望重复登记 ERROR，实际 %s", res.Status)
	}
}
