package workflow

import (
	"os"
	"path/filepath"
	"testing"
)

// --- Program State 解析与校验（Task 12）---

func TestParseProgramStateValid(t *testing.T) {
	ps := mustParseProgram(t, `schema_version: 1
program_id: prog-1
parent_branch: develop
base_commit: `+testSHA(1)+`
tasks:
  - task_id: t1
    task_type: NORMAL
    branch: feature/t1
  - task_id: t2
    task_type: COMPLEX
    branch: feature/t2
    dependencies: [t1]
final_integration:
  status: NOT_RUN
`)
	if ps.ProgramID != "prog-1" {
		t.Fatalf("ProgramID = %q", ps.ProgramID)
	}
	if len(ps.Tasks) != 2 {
		t.Fatalf("Tasks = %d", len(ps.Tasks))
	}
	if ps.Tasks[1].Dependencies[0] != "t1" {
		t.Fatalf("deps = %v", ps.Tasks[1].Dependencies)
	}
}

// 非法嵌套拒绝（Program → Program）：任何父 Program 字段都被 strict decode 拒绝。
func TestParseProgramStateRejectProgramNesting(t *testing.T) {
	mustParseProgramError(t, `schema_version: 1
program_id: prog-1
parent_branch: develop
base_commit: `+testSHA(1)+`
parent_program: other-prog
tasks:
  - task_id: t1
    task_type: NORMAL
    branch: feature/t1
`, "parent_program")
}

// 非法嵌套拒绝（Task → Task）：子 Task 出现「子 Task」字段被 strict decode 拒绝。
func TestParseProgramStateRejectTaskNesting(t *testing.T) {
	mustParseProgramError(t, `schema_version: 1
program_id: prog-1
parent_branch: develop
base_commit: `+testSHA(1)+`
tasks:
  - task_id: t1
    task_type: NORMAL
    branch: feature/t1
    subtasks:
      - task_id: t1a
`, "subtasks")
}

// Program 不得通过 task_type 冒充 Task 工作流模式。
func TestParseProgramStateRejectProgramTaskType(t *testing.T) {
	mustParseProgramError(t, `schema_version: 1
program_id: prog-1
parent_branch: develop
base_commit: `+testSHA(1)+`
tasks:
  - task_id: t1
    task_type: PROGRAM
    branch: feature/t1
`, "task_type")
}

func TestParseProgramStateRejectSelfDependency(t *testing.T) {
	mustParseProgramError(t, `schema_version: 1
program_id: prog-1
parent_branch: develop
base_commit: `+testSHA(1)+`
tasks:
  - task_id: t1
    task_type: NORMAL
    branch: feature/t1
    dependencies: [t1]
`, "自依赖")
}

func TestParseProgramStateRejectExternalDependency(t *testing.T) {
	mustParseProgramError(t, `schema_version: 1
program_id: prog-1
parent_branch: develop
base_commit: `+testSHA(1)+`
tasks:
  - task_id: t1
    task_type: NORMAL
    branch: feature/t1
    dependencies: [ghost]
`, "未规划")
}

func TestParseProgramStateRejectDuplicateTask(t *testing.T) {
	mustParseProgramError(t, `schema_version: 1
program_id: prog-1
parent_branch: develop
base_commit: `+testSHA(1)+`
tasks:
  - task_id: t1
    task_type: NORMAL
    branch: feature/t1
  - task_id: t1
    task_type: NORMAL
    branch: feature/t1
`, "重复")
}

func TestParseProgramStateRejectEmptyTasks(t *testing.T) {
	mustParseProgramError(t, `schema_version: 1
program_id: prog-1
parent_branch: develop
base_commit: `+testSHA(1)+`
tasks: []
`, "至少需要")
}

// Program State 与 Task State 物理隔离。
func TestProgramStatePhysicalIsolation(t *testing.T) {
	if ProgramStatePath("p1") == filepath.Join(".agent", "tasks", "p1", "state.yaml") {
		t.Fatal("Program State 路径不得与 Task State 路径相同")
	}
	if ProgramStatePath("p1") != filepath.Join(".agent", "programs", "p1", "program-state.yaml") {
		t.Fatalf("ProgramStatePath = %q", ProgramStatePath("p1"))
	}
	if ProgramRegistryPath("p1") != filepath.Join(".agent", "programs", "p1", "registry.yaml") {
		t.Fatalf("ProgramRegistryPath = %q", ProgramRegistryPath("p1"))
	}
}

// LoadProgramState 从磁盘读取。
func TestLoadProgramStateFromDisk(t *testing.T) {
	root := t.TempDir()
	yaml := `schema_version: 1
program_id: prog-1
parent_branch: develop
base_commit: ` + testSHA(1) + `
tasks:
  - task_id: t1
    task_type: NORMAL
    branch: feature/t1
`
	if err := os.MkdirAll(filepath.Join(root, ProgramDirPath("prog-1")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ProgramStatePath("prog-1")), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	ps, err := LoadProgramState(root, "prog-1")
	if err != nil {
		t.Fatalf("LoadProgramState: %v", err)
	}
	if ps.ProgramID != "prog-1" {
		t.Fatalf("ProgramID = %q", ps.ProgramID)
	}
}

// --- F5：ProgramReview.Evidence / EvidenceCommit 扩展的严格解析与向后兼容 ---

func TestParseProgramReviewEvidencePresent(t *testing.T) {
	ps := mustParseProgram(t, `schema_version: 1
program_id: prog-1
parent_branch: develop
base_commit: `+testSHA(1)+`
tasks:
  - task_id: t1
    task_type: NORMAL
    branch: feature/t1
final_integration:
  status: PASS
  sha: `+shaLike("final")+`
review:
  status: CLEAN
  target: `+shaLike("final")+`
  evidence: `+ProgramCleanerEvidencePath("prog-1")+`
  evidence_commit: `+shaLike("evidence")+`
`)
	if ps.Review.Evidence != ProgramCleanerEvidencePath("prog-1") {
		t.Fatalf("Review.Evidence = %q", ps.Review.Evidence)
	}
	if ps.Review.EvidenceCommit != shaLike("evidence") {
		t.Fatalf("Review.EvidenceCommit = %q", ps.Review.EvidenceCommit)
	}
}

// 向后兼容：旧 CLEAN 状态（仅 status + target，无 evidence/evidence_commit）仍可解析，
// Evidence/EvidenceCommit 为空（Gate 阶段 FAIL，解析不报错）。
func TestParseProgramReviewEvidenceBackwardCompat(t *testing.T) {
	ps := mustParseProgram(t, `schema_version: 1
program_id: prog-1
parent_branch: develop
base_commit: `+testSHA(1)+`
tasks:
  - task_id: t1
    task_type: NORMAL
    branch: feature/t1
final_integration:
  status: PASS
  sha: `+shaLike("final")+`
review:
  status: CLEAN
  target: `+shaLike("final")+`
`)
	if ps.Review.Evidence != "" {
		t.Fatalf("旧状态 Review.Evidence 应为空，实际 %q", ps.Review.Evidence)
	}
	if ps.Review.EvidenceCommit != "" {
		t.Fatalf("旧状态 Review.EvidenceCommit 应为空，实际 %q", ps.Review.EvidenceCommit)
	}
}

// 严格解析：review.evidence_commit 非完整 SHA 时被拒绝（fail-closed）。
func TestParseProgramReviewEvidenceCommitInvalidSHARejected(t *testing.T) {
	mustParseProgramError(t, `schema_version: 1
program_id: prog-1
parent_branch: develop
base_commit: `+testSHA(1)+`
tasks:
  - task_id: t1
    task_type: NORMAL
    branch: feature/t1
review:
  status: CLEAN
  target: `+shaLike("final")+`
  evidence_commit: not-a-sha
`, "evidence_commit")
}

// 严格解析：review 内的未知字段被拒绝（typo fail-closed）。
func TestParseProgramReviewUnknownFieldRejected(t *testing.T) {
	mustParseProgramError(t, `schema_version: 1
program_id: prog-1
parent_branch: develop
base_commit: `+testSHA(1)+`
tasks:
  - task_id: t1
    task_type: NORMAL
    branch: feature/t1
review:
  status: PENDING
  evidenc: review-report.md
`, "evidenc")
}
