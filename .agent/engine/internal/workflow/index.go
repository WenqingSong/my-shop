package workflow

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// IndexSnapshot is disposable discovery data, never approval evidence.
// Building it only reads direct Task/Program state files and local Git objects.
type IndexSnapshot struct {
	SourceSHA   string
	GeneratedAt time.Time
	Tasks       []IndexTask
	Programs    []IndexProgram
}

type IndexTask struct {
	ID, Path, Schema, Type, Workflow, Program string
	Contract, Review, Owner, Delivery         string
	ReviewTarget, DeliveryHead, OwnerTarget   string
	Blocked                                   bool
	Branch, Base, Intake, LastCommit, Updated string
	Flags                                     []string
	Reason                                    string
	stamp                                     int64
}

type IndexProgram struct {
	ID, Path, Integration, Review, Owner string
	Tasks                                []string
	Warnings                             []string
}

func indexGit(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// indexRead refuses symlinks instead of following a project path outside the checkout.
func indexCheckPath(root, rel string) error {
	current := root
	for _, part := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink not indexed: %s", rel)
		}
	}
	return nil
}

func indexRead(root, rel string) ([]byte, error) {
	if err := indexCheckPath(root, rel); err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(root, rel))
}

func indexDirs(root, rel string) ([]os.DirEntry, error) {
	if err := indexCheckPath(root, rel); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return os.ReadDir(filepath.Join(root, rel))
}

// BuildIndex tolerates corrupt individual records, preserving each row and reason.
// Repository-level I/O failures remain errors rather than looking like an empty index.
func BuildIndex(root string, now time.Time) (IndexSnapshot, error) {
	sha, err := indexGit(root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return IndexSnapshot{}, fmt.Errorf("index HEAD: %w", err)
	}
	snap := IndexSnapshot{SourceSHA: sha, GeneratedAt: now.UTC()}
	entries, err := indexDirs(root, ".agent/programs")
	if err != nil {
		return snap, err
	}
	members := map[string]map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			continue
		}
		rel := filepath.Join(".agent/programs", e.Name(), "program-state.yaml")
		p := IndexProgram{ID: e.Name(), Path: filepath.ToSlash(rel), Integration: "-", Review: "-", Owner: "-"}
		raw, readErr := indexRead(root, rel)
		var state ProgramState
		if readErr == nil {
			state, readErr = ParseProgramState(raw)
		}
		if readErr != nil {
			p.Warnings = append(p.Warnings, "corrupt: "+readErr.Error())
		} else {
			p.ID = state.ProgramID
			p.Integration = state.FinalIntegration.Status
			p.Review = state.Review.Status
			p.Owner = state.Owner.Status
			if p.ID != e.Name() {
				p.Warnings = append(p.Warnings, "ghost: program_id differs from directory")
			}
			members[e.Name()] = map[string]bool{}
			for _, t := range state.Tasks {
				p.Tasks = append(p.Tasks, t.TaskID)
				members[e.Name()][t.TaskID] = true
			}
		}
		snap.Programs = append(snap.Programs, p)
	}
	entries, err = indexDirs(root, ".agent/tasks")
	if err != nil {
		return snap, err
	}
	type branchInfo struct {
		sha, stamp string
		exists     bool
	}
	branches := map[string]branchInfo{}
	taskPrograms := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			continue
		}
		rel := filepath.Join(".agent/tasks", e.Name(), "state.yaml")
		t := IndexTask{ID: e.Name(), Path: filepath.ToSlash(rel)}
		raw, readErr := indexRead(root, rel)
		var state State
		if readErr == nil {
			state, readErr = parseState(raw)
		}
		if readErr == nil {
			if issues := schemaValidation(state); len(issues) > 0 {
				readErr = fmt.Errorf("invalid state: %v", issues)
			}
		}
		if readErr != nil {
			t.Flags = []string{"corrupt"}
			t.Reason = readErr.Error()
			snap.Tasks = append(snap.Tasks, t)
			continue
		}
		t.ID = state.TaskID
		t.Schema = strconv.Itoa(state.SchemaVersion)
		t.Type = state.TaskType
		t.Workflow = state.TaskType
		if t.ID != e.Name() {
			t.Flags = append(t.Flags, "ghost")
			t.Reason = "task_id differs from directory"
		}
		t.Contract = state.Contract.Status
		t.Review = state.Review.Status
		t.Owner = state.Owner.Status
		t.Delivery = state.Delivery.Status
		t.ReviewTarget = state.Review.Target
		t.DeliveryHead = state.Delivery.FeatureHead
		t.OwnerTarget = state.Owner.ReviewTarget
		t.Blocked = state.Blocked.Active
		if state.SchemaVersion <= 3 {
			t.Flags = append(t.Flags, "v3")
		}
		if state.Git != nil {
			t.Branch = state.Git.TaskBranch
			t.Base = state.Git.BaseCommit
		}
		if state.Intake != nil {
			t.Intake = state.Intake.ID
		}
		if state.Program != nil {
			t.Program = state.Program.ID
			t.Workflow = "COORDINATED(child)"
			if !members[t.Program][t.ID] {
				t.Flags = append(t.Flags, "orphan")
				t.Reason = joinReason(t.Reason, "Program missing, corrupt, or does not list Task")
			}
		}
		taskPrograms[e.Name()] = t.Program
		info, ok := branches[t.Branch]
		if !ok && t.Branch != "" {
			for _, prefix := range []string{"refs/heads/", "refs/remotes/origin/"} {
				ref := prefix + t.Branch
				if _, err := indexGit(root, "show-ref", "--verify", ref); err != nil {
					continue
				}
				log, err := indexGit(root, "log", "-1", "--format=%ct %H", ref, "--")
				fields := strings.Fields(log)
				if err == nil && len(fields) == 2 {
					info = branchInfo{sha: fields[1], stamp: fields[0], exists: true}
					break
				}
			}
			branches[t.Branch] = info
		}
		if !info.exists {
			t.Flags = append(t.Flags, "no-branch")
		} else {
			t.LastCommit = info.sha
			t.stamp, _ = strconv.ParseInt(info.stamp, 10, 64)
			t.Updated = time.Unix(t.stamp, 0).UTC().Format(time.RFC3339)
			if now.Sub(time.Unix(t.stamp, 0)) > 180*24*time.Hour {
				t.Flags = append(t.Flags, "stale")
			}
		}
		snap.Tasks = append(snap.Tasks, t)
	}
	// Reverse membership checks catch a Program-only declaration as well.
	for i := range snap.Programs {
		p := &snap.Programs[i]
		dir := filepath.Base(filepath.Dir(p.Path))
		for _, id := range p.Tasks {
			program, exists := taskPrograms[id]
			if !exists || program != dir {
				p.Warnings = append(p.Warnings, "orphan: "+id+" missing/corrupt or points to another Program")
			}
		}
	}
	for i := range snap.Tasks {
		t := &snap.Tasks[i]
		dir := filepath.Base(filepath.Dir(t.Path))
		for _, programRow := range snap.Programs {
			program := filepath.Base(filepath.Dir(programRow.Path))
			list := members[program]
			if list[dir] && t.Program != program {
				t.Flags = append(t.Flags, "orphan")
				t.Reason = joinReason(t.Reason, "listed in Program "+program+" but Task reference differs")
			}
		}
		sort.Strings(t.Flags)
		t.Flags = uniqueStrings(t.Flags)
	}
	sort.SliceStable(snap.Tasks, func(i, j int) bool {
		a, b := snap.Tasks[i], snap.Tasks[j]
		if a.Blocked != b.Blocked {
			return a.Blocked
		}
		if a.stamp != b.stamp {
			return a.stamp > b.stamp
		}
		return a.Path < b.Path
	})
	return snap, nil
}

func joinReason(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}
func uniqueStrings(in []string) []string {
	out := []string{}
	for _, s := range in {
		if len(out) == 0 || out[len(out)-1] != s {
			out = append(out, s)
		}
	}
	return out
}
func indexCell(s string) string {
	if s == "" {
		return "-"
	}
	return strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(s)
}

// TSV is stable for a fixed source/state; only Metadata contains generation time.
func (s IndexSnapshot) TSV() string {
	var b bytes.Buffer
	b.WriteString("# DERIVED — 不得作为审批证据\n")
	w := csv.NewWriter(&b)
	w.Comma = '\t'
	_ = w.Write([]string{"task_id", "path", "schema", "task_type", "workflow", "program", "contract", "review", "owner", "delivery", "review_target", "delivery_head", "owner_target", "blocked", "branch", "base", "intake", "last_commit", "updated", "flags", "reason"})
	corrupt := 0
	for _, t := range s.Tasks {
		row := []string{t.ID, t.Path, t.Schema, t.Type, t.Workflow, t.Program, t.Contract, t.Review, t.Owner, t.Delivery, t.ReviewTarget, t.DeliveryHead, t.OwnerTarget, strconv.FormatBool(t.Blocked), t.Branch, t.Base, t.Intake, t.LastCommit, t.Updated, strings.Join(t.Flags, ","), t.Reason}
		for _, flag := range t.Flags {
			if flag == "corrupt" {
				corrupt++
				row[13] = "-"
			}
		}
		for i := range row {
			row[i] = indexCell(row[i])
		}
		_ = w.Write(row)
	}
	w.Flush()
	fmt.Fprintf(&b, "# tasks=%d corrupt=%d\n", len(s.Tasks), corrupt)
	return b.String()
}

func (s IndexSnapshot) ProgramsMarkdown() string {
	var b strings.Builder
	b.WriteString("# DERIVED — 不得作为审批证据\n\n| Program | Path | Tasks | Integration | Review | Owner | Warnings |\n|---|---|---|---|---|---|---|\n")
	for _, p := range s.Programs {
		row := []string{p.ID, p.Path, strings.Join(p.Tasks, ", "), p.Integration, p.Review, p.Owner, strings.Join(p.Warnings, "; ")}
		for i := range row {
			row[i] = strings.ReplaceAll(indexCell(row[i]), "|", "\\|")
		}
		fmt.Fprintf(&b, "| %s |\n", strings.Join(row, " | "))
	}
	return b.String()
}

func (s IndexSnapshot) Metadata() string {
	return fmt.Sprintf("# DERIVED — 不得作为审批证据\nindex_version: 1\ngenerated_at: %q\nsource_sha: %q\n", s.GeneratedAt.Format(time.RFC3339Nano), s.SourceSHA)
}
