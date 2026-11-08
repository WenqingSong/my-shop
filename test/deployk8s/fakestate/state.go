// Package fakestate 是 T2-06.1 / T2-06.1.1 发布脚本行为测试的「集群状态」模型。
//
// 设计边界（必须如实理解）：
//   - 这是 fake kubectl 的状态文件，不是 Kubernetes API Server 的模拟器；
//   - 它只实现 deploy-k8s.sh 真正用到的语义：Lease 的原子 create / CAS replace（含
//     resourceVersion 与 UID 校验）、Job 条件与 UID、Deployment 状态字段、Pod 状态；
//   - 它能证明「脚本在给定状态序列下的行为」，**不能**证明「真实 API Server 的并发语义」
//     （etcd 事务、多 apiserver、watch 缓存）。真实集群验证仍然是 NOT_VERIFIED
//     （见 docs/development/kubernetes.md §14）。
package fakestate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// 状态目录中的文件名。
const (
	StateFileName       = "state.json"
	CallsFileName       = "calls.log"
	EventsFileName      = "events.log"
	LockFileName        = "state.lock"
	MigrationManifest   = "migration.yaml"
	ApplicationManifest = "application.yaml"
)

// Permissions 模拟发布身份的授权范围。
//
// 权威字段是 Rules（见 rbac.go）：结构化规则 + 唯一的 Authorize 判定入口，
// 可以整体替换成「从真实 RBAC 清单解析出来的规则」（RBACRulesFromYAML）。
//
// Get/Create/Update/Patch/Denied 是 T2-06.1.3 及更早的写法，**已废弃**：
// Load() 会把它们翻译成 Rules（Get→get/list、Create→create/delete、Update→update、
// Patch→patch 的一条 allow 规则；Denied 的每个 "<verb> <resource>" → 一条 deny 规则）。
// 翻译出的规则带 Source="legacy" 标记，每次 Normalize 重新生成，因此不存在两套并存的
// 判定逻辑 —— 判定永远只看 Rules。
type Permissions struct {
	// Rules 是授权规则（默认拒绝：没有匹配的 allow 规则即 Forbidden）。
	Rules []Rule `json:"rules,omitempty"`

	// Deprecated: 历史布尔开关，Load() 时翻译为 Rules。
	Get    bool     `json:"get,omitempty"`
	Create bool     `json:"create,omitempty"`
	Update bool     `json:"update,omitempty"`
	Patch  bool     `json:"patch,omitempty"`
	Denied []string `json:"denied,omitempty"`
}

// Lease 是 coordination.k8s.io/v1 Lease 中脚本实际读写的字段。
//
// Annotations 承载迁移生命周期记录（T2-06.1.1）：脚本用注解把「本轮迁移 Job」与锁绑定，
// fake 必须在 create/replace 时完整持久化它，否则「PUT 丢记录」这类缺陷无法被发现。
type Lease struct {
	ResourceVersion string            `json:"resourceVersion"`
	UID             string            `json:"uid"`
	HolderIdentity  string            `json:"holderIdentity"`
	AcquireTime     string            `json:"acquireTime"`
	RenewTime       string            `json:"renewTime"`
	DurationSeconds int               `json:"leaseDurationSeconds"`
	Transitions     int               `json:"leaseTransitions"`
	Annotations     map[string]string `json:"annotations"`
}

// Container 是一个容器状态（AUD-11：多容器 Pod 只能按名字取应用容器）。
type Container struct {
	Name  string `json:"name"`
	Image string `json:"image"`
	Ready bool   `json:"ready"`
}

// Pod 是发布验证与迁移超时判定需要的 Pod 字段。
type Pod struct {
	Name     string `json:"name"`
	Phase    string `json:"phase"`
	Deleting bool   `json:"deleting"`
	Version  string `json:"version"`
	Image    string `json:"image"`
	Ready    bool   `json:"ready"`
	// Containers 非空时按它渲染（可构造 sidecar / 多容器场景）；
	// 为空时 fake 使用默认的单个 app 容器。
	Containers []Container `json:"containers"`
	// OwnerJobUID 覆盖 Pod 的 ownerReferences Job uid（T2-06.1.2 / AUD-D3）：
	// 默认为「该 Job 的 uid」，用于构造「同标签但属于另一个 Job」的干扰 Pod。
	OwnerJobUID string `json:"ownerJobUID"`
	// Orphan 为 true 时不渲染任何 ownerReferences：用于构造「无法归属」的孤儿 Pod，
	// 脚本必须据此 fail-closed（不能仅凭 job-name 标签就认定归属）。
	Orphan bool `json:"orphan"`
}

// Deployment 是 Deployment 的结构化快照（只含脚本读取的字段）。
type Deployment struct {
	Exists              bool   `json:"exists"`
	Generation          int    `json:"generation"`
	ObservedGeneration  int    `json:"observedGeneration"`
	Replicas            int    `json:"replicas"`
	StatusReplicas      int    `json:"statusReplicas"`
	UpdatedReplicas     int    `json:"updatedReplicas"`
	AvailableReplicas   int    `json:"availableReplicas"`
	UnavailableReplicas int    `json:"unavailableReplicas"`
	Image               string `json:"image"`
	Version             string `json:"version"`
	Pods                []Pod  `json:"pods"`
}

// Job 是迁移 Job 的状态快照。
type Job struct {
	// Name 为空时，fake 按请求的 Job 名渲染（便于脚本按名称核验）。
	Name     string `json:"name"`
	UID      string `json:"uid"`
	Exists   bool   `json:"exists"`
	Deleting bool   `json:"deleting"`
	Complete bool   `json:"complete"`
	Failed   bool   `json:"failed"`
	Reason   string `json:"reason"`
	Message  string `json:"message"`
	Logs     string `json:"logs"`
	Pods     []Pod  `json:"pods"`
}

// State 是 fake kubectl 的全部可变状态。
//
// 读模型的阶段区分很重要：
//   - Deployment 表示「Application 清单 apply 之前」的线上状态（版本降级保护读取它）；
//   - DeploymentAfterApply 是 apply 之后逐个 get 返回的状态序列（最后一个重复），
//     用来构造「收敛中」「收敛后又被他方改写」「Pod 未 Ready」等时序场景；
//   - JobScript 同理，是迁移等待循环每次 get 返回的状态序列；
//   - Jobs 是 `get jobs -l ...` 的列表结果（迁移生命周期扫描用），为空时退化为 st.Job。
type State struct {
	ClusterReachable bool        `json:"clusterReachable"`
	NamespaceExists  bool        `json:"namespaceExists"`
	Permissions      Permissions `json:"permissions"`
	// AuthCanIError 非空时，`auth can-i` 以该错误文本失败（非 0 且不是 "no"）：
	// 模拟 SubjectAccessReview 无法完成（HTTP 5xx / 网络 / RBAC 自身不可用）。
	// 用于验证权限预检能区分「明确被拒绝」与「无法确认」（T2-06.1.3 / F-01）。
	AuthCanIError string   `json:"authCanIError"`
	Secrets       []string `json:"secrets"`

	// ResourceVersionSeq / LeaseUIDSeq 是**全局**单调计数器（T2-06.1.4 / C-9）：
	// 任何写都推进 ResourceVersionSeq，因此不会出现「两个对象版本号相同」或版本回退。
	ResourceVersionSeq int64 `json:"resourceVersionSeq"`
	// LeaseUIDSeq 保证「删除后重建」产生新 uid（对象 uid 不复用）。
	LeaseUIDSeq int `json:"leaseUIDSeq"`
	// Injections 是显式故障注入（按调用语义匹配，见 injection.go）。
	// 旧的扁平注入字段仍然有效，但它们只是 Injection 的薄封装。
	Injections []Injection `json:"injections,omitempty"`

	Lease *Lease `json:"lease"`

	// 下面的 Lease* 注入字段**全部是 Injection 的薄封装**（见 injection.go 的
	// legacyInjections）：匹配条件一律是「调用语义」（verb=update/get，
	// group=coordination.k8s.io，resource=leases，以及由清单内容推导的写意图），
	// **不再按 YAML 文件名判断**。新测试请直接用 State.Injections。

	// LeaseReplaceFailures 让接下来 N 次 replace（续租/接管/释放）返回 API 500。
	LeaseReplaceFailures int `json:"leaseReplaceFailures"`
	// LeaseGetFailures 让接下来 N 次 get lease 返回 API 500。
	LeaseGetFailures int `json:"leaseGetFailures"`
	// LeaseReplaceConflicts 让接下来 N 次 replace 返回确定性 409（resourceVersion 冲突）。
	LeaseReplaceConflicts int `json:"leaseReplaceConflicts"`
	// LeaseRecordConflicts 只让接下来 N 次「写迁移记录」的 update 返回 409
	// （写意图 intent=record：holder 不变但迁移记录注解被改写）：
	// 用于确定性地验证「重试时必须重读并保留最新记录」（AUD-D2 / AUD-D5-B6）。
	LeaseRecordConflicts int `json:"leaseRecordConflicts"`
	// LeaseReplaceUIDConflicts 让接下来 N 次 replace 返回「UID 不匹配」的 409。
	LeaseReplaceUIDConflicts int `json:"leaseReplaceUIDConflicts"`
	// LeaseReplaceTimeouts 让接下来 N 次 replace 以客户端请求超时失败。
	LeaseReplaceTimeouts int `json:"leaseReplaceTimeouts"`
	// LeaseGetTimeouts 让接下来 N 次 get lease 以客户端请求超时失败。
	LeaseGetTimeouts int `json:"leaseGetTimeouts"`
	// 续租专用故障（只作用于 intent=renew 的 update：holder 不变且记录未变）：
	// 续租是最不能慢/最不能失败的请求，需要与「写迁移记录」区分开的确定性注入。
	LeaseRenewFailures     int `json:"leaseRenewFailures"`
	LeaseRenewConflicts    int `json:"leaseRenewConflicts"`
	LeaseRenewTimeouts     int `json:"leaseRenewTimeouts"`
	LeaseRenewUIDConflicts int `json:"leaseRenewUIDConflicts"`
	// LeaseStealBeforeRenewReplace > 0 时，在下一次续租 replace **执行 CAS 之前**
	// 先模拟「另一个 Runner 已经接管」：推进 resourceVersion 并改写 holder（T2-06.1.3 / F-06）。
	// 这样旧持有者用旧 resourceVersion 的 PUT 必须得到 409，且不能覆盖新 holder。
	LeaseStealBeforeRenewReplace int `json:"leaseStealBeforeRenewReplace"`
	// LeaseExpireAfterRenewConflicts > 0 时，在第 N 次「续租 replace 冲突」之后把
	// 服务器上的 renewTime 推到已过期：模拟「重试链比租期还长，其他 Runner 已可接管」。
	// 用于验证续租在过期后不再盲目写入（F-02.4）。
	LeaseExpireAfterRenewConflicts int `json:"leaseExpireAfterRenewConflicts"`
	// LeaseStealAfterGets > 0 时，在第 N 次 get lease 之后把持有者替换为 LeaseStealHolder，
	// 模拟「旧持有者的锁已过期并被其他 Runner 接管」。
	LeaseStealAfterGets int    `json:"leaseStealAfterGets"`
	LeaseStealHolder    string `json:"leaseStealHolder"`
	// LeaseStealAfterState 非空时，一旦某次 replace 成功写入该迁移状态
	// （注解 deploy-lock.surgecart.io/migration-state），锁立即被 LeaseStealHolder 接管。
	// 这是确定性的「写入成功后立刻失锁」注入，用于验证脚本不再发起新操作。
	LeaseStealAfterState string `json:"leaseStealAfterState"`
	MigrationApplied     bool   `json:"migrationApplied"`
	// LeaseAcquisitions 统计成功获取锁的次数（create 成功或非空持有者接管成功）。
	LeaseGets         int `json:"leaseGets"`
	LeaseAcquisitions int `json:"leaseAcquisitions"`

	Job       Job   `json:"job"`
	JobScript []Job `json:"jobScript"`
	JobGets   int   `json:"jobGets"`
	// Jobs 是命名空间内全部迁移 Job（生命周期扫描用）；为空时退化为 []Job{st.Job}。
	Jobs []Job `json:"jobs"`
	// MigrationPods 是 `get pods -l app.kubernetes.io/component=database-migration` 的
	// 结果（T2-06.1.2）：用于构造「Job 已被删除但 Pod 仍存活」的孤儿/残留场景。
	MigrationPods []Pod `json:"migrationPods"`
	// JobPods 按 job-name 标签索引 Pod：`get pods -l job-name=<name>` 优先取这里，
	// 未命中时回退到 Job.Pods。Pod 的生命周期独立于 Job 对象是否存在 ——
	// Job 被 TTL 清理后，残留 Pod 仍然会被这个标签查询命中。
	JobPods        map[string][]Pod `json:"jobPods"`
	JobGetFailures int              `json:"jobGetFailures"`
	JobGetTimeouts int              `json:"jobGetTimeouts"`
	// JobGetFailAt > 0 时，第 N 次 get job 返回 API 500（只触发一次）。
	// 用于「等待循环读得到、二次判定读失败」这类确定性时序。
	JobGetFailAt     int `json:"jobGetFailAt"`
	JobsListFailures int `json:"jobsListFailures"`
	JobsListTimeouts int `json:"jobsListTimeouts"`
	// PodsGetFailures / PodsGetTimeouts 只作用于「按 job-name 读取 Pod」。
	PodsGetFailures int `json:"podsGetFailures"`
	PodsGetTimeouts int `json:"podsGetTimeouts"`
	// PodsGetForbidden 让接下来 N 次「按 job-name 读取 Pod」返回 403 Forbidden
	// （与 500/超时区分开：审计 F-01/F-07 要求错误信息不得把 403 说成「Pod 存在」）。
	PodsGetForbidden int `json:"podsGetForbidden"`
	// MigrationPodsGetFailures / Timeouts 只作用于「按 component 标签扫描迁移 Pod」
	// （全局残留扫描，AUD-D3/D4）。分开注入是为了让两处 fail-closed 行为可独立验证。
	MigrationPodsGetFailures  int `json:"migrationPodsGetFailures"`
	MigrationPodsGetTimeouts  int `json:"migrationPodsGetTimeouts"`
	MigrationPodsGetForbidden int `json:"migrationPodsGetForbidden"`

	Deployment           Deployment   `json:"deployment"`
	DeploymentAfterApply []Deployment `json:"deploymentAfterApply"`
	DeploymentGets       int          `json:"deploymentGets"`
	// DeploymentGetError 非空时 get deployment 直接返回该错误（模拟命名空间不存在、
	// 鉴权失败等「NotFound 但不是 Deployment 不存在」的场景，AUD-09）。
	DeploymentGetError string `json:"deploymentGetError"`
	Applied            bool   `json:"applied"`

	// ApplyFailures 按清单类型（migration / application）注入 apply 失败（对象未被创建）。
	ApplyFailures map[string]bool `json:"applyFailures"`
	// ApplyErrorsApplied 按清单类型注入「API 返回错误，但对象实际已创建」
	// （客户端超时 / 网关错误；AUD-01 要求脚本不能靠 apply 的退出码判断是否创建）。
	ApplyErrorsApplied map[string]bool `json:"applyErrorsApplied"`
}

// Dir 返回状态目录（由测试通过环境变量注入）。
func Dir() string { return os.Getenv("FAKE_KUBECTL_STATE") }

// Load 读取状态文件，并把历史写法归一为新模型（幂等）。
func Load(dir string) (*State, error) {
	raw, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", StateFileName, err)
	}
	st.Normalize()
	return &st, nil
}

// Normalize 把「旧的扁平写法」翻译成新模型，使判定与注入都只有一条路径：
//   - Permissions.Get/Create/Update/Patch/Denied → Permissions.Rules（rbac.go）；
//   - 全局 resourceVersion 计数器以现存对象的版本号为下界（不回退）。
//
// 幂等：可以重复调用。
func (st *State) Normalize() {
	st.Permissions.Normalize()
	if st.ApplyFailures == nil {
		st.ApplyFailures = map[string]bool{}
	}
	if st.ApplyErrorsApplied == nil {
		st.ApplyErrorsApplied = map[string]bool{}
	}
	if st.Lease != nil {
		if value, err := strconv.ParseInt(st.Lease.ResourceVersion, 10, 64); err == nil && value > st.ResourceVersionSeq {
			st.ResourceVersionSeq = value
		}
	}
}

// Save 原子写回状态文件（临时文件 + rename）。
func Save(dir string, st *State) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, StateFileName+".tmp")
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, StateFileName))
}

// Lock 对状态目录加排他文件锁：并发 Runner 的 create 必须真正互斥，
// 否则「两个 Runner 同时申请锁只有一个成功」的测试就没有意义。
func Lock(dir string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(dir, LockFileName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
	}, nil
}

// Append 追加一行到日志文件（调用方需自行持锁）。
func Append(dir, name, line string) {
	file, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(line + "\n")
}

// Read 读取日志文件的全部行。
func Read(dir, name string) []string {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return nil
	}
	trimmed := strings.TrimSuffix(string(raw), "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}
