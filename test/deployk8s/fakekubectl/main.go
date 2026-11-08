// fake kubectl —— T2-06.1 / T2-06.1.1 发布脚本行为测试的执行替身。
//
// 它不是 kubectl 的完整实现，只忠实实现 scripts/deploy-k8s.sh 真正依赖的语义：
//
//	version                               集群可达性
//	kustomize <path>                      输出预置清单（migration.yaml / application.yaml）
//	auth can-i <verb> <resource>          权限预检
//	create -f <Lease>                     原子创建（同名只能成功一次），持久化注解
//	replace -f <Lease>                    PUT + resourceVersion/UID 校验的 CAS，持久化注解
//	apply -f <manifest>                   记录 apply，并按清单 kind 推进阶段
//	get namespace|secret|lease|job|jobs|deployment|pods
//	logs | describe | wait                只输出文本，供失败 runbook 使用
//
// 故障注入（T2-06.1.1 / AUD-03、AUD-04）：
//   - API 500 / 客户端请求超时 / 确定性 409 / UID 冲突；
//   - Job 不存在、Job 状态按脚本序列推进、Job 删除中；
//   - 「apply 返回错误但对象已创建」；
//   - 迁移之后第一次 get lease 时锁被接管（检查-写入竞态窗口）。
//
// 关键点：并发 Runner 的 create/replace 用 flock 串行化，因此「两个 Runner 同时申请锁
// 只有一个成功」在**脚本行为**层面是真实竞争出来的，不是预设结论。
// 但 fake 无法证明真实 API Server 的并发语义（etcd 事务、watch 缓存、多副本 apiserver），
// 那部分始终是 NOT_VERIFIED。
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

const (
	lockName  = "surgecart-deploy-lock"
	namespace = "surgecart"
	// 迁移生命周期记录的状态注解键（与 scripts/deploy-k8s.sh 的数据契约一致）。
	annMigrationState = "deploy-lock.surgecart.io/migration-state"
)

func main() {
	dir := fakestate.Dir()
	if dir == "" {
		fmt.Fprintln(os.Stderr, "fake kubectl: 环境变量 FAKE_KUBECTL_STATE 未设置")
		os.Exit(99)
	}
	args := os.Args[1:]
	recordCall(dir, args)
	os.Exit(run(dir, args))
}

// ---------------------------------------------------------------------------
// 调用记录与状态访问
// ---------------------------------------------------------------------------

func recordCall(dir string, args []string) {
	unlock, err := fakestate.Lock(dir)
	if err != nil {
		return
	}
	defer unlock()
	fakestate.Append(dir, fakestate.CallsFileName, strings.Join(args, " "))
}

func event(dir, name string) {
	unlock, err := fakestate.Lock(dir)
	if err != nil {
		return
	}
	defer unlock()
	fakestate.Append(dir, fakestate.EventsFileName, name)
}

// mutate 在文件锁内读取-修改-写回状态；fn 返回进程退出码。
func mutate(dir string, fn func(*fakestate.State) int) int {
	unlock, err := fakestate.Lock(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake kubectl: 获取状态锁失败: %v\n", err)
		return 97
	}
	defer unlock()

	st, err := fakestate.Load(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake kubectl: 读取状态失败: %v\n", err)
		return 97
	}
	code := fn(st)
	if err := fakestate.Save(dir, st); err != nil {
		fmt.Fprintf(os.Stderr, "fake kubectl: 写回状态失败: %v\n", err)
		return 97
	}
	return code
}

// ---------------------------------------------------------------------------
// 参数解析
// ---------------------------------------------------------------------------

type invocation struct {
	Namespace  string
	Output     string
	Selector   string
	Filename   string
	Positional []string
}

func parseInvocation(args []string) invocation {
	inv := invocation{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		next := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case arg == "--context" || arg == "--request-timeout" || arg == "--tail" ||
			arg == "-p" || arg == "--type" || arg == "--timeout" || arg == "--for":
			next()
		case strings.HasPrefix(arg, "--context=") || strings.HasPrefix(arg, "--request-timeout=") ||
			strings.HasPrefix(arg, "--tail=") || strings.HasPrefix(arg, "--type=") ||
			strings.HasPrefix(arg, "--timeout=") || strings.HasPrefix(arg, "--for=") ||
			arg == "--all-containers=true" || arg == "--all-containers":
			// 无值或无副作用的参数：忽略。
		case arg == "-n" || arg == "--namespace":
			inv.Namespace = next()
		case strings.HasPrefix(arg, "--namespace="):
			inv.Namespace = strings.TrimPrefix(arg, "--namespace=")
		case arg == "-o" || arg == "--output":
			inv.Output = next()
		case strings.HasPrefix(arg, "--output="):
			inv.Output = strings.TrimPrefix(arg, "--output=")
		case arg == "-f" || arg == "--filename":
			inv.Filename = next()
		case strings.HasPrefix(arg, "--filename="):
			inv.Filename = strings.TrimPrefix(arg, "--filename=")
		case arg == "-l" || arg == "--selector":
			inv.Selector = next()
		case strings.HasPrefix(arg, "--selector="):
			inv.Selector = strings.TrimPrefix(arg, "--selector=")
		case strings.HasPrefix(arg, "-"):
			// 其他未知 flag：忽略，不参与定位子命令。
		default:
			inv.Positional = append(inv.Positional, arg)
		}
	}
	return inv
}

// ---------------------------------------------------------------------------
// 命令分发
// ---------------------------------------------------------------------------

func run(dir string, args []string) int {
	inv := parseInvocation(args)
	if len(inv.Positional) == 0 {
		fmt.Fprintln(os.Stderr, "fake kubectl: 缺少子命令")
		return 1
	}
	switch inv.Positional[0] {
	case "version":
		return cmdVersion(dir)
	case "kustomize":
		return cmdKustomize(dir, inv)
	case "auth":
		return cmdAuth(dir, inv)
	case "create":
		return cmdCreate(dir, inv)
	case "replace":
		return cmdReplace(dir, inv)
	case "apply":
		return cmdApply(dir, inv)
	case "get":
		return cmdGet(dir, inv)
	case "logs":
		return cmdLogs(dir)
	case "describe":
		fmt.Println("Events: <none>")
		return 0
	case "wait":
		fmt.Println("condition met")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "fake kubectl: 未实现的命令 %q\n", strings.Join(args, " "))
		event(dir, "unsupported "+strings.Join(args, " "))
		return 1
	}
}

func cmdVersion(dir string) int {
	st, err := fakestate.Load(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake kubectl: %v\n", err)
		return 97
	}
	if !st.ClusterReachable {
		fmt.Fprintln(os.Stderr, "The connection to the server localhost:8080 was refused - did you specify the right host or port?")
		return 1
	}
	fmt.Println("Client Version: v1.31.0")
	fmt.Println("Server Version: v1.31.0")
	return 0
}

func cmdKustomize(dir string, inv invocation) int {
	if len(inv.Positional) < 2 {
		fmt.Fprintln(os.Stderr, "fake kubectl: kustomize 缺少路径")
		return 1
	}
	name := fakestate.ApplicationManifest
	if strings.Contains(inv.Positional[1], "migration") {
		name = fakestate.MigrationManifest
	}
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: accumulating resources from %q: %v\n", inv.Positional[1], err)
		return 1
	}
	os.Stdout.Write(raw)
	return 0
}

func cmdAuth(dir string, inv invocation) int {
	if len(inv.Positional) < 3 {
		fmt.Fprintln(os.Stderr, "fake kubectl: auth can-i 参数不足")
		return 1
	}
	verb := inv.Positional[2]
	return mutate(dir, func(st *fakestate.State) int {
		var allowed bool
		switch verb {
		case "get":
			allowed = st.Permissions.Get
		case "create":
			allowed = st.Permissions.Create
		case "update":
			allowed = st.Permissions.Update
		case "patch":
			allowed = st.Permissions.Patch
		case "list":
			// 迁移生命周期核验需要 jobs 的 list 权限，沿用 get 的授权开关。
			allowed = st.Permissions.Get
		default:
			allowed = false
		}
		if allowed {
			fmt.Println("yes")
			return 0
		}
		fmt.Println("no")
		return 1
	})
}

// leaseManifest 是 create/replace 传入的 Lease 清单。
type leaseManifest struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name            string            `yaml:"name"`
		Namespace       string            `yaml:"namespace"`
		ResourceVersion string            `yaml:"resourceVersion"`
		UID             string            `yaml:"uid"`
		Annotations     map[string]string `yaml:"annotations"`
	} `yaml:"metadata"`
	Spec struct {
		HolderIdentity       string `yaml:"holderIdentity"`
		LeaseDurationSeconds int    `yaml:"leaseDurationSeconds"`
		AcquireTime          string `yaml:"acquireTime"`
		RenewTime            string `yaml:"renewTime"`
		LeaseTransitions     int    `yaml:"leaseTransitions"`
	} `yaml:"spec"`
}

func readLeaseManifest(path string) (*leaseManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest leaseManifest
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		return nil, err
	}
	if manifest.Kind != "Lease" {
		return nil, fmt.Errorf("不是 Lease 清单（kind=%q）", manifest.Kind)
	}
	return &manifest, nil
}

func cmdCreate(dir string, inv invocation) int {
	path := fileFlag(inv)
	if path == "" {
		fmt.Fprintln(os.Stderr, "fake kubectl: create 缺少 -f")
		return 1
	}
	manifest, err := readLeaseManifest(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return mutate(dir, func(st *fakestate.State) int {
		if st.Lease != nil {
			fmt.Fprintf(os.Stderr, "Error from server (AlreadyExists): error when creating %q: leases.coordination.k8s.io %q already exists\n", path, lockName)
			return 1
		}
		st.Lease = &fakestate.Lease{
			ResourceVersion: "1",
			UID:             fmt.Sprintf("lease-uid-%d", st.LeaseAcquisitions+1),
			HolderIdentity:  manifest.Spec.HolderIdentity,
			AcquireTime:     manifest.Spec.AcquireTime,
			RenewTime:       manifest.Spec.RenewTime,
			DurationSeconds: manifest.Spec.LeaseDurationSeconds,
			Transitions:     manifest.Spec.LeaseTransitions,
			Annotations:     manifest.Metadata.Annotations,
		}
		st.LeaseAcquisitions++
		fakestate.Append(dir, fakestate.EventsFileName, "lease.acquire holder="+manifest.Spec.HolderIdentity)
		fmt.Printf("lease.coordination.k8s.io/%s created\n", lockName)
		return 0
	})
}

// conflictMessage 与真实 API Server 的 409 文案同形：脚本用「409 Conflict / 已修改」
// 作为「CAS 失败」的唯一判据。
func conflictMessage(reason string) {
	fmt.Fprintf(os.Stderr, "Error from server (Conflict): Operation cannot be fulfilled on leases.coordination.k8s.io %q: %s\n", lockName, reason)
}

func cmdReplace(dir string, inv invocation) int {
	path := fileFlag(inv)
	if path == "" {
		fmt.Fprintln(os.Stderr, "fake kubectl: replace 缺少 -f")
		return 1
	}
	manifest, err := readLeaseManifest(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return mutate(dir, func(st *fakestate.State) int {
		if st.Lease == nil {
			fmt.Fprintf(os.Stderr, "Error from server (NotFound): leases.coordination.k8s.io %q not found\n", lockName)
			return 1
		}
		// 续租故障是「持续性」的：脚本会对 409 做有限重试，单次失败不足以判定失锁，
		// 因此这几个计数器不递减（模拟 API 持续不可用 / 锁持续被抢占）。
		isRenew := filepath.Base(path) == "lease-renew.yaml"
		if isRenew && st.LeaseRenewTimeouts > 0 {
			fakestate.Append(dir, fakestate.EventsFileName, "lease.renew-timeout")
			fmt.Fprintln(os.Stderr, "Unable to connect to the server: net/http: request canceled (Client.Timeout exceeded while awaiting headers)")
			return 1
		}
		if isRenew && st.LeaseRenewFailures > 0 {
			fakestate.Append(dir, fakestate.EventsFileName, "lease.renew-error")
			fmt.Fprintln(os.Stderr, `Error from server (InternalError): an error on the server ("injected renew failure") has prevented the request from succeeding`)
			return 1
		}
		if isRenew && st.LeaseRenewConflicts > 0 {
			fakestate.Append(dir, fakestate.EventsFileName, "lease.renew-conflict")
			conflictMessage("the object has been modified; please apply your changes to the latest version and try again")
			return 1
		}
		if isRenew && st.LeaseRenewUIDConflicts > 0 {
			fakestate.Append(dir, fakestate.EventsFileName, "lease.renew-uid-conflict")
			conflictMessage("the UID in the precondition does not match the UID in record")
			return 1
		}
		if st.LeaseReplaceTimeouts > 0 {
			st.LeaseReplaceTimeouts--
			fakestate.Append(dir, fakestate.EventsFileName, "lease.replace-timeout")
			fmt.Fprintln(os.Stderr, "Unable to connect to the server: net/http: request canceled (Client.Timeout exceeded while awaiting headers)")
			return 1
		}
		if st.LeaseReplaceFailures > 0 {
			st.LeaseReplaceFailures--
			fakestate.Append(dir, fakestate.EventsFileName, "lease.replace-error")
			fmt.Fprintln(os.Stderr, `Error from server (InternalError): an error on the server ("injected lease failure") has prevented the request from succeeding`)
			return 1
		}
		if st.LeaseReplaceConflicts > 0 {
			st.LeaseReplaceConflicts--
			fakestate.Append(dir, fakestate.EventsFileName, "lease.conflict")
			conflictMessage("the object has been modified; please apply your changes to the latest version and try again")
			return 1
		}
		if st.LeaseReplaceUIDConflicts > 0 {
			st.LeaseReplaceUIDConflicts--
			fakestate.Append(dir, fakestate.EventsFileName, "lease.uid-conflict")
			conflictMessage("the UID in the precondition does not match the UID in record")
			return 1
		}
		// 只作用于迁移记录写入：确定性验证「重试时重读并保留最新记录」（AUD-D2）。
		if filepath.Base(path) == "lease-record.yaml" && st.LeaseRecordConflicts > 0 {
			st.LeaseRecordConflicts--
			fakestate.Append(dir, fakestate.EventsFileName, "lease.record-conflict")
			conflictMessage("the object has been modified; please apply your changes to the latest version and try again")
			return 1
		}
		// UID 校验（T2-06.1.1）：真实 API Server 会拒绝 uid 与现存对象不一致的 PUT，
		// 这正是「同名 Lease 被删除重建」时旧持有者不能写入的原因。
		if manifest.Metadata.UID != "" && manifest.Metadata.UID != st.Lease.UID {
			fakestate.Append(dir, fakestate.EventsFileName, "lease.uid-conflict")
			conflictMessage("the UID in the precondition does not match the UID in record")
			return 1
		}
		if manifest.Metadata.ResourceVersion != st.Lease.ResourceVersion {
			fakestate.Append(dir, fakestate.EventsFileName, "lease.conflict")
			conflictMessage("the object has been modified; please apply your changes to the latest version and try again")
			return 1
		}

		previous := st.Lease.HolderIdentity
		st.Lease.HolderIdentity = manifest.Spec.HolderIdentity
		st.Lease.AcquireTime = manifest.Spec.AcquireTime
		st.Lease.RenewTime = manifest.Spec.RenewTime
		st.Lease.DurationSeconds = manifest.Spec.LeaseDurationSeconds
		st.Lease.Transitions = manifest.Spec.LeaseTransitions
		// PUT 覆盖整个对象：注解必须整体回写，否则记录丢失。
		st.Lease.Annotations = manifest.Metadata.Annotations
		st.Lease.ResourceVersion = nextResourceVersion(st.Lease.ResourceVersion)

		// 确定性注入：记录写入成功后锁立刻被别的 Runner 接管。
		if state := manifest.Metadata.Annotations[annMigrationState]; state != "" && st.LeaseStealAfterState == state {
			st.LeaseStealAfterState = ""
			holder := st.Lease.HolderIdentity
			st.Lease.HolderIdentity = st.LeaseStealHolder
			st.Lease.ResourceVersion = nextResourceVersion(st.Lease.ResourceVersion)
			fakestate.Append(dir, fakestate.EventsFileName, "lease.stolen-after-state state="+state+" previous="+holder)
		}

		recordWriteEvent(dir, path, manifest.Metadata.Annotations, previous)
		switch {
		case manifest.Spec.HolderIdentity == "":
			fakestate.Append(dir, fakestate.EventsFileName, "lease.release holder="+previous)
		case manifest.Spec.HolderIdentity == previous:
			fakestate.Append(dir, fakestate.EventsFileName, "lease.renew holder="+previous)
		default:
			st.LeaseAcquisitions++
			fakestate.Append(dir, fakestate.EventsFileName, "lease.acquire holder="+manifest.Spec.HolderIdentity+" previous="+previous)
		}
		fmt.Printf("lease.coordination.k8s.io/%s replaced\n", lockName)
		return 0
	})
}

func nextResourceVersion(current string) string {
	var value int
	if _, err := fmt.Sscanf(current, "%d", &value); err != nil {
		return current + "1"
	}
	return fmt.Sprintf("%d", value+1)
}

// recordWriteEvent 记录每次 Lease 写入「携带了什么迁移记录」。
//
// 这是 AUD-D2 的可观测性来源：测试可以逐次断言 renew/release/takeover 没有把记录
// 写成默认值（例如 NO_MIGRATION + 空 job），而不是只看最终状态 —— 最终状态可能被
// 后续一次正确的写入掩盖掉中间的丢失。
func recordWriteEvent(dir, path string, ann map[string]string, previousHolder string) {
	const prefix = "deploy-lock.surgecart.io/"
	fakestate.Append(dir, fakestate.EventsFileName, fmt.Sprintf(
		"lease.write kind=%s state=%s job=%s uid=%s prev=%s previous=%s",
		filepath.Base(path),
		ann[prefix+"migration-state"],
		ann[prefix+"migration-job"],
		ann[prefix+"migration-job-uid"],
		ann[prefix+"migration-previous-state"],
		previousHolder,
	))
}

func cmdApply(dir string, inv invocation) int {
	path := fileFlag(inv)
	if path == "" {
		fmt.Fprintln(os.Stderr, "fake kubectl: apply 缺少 -f")
		return 1
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	kind := classifyManifest(string(raw))
	return mutate(dir, func(st *fakestate.State) int {
		if st.ApplyFailures[kind] {
			fakestate.Append(dir, fakestate.EventsFileName, "apply.error "+kind)
			fmt.Fprintf(os.Stderr, `Error from server (InternalError): an error on the server ("injected apply failure for %s") has prevented the request from succeeding`+"\n", kind)
			return 1
		}
		// 「API 返回错误但对象已创建」：脚本不能靠 apply 的退出码判断是否创建。
		if st.ApplyErrorsApplied[kind] {
			fakestate.Append(dir, fakestate.EventsFileName, "apply.error-but-created "+kind)
			applyKind(st, kind)
			fmt.Fprintf(os.Stderr, `Error from server (Timeout): the server was unable to return a response in the time allotted, but the request may have been processed`+"\n")
			return 1
		}
		fakestate.Append(dir, fakestate.EventsFileName, "apply."+kind)
		applyKind(st, kind)
		fmt.Printf("%s applied\n", kind)
		return 0
	})
}

func applyKind(st *fakestate.State, kind string) {
	switch kind {
	case "application":
		st.Applied = true
		st.DeploymentGets = 0
	case "namespace":
		st.NamespaceExists = true
	case "migration":
		// 迁移已发起：后续第一次 get lease 可能触发「检查后被接管」的注入。
		st.MigrationApplied = true
		// 迁移 Job 进入命名空间：后续 Runner 的「是否存在未终结迁移」扫描必须能看到它。
		// 用脚本序列的最后一个快照代表它的最终状态。
		snapshot := st.Job
		if len(st.JobScript) > 0 {
			snapshot = st.JobScript[len(st.JobScript)-1]
		}
		snapshot.Exists = true
		if snapshot.Name == "" {
			snapshot.Name = "surgecart-migrate"
		}
		st.Jobs = append(st.Jobs, snapshot)
	}
}

// classifyManifest 按清单内容判断类型：发布脚本用「先迁移后应用」的顺序，
// fake 必须能区分两者才能验证「迁移失败时应用清单没有被 apply」。
func classifyManifest(raw string) string {
	hasJob, hasDeployment := false, false
	for _, line := range strings.Split(raw, "\n") {
		switch strings.TrimSpace(line) {
		case "kind: Job":
			hasJob = true
		case "kind: Deployment":
			hasDeployment = true
		}
	}
	switch {
	case hasJob:
		return "migration"
	case hasDeployment:
		return "application"
	default:
		return "namespace"
	}
}

func fileFlag(inv invocation) string { return inv.Filename }

// ---------------------------------------------------------------------------
// get
// ---------------------------------------------------------------------------

func cmdGet(dir string, inv invocation) int {
	if len(inv.Positional) < 2 {
		fmt.Fprintln(os.Stderr, "fake kubectl: get 缺少资源类型")
		return 1
	}
	switch inv.Positional[1] {
	case "namespace":
		return cmdGetNamespace(dir)
	case "secret":
		return cmdGetSecret(dir, inv)
	case "lease":
		return cmdGetLease(dir, inv)
	case "job":
		return cmdGetJob(dir, inv)
	case "jobs":
		return cmdGetJobs(dir, inv)
	case "deployment":
		return cmdGetDeployment(dir, inv)
	case "pods":
		return cmdGetPods(dir, inv)
	case "deployment,service,pod":
		fmt.Println("deployment.apps/surgecart   1/1   1   1   10s")
		fmt.Println("service/surgecart           ClusterIP   10.0.0.10   <none>   8000/TCP   10s")
		return 0
	default:
		fmt.Fprintf(os.Stderr, "fake kubectl: 未实现的 get %q\n", inv.Positional[1])
		return 1
	}
}

func cmdGetNamespace(dir string) int {
	st, err := fakestate.Load(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake kubectl: %v\n", err)
		return 97
	}
	if !st.NamespaceExists {
		fmt.Fprintf(os.Stderr, "Error from server (NotFound): namespaces %q not found\n", namespace)
		return 1
	}
	fmt.Printf("NAME        STATUS   AGE\n%s      Active   10d\n", namespace)
	return 0
}

func cmdGetSecret(dir string, inv invocation) int {
	if len(inv.Positional) < 3 {
		fmt.Fprintln(os.Stderr, "fake kubectl: get secret 缺少名称")
		return 1
	}
	name := inv.Positional[2]
	st, err := fakestate.Load(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake kubectl: %v\n", err)
		return 97
	}
	for _, secret := range st.Secrets {
		if secret == name {
			fmt.Printf("NAME%s\n", strings.ToUpper(name))
			return 0
		}
	}
	fmt.Fprintf(os.Stderr, "Error from server (NotFound): secrets %q not found\n", name)
	return 1
}

func cmdGetLease(dir string, inv invocation) int {
	return mutate(dir, func(st *fakestate.State) int {
		st.LeaseGets++
		if st.LeaseGetTimeouts > 0 {
			st.LeaseGetTimeouts--
			fakestate.Append(dir, fakestate.EventsFileName, "lease.get-timeout")
			fmt.Fprintln(os.Stderr, "Unable to connect to the server: net/http: request canceled (Client.Timeout exceeded while awaiting headers)")
			return 1
		}
		if st.LeaseGetFailures > 0 {
			st.LeaseGetFailures--
			fakestate.Append(dir, fakestate.EventsFileName, "lease.get-error")
			fmt.Fprintln(os.Stderr, `Error from server (InternalError): an error on the server ("injected lease read failure") has prevented the request from succeeding`)
			return 1
		}
		if st.Lease == nil {
			fmt.Fprintf(os.Stderr, "Error from server (NotFound): leases.coordination.k8s.io %q not found\n", lockName)
			return 1
		}
		if st.LeaseStealAfterGets > 0 && st.LeaseGets >= st.LeaseStealAfterGets {
			st.LeaseStealAfterGets = 0
			stealLease(st)
		}
		return render(inv.Output, leaseObject(st.Lease), leaseTable(st.Lease))
	})
}

func stealLease(st *fakestate.State) {
	st.Lease.HolderIdentity = st.LeaseStealHolder
	st.Lease.ResourceVersion = nextResourceVersion(st.Lease.ResourceVersion)
	fakestate.Append(fakestate.Dir(), fakestate.EventsFileName, "lease.stolen holder="+st.LeaseStealHolder)
}

func jobNameFor(explicit, requested string) string {
	if explicit != "" {
		return explicit
	}
	if requested != "" {
		return requested
	}
	return "surgecart-migrate"
}

// jobUIDFor 决定 Job 的 uid：显式值 > 状态里已有对象的 uid > 按名称派生的稳定值。
// JobScript 模拟的是**同一个对象**的状态变化，uid 必须保持不变，否则脚本的
// 「同名 Job 是否被删除重建」判断会被测试替身自己打乱。
func jobUIDFor(explicit, fallback, name string) string {
	if explicit != "" {
		return explicit
	}
	if fallback != "" {
		return fallback
	}
	return "uid-" + name
}

func cmdGetJob(dir string, inv invocation) int {
	requested := ""
	if len(inv.Positional) >= 3 {
		requested = inv.Positional[2]
	}
	return mutate(dir, func(st *fakestate.State) int {
		if st.JobGetFailAt > 0 && st.JobGets+1 == st.JobGetFailAt {
			st.JobGetFailAt = 0
			fakestate.Append(dir, fakestate.EventsFileName, "job.get-error")
			fmt.Fprintln(os.Stderr, `Error from server (InternalError): an error on the server ("injected job read failure") has prevented the request from succeeding`)
			return 1
		}
		if st.JobGetTimeouts > 0 {
			st.JobGetTimeouts--
			fakestate.Append(dir, fakestate.EventsFileName, "job.get-timeout")
			fmt.Fprintln(os.Stderr, "Unable to connect to the server: net/http: request canceled (Client.Timeout exceeded while awaiting headers)")
			return 1
		}
		if st.JobGetFailures > 0 {
			st.JobGetFailures--
			fakestate.Append(dir, fakestate.EventsFileName, "job.get-error")
			fmt.Fprintln(os.Stderr, `Error from server (InternalError): an error on the server ("injected job read failure") has prevented the request from succeeding`)
			return 1
		}
		job := st.Job
		if len(st.JobScript) > 0 {
			index := st.JobGets
			if index >= len(st.JobScript) {
				index = len(st.JobScript) - 1
			}
			job = st.JobScript[index]
		}
		st.JobGets++
		if !job.Exists {
			fmt.Fprintf(os.Stderr, "Error from server (NotFound): jobs.batch %q not found\n", requested)
			return 1
		}
		return render(inv.Output, jobObject(job, requested, st.Job.UID), jobTable(job))
	})
}

func cmdGetJobs(dir string, inv invocation) int {
	return mutate(dir, func(st *fakestate.State) int {
		if st.JobsListTimeouts > 0 {
			st.JobsListTimeouts--
			fakestate.Append(dir, fakestate.EventsFileName, "jobs.list-timeout")
			fmt.Fprintln(os.Stderr, "Unable to connect to the server: net/http: request canceled (Client.Timeout exceeded while awaiting headers)")
			return 1
		}
		if st.JobsListFailures > 0 {
			st.JobsListFailures--
			fakestate.Append(dir, fakestate.EventsFileName, "jobs.list-error")
			fmt.Fprintln(os.Stderr, `Error from server (InternalError): an error on the server ("injected job list failure") has prevented the request from succeeding`)
			return 1
		}
		// 只返回「集群里真实存在的 Job」：st.Job / JobScript 是脚本按名称读取的
		// 状态序列，不代表对象已经存在（apply 之前它必须不在列表里）。
		jobs := st.Jobs
		items := make([]any, 0, len(jobs))
		for _, job := range jobs {
			items = append(items, jobObject(job, job.Name, st.Job.UID))
		}
		return render(inv.Output, map[string]any{
			"apiVersion": "batch/v1",
			"kind":       "JobList",
			"items":      items,
		}, jobsTable(jobs))
	})
}

func cmdGetDeployment(dir string, inv invocation) int {
	return mutate(dir, func(st *fakestate.State) int {
		// AUD-09：命名空间/鉴权类错误必须原样暴露，不能被脚本当成「Deployment 不存在」。
		if st.DeploymentGetError != "" {
			fakestate.Append(dir, fakestate.EventsFileName, "deployment.get-error")
			fmt.Fprintln(os.Stderr, st.DeploymentGetError)
			return 1
		}
		deployment := st.Deployment
		if st.Applied && len(st.DeploymentAfterApply) > 0 {
			index := st.DeploymentGets
			if index >= len(st.DeploymentAfterApply) {
				index = len(st.DeploymentAfterApply) - 1
			}
			deployment = st.DeploymentAfterApply[index]
			st.DeploymentGets++
		}
		if !deployment.Exists {
			fmt.Fprintf(os.Stderr, "Error from server (NotFound): deployments.apps %q not found\n", "surgecart")
			return 1
		}
		return render(inv.Output, deploymentObject(deployment), deploymentTable(deployment))
	})
}

func cmdGetPods(dir string, inv invocation) int {
	return mutate(dir, func(st *fakestate.State) int {
		isJobPods := strings.Contains(inv.Selector, "job-name=")
		isMigrationPods := strings.Contains(inv.Selector, "database-migration")
		if isMigrationPods && !isJobPods {
			if st.MigrationPodsGetTimeouts > 0 {
				st.MigrationPodsGetTimeouts--
				fakestate.Append(dir, fakestate.EventsFileName, "migration-pods.get-timeout")
				fmt.Fprintln(os.Stderr, "Unable to connect to the server: net/http: request canceled (Client.Timeout exceeded while awaiting headers)")
				return 1
			}
			if st.MigrationPodsGetFailures > 0 {
				st.MigrationPodsGetFailures--
				fakestate.Append(dir, fakestate.EventsFileName, "migration-pods.get-error")
				fmt.Fprintln(os.Stderr, `Error from server (InternalError): an error on the server ("injected migration pod list failure") has prevented the request from succeeding`)
				return 1
			}
		}
		if isJobPods {
			if st.PodsGetTimeouts > 0 {
				st.PodsGetTimeouts--
				fakestate.Append(dir, fakestate.EventsFileName, "pods.get-timeout")
				fmt.Fprintln(os.Stderr, "Unable to connect to the server: net/http: request canceled (Client.Timeout exceeded while awaiting headers)")
				return 1
			}
			if st.PodsGetFailures > 0 {
				st.PodsGetFailures--
				fakestate.Append(dir, fakestate.EventsFileName, "pods.get-error")
				fmt.Fprintln(os.Stderr, `Error from server (InternalError): an error on the server ("injected pod list failure") has prevented the request from succeeding`)
				return 1
			}
		}
		var pods []fakestate.Pod
		ownerUID := ""
		switch {
		case isJobPods:
			jobName := selectorValue(inv.Selector, "job-name")
			if explicit, ok := st.JobPods[jobName]; ok {
				pods = explicit
			} else {
				pods = st.Job.Pods
			}
			ownerUID = jobUIDForSelector(st, jobName)
		case isMigrationPods:
			pods = st.MigrationPods
		case st.Applied && len(st.DeploymentAfterApply) > 0:
			index := st.DeploymentGets
			if index >= len(st.DeploymentAfterApply) {
				index = len(st.DeploymentAfterApply) - 1
			}
			pods = st.DeploymentAfterApply[index].Pods
		default:
			pods = st.Deployment.Pods
		}
		return render(inv.Output, podsListObject(pods, ownerUID), podsTable(pods))
	})
}

// selectorValue 取 `k=v,k2=v2` 中指定 key 的值。
func selectorValue(selector, key string) string {
	for _, part := range strings.Split(selector, ",") {
		if strings.HasPrefix(part, key+"=") {
			return strings.TrimPrefix(part, key+"=")
		}
	}
	return ""
}

// jobUIDForSelector 返回「按名称读取 Pod 时该 Job 应有的 uid」：
// 优先在已存在的 Job 列表里查找，其次用按名称读取的 Job 快照，最后按名称派生。
func jobUIDForSelector(st *fakestate.State, name string) string {
	for _, job := range st.Jobs {
		if jobNameFor(job.Name, "") == name {
			return jobUIDFor(job.UID, "", name)
		}
	}
	if jobNameFor(st.Job.Name, "") == name || st.Job.Name == "" {
		return jobUIDFor(st.Job.UID, "", name)
	}
	return "uid-" + name
}

func cmdLogs(dir string) int {
	st, err := fakestate.Load(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake kubectl: %v\n", err)
		return 97
	}
	if st.Job.Logs == "" {
		fmt.Println("(fake kubectl: 该 Job 没有预置日志)")
		return 0
	}
	fmt.Print(st.Job.Logs)
	if !strings.HasSuffix(st.Job.Logs, "\n") {
		fmt.Println()
	}
	return 0
}

// ---------------------------------------------------------------------------
// 对象构造与渲染
// ---------------------------------------------------------------------------

func render(output string, object any, table string) int {
	if strings.HasPrefix(output, "go-template=") {
		return renderTemplate(strings.TrimPrefix(output, "go-template="), object)
	}
	fmt.Print(table)
	return 0
}

func renderTemplate(expr string, object any) int {
	tmpl, err := template.New("output").Parse(expr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: error parsing template {{%s}}: %v\n", expr, err)
		return 1
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, object); err != nil {
		fmt.Fprintf(os.Stderr, "error: error executing template {{%s}}: %v\n", expr, err)
		return 1
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		buf.WriteString("\n")
	}
	os.Stdout.Write(buf.Bytes())
	return 0
}

func leaseObject(lease *fakestate.Lease) map[string]any {
	metadata := map[string]any{
		"name":            lockName,
		"namespace":       namespace,
		"resourceVersion": lease.ResourceVersion,
		"uid":             lease.UID,
	}
	if lease.Annotations != nil {
		metadata["annotations"] = lease.Annotations
	}
	return map[string]any{
		"apiVersion": "coordination.k8s.io/v1",
		"kind":       "Lease",
		"metadata":   metadata,
		"spec": map[string]any{
			"holderIdentity":       lease.HolderIdentity,
			"acquireTime":          lease.AcquireTime,
			"renewTime":            lease.RenewTime,
			"leaseDurationSeconds": lease.DurationSeconds,
			"leaseTransitions":     lease.Transitions,
		},
	}
}

func leaseTable(lease *fakestate.Lease) string {
	return fmt.Sprintf("NAME\tHOLDER\tAGE\n%s\t%s\n", lockName, lease.HolderIdentity)
}

// deploymentObject 只包含脚本读取的字段；omitempty 语义与真实 API Server 一致：
// 0 值字段不出现，因此脚本必须能处理「字段缺失」而不是把缺失当失败。
func deploymentObject(deployment fakestate.Deployment) map[string]any {
	status := map[string]any{}
	if deployment.ObservedGeneration != 0 {
		status["observedGeneration"] = deployment.ObservedGeneration
	}
	if deployment.StatusReplicas != 0 {
		status["replicas"] = deployment.StatusReplicas
	}
	if deployment.UpdatedReplicas != 0 {
		status["updatedReplicas"] = deployment.UpdatedReplicas
	}
	if deployment.AvailableReplicas != 0 {
		status["availableReplicas"] = deployment.AvailableReplicas
	}
	if deployment.UnavailableReplicas != 0 {
		status["unavailableReplicas"] = deployment.UnavailableReplicas
	}

	object := map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":       "surgecart",
			"namespace":  namespace,
			"generation": deployment.Generation,
		},
		"spec": map[string]any{
			"replicas": deployment.Replicas,
			"template": map[string]any{
				"metadata": map[string]any{
					"labels": map[string]any{"app.kubernetes.io/version": deployment.Version},
				},
				"spec": map[string]any{
					"containers": []any{map[string]any{"name": "app", "image": deployment.Image}},
				},
			},
		},
	}
	if len(status) > 0 {
		object["status"] = status
	}
	return object
}

func deploymentTable(deployment fakestate.Deployment) string {
	return fmt.Sprintf("NAME\tREADY\tUP-TO-DATE\tAVAILABLE\tAGE\nsurgecart\t%d/%d\t%d\t%d\t1m\n",
		deployment.AvailableReplicas, deployment.Replicas, deployment.UpdatedReplicas, deployment.AvailableReplicas)
}

func jobObject(job fakestate.Job, requested, defaultUID string) map[string]any {
	name := jobNameFor(job.Name, requested)
	conditions := []any{}
	if job.Complete {
		conditions = append(conditions, map[string]any{"type": "Complete", "status": "True"})
	}
	if job.Failed {
		conditions = append(conditions, map[string]any{
			"type":    "Failed",
			"status":  "True",
			"reason":  job.Reason,
			"message": job.Message,
		})
	}
	metadata := map[string]any{
		"name":      name,
		"namespace": namespace,
		"uid":       jobUIDFor(job.UID, defaultUID, name),
		"labels": map[string]any{
			"app.kubernetes.io/component": "database-migration",
		},
	}
	if job.Deleting {
		metadata["deletionTimestamp"] = "2026-11-07T00:00:00Z"
	}
	object := map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata":   metadata,
		"spec":       map[string]any{"parallelism": 1, "completions": 1},
	}
	if len(conditions) > 0 {
		object["status"] = map[string]any{"conditions": conditions}
	}
	return object
}

func jobTable(job fakestate.Job) string {
	state := "Running"
	switch {
	case job.Complete:
		state = "Complete"
	case job.Failed:
		state = "Failed"
	case job.Deleting:
		state = "Terminating"
	}
	return fmt.Sprintf("NAME\tSTATUS\tCOMPLETIONS\tAGE\nsurgecart-migrate\t%s\t1/1\t1m\n", state)
}

func jobsTable(jobs []fakestate.Job) string {
	var builder strings.Builder
	builder.WriteString("NAME\tSTATUS\tCOMPLETIONS\tAGE\n")
	for _, job := range jobs {
		state := "Running"
		switch {
		case job.Complete:
			state = "Complete"
		case job.Failed:
			state = "Failed"
		case job.Deleting:
			state = "Terminating"
		}
		fmt.Fprintf(&builder, "%s\t%s\t1/1\t1m\n", jobNameFor(job.Name, ""), state)
	}
	return builder.String()
}

// podContainers 返回 Pod 的容器状态：Containers 非空时按它渲染（可构造 sidecar），
// 否则退化为单个 app 容器。
func podContainers(pod fakestate.Pod) []fakestate.Container {
	if len(pod.Containers) > 0 {
		return pod.Containers
	}
	return []fakestate.Container{{Name: "app", Image: pod.Image, Ready: pod.Ready}}
}

func podsListObject(pods []fakestate.Pod, ownerJobUID string) map[string]any {
	items := make([]any, 0, len(pods))
	for _, pod := range pods {
		metadata := map[string]any{
			"name":   pod.Name,
			"labels": map[string]any{"app.kubernetes.io/version": pod.Version},
		}
		if pod.Deleting {
			metadata["deletionTimestamp"] = "2026-11-07T00:00:00Z"
		}
		// AUD-D3：迁移 Pod 必须带 Job ownerReferences，脚本据此判断归属；
		// Orphan / OwnerJobUID 用于构造「无法归属」和「属于另一个 Job」的干扰。
		if !pod.Orphan {
			uid := ownerJobUID
			if pod.OwnerJobUID != "" {
				uid = pod.OwnerJobUID
			}
			if uid != "" {
				metadata["ownerReferences"] = []any{map[string]any{
					"apiVersion": "batch/v1",
					"kind":       "Job",
					"name":       "surgecart-migrate",
					"uid":        uid,
					"controller": true,
				}}
			}
		}
		status := map[string]any{"phase": pod.Phase}
		specContainers := make([]any, 0, 2)
		// Pending 且未启动容器时没有 containerStatuses：脚本必须按「未 Ready」处理。
		if pod.Phase != "Pending" {
			statuses := make([]any, 0, 2)
			for _, container := range podContainers(pod) {
				statuses = append(statuses, map[string]any{
					"name": container.Name, "image": container.Image, "ready": container.Ready,
				})
			}
			status["containerStatuses"] = statuses
		}
		for _, container := range podContainers(pod) {
			specContainers = append(specContainers, map[string]any{
				"name": container.Name, "image": container.Image,
			})
		}
		items = append(items, map[string]any{
			"metadata": metadata,
			"status":   status,
			"spec":     map[string]any{"containers": specContainers},
		})
	}
	return map[string]any{"apiVersion": "v1", "kind": "List", "items": items}
}

func podsTable(pods []fakestate.Pod) string {
	var builder strings.Builder
	builder.WriteString("NAME\tREADY\tSTATUS\tRESTARTS\tAGE\n")
	for _, pod := range pods {
		ready := "0/1"
		if pod.Ready {
			ready = "1/1"
		}
		fmt.Fprintf(&builder, "%s\t%s\t%s\t0\t1m\n", pod.Name, ready, pod.Phase)
	}
	return builder.String()
}
