package deployk8s

// 本文件是 T2-06.1.4 的**真实多进程**并发测试（审计 C-1 / C-2 / C-3 / C-9 / C-11 /
// C-13 / C-14、A-5）。
//
// 与其它 *_test.go 的区别：
//   - 用例直接启动 fake kubectl **进程**（不是在一个进程里调函数），因此锁竞争、
//     resourceVersion 推进、CAS 拒绝都是真实发生的；
//   - 断言「只有一个成功、其余 409、最终 rv 单调、无丢更新」，而不是只断言参数算对了。
//
// 边界（必须如实理解）：fake kubectl 仍然是**单文件状态 + flock** 的替身。它能证明
// 「脚本/替身在给定状态序列下的行为」，不能证明真实 API Server / etcd 的事务与
// watch 语义 —— 真实集群上的并发行为仍然是 NOT_VERIFIED。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// ---------------------------------------------------------------------------
// 测试脚手架
// ---------------------------------------------------------------------------

// permissiveRules 是「什么都允许」的显式规则：本文件的用例关心的是并发/CAS 语义，
// 不是权限语义（权限用例在文件末尾单独构造）。
func permissiveRules() []fakestate.Rule {
	return []fakestate.Rule{{
		APIGroups: []string{"*"},
		Resources: []string{"*"},
		Verbs:     []string{"*"},
	}}
}

// newFakeStateDir 建立一个可以直接跑 fake kubectl 的状态目录。
func newFakeStateDir(t *testing.T, seed func(*fakestate.State)) string {
	t.Helper()
	dir := t.TempDir()
	state := &fakestate.State{
		ClusterReachable: true,
		NamespaceExists:  true,
		Permissions:      fakestate.Permissions{Rules: permissiveRules()},
		Deployment:       readyDeployment("c3", 1),
	}
	if seed != nil {
		seed(state)
	}
	if err := fakestate.Save(dir, state); err != nil {
		t.Fatalf("写入初始状态: %v", err)
	}
	return dir
}

// runFake 真实执行 fake kubectl 进程。
func runFake(t *testing.T, dir string, args ...string) result {
	t.Helper()
	cmd := exec.Command(fakeKubectlPath, args...)
	cmd.Env = append(os.Environ(), "FAKE_KUBECTL_STATE="+dir)
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		exit, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("执行 fake kubectl 失败: %v", err)
		}
		code = exit.ExitCode()
	}
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// writeLeaseFile 写出一份 Lease 清单（create/replace 的输入）。
func writeLeaseFile(t *testing.T, dir, name, holder, resourceVersion, uid string, annotations map[string]string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if annotations == nil {
		annotations = map[string]string{}
	}
	keys := make([]string, 0, len(annotations))
	for key := range annotations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var block strings.Builder
	if len(keys) == 0 {
		block.WriteString("  annotations: {}\n")
	} else {
		block.WriteString("  annotations:\n")
		for _, key := range keys {
			fmt.Fprintf(&block, "    %q: %q\n", key, annotations[key])
		}
	}
	manifest := fmt.Sprintf(`apiVersion: coordination.k8s.io/v1
kind: Lease
metadata:
  name: surgecart-deploy-lock
  namespace: surgecart
  resourceVersion: %q
  uid: %q
%s
spec:
  holderIdentity: %q
  acquireTime: "2026-11-08T00:00:00Z"
  renewTime: "2026-11-08T00:00:00Z"
  leaseDurationSeconds: 30
  leaseTransitions: 1
`, resourceVersion, uid, block.String(), holder)
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatalf("写 Lease 清单: %v", err)
	}
	return path
}

func requireLease(t *testing.T, dir string) *fakestate.Lease {
	t.Helper()
	state, err := fakestate.Load(dir)
	if err != nil {
		t.Fatalf("读取状态: %v", err)
	}
	if state.Lease == nil {
		t.Fatal("Lease 不存在")
	}
	return state.Lease
}

// ---------------------------------------------------------------------------
// C-2 / C-9：真实多进程 CAS 竞争
// ---------------------------------------------------------------------------

// TestConcurrentReplaceHasSingleWinner 覆盖「N 个进程用**同一个** resourceVersion
// 同时 replace，只有一个成功、其余全部 409，且失败者没有覆盖赢家」。
//
// 这是 CAS 最小语义的真正验证：fake 的 flock 把请求串行化，但**不做**任何版本兜底，
// 因此第二个请求必然因为 rv 已被推进而失败。
func TestConcurrentReplaceHasSingleWinner(t *testing.T) {
	t.Parallel()
	const contenders = 8
	dir := newFakeStateDir(t, func(st *fakestate.State) {
		st.Lease = &fakestate.Lease{
			ResourceVersion: "41",
			UID:             "lease-uid-seed",
			HolderIdentity:  "runner-0",
			AcquireTime:     "2026-11-08T00:00:00Z",
			RenewTime:       "2026-11-08T00:00:00Z",
			DurationSeconds: 30,
			Transitions:     1,
			Annotations:     map[string]string{},
		}
	})

	manifests := make([]string, contenders)
	for i := 0; i < contenders; i++ {
		manifests[i] = writeLeaseFile(t, dir, fmt.Sprintf("replace-%d.yaml", i),
			fmt.Sprintf("runner-%d", i+1), "41", "lease-uid-seed", nil)
	}

	results := make([]result, contenders)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = runFake(t, dir, "replace", "-f", manifests[i])
		}()
	}
	close(start)
	wg.Wait()

	winners := 0
	winner := ""
	for i, res := range results {
		switch res.code {
		case 0:
			winners++
			winner = fmt.Sprintf("runner-%d", i+1)
		case 1:
			if !strings.Contains(res.stderr, "Conflict") {
				t.Fatalf("失败的 replace 必须是 409 Conflict，实际输出:\n%s", res.output())
			}
		default:
			t.Fatalf("replace 退出码 = %d，期望 0 或 1\n%s", res.code, res.output())
		}
	}
	if winners != 1 {
		t.Fatalf("同一 resourceVersion 的并发 replace 必须只有一个成功，实际 %d 个", winners)
	}

	lease := requireLease(t, dir)
	if lease.HolderIdentity != winner {
		t.Fatalf("最终 holder 必须是赢家 %q，实际 %q（失败者覆盖了赢家的写入 = 丢更新）",
			winner, lease.HolderIdentity)
	}
	// 每次成功写入都推进全局计数：rv 必须已经离开 41 且不回退。
	if version(lease.ResourceVersion) <= 41 {
		t.Fatalf("成功写入后 resourceVersion 必须推进（>41），实际 %q", lease.ResourceVersion)
	}

	state, err := fakestate.Load(dir)
	if err != nil {
		t.Fatalf("读取状态: %v", err)
	}
	if state.ResourceVersionSeq != version(lease.ResourceVersion) {
		t.Fatalf("全局计数器(%d)与对象 rv(%d)不一致：全局单调推进被破坏",
			state.ResourceVersionSeq, version(lease.ResourceVersion))
	}
}

// TestConcurrentReadModifyWriteIsSerializable 覆盖「N 个进程各自 GET → PUT 重试」：
// 全部最终成功，但每次成功都推进了全局 rv（没有两个写共用同一个 rv = 没有丢更新）。
func TestConcurrentReadModifyWriteIsSerializable(t *testing.T) {
	t.Parallel()
	const contenders = 6
	const startVersion = 100
	dir := newFakeStateDir(t, func(st *fakestate.State) {
		st.Lease = &fakestate.Lease{
			ResourceVersion: fmt.Sprintf("%d", startVersion),
			UID:             "lease-uid-seed",
			HolderIdentity:  "runner-0",
			RenewTime:       "2026-11-08T00:00:00Z",
			DurationSeconds: 30,
			Transitions:     1,
			Annotations:     map[string]string{},
		}
	})

	var wg sync.WaitGroup
	failures := make(chan string, contenders)
	for i := 0; i < contenders; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 每个进程最多重试 30 次：每次冲突后**重新读取**最新 rv 再写。
			for attempt := 0; attempt < 30; attempt++ {
				read := runFake(t, dir, "get", "lease", "surgecart-deploy-lock",
					"-o", "go-template={{.metadata.resourceVersion}}")
				if read.code != 0 {
					failures <- fmt.Sprintf("runner-%d get 失败: %s", i+1, read.output())
					return
				}
				resourceVersion := strings.TrimSpace(read.stdout)
				manifest := writeLeaseFile(t, dir, fmt.Sprintf("rmw-%d.yaml", i),
					fmt.Sprintf("runner-%d", i+1), resourceVersion, "lease-uid-seed", nil)
				write := runFake(t, dir, "replace", "-f", manifest)
				if write.code == 0 {
					return
				}
				if !strings.Contains(write.stderr, "Conflict") {
					failures <- fmt.Sprintf("runner-%d 遇到非冲突错误: %s", i+1, write.output())
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			failures <- fmt.Sprintf("runner-%d 在 30 次重试内没有成功（活锁）", i+1)
		}()
	}
	wg.Wait()
	close(failures)
	for message := range failures {
		t.Fatal(message)
	}

	state, err := fakestate.Load(dir)
	if err != nil {
		t.Fatalf("读取状态: %v", err)
	}
	if state.Lease == nil {
		t.Fatal("Lease 不存在")
	}
	// 每次成功写入都推进一次全局计数：contenders 次成功 → seq = 起始 + contenders。
	if want := int64(startVersion + contenders); state.ResourceVersionSeq != want {
		t.Fatalf("全局 resourceVersion 计数 = %d，期望 %d（%d 次写入各推进一次，说明有写丢失或重复推进）",
			state.ResourceVersionSeq, want, contenders)
	}
	if state.Lease.ResourceVersion != fmt.Sprintf("%d", state.ResourceVersionSeq) {
		t.Fatalf("对象 rv (%q) 必须等于全局计数 (%d)", state.Lease.ResourceVersion, state.ResourceVersionSeq)
	}
}

// version 把 resourceVersion 解析成整数（测试用）。
func version(raw string) int64 {
	var value int64
	if _, err := fmt.Sscanf(raw, "%d", &value); err != nil {
		return -1
	}
	return value
}

// ---------------------------------------------------------------------------
// C-1：注入型 409 必须推进版本，同一 rv 重试必须继续失败
// ---------------------------------------------------------------------------

func TestInjectedConflictAdvancesResourceVersion(t *testing.T) {
	t.Parallel()
	dir := newFakeStateDir(t, func(st *fakestate.State) {
		st.Lease = &fakestate.Lease{
			ResourceVersion: "7",
			UID:             "lease-uid-seed",
			HolderIdentity:  "runner-0",
			RenewTime:       "2026-11-08T00:00:00Z",
			DurationSeconds: 30,
			Annotations:     map[string]string{},
		}
		// 按调用语义注入一次 409（第 N 次匹配才生效的 Ordinal 用法）。
		st.Injections = []fakestate.Injection{{
			Verb:     "update",
			Group:    "coordination.k8s.io",
			Resource: "leases",
			Status:   409,
			Times:    1,
			Event:    "lease.conflict-injected",
		}}
	})

	// 用陈旧 rv 写：注入 409。
	stale := writeLeaseFile(t, dir, "stale.yaml", "runner-1", "7", "lease-uid-seed", nil)
	first := runFake(t, dir, "replace", "-f", stale)
	first.requireCode(t, 1)
	first.requireContains(t, "Conflict")

	// 409 必须体现「别人已经推进了版本」：服务端 rv 不能还是 7。
	lease := requireLease(t, dir)
	if lease.ResourceVersion == "7" {
		t.Fatal("注入型 409 没有推进 resourceVersion：调用方用同一个 rv 重试就会成功，CAS 冲突无法证伪")
	}
	if lease.HolderIdentity != "runner-0" {
		t.Fatalf("注入的 409 不得改写 holder，实际 %q", lease.HolderIdentity)
	}

	// **同一个** rv 立即重试：必须继续 409（不会被注入放行）。
	retry := runFake(t, dir, "replace", "-f", stale)
	retry.requireCode(t, 1)
	retry.requireContains(t, "Conflict")

	// 重新读取到最新 rv 后写入才允许成功。
	fresh := writeLeaseFile(t, dir, "fresh.yaml", "runner-1", lease.ResourceVersion, "lease-uid-seed", nil)
	ok := runFake(t, dir, "replace", "-f", fresh)
	ok.requireCode(t, 0)
	if got := requireLease(t, dir).HolderIdentity; got != "runner-1" {
		t.Fatalf("重读后写入应成功接管，实际 holder=%q", got)
	}
}

// TestFailedWriteDoesNotTouchRenewTime 覆盖 C-11：失败请求不得改写 renewTime、不得推进 rv。
func TestFailedWriteDoesNotTouchRenewTime(t *testing.T) {
	t.Parallel()
	const renewTime = "2026-11-01T00:00:00Z"
	for _, tc := range []struct {
		name   string
		status int
	}{
		{name: "500", status: 500},
		{name: "timeout", status: 504},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := newFakeStateDir(t, func(st *fakestate.State) {
				st.Lease = &fakestate.Lease{
					ResourceVersion: "12",
					UID:             "lease-uid-seed",
					HolderIdentity:  "runner-0",
					RenewTime:       renewTime,
					DurationSeconds: 30,
					Annotations:     map[string]string{},
				}
				st.Injections = []fakestate.Injection{{
					Verb: "update", Group: "coordination.k8s.io", Resource: "leases",
					Status: tc.status, Times: 1,
				}}
			})

			manifest := writeLeaseFile(t, dir, "renew.yaml", "runner-0", "12", "lease-uid-seed",
				map[string]string{"deploy-lock.surgecart.io/migration-state": "MIGRATION_RUNNING"})
			res := runFake(t, dir, "replace", "-f", manifest)
			res.requireCode(t, 1)

			lease := requireLease(t, dir)
			if lease.RenewTime != renewTime {
				t.Fatalf("失败请求不得改写 renewTime：期望 %q，实际 %q", renewTime, lease.RenewTime)
			}
			if lease.ResourceVersion != "12" {
				t.Fatalf("失败请求不得推进 rv：期望 12，实际 %q", lease.ResourceVersion)
			}
			if len(lease.Annotations) != 0 {
				t.Fatalf("失败请求不得写入注解，实际 %v", lease.Annotations)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// C-2：UID 前置条件与「删除-重建产生新 UID」
// ---------------------------------------------------------------------------

func TestDeleteRecreateYieldsNewUIDAndRejectsStaleUID(t *testing.T) {
	t.Parallel()
	dir := newFakeStateDir(t, nil)

	created := runFake(t, dir, "create", "-f", writeLeaseFile(t, dir, "create.yaml", "runner-1", "", "", nil))
	created.requireCode(t, 0)
	first := requireLease(t, dir)
	if first.UID == "" || first.ResourceVersion == "" {
		t.Fatalf("create 必须产生 uid 与 resourceVersion，实际 %+v", first)
	}

	// -o name 输出对象名（C-4 的 CLI 契约）。
	deleted := runFake(t, dir, "delete", "lease", "surgecart-deploy-lock", "-o", "name")
	deleted.requireCode(t, 0)
	deleted.requireContains(t, "lease.coordination.k8s.io/surgecart-deploy-lock")
	state, err := fakestate.Load(dir)
	if err != nil {
		t.Fatalf("读取状态: %v", err)
	}
	if state.Lease != nil {
		t.Fatalf("delete 后 Lease 必须不存在，实际 %+v", state.Lease)
	}

	// --ignore-not-found：已删除是不算错误；不带该 flag 必须 NotFound。
	runFake(t, dir, "delete", "lease", "surgecart-deploy-lock", "--ignore-not-found").requireCode(t, 0)
	notFound := runFake(t, dir, "delete", "lease", "surgecart-deploy-lock")
	notFound.requireCode(t, 1)
	notFound.requireContains(t, "NotFound")

	// 删除后旧 uid 的 replace 必须失败（对象已不存在）。
	staleReplace := writeLeaseFile(t, dir, "stale.yaml", "runner-2", first.ResourceVersion, first.UID, nil)
	replaced := runFake(t, dir, "replace", "-f", staleReplace)
	replaced.requireCode(t, 1)
	replaced.requireContains(t, "NotFound")

	// 重建：必须产生**新的** uid 与更高的 rv。
	runFake(t, dir, "create", "-f", writeLeaseFile(t, dir, "recreate.yaml", "runner-2", "", "", nil)).requireCode(t, 0)
	second := requireLease(t, dir)
	if second.UID == first.UID {
		t.Fatalf("删除后重建必须产生新 uid，实际仍是 %q（旧持有者用旧 uid 仍能写入）", second.UID)
	}
	if version(second.ResourceVersion) <= version(first.ResourceVersion) {
		t.Fatalf("重建后的 rv (%s) 必须大于旧对象的 rv (%s)", second.ResourceVersion, first.ResourceVersion)
	}

	// 用旧 uid 写新对象：uid 前置条件不匹配 → 409。
	wrongUID := writeLeaseFile(t, dir, "wrong-uid.yaml", "runner-3", second.ResourceVersion, first.UID, nil)
	conflict := runFake(t, dir, "replace", "-f", wrongUID)
	conflict.requireCode(t, 1)
	conflict.requireContains(t, "Conflict", "UID")
	if got := requireLease(t, dir).HolderIdentity; got != "runner-2" {
		t.Fatalf("uid 不匹配的写入不得生效，实际 holder=%q", got)
	}
}

// ---------------------------------------------------------------------------
// C-6 / C-12：GET 返回接管前的快照
// ---------------------------------------------------------------------------

func TestGetReturnsSnapshotBeforeTakeover(t *testing.T) {
	t.Parallel()
	dir := newFakeStateDir(t, func(st *fakestate.State) {
		st.Lease = &fakestate.Lease{
			ResourceVersion: "20",
			UID:             "lease-uid-seed",
			HolderIdentity:  "runner-old",
			RenewTime:       "2026-11-08T00:00:00Z",
			DurationSeconds: 30,
			Annotations:     map[string]string{},
		}
		// 读取之后才被别的 Runner 接管（并发窗口）。
		st.Injections = []fakestate.Injection{{
			Verb: "get", Group: "coordination.k8s.io", Resource: "leases",
			Phase: "after", Takeover: "runner-new", Times: 1,
		}}
	})

	read := runFake(t, dir, "get", "lease", "surgecart-deploy-lock",
		"-o", "go-template={{.spec.holderIdentity}}")
	read.requireCode(t, 0)
	if got := strings.TrimSpace(read.stdout); got != "runner-old" {
		t.Fatalf("GET 必须返回**接管前**的快照（holder=runner-old），实际 %q", got)
	}

	lease := requireLease(t, dir)
	if lease.HolderIdentity != "runner-new" {
		t.Fatalf("接管必须发生在 GET 返回之后并落到状态里，实际 holder=%q", lease.HolderIdentity)
	}
	if version(lease.ResourceVersion) <= 20 {
		t.Fatalf("接管必须推进 resourceVersion，实际 %q", lease.ResourceVersion)
	}
	if lease.Transitions != 1 {
		t.Fatalf("接管必须递增 leaseTransitions，实际 %d", lease.Transitions)
	}
}

// ---------------------------------------------------------------------------
// C-13：apply 按解析出的 Kind 判断，不看文件名
// ---------------------------------------------------------------------------

func TestApplyUsesParsedKindNotFileName(t *testing.T) {
	t.Parallel()
	dir := newFakeStateDir(t, nil)

	jobManifest := `apiVersion: batch/v1
kind: Job
metadata:
  name: surgecart-migrate-fake
  namespace: surgecart
spec:
  parallelism: 1
  completions: 1
`
	// 文件名故意叫 application.yaml，内容却是 Job：必须按 migration 语义处理。
	path := filepath.Join(dir, "application.yaml")
	if err := os.WriteFile(path, []byte(jobManifest), 0o644); err != nil {
		t.Fatalf("写清单: %v", err)
	}
	res := runFake(t, dir, "apply", "-f", path)
	res.requireCode(t, 0)
	res.requireContains(t, "migration applied")

	state, err := fakestate.Load(dir)
	if err != nil {
		t.Fatalf("读取状态: %v", err)
	}
	if !state.MigrationApplied || len(state.Jobs) == 0 {
		t.Fatalf("apply Job 必须产生迁移语义（MigrationApplied/Jobs），实际 applied=%v jobs=%d",
			state.MigrationApplied, len(state.Jobs))
	}
	if state.Applied {
		t.Fatal("job: 内容为 Job 的清单不得被当成 application（Deployment）应用")
	}

	// 未知 Kind：服务端校验失败（C-14），且不得产生任何对象。
	unknown := filepath.Join(dir, "unknown.yaml")
	if err := os.WriteFile(unknown, []byte("apiVersion: v1\nkind: Nope\nmetadata:\n  name: x\n"), 0o644); err != nil {
		t.Fatalf("写清单: %v", err)
	}
	bad := runFake(t, dir, "apply", "-f", unknown)
	bad.requireCode(t, 1)
	bad.requireContains(t, "BadRequest", "Nope")
}

// ---------------------------------------------------------------------------
// A-5：写路径必须走 RBAC，未授权即 403
// ---------------------------------------------------------------------------

func TestWritePathsEnforceRBAC(t *testing.T) {
	t.Parallel()
	dir := newFakeStateDir(t, func(st *fakestate.State) {
		// 只允许读：任何写都必须 403。
		st.Permissions = fakestate.Permissions{Rules: []fakestate.Rule{{
			APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"get", "list"},
		}}}
	})

	create := runFake(t, dir, "create", "-f", writeLeaseFile(t, dir, "create.yaml", "runner-1", "", "", nil))
	create.requireCode(t, 1)
	create.requireContains(t, "Forbidden", "cannot create")

	if state, err := fakestate.Load(dir); err != nil || state.Lease != nil {
		t.Fatalf("被拒绝的 create 不得创建对象（err=%v, lease=%+v）", err, state.Lease)
	}

	// 手工塞一个对象，再验证 replace / delete 也被拒绝。
	state, err := fakestate.Load(dir)
	if err != nil {
		t.Fatalf("读取状态: %v", err)
	}
	state.Lease = &fakestate.Lease{
		ResourceVersion: "3", UID: "lease-uid-seed", HolderIdentity: "runner-0",
		RenewTime: "2026-11-08T00:00:00Z", DurationSeconds: 30, Annotations: map[string]string{},
	}
	if err := fakestate.Save(dir, state); err != nil {
		t.Fatalf("写回状态: %v", err)
	}

	replace := runFake(t, dir, "replace", "-f", writeLeaseFile(t, dir, "replace.yaml", "runner-1", "3", "lease-uid-seed", nil))
	replace.requireCode(t, 1)
	replace.requireContains(t, "Forbidden", "cannot update")

	deleteRes := runFake(t, dir, "delete", "lease", "surgecart-deploy-lock")
	deleteRes.requireCode(t, 1)
	deleteRes.requireContains(t, "Forbidden", "cannot delete")

	if lease := requireLease(t, dir).HolderIdentity; lease != "runner-0" {
		t.Fatalf("被拒绝的写不得生效，实际 holder=%q", lease)
	}

	// 读路径不受写授权影响（读授权由脚本的 auth can-i 预检覆盖）。
	runFake(t, dir, "get", "lease", "surgecart-deploy-lock").requireCode(t, 0)
}

// TestRealRBACManifestDrivesAuthorization 覆盖 C-8：交付的 RBAC 清单可以直接驱动 fake，
// 删除其中任一 (verb, resource) 后 `auth can-i` 必须变成 no。
//
// 断言分两层，避免「清单随审计要求扩充 → 契约测试变成假失败」：
//   - 正例：脚本预检的 7 个 (verb, resource) 必须全 yes（F-01 的回归护栏）；
//   - 负例：从**规则结构**推导一个确实未被授予的组合再断言 no —— 交付清单会扩充
//     （secrets get、pods/log get、deployments/configmaps/services/pvc 都是本工作包
//     补上的），把 `get secrets` 这类负例写死，会在清单补齐后变成假失败。
func TestRealRBACManifestDrivesAuthorization(t *testing.T) {
	t.Parallel()
	path := filepath.Join(repoRootPath, "manifest", "kubernetes", "examples", "deploy-lock", "rbac.yaml")
	requireRBACManifestContract(t, []string{path})

	// 模拟「清单继续扩充」：额外授予 secrets get/list 与 jobs delete。
	// 负例必须依然是推导出来的，否则这一步就会假失败。
	extra := writeManifest(t, t.TempDir(), "extra.yaml", `apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: surgecart-deploy-extra
  namespace: surgecart
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "list"]
  - apiGroups: ["batch"]
    resources: ["jobs"]
    verbs: ["delete"]
`)
	requireRBACManifestContract(t, []string{path, extra})
}

// ruleGrants 是**结构**检查（不调用被测的 Authorize）：规则集里是否存在 allow 规则
// 覆盖 (group, resource, verb)。用于给「默认拒绝」挑选一个确实未被授予的组合。
func ruleGrants(rules []fakestate.Rule, group, resource, verb string) bool {
	for _, rule := range rules {
		if strings.EqualFold(rule.Effect, "deny") {
			continue
		}
		if !contains(rule.APIGroups, group) && !contains(rule.APIGroups, "*") {
			continue
		}
		if !contains(rule.Resources, resource) && !contains(rule.Resources, "*") {
			continue
		}
		if contains(rule.Verbs, verb) || contains(rule.Verbs, "*") {
			return true
		}
	}
	return false
}

func ruleGrantsAny(rules []fakestate.Rule, group, resource string, verbs ...string) bool {
	for _, verb := range verbs {
		if ruleGrants(rules, group, resource, verb) {
			return true
		}
	}
	return false
}

func requireRBACManifestContract(t *testing.T, paths []string) {
	t.Helper()
	rules, err := fakestate.LoadRBACRulesFromPaths(paths)
	if err != nil {
		t.Fatalf("解析 %v: %v", paths, err)
	}
	if len(rules) == 0 {
		t.Fatalf("%v 没有解析出任何规则", paths)
	}
	dir := newFakeStateDir(t, func(st *fakestate.State) {
		st.Permissions = fakestate.Permissions{Rules: rules}
	})

	// 正例：脚本预检的 (verb, resource) 必须全部允许（pods 的 get/list 曾漏过 → F-01）。
	//
	// 注意 leases：交付清单用 resourceNames 限定到锁对象，因此**必须带对象名**才会
	// 返回 yes（T2-06.1.5 / 审计 F-01）。写成不带名字的形式会得到 no —— 那不是授权不足。
	for _, pair := range [][2]string{
		{"get", "leases.coordination.k8s.io/surgecart-deploy-lock"},
		{"create", "leases.coordination.k8s.io"},
		{"update", "leases.coordination.k8s.io/surgecart-deploy-lock"},
		{"get", "jobs.batch"},
		{"list", "jobs.batch"},
		{"get", "pods"},
		{"list", "pods"},
	} {
		res := runFake(t, dir, "auth", "can-i", pair[0], pair[1], "-n", "surgecart")
		res.requireCode(t, 0)
		if got := strings.TrimSpace(res.stdout); got != "yes" {
			t.Fatalf("auth can-i %s %s = %q，期望 yes", pair[0], pair[1], got)
		}
	}

	// 反例：受 resourceNames 限制的 leases 操作，**不带对象名**必须为 no
	// （真实集群同样如此；旧替身在这里放行，掩盖了 F-01）。
	for _, verb := range []string{"get", "update"} {
		res := runFake(t, dir, "auth", "can-i", verb, "leases.coordination.k8s.io", "-n", "surgecart")
		res.requireCode(t, 1)
		if got := strings.TrimSpace(res.stdout); got != "no" {
			t.Fatalf("auth can-i %s leases.coordination.k8s.io（无对象名）= %q，期望 no", verb, got)
		}
	}
	// 反例：错误的对象名同样必须为 no。
	wrong := runFake(t, dir, "auth", "can-i", "get", "leases.coordination.k8s.io/another-pipeline-lock", "-n", "surgecart")
	wrong.requireCode(t, 1)
	if got := strings.TrimSpace(wrong.stdout); got != "no" {
		t.Fatalf("错误对象名的 get leases = %q，期望 no", got)
	}

	// 负例：从规则结构里挑一个**确认未被授予**的组合，断言默认拒绝。
	candidates := []struct{ verb, group, resource string }{
		{"delete", "coordination.k8s.io", "leases"},
		{"list", "", "secrets"},
		{"create", "", "pods"},
		{"watch", "", "pods"},
		{"delete", "batch", "jobs"},
		{"create", "apps", "statefulsets"},
	}
	checked := 0
	for _, candidate := range candidates {
		if ruleGrants(rules, candidate.group, candidate.resource, candidate.verb) {
			continue // 清单确实授予了这一项：换下一个组合，不把授权当成拒绝
		}
		resourceArg := candidate.resource
		if candidate.group != "" {
			resourceArg += "." + candidate.group
		}
		res := runFake(t, dir, "auth", "can-i", candidate.verb, resourceArg, "-n", "surgecart")
		res.requireCode(t, 1)
		if got := strings.TrimSpace(res.stdout); got != "no" {
			t.Fatalf("auth can-i %s %s = %q，期望 no（默认拒绝）", candidate.verb, resourceArg, got)
		}
		checked++
	}
	if checked < 2 {
		t.Fatalf("只构造出 %d 个未授权组合，负例覆盖不足（交付清单几乎全授权？）", checked)
	}

	// 其它命名空间不在 Role 的授权范围内（ClusterRole 的规则 Namespaces 为空，不受影响）。
	other := runFake(t, dir, "auth", "can-i", "get", "leases", "-n", "default")
	other.requireCode(t, 1)

	// 「删除 RBAC 任一项 → 行为失败」：删掉 pods 的 list 后预检必须失败。
	trimmed := trimRuleVerb(t, rules, "", "pods", "list")
	dir2 := newFakeStateDir(t, func(st *fakestate.State) {
		st.Permissions = fakestate.Permissions{Rules: trimmed}
	})
	denied := runFake(t, dir2, "auth", "can-i", "list", "pods", "-n", "surgecart")
	denied.requireCode(t, 1)
	if got := strings.TrimSpace(denied.stdout); got != "no" {
		t.Fatalf("删除 pods list 授权后 auth can-i 必须为 no，实际 %q", got)
	}

	// 清单没覆盖的对象（apps/statefulsets）apply 必须 403：fake 不会隐式放行 apply。
	statefulSet := writeManifest(t, dir, "statefulset.yaml",
		"apiVersion: apps/v1\nkind: StatefulSet\nmetadata:\n  name: surgecart\n  namespace: surgecart\n")
	if !ruleGrantsAny(rules, "apps", "statefulsets", "create", "patch", "update") {
		forbidden := runFake(t, dir, "apply", "-f", statefulSet)
		forbidden.requireCode(t, 1)
		forbidden.requireContains(t, "Forbidden")
	}

	// 交付清单覆盖 apply 的真实动词（create/patch）时，apply 必须放行 —— 这一条是
	// A 的「仅交付 RBAC → 发布成功」在 fake 侧的对应断言（apply 走 PATCH，不是 update）。
	if ruleGrantsAny(rules, "apps", "deployments", "create", "patch") {
		deployment := writeManifest(t, dir, "deployment.yaml",
			"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: surgecart\n  namespace: surgecart\n")
		runFake(t, dir, "apply", "-f", deployment).requireCode(t, 0)
	}

	// 显式追加 ManifestWriteRules 后，即使清单没覆盖 statefulsets 也能 apply。
	withWrites := newFakeStateDir(t, func(st *fakestate.State) {
		st.Permissions = fakestate.Permissions{Rules: append(append([]fakestate.Rule(nil), rules...), fakestate.ManifestWriteRules()...)}
	})
	allowed := writeManifest(t, withWrites, "deployment.yaml",
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: surgecart\n  namespace: surgecart\n")
	runFake(t, withWrites, "apply", "-f", allowed).requireCode(t, 0)
}

// trimRuleVerb 从规则集里删掉 (group, resource, verb)：模拟「RBAC 清单少写了一项」。
func trimRuleVerb(t *testing.T, rules []fakestate.Rule, group, resource, verb string) []fakestate.Rule {
	t.Helper()
	out := make([]fakestate.Rule, 0, len(rules))
	removed := false
	for _, rule := range rules {
		next := rule
		if contains(rule.APIGroups, group) && contains(rule.Resources, resource) {
			verbs := make([]string, 0, len(rule.Verbs))
			for _, candidate := range rule.Verbs {
				if candidate == verb {
					removed = true
					continue
				}
				verbs = append(verbs, candidate)
			}
			next.Verbs = verbs
			if len(verbs) == 0 {
				continue
			}
		}
		out = append(out, next)
	}
	if !removed {
		t.Fatalf("规则集里没有 %s/%s 的 %s 授权，无法构造删除场景", group, resource, verb)
	}
	return out
}

// TestAuthCanISupportsNamespaceAndSubresource 覆盖 `auth can-i` 的参数语义：
//   - -n 真正参与判定；
//   - 子资源只能用 `--subresource=` 表达；`pods/log` 里的 "log" 是**对象名**
//     （与真实 kubectl 的 SplitN(args[1], "/", 2) 一致，T2-06.1.5 / 审计 F-01）；
//   - 子资源必须显式授权（只有 "pods" 的规则不授予 "pods/log"）；
//   - 第三个位置参数（`can-i VERB TYPE NAME`）会被真实 kubectl 拒绝，替身同样拒绝。
func TestAuthCanISupportsNamespaceAndSubresource(t *testing.T) {
	t.Parallel()
	dir := newFakeStateDir(t, func(st *fakestate.State) {
		st.Permissions = fakestate.Permissions{Rules: []fakestate.Rule{{
			APIGroups:  []string{""},
			Resources:  []string{"pods/log"},
			Verbs:      []string{"get"},
			Namespaces: []string{"surgecart"},
		}}}
	})

	cases := []struct {
		args []string
		code int
		want string
	}{
		{args: []string{"auth", "can-i", "get", "pods", "--subresource=log", "-n", "surgecart"}, code: 0, want: "yes"},
		// "pods/log" 是「名为 log 的 Pod」，不是子资源：只授予 pods/log 时必须 no。
		{args: []string{"auth", "can-i", "get", "pods/log", "-n", "surgecart"}, code: 1, want: "no"},
		// 子资源必须显式授权：pods 本体没有规则。
		{args: []string{"auth", "can-i", "get", "pods", "-n", "surgecart"}, code: 1, want: "no"},
		// 命名空间参与判定。
		{args: []string{"auth", "can-i", "get", "pods", "--subresource=log", "-n", "default"}, code: 1, want: "no"},
		// 未声明的动词默认拒绝。
		{args: []string{"auth", "can-i", "list", "pods", "--subresource=log", "-n", "surgecart"}, code: 1, want: "no"},
	}
	for _, tc := range cases {
		res := runFake(t, dir, tc.args...)
		res.requireCode(t, tc.code)
		if got := strings.TrimSpace(res.stdout); got != tc.want {
			t.Fatalf("%v = %q，期望 %q", tc.args, got, tc.want)
		}
	}

	// TYPE/NAME 的 NAME 是对象名：授予 pods get 后，`pods/log`（名为 log 的 Pod）允许，
	// 但 `pods --subresource=log` 仍然拒绝（pods/log 子资源没有授权）。
	named := newFakeStateDir(t, func(st *fakestate.State) {
		st.Permissions = fakestate.Permissions{Rules: []fakestate.Rule{{
			APIGroups:  []string{""},
			Resources:  []string{"pods"},
			Verbs:      []string{"get"},
			Namespaces: []string{"surgecart"},
		}}}
	})
	res := runFake(t, named, "auth", "can-i", "get", "pods/log", "-n", "surgecart")
	res.requireCode(t, 0)
	if got := strings.TrimSpace(res.stdout); got != "yes" {
		t.Fatalf("授予 pods get 后，`get pods/log` 应被当作名为 log 的对象（允许），实际 %q", got)
	}
	res = runFake(t, named, "auth", "can-i", "get", "pods", "--subresource=log", "-n", "surgecart")
	res.requireCode(t, 1)
	if got := strings.TrimSpace(res.stdout); got != "no" {
		t.Fatalf("只授予 pods get 时，pods/log 子资源必须拒绝，实际 %q", got)
	}

	// 第三个位置参数：真实 kubectl 报 "you must specify two arguments..."。
	tooMany := runFake(t, dir, "auth", "can-i", "get", "pods", "surgecart-deploy-lock", "-n", "surgecart")
	tooMany.requireCode(t, 1)
	tooMany.requireContains(t, "you must specify two arguments")
}

// TestLoadRBACRulesFromMultipleFiles 覆盖「RBAC 拆成多份物理文件」的载入：
// 并集生效，删掉任一文件里的任一条规则都会让对应 (verb, resource) 变成 no。
func TestLoadRBACRulesFromMultipleFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "01-lock.yaml")
	clusterPath := filepath.Join(dir, "02-cluster.yaml")
	writeManifest(t, dir, "01-lock.yaml", `apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: surgecart-deploy-lock
  namespace: surgecart
rules:
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    resourceNames: ["surgecart-deploy-lock"]
    verbs: ["get", "update", "patch"]
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["create"]
`)
	writeManifest(t, dir, "02-cluster.yaml", `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: surgecart-deploy-cluster
rules:
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["get", "create"]
  - apiGroups: ["authorization.k8s.io"]
    resources: ["selfsubjectaccessreviews"]
    verbs: ["create"]
`)

	rules, err := fakestate.LoadRBACRulesFromPaths([]string{lockPath, clusterPath})
	if err != nil {
		t.Fatalf("LoadRBACRulesFromPaths: %v", err)
	}
	fromDir, err := fakestate.LoadRBACRulesFromDir(dir)
	if err != nil {
		t.Fatalf("LoadRBACRulesFromDir: %v", err)
	}
	if len(rules) == 0 || len(fromDir) != len(rules) {
		t.Fatalf("多文件载入结果不一致：paths=%d dir=%d", len(rules), len(fromDir))
	}

	state := &fakestate.State{Permissions: fakestate.Permissions{Rules: rules}}
	check := func(verb, group, resource, namespace, name string, want fakestate.Decision) {
		t.Helper()
		decision, _ := fakestate.Authorize(state, verb, group, resource, namespace, name)
		if decision != want {
			t.Fatalf("Authorize(%s %s/%s ns=%q name=%q) = %s，期望 %s",
				verb, group, resource, namespace, name, decision, want)
		}
	}
	check("get", "coordination.k8s.io", "leases", "surgecart", knownLeaseName, fakestate.DecisionAllowed)
	check("update", "coordination.k8s.io", "leases", "surgecart", knownLeaseName, fakestate.DecisionAllowed)
	// resourceNames 严格生效：换一个 Lease 名字必须被拒。
	check("get", "coordination.k8s.io", "leases", "surgecart", "other-lock", fakestate.DecisionForbidden)
	// 集群权限来自另一个文件（ClusterRole）。
	check("create", "", "namespaces", "surgecart", "", fakestate.DecisionAllowed)
	check("create", "authorization.k8s.io", "selfsubjectaccessreviews", "", "", fakestate.DecisionAllowed)
	// 未授予的动词默认拒绝。
	check("delete", "coordination.k8s.io", "leases", "surgecart", knownLeaseName, fakestate.DecisionForbidden)

	// 删掉锁文件里的 update 授权 → 必须变成 Forbidden。
	writeManifest(t, dir, "01-lock.yaml", `apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: surgecart-deploy-lock
  namespace: surgecart
rules:
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    resourceNames: ["surgecart-deploy-lock"]
    verbs: ["get", "patch"]
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["create"]
`)
	trimmed, err := fakestate.LoadRBACRulesFromDir(dir)
	if err != nil {
		t.Fatalf("删除规则后重新载入: %v", err)
	}
	state = &fakestate.State{Permissions: fakestate.Permissions{Rules: trimmed}}
	if decision, _ := fakestate.Authorize(state, "update", "coordination.k8s.io", "leases", "surgecart", knownLeaseName); decision != fakestate.DecisionForbidden {
		t.Fatalf("删掉 update 授权后仍判定为 %s（多文件链路失效）", decision)
	}
	// 其它文件授予的权限不受影响。
	if decision, _ := fakestate.Authorize(state, "create", "", "namespaces", "surgecart", ""); decision != fakestate.DecisionAllowed {
		t.Fatalf("删除锁文件的一条规则不得影响集群权限，实际 %s", decision)
	}
}

const knownLeaseName = "surgecart-deploy-lock"

// TestApplyAuthorizationUsesPatchForExistingObjects 覆盖 A 指出的语义差异：
// 真实 kubectl apply 对**已存在**的对象发 PATCH（不是 PUT/update）。
//
//   - 只授予 get+create+patch 的身份必须能 apply 已存在的对象（否则会把
//     「按交付 RBAC 授权」误判成 403，逼调用方过度授权 update）；
//   - 只授予 update 的历史写法仍然可用（兼容）；
//   - 两者都没有 → 403，且错误里报的是 patch（真实的动词）。
func TestApplyAuthorizationUsesPatchForExistingObjects(t *testing.T) {
	t.Parallel()
	deploymentManifest := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: surgecart
  namespace: surgecart
spec:
  replicas: 1
`
	newDeploymentState := func(verbs ...string) string {
		return newFakeStateDir(t, func(st *fakestate.State) {
			st.Deployment = readyDeployment("c3", 1)
			st.Permissions = fakestate.Permissions{Rules: []fakestate.Rule{{
				APIGroups:  []string{"apps"},
				Resources:  []string{"deployments"},
				Verbs:      verbs,
				Namespaces: []string{"surgecart"},
			}}}
		})
	}

	// 1) get + create + patch（kubectl apply 的真实动词组合）
	dir := newDeploymentState("get", "create", "patch")
	path := writeManifest(t, dir, "deployment.yaml", deploymentManifest)
	runFake(t, dir, "apply", "-f", path).requireCode(t, 0)

	// 2) 只授予 update：历史写法仍然可用
	dirUpdate := newDeploymentState("get", "create", "update")
	updatePath := writeManifest(t, dirUpdate, "deployment.yaml", deploymentManifest)
	runFake(t, dirUpdate, "apply", "-f", updatePath).requireCode(t, 0)

	// 3) 只有 get：必须 403，且报 patch（不是 update）
	dirDenied := newDeploymentState("get")
	deniedPath := writeManifest(t, dirDenied, "deployment.yaml", deploymentManifest)
	denied := runFake(t, dirDenied, "apply", "-f", deniedPath)
	denied.requireCode(t, 1)
	denied.requireContains(t, "Forbidden", "cannot patch")

	// can-i apply 的判定：create/patch/update 任一允许即 yes（apply 不是 RBAC 动词）。
	canI := runFake(t, dir, "auth", "can-i", "apply", "deployments.apps", "-n", "surgecart")
	canI.requireCode(t, 0)
	deniedCanI := runFake(t, dirDenied, "auth", "can-i", "apply", "deployments.apps", "-n", "surgecart")
	deniedCanI.requireCode(t, 1)
}

// TestWriteIntentUsesCallerBaseVersion 覆盖一个真实回归：
//
// 后台续租与主流程「写迁移记录」并发时，续租请求携带的是**它读到的**（较旧的）记录，
// 而服务端记录已经被主流程推进。如果写意图按「当前服务端状态」判定，续租会被误判成
// 写记录，按续租注入的故障（409/500）就漏掉了 —— 注入次数会随并发时序漂移。
//
// 正确语义：意图比较的是「调用方读到的那个版本 (metadata.resourceVersion)」与它写出的内容。
func TestWriteIntentUsesCallerBaseVersion(t *testing.T) {
	t.Parallel()
	const prefix = "deploy-lock.surgecart.io/"
	dir := newFakeStateDir(t, func(st *fakestate.State) {
		st.Lease = &fakestate.Lease{
			ResourceVersion: "10",
			UID:             "lease-uid-seed",
			HolderIdentity:  "runner-0",
			RenewTime:       "2026-11-08T00:00:00Z",
			DurationSeconds: 30,
			Transitions:     1,
			Annotations: map[string]string{
				prefix + "migration-state": "MIGRATION_PENDING",
				prefix + "migration-job":   "surgecart-migrate-old",
			},
		}
	})

	// 1) 主流程把记录推进到 RUNNING（holder 不变 → 这是 record 写）。
	record := writeLeaseFile(t, dir, "record.yaml", "runner-0", "10", "lease-uid-seed", map[string]string{
		prefix + "migration-state": "MIGRATION_RUNNING",
		prefix + "migration-job":   "surgecart-migrate-old",
	})
	runFake(t, dir, "replace", "-f", record).requireCode(t, 0)

	// 2) 后台续租用**旧 rv + 旧记录**写（它只读到过 PENDING）。
	//    注入只作用于 intent=renew，并且用 **500**（而不是 409）：这样「注入是否命中」
	//    可以和「CAS 冲突」区分开 —— 若意图被误判成 record，就会看到 CAS 的 409。
	state, err := fakestate.Load(dir)
	if err != nil {
		t.Fatalf("读取状态: %v", err)
	}
	state.Injections = []fakestate.Injection{{
		Verb: "update", Group: "coordination.k8s.io", Resource: "leases",
		Intent: "renew", Status: 500, Message: "injected renew failure", Times: 1,
	}}
	if err := fakestate.Save(dir, state); err != nil {
		t.Fatalf("写入注入: %v", err)
	}

	stale := writeLeaseFile(t, dir, "renew.yaml", "runner-0", "10", "lease-uid-seed", map[string]string{
		prefix + "migration-state": "MIGRATION_PENDING",
		prefix + "migration-job":   "surgecart-migrate-old",
	})
	res := runFake(t, dir, "replace", "-f", stale)
	res.requireCode(t, 1)
	res.requireContains(t, "InternalError", "injected renew failure")
	res.requireNotContains(t, "Conflict")

	// 3) 反向：真正改写记录的写（payload 相对自己的基线变了）必须是 record 意图，
	//    不能被 intent=renew 的注入命中。
	fresh := requireLease(t, dir)
	state, err = fakestate.Load(dir)
	if err != nil {
		t.Fatalf("读取状态: %v", err)
	}
	state.Injections = []fakestate.Injection{{
		Verb: "update", Group: "coordination.k8s.io", Resource: "leases",
		Intent: "renew", Status: 500, Times: 1,
	}}
	if err := fakestate.Save(dir, state); err != nil {
		t.Fatalf("写入注入: %v", err)
	}
	nextRecord := writeLeaseFile(t, dir, "record2.yaml", "runner-0", fresh.ResourceVersion, "lease-uid-seed", map[string]string{
		prefix + "migration-state": "MIGRATION_COMPLETE",
		prefix + "migration-job":   "surgecart-migrate-old",
	})
	runFake(t, dir, "replace", "-f", nextRecord).requireCode(t, 0)

	events := fakestate.Read(dir, fakestate.EventsFileName)
	want := "lease.write kind=record2.yaml"
	found := false
	for _, line := range events {
		if strings.HasPrefix(line, want) && strings.HasSuffix(line, "intent=record") {
			found = true
		}
	}
	if !found {
		t.Fatalf("改写记录的写必须判定为 intent=record；事件:\n%s", strings.Join(events, "\n"))
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writeManifest(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写清单 %s: %v", name, err)
	}
	return path
}
