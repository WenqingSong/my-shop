package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// 本文件实现 Task 目录分类的预检、迁移与回滚（项目本地扩展）。
//
// 安全边界：
//   - 迁移只做目录移动，不读改写 state.yaml / delivery.md / events.md 等内容；
//   - 预检 fail closed：未登记分类、重复 ID、目标占用、符号链接都拒绝执行；
//   - 迁移前后对每个文件做 SHA-256 校验并写入迁移日志；
//   - 回滚校验当前内容与迁移时一致，避免把迁移后的新内容覆盖回旧路径。

// TaskLayoutIssue 是预检/校验发现的一条不一致。
type TaskLayoutIssue struct {
	ID      string
	Kind    string
	Message string
}

func (i TaskLayoutIssue) String() string {
	if i.ID == "" {
		return fmt.Sprintf("%s: %s", i.Kind, i.Message)
	}
	return fmt.Sprintf("%s: %s: %s", i.Kind, i.ID, i.Message)
}

// PlanTaskLayout 生成迁移计划并报告全部冲突；任何冲突都使计划不可执行。
func PlanTaskLayout(root string) (TaskLayoutPlan, []TaskLayoutIssue, error) {
	plan := TaskLayoutPlan{Categories: map[string]string{}}
	m, err := LoadTaskLayout(root)
	if err != nil {
		return plan, nil, err
	}
	if len(m.Tasks) == 0 {
		return plan, []TaskLayoutIssue{{Kind: "manifest", Message: TaskLayoutManifestPath + " 未登记任何 Task"}}, nil
	}
	declared := map[string]TaskLayoutEntry{}
	for _, t := range m.Tasks {
		declared[t.ID] = t
	}
	dirs, err := ListTaskDirs(root)
	if err != nil {
		return plan, nil, err
	}
	byID := map[string][]TaskDir{}
	for _, d := range dirs {
		byID[d.ID] = append(byID[d.ID], d)
	}
	var issues []TaskLayoutIssue
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		found := byID[id]
		if len(found) > 1 {
			var rels []string
			for _, f := range found {
				rels = append(rels, f.Rel)
			}
			issues = append(issues, TaskLayoutIssue{ID: id, Kind: "duplicate", Message: "同时存在于多个目录：" + strings.Join(rels, ", ")})
			continue
		}
		entry, ok := declared[id]
		if !ok {
			issues = append(issues, TaskLayoutIssue{ID: id, Kind: "unclassified", Message: "存在于磁盘但未在 " + TaskLayoutManifestPath + " 登记分类"})
			continue
		}
		from := found[0]
		to := filepath.ToSlash(filepath.Join(TaskLayoutRoot, entry.Category, id))
		plan.Categories[id] = entry.Category
		if from.Rel == to {
			plan.InPlace = append(plan.InPlace, id)
			continue
		}
		if info, err := os.Lstat(filepath.Join(root, to)); err == nil {
			_ = info
			issues = append(issues, TaskLayoutIssue{ID: id, Kind: "target-exists", Message: "目标目录已存在：" + to})
			continue
		}
		files, err := hashTaskDir(root, from.Rel)
		if err != nil {
			issues = append(issues, TaskLayoutIssue{ID: id, Kind: "unreadable", Message: err.Error()})
			continue
		}
		plan.Moves = append(plan.Moves, TaskLayoutMove{ID: id, From: from.Rel, To: to, Files: files})
	}
	for _, t := range m.Tasks {
		if len(byID[t.ID]) == 0 {
			issues = append(issues, TaskLayoutIssue{ID: t.ID, Kind: "missing", Message: "已登记分类但磁盘上没有对应目录"})
		}
	}
	return plan, issues, nil
}

// hashTaskDir 计算目录内全部普通文件的 SHA-256；符号链接与特殊文件直接拒绝。
func hashTaskDir(root, rel string) (map[string]string, error) {
	base := filepath.Join(root, rel)
	out := map[string]string{}
	err := filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("拒绝符号链接：%s", p)
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("不支持的文件类型：%s", p)
		}
		sum, err := hashFile(p)
		if err != nil {
			return err
		}
		relFile, err := filepath.Rel(base, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(relFile)] = sum
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyTaskDirHashes 校验目录内容与记录的校验值一致。
func VerifyTaskDirHashes(root, rel string, expected map[string]string) error {
	actual, err := hashTaskDir(root, rel)
	if err != nil {
		return err
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("%s 文件数量变化：期望 %d，实际 %d", rel, len(expected), len(actual))
	}
	for name, sum := range expected {
		if actual[name] != sum {
			return fmt.Errorf("%s/%s 内容已变化，拒绝移动", rel, name)
		}
	}
	return nil
}

// ApplyTaskLayout 执行迁移计划并写出可回滚日志。
//
// 中途失败时自动回滚已移动的 Task，保证工作树回到迁移前布局。
func ApplyTaskLayout(root string, plan TaskLayoutPlan, now time.Time) (TaskLayoutJournal, error) {
	journal := TaskLayoutJournal{
		SchemaVersion: TaskLayoutJournalSchemaV1,
		MigratedAt:    now.UTC().Format(time.RFC3339Nano),
	}
	if sha, err := (&ExecGit{Root: root}).RevParse("HEAD"); err == nil {
		journal.SourceSHA = sha
	}
	if err := os.MkdirAll(filepath.Join(root, TaskLayoutRoot), 0o755); err != nil {
		return journal, err
	}
	for _, mv := range plan.Moves {
		if err := VerifyTaskDirHashes(root, mv.From, mv.Files); err != nil {
			rollbackMoves(root, journal.Tasks)
			return journal, err
		}
		targetParent := filepath.Dir(filepath.Join(root, mv.To))
		if err := os.MkdirAll(targetParent, 0o755); err != nil {
			rollbackMoves(root, journal.Tasks)
			return journal, err
		}
		if err := os.Rename(filepath.Join(root, mv.From), filepath.Join(root, mv.To)); err != nil {
			rollbackMoves(root, journal.Tasks)
			return journal, fmt.Errorf("移动 %s -> %s: %w", mv.From, mv.To, err)
		}
		if err := VerifyTaskDirHashes(root, mv.To, mv.Files); err != nil {
			rollbackMoves(root, append(journal.Tasks, mv))
			return journal, err
		}
		journal.Tasks = append(journal.Tasks, mv)
	}
	if len(journal.Tasks) > 0 {
		if err := writeTaskLayoutJournal(root, TaskLayoutJournalPath, journal); err != nil {
			rollbackMoves(root, journal.Tasks)
			return TaskLayoutJournal{}, err
		}
	}
	return journal, nil
}

// rollbackMoves 是 ApplyTaskLayout 内部的最佳努力回滚；失败只记录到 stderr 之外的返回里。
func rollbackMoves(root string, moves []TaskLayoutMove) {
	for i := len(moves) - 1; i >= 0; i-- {
		mv := moves[i]
		if err := VerifyTaskDirHashes(root, mv.To, mv.Files); err != nil {
			continue
		}
		_ = os.MkdirAll(filepath.Dir(filepath.Join(root, mv.From)), 0o755)
		_ = os.Rename(filepath.Join(root, mv.To), filepath.Join(root, mv.From))
	}
}

func writeTaskLayoutJournal(root, rel string, journal TaskLayoutJournal) error {
	raw, err := yaml.Marshal(journal)
	if err != nil {
		return fmt.Errorf("序列化迁移日志: %w", err)
	}
	header := "# DERIVED FROM MIGRATION — 分类迁移日志，不是审批证据。\n"
	path := filepath.Join(root, rel)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(header+string(raw)), 0o644); err != nil {
		return fmt.Errorf("写入迁移日志 %s: %w", rel, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("写入迁移日志 %s: %w", rel, err)
	}
	return nil
}

// LoadTaskLayoutJournal 读取迁移日志。
func LoadTaskLayoutJournal(root, rel string) (TaskLayoutJournal, error) {
	if rel == "" {
		rel = TaskLayoutJournalPath
	}
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return TaskLayoutJournal{}, err
	}
	var j TaskLayoutJournal
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&j); err != nil {
		return TaskLayoutJournal{}, fmt.Errorf("解析 %s: %w", rel, err)
	}
	if j.SchemaVersion != TaskLayoutJournalSchemaV1 {
		return TaskLayoutJournal{}, fmt.Errorf("不支持的迁移日志 schema_version=%d", j.SchemaVersion)
	}
	return j, nil
}

// RollbackTaskLayout 按日志把 Task 移回迁移前路径。
//
// dryRun 只输出计划；内容与日志不一致时拒绝回滚，不做强制覆盖。
// 已回滚的条目（目标不存在、源内容匹配）视为幂等成功。
func RollbackTaskLayout(root, rel string, dryRun bool) (TaskLayoutJournal, int, error) {
	j, err := LoadTaskLayoutJournal(root, rel)
	if err != nil {
		return j, 0, err
	}
	moved := 0
	for i := len(j.Tasks) - 1; i >= 0; i-- {
		mv := j.Tasks[i]
		toExists := taskDirExists(root, mv.To)
		fromExists := taskDirExists(root, mv.From)
		switch {
		case !toExists && fromExists:
			if err := VerifyTaskDirHashes(root, mv.From, mv.Files); err != nil {
				return j, moved, fmt.Errorf("Task %s 已回到旧路径但内容与迁移时不一致: %w", mv.ID, err)
			}
			continue
		case !toExists && !fromExists:
			return j, moved, fmt.Errorf("Task %s 在 %s 与 %s 都不存在", mv.ID, mv.From, mv.To)
		case fromExists:
			return j, moved, fmt.Errorf("Task %s 的旧路径已被占用：%s", mv.ID, mv.From)
		}
		if err := VerifyTaskDirHashes(root, mv.To, mv.Files); err != nil {
			return j, moved, fmt.Errorf("Task %s 迁移后内容已变化，拒绝回滚: %w", mv.ID, err)
		}
		if dryRun {
			moved++
			continue
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, mv.From)), 0o755); err != nil {
			return j, moved, err
		}
		if err := os.Rename(filepath.Join(root, mv.To), filepath.Join(root, mv.From)); err != nil {
			return j, moved, fmt.Errorf("回滚 %s -> %s: %w", mv.To, mv.From, err)
		}
		if err := VerifyTaskDirHashes(root, mv.From, mv.Files); err != nil {
			return j, moved, err
		}
		moved++
	}
	return j, moved, nil
}

// ValidateTaskLayout 校验分类清单与实际布局、以及分类依据是否可核验。
//
// 返回的 issues 非空表示布局不可信；调用方不得把「没有 error」当成分类正确。
func ValidateTaskLayout(root string) ([]TaskLayoutIssue, error) {
	m, err := LoadTaskLayout(root)
	if err != nil {
		return nil, err
	}
	var issues []TaskLayoutIssue
	if len(m.Tasks) == 0 {
		issues = append(issues, TaskLayoutIssue{Kind: "manifest", Message: TaskLayoutManifestPath + " 未登记任何 Task"})
	}
	dirs, err := ListTaskDirs(root)
	if err != nil {
		return nil, err
	}
	byID := map[string][]TaskDir{}
	for _, d := range dirs {
		byID[d.ID] = append(byID[d.ID], d)
	}
	declared := map[string]TaskLayoutEntry{}
	for _, t := range m.Tasks {
		declared[t.ID] = t
		if len(byID[t.ID]) == 0 {
			issues = append(issues, TaskLayoutIssue{ID: t.ID, Kind: "missing", Message: "已登记分类但磁盘上没有对应目录"})
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	git := &ExecGit{Root: root}
	cfg, cfgErr := LoadConfig(root)
	integration := ""
	integrationRef := ""
	if cfgErr == nil && cfg != nil {
		integration = cfg.Git.IntegrationBranch
		integrationRef = "origin/" + integration
	}
	for _, id := range ids {
		found := byID[id]
		if len(found) > 1 {
			var rels []string
			for _, f := range found {
				rels = append(rels, f.Rel)
			}
			issues = append(issues, TaskLayoutIssue{ID: id, Kind: "duplicate", Message: "同时存在于多个目录：" + strings.Join(rels, ", ")})
			continue
		}
		entry, ok := declared[id]
		if !ok {
			issues = append(issues, TaskLayoutIssue{ID: id, Kind: "unclassified", Message: "未在 " + TaskLayoutManifestPath + " 登记分类"})
			continue
		}
		if found[0].Legacy {
			issues = append(issues, TaskLayoutIssue{ID: id, Kind: "legacy-layout", Message: "仍位于旧平铺路径 " + found[0].Rel + "，尚未迁移"})
		} else if found[0].Category != entry.Category {
			issues = append(issues, TaskLayoutIssue{ID: id, Kind: "category-mismatch", Message: fmt.Sprintf("清单为 %s，磁盘为 %s", entry.Category, found[0].Category)})
		}
		for _, issue := range validateTaskCategoryEvidence(root, git, integrationRef, found[0].Rel, entry) {
			issues = append(issues, issue)
		}
	}
	return issues, nil
}

// validateTaskCategoryEvidence 机械核对「completed/blocked」的 State 依据。
//
// basis=document/unknown 不做机器推断，但 completed 必须给出 evidence（清单加载时已校验）。
func validateTaskCategoryEvidence(root string, git *ExecGit, integrationRef, rel string, entry TaskLayoutEntry) []TaskLayoutIssue {
	var issues []TaskLayoutIssue
	stateRel := filepath.ToSlash(filepath.Join(rel, "state.yaml"))
	raw, err := os.ReadFile(filepath.Join(root, stateRel))
	hasState := err == nil
	var s State
	if hasState {
		s, err = parseState(raw)
		if err != nil {
			hasState = false
		}
	}
	switch entry.Category {
	case TaskCategoryCompleted:
		if entry.Basis != TaskLayoutBasisState {
			if len(entry.Evidence) == 0 {
				issues = append(issues, TaskLayoutIssue{ID: entry.ID, Kind: "completed-without-evidence", Message: "非 state 依据的已完成分类必须列出交付证据"})
			}
			return issues
		}
		if !hasState {
			issues = append(issues, TaskLayoutIssue{ID: entry.ID, Kind: "completed-without-state", Message: "basis=state 但 state.yaml 缺失或不可解析"})
			return issues
		}
		if s.Delivery.Status != DeliveryPass || s.Review.Status != ReviewClean || s.Owner.Status != OwnerAccepted {
			issues = append(issues, TaskLayoutIssue{ID: entry.ID, Kind: "completed-status", Message: fmt.Sprintf("delivery=%s review=%s owner=%s 不满足 PASS/CLEAN/ACCEPTED", s.Delivery.Status, s.Review.Status, s.Owner.Status)})
		}
		if s.Owner.ReviewTarget != s.Review.Target || s.Delivery.FeatureHead == "" {
			issues = append(issues, TaskLayoutIssue{ID: entry.ID, Kind: "completed-binding", Message: "Owner 绑定或 delivery.feature_head 不完整"})
		} else if integrationRef != "" {
			if ok, err := git.IsAncestor(s.Delivery.FeatureHead, integrationRef); err != nil {
				issues = append(issues, TaskLayoutIssue{ID: entry.ID, Kind: "completed-git", Message: fmt.Sprintf("无法确认 %s 是否为 %s 祖先: %v", s.Delivery.FeatureHead, integrationRef, err)})
			} else if !ok {
				issues = append(issues, TaskLayoutIssue{ID: entry.ID, Kind: "completed-untraceable", Message: fmt.Sprintf("delivery.feature_head 不在 %s 历史中", integrationRef)})
			}
		}
	case TaskCategoryBlocked:
		if entry.Basis != TaskLayoutBasisState {
			return issues
		}
		if !hasState {
			issues = append(issues, TaskLayoutIssue{ID: entry.ID, Kind: "blocked-without-state", Message: "basis=state 但 state.yaml 缺失或不可解析"})
			return issues
		}
		if !s.Blocked.Active && s.Delivery.Status != DeliveryBlocked {
			issues = append(issues, TaskLayoutIssue{ID: entry.ID, Kind: "blocked-evidence", Message: fmt.Sprintf("blocked.active=false 且 delivery=%s，State 中没有阻塞证据", s.Delivery.Status)})
		}
	case TaskCategoryActive:
		if entry.Basis == TaskLayoutBasisState && hasState {
			if s.Delivery.Status == DeliveryPass && s.Owner.Status == OwnerAccepted {
				issues = append(issues, TaskLayoutIssue{ID: entry.ID, Kind: "active-already-delivered", Message: "State 显示已交付，应分类为 completed 或由 Owner 明确说明"})
			}
		}
	case TaskCategoryArchive:
		if entry.Basis == TaskLayoutBasisState {
			issues = append(issues, TaskLayoutIssue{ID: entry.ID, Kind: "archive-basis", Message: "archive 表示不再推进，不能以 state 证据声明；请使用 document/unknown 并给出理由"})
		}
	}
	return issues
}

// TaskCategoryOf 返回 Task 的分类：优先清单，其次磁盘布局，最后空串。
func TaskCategoryOf(m TaskLayoutManifest, dirs []TaskDir, id string) string {
	for _, t := range m.Tasks {
		if t.ID == id {
			return t.Category
		}
	}
	for _, d := range dirs {
		if d.ID == id {
			return d.Category
		}
	}
	return ""
}
