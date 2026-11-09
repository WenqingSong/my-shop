package deployk8s

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件锁定 T2-06.1.5 的交付契约（审计 F-01）：
//
//	交付的 RBAC 用 resourceNames 限定 Lease 操作，因此**预检必须带对象名**；
//	替身（fake kubectl）必须与真实 RBAC 一样，对「无对象名」与「错误对象名」拒绝。
//
// 场景编号与任务书一致：
//
//	T1  交付 RBAC + 首次部署 → PASS（rbac_write_test.go，本文件补充调用/状态断言）
//	T2  交付 RBAC + 后续升级 → PASS（同上）
//	T3  正确 Lease 名称的 get    → 允许（替身语义：fakestate/rbac_test.go；端到端：本文件）
//	T4  正确 Lease 名称的 update → 允许
//	T5  正确 Lease 名称的 patch  → 允许（替身语义）
//	T6  错误 Lease 名称          → 拒绝
//	T7  无对象名检查受限权限     → 拒绝
//	T8  缺少必要 Lease 权限      → 预检失败（退出码 3）
//	T9  预检失败后的资源写操作   → 0 次
//	T10 不受 resourceNames 限制  → 保持正常语义（fakestate/rbac_test.go）
//
// 另外两条反向契约：
//   - 把交付 RBAC 里 Lease 的 resourceNames 改成错误值 / 删掉那条规则 → 预检必须失败；
//   - 预检通过后权限被撤销（TOCTOU）→ 真实写入必须被拒并 fail-closed（A-5 不被削弱）。

// ---------------------------------------------------------------------------
// 断言工具：真实调用记录（calls.log / events.log）
// ---------------------------------------------------------------------------

// authCanICalls 返回所有 `auth can-i` 调用的原始参数行。
func authCanICalls(f *fixture) []string {
	var out []string
	for _, call := range f.calls() {
		if strings.Contains(call, " auth can-i ") {
			out = append(out, call)
		}
	}
	return out
}

// requireAuthCheck 断言脚本确实发起了某条授权预检（calls.log 是 fake 记录的原始参数）。
func requireAuthCheck(t *testing.T, f *fixture, fragment string) {
	t.Helper()
	for _, call := range f.calls() {
		if strings.Contains(call, fragment) {
			return
		}
	}
	t.Fatalf("缺少授权预检 %q；实际 auth can-i 调用：\n%s",
		fragment, strings.Join(authCanICalls(f), "\n"))
}

// requireNoAuthCheck 断言脚本没有发起某种形态的授权预检（例如给 create 加对象名）。
func requireNoAuthCheck(t *testing.T, f *fixture, fragment string) {
	t.Helper()
	for _, call := range f.calls() {
		if strings.Contains(call, fragment) {
			t.Fatalf("不应出现授权预检 %q；实际: %s", fragment, call)
		}
	}
}

// countCalls 统计包含 fragment 的 kubectl 调用次数。
func countCalls(f *fixture, fragment string) int {
	count := 0
	for _, call := range f.calls() {
		if strings.Contains(call, fragment) {
			count++
		}
	}
	return count
}

// eventIndex 返回第一个以 prefix 开头的事件下标；不存在返回 -1。
func eventIndex(f *fixture, prefix string) int {
	for i, line := range f.events() {
		if strings.HasPrefix(line, prefix) {
			return i
		}
	}
	return -1
}

// requireLeaseReleaseAndRecord 断言发布结束后锁已释放、迁移记录是 COMPLETE。
func requireLeaseReleaseAndRecord(t *testing.T, f *fixture) {
	t.Helper()
	state := f.state()
	if state.Lease == nil {
		t.Fatal("发布结束后 Lease 不存在：锁没有被创建")
	}
	if state.Lease.HolderIdentity != "" {
		t.Fatalf("发布成功后必须释放锁（holderIdentity 置空），实际 holderIdentity=%q", state.Lease.HolderIdentity)
	}
	if got := state.Lease.Annotations[annMigrationState]; got != "MIGRATION_COMPLETE" {
		t.Fatalf("锁上的迁移记录应为 MIGRATION_COMPLETE，实际 %q（全部注解: %#v）", got, state.Lease.Annotations)
	}
	wantJob := "surgecart-migrate-" + f.shas["target"]
	if got := state.Lease.Annotations[annMigrationJob]; got != wantJob {
		t.Fatalf("锁上的迁移 Job 名应为 %q，实际 %q", wantJob, got)
	}
	if got := state.Lease.Annotations[annMigrationUID]; got == "" {
		t.Fatal("锁上的迁移 Job uid 为空：记录不完整")
	}
}

// requireDeliveredRBACPreflightShape 断言预检的「形状」：
//   - 数量 == REQUIRED_PERMISSIONS 的条数（每条一次，不漏不重）；
//   - leases 的 get/update 带对象名；create 不带对象名；
//   - pods/log 用 --subresource 检查（而不是会被解析成对象名的 TYPE/NAME）。
func requireDeliveredRBACPreflightShape(t *testing.T, f *fixture, lockName string) {
	t.Helper()
	entries := requiredPermissionsFromScript(t)
	calls := authCanICalls(f)
	if len(calls) != len(entries) {
		t.Fatalf("auth can-i 调用次数 = %d，期望 %d（REQUIRED_PERMISSIONS 每条一次）；实际：\n%s",
			len(calls), len(entries), strings.Join(calls, "\n"))
	}
	requireAuthCheck(t, f, " auth can-i get leases.coordination.k8s.io/"+lockName+" ")
	requireAuthCheck(t, f, " auth can-i update leases.coordination.k8s.io/"+lockName+" ")
	requireAuthCheck(t, f, " auth can-i create leases.coordination.k8s.io ")
	requireNoAuthCheck(t, f, " auth can-i create leases.coordination.k8s.io/")
	requireAuthCheck(t, f, " auth can-i get pods --subresource=log ")
	requireNoAuthCheck(t, f, " auth can-i get pods/log ")
}

// ---------------------------------------------------------------------------
// T3/T4：预检必须按对象名检查，并且最终发布成功（端到端证据）
// ---------------------------------------------------------------------------

// TestPreflightUsesObjectLevelLeaseChecks 用交付的 RBAC 跑一次完整升级发布，断言：
//   - 预检对 leases 的 get/update 使用 `TYPE/NAME`（对象级）；
//   - create 不带对象名（真实 create 请求没有 name）；
//   - 每条 REQUIRED_PERMISSIONS 恰好检查一次；
//   - 替身记录了 `auth.allowed get leases/surgecart-deploy-lock` 事件。
func TestPreflightUsesObjectLevelLeaseChecks(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	if !injectRules(t, f, deliveredRBACRules(t)) {
		t.Fatalf("%s", writeEnforcementMissing("对象级预检"))
	}

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")
	res.requireSuccess(t)

	requireDeliveredRBACPreflightShape(t, f, "surgecart-deploy-lock")
	f.requireEventContaining(t, "auth.allowed", "get", "leases/surgecart-deploy-lock")
	f.requireEventContaining(t, "auth.allowed", "update", "leases/surgecart-deploy-lock")

	// 写操作次数：Lease 原子 create 一次；迁移与应用清单各 apply 一次。
	if got := countCalls(f, " create -f "); got != 1 {
		t.Fatalf("Lease create 调用次数 = %d，期望 1（原子创建）", got)
	}
	if got := countCalls(f, " apply -f "); got != 2 {
		t.Fatalf("apply -f 调用次数 = %d，期望 2（迁移清单 + 应用清单）；实际：\n%s", got, strings.Join(f.calls(), "\n"))
	}
	requireLeaseReleaseAndRecord(t, f)
	if state := f.state(); !state.Applied || !state.MigrationApplied {
		t.Fatalf("最终状态不完整：applied=%v migrationApplied=%v", state.Applied, state.MigrationApplied)
	}
}

// ---------------------------------------------------------------------------
// T6/T7/T8/T9 + 反向契约：改坏 resourceNames → 预检失败且零资源写入
// ---------------------------------------------------------------------------

// TestWrongResourceNameFailsPreflightBeforeAnyWrite 是 F-01 的反向契约：
// 把交付 RBAC 里 leases 的 resourceNames 改成别的名字（或删掉那条规则），
// 预检必须 fail-closed：退出码 3、错误信息点名对象、并且没有任何资源写入。
func TestWrongResourceNameFailsPreflightBeforeAnyWrite(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(rules []map[string]any) ([]map[string]any, bool)
	}{
		{
			name: "resourceNames 改成错误名称",
			mutate: func(rules []map[string]any) ([]map[string]any, bool) {
				patched := false
				for _, rule := range rules {
					if _, ok := rule["resourceNames"]; !ok {
						continue
					}
					rule["resourceNames"] = []string{"another-pipeline-lock"}
					patched = true
				}
				return rules, patched
			},
		},
		{
			name: "删除带 resourceNames 的 leases 规则（只剩 create）",
			mutate: func(rules []map[string]any) ([]map[string]any, bool) {
				var kept []map[string]any
				removed := false
				for _, rule := range rules {
					if _, ok := rule["resourceNames"]; ok {
						removed = true
						continue
					}
					kept = append(kept, rule)
				}
				return kept, removed
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, fixtureOptions{})
			rules, patched := tc.mutate(deliveredRBACRules(t))
			if !patched {
				t.Fatal("交付的 RBAC 里没有带 resourceNames 的规则：本用例的前提不成立")
			}
			if !injectRules(t, f, rules) {
				t.Fatalf("%s", writeEnforcementMissing("对象名预检"))
			}

			res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

			res.requireCode(t, 3)
			res.requireNoSuccess(t)
			res.requireContains(t,
				"缺少 leases.coordination.k8s.io/surgecart-deploy-lock 的 get 权限",
				"Forbidden",
			)
			res.requireNotContains(t, "权限预检通过")
			f.requireNoEvent(t, "apply.migration")
			f.requireNoEvent(t, "apply.application")
			requireNoResourceMutations(t, f)
			if lease := f.state().Lease; lease != nil {
				t.Fatalf("预检失败时不得创建 Lease，实际: %+v", lease)
			}
		})
	}
}

// TestMissingLeasePermissionFailsPreflightWithZeroWrites 覆盖 T8/T9：
// 对 leases 的 get / update 分别单独拒绝时，预检必须退出 3、点名具体对象与动词，
// 且不产生任何资源写操作（Lease / Job / Deployment 都不动）。
func TestMissingLeasePermissionFailsPreflightWithZeroWrites(t *testing.T) {
	t.Parallel()

	cases := []struct {
		denied string
		verb   string
	}{
		{denied: "get leases", verb: "get"},
		{denied: "update leases", verb: "update"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.denied, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t, fixtureOptions{})
			f.updateState(func(st *fakestate.State) {
				st.Permissions.Denied = []string{tc.denied}
			})

			res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

			res.requireCode(t, 3)
			res.requireNoSuccess(t)
			res.requireContains(t,
				"缺少 leases.coordination.k8s.io/surgecart-deploy-lock 的 "+tc.verb+" 权限",
				"Forbidden",
			)
			res.requireNotContains(t, "权限预检通过")
			f.requireNoEvent(t, "apply.migration")
			f.requireNoEvent(t, "apply.application")
			requireNoResourceMutations(t, f)
			state := f.state()
			if state.Lease != nil || state.MigrationApplied || state.Applied {
				t.Fatalf("预检失败后不得有任何资源变更：lease=%+v migrationApplied=%v applied=%v",
					state.Lease, state.MigrationApplied, state.Applied)
			}
		})
	}
}

// TestPreflightRejectsObjectNameForCreate：create 不能按对象名检查。
// 这不是「脚本写错」的兜底，而是契约：如果将来有人给 create 加上对象名，
// real kubectl + real RBAC 的组合会让它恒为 no（create 请求没有 name）。
// 该断言与 REQUIRED_PERMISSIONS 的 name 字段（create 为空）互为保险。
func TestPreflightRejectsObjectNameForCreate(t *testing.T) {
	t.Parallel()
	entries := requiredPermissionsFromScript(t)
	found := false
	for _, entry := range entries {
		if entry.Verb != "create" || entry.Resource != "leases" {
			continue
		}
		found = true
		if entry.Name != "" {
			t.Fatalf("create leases 的预检带了对象名 %q：真实 create 请求的授权属性没有 name，resourceNames 规则永远匹配不到", entry.Name)
		}
	}
	if !found {
		t.Fatal("REQUIRED_PERMISSIONS 缺少 create leases：权限契约不完整")
	}
}

// ---------------------------------------------------------------------------
// 写路径强制授权（A-5 不被削弱）：预检通过后权限被撤销 → 写入必须被拒
// ---------------------------------------------------------------------------

// TestWritePathDeniedAfterPreflightFailsClosed 保留原
// TestWritePathDeniedByResourceNamesFailsAfterPreflight 的意图（A-5：写路径本身必须
// 授权），但因为预检现在会按对象名检查，静态地把 resourceNames 改错已经会被预检拦住。
// 于是改造成真实的 TOCTOU：预检全部通过 → 取锁写入的替换请求在途时把 update 规则
// 改成另一个对象名 → 写入必须被 API 拒绝、脚本 fail-closed、不开始迁移/应用。
func TestWritePathDeniedAfterPreflightFailsClosed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	if !injectRules(t, f, deliveredRBACRules(t)) {
		t.Fatalf("%s", writeEnforcementMissing("预检通过后的写路径"))
	}
	marker := filepath.Join(f.stateDir, "inflight-lease-renew")
	wrapKubectlOnceWithMarker(t, f, "lease-renew-hold", `*" replace -f "*"lease-renew.yaml"*`, marker, 2*time.Second)

	p := f.start("--migration-timeout", "10", "--rollout-timeout", "10")
	f.waitForFile(t, marker, 60*time.Second)

	// 此刻预检已经结束（预检在任何写之前完成，锁已经创建成功）。
	// 把 leases 的 update 规则换成另一个对象名：只影响后续写入，不影响已完成的预检。
	rules := deliveredRBACRules(t)
	patched := false
	for _, rule := range rules {
		names, ok := rule["resourceNames"]
		if !ok {
			continue
		}
		verbs, _ := rule["verbs"].([]string)
		hasUpdate := false
		for _, verb := range verbs {
			if verb == "update" {
				hasUpdate = true
			}
		}
		if hasUpdate && names != nil {
			rule["resourceNames"] = []string{"another-pipeline-lock"}
			patched = true
		}
	}
	if !patched {
		p.kill()
		t.Fatal("交付的 RBAC 里没有带 resourceNames 的 update 规则：本用例的前提不成立")
	}
	unlock, err := fakestate.Lock(f.stateDir)
	if err != nil {
		p.kill()
		t.Fatalf("获取 fake 状态锁: %v", err)
	}
	injectPermissions(t, f, map[string]any{"rules": rules})
	unlock()

	res := p.wait(t, 120*time.Second)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "权限预检通过", "Forbidden", `cannot update resource "leases"`)
	res.requireContains(t, "即时续租失败")
	f.requireNoEvent(t, "apply.migration")
	f.requireNoEvent(t, "apply.application")
	if state := f.state(); state.MigrationApplied || state.Applied {
		t.Fatalf("写路径被拒后不得继续迁移/应用：migrationApplied=%v applied=%v", state.MigrationApplied, state.Applied)
	}
	// 事件顺序：预检的 allowed 在前，写入的 forbidden 在后 —— 证明拒绝发生在写路径。
	allowed := eventIndex(f, "auth.allowed update leases/surgecart-deploy-lock")
	forbidden := eventIndex(f, "auth.forbidden update leases")
	if allowed < 0 {
		t.Fatalf("缺少预检 allowed 事件；实际事件：\n%s", strings.Join(f.events(), "\n"))
	}
	if forbidden < 0 {
		t.Fatalf("缺少写路径 forbidden 事件；实际事件：\n%s", strings.Join(f.events(), "\n"))
	}
	if forbidden < allowed {
		t.Fatalf("forbidden 事件出现在预检 allowed 之前（%d < %d）：用例没有验证到 TOCTOU", forbidden, allowed)
	}
}

// wrapKubectlOnceWithMarker 在**第一次**命中 pattern 时写 marker 并 sleep，之后原样转发。
// 用于制造「请求在途」的确定窗口：测试看到 marker 后改写 fake 的权限规则。
func wrapKubectlOnceWithMarker(t *testing.T, f *fixture, name, pattern, marker string, delay time.Duration) {
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
      : > %s
      sleep %s
    fi
    ;;
esac
exec "${FAKE_KUBECTL_REAL}" "$@"
`, shellSingleQuote(counter), pattern, shellSingleQuote(marker), fmt.Sprintf("%.3f", delay.Seconds()))
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatalf("写包装器: %v", err)
	}
	prependFakePath(t, f, dir)
}
