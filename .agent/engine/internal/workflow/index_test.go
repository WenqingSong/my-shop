package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func writeIndexState(t *testing.T, r *testRepo, dir string, s State) {
	t.Helper()
	raw, err := yaml.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	r.write(".agent/tasks/"+dir+"/state.yaml", string(raw))
}

func TestIndexCorruptLegacyAndMembership(t *testing.T) {
	r, base := setupTaskBuilderRepo(t)
	s := baseState()
	s.SchemaVersion = 4
	s.TaskID = "mismatch"
	s.TaskType = TaskTypeNormal
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: base, TaskBranch: "missing"}
	s.Program = &ProgramRef{ID: "unlisted", Version: 1}
	s.Blocked.Active = true
	writeIndexState(t, r, "ghost", s)
	legacy := baseState()
	legacy.TaskID = "legacy"
	writeIndexState(t, r, "legacy", legacy)
	r.write(".agent/tasks/broken/state.yaml", "schema_version: [\n")
	p := programWithTasks("batch", "develop", base, []ProgramTask{{TaskID: "demo", TaskType: TaskTypeNormal, Branch: "feature/agent-workflow-v2"}})
	raw, err := yaml.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	r.write(".agent/programs/batch/program-state.yaml", string(raw))
	before := r.git("status", "--porcelain")
	now := time.Now()
	snap, err := BuildIndex(r.dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tasks) != 4 {
		t.Fatalf("lost rows: %+v", snap.Tasks)
	}
	if snap.Tasks[0].ID != "mismatch" {
		t.Fatal("blocked Task not first")
	}
	byPath := map[string]IndexTask{}
	for _, task := range snap.Tasks {
		byPath[task.Path] = task
	}
	ghost := byPath[".agent/tasks/ghost/state.yaml"]
	for _, flag := range []string{"ghost", "orphan", "no-branch"} {
		if !strings.Contains(strings.Join(ghost.Flags, ","), flag) {
			t.Fatalf("missing %s: %+v", flag, ghost)
		}
	}
	if !strings.Contains(snap.TSV(), "corrupt=1") || !strings.Contains(snap.TSV(), "解析") {
		t.Fatal(snap.TSV())
	}
	if !strings.Contains(snap.ProgramsMarkdown(), "orphan: demo") {
		t.Fatal(snap.ProgramsMarkdown())
	}
	if !strings.Contains(snap.TSV(), "v3") {
		t.Fatal("legacy not flagged")
	}
	again, err := BuildIndex(r.dir, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if snap.TSV() != again.TSV() || snap.ProgramsMarkdown() != again.ProgramsMarkdown() {
		t.Fatal("non-deterministic data")
	}
	if snap.Metadata() == again.Metadata() {
		t.Fatal("generation timestamp not updated")
	}
	if before != r.git("status", "--porcelain") {
		t.Fatal("index mutated repository")
	}
}

func TestIndexIgnoredRebuildPreservesGates(t *testing.T) {
	r, _ := setupHandoffState(t)
	r.write(".agent/index/.gitignore", "*\n!.gitignore\n!README.md\n")
	r.write(".agent/index/README.md", "DERIVED\n")
	r.commit("index skeleton")
	r.syncOrigin("feature/agent-workflow-v2")
	before := r.validator().GateHandoffPrepared(".agent/tasks/demo")
	expectStatus(t, before, StatusPass)
	cleanerBefore := r.validator().GateCleanerStart(".agent/tasks/demo")
	snap, err := BuildIndex(r.dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 2; round++ {
		r.write(".agent/index/tasks.tsv", snap.TSV())
		r.write(".agent/index/programs.md", snap.ProgramsMarkdown())
		r.write(".agent/index/index-meta.yaml", snap.Metadata())
		if got := r.git("status", "--porcelain"); got != "" {
			t.Fatal(got)
		}
		expectStatus(t, r.validator().GateHandoffPrepared(".agent/tasks/demo"), before.Status)
		expectStatus(t, r.validator().GateCleanerStart(".agent/tasks/demo"), cleanerBefore.Status)
		for _, name := range []string{"tasks.tsv", "programs.md", "index-meta.yaml"} {
			ignored := r.git("check-ignore", "-v", ".agent/index/"+name)
			if !strings.Contains(ignored, ".agent/index/.gitignore:1:") {
				t.Fatal(ignored)
			}
			if err := os.Remove(filepath.Join(r.dir, ".agent/index", name)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestIndexScaleAndSymlink(t *testing.T) {
	r, base := setupTaskBuilderRepo(t)
	s := baseState()
	s.SchemaVersion = 4
	s.TaskType = TaskTypeNormal
	s.Git = &GitBaseline{ParentBranch: "develop", BaseCommit: base, TaskBranch: "feature/agent-workflow-v2"}
	for i := 0; i < 199; i++ {
		id := fmt.Sprintf("task-%03d", i)
		s.TaskID = id
		s.Git.TaskBranch = "feature/" + id
		r.git("branch", s.Git.TaskBranch, base)
		writeIndexState(t, r, id, s)
	}
	start := time.Now()
	snap, err := BuildIndex(r.dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tasks) != 200 {
		t.Fatalf("got %d", len(snap.Tasks))
	}
	elapsed := time.Since(start)
	t.Logf("200 Tasks: %s", elapsed)
	if elapsed > 60*time.Second {
		t.Fatal("index exceeded 60s")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(r.dir, ".agent/tasks/linked")); err != nil {
		t.Fatal(err)
	}
	snap, err = BuildIndex(r.dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snap.TSV(), "symlink not indexed") || len(snap.Tasks) != 201 {
		t.Fatal("symlink silently followed or omitted")
	}
}

// A Task listed by multiple Programs must report warnings in stable order.
func TestIndexMultipleProgramWarningsDeterministic(t *testing.T) {
	r, base := setupTaskBuilderRepo(t)
	for _, id := range []string{"alpha", "beta", "gamma"} {
		p := programWithTasks(id, "develop", base, []ProgramTask{{TaskID: "demo", TaskType: TaskTypeNormal, Branch: "feature/agent-workflow-v2"}})
		raw, err := yaml.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		r.write(".agent/programs/"+id+"/program-state.yaml", string(raw))
	}
	now := time.Now()
	first, err := BuildIndex(r.dir, now)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 15; i++ {
		next, err := BuildIndex(r.dir, now)
		if err != nil {
			t.Fatal(err)
		}
		if next.TSV() != first.TSV() || next.ProgramsMarkdown() != first.ProgramsMarkdown() {
			t.Fatal("Program warning output order changed")
		}
	}
}
