package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// 本文件实现项目本地的 Task 目录分类扩展（上游 V4.3.0 未提供）。
//
// 上游把每个 Task 平铺在 .agent/tasks/<task-id>/。本扩展在不改变 Task ID、
// State Schema、Gate、Handoff、Event Log 语义的前提下，允许把 Task 物理放入
// .agent/tasks/<category>/<task-id>/，并由本文件统一解析：
//
//   - Task ID 仍是唯一身份，物理路径不是身份；
//   - 所有路径解析（Gate、CLI、索引、事件日志、交付、Program）都经过本文件；
//   - 旧平铺布局 .agent/tasks/<task-id>/ 继续可解析，不保留第二套权威路径；
//   - 分类清单 .agent/tasks/layout.yaml 是「分类与迁移记录」，不是 State，
//     不表达 CLEAN / ACCEPTED / PASS，也不得替代 State 与 Evidence。
//
// 与上游差异及回滚方式见 .agent/specs/TaskLayoutExtension.md。

const (
	// TaskLayoutSchemaV1 是分类清单（layout.yaml）当前唯一支持的 schema_version。
	TaskLayoutSchemaV1 = 1
	// TaskLayoutJournalSchemaV1 是迁移日志（layout-journal.yaml）的 schema_version。
	TaskLayoutJournalSchemaV1 = 1
	// TaskLayoutRoot 是 Task 目录根（相对仓库根）。
	TaskLayoutRoot = ".agent/tasks"
	// TaskLayoutManifestPath 是分类清单路径（相对仓库根）。
	TaskLayoutManifestPath = ".agent/tasks/layout.yaml"
	// TaskLayoutJournalPath 是默认迁移日志路径（相对仓库根）。
	TaskLayoutJournalPath = ".agent/tasks/layout-journal.yaml"

	// TaskCategoryActive：有证据表明仍在进行的任务。
	TaskCategoryActive = "active"
	// TaskCategoryCompleted：有正式完成证据（State 或交付文档）的任务。
	TaskCategoryCompleted = "completed"
	// TaskCategoryBlocked：真实阻塞、等待决策或无法继续的任务。
	TaskCategoryBlocked = "blocked"
	// TaskCategoryArchive：不再作为活动任务推进的历史任务。
	TaskCategoryArchive = "archive"
)

// TaskLayoutCategories 是分类目录的固定顺序；解析多个候选时按此顺序。
var TaskLayoutCategories = []string{
	TaskCategoryActive,
	TaskCategoryCompleted,
	TaskCategoryBlocked,
	TaskCategoryArchive,
}

// TaskLayoutBasis 描述分类依据的可核验程度。
const (
	// TaskLayoutBasisState：分类可由当前 State（blocked/delivery 等）机械核验。
	TaskLayoutBasisState = "state"
	// TaskLayoutBasisDocument：分类来自人工维护的交付文档证据，机器无法核验。
	TaskLayoutBasisDocument = "document"
	// TaskLayoutBasisUnknown：无法核实状态，只能作为历史待核验项归档。
	TaskLayoutBasisUnknown = "unknown"
)

// TaskLayoutManifest 是 .agent/tasks/layout.yaml 的磁盘表示。
//
// 每一步迁移都必须能由本清单重现；清单缺项视为未分类，迁移预检 fail closed，
// 不做「按目录名猜测分类」。
type TaskLayoutManifest struct {
	SchemaVersion int               `yaml:"schema_version"`
	Extension     string            `yaml:"extension"`
	OwnerDecision string            `yaml:"owner_decision,omitempty"`
	Categories    []string          `yaml:"categories"`
	Tasks         []TaskLayoutEntry `yaml:"tasks"`
}

// TaskLayoutEntry 是单个 Task 的分类记录。
type TaskLayoutEntry struct {
	ID       string   `yaml:"id"`
	Category string   `yaml:"category"`
	Basis    string   `yaml:"basis"`
	Reason   string   `yaml:"reason,omitempty"`
	Evidence []string `yaml:"evidence,omitempty"`
}

// TaskLayoutJournal 是一次目录迁移的可回滚记录。
type TaskLayoutJournal struct {
	SchemaVersion int              `yaml:"schema_version"`
	MigratedAt    string           `yaml:"migrated_at"`
	SourceSHA     string           `yaml:"source_sha,omitempty"`
	Tasks         []TaskLayoutMove `yaml:"tasks"`
}

// TaskLayoutMove 记录单个 Task 的迁移前后路径与文件校验值。
type TaskLayoutMove struct {
	ID    string            `yaml:"id"`
	From  string            `yaml:"from"`
	To    string            `yaml:"to"`
	Files map[string]string `yaml:"files"`
}

// TaskLayoutPlan 是迁移预检结果。
type TaskLayoutPlan struct {
	Moves      []TaskLayoutMove
	InPlace    []string
	Categories map[string]string
}

// TaskDir 是发现结果：物理目录 + 分类 + 是否仍为旧平铺布局。
type TaskDir struct {
	ID       string
	Rel      string
	Category string
	Legacy   bool
}

// TaskDirCandidatesRel 返回某个 Task ID 的全部候选相对目录（含旧平铺布局）。
//
// 用于 review/delivery neutral 判定：Task 目录布局变化本身不应被误判为实质变化，
// 但候选集合严格限定在同一 Task ID 下。
func TaskDirCandidatesRel(root, id string) ([]string, error) {
	if !ValidScopeID(id) {
		return nil, fmt.Errorf("非法 task id %q（必须为单一路径标识）", id)
	}
	m, err := LoadTaskLayout(root)
	if err != nil {
		return nil, err
	}
	return TaskDirCandidates(m, id), nil
}

// IsTaskCategory 报告 name 是否为受支持的分类目录名。
func IsTaskCategory(name string) bool {
	for _, c := range TaskLayoutCategories {
		if c == name {
			return true
		}
	}
	return false
}

// LoadTaskLayout 读取分类清单。
//
// 清单不存在返回空清单（仅旧平铺布局的仓库可以没有清单）；清单存在但非法
// 返回 error（fail closed，不静默忽略半可读的分类事实）。
func LoadTaskLayout(root string) (TaskLayoutManifest, error) {
	path := filepath.Join(root, TaskLayoutManifestPath)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return TaskLayoutManifest{SchemaVersion: TaskLayoutSchemaV1}, nil
		}
		return TaskLayoutManifest{}, fmt.Errorf("读取 %s: %w", TaskLayoutManifestPath, err)
	}
	var m TaskLayoutManifest
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return TaskLayoutManifest{}, fmt.Errorf("解析 %s: %w", TaskLayoutManifestPath, err)
	}
	if m.SchemaVersion != TaskLayoutSchemaV1 {
		return TaskLayoutManifest{}, fmt.Errorf("不支持的 %s schema_version=%d（期望 %d）", TaskLayoutManifestPath, m.SchemaVersion, TaskLayoutSchemaV1)
	}
	if len(m.Categories) == 0 {
		m.Categories = append([]string{}, TaskLayoutCategories...)
	}
	for _, c := range m.Categories {
		if !IsTaskCategory(c) {
			return TaskLayoutManifest{}, fmt.Errorf("%s 声明了未知分类 %q", TaskLayoutManifestPath, c)
		}
	}
	seen := map[string]bool{}
	for _, t := range m.Tasks {
		if !ValidScopeID(t.ID) {
			return TaskLayoutManifest{}, fmt.Errorf("%s 含非法 task id %q", TaskLayoutManifestPath, t.ID)
		}
		if seen[t.ID] {
			return TaskLayoutManifest{}, fmt.Errorf("%s 重复登记 task %q", TaskLayoutManifestPath, t.ID)
		}
		seen[t.ID] = true
		if !IsTaskCategory(t.Category) {
			return TaskLayoutManifest{}, fmt.Errorf("%s 中 task %q 的分类 %q 非法", TaskLayoutManifestPath, t.ID, t.Category)
		}
		switch t.Basis {
		case TaskLayoutBasisState, TaskLayoutBasisDocument, TaskLayoutBasisUnknown:
		default:
			return TaskLayoutManifest{}, fmt.Errorf("%s 中 task %q 的分类依据 %q 非法（期望 state|document|unknown）", TaskLayoutManifestPath, t.ID, t.Basis)
		}
		if t.Basis != TaskLayoutBasisState && len(t.Evidence) == 0 {
			return TaskLayoutManifest{}, fmt.Errorf("%s 中 task %q 使用非 state 依据时必须给出 evidence", TaskLayoutManifestPath, t.ID)
		}
	}
	return m, nil
}

// TaskDirCandidates 返回某个 Task ID 可能所在的全部相对目录，按解析优先级排列。
//
// 顺序为：清单登记的物理目录、旧平铺目录、其余分类目录。调用方不得用「先到先得」
// 掩盖多目录重复；TaskLayoutAmbiguity 会显式拒绝重复。
func TaskDirCandidates(m TaskLayoutManifest, id string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, t := range m.Tasks {
		if t.ID == id {
			add(filepath.ToSlash(filepath.Join(TaskLayoutRoot, t.Category, id)))
		}
	}
	add(filepath.ToSlash(filepath.Join(TaskLayoutRoot, id)))
	for _, c := range TaskLayoutCategories {
		add(filepath.ToSlash(filepath.Join(TaskLayoutRoot, c, id)))
	}
	return out
}

// TaskDirRel 把 Task ID 解析为实际存在的相对目录。
//
// 未找到返回 error；调用方不应把「新任务」与「历史任务」混为一谈。
func TaskDirRel(root, id string) (string, error) {
	if !ValidScopeID(id) {
		return "", fmt.Errorf("非法 task id %q（必须为单一路径标识）", id)
	}
	m, err := LoadTaskLayout(root)
	if err != nil {
		return "", err
	}
	found := []string{}
	for _, rel := range TaskDirCandidates(m, id) {
		if taskDirExists(root, rel) {
			found = append(found, rel)
		}
	}
	if len(found) == 0 {
		// 包装 os.ErrNotExist，便于调用方（Program 快照回退）区分「目录不存在」与其它错误。
		return "", fmt.Errorf("未找到 Task %q 的目录（已检查 .agent/tasks/%s、.agent/tasks/<category>/%s）: %w", id, id, id, os.ErrNotExist)
	}
	if len(found) > 1 {
		return "", fmt.Errorf("Task %q 同时存在于多个目录，分类不确定：%s", id, strings.Join(found, ", "))
	}
	return found[0], nil
}

// NewTaskDirRel 返回新建 Task 的规范目录（active）。
//
// 新建任务总是进入 active；任务完成后由 layout migrate 依据清单移动到其他分类。
func NewTaskDirRel(id string) string {
	return filepath.ToSlash(filepath.Join(TaskLayoutRoot, TaskCategoryActive, id))
}

// TaskStateRel 返回 Task state.yaml 的实际相对路径。
func TaskStateRel(root, id string) (string, error) {
	dir, err := TaskDirRel(root, id)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join(dir, "state.yaml")), nil
}

// TaskStateCandidates 返回 state.yaml 的候选相对路径（含旧平铺布局）。
//
// 读取 Git 历史快照时，工作树布局可能与目标提交不同，调用方须按候选顺序尝试。
func TaskStateCandidates(m TaskLayoutManifest, id string) []string {
	var out []string
	for _, dir := range TaskDirCandidates(m, id) {
		out = append(out, filepath.ToSlash(filepath.Join(dir, "state.yaml")))
	}
	return out
}

// TaskArtifactCandidates 返回 Task 下固定文件的候选相对路径（含旧平铺布局）。
//
// 读取 Git 历史快照时必须按候选顺序尝试：目标提交的目录布局可能与当前工作树不同。
func TaskArtifactCandidates(m TaskLayoutManifest, id, name string) []string {
	var out []string
	for _, dir := range TaskDirCandidates(m, id) {
		out = append(out, filepath.ToSlash(filepath.Join(dir, name)))
	}
	return out
}

// ListTaskDirs 发现全部 Task 目录，覆盖新旧两种布局。
//
// 只返回真实目录（不跟随符号链接）；同时出现在旧布局与分类目录的 ID 会被
// 重复返回，由调用方（索引/预检）显式报告，而不是静默去重。
func ListTaskDirs(root string) ([]TaskDir, error) {
	base := filepath.Join(root, TaskLayoutRoot)
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取 %s: %w", TaskLayoutRoot, err)
	}
	var out []TaskDir
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			out = append(out, TaskDir{ID: e.Name(), Rel: filepath.ToSlash(filepath.Join(TaskLayoutRoot, e.Name())), Legacy: true})
			continue
		}
		if !e.IsDir() {
			continue
		}
		if IsTaskCategory(e.Name()) {
			children, err := os.ReadDir(filepath.Join(base, e.Name()))
			if err != nil {
				return nil, fmt.Errorf("读取 %s/%s: %w", TaskLayoutRoot, e.Name(), err)
			}
			for _, c := range children {
				if c.IsDir() || c.Type()&os.ModeSymlink != 0 {
					out = append(out, TaskDir{
						ID:       c.Name(),
						Rel:      filepath.ToSlash(filepath.Join(TaskLayoutRoot, e.Name(), c.Name())),
						Category: e.Name(),
					})
				}
			}
			continue
		}
		out = append(out, TaskDir{ID: e.Name(), Rel: filepath.ToSlash(filepath.Join(TaskLayoutRoot, e.Name())), Legacy: true})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Rel < out[j].Rel
	})
	return out, nil
}

// ResolveTaskArg 把 CLI 位置参数解析为相对仓库根的 Task 目录。
//
// 支持：存在的路径（含旧布局与分类目录）、省略分类的旧路径 `.agent/tasks/<id>`、
// 以及裸 Task ID 简写。相对路径按仓库根解析，不允许越出仓库。
func ResolveTaskArg(root, arg string) (string, error) {
	if arg == "" {
		return "", fmt.Errorf("缺少 Task 参数")
	}
	// 1. 已存在的路径（目录或目录内文件）。
	if rel, err := normalizeUnderRoot(root, arg); err == nil {
		if info, statErr := os.Stat(filepath.Join(root, rel)); statErr == nil {
			if !info.IsDir() {
				rel = filepath.ToSlash(filepath.Dir(rel))
			}
			if isTaskDirRel(rel) {
				return rel, nil
			}
			return "", fmt.Errorf("%s 不在 %s/ 下，不是 Task 目录", arg, TaskLayoutRoot)
		}
	}
	// 2. 旧路径简写：.agent/tasks/<id>；分类路径：.agent/tasks/<category>/<id>。
	cleaned := filepath.ToSlash(filepath.Clean(arg))
	if strings.HasPrefix(cleaned, TaskLayoutRoot+"/") {
		parts := strings.Split(strings.TrimPrefix(cleaned, TaskLayoutRoot+"/"), "/")
		switch len(parts) {
		case 1:
			return TaskDirRel(root, parts[0])
		case 2:
			if IsTaskCategory(parts[0]) {
				return TaskDirRel(root, parts[1])
			}
		}
	}
	// 3. 裸 Task ID。
	if ValidScopeID(cleaned) {
		return TaskDirRel(root, cleaned)
	}
	return "", fmt.Errorf("无法解析 Task 参数 %q（支持 task-id、.agent/tasks/<id> 或实际目录）", arg)
}

func normalizeUnderRoot(root, p string) (string, error) {
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, p)
	}
	abs, err := filepath.Abs(abs)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("路径 %q 不在仓库根 %q 内", p, root)
	}
	return filepath.ToSlash(rel), nil
}

func isTaskDirRel(rel string) bool {
	parts := strings.Split(rel, "/")
	if len(parts) < 3 || parts[0] != ".agent" || parts[1] != "tasks" {
		return false
	}
	if len(parts) == 3 {
		return true
	}
	if len(parts) == 4 && IsTaskCategory(parts[2]) {
		return true
	}
	return false
}

func taskDirExists(root, rel string) bool {
	info, err := os.Lstat(filepath.Join(root, rel))
	return err == nil && info.IsDir()
}

// showFirstFileAt 按候选顺序读取 Git 快照中的第一个存在文件。
//
// 用于 Program 交付校验：协调者工作树的布局不代表目标提交的布局，
// 因此不能只尝试单一固定路径。
func showFirstFileAt(g *ExecGit, ref string, candidates []string) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("ref %s 没有可用的候选路径", ref)
	}
	var lastErr error
	for _, rel := range candidates {
		raw, err := g.ShowFile(ref, rel)
		if err == nil {
			return raw, nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("ref %s 未找到任务文件（尝试 %s）: %w", ref, strings.Join(candidates, ", "), lastErr)
}
