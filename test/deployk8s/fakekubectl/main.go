// fake kubectl —— T2-06.1 发布脚本行为测试的执行替身。
//
// 它不是 kubectl 的完整实现，只忠实实现 scripts/deploy-k8s.sh 真正依赖的语义：
//
//	version                               集群可达性
//	kustomize <path>                      输出预置清单（migration.yaml / application.yaml）
//	auth can-i <verb> leases...           权限预检
//	create -f <Lease>                     原子创建（同名只能成功一次）
//	replace -f <Lease>                    PUT + metadata.resourceVersion 的 CAS
//	apply -f <manifest>                   记录 apply，并按清单 kind 推进阶段
//	get namespace|secret|lease|job|deployment|pods
//	logs | describe | wait                只输出文本，供失败 runbook 使用
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
		Name            string `yaml:"name"`
		Namespace       string `yaml:"namespace"`
		ResourceVersion string `yaml:"resourceVersion"`
		UID             string `yaml:"uid"`
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
		}
		st.LeaseAcquisitions++
		fakestate.Append(dir, fakestate.EventsFileName, "lease.acquire holder="+manifest.Spec.HolderIdentity)
		fmt.Printf("lease.coordination.k8s.io/%s created\n", lockName)
		return 0
	})
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
		if st.LeaseReplaceFailures > 0 {
			st.LeaseReplaceFailures--
			fakestate.Append(dir, fakestate.EventsFileName, "lease.replace-error")
			fmt.Fprintln(os.Stderr, `Error from server (InternalError): an error on the server ("injected lease failure") has prevented the request from succeeding`)
			return 1
		}
		if manifest.Metadata.ResourceVersion != st.Lease.ResourceVersion {
			fakestate.Append(dir, fakestate.EventsFileName, "lease.conflict")
			fmt.Fprintf(os.Stderr, "Error from server (Conflict): Operation cannot be fulfilled on leases.coordination.k8s.io %q: the object has been modified; please apply your changes to the latest version and try again\n", lockName)
			return 1
		}

		previous := st.Lease.HolderIdentity
		st.Lease.HolderIdentity = manifest.Spec.HolderIdentity
		st.Lease.AcquireTime = manifest.Spec.AcquireTime
		st.Lease.RenewTime = manifest.Spec.RenewTime
		st.Lease.DurationSeconds = manifest.Spec.LeaseDurationSeconds
		st.Lease.Transitions = manifest.Spec.LeaseTransitions
		st.Lease.ResourceVersion = nextResourceVersion(st.Lease.ResourceVersion)

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
		fakestate.Append(dir, fakestate.EventsFileName, "apply."+kind)
		switch kind {
		case "application":
			st.Applied = true
			st.DeploymentGets = 0
		case "namespace":
			st.NamespaceExists = true
		}
		fmt.Printf("%s applied\n", kind)
		return 0
	})
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
	code := mutate(dir, func(st *fakestate.State) int {
		st.LeaseGets++
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
			st.Lease.HolderIdentity = st.LeaseStealHolder
			st.Lease.ResourceVersion = nextResourceVersion(st.Lease.ResourceVersion)
			fakestate.Append(dir, fakestate.EventsFileName, "lease.stolen holder="+st.LeaseStealHolder)
		}
		return render(inv.Output, leaseObject(st.Lease), leaseTable(st.Lease))
	})
	return code
}

func cmdGetJob(dir string, inv invocation) int {
	return mutate(dir, func(st *fakestate.State) int {
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
			fmt.Fprintf(os.Stderr, "Error from server (NotFound): jobs.batch %q not found\n", "surgecart-migrate")
			return 1
		}
		return render(inv.Output, jobObject(job), jobTable(job))
	})
}

func cmdGetDeployment(dir string, inv invocation) int {
	return mutate(dir, func(st *fakestate.State) int {
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
		var pods []fakestate.Pod
		if strings.Contains(inv.Selector, "job-name=") {
			pods = st.Job.Pods
		} else if st.Applied && len(st.DeploymentAfterApply) > 0 {
			index := st.DeploymentGets
			if index >= len(st.DeploymentAfterApply) {
				index = len(st.DeploymentAfterApply) - 1
			}
			pods = st.DeploymentAfterApply[index].Pods
		} else {
			pods = st.Deployment.Pods
		}
		return render(inv.Output, podsListObject(pods), podsTable(pods))
	})
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
	return map[string]any{
		"apiVersion": "coordination.k8s.io/v1",
		"kind":       "Lease",
		"metadata": map[string]any{
			"name":            lockName,
			"namespace":       namespace,
			"resourceVersion": lease.ResourceVersion,
			"uid":             lease.UID,
		},
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

func jobObject(job fakestate.Job) map[string]any {
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
	object := map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata": map[string]any{
			"name":      "surgecart-migrate",
			"namespace": namespace,
		},
		"spec": map[string]any{"parallelism": 1, "completions": 1},
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
	}
	return fmt.Sprintf("NAME\tSTATUS\tCOMPLETIONS\tAGE\nsurgecart-migrate\t%s\t1/1\t1m\n", state)
}

func podsListObject(pods []fakestate.Pod) map[string]any {
	items := make([]any, 0, len(pods))
	for _, pod := range pods {
		metadata := map[string]any{
			"name":   pod.Name,
			"labels": map[string]any{"app.kubernetes.io/version": pod.Version},
		}
		if pod.Deleting {
			metadata["deletionTimestamp"] = "2026-11-07T00:00:00Z"
		}
		status := map[string]any{"phase": pod.Phase}
		// Pending 且未启动容器时没有 containerStatuses：脚本必须按「未 Ready」处理。
		if pod.Phase != "Pending" {
			status["containerStatuses"] = []any{
				map[string]any{"name": "app", "image": pod.Image, "ready": pod.Ready},
			}
		}
		items = append(items, map[string]any{
			"metadata": metadata,
			"status":   status,
			"spec": map[string]any{
				"containers": []any{map[string]any{"name": "app", "image": pod.Image}},
			},
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
