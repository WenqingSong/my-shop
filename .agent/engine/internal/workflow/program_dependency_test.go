package workflow

import (
	"reflect"
	"testing"
)

// --- 依赖图校验与集成批次（Task 13）---

func TestComputeBatchesLinear(t *testing.T) {
	tasks := []ProgramTask{
		{TaskID: "a", TaskType: TaskTypeNormal, Branch: "feature/a"},
		{TaskID: "b", TaskType: TaskTypeNormal, Branch: "feature/b", Dependencies: []string{"a"}},
		{TaskID: "c", TaskType: TaskTypeNormal, Branch: "feature/c", Dependencies: []string{"b"}},
	}
	batches, err := ComputeBatches(tasks)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"a"}, {"b"}, {"c"}}
	if !reflect.DeepEqual(batches, want) {
		t.Fatalf("batches=%v 期望 %v", batches, want)
	}
}

func TestComputeBatchesParallel(t *testing.T) {
	tasks := []ProgramTask{
		{TaskID: "a", TaskType: TaskTypeNormal, Branch: "feature/a"},
		{TaskID: "b", TaskType: TaskTypeNormal, Branch: "feature/b"},
		{TaskID: "c", TaskType: TaskTypeNormal, Branch: "feature/c", Dependencies: []string{"a", "b"}},
	}
	batches, err := ComputeBatches(tasks)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"a", "b"}, {"c"}}
	if !reflect.DeepEqual(batches, want) {
		t.Fatalf("batches=%v 期望 %v", batches, want)
	}
}

func TestValidateDependencyGraphCycle(t *testing.T) {
	tasks := []ProgramTask{
		{TaskID: "a", TaskType: TaskTypeNormal, Branch: "feature/a", Dependencies: []string{"b"}},
		{TaskID: "b", TaskType: TaskTypeNormal, Branch: "feature/b", Dependencies: []string{"a"}},
	}
	if issues := ValidateDependencyGraph(tasks); len(issues) == 0 {
		t.Fatal("期望检测到环")
	}
	if _, err := ComputeBatches(tasks); err == nil {
		t.Fatal("期望有环时报错")
	}
}

func TestValidateDependencyGraphOK(t *testing.T) {
	tasks := []ProgramTask{
		{TaskID: "a", TaskType: TaskTypeNormal, Branch: "feature/a"},
		{TaskID: "b", TaskType: TaskTypeNormal, Branch: "feature/b", Dependencies: []string{"a"}},
	}
	if issues := ValidateDependencyGraph(tasks); len(issues) != 0 {
		t.Fatalf("期望无环，实际 %v", issues)
	}
}
