package workflow

import (
	"fmt"
	"sync"
	"testing"
)

// --- Program Registry 解析与并发写入（Task 12）---

func TestParseProgramRegistryValid(t *testing.T) {
	d := validDelivery("t1")
	raw := `schema_version: 1
program_id: prog-1
revision: 1
deliveries:
  - task_id: t1
    branch: feature/t1
    delivery_sha: ` + d.DeliverySHA + `
    review_status: CLEAN
    review_target: ` + d.ReviewTarget + `
    owner_status: ACCEPTED
    owner_target: ` + d.OwnerTarget + `
    registered_by: CollectorAgent
    registered_at: "2026-10-16T00:00:00Z"
`
	r, err := ParseProgramRegistry([]byte(raw))
	if err != nil {
		t.Fatalf("ParseProgramRegistry: %v", err)
	}
	if r.Revision != 1 || len(r.Deliveries) != 1 {
		t.Fatalf("revision=%d deliveries=%d", r.Revision, len(r.Deliveries))
	}
}

// 未 CLEAN 的交付记录不得进入 Registry。
func TestParseProgramRegistryRejectNotClean(t *testing.T) {
	raw := `schema_version: 1
program_id: prog-1
revision: 0
deliveries:
  - task_id: t1
    branch: feature/t1
    delivery_sha: ` + testSHA(1) + `
    review_status: PENDING
    review_target: ` + testSHA(2) + `
    owner_status: ACCEPTED
    owner_target: ` + testSHA(2) + `
`
	if _, err := ParseProgramRegistry([]byte(raw)); err == nil {
		t.Fatal("期望 review_status 非 CLEAN 被拒绝")
	}
}

// 未 Owner ACCEPT 的交付记录不得进入 Registry。
func TestParseProgramRegistryRejectNotAccepted(t *testing.T) {
	raw := `schema_version: 1
program_id: prog-1
revision: 0
deliveries:
  - task_id: t1
    branch: feature/t1
    delivery_sha: ` + testSHA(1) + `
    review_status: CLEAN
    review_target: ` + testSHA(2) + `
    owner_status: PENDING
    owner_target: ` + testSHA(2) + `
`
	if _, err := ParseProgramRegistry([]byte(raw)); err == nil {
		t.Fatal("期望 owner_status 非 ACCEPTED 被拒绝")
	}
}

func TestRegistryStoreRegisterAndRevision(t *testing.T) {
	s := &RegistryStore{Root: t.TempDir()}
	if err := s.Register("prog-1", validDelivery("t1")); err != nil {
		t.Fatalf("Register t1: %v", err)
	}
	if err := s.Register("prog-1", validDelivery("t2")); err != nil {
		t.Fatalf("Register t2: %v", err)
	}
	r, err := s.Load("prog-1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Revision != 2 || len(r.Deliveries) != 2 {
		t.Fatalf("revision=%d deliveries=%d", r.Revision, len(r.Deliveries))
	}
}

// 重复登记被拒绝（不覆盖、不静默去重）。
func TestRegistryStoreRejectDuplicate(t *testing.T) {
	s := &RegistryStore{Root: t.TempDir()}
	if err := s.Register("prog-1", validDelivery("t1")); err != nil {
		t.Fatal(err)
	}
	if err := s.Register("prog-1", validDelivery("t1")); err == nil {
		t.Fatal("期望重复登记被拒绝")
	}
	r, _ := s.Load("prog-1")
	if len(r.Deliveries) != 1 {
		t.Fatalf("deliveries=%d，期望仍为 1", len(r.Deliveries))
	}
}

// 版本条件更新：磁盘 revision 前进后，旧 revision 的写入被显式检测为冲突。
func TestRegistryStoreRegisterExpectedConflict(t *testing.T) {
	s := &RegistryStore{Root: t.TempDir()}
	if err := s.RegisterExpected("prog-1", 0, validDelivery("t1")); err != nil {
		t.Fatalf("第一次 RegisterExpected: %v", err)
	}
	err := s.RegisterExpected("prog-1", 0, validDelivery("t2"))
	if err == nil {
		t.Fatal("期望版本冲突错误")
	}
	if _, ok := err.(*RegistryConflictError); !ok {
		t.Fatalf("期望 *RegistryConflictError，实际 %T: %v", err, err)
	}
	// 冲突后 t2 未写入。
	r, _ := s.Load("prog-1")
	if len(r.Deliveries) != 1 || r.Deliveries[0].TaskID != "t1" {
		t.Fatalf("deliveries=%+v", r.Deliveries)
	}
}

// 并发写入不得静默丢失：N 个并发 Register 后，N 条记录全部保留，revision == N。
func TestRegistryStoreConcurrentNoSilentOverwrite(t *testing.T) {
	s := &RegistryStore{Root: t.TempDir()}
	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- s.Register("prog-1", validDelivery(fmt.Sprintf("t%02d", i)))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发 Register 失败: %v", err)
		}
	}
	r, err := s.Load("prog-1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Revision != n {
		t.Fatalf("revision=%d，期望 %d", r.Revision, n)
	}
	if len(r.Deliveries) != n {
		t.Fatalf("deliveries=%d，期望 %d（存在静默丢失）", len(r.Deliveries), n)
	}
}
