package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// 本文件覆盖项目本地 Task 目录分类扩展：解析、预检、迁移、回滚与分类依据校验。

func writeLayoutManifest(t *testing.T, r *testRepo, entries ...TaskLayoutEntry) {
	t.Helper()
	m := TaskLayoutManifest{
		SchemaVersion: TaskLayoutSchemaV1,
		Extension:     "my-shop task layout",
		Categories:    append([]string{}, TaskLayoutCategories...),
		Tasks:         entries,
	}
	raw, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	r.write(TaskLayoutManifestPath, string(raw))
}

func TestTaskLayoutResolveAndAliases(t *testing.T) {
	r, _ := setupTaskBuilderRepo(t)
	writeLayoutManifest(t, r, TaskLayoutEntry{ID: "demo", Category: TaskCategoryActive, Basis: TaskLayoutBasisState})

	// 尚未迁移：清单给出 active，但磁盘仍是旧平铺布局，解析必须以磁盘事实为准。
	rel, err := ResolveTaskArg(r.dir, "demo")
	if err != nil || rel != ".agent/tasks/demo" {
		t.Fatalf("旧布局解析 = %q, err=%v", rel, err)
	}
	// 旧简写别名也必须解析到现有目录。
	rel, err = ResolveTaskArg(r.dir, ".agent/tasks/demo")
	if err != nil || rel != ".agent/tasks/demo" {
		t.Fatalf("旧简写解析 = %q, err=%v", rel, err)
	}

	plan, issues, err := PlanTaskLayout(r.dir)
	if err != nil || len(issues) != 0 {
		t.Fatalf("预检应无冲突: issues=%v err=%v", issues, err)
	}
	if _, err := ApplyTaskLayout(r.dir, plan, time.Now()); err != nil {
		t.Fatal(err)
	}

	rel, err = ResolveTaskArg(r.dir, "demo")
	if err != nil || rel != ".agent/tasks/active/demo" {
		t.Fatalf("迁移后解析 = %q, err=%v", rel, err)
	}
	rel, err = ResolveTaskArg(r.dir, ".agent/tasks/demo")
	if err != nil || rel != ".agent/tasks/active/demo" {
		t.Fatalf("迁移后旧别名解析 = %q, err=%v", rel, err)
	}
	rel, err = ResolveTaskArg(r.dir, ".agent/tasks/active/demo")
	if err != nil || rel != ".agent/tasks/active/demo" {
		t.Fatalf("分类路径解析 = %q, err=%v", rel, err)
	}
	if _, err := ResolveTaskArg(r.dir, "absent"); err == nil || !strings.Contains(err.Error(), "未找到") {
		t.Fatalf("未知 Task 应报错，实际 %v", err)
	}
	if _, err := TaskDirRel(r.dir, "demo"); err != nil {
		t.Fatalf("TaskDirRel 迁移后失败: %v", err)
	}
}

func TestTaskLayoutMigratePreservesContentAndRollback(t *testing.T) {
	r, _ := setupTaskBuilderRepo(t)
	writeLayoutManifest(t, r, TaskLayoutEntry{ID: "demo", Category: TaskCategoryActive, Basis: TaskLayoutBasisState})
	before, err := hashTaskDir(r.dir, ".agent/tasks/demo")
	if err != nil {
		t.Fatal(err)
	}

	plan, issues, err := PlanTaskLayout(r.dir)
	if err != nil || len(issues) != 0 {
		t.Fatalf("预检失败: %v %v", issues, err)
	}
	journal, err := ApplyTaskLayout(r.dir, plan, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(journal.Tasks) != 1 || journal.Tasks[0].To != ".agent/tasks/active/demo" {
		t.Fatalf("迁移日志不符: %+v", journal.Tasks)
	}
	if _, err := os.Stat(filepath.Join(r.dir, ".agent/tasks/demo")); !os.IsNotExist(err) {
		t.Fatalf("旧目录仍存在: %v", err)
	}
	if err := VerifyTaskDirHashes(r.dir, ".agent/tasks/active/demo", before); err != nil {
		t.Fatalf("迁移后内容不一致: %v", err)
	}
	loaded, err := LoadTaskLayoutJournal(r.dir, TaskLayoutJournalPath)
	if err != nil || len(loaded.Tasks) != 1 {
		t.Fatalf("读取迁移日志失败: %v %+v", err, loaded)
	}

	// 迁移后 State 仍可通过统一解析读取，Gate 路径不变。
	if _, err := LoadTaskStateFile(r.dir, "demo"); err != nil {
		t.Fatalf("迁移后读取 State 失败: %v", err)
	}

	if _, moved, err := RollbackTaskLayout(r.dir, TaskLayoutJournalPath, false); err != nil || moved != 1 {
		t.Fatalf("回滚失败: moved=%d err=%v", moved, err)
	}
	if _, err := os.Stat(filepath.Join(r.dir, ".agent/tasks/demo/state.yaml")); err != nil {
		t.Fatalf("回滚后旧路径不存在: %v", err)
	}
	after, err := hashTaskDir(r.dir, ".agent/tasks/demo")
	if err != nil {
		t.Fatal(err)
	}
	for name, sum := range before {
		if after[name] != sum {
			t.Fatalf("回滚后 %s 内容与迁移前不一致", name)
		}
	}
	// 回滚幂等：再次执行不应报错，也不应重复移动。
	if _, moved, err := RollbackTaskLayout(r.dir, TaskLayoutJournalPath, false); err != nil || moved != 0 {
		t.Fatalf("重复回滚应幂等: moved=%d err=%v", moved, err)
	}
}

func TestTaskLayoutPreflightRejectsConflicts(t *testing.T) {
	t.Run("未登记分类", func(t *testing.T) {
		r, _ := setupTaskBuilderRepo(t)
		writeLayoutManifest(t, r)
		_, issues, err := PlanTaskLayout(r.dir)
		if err != nil {
			t.Fatal(err)
		}
		if !hasLayoutIssue(issues, "unclassified", "demo") && !hasLayoutIssue(issues, "manifest", "") {
			t.Fatalf("应报告未分类，实际 %v", issues)
		}
	})

	t.Run("目标目录已存在", func(t *testing.T) {
		r, _ := setupTaskBuilderRepo(t)
		writeLayoutManifest(t, r, TaskLayoutEntry{ID: "demo", Category: TaskCategoryActive, Basis: TaskLayoutBasisState})
		r.write(".agent/tasks/active/demo/state.yaml", "schema_version: 4\n")
		_, issues, err := PlanTaskLayout(r.dir)
		if err != nil {
			t.Fatal(err)
		}
		if !hasLayoutIssue(issues, "duplicate", "demo") && !hasLayoutIssue(issues, "target-exists", "demo") {
			t.Fatalf("应报告目标占用/重复，实际 %v", issues)
		}
	})

	t.Run("同一 Task 出现在两个分类", func(t *testing.T) {
		r, _ := setupTaskBuilderRepo(t)
		writeLayoutManifest(t, r, TaskLayoutEntry{ID: "demo", Category: TaskCategoryActive, Basis: TaskLayoutBasisState})
		r.write(".agent/tasks/completed/demo/state.yaml", "schema_version: 4\n")
		_, issues, err := PlanTaskLayout(r.dir)
		if err != nil {
			t.Fatal(err)
		}
		if !hasLayoutIssue(issues, "duplicate", "demo") {
			t.Fatalf("应报告 duplicate，实际 %v", issues)
		}
	})

	t.Run("清单非法", func(t *testing.T) {
		r, _ := setupTaskBuilderRepo(t)
		r.write(TaskLayoutManifestPath, "schema_version: 1\ntasks:\n  - id: demo\n    category: nowhere\n    basis: state\n")
		if _, err := LoadTaskLayout(r.dir); err == nil {
			t.Fatal("非法分类应被拒绝")
		}
	})
}

func TestValidateTaskLayoutRejectsUnverifiedCompletion(t *testing.T) {
	r, _ := setupTaskBuilderRepo(t)
	writeLayoutManifest(t, r, TaskLayoutEntry{
		ID: "demo", Category: TaskCategoryCompleted, Basis: TaskLayoutBasisState,
	})
	issues, err := ValidateTaskLayout(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLayoutIssue(issues, "completed-status", "demo") {
		t.Fatalf("未交付的 Task 不应被验证为 completed，实际 %v", issues)
	}
}

func TestValidateTaskLayoutReportsLegacyLayout(t *testing.T) {
	r, _ := setupTaskBuilderRepo(t)
	writeLayoutManifest(t, r, TaskLayoutEntry{ID: "demo", Category: TaskCategoryActive, Basis: TaskLayoutBasisState})
	issues, err := ValidateTaskLayout(r.dir)
	if err != nil {
		t.Fatal(err)
	}
	if !hasLayoutIssue(issues, "legacy-layout", "demo") {
		t.Fatalf("旧平铺布局应被报告，实际 %v", issues)
	}
}

func TestBuildIndexIncludesLayoutCategory(t *testing.T) {
	r, _ := setupTaskBuilderRepo(t)
	writeLayoutManifest(t, r, TaskLayoutEntry{ID: "demo", Category: TaskCategoryActive, Basis: TaskLayoutBasisState})
	plan, issues, err := PlanTaskLayout(r.dir)
	if err != nil || len(issues) != 0 {
		t.Fatalf("预检失败: %v %v", issues, err)
	}
	if _, err := ApplyTaskLayout(r.dir, plan, time.Now()); err != nil {
		t.Fatal(err)
	}
	snap, err := BuildIndex(r.dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tasks) != 1 {
		t.Fatalf("索引任务数 = %d", len(snap.Tasks))
	}
	if snap.Tasks[0].Category != TaskCategoryActive {
		t.Fatalf("category = %q", snap.Tasks[0].Category)
	}
	if !strings.HasSuffix(snap.Tasks[0].Path, ".agent/tasks/active/demo/state.yaml") {
		t.Fatalf("path = %q", snap.Tasks[0].Path)
	}
	tsv := snap.TSV()
	if !strings.Contains(tsv, "\tcategory\n") {
		t.Fatal("TSV 缺少 category 列")
	}
}

func hasLayoutIssue(issues []TaskLayoutIssue, kind, id string) bool {
	for _, issue := range issues {
		if issue.Kind == kind && issue.ID == id {
			return true
		}
	}
	return false
}

// TestGatesAndEventPathsAfterLayoutMigration 验证迁移后 Gate 与事件路径仍走同一解析规则。
func TestGatesAndEventPathsAfterLayoutMigration(t *testing.T) {
	r, _ := setupTaskBuilderRepo(t)
	writeLayoutManifest(t, r, TaskLayoutEntry{ID: "demo", Category: TaskCategoryActive, Basis: TaskLayoutBasisState})
	plan, issues, err := PlanTaskLayout(r.dir)
	if err != nil || len(issues) != 0 {
		t.Fatalf("预检失败: %v %v", issues, err)
	}
	if _, err := ApplyTaskLayout(r.dir, plan, time.Now()); err != nil {
		t.Fatal(err)
	}
	if res := r.validator().GateTaskBuilderInit(".agent/tasks/active/demo"); res.Status != StatusPass {
		t.Fatalf("迁移后 taskbuilder-init 应 PASS: %+v", res)
	}
	// Gate 接收的是真实目录（相对仓库根）：旧简写由 CLI 的 ResolveTaskArg 解析后再传入，
	// 直接把不存在的旧路径交给 Validator 必须显式 ERROR，不能静默读取错误位置。
	if res := r.validator().GateTaskBuilderInit(".agent/tasks/demo"); res.Status != StatusError {
		t.Fatalf("不存在的旧目录应 ERROR: %+v", res)
	}
	if resolved, err := ResolveTaskArg(r.dir, ".agent/tasks/demo"); err != nil || resolved != ".agent/tasks/active/demo" {
		t.Fatalf("命令行解析旧简写失败: %q %v", resolved, err)
	}
	rel, err := NewEventWriter(r.dir).Path(ScopeTask, "demo")
	if err != nil || rel != ".agent/tasks/active/demo/events.md" {
		t.Fatalf("事件路径 = %q, err=%v", rel, err)
	}
}
