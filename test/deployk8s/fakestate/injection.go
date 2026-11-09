package fakestate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// 本文件是 T2-06.1.4 的**唯一**故障注入模型（审计 C-1 / C-3 / C-13）。
//
// 设计要点：
//   - 注入匹配的是**调用语义**（verb + group + resource + namespace + name +
//     selector + 写意图），不再按 YAML 文件名猜「这是什么操作」；
//     `error/mutate` 与 `fakekubectl` 共用同一套匹配与执行路径；
//   - 旧的约 20 个扁平字段（LeaseReplaceConflicts / LeaseStealAfterGets /
//     ApplyFailures / PodsGetFailures …）保留，但只是 Injection 的**薄封装**：
//     它们在 legacyInjections() 里被翻译成等价的 Injection，消耗时递减原字段。
//     不存在第二套注入执行逻辑。
//
// 边界：延迟注入是进程内 sleep，只用于确定性地制造时间窗，不代表网络行为。

// 迁移生命周期注解的键（与 scripts/deploy-k8s.sh 的数据契约一致）。
const AnnotationMigrationState = "deploy-lock.surgecart.io/migration-state"

// 写意图：由写入的 Lease 清单与服务端现存对象**内容**推导，与文件名无关。
const (
	IntentAcquire = "acquire" // holder 从空/他人变为本 Runner
	IntentRenew   = "renew"   // holder 不变，记录未变
	IntentRelease = "release" // holder 置空
	IntentRecord  = "record"  // holder 不变，但迁移记录注解被改写
)

// Invocation 是一次调用解析后的语义。fake 与测试都用它表达「这是什么操作」。
type Invocation struct {
	Verb      string // get|list|create|update|patch|delete|apply
	Group     string // "" = core
	Resource  string // "leases"/"pods"/"jobs"/"pods/log"
	Namespace string
	Name      string
	Selector  string // -l 的原始值；空 = 无选择器
	Intent    string // 仅写操作：acquire|renew|release|record
	// Annotations 是写入对象携带的注解（仅写操作）。
	Annotations map[string]string
}

// Injection 是一条故障注入。
//
// 匹配条件为空/0 表示「不限」；动作可组合，执行顺序为
// Delay → Status（失败即终止）→ Delete / Takeover。
type Injection struct {
	Verb      string `json:"verb,omitempty"`
	Group     string `json:"group,omitempty"`
	Resource  string `json:"resource,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	Selector  string `json:"selector,omitempty"`
	Intent    string `json:"intent,omitempty"`
	// Ordinal > 0 时只在**第 N 次匹配**时生效（1-based）；0 = 每次匹配都生效。
	Ordinal int `json:"ordinal,omitempty"`
	// Times > 0 时最多生效 N 次；0 = 不限次数。
	Times int `json:"times,omitempty"`

	DelayMS  int    `json:"delayMS,omitempty"`
	Status   int    `json:"status,omitempty"`
	Takeover string `json:"takeover,omitempty"`
	Delete   bool   `json:"delete,omitempty"`
	// Applied=true 时「错误注入仍然让写入生效」：模拟客户端超时但服务端已接受请求。
	Applied bool   `json:"applied,omitempty"`
	Message string `json:"message,omitempty"`
	// Event 是事件日志标签（仅用于测试观测）。
	Event string `json:"event,omitempty"`
	// Phase：""/"before" 在读写之前；"after" 在成功返回之后。
	Phase string `json:"phase,omitempty"`
	// RequireAnnotations 非空时，只有写入对象带有这些注解（键=值）才匹配。
	RequireAnnotations map[string]string `json:"requireAnnotations,omitempty"`

	// Matches 是引擎维护的匹配计数（测试不要设置）。
	Matches int `json:"matches,omitempty"`

	// consume 是旧字段的消耗方式（递减计数器 / 清零），不参与序列化。
	consume func(*State)
}

func (inj *Injection) phase() string {
	if inj.Phase == "" {
		return "before"
	}
	return inj.Phase
}

// matches 判断注入是否作用于该调用（不含 Ordinal/Times 的消耗判定之外的副作用）。
func (inj *Injection) matches(inv Invocation, phase string) bool {
	if inj.phase() != phase {
		return false
	}
	if inj.Times > 0 && inj.Matches >= inj.Times {
		return false
	}
	if inj.Ordinal > 0 && inj.Matches+1 != inj.Ordinal {
		return false
	}
	if inj.Verb != "" && inj.Verb != inv.Verb {
		return false
	}
	if inj.Group != "" && inj.Group != inv.Group {
		return false
	}
	if inj.Resource != "" && inj.Resource != inv.Resource {
		return false
	}
	if inj.Namespace != "" && inj.Namespace != inv.Namespace {
		return false
	}
	if inj.Name != "" && inj.Name != inv.Name {
		return false
	}
	if inj.Selector != "" && !strings.Contains(inv.Selector, inj.Selector) {
		return false
	}
	if inj.Intent != "" && inj.Intent != inv.Intent {
		return false
	}
	for key, want := range inj.RequireAnnotations {
		if inv.Annotations[key] != want {
			return false
		}
	}
	return true
}

// ConsumeInjection 返回匹配 inv 且仍生效的注入，并消耗一次。
// 这是**唯一**的注入匹配入口；执行动作的代码也只有一个地方（fakekubectl）。
func (st *State) ConsumeInjection(inv Invocation, phase string) *Injection {
	for i := range st.Injections {
		injection := &st.Injections[i]
		if injection.matches(inv, phase) {
			injection.Matches++
			return injection
		}
	}
	legacy := st.legacyInjections()
	for i := range legacy {
		injection := &legacy[i]
		if injection.matches(inv, phase) {
			injection.Matches++
			if injection.consume != nil {
				injection.consume(st)
			}
			return injection
		}
	}
	return nil
}

// decrement 返回「把计数器减一」的消耗函数。
func decrement(counter *int) func(*State) {
	return func(*State) {
		if *counter > 0 {
			*counter--
		}
	}
}

// legacyInjections 把旧的扁平字段翻译成 Injection（薄封装）。
//
// 顺序与旧实现一致：续租专用 → 通用 replace → 迁移记录；同一调用只取第一条匹配。
func (st *State) legacyInjections() []Injection {
	var injections []Injection
	lease := func(injection Injection) Injection {
		if injection.Verb == "" {
			injection.Verb = "update"
		}
		if injection.Group == "" {
			injection.Group = "coordination.k8s.io"
		}
		if injection.Resource == "" {
			injection.Resource = "leases"
		}
		return injection
	}

	// ---- 续租专用（holder 不变的 update）：持续性注入，计数器不递减 ----
	if st.LeaseStealBeforeRenewReplace > 0 {
		injections = append(injections, lease(Injection{
			Intent: IntentRenew, Takeover: st.LeaseStealHolder, Phase: "before",
			Times:   st.LeaseStealBeforeRenewReplace,
			Event:   "lease.stolen",
			consume: decrement(&st.LeaseStealBeforeRenewReplace),
		}))
	}
	if st.LeaseRenewTimeouts > 0 {
		injections = append(injections, lease(Injection{
			Intent: IntentRenew, Status: 504, Event: "lease.renew-timeout",
		}))
	}
	if st.LeaseRenewFailures > 0 {
		injections = append(injections, lease(Injection{
			Intent: IntentRenew, Status: 500, Event: "lease.renew-error",
			Message: "injected renew failure",
		}))
	}
	if st.LeaseRenewConflicts > 0 {
		injections = append(injections, lease(Injection{
			Intent: IntentRenew, Status: 409, Event: "lease.renew-conflict",
			consume: func(state *State) {
				// F-02.4：重试链耗尽租期 —— 第 N 次冲突之后租约在服务端已过期。
				if state.LeaseExpireAfterRenewConflicts > 0 {
					state.LeaseExpireAfterRenewConflicts--
					if state.LeaseExpireAfterRenewConflicts == 0 && state.Lease != nil {
						state.Lease.RenewTime = time.Now().
							Add(-time.Duration(state.Lease.DurationSeconds+10) * time.Second).
							UTC().Format(time.RFC3339)
						state.Lease.ResourceVersion = state.NextResourceVersion()
						Append(Dir(), EventsFileName, "lease.expired holder="+state.Lease.HolderIdentity)
					}
				}
			},
		}))
	}
	if st.LeaseRenewUIDConflicts > 0 {
		injections = append(injections, lease(Injection{
			Intent: IntentRenew, Status: 409, Event: "lease.renew-uid-conflict",
			Message: "the UID in the precondition does not match the UID in record",
		}))
	}

	// ---- 通用 replace（续租/接管/释放/记录写入都会命中） ----
	if st.LeaseReplaceTimeouts > 0 {
		injections = append(injections, lease(Injection{
			Status: 504, Event: "lease.replace-timeout",
			Times:   st.LeaseReplaceTimeouts,
			consume: decrement(&st.LeaseReplaceTimeouts),
		}))
	}
	if st.LeaseReplaceFailures > 0 {
		injections = append(injections, lease(Injection{
			Status: 500, Event: "lease.replace-error", Message: "injected lease failure",
			Times:   st.LeaseReplaceFailures,
			consume: decrement(&st.LeaseReplaceFailures),
		}))
	}
	if st.LeaseReplaceConflicts > 0 {
		injections = append(injections, lease(Injection{
			Status: 409, Event: "lease.conflict",
			Times:   st.LeaseReplaceConflicts,
			consume: decrement(&st.LeaseReplaceConflicts),
		}))
	}
	if st.LeaseReplaceUIDConflicts > 0 {
		injections = append(injections, lease(Injection{
			Status: 409, Event: "lease.uid-conflict",
			Message: "the UID in the precondition does not match the UID in record",
			Times:   st.LeaseReplaceUIDConflicts,
			consume: decrement(&st.LeaseReplaceUIDConflicts),
		}))
	}
	// 只作用于「写迁移记录」：确定性验证「重试时重读并保留最新记录」（AUD-D2）。
	if st.LeaseRecordConflicts > 0 {
		injections = append(injections, lease(Injection{
			Intent: IntentRecord, Status: 409, Event: "lease.record-conflict",
			Times:   st.LeaseRecordConflicts,
			consume: decrement(&st.LeaseRecordConflicts),
		}))
	}

	// ---- 写入成功之后立刻失锁（在途窗口） ----
	if st.LeaseStealAfterState != "" {
		injections = append(injections, lease(Injection{
			Phase: "after", Takeover: st.LeaseStealHolder, Event: "lease.stolen-after-state",
			Times:              1,
			RequireAnnotations: map[string]string{AnnotationMigrationState: st.LeaseStealAfterState},
			consume:            func(state *State) { state.LeaseStealAfterState = "" },
		}))
	}
	if st.LeaseStealAfterGets > 0 && st.LeaseGets >= st.LeaseStealAfterGets {
		injections = append(injections, lease(Injection{
			Verb: "get", Phase: "after", Takeover: st.LeaseStealHolder, Event: "lease.stolen",
			Times:   1,
			consume: func(state *State) { state.LeaseStealAfterGets = 0 },
		}))
	}

	// ---- Lease 读取故障 ----
	if st.LeaseGetTimeouts > 0 {
		injections = append(injections, lease(Injection{
			Verb: "get", Status: 504, Event: "lease.get-timeout",
			Times:   st.LeaseGetTimeouts,
			consume: decrement(&st.LeaseGetTimeouts),
		}))
	}
	if st.LeaseGetFailures > 0 {
		injections = append(injections, lease(Injection{
			Verb: "get", Status: 500, Event: "lease.get-error", Message: "injected lease read failure",
			Times:   st.LeaseGetFailures,
			consume: decrement(&st.LeaseGetFailures),
		}))
	}

	// ---- Job 读取 ----
	if st.JobGetFailAt > 0 && st.JobGets+1 == st.JobGetFailAt {
		injections = append(injections, Injection{
			Verb: "get", Group: "batch", Resource: "jobs", Status: 500, Event: "job.get-error",
			Message: "injected job read failure",
			Times:   1,
			consume: func(state *State) { state.JobGetFailAt = 0 },
		})
	}
	if st.JobGetTimeouts > 0 {
		injections = append(injections, Injection{
			Verb: "get", Group: "batch", Resource: "jobs", Status: 504, Event: "job.get-timeout",
			Times:   st.JobGetTimeouts,
			consume: decrement(&st.JobGetTimeouts),
		})
	}
	if st.JobGetFailures > 0 {
		injections = append(injections, Injection{
			Verb: "get", Group: "batch", Resource: "jobs", Status: 500, Event: "job.get-error",
			Message: "injected job read failure",
			Times:   st.JobGetFailures,
			consume: decrement(&st.JobGetFailures),
		})
	}
	if st.JobsListTimeouts > 0 {
		injections = append(injections, Injection{
			Verb: "list", Group: "batch", Resource: "jobs", Status: 504, Event: "jobs.list-timeout",
			Times:   st.JobsListTimeouts,
			consume: decrement(&st.JobsListTimeouts),
		})
	}
	if st.JobsListFailures > 0 {
		injections = append(injections, Injection{
			Verb: "list", Group: "batch", Resource: "jobs", Status: 500, Event: "jobs.list-error",
			Message: "injected job list failure",
			Times:   st.JobsListFailures,
			consume: decrement(&st.JobsListFailures),
		})
	}

	// ---- Pod 读取（按 job-name 与按 component 标签是两个独立入口） ----
	const jobSelector, migrationSelector = "job-name=", "app.kubernetes.io/component=database-migration"
	if st.PodsGetTimeouts > 0 {
		injections = append(injections, Injection{
			Verb: "list", Resource: "pods", Selector: jobSelector,
			Status: 504, Event: "pods.get-timeout",
			Times:   st.PodsGetTimeouts,
			consume: decrement(&st.PodsGetTimeouts),
		})
	}
	if st.PodsGetForbidden > 0 {
		injections = append(injections, Injection{
			Verb: "list", Resource: "pods", Selector: jobSelector,
			Status: 403, Event: "pods.get-forbidden",
			Times:   st.PodsGetForbidden,
			consume: decrement(&st.PodsGetForbidden),
		})
	}
	if st.PodsGetFailures > 0 {
		injections = append(injections, Injection{
			Verb: "list", Resource: "pods", Selector: jobSelector,
			Status: 500, Event: "pods.get-error", Message: "injected pod list failure",
			Times:   st.PodsGetFailures,
			consume: decrement(&st.PodsGetFailures),
		})
	}
	if st.MigrationPodsGetTimeouts > 0 {
		injections = append(injections, Injection{
			Verb: "list", Resource: "pods", Selector: migrationSelector,
			Status: 504, Event: "migration-pods.get-timeout",
			Times:   st.MigrationPodsGetTimeouts,
			consume: decrement(&st.MigrationPodsGetTimeouts),
		})
	}
	if st.MigrationPodsGetForbidden > 0 {
		injections = append(injections, Injection{
			Verb: "list", Resource: "pods", Selector: migrationSelector,
			Status: 403, Event: "migration-pods.get-forbidden",
			Times:   st.MigrationPodsGetForbidden,
			consume: decrement(&st.MigrationPodsGetForbidden),
		})
	}
	if st.MigrationPodsGetFailures > 0 {
		injections = append(injections, Injection{
			Verb: "list", Resource: "pods", Selector: migrationSelector,
			Status: 500, Event: "migration-pods.get-error", Message: "injected migration pod list failure",
			Times:   st.MigrationPodsGetFailures,
			consume: decrement(&st.MigrationPodsGetFailures),
		})
	}

	// ---- apply（按解析出的 Kind → group/resource 匹配，不看文件名） ----
	for _, entry := range []struct {
		key     string
		group   string
		name    string
		status  int
		applied bool
		event   string
	}{
		{"migration", "batch", "jobs", 500, false, "apply.error migration"},
		{"application", "apps", "deployments", 500, false, "apply.error application"},
		{"namespace", "", "namespaces", 500, false, "apply.error namespace"},
	} {
		key, group, resource, event := entry.key, entry.group, entry.name, entry.event
		if st.ApplyFailures[key] {
			injections = append(injections, Injection{
				Verb: "apply", Group: group, Resource: resource,
				Status: entry.status, Event: event, Message: "injected apply failure for " + key,
				Times:   1,
				consume: func(state *State) { delete(state.ApplyFailures, key) },
			})
		}
		if st.ApplyErrorsApplied[key] {
			injections = append(injections, Injection{
				Verb: "apply", Group: group, Resource: resource,
				Status: 504, Applied: true, Event: "apply.error-but-created " + key,
				Times:   1,
				consume: func(state *State) { delete(state.ApplyErrorsApplied, key) },
			})
		}
	}
	return injections
}

// ---------------------------------------------------------------------------
// 状态不变量：全局 resourceVersion、UID 语义、唯一 CAS helper
// ---------------------------------------------------------------------------

// leaseHistoryLimit 是 Lease 版本历史保留的条数（写意图判定只需要最近若干个版本）。
const leaseHistoryLimit = 32

// rememberLeaseVersion 记录当前 Lease 版本的记录注解快照。
//
// 用途：判定一次写是「续租」还是「写迁移记录」时，必须比较**调用方读到的那个版本**
// 与它写出去的内容，而不是比较「当前服务端状态」。否则后台续租与主流程写记录并发时，
// 续租请求携带的是它读到的旧记录（服务端已被主流程推进），会被误判成「写记录」——
// 于是按续租注入的故障（409/500）就漏掉了，注入次数随并发时序漂移（这是真实回归，
// 见 TestRenewThreeCASConflictsExhaustAttempts）。
func (st *State) rememberLeaseVersion() {
	if st.Lease == nil {
		return
	}
	if st.LeaseHistory == nil {
		st.LeaseHistory = map[string]map[string]string{}
	}
	st.LeaseHistory[st.Lease.ResourceVersion] = copyAnnotations(st.Lease.Annotations)
	if len(st.LeaseHistory) <= leaseHistoryLimit {
		return
	}
	// 超出上限：删除版本号最小的条目（resourceVersion 全局单调）。
	oldestKey, oldestValue := "", int64(-1)
	for key := range st.LeaseHistory {
		var value int64
		if _, err := fmt.Sscanf(key, "%d", &value); err != nil {
			oldestKey = key
			break
		}
		if oldestValue < 0 || value < oldestValue {
			oldestValue, oldestKey = value, key
		}
	}
	delete(st.LeaseHistory, oldestKey)
}

// LeaseBaseAnnotations 返回「调用方读到的那个版本」上的记录注解。
// 未知版本返回 nil，调用方应据此按「未改写记录」处理（保守：不伪造 record 意图）。
func (st *State) LeaseBaseAnnotations(resourceVersion string) map[string]string {
	if resourceVersion == "" {
		return nil
	}
	if annotations, ok := st.LeaseHistory[resourceVersion]; ok {
		return annotations
	}
	if st.Lease != nil && st.Lease.ResourceVersion == resourceVersion {
		return copyAnnotations(st.Lease.Annotations)
	}
	return nil
}

// ErrLeaseNotFound 表示 Lease 对象不存在。
var ErrLeaseNotFound = errors.New("leases.coordination.k8s.io not found")

// ErrConflict 表示 CAS 的 resourceVersion 冲突（真实 API Server 的 409）。
var ErrConflict = errors.New("the object has been modified; please apply your changes to the latest version and try again")

// ErrUIDConflict 表示 uid 前置条件不匹配（对象被删除重建）。
var ErrUIDConflict = errors.New("the UID in the precondition does not match the UID in record")

// NextResourceVersion 推进**全局**单调计数器并返回新值（审计 C-9）。
//
// 旧实现是按对象自增（每个对象各有一份 1,2,3…），因此「不同对象的版本号相同」
// 会让「版本是否推进」这类断言失去意义。这里保证：任何一次成功写入都推进全局计数，
// 且同一个对象的版本号严格递增、不同对象之间不重复。
func (st *State) NextResourceVersion() string {
	st.ResourceVersionSeq++
	return strconv.FormatInt(st.ResourceVersionSeq, 10)
}

// NextLeaseUID 返回一个新的 Lease uid；删除重建后一定不同（对象 uid 不复用）。
func (st *State) NextLeaseUID() string {
	st.LeaseUIDSeq++
	return "lease-uid-" + strconv.Itoa(st.LeaseUIDSeq)
}

// TakeoverLease 是**唯一**的并发接管实现（审计 C-3）：holder / renewTime /
// transitions / resourceVersion 一起变。fake 注入、fakekubectl 与 harness 都调用它，
// 避免出现「有的实现推进 rv、有的不推进」。
func TakeoverLease(st *State, holder string) error {
	if st == nil || st.Lease == nil {
		return ErrLeaseNotFound
	}
	now := time.Now().UTC().Format(time.RFC3339)
	st.Lease.HolderIdentity = holder
	st.Lease.AcquireTime = now
	st.Lease.RenewTime = now
	st.Lease.Transitions++
	st.Lease.ResourceVersion = st.NextResourceVersion()
	st.rememberLeaseVersion()
	return nil
}

// ErrLeaseExists 表示同名 Lease 已存在（create 的 AlreadyExists）。
var ErrLeaseExists = errors.New("leases.coordination.k8s.io already exists")

// CreateLease 原子创建一个 Lease：分配全局 rv 与**新** uid（删除重建后不复用），
// 并记录版本快照（写意图判定的基线）。
func CreateLease(st *State, lease Lease) error {
	if st.Lease != nil {
		return ErrLeaseExists
	}
	created := lease
	created.ResourceVersion = st.NextResourceVersion()
	created.UID = st.NextLeaseUID()
	created.Annotations = copyAnnotations(lease.Annotations)
	st.Lease = &created
	st.LeaseAcquisitions++
	st.rememberLeaseVersion()
	return nil
}

// DeleteLease 删除 Lease 对象；返回是否真的删除了。删除后重建必须产生新 UID。
func DeleteLease(st *State) bool {
	if st == nil || st.Lease == nil {
		return false
	}
	st.Lease = nil
	return true
}

// LeaseUpdate 描述一次 PUT 的期望前置条件与写入内容。
type LeaseUpdate struct {
	// ExpectResourceVersion / ExpectUID 为空表示不校验该前置条件。
	ExpectResourceVersion string
	ExpectUID             string
	HolderIdentity        string
	AcquireTime           string
	RenewTime             string
	DurationSeconds       int
	Transitions           int
	Annotations           map[string]string
}

// CommitLeaseUpdate 是**唯一**的 CAS helper（审计 C-1 / C-2 / C-3）。
//
// 所有写路径（replace / apply-update / takeover）都必须经它比较 resourceVersion 与
// uid；任何写路径跳过 CAS 都会让「CAS 冲突」这条行为无法被证伪。
func CommitLeaseUpdate(st *State, update LeaseUpdate) (*Lease, error) {
	if st == nil || st.Lease == nil {
		return nil, ErrLeaseNotFound
	}
	if update.ExpectUID != "" && update.ExpectUID != st.Lease.UID {
		return nil, ErrUIDConflict
	}
	if update.ExpectResourceVersion != "" && update.ExpectResourceVersion != st.Lease.ResourceVersion {
		return nil, ErrConflict
	}
	previous := st.Lease.Clone()
	st.Lease.HolderIdentity = update.HolderIdentity
	st.Lease.AcquireTime = update.AcquireTime
	st.Lease.RenewTime = update.RenewTime
	st.Lease.DurationSeconds = update.DurationSeconds
	st.Lease.Transitions = update.Transitions
	st.Lease.Annotations = copyAnnotations(update.Annotations)
	st.Lease.ResourceVersion = st.NextResourceVersion()
	st.rememberLeaseVersion()
	return previous, nil
}

// Clone 返回 Lease 的深拷贝（注解 map 必须一起复制，否则「上一版记录」会被覆盖）。
func (l *Lease) Clone() *Lease {
	if l == nil {
		return nil
	}
	clone := *l
	clone.Annotations = copyAnnotations(l.Annotations)
	return &clone
}

func copyAnnotations(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
