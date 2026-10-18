package workflow

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"gopkg.in/yaml.v3"
)

// 本文件实现 Program Registry（Task 12）：Collector 交付登记的唯一权威事实源，
// 以及受控单写者 + Git 版本条件更新的并发写入机制。
//
// Program Registry 与 Program State 分离：
//   - Program State（program-state.yaml）：Owner 规划与 Program 层决策事实；
//   - Program Registry（registry.yaml）：Collector 登记的交付记录（已登记交付版本）。
//
// 并发写入保证（AC：并发写入冲突不能静默丢失）：
//   - flock(LOCK_EX) 全程串行化「读-改-写」，不同 Collector 的登记不会互相覆盖；
//   - revision 单调递增的版本条件更新：RegisterExpected 在写前核验磁盘 revision，
//     若已被其它 Collector 前进则返回 *RegistryConflictError，冲突可被显式检测；
//   - 重复 Task 登记被拒绝（不覆盖、不静默去重）。

// ProgramRegistrySchemaV1 是 Program Registry 的 schema_version。
const ProgramRegistrySchemaV1 = 1

// RegisteredDelivery 是 Collector 登记的一条子 Task 交付记录。
//
// 字段均为不可变证据引用：review_target / owner_target / delivery_sha 必须来自
// 子 Task 自身 state.yaml 的机械事实，Collector 只做校验与登记，不伪造证据。
type RegisteredDelivery struct {
	TaskID       string `yaml:"task_id"`
	Branch       string `yaml:"branch"`        // 该交付对应的独立分支
	DeliverySHA  string `yaml:"delivery_sha"`  // 已验证快照 SHA；实际来源 tip 必须证明 delivery-neutral tail，不能替换此值
	ReviewStatus string `yaml:"review_status"` // 必须 CLEAN
	ReviewTarget string `yaml:"review_target"` // 正式 Review Evidence Commit SHA
	OwnerStatus  string `yaml:"owner_status"`  // 必须 ACCEPTED
	OwnerTarget  string `yaml:"owner_target"`  // Owner ACCEPT Evidence Commit SHA
	RegisteredBy string `yaml:"registered_by"`
	RegisteredAt string `yaml:"registered_at"`
}

// ProgramRegistry 是 Program 交付登记的权威事实源。
// Revision 是版本条件更新的并发令牌，每登记一条 +1。
type ProgramRegistry struct {
	SchemaVersion int                  `yaml:"schema_version"`
	ProgramID     string               `yaml:"program_id"`
	Revision      int                  `yaml:"revision"`
	Deliveries    []RegisteredDelivery `yaml:"deliveries,omitempty"`
}

// ProgramRegistryPath 返回 registry.yaml 的固定路径（相对仓库根）。
func ProgramRegistryPath(programID string) string {
	return filepath.Join(ProgramDirPath(programID), "registry.yaml")
}

// ParseProgramRegistry 解析并校验 registry.yaml（strict decode + fail-closed）。
func ParseProgramRegistry(raw []byte) (ProgramRegistry, error) {
	var r ProgramRegistry
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return ProgramRegistry{}, fmt.Errorf("解析 registry.yaml: %w", err)
	}
	if err := validateProgramRegistry(r); err != nil {
		return ProgramRegistry{}, err
	}
	return r, nil
}

func validateProgramRegistry(r ProgramRegistry) error {
	if r.SchemaVersion != ProgramRegistrySchemaV1 {
		return fmt.Errorf("不支持的 registry schema_version=%d（期望 %d）", r.SchemaVersion, ProgramRegistrySchemaV1)
	}
	if !ValidScopeID(r.ProgramID) {
		return fmt.Errorf("非法的 registry program_id=%q", r.ProgramID)
	}
	if r.Revision < 0 {
		return fmt.Errorf("非法的 revision=%d（不得为负数）", r.Revision)
	}
	seen := map[string]struct{}{}
	for i, d := range r.Deliveries {
		if err := validateRegisteredDelivery(d); err != nil {
			return fmt.Errorf("deliveries[%d]: %w", i, err)
		}
		if _, dup := seen[d.TaskID]; dup {
			return fmt.Errorf("deliveries 中存在重复的 task_id=%q", d.TaskID)
		}
		seen[d.TaskID] = struct{}{}
	}
	return nil
}

// validateRegisteredDelivery 做交付记录的结构性校验（fail-closed）。
// 已登记交付的定义性前提：独立审查 CLEAN + Owner ACCEPT，否则不得进入 Registry。
func validateRegisteredDelivery(d RegisteredDelivery) error {
	if !ValidScopeID(d.TaskID) {
		return fmt.Errorf("非法的 task_id=%q", d.TaskID)
	}
	if d.Branch == "" {
		return fmt.Errorf("branch 不得为空")
	}
	if !validRequiredSHA(d.DeliverySHA) {
		return fmt.Errorf("非法的 delivery_sha=%q（必须为完整 Commit SHA）", d.DeliverySHA)
	}
	if d.ReviewStatus != ReviewClean {
		return fmt.Errorf("review_status=%q 非法（已登记交付必须为 CLEAN）", d.ReviewStatus)
	}
	if !validRequiredSHA(d.ReviewTarget) {
		return fmt.Errorf("非法的 review_target=%q", d.ReviewTarget)
	}
	if d.OwnerStatus != OwnerAccepted {
		return fmt.Errorf("owner_status=%q 非法（已登记交付必须为 ACCEPTED）", d.OwnerStatus)
	}
	if !validRequiredSHA(d.OwnerTarget) {
		return fmt.Errorf("非法的 owner_target=%q", d.OwnerTarget)
	}
	return nil
}

// RegistryConflictError 表示版本条件更新冲突（磁盘 revision 已被其它 Collector 前进）。
type RegistryConflictError struct {
	ProgramID   string
	ExpectedRev int
	ActualRev   int
}

func (e *RegistryConflictError) Error() string {
	return fmt.Sprintf("program %s registry 冲突：期望 revision=%d，实际 revision=%d（已有其它 Collector 写入）",
		e.ProgramID, e.ExpectedRev, e.ActualRev)
}

// RegistryStore 是 Program Registry 的受控读写器。
type RegistryStore struct {
	Root string // 仓库根目录
}

func (s *RegistryStore) registryPath(programID string) string {
	return filepath.Join(s.Root, ProgramRegistryPath(programID))
}

func (s *RegistryStore) lockPath(programID string) string {
	return filepath.Join(s.Root, ProgramRegistryPath(programID)+".lock")
}

// Load 读取 registry；文件不存在时返回空 registry（SchemaVersion 就绪、Revision=0）。
func (s *RegistryStore) Load(programID string) (ProgramRegistry, error) {
	raw, err := os.ReadFile(s.registryPath(programID))
	if err != nil {
		if os.IsNotExist(err) {
			return ProgramRegistry{
				SchemaVersion: ProgramRegistrySchemaV1,
				ProgramID:     programID,
			}, nil
		}
		return ProgramRegistry{}, err
	}
	return ParseProgramRegistry(raw)
}

// Register 原子登记一条交付记录。
// 全程持有排它锁（flock）串行化并发写；重复登记、结构非法返回 error，绝不静默覆盖。
func (s *RegistryStore) Register(programID string, d RegisteredDelivery) error {
	return s.withLock(programID, func() error {
		reg, err := s.loadLocked(programID)
		if err != nil {
			return err
		}
		next, err := appendDelivery(reg, programID, d)
		if err != nil {
			return err
		}
		return s.writeLocked(programID, next)
	})
}

// RegisterExpected 基于调用方已知的旧 revision 做版本条件更新。
// 若磁盘 revision 已前进（其它 Collector 已写入），返回 *RegistryConflictError，
// 显式检测并发冲突而不是静默覆盖。
func (s *RegistryStore) RegisterExpected(programID string, expectedRevision int, d RegisteredDelivery) error {
	return s.withLock(programID, func() error {
		reg, err := s.loadLocked(programID)
		if err != nil {
			return err
		}
		if reg.Revision != expectedRevision {
			return &RegistryConflictError{ProgramID: programID, ExpectedRev: expectedRevision, ActualRev: reg.Revision}
		}
		next, err := appendDelivery(reg, programID, d)
		if err != nil {
			return err
		}
		return s.writeLocked(programID, next)
	})
}

// withLock 打开/创建 lock 文件并加 flock(LOCK_EX)，在锁内执行 fn。
func (s *RegistryStore) withLock(programID string, fn func() error) error {
	lockPath := s.lockPath(programID)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return fmt.Errorf("创建 program 目录: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("打开锁文件: %w", err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("加锁: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func (s *RegistryStore) loadLocked(programID string) (ProgramRegistry, error) {
	return s.Load(programID)
}

func (s *RegistryStore) writeLocked(programID string, reg ProgramRegistry) error {
	b, err := yaml.Marshal(reg)
	if err != nil {
		return fmt.Errorf("序列化 registry: %w", err)
	}
	path := s.registryPath(programID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建 program 目录: %w", err)
	}
	// 先写临时文件再原子 rename，避免中途崩溃产生半截 registry。
	tmp, err := os.CreateTemp(filepath.Dir(path), ".registry-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// appendDelivery 校验并追加一条交付记录，revision +1。
func appendDelivery(reg ProgramRegistry, programID string, d RegisteredDelivery) (ProgramRegistry, error) {
	if err := validateRegisteredDelivery(d); err != nil {
		return reg, err
	}
	if reg.SchemaVersion != ProgramRegistrySchemaV1 {
		return reg, fmt.Errorf("registry schema_version=%d 非法", reg.SchemaVersion)
	}
	if reg.ProgramID != "" && reg.ProgramID != programID {
		return reg, fmt.Errorf("registry program_id=%q 与 %q 不一致", reg.ProgramID, programID)
	}
	for _, e := range reg.Deliveries {
		if e.TaskID == d.TaskID {
			return reg, fmt.Errorf("task %s 已登记，重复交付记录被拒绝", d.TaskID)
		}
	}
	if reg.ProgramID == "" {
		reg.ProgramID = programID
	}
	reg.Deliveries = append(reg.Deliveries, d)
	reg.Revision++
	return reg, nil
}
