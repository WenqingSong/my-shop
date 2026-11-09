package deployk8s

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1.4 的权限行为契约（审计 A-1 / A-2 / A-3 / A-5 / A-8 / A-9 / D-3）。
//
// 三层证据：
//  1. 权限矩阵 —— 对 REQUIRED_PERMISSIONS **逐条**拒绝，发布必须 fail-closed
//     （退出码 3），且发生在任何资源写请求之前；
//  2. 错误分类 —— Forbidden / NotFound / 5xx / 超时四种注入下，脚本的措辞与退出码
//     必须正确：不允许把「查询失败」说成「不存在」，也不允许把「不存在」说成「无权限」；
//  3. 首次发布 —— 从空命名空间开始（namespace 不存在），脚本必须能走到
//     DEPLOY_K8S_SUCCESS（证明「只授予交付的 RBAC 权限就能发布」这条路径是通的）。
//
// 边界：fake kubectl 不是 API Server。这里的 deny 是「auth can-i 返回 no」，
// 真实集群上的 403 由 RBAC 决定；真实集群验证仍是 NOT_VERIFIED。
// 写路径的真实拒绝（缺权限 → apply/create 403）在 rbac_write_test.go。

// ---------------------------------------------------------------------------
// REQUIRED_PERMISSIONS 解析（与 test/manifest 的断言同源：scripts/deploy-k8s.sh）
// ---------------------------------------------------------------------------

type requiredPermission struct {
	Verb     string
	Group    string // core/v1 归一化为 ""
	Resource string
	Scope    string // namespace | cluster
	CallSite string
	// Name 是可选的第 6 字段：非空表示该权限在交付 RBAC 里受 resourceNames 限制，
	// 预检必须用 `TYPE/NAME` 的对象级写法（T2-06.1.5 / F-01）。
	Name string
}

// ref 返回 `kubectl auth can-i` 接受的资源写法（非 core group 带 group 后缀）。
func (p requiredPermission) ref() string {
	if p.Group == "" {
		return p.Resource
	}
	return p.Resource + "." + p.Group
}

// objectRef 是**错误信息里展示**的授权对象：
// 非空 Name 时追加 "/<name>"（受 resourceNames 限制的权限必须让读者看到具体对象）。
func (p requiredPermission) objectRef() string {
	if p.Name == "" {
		return p.ref()
	}
	return p.ref() + "/" + p.Name
}

// shortResource 是 fake 的 Denied 使用的写法：fake 按第一个 "." 截断 group，
// 但保留 "/" 子资源（pods/log）。
func (p requiredPermission) shortResource() string { return p.Resource }

func (p requiredPermission) String() string {
	return fmt.Sprintf("%s %s（scope=%s，调用点 %s）", p.Verb, p.ref(), p.Scope, p.CallSite)
}

func requiredPermissionsFromScript(t *testing.T) []requiredPermission {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRootPath, "scripts", "deploy-k8s.sh"))
	if err != nil {
		t.Fatalf("读取发布脚本: %v", err)
	}
	block := regexp.MustCompile(`(?s)REQUIRED_PERMISSIONS=\((.*?)\n\)`).FindStringSubmatch(string(raw))
	if block == nil {
		t.Fatal("scripts/deploy-k8s.sh 缺少 REQUIRED_PERMISSIONS：本文件的权限矩阵会失去意义")
	}
	// 5 个必填字段 + 1 个可选字段（resourceName）。可选字段存在时不能为空以外
	// 的形式被吞掉：多一个 "|" 就无法解析（严格失败，不静默降级）。
	entryPattern := regexp.MustCompile(`^"([^"|]+)\|([^"|]*)\|([^"|]+)\|([^"|]+)\|([^"|]*)(?:\|([^"|]*))?"$`)
	var out []requiredPermission
	for _, line := range strings.Split(block[1], "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		match := entryPattern.FindStringSubmatch(trimmed)
		if match == nil {
			t.Fatalf("REQUIRED_PERMISSIONS 条目无法解析: %q", trimmed)
		}
		group := match[2]
		if group == "core" {
			group = ""
		}
		entry := requiredPermission{
			Verb: match[1], Group: group, Resource: match[3], Scope: match[4],
			CallSite: match[5], Name: match[6],
		}
		// 与脚本的 validate_permission_table 同一条不变量：
		// create/list/watch/deletecollection 不能带对象名（真实 RBAC 无此语义）。
		if entry.Name != "" {
			switch entry.Verb {
			case "get", "update", "patch", "delete":
			default:
				t.Fatalf("REQUIRED_PERMISSIONS 条目 %q 给 %q 指定了对象名：resourceNames 只对 get/update/patch/delete 生效",
					trimmed, entry.Verb)
			}
		}
		out = append(out, entry)
	}
	if len(out) == 0 {
		t.Fatal("REQUIRED_PERMISSIONS 为空")
	}
	return out
}

// ---------------------------------------------------------------------------
// 权限矩阵：逐条拒绝 → 必须 fail-closed
// ---------------------------------------------------------------------------

// TestEveryRequiredPermissionIsEnforced 是本工作包最重要的证据：
// 对 REQUIRED_PERMISSIONS 的**每一条**单独拒绝，发布都必须以退出码 3 失败，
// 输出对应的「缺少 <resource> 的 <verb> 权限（Forbidden）」，
// 并且不发起任何资源写请求（不建 Lease、不 apply 迁移/应用清单）。
func TestEveryRequiredPermissionIsEnforced(t *testing.T) {
	t.Parallel()
	entries := requiredPermissionsFromScript(t)
	if len(entries) < 20 {
		t.Fatalf("REQUIRED_PERMISSIONS 只有 %d 项：权限矩阵会失去意义（少于 20 项说明表被改小了）", len(entries))
	}
	for _, entry := range entries {
		entry := entry
		t.Run(entry.Verb+"-"+strings.ReplaceAll(entry.ref(), ".", "_")+"-"+entry.Scope, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, fixtureOptions{})
			denied := entry.Verb + " " + entry.shortResource()
			f.updateState(func(st *fakestate.State) {
				// 只拒绝这一条：其余权限仍然可用，证明检查是逐项的。
				st.Permissions.Denied = []string{denied}
			})

			res := runDeploy(t, f)

			res.requireCode(t, 3)
			res.requireNoSuccess(t)
			res.requireContains(t,
				"缺少 "+entry.objectRef()+" 的 "+entry.Verb+" 权限",
				"Forbidden",
			)
			// 权限不足时不得开始迁移/应用（工作负载变更）。
			f.requireNoEvent(t, "apply.migration")
			f.requireNoEvent(t, "apply.application")
			requireNoResourceMutations(t, f)
		})
	}
}

// requireNoResourceMutations 断言权限预检失败时没有发起任何资源写请求。
//
// kubectl 参数以 `--request-timeout=...` 开头，因此判断必须看子命令而不是行首；
// `auth can-i` 是预检自身，不在此列。
func requireNoResourceMutations(t *testing.T, f *fixture) {
	t.Helper()
	for _, call := range f.calls() {
		if strings.Contains(call, " auth can-i ") {
			continue
		}
		for _, verb := range []string{" create ", " replace ", " apply ", " delete ", " logs "} {
			if strings.Contains(" "+call+" ", verb) {
				t.Fatalf("权限预检失败时不得发起资源操作（%s），实际调用: %s", strings.TrimSpace(verb), call)
			}
		}
	}
	if lease := f.state().Lease; lease != nil {
		t.Fatalf("权限预检失败时不得创建 Lease，实际: %+v", lease)
	}
}

// TestPermissionPreflightDeniedMessageNamesScope 断言集群级与命名空间级的错误信息
// 能区分作用域：namespaces 是集群级资源，把它写成「命名空间里的权限」会让人去改 Role。
func TestPermissionPreflightDeniedMessageNamesScope(t *testing.T) {
	t.Parallel()

	cases := []struct {
		denied   string
		fragment string
	}{
		{denied: "get namespaces", fragment: "缺少 namespaces 的 get 权限"},
		{denied: "create namespaces", fragment: "缺少 namespaces 的 create 权限"},
		{denied: "create selfsubjectaccessreviews", fragment: "缺少 selfsubjectaccessreviews.authorization.k8s.io 的 create 权限"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.denied, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, fixtureOptions{})
			f.updateState(func(st *fakestate.State) { st.Permissions.Denied = []string{tc.denied} })
			res := runDeploy(t, f)
			res.requireCode(t, 3)
			res.requireContains(t, tc.fragment, "Forbidden")
		})
	}
}

// TestPermissionPreflightDistinguishesForbiddenFromUnknown 覆盖 F-01 / A-9 的判定质量：
// `auth can-i` 本身失败（SSAR 不可用）时，不能报成「权限被拒绝」。
func TestPermissionPreflightDistinguishesForbiddenFromUnknown(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		st.AuthCanIError = `Error from server (ServiceUnavailable): the server is currently unable to handle the request`
	})

	res := runDeploy(t, f)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "无法确认当前身份", "不是", "被拒绝", "ServiceUnavailable")
	res.requireNotContains(t, "缺少 pods 的", "缺少 leases.coordination.k8s.io")
	f.requireNoEvent(t, "apply.migration")
	requireNoResourceMutations(t, f)
}

// ---------------------------------------------------------------------------
// 命名空间：严格分类（审计 A-1 / A-2 / D-9）
// ---------------------------------------------------------------------------

// TestNamespaceReadFailureIsNotReportedAsAbsent 覆盖 A-1 的根因：
// `get namespace` 失败（403 / 5xx / 超时）**不能**被当成「命名空间不存在」，
// 否则脚本会立刻去 apply 命名空间，把一次权限/网络故障变成一次失败的写请求。
func TestNamespaceReadFailureIsNotReportedAsAbsent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		stderr   string
		fragment string
	}{
		{
			name:     "forbidden",
			stderr:   `Error from server (Forbidden): namespaces "surgecart" is forbidden: User "ci-deployer" cannot get resource "namespaces" in API group "" at the cluster scope`,
			fragment: "无权读取命名空间 surgecart",
		},
		{
			name:     "server-error",
			stderr:   `Error from server (InternalError): an error on the server ("injected") has prevented the request from succeeding`,
			fragment: "API 故障",
		},
		{
			name:     "timeout",
			stderr:   `Unable to connect to the server: net/http: request canceled (Client.Timeout exceeded while awaiting headers)`,
			fragment: "网络超时",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, fixtureOptions{})
			// 命名空间不存在：如果脚本把错误当成「不存在」，就会去 apply 命名空间。
			f.updateState(func(st *fakestate.State) { st.NamespaceExists = false })
			wrapKubectl(t, f, "ns-get", `*" get namespace "*`, tc.stderr, 1)

			res := runDeploy(t, f)

			res.requireCode(t, 3)
			res.requireNoSuccess(t)
			res.requireContains(t, tc.fragment, "这不是「命名空间不存在」")
			res.requireNotContains(t, "按 manifest/kubernetes/shared/namespace.yaml 创建")
			f.requireNoEvent(t, "apply.namespace")
			requireNoResourceMutations(t, f)
		})
	}
}

// TestFirstDeployCreatesNamespaceAndSucceeds 是「只授予交付的 RBAC 权限，首次发布
// 能完整运行」的正面证据：命名空间不存在 → 集群级预检通过 → 命名空间级预检通过 →
// 创建命名空间 → 锁 → 迁移 → 应用 → DEPLOY_K8S_SUCCESS。
func TestFirstDeployCreatesNamespaceAndSucceeds(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{firstDeploy: true})
	f.updateState(func(st *fakestate.State) { st.NamespaceExists = false })

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "创建命名空间 surgecart", "首次部署", "权限预检通过")
	if !f.state().NamespaceExists {
		t.Fatal("首次发布必须创建命名空间")
	}
	f.requireEvent(t, "apply.namespace")
	// 命名空间引导必须发生在锁之前（否则锁无处落地）。
	f.requireEventOrder(t, "apply.namespace", "apply.migration", "apply.application")
}

// ---------------------------------------------------------------------------
// Secret：严格分类（审计 A-3）
// ---------------------------------------------------------------------------

func TestSecretReadForbiddenIsNotMissingSecret(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) { st.Secrets = nil })
	wrapKubectl(t, f, "secret-get", `*" get secret "*`,
		`Error from server (Forbidden): secrets "surgecart-db-credentials" is forbidden: User "ci-deployer" cannot get resource "secrets" in API group "" in the namespace "surgecart"`, 1)

	res := runDeploy(t, f, "--migration-timeout", "10")

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "无权读取 Secret", "Forbidden", "这不是「Secret 不存在」", "secrets 的 get 权限")
	res.requireNotContains(t, "缺少 Secret surgecart")
	f.requireNoEvent(t, "apply.migration")
}

func TestSecretReadFailureIsNotMissingSecret(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) { st.Secrets = nil })
	wrapKubectl(t, f, "secret-get", `*" get secret "*`,
		`Error from server (ServiceUnavailable): the server is currently unable to handle the request`, 1)

	res := runDeploy(t, f, "--migration-timeout", "10")

	res.requireCode(t, 1)
	res.requireNoSuccess(t)
	res.requireContains(t, "无法确认 Secret", "API 故障", "这不等于「缺少 Secret」")
	res.requireNotContains(t, "缺少 Secret surgecart")
	f.requireNoEvent(t, "apply.migration")
}

// TestSecretNotFoundIsReportedAsMissing 是分类的正向分支：真的 NotFound 必须仍然
// 报「缺少 Secret」（不能让 fail-closed 把可操作的提示也吞掉）。
func TestSecretNotFoundIsReportedAsMissing(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) { st.Secrets = nil })

	res := runDeploy(t, f, "--migration-timeout", "10")

	res.requireCode(t, 1)
	// 两个 Secret 都缺失时先命中排序后的第一个；这里只断言「确实报成缺少」。
	res.requireContains(t, "缺少 Secret surgecart/", "NotFound")
	f.requireNoEvent(t, "apply.migration")
}

// ---------------------------------------------------------------------------
// 迁移 Job 列表：查询失败 ≠ 存在未终结的 Job（审计 D-3）
// ---------------------------------------------------------------------------

func TestJobsListForbiddenIsNotUnfinishedJob(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	// 权限预检用 auth can-i，不受这里影响：拦截发生在 begin_migration 的列表查询。
	wrapKubectl(t, f, "jobs-list", `*" get jobs "*`,
		`Error from server (Forbidden): jobs.batch is forbidden: User "ci-deployer" cannot list resource "jobs" in API group "batch" in the namespace "surgecart"`, 1)

	res := runDeploy(t, f, "--migration-timeout", "10")

	res.requireCode(t, 7)
	res.requireNoSuccess(t)
	res.requireContains(t,
		"**无法确认**迁移 Job 状态",
		"Forbidden",
		"这是**权限不足**（jobs 的 list 缺失）",
		"**不是**「存在未终结的 Job」",
	)
	res.requireNotContains(t, "**确实存在**未终结的迁移 Job", "存在未终结的迁移 Job：拒绝开始新一轮迁移")
	f.requireNoEvent(t, "apply.migration")
}

func TestJobsListServerErrorIsNotUnfinishedJob(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	wrapKubectl(t, f, "jobs-list", `*" get jobs "*`,
		`Error from server (InternalError): an error on the server ("injected") has prevented the request from succeeding`, 1)

	res := runDeploy(t, f, "--migration-timeout", "10")

	res.requireCode(t, 7)
	res.requireContains(t, "**无法确认**迁移 Job 状态", "API 故障", "查询失败")
	res.requireNotContains(t, "**确实存在**未终结的迁移 Job")
	f.requireNoEvent(t, "apply.migration")
}

// ---------------------------------------------------------------------------
// 日志：失败必须被报告（审计 A-8）
// ---------------------------------------------------------------------------

func TestJobLogsForbiddenIsReported(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		failed := fakestate.Job{
			Name:    "surgecart-migrate-" + f.shas["target"],
			UID:     "job-uid-" + f.shas["target"],
			Exists:  true,
			Failed:  true,
			Reason:  "BackoffLimitExceeded",
			Message: "Job has reached the specified backoff limit",
		}
		st.Job = failed
		st.JobScript = []fakestate.Job{failed}
	})
	wrapKubectl(t, f, "logs", `*" logs "*`,
		`Error from server (Forbidden): pods "surgecart-migrate-abc1234-x7k2p" is forbidden: User "ci-deployer" cannot get resource "pods/log" in API group "" in the namespace "surgecart"`, 1)

	res := runDeploy(t, f, "--migration-timeout", "10")

	res.requireCode(t, 2)
	res.requireContains(t,
		"无法读取 Job/",
		"pods/log 的 get 权限",
		"这**不等于**「Job 没有日志」",
		"Forbidden",
	)
}

// ---------------------------------------------------------------------------
// 命名空间引导与 unknown 复检（本轮新增的顺序逻辑）
// ---------------------------------------------------------------------------

// TestNamespaceScopedUnknownIsRetriedAfterNamespaceCreation 覆盖预检顺序里唯一
// 「先写后检」的分支：命名空间不存在 + 某个 SSAR 无法完成（unknown，例如授权器要求
// 命名空间已存在）→ 创建**空**命名空间 → 只复检 unknown 的条目 → 通过 → 继续发布。
//
// 这条路径必须真的能走通，否则「首次部署 + webhook 授权器」的集群会永久无法发布；
// 同时它必须仍然 fail-closed：复检不过就终止（见下一个用例）。
func TestNamespaceScopedUnknownIsRetriedAfterNamespaceCreation(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{firstDeploy: true})
	f.updateState(func(st *fakestate.State) { st.NamespaceExists = false })
	// 只有第一次 `auth can-i get pods` 失败（模拟 SSAR 依赖命名空间存在）。
	wrapKubectlOnce(t, f, "ssar-pods", `*" auth can-i get pods "*`,
		`Error from server (ServiceUnavailable): the server is currently unable to handle the request`, 1)

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "先创建空命名空间", "复检 1 项，全部通过")
	f.requireEventOrder(t, "apply.namespace", "apply.migration", "apply.application")
	if !f.state().NamespaceExists {
		t.Fatal("复检路径必须真的创建命名空间")
	}
}

// TestNamespaceScopedUnknownStillUnknownFailsClosed：复检仍然 unknown → 退出 3，
// 且只创建了空命名空间（不建锁、不迁移、不应用）。
func TestNamespaceScopedUnknownStillUnknownFailsClosed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{firstDeploy: true})
	f.updateState(func(st *fakestate.State) { st.NamespaceExists = false })
	// 每次 `auth can-i get pods` 都失败：第一次触发 unknown → 建命名空间 → 复检仍失败。
	wrapKubectl(t, f, "ssar-pods", `*" auth can-i get pods "*`,
		`Error from server (ServiceUnavailable): the server is currently unable to handle the request`, 1)

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "权限预检无法完成", "fail-closed")
	f.requireNoEvent(t, "apply.migration")
	f.requireNoEvent(t, "apply.application")
	if lease := f.state().Lease; lease != nil {
		t.Fatalf("权限未确认时不得创建发布锁，实际: %+v", lease)
	}
}

// ---------------------------------------------------------------------------
// 工具：在 PATH 最前面插入一个只拦截特定调用的包装器
// ---------------------------------------------------------------------------

// wrapKubectl 在 fake kubectl 前面套一层包装：命中 pattern（bash `case` 模式，
// 匹配 " $* "）时直接把 stderr 文本写到标准错误并以 code 退出，其余调用转发。
//
// 为什么用包装器而不是改 fake：fake（test/deployk8s/fakekubectl）归 Agent C 所有，
// 本工作包不应该为了「注入一次 403」去改它的状态模型。包装器只影响被拦截的那一条
// 调用，其余语义完全由 fake 决定。
func wrapKubectl(t *testing.T, f *fixture, name, pattern, stderrText string, code int) {
	t.Helper()
	dir := filepath.Join(f.root, ".wrap-"+name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("创建包装目录: %v", err)
	}
	script := fmt.Sprintf(`#!/usr/bin/env bash
case " $* " in
  %s)
    printf '%%s\n' %s >&2
    exit %d
    ;;
esac
exec "${FAKE_KUBECTL_REAL}" "$@"
`, pattern, shellSingleQuote(stderrText), code)
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatalf("写包装器: %v", err)
	}
	prependFakePath(t, f, dir)
}

// wrapKubectlOnce 只在**第一次**命中 pattern 时注入错误，之后原样转发给 fake。
// 用于验证「unknown → 建空命名空间 → 复检通过」这条路径确实能走通。
func wrapKubectlOnce(t *testing.T, f *fixture, name, pattern, stderrText string, code int) {
	t.Helper()
	dir := filepath.Join(f.root, ".wrap-"+name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("创建包装目录: %v", err)
	}
	counter := filepath.Join(f.stateDir, "wrap-"+name+".count")
	script := fmt.Sprintf(`#!/usr/bin/env bash
counter=%s
hits=0
if [[ -f "${counter}" ]]; then hits="$(cat "${counter}")"; fi
case " $* " in
  %s)
    hits=$(( hits + 1 ))
    printf '%%s' "${hits}" > "${counter}"
    if (( hits == 1 )); then
      printf '%%s\n' %s >&2
      exit %d
    fi
    ;;
esac
exec "${FAKE_KUBECTL_REAL}" "$@"
`, shellSingleQuote(counter), pattern, shellSingleQuote(stderrText), code)
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatalf("写包装器: %v", err)
	}
	prependFakePath(t, f, dir)
}

// prependFakePath 把包装目录放到 fixture 的 PATH 最前面。
func prependFakePath(t *testing.T, f *fixture, dir string) {
	t.Helper()
	for i, kv := range f.env {
		if strings.HasPrefix(kv, "PATH=") {
			f.env[i] = "PATH=" + dir + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
			return
		}
	}
	t.Fatal("fixture 的 env 里没有 PATH：无法注入调用拦截")
}

// shellSingleQuote 把任意文本包成安全的单引号 shell 字符串。
func shellSingleQuote(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}
