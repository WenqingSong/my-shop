// Package fakestate 是 T2-06.1 发布脚本行为测试的「集群状态」模型。
//
// 设计边界（必须如实理解）：
//   - 这是 fake kubectl 的状态文件，不是 Kubernetes API Server 的模拟器；
//   - 它只实现 deploy-k8s.sh 真正用到的语义：Lease 的原子 create / CAS replace、
//     Deployment 状态字段、Job 条件、Pod 状态；
//   - 因此它能证明「脚本在给定状态序列下的行为」，不能证明「真实 API Server 的并发语义」。
//     真实集群验证仍然是 NOT_VERIFIED（见 docs/reports/t2-06-1-deploy-safety）。
package fakestate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// Permissions 模拟 `kubectl auth can-i <verb> leases.coordination.k8s.io`。
type Permissions struct {
	Get    bool `json:"get"`
	Create bool `json:"create"`
	Update bool `json:"update"`
	Patch  bool `json:"patch"`
}

// Lease 是 coordination.k8s.io/v1 Lease 中脚本实际读写的字段。
type Lease struct {
	ResourceVersion string `json:"resourceVersion"`
	UID             string `json:"uid"`
	HolderIdentity  string `json:"holderIdentity"`
	AcquireTime     string `json:"acquireTime"`
	RenewTime       string `json:"renewTime"`
	DurationSeconds int    `json:"leaseDurationSeconds"`
	Transitions     int    `json:"leaseTransitions"`
}

// Pod 是发布验证与迁移超时判定需要的 Pod 字段。
type Pod struct {
	Name     string `json:"name"`
	Phase    string `json:"phase"`
	Deleting bool   `json:"deleting"`
	Version  string `json:"version"`
	Image    string `json:"image"`
	Ready    bool   `json:"ready"`
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
	Exists   bool   `json:"exists"`
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
//   - JobScript 同理，是迁移等待循环每次 get 返回的状态序列。
type State struct {
	ClusterReachable bool        `json:"clusterReachable"`
	NamespaceExists  bool        `json:"namespaceExists"`
	Permissions      Permissions `json:"permissions"`
	Secrets          []string    `json:"secrets"`

	Lease *Lease `json:"lease"`
	// LeaseReplaceFailures 让接下来 N 次 replace（续租/接管/释放）返回 API 500，
	// 用于模拟「续租失败」与「API Server 抖动」。
	LeaseReplaceFailures int `json:"leaseReplaceFailures"`
	// LeaseGetFailures 让接下来 N 次 get lease 返回 API 500。
	LeaseGetFailures int `json:"leaseGetFailures"`
	// LeaseStealAfterGets > 0 时，在第 N 次 get lease 之后把持有者替换为 LeaseStealHolder，
	// 模拟「旧持有者的锁已过期并被其他 Runner 接管」。
	LeaseStealAfterGets int    `json:"leaseStealAfterGets"`
	LeaseStealHolder    string `json:"leaseStealHolder"`
	LeaseGets           int    `json:"leaseGets"`
	// LeaseAcquisitions 统计成功获取锁的次数（create 成功或非空持有者接管成功）。
	LeaseAcquisitions int `json:"leaseAcquisitions"`

	Job       Job   `json:"job"`
	JobScript []Job `json:"jobScript"`
	JobGets   int   `json:"jobGets"`

	Deployment           Deployment   `json:"deployment"`
	DeploymentAfterApply []Deployment `json:"deploymentAfterApply"`
	DeploymentGets       int          `json:"deploymentGets"`
	Applied              bool         `json:"applied"`

	// ApplyFailures 按清单类型（migration / application）注入 apply 失败。
	ApplyFailures map[string]bool `json:"applyFailures"`
}

// Dir 返回状态目录（由测试通过环境变量注入）。
func Dir() string { return os.Getenv("FAKE_KUBECTL_STATE") }

// Load 读取状态文件。
func Load(dir string) (*State, error) {
	raw, err := os.ReadFile(filepath.Join(dir, StateFileName))
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", StateFileName, err)
	}
	return &st, nil
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
