package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// 本文件实现 Workflow V4 的最小可靠事件日志写入端。
//
// 事件日志是纯诊断记录，不承载任何审批权威：Gate 不得根据 ACK / COMPLETED 等事件推断审批有效，
// Owner / Contract / Cleaner 的审批证据仍以 State + Git Evidence 为准（见 .agent/specs/protocols/EventLogProtocol.md）。
//
// 写入端职责：
//   - 严格校验事件字段（scope_kind / scope_id / 事件类型 / 时间 / SHA / BLOCKED reason）；
//   - 仅追加（O_APPEND），绝不覆盖既有记录；
//   - 单进程内用互斥锁串行化，跨进程用 flock(LOCK_EX) 串行化，避免多事件交错；
//   - 只写入 .agent/tasks/<id>/events.md 或 .agent/programs/<id>/events.md，不接受任意路径覆盖源码。

// 固定事件类型（与 Task 04 读取端共享协议，字段名与格式不得自行增改）。
const (
	EventStart           = "START"
	EventHandoffPrepared = "HANDOFF_PREPARED"
	EventAck             = "ACK"
	EventBlocked         = "BLOCKED"
	EventCompleted       = "COMPLETED"
)

// 固定 scope 类型。
const (
	ScopeTask    = "task"
	ScopeProgram = "program"
)

// eventsHeader 是事件文件允许的文件头（后跟一个空行）。
const eventsHeader = "# Workflow Events\n\n"

// eventTypeSet 是合法事件类型集合。
var eventTypeSet = map[string]struct{}{
	EventStart:           {},
	EventHandoffPrepared: {},
	EventAck:             {},
	EventBlocked:         {},
	EventCompleted:       {},
}

// scopeKindSet 是合法 scope 类型集合。
var scopeKindSet = map[string]struct{}{
	ScopeTask:    {},
	ScopeProgram: {},
}

// scopeIDPattern 约束 scope_id 为安全单一路径段（禁止路径穿越）。
// 必须以字母数字开头，后续仅允许 [A-Za-z0-9._-]；"." / ".." / 含 "/"、"\""、NUL 均被拒绝。
var scopeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// shaPattern 约束 SHA 为完整十六进制 Commit SHA（SHA-1 40 位 或 SHA-256 64 位）。
var shaPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$|^[0-9a-fA-F]{64}$`)

// Event 是一条 Workflow 事件记录。
// 字段声明顺序即磁盘 JSON 字段顺序，与 Task 04 解析协议一致，不得增删字段。
type Event struct {
	Timestamp string `json:"timestamp"`
	Role      string `json:"role"`
	Event     string `json:"event"`
	ScopeKind string `json:"scope_kind"`
	ScopeID   string `json:"scope_id"`
	SHA       string `json:"sha"`
	Reason    string `json:"reason"`
}

// ValidEventType 报告 s 是否为合法事件类型。
func ValidEventType(s string) bool {
	_, ok := eventTypeSet[s]
	return ok
}

// ValidScopeKind 报告 k 是否为合法 scope 类型。
func ValidScopeKind(k string) bool {
	_, ok := scopeKindSet[k]
	return ok
}

// ValidScopeID 报告 id 是否为安全单一路径段标识。
func ValidScopeID(id string) bool {
	return scopeIDPattern.MatchString(id)
}

// validTimestamp 报告 s 是否为 UTC RFC3339Nano 时间（偏移必须为 0）。
func validTimestamp(s string) bool {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return false
	}
	_, offset := t.Zone()
	return offset == 0
}

// validSHA 报告 s 是否为合法 SHA（空串允许，表示无关联 Commit）。
func validSHA(s string) bool {
	if s == "" {
		return true
	}
	return shaPattern.MatchString(s)
}

// Validate 校验事件字段，返回首个错误。
func (e Event) Validate() error {
	if !ValidScopeKind(e.ScopeKind) {
		return fmt.Errorf("非法的 scope_kind=%q（期望 task | program）", e.ScopeKind)
	}
	if !ValidScopeID(e.ScopeID) {
		return fmt.Errorf("非法的 scope_id=%q（必须为不穿越路径的单一标识）", e.ScopeID)
	}
	if !ValidEventType(e.Event) {
		return fmt.Errorf("非法的事件类型=%q（期望 START | HANDOFF_PREPARED | ACK | BLOCKED | COMPLETED）", e.Event)
	}
	if e.Role == "" {
		return fmt.Errorf("role 不得为空")
	}
	if !validTimestamp(e.Timestamp) {
		return fmt.Errorf("非法的时间戳=%q（期望 UTC RFC3339Nano）", e.Timestamp)
	}
	if !validSHA(e.SHA) {
		return fmt.Errorf("非法的 sha=%q（期望 40 或 64 位十六进制完整 Commit SHA，或空串）", e.SHA)
	}
	if e.Event == EventBlocked && e.Reason == "" {
		return fmt.Errorf("BLOCKED 事件必须提供非空 reason")
	}
	return nil
}

// eventLine 序列化事件为一行 `- {json}\n`。JSON 编码负责转义 reason/role 中的特殊字符。
func (e Event) eventLine() (string, error) {
	b, err := json.Marshal(e)
	if err != nil {
		return "", fmt.Errorf("序列化事件: %w", err)
	}
	return "- " + string(b) + "\n", nil
}

// eventsRelPath 计算事件文件相对仓库根的路径，并校验 scope 参数。
// scope_kind 必须是 task 或 program；scope_id 必须是安全单一路径段。
func eventsRelPath(scopeKind, scopeID string) (string, error) {
	if !ValidScopeKind(scopeKind) {
		return "", fmt.Errorf("非法的 scope_kind=%q", scopeKind)
	}
	if !ValidScopeID(scopeID) {
		return "", fmt.Errorf("非法的 scope_id=%q", scopeID)
	}
	switch scopeKind {
	case ScopeTask:
		return filepath.Join(".agent", "tasks", scopeID, "events.md"), nil
	case ScopeProgram:
		return filepath.Join(".agent", "programs", scopeID, "events.md"), nil
	default:
		// ValidScopeKind 已拦截，此分支仅作防御。
		return "", fmt.Errorf("非法的 scope_kind=%q", scopeKind)
	}
}

// eventLogMu 串行化单进程内所有事件追加，避免多 goroutine 交错写同一文件。
var eventLogMu sync.Mutex

// EventWriter 是事件日志写入器，仅写入指定 Workflow 事件路径。
type EventWriter struct {
	Root string // 仓库根目录
}

// NewEventWriter 返回以 root 为仓库根的事件写入器。
func NewEventWriter(root string) *EventWriter {
	return &EventWriter{Root: root}
}

// Path 返回事件文件相对仓库根的路径（不做磁盘写入）。
func (w *EventWriter) Path(scopeKind, scopeID string) (string, error) {
	return eventsRelPath(scopeKind, scopeID)
}

// Append 校验并追加一条事件。
// 返回 error 表示校验失败或 I/O 失败；写入失败绝不返回 nil（不伪造成功记录）。
func (w *EventWriter) Append(e Event) error {
	if err := e.Validate(); err != nil {
		return err
	}
	rel, err := eventsRelPath(e.ScopeKind, e.ScopeID)
	if err != nil {
		return err
	}
	line, err := e.eventLine()
	if err != nil {
		return err
	}

	// 单进程互斥锁：先串行化本进程内的追加。
	eventLogMu.Lock()
	defer eventLogMu.Unlock()

	// 以仓库根为可信边界，逐级 openat（O_NOFOLLOW|O_DIRECTORY）打开/创建目录，
	// 最终文件以 O_NOFOLLOW 打开，杜绝任意一级符号链接重定向。
	f, err := w.openEventFile(rel)
	if err != nil {
		return err
	}
	defer f.Close()

	// 跨进程串行化：对同一文件加排它 flock，避免多进程事件交错。
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("锁定事件文件: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat 事件文件: %w", err)
	}
	// 仅当文件为空时写文件头，避免覆盖既有内容；否则直接追加。
	if info.Size() == 0 {
		if _, err := f.WriteString(eventsHeader); err != nil {
			return fmt.Errorf("写事件头: %w", err)
		}
	}
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("写事件: %w", err)
	}
	// 将写入落盘，捕获底层 I/O 错误（如 ENOSPC），不伪造成功。
	if err := f.Sync(); err != nil {
		return fmt.Errorf("同步事件文件: %w", err)
	}
	return nil
}

// openEventFile 以仓库根为可信边界，通过 openat + O_NOFOLLOW + O_DIRECTORY 逐级
// 打开/创建事件路径的每个组件，最终文件以 O_NOFOLLOW 打开。
//
// 安全性：每个目录组件与最终文件都由内核在 open 时直接拒绝符号链接（O_NOFOLLOW），
// 不存在「先 Lstat 检查、后打开」的 TOCTOU 窗口；创建目录使用 Mkdirat（相对已打开的
// 目录 fd），同样不会经过符号链接中间目录。因此 .agent、tasks/programs、<scope-id>
// 目录或最终 events.md 任意一级为符号链接（无论指向仓库内还是仓库外）都会 fail-closed，
// 绝不追踪链接继续写入。
//
// rel 由 eventsRelPath 生成，只包含 .agent/{tasks|programs}/<scope-id>/events.md
// 固定组件，scope-id 已经过 ValidScopeID 校验（无路径分隔符），此处逐级打开仍以
// O_NOFOLLOW 作为纵深防御。
func (w *EventWriter) openEventFile(rel string) (*os.File, error) {
	root := w.Root
	if root == "" {
		root = "."
	}
	rootFd, err := syscall.Open(root, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("打开仓库根目录: %w", err)
	}
	defer syscall.Close(rootFd)

	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("非法事件路径 %q", rel)
	}

	curFd := rootFd
	closeCur := func() {
		if curFd != rootFd {
			syscall.Close(curFd)
		}
	}

	// 逐级打开/创建目录组件（除最终文件名外的所有段）。
	for _, part := range parts[:len(parts)-1] {
		if part == "" {
			continue
		}
		nextFd, err := syscall.Openat(curFd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err == syscall.ENOENT {
			if mkErr := syscall.Mkdirat(curFd, part, 0o755); mkErr != nil && mkErr != syscall.EEXIST {
				closeCur()
				return nil, fmt.Errorf("创建事件目录 %q: %w", part, mkErr)
			}
			nextFd, err = syscall.Openat(curFd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		}
		if err != nil {
			closeCur()
			return nil, fmt.Errorf("打开事件目录 %q: %w", part, err)
		}
		closeCur()
		curFd = nextFd
	}

	fileName := parts[len(parts)-1]
	fileFd, err := syscall.Openat(curFd, fileName, syscall.O_CREAT|syscall.O_WRONLY|syscall.O_APPEND|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o644)
	closeCur()
	if err != nil {
		return nil, fmt.Errorf("打开事件文件 %q: %w", fileName, err)
	}
	return os.NewFile(uintptr(fileFd), rel), nil
}
