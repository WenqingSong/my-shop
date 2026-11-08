// fake kubectl —— T2-06.1 / T2-06.1.1 / T2-06.1.4 发布脚本行为测试的执行替身。
//
// 它不是 kubectl 的完整实现，只忠实实现 scripts/deploy-k8s.sh 真正依赖的语义：
//
//	version                               集群可达性
//	kustomize <path>                      输出预置清单（migration.yaml / application.yaml）
//	auth can-i <verb> <resource>          权限预检（走唯一的 fakestate.Authorize）
//	create -f <Lease>                     原子创建（同名只能成功一次），持久化注解
//	replace -f <Lease>                    PUT + resourceVersion/UID 校验的 CAS，持久化注解
//	delete lease <name>                   删除 Lease（--ignore-not-found / -o name）
//	apply -f <manifest>                   按解析出的 Kind 走 CREATE/UPDATE 语义
//	get namespace|secret|lease|job|jobs|deployment|pods
//	logs | describe | wait                只输出文本，供失败 runbook 使用
//
// T2-06.1.4 的语义变化（审计 C-1/C-3/C-9/C-10/C-13/C-14、A-5）：
//   - 操作类型**不再按 YAML 文件名判断**：apply 解析每个文档的 Kind，Lease 写入按
//     清单内容推导 intent（acquire/renew/release/record）；文件名只出现在事件日志里；
//   - 故障注入匹配**调用语义**（verb/group/resource/namespace/name/selector/intent），
//     由 fakestate.Injection 统一执行；旧的扁平字段是它的薄封装；
//   - 所有写路径先做 RBAC 授权（无规则即 403），并且都经同一个 CAS helper
//     （fakestate.CommitLeaseUpdate）比较 resourceVersion / uid；
//   - resourceVersion 全局单调（fakestate.State.NextResourceVersion）；
//   - 注入型 409 一定先推进对象版本（模拟「别人先写了」），因此用同一个 rv 立即重试
//     必然继续失败 —— 否则 CAS 冲突不可证伪（C-1）；
//   - 先落盘提交、后返回成功（C-10）：ack 输出发生在状态落盘之后。
//
// 关键点：并发 Runner 的 create/replace 用 flock 串行化，因此「两个 Runner 同时申请锁
// 只有一个成功」在**脚本行为**层面是真实竞争出来的，不是预设结论。
// 但 fake 无法证明真实 API Server 的并发语义（etcd 事务、watch 缓存、多副本 apiserver），
// 那部分始终是 NOT_VERIFIED。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

const (
	lockName  = "surgecart-deploy-lock"
	namespace = "surgecart"
	// 迁移生命周期记录的状态注解键（与 scripts/deploy-k8s.sh 的数据契约一致）。
	annMigrationState = fakestate.AnnotationMigrationState
	// 迁移记录注解的公共前缀：用于区分「续租」与「写记录」两种写意图。
	recordAnnotationPrefix = "deploy-lock.surgecart.io/"
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

// mutateAck 在文件锁内读取-修改-写回状态，然后才执行 ack（打印成功输出）。
//
// C-10（ACK-before-commit）：成功输出必须在**落盘之后**。旧的 mutate 在 fn 内部
// fmt.Printf("... created")，随后 Save 才执行；如果进程在 Save 之前崩溃，调用方已经
// 看到了「成功」，但集群里没有对象 —— 这正是要消除的「已 ack 未生效」。
func mutateAck(dir string, fn func(*fakestate.State) (int, func() int)) int {
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
	code, ack := fn(st)
	if err := fakestate.Save(dir, st); err != nil {
		fmt.Fprintf(os.Stderr, "fake kubectl: 写回状态失败: %v\n", err)
		return 97
	}
	if code == 0 && ack != nil {
		return ack()
	}
	return code
}

// ---------------------------------------------------------------------------
// 参数解析
// ---------------------------------------------------------------------------

type invocation struct {
	Namespace      string
	Output         string
	Selector       string
	Filename       string
	Subresource    string
	AllNamespaces  bool
	IgnoreNotFound bool
	Positional     []string
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
		case arg == "-A" || arg == "--all-namespaces" || arg == "--all-namespaces=true":
			inv.AllNamespaces = true
		case arg == "--ignore-not-found" || arg == "--ignore-not-found=true":
			inv.IgnoreNotFound = true
		case arg == "--subresource":
			inv.Subresource = next()
		case strings.HasPrefix(arg, "--subresource="):
			inv.Subresource = strings.TrimPrefix(arg, "--subresource=")
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
	case "delete":
		return cmdDelete(dir, inv)
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
	// kustomize 的路径决定**清单来源**（migration 还是 application），这是构建输入，
	// 不是「操作类型判断」：真正的操作类型由 apply 解析出的 Kind 决定。
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

// cmdAuth 实现 `kubectl auth can-i <verb> <resource> [-n ns] [name]`，判定一律走
// fakestate.Authorize（唯一入口）。这保证「删除 RBAC 任一 (verb,resource) →
// 预检失败 → 行为测试失败」这条链真的成立（审计 A-5 / C-8）。
func cmdAuth(dir string, inv invocation) int {
	if len(inv.Positional) < 3 {
		fmt.Fprintln(os.Stderr, "fake kubectl: auth can-i 参数不足")
		return 1
	}
	verb := inv.Positional[2]
	resourceArg := ""
	name := ""
	if len(inv.Positional) >= 4 {
		resourceArg = inv.Positional[3]
	}
	if len(inv.Positional) >= 5 {
		name = inv.Positional[4]
	}
	if inv.Subresource != "" {
		resourceArg = strings.TrimSuffix(resourceArg, "/"+inv.Subresource) + "/" + inv.Subresource
	}
	group, resource := fakestate.SplitResource(resourceArg)
	if inv.Namespace == "" && !inv.AllNamespaces {
		inv.Namespace = namespace
	}
	if inv.AllNamespaces {
		inv.Namespace = ""
	}
	return mutateAck(dir, func(st *fakestate.State) (int, func() int) {
		decision, err := fakestate.Authorize(st, verb, group, resource, inv.Namespace, name)
		switch decision {
		case fakestate.DecisionAllowed:
			fakestate.Append(dir, fakestate.EventsFileName, "auth.allowed "+verb+" "+resource)
			return 0, func() int { fmt.Println("yes"); return 0 }
		case fakestate.DecisionUnknown:
			fakestate.Append(dir, fakestate.EventsFileName, "auth.error "+verb+" "+resource)
			fmt.Fprintln(os.Stderr, err)
			return 1, nil
		default:
			fakestate.Append(dir, fakestate.EventsFileName, "auth.denied "+verb+" "+resource)
			fmt.Println("no")
			return 1, nil
		}
	})
}

// ---------------------------------------------------------------------------
// 授权与错误渲染
// ---------------------------------------------------------------------------

// resourceLabel 返回与真实 kubectl 一致的资源全名（错误信息用）。
func resourceLabel(call fakestate.Invocation) string {
	switch call.Resource {
	case "leases":
		return "leases.coordination.k8s.io"
	case "jobs":
		return "jobs.batch"
	case "deployments":
		return "deployments.apps"
	case "namespaces":
		return "namespaces"
	case "pods":
		return "pods"
	case "secrets":
		return "secrets"
	case "configmaps":
		return "configmaps"
	case "services":
		return "services"
	default:
		return call.Resource
	}
}

// authorizeWrite 是**所有写路径**的统一授权检查（create / update / patch / delete / apply）。
//
// verbs 按顺序尝试：真实 kubectl apply 对已存在的对象发 **PATCH**（不是 PUT/update），
// 但历史上也允许用 update 授权；因此 apply 的已存在分支传 ("patch", "update")，
// 任一允许即放行 —— 既贴近真实动词，又不逼着调用方过度授权。
//
// 返回 0 表示允许；非 0 表示已经打印错误，调用方必须放弃写入（不得有任何状态变更）。
func authorizeWrite(dir string, st *fakestate.State, call fakestate.Invocation, verbs ...string) int {
	var decision fakestate.Decision
	var err error
	for _, verb := range verbs {
		decision, err = fakestate.Authorize(st, verb, call.Group, call.Resource, call.Namespace, call.Name)
		if decision == fakestate.DecisionAllowed {
			return 0
		}
	}
	if len(verbs) == 0 {
		return 0
	}
	primary := verbs[0]
	switch decision {
	case fakestate.DecisionUnknown:
		fakestate.Append(dir, fakestate.EventsFileName, "auth.error "+primary+" "+call.Resource)
		fmt.Fprintf(os.Stderr, "Error from server: %v\n", err)
		return 1
	default:
		fakestate.Append(dir, fakestate.EventsFileName, "auth.forbidden "+primary+" "+call.Resource)
		fmt.Fprintf(os.Stderr,
			"Error from server (Forbidden): %s is forbidden: User \"ci-deployer\" cannot %s resource %q in API group %q in the namespace %q\n",
			resourceLabel(call), primary, call.Resource, call.Group, call.Namespace)
		return 1
	}
}

// executeInjection 是**唯一**的注入动作执行点（延迟 / 错误 / 并发接管 / 删除）。
//
// 返回非 0 表示请求应当以该退出码失败（错误已经打印）。
func executeInjection(dir string, st *fakestate.State, inj *fakestate.Injection, call fakestate.Invocation) int {
	if inj.DelayMS > 0 {
		time.Sleep(time.Duration(inj.DelayMS) * time.Millisecond)
	}
	if inj.Event != "" && inj.Takeover == "" && !inj.Delete {
		fakestate.Append(dir, fakestate.EventsFileName, inj.Event)
	}
	if inj.Takeover != "" {
		previous := ""
		if st.Lease != nil {
			previous = st.Lease.HolderIdentity
		}
		if err := fakestate.TakeoverLease(st, inj.Takeover); err == nil {
			if inj.Event == "lease.stolen-after-state" {
				fakestate.Append(dir, fakestate.EventsFileName,
					"lease.stolen-after-state state="+call.Annotations[annMigrationState]+" previous="+previous)
			} else {
				fakestate.Append(dir, fakestate.EventsFileName,
					"lease.stolen holder="+inj.Takeover+" rv="+st.Lease.ResourceVersion)
			}
		}
	}
	if inj.Delete {
		if fakestate.DeleteLease(st) {
			fakestate.Append(dir, fakestate.EventsFileName, "lease.deleted-injected")
		}
	}
	if inj.Status != 0 {
		// C-1：注入型 409 必须体现「其他写者已经推进了版本」。否则调用方用同一个
		// resourceVersion 立即重试就会成功，「CAS 冲突」永远不可证伪。
		if inj.Status == 409 && inj.Takeover == "" && st.Lease != nil && call.Verb != "get" && call.Verb != "list" {
			st.Lease.ResourceVersion = st.NextResourceVersion()
			fakestate.Append(dir, fakestate.EventsFileName, "lease.version-advanced rv="+st.Lease.ResourceVersion)
		}
		return printInjectionError(dir, inj, call)
	}
	return 0
}

// printInjectionError 把注入的 HTTP 状态码渲染成与真实 kubectl 同形的错误。
func printInjectionError(dir string, inj *fakestate.Injection, call fakestate.Invocation) int {
	label := resourceLabel(call)
	switch inj.Status {
	case 409:
		message := inj.Message
		if message == "" {
			message = "the object has been modified; please apply your changes to the latest version and try again"
		}
		fmt.Fprintf(os.Stderr, "Error from server (Conflict): Operation cannot be fulfilled on %s %q: %s\n", label, call.Name, message)
	case 403:
		fmt.Fprintf(os.Stderr,
			"Error from server (Forbidden): %s is forbidden: User \"ci-deployer\" cannot %s resource %q in API group %q in the namespace %q\n",
			label, call.Verb, call.Resource, call.Group, call.Namespace)
	case 404:
		fmt.Fprintf(os.Stderr, "Error from server (NotFound): %s %q not found\n", label, call.Name)
	case 504:
		fmt.Fprintln(os.Stderr, "Unable to connect to the server: net/http: request canceled (Client.Timeout exceeded while awaiting headers)")
	default:
		message := inj.Message
		if message == "" {
			message = "injected failure"
		}
		fmt.Fprintf(os.Stderr, "Error from server (InternalError): an error on the server (%q) has prevented the request from succeeding\n", message)
	}
	return 1
}

// ---------------------------------------------------------------------------
// Lease 清单
// ---------------------------------------------------------------------------

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

// leaseIntent 从清单**内容**与服务端现存对象推导写意图（不看文件名）。
func leaseIntent(st *fakestate.State, manifest *leaseManifest) string {
	if st.Lease == nil {
		return fakestate.IntentAcquire
	}
	if manifest.Spec.HolderIdentity == "" {
		return fakestate.IntentRelease
	}
	if manifest.Spec.HolderIdentity != st.Lease.HolderIdentity {
		return fakestate.IntentAcquire
	}
	// 关键：比较的是「调用方读到的那个版本」与它写出去的内容，而不是当前服务端状态。
	// 后台续租与主流程写记录并发时，续租请求携带的是它读到的旧记录；若拿它跟已被
	// 推进的服务端比较，就会被误判成「写记录」，续租类注入（409/500）随之漏掉。
	base := st.LeaseBaseAnnotations(manifest.Metadata.ResourceVersion)
	if base != nil && recordChanged(base, manifest.Metadata.Annotations) {
		return fakestate.IntentRecord
	}
	return fakestate.IntentRenew
}

// recordChanged 判断迁移记录注解是否被改写（只比较 deploy-lock.surgecart.io/ 前缀的键）。
func recordChanged(current, next map[string]string) bool {
	for key, value := range next {
		if !strings.HasPrefix(key, recordAnnotationPrefix) {
			continue
		}
		if current[key] != value {
			return true
		}
	}
	for key := range current {
		if !strings.HasPrefix(key, recordAnnotationPrefix) {
			continue
		}
		if _, ok := next[key]; !ok {
			return true
		}
	}
	return false
}

func leaseCall(verb string, manifest *leaseManifest) fakestate.Invocation {
	call := fakestate.Invocation{
		Verb:      verb,
		Group:     "coordination.k8s.io",
		Resource:  "leases",
		Namespace: namespace,
		Name:      lockName,
	}
	if manifest != nil {
		call.Annotations = manifest.Metadata.Annotations
	}
	return call
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
	call := leaseCall("create", manifest)
	call.Intent = fakestate.IntentAcquire
	return mutateAck(dir, func(st *fakestate.State) (int, func() int) {
		if code := authorizeWrite(dir, st, call, "create"); code != 0 {
			return code, nil
		}
		if inj := st.ConsumeInjection(call, "before"); inj != nil {
			if code := executeInjection(dir, st, inj, call); code != 0 {
				return code, nil
			}
		}
		err := fakestate.CreateLease(st, fakestate.Lease{
			HolderIdentity:  manifest.Spec.HolderIdentity,
			AcquireTime:     manifest.Spec.AcquireTime,
			RenewTime:       manifest.Spec.RenewTime,
			DurationSeconds: manifest.Spec.LeaseDurationSeconds,
			Transitions:     manifest.Spec.LeaseTransitions,
			Annotations:     manifest.Metadata.Annotations,
		})
		if err == fakestate.ErrLeaseExists {
			fmt.Fprintf(os.Stderr, "Error from server (AlreadyExists): error when creating %q: leases.coordination.k8s.io %q already exists\n", path, lockName)
			return 1, nil
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "fake kubectl: 创建 Lease 失败: %v\n", err)
			return 97, nil
		}
		fakestate.Append(dir, fakestate.EventsFileName, "lease.acquire holder="+manifest.Spec.HolderIdentity)
		return 0, func() int { fmt.Printf("lease.coordination.k8s.io/%s created\n", lockName); return 0 }
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
	return mutateAck(dir, func(st *fakestate.State) (int, func() int) {
		call := leaseCall("update", manifest)
		call.Intent = leaseIntent(st, manifest)

		// A-5：写之前必须授权（deploy-lock 的 Role 给 update；没有规则就是 403）。
		if code := authorizeWrite(dir, st, call, "update"); code != 0 {
			return code, nil
		}
		// 「校验/写入之前」的注入：错误、延迟、并发接管（接管后 CAS 必然失败）。
		if inj := st.ConsumeInjection(call, "before"); inj != nil {
			if code := executeInjection(dir, st, inj, call); code != 0 {
				return code, nil
			}
		}
		if st.Lease == nil {
			fmt.Fprintf(os.Stderr, "Error from server (NotFound): leases.coordination.k8s.io %q not found\n", lockName)
			return 1, nil
		}
		// 所有写路径统一走同一个 CAS helper（C-1/C-2/C-3）。
		previous, err := fakestate.CommitLeaseUpdate(st, fakestate.LeaseUpdate{
			ExpectResourceVersion: manifest.Metadata.ResourceVersion,
			ExpectUID:             manifest.Metadata.UID,
			HolderIdentity:        manifest.Spec.HolderIdentity,
			AcquireTime:           manifest.Spec.AcquireTime,
			RenewTime:             manifest.Spec.RenewTime,
			DurationSeconds:       manifest.Spec.LeaseDurationSeconds,
			Transitions:           manifest.Spec.LeaseTransitions,
			Annotations:           manifest.Metadata.Annotations,
		})
		switch {
		case err == fakestate.ErrLeaseNotFound:
			fmt.Fprintf(os.Stderr, "Error from server (NotFound): leases.coordination.k8s.io %q not found\n", lockName)
			return 1, nil
		case err == fakestate.ErrUIDConflict:
			fakestate.Append(dir, fakestate.EventsFileName, "lease.uid-conflict")
			conflictMessage("the UID in the precondition does not match the UID in record")
			return 1, nil
		case err != nil:
			fakestate.Append(dir, fakestate.EventsFileName, "lease.conflict")
			conflictMessage("the object has been modified; please apply your changes to the latest version and try again")
			return 1, nil
		}

		// 写入成功之后的注入：模拟「刚写完就被别的 Runner 接管」。
		if inj := st.ConsumeInjection(call, "after"); inj != nil {
			executeInjection(dir, st, inj, call)
		}

		recordWriteEvent(dir, path, manifest.Metadata.Annotations, previous.HolderIdentity, call.Intent)
		switch {
		case manifest.Spec.HolderIdentity == "":
			fakestate.Append(dir, fakestate.EventsFileName, "lease.release holder="+previous.HolderIdentity)
		case manifest.Spec.HolderIdentity == previous.HolderIdentity:
			fakestate.Append(dir, fakestate.EventsFileName, "lease.renew holder="+previous.HolderIdentity)
		default:
			st.LeaseAcquisitions++
			fakestate.Append(dir, fakestate.EventsFileName,
				"lease.acquire holder="+manifest.Spec.HolderIdentity+" previous="+previous.HolderIdentity)
		}
		return 0, func() int { fmt.Printf("lease.coordination.k8s.io/%s replaced\n", lockName); return 0 }
	})
}

// cmdDelete 实现 `kubectl delete lease <name>`（C-4：让「删除-重建」路径可达）。
func cmdDelete(dir string, inv invocation) int {
	if len(inv.Positional) < 2 {
		fmt.Fprintln(os.Stderr, "fake kubectl: delete 缺少资源类型")
		return 1
	}
	resource := inv.Positional[1]
	if resource != "lease" && resource != "leases" && resource != "leases.coordination.k8s.io" {
		fmt.Fprintf(os.Stderr, "fake kubectl: 未实现的 delete %q\n", resource)
		return 1
	}
	name := lockName
	if len(inv.Positional) >= 3 {
		name = inv.Positional[2]
	}
	call := fakestate.Invocation{
		Verb: "delete", Group: "coordination.k8s.io", Resource: "leases",
		Namespace: namespace, Name: name,
	}
	return mutateAck(dir, func(st *fakestate.State) (int, func() int) {
		if code := authorizeWrite(dir, st, call, "delete"); code != 0 {
			return code, nil
		}
		if inj := st.ConsumeInjection(call, "before"); inj != nil {
			if code := executeInjection(dir, st, inj, call); code != 0 {
				return code, nil
			}
		}
		existed := name == lockName && fakestate.DeleteLease(st)
		if !existed {
			if inv.IgnoreNotFound {
				return 0, nil
			}
			fmt.Fprintf(os.Stderr, "Error from server (NotFound): leases.coordination.k8s.io %q not found\n", name)
			return 1, nil
		}
		fakestate.Append(dir, fakestate.EventsFileName, "lease.delete name="+name)
		return 0, func() int {
			if inv.Output == "name" {
				fmt.Printf("lease.coordination.k8s.io/%s\n", name)
				return 0
			}
			fmt.Printf("lease.coordination.k8s.io/%s deleted\n", name)
			return 0
		}
	})
}

// recordWriteEvent 记录每次 Lease 写入「携带了什么迁移记录」。
//
// 这是 AUD-D2 的可观测性来源：测试可以逐次断言 renew/release/takeover 没有把记录
// 写成默认值（例如 NO_MIGRATION + 空 job），而不是只看最终状态 —— 最终状态可能被
// 后续一次正确的写入掩盖掉中间的丢失。
//
// kind=（清单文件名）与 intent=（由内容推导的写意图）都记录下来：**判定**只看 intent，
// 文件名仅用于兼容既有断言的观测字段。
func recordWriteEvent(dir, path string, ann map[string]string, previousHolder, intent string) {
	fakestate.Append(dir, fakestate.EventsFileName, fmt.Sprintf(
		"lease.write kind=%s state=%s job=%s uid=%s prev=%s previous=%s intent=%s",
		filepath.Base(path),
		ann[recordAnnotationPrefix+"migration-state"],
		ann[recordAnnotationPrefix+"migration-job"],
		ann[recordAnnotationPrefix+"migration-job-uid"],
		ann[recordAnnotationPrefix+"migration-previous-state"],
		previousHolder,
		intent,
	))
}

// ---------------------------------------------------------------------------
// apply（按解析出的 Kind，不看文件名）
// ---------------------------------------------------------------------------

// manifestObject 是清单里的一个文档中本实现关心的字段。
type manifestObject struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name            string            `yaml:"name"`
		Namespace       string            `yaml:"namespace"`
		UID             string            `yaml:"uid"`
		ResourceVersion string            `yaml:"resourceVersion"`
		Labels          map[string]string `yaml:"labels"`
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

// parseManifest 解析多文档 YAML 清单（服务端会逐个对象处理）。
func parseManifest(raw []byte) ([]manifestObject, error) {
	var objects []manifestObject
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		var object manifestObject
		err := decoder.Decode(&object)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if object.Kind == "" && len(object.Metadata.Name) == 0 {
			continue
		}
		objects = append(objects, object)
	}
	return objects, nil
}

// kindTarget 把 Kind 映射成 (group, resource) —— apply 的授权与注入都按它判定（C-14）。
func kindTarget(kind string) (group, resource string, ok bool) {
	switch kind {
	case "Namespace":
		return "", "namespaces", true
	case "ConfigMap":
		return "", "configmaps", true
	case "Secret":
		return "", "secrets", true
	case "Service":
		return "", "services", true
	case "Pod":
		return "", "pods", true
	case "Deployment":
		return "apps", "deployments", true
	case "StatefulSet":
		return "apps", "statefulsets", true
	case "Job":
		return "batch", "jobs", true
	case "Lease":
		return "coordination.k8s.io", "leases", true
	default:
		return "", "", false
	}
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
	objects, err := parseManifest(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error from server (BadRequest): error parsing %q: %v\n", path, err)
		return 1
	}
	if len(objects) == 0 {
		fmt.Fprintf(os.Stderr, "Error from server (BadRequest): no objects passed to apply\n")
		return 1
	}
	// 未知 Kind 必须在写入任何对象之前失败（C-14）：服务端校验，不靠文件名猜。
	for _, object := range objects {
		if _, _, ok := kindTarget(object.Kind); !ok {
			fmt.Fprintf(os.Stderr, "Error from server (BadRequest): unable to recognize %q: no matches for kind %q\n", path, object.Kind)
			return 1
		}
	}
	// 事件名沿用历史的 migration/application/namespace 三分法（仅为观测兼容），
	// 但它是从**解析出的 Kind** 推导的，不是从文件名。
	legacyKind := legacyApplyKind(objects)
	return mutateAck(dir, func(st *fakestate.State) (int, func() int) {
		for _, object := range objects {
			group, resource, _ := kindTarget(object.Kind)
			call := fakestate.Invocation{
				Verb: "apply", Group: group, Resource: resource,
				Namespace: firstNonEmpty(object.Metadata.Namespace, namespace),
				Name:      object.Metadata.Name,
			}
			// 真实 kubectl apply = GET → 不存在 POST(create) / 存在 PATCH(patch)。
			// 「已存在 → update」是不对的：按交付 RBAC 授权的身份只有 patch，
			// 会被 fake 误判成 403（而在真实集群上是 200）。这里先 patch，兼容
			// 只授予 update 的历史授权写法。
			verbs := []string{"create"}
			if objectExists(st, group, resource, object.Metadata.Name) {
				verbs = []string{"patch", "update"}
			}
			if code := authorizeWrite(dir, st, call, verbs...); code != 0 {
				return code, nil
			}
			if inj := st.ConsumeInjection(call, "before"); inj != nil {
				if inj.Applied {
					// 「服务端已接受但客户端看到超时」：写入生效，然后返回错误。
					// 对象已被创建这件事必须落到状态里 —— 客户端非零 ≠ 没有变更。
					if err := applyObject(st, object); err != nil {
						conflictMessage(err.Error())
						return 1, nil
					}
					if inj.Event != "" {
						fakestate.Append(dir, fakestate.EventsFileName, inj.Event)
					}
					fmt.Fprintln(os.Stderr, "Error from server (Timeout): the server was unable to return a response in the time allotted, but the request may have been processed")
					return 1, nil
				}
				if code := executeInjection(dir, st, inj, call); code != 0 {
					return code, nil
				}
			}
			// apply 的 UPDATE 语义同样不允许跳过 CAS（C-3）：冲突必须变成 409。
			if err := applyObject(st, object); err != nil {
				fakestate.Append(dir, fakestate.EventsFileName, "apply.conflict "+resource)
				conflictMessage(err.Error())
				return 1, nil
			}
		}
		fakestate.Append(dir, fakestate.EventsFileName, "apply."+legacyKind)
		return 0, func() int { fmt.Printf("%s applied\n", legacyKind); return 0 }
	})
}

// legacyApplyKind 从解析出的 Kind 推导历史事件名（migration / application / namespace）。
func legacyApplyKind(objects []manifestObject) string {
	for _, object := range objects {
		if object.Kind == "Job" {
			return "migration"
		}
	}
	for _, object := range objects {
		if object.Kind == "Deployment" {
			return "application"
		}
	}
	return "namespace"
}

// objectExists 判断 apply 的目标对象是否已存在（决定 create 还是 update 语义）。
func objectExists(st *fakestate.State, group, resource, name string) bool {
	switch {
	case resource == "namespaces":
		return st.NamespaceExists
	case resource == "deployments":
		return st.Applied || st.Deployment.Exists
	case resource == "jobs":
		for _, job := range st.Jobs {
			if jobNameFor(job.Name, "") == jobNameFor(name, "") {
				return true
			}
		}
		return false
	case resource == "leases":
		return st.Lease != nil
	default:
		return false
	}
}

// applyObject 按对象 Kind 执行 CREATE/UPDATE 语义（不再按清单文件名判断）。
// 返回非 nil 表示服务端拒绝了这次写入（Lease 的 UPDATE 同样走 CAS，不允许跳过版本校验）。
func applyObject(st *fakestate.State, object manifestObject) error {
	_, resource, _ := kindTarget(object.Kind)
	switch resource {
	case "namespaces":
		st.NamespaceExists = true
	case "deployments":
		st.Applied = true
		st.DeploymentGets = 0
	case "jobs":
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
	case "leases":
		if st.Lease == nil {
			return fakestate.CreateLease(st, fakestate.Lease{
				HolderIdentity:  object.Spec.HolderIdentity,
				AcquireTime:     object.Spec.AcquireTime,
				RenewTime:       object.Spec.RenewTime,
				DurationSeconds: object.Spec.LeaseDurationSeconds,
				Transitions:     object.Spec.LeaseTransitions,
				Annotations:     object.Metadata.Annotations,
			})
		}
		// 已存在 → UPDATE 语义，仍然走 CAS（apply 不允许跳过版本校验）。
		if _, err := fakestate.CommitLeaseUpdate(st, fakestate.LeaseUpdate{
			ExpectResourceVersion: object.Metadata.ResourceVersion,
			ExpectUID:             object.Metadata.UID,
			HolderIdentity:        object.Spec.HolderIdentity,
			AcquireTime:           object.Spec.AcquireTime,
			RenewTime:             object.Spec.RenewTime,
			DurationSeconds:       object.Spec.LeaseDurationSeconds,
			Transitions:           object.Spec.LeaseTransitions,
			Annotations:           object.Metadata.Annotations,
		}); err != nil {
			return err
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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

// cmdGetLease 读取 Lease。
//
// C-6/C-12：并发接管注入必须发生在「GET 返回的快照被调用方使用之后」——
// 调用方拿到的是**接管前**的快照，接管在返回之后才落到状态里。
func cmdGetLease(dir string, inv invocation) int {
	call := fakestate.Invocation{
		Verb: "get", Group: "coordination.k8s.io", Resource: "leases",
		Namespace: namespace, Name: lockName,
	}
	return mutateAck(dir, func(st *fakestate.State) (int, func() int) {
		st.LeaseGets++
		if inj := st.ConsumeInjection(call, "before"); inj != nil {
			if code := executeInjection(dir, st, inj, call); code != 0 {
				return code, nil
			}
		}
		if st.Lease == nil {
			fmt.Fprintf(os.Stderr, "Error from server (NotFound): leases.coordination.k8s.io %q not found\n", lockName)
			return 1, nil
		}
		snapshot := st.Lease.Clone()
		// 返回之后才接管/删除：快照是接管前的状态。
		if inj := st.ConsumeInjection(call, "after"); inj != nil {
			executeInjection(dir, st, inj, call)
		}
		return 0, func() int {
			return render(inv.Output, leaseObject(snapshot), leaseTable(snapshot))
		}
	})
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
	call := fakestate.Invocation{
		Verb: "get", Group: "batch", Resource: "jobs",
		Namespace: namespace, Name: requested,
	}
	return mutateAck(dir, func(st *fakestate.State) (int, func() int) {
		if inj := st.ConsumeInjection(call, "before"); inj != nil {
			if code := executeInjection(dir, st, inj, call); code != 0 {
				return code, nil
			}
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
			return 1, nil
		}
		return 0, func() int {
			return render(inv.Output, jobObject(job, requested, st.Job.UID), jobTable(job))
		}
	})
}

func cmdGetJobs(dir string, inv invocation) int {
	call := fakestate.Invocation{
		Verb: "list", Group: "batch", Resource: "jobs", Namespace: namespace, Selector: inv.Selector,
	}
	return mutateAck(dir, func(st *fakestate.State) (int, func() int) {
		if inj := st.ConsumeInjection(call, "before"); inj != nil {
			if code := executeInjection(dir, st, inj, call); code != 0 {
				return code, nil
			}
		}
		// 只返回「集群里真实存在的 Job」：st.Job / JobScript 是脚本按名称读取的
		// 状态序列，不代表对象已经存在（apply 之前它必须不在列表里）。
		jobs := st.Jobs
		items := make([]any, 0, len(jobs))
		for _, job := range jobs {
			items = append(items, jobObject(job, job.Name, st.Job.UID))
		}
		return 0, func() int {
			return render(inv.Output, map[string]any{
				"apiVersion": "batch/v1",
				"kind":       "JobList",
				"items":      items,
			}, jobsTable(jobs))
		}
	})
}

func cmdGetDeployment(dir string, inv invocation) int {
	return mutateAck(dir, func(st *fakestate.State) (int, func() int) {
		// AUD-09：命名空间/鉴权类错误必须原样暴露，不能被脚本当成「Deployment 不存在」。
		if st.DeploymentGetError != "" {
			fakestate.Append(dir, fakestate.EventsFileName, "deployment.get-error")
			fmt.Fprintln(os.Stderr, st.DeploymentGetError)
			return 1, nil
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
			return 1, nil
		}
		return 0, func() int {
			return render(inv.Output, deploymentObject(deployment), deploymentTable(deployment))
		}
	})
}

func cmdGetPods(dir string, inv invocation) int {
	call := fakestate.Invocation{
		Verb: "list", Resource: "pods", Namespace: namespace, Selector: inv.Selector,
	}
	return mutateAck(dir, func(st *fakestate.State) (int, func() int) {
		if inj := st.ConsumeInjection(call, "before"); inj != nil {
			if code := executeInjection(dir, st, inj, call); code != 0 {
				return code, nil
			}
		}
		isJobPods := strings.Contains(inv.Selector, "job-name=")
		isMigrationPods := strings.Contains(inv.Selector, "database-migration")
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
		return 0, func() int {
			return render(inv.Output, podsListObject(pods, ownerUID), podsTable(pods))
		}
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
