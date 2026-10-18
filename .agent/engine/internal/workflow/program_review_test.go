package workflow

import "testing"

// --- Program Cleaner 审查报告严格解析与证据路径约束（F5） ---

func TestParseProgramCleanerReportValid(t *testing.T) {
	rep, err := ParseProgramCleanerReport([]byte("program_id: prog-1\nkind: program-cleaner-review\ntarget: " + testSHA(1) + "\nstatus: CLEAN\n"))
	if err != nil {
		t.Fatalf("合法报告应解析成功，实际 %v", err)
	}
	if rep.ProgramID != "prog-1" || rep.Kind != ProgramCleanerReviewKind || rep.Target != testSHA(1) || rep.Status != ReviewClean {
		t.Fatalf("报告字段错误: %+v", rep)
	}
}

func TestParseProgramCleanerReportWrongKind(t *testing.T) {
	if _, err := ParseProgramCleanerReport([]byte("program_id: prog-1\nkind: other-report\ntarget: " + testSHA(1) + "\nstatus: CLEAN\n")); err == nil {
		t.Fatal("非 program-cleaner-review 类型应被拒绝")
	}
}

func TestParseProgramCleanerReportNotClean(t *testing.T) {
	if _, err := ParseProgramCleanerReport([]byte("program_id: prog-1\nkind: program-cleaner-review\ntarget: " + testSHA(1) + "\nstatus: CHANGES_REQUIRED\n")); err == nil {
		t.Fatal("非 CLEAN 结论应被拒绝")
	}
}

func TestParseProgramCleanerReportUnknownField(t *testing.T) {
	if _, err := ParseProgramCleanerReport([]byte("program_id: prog-1\nkind: program-cleaner-review\ntarget: " + testSHA(1) + "\nstatus: CLEAN\nrole: Cleaner\n")); err == nil {
		t.Fatal("未知字段应被 strict decode 拒绝")
	}
}

func TestParseProgramCleanerReportMissingTarget(t *testing.T) {
	if _, err := ParseProgramCleanerReport([]byte("program_id: prog-1\nkind: program-cleaner-review\nstatus: CLEAN\n")); err == nil {
		t.Fatal("缺少 target 应被拒绝")
	}
}

func TestProgramCleanerEvidencePath(t *testing.T) {
	want := ".agent/programs/prog-1/cleaner-review.yaml"
	if got := ProgramCleanerEvidencePath("prog-1"); got != want {
		t.Fatalf("ProgramCleanerEvidencePath = %q，期望 %q", got, want)
	}
}

func TestIsProgramCleanerEvidencePath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{".agent/programs/prog-1/cleaner-review.yaml", true},
		{`.agent\programs\prog-1\cleaner-review.yaml`, true}, // 反斜杠归一化为斜杠
		{"README.md", false},
		{".agent/programs/prog-1/review-report.md", false},
		{".agent/programs/other/cleaner-review.yaml", false},
		{".agent/tasks/t1/cleaner-review.yaml", false},
		{"../../etc/passwd", false},
	}
	for _, c := range cases {
		if got := isProgramCleanerEvidencePath("prog-1", c.path); got != c.want {
			t.Fatalf("isProgramCleanerEvidencePath(%q) = %v，期望 %v", c.path, got, c.want)
		}
	}
}
