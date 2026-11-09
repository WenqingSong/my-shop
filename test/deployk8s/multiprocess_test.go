package deployk8s

// 本文件覆盖 T2-06.1.4 / B-1、B-2、B-3、B-4、B-10、B-11、B-13 的**真实多进程**验证，
// 以及「时间语义」修复前后的可证明差别。
//
// 与其它测试文件的区别：这里的用例真的启动**多个** `scripts/deploy-k8s.sh` 进程，
// 共享同一个 fake kubectl 状态目录（fake 的 create/replace 用 flock 串行化，因此
// 「两个 Runner 同时抢锁」在这里是真实的两条执行流，而不是同一个测试进程里的两个 goroutine）。
//
// 时间语义（B-1）：
//
//	t_req  ：客户端构造并发出 Create/PUT 的时刻（本地时钟）
//	t_acc  ：API Server 接受请求的时刻（服务端时钟，**不可观测**）
//	t_resp ：客户端收到响应的时刻（本地时钟）
//
// 必须证明的两件事：
//   - create 写入服务器的 renewTime 不能是「脚本启动时刻」：预检耗时 D 会直接吃掉
//     租期窗口（审计实测 D=51s、租期 45s → 创建即过期 → exit 3）。修复前本文件的
//     TestMultiprocessPreflightDelayKeepsRenewTimeFresh 会以 exit 3 失败；
//   - create 返回后必须立即做窗口断言 + 立即续租（B-2），否则 Create 延迟同样会
//     把 renewTime 留在 t_req，窗口被请求耗时侵蚀。
//
// 边界（不得夸大）：
//   - fake kubectl 的 flock 不等于 etcd 事务，本文件证明的是**脚本行为**；
//   - 已发出的 kubectl 请求不可撤销 → AUD-02 = NOT_CLOSED、AUD-05 = NOT_CLOSED。
//     TestMultiprocessPausedRunnerWriteLandsAfterTakeover **断言风险仍然存在**，
//     不是断言"已修复"。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// ---------------------------------------------------------------------------
// 按调用语义的延迟/篡改包装（B 自己的测试基建，不改 C 的 harness/fakestate）
// ---------------------------------------------------------------------------

// wrapperSpec 描述要注入的故障。全部按**调用语义**（命令行参数）命中，
// 不依赖 YAML 文件名之外的隐藏约定；命中的调用会先落 marker 再 sleep，
// 因此「请求在途」是真实的进程窗口，而不是假象。
type wrapperSpec struct {
	// preflightDelay：延迟一次 `get namespace`（锁之前），模拟审计里的预检耗时 D。
	preflightDelay time.Duration
	// createDelay：延迟 Lease 的 create 请求（服务端在延迟之后才接受）。
	createDelay time.Duration
	// createSnapshot：create 返回后快照 state.json → state.after-create.json，
	// 用于断言「服务器上的 renewTime 到底是什么」。
	createSnapshot bool
	// staleCreateSeconds > 0：在 create 转发之前把 renewTime/acquireTime 改写成
	// 「now - staleCreateSeconds」，精确复现修复前的 LOCK_ACQUIRED_AT 语义。
	staleCreateSeconds int
	// secretDelay：延迟一次 `get secret`（锁内复检），用于把 Runner 冻结在
	// 「已持锁、尚未开始迁移」的确定位置。
	secretDelay time.Duration
	// applyDelay：延迟一次 `apply application.yaml`，制造真实的在途写请求。
	applyDelay time.Duration
	// migrationApplyDelay：延迟一次 `apply migration.yaml`，用于构造「迁移 apply 在途失锁」。
	migrationApplyDelay time.Duration
	// verifyDelay：在 application apply **返回之后**延迟一次 `get deployment`
	// （发布验证），用于构造「apply 已返回、结果尚未核实」的信号窗口（B-11）。
	verifyDelay time.Duration
	// releaseDelay：延迟一次 `replace -f .../lease-release.yaml`。
	// 用于确定性地把 EXIT trap（cleanup）撑开一个窗口，从而可以
	// 「在清理过程中」再投递信号 —— 这是 T2-06.1.4 清理重入保护的唯一可复现入口。
	releaseDelay time.Duration
}

func secs(d time.Duration) string {
	if d <= 0 {
		return "0"
	}
	return fmt.Sprintf("%.3f", d.Seconds())
}

// installWrapper 用包装脚本替换 fixture 里的 kubectl 符号链接。
//
// 包装仍然把调用转发给真正的 fake kubectl（FAKE_KUBECTL_REAL），因此 fake 的语义
// 不变：延迟只影响「请求何时到达」，不改变「请求是否生效」。
func installWrapper(t *testing.T, f *fixture, spec wrapperSpec) {
	t.Helper()
	path := filepath.Join(f.root, ".bin", "kubectl")
	if err := os.Remove(path); err != nil {
		t.Fatalf("移除 kubectl 符号链接: %v", err)
	}
	script := fmt.Sprintf(`#!/usr/bin/env bash
set -u
args="$*"
STATE="${FAKE_KUBECTL_STATE}"
REAL="${FAKE_KUBECTL_REAL}"
printf '%%s\t%%s\n' "$(date +%%s%%3N)" "$args" >> "${STATE}/timing.log"

# 1) 预检延迟（只延迟一次）：锁之前的耗时，直接决定「create 距脚本启动多久」。
case " $args " in
  *" get namespace "*)
    if [ ! -f "${STATE}/.marker.preflight" ]; then
      : > "${STATE}/.marker.preflight"
      sleep %[1]s
    fi
    ;;
esac

# 2) Lease create：可选的陈旧锚点篡改 + 请求延迟 + 返回后快照。
create_file=""
for a in "$@"; do
  case "$a" in
    *lease-create.yaml) create_file="$a" ;;
  esac
done
if [ -n "$create_file" ]; then
  if [ "%[2]s" != "0" ]; then
    stale_epoch=$(( $(date +%%s) - %[2]s ))
    stale_ts=$(date -u -d "@${stale_epoch}" +%%Y-%%m-%%dT%%H:%%M:%%SZ)
    sed -i "s|^  renewTime: .*|  renewTime: \"${stale_ts}\"|" "$create_file"
    sed -i "s|^  acquireTime: .*|  acquireTime: \"${stale_ts}\"|" "$create_file"
  fi
  if [ "%[3]s" != "0" ]; then sleep %[3]s; fi
fi

# 3) Secret 复检延迟（只延迟一次）：把 Runner 冻结在「已持锁、未开始迁移」。
case " $args " in
  *" get secret "*)
    if [ ! -f "${STATE}/.marker.secret" ]; then
      : > "${STATE}/.marker.secret"
      sleep %[4]s
    fi
    ;;
esac

# 4) Application apply 延迟（只延迟一次）：制造真实的在途写请求。
case " $args " in
  *application.yaml*)
    if [ ! -f "${STATE}/.marker.apply" ]; then
      : > "${STATE}/.marker.apply"
      sleep %[5]s
    fi
    ;;
esac

# 4b) Migration apply 延迟（只延迟一次）：构造「迁移 apply 在途时失锁」。
case " $args " in
  *migration.yaml*)
    if [ "%[8]s" != "0" ] && [ ! -f "${STATE}/.marker.migapply" ]; then
      : > "${STATE}/.marker.migapply"
      sleep %[8]s
    fi
    ;;
esac

# 5) apply 返回之后、验证阶段的 get deployment（只延迟一次）：
#    构造「apply 已返回但结果尚未核实」的信号窗口（B-11）。
case " $args " in
  *" get deployment "*)
    if [ -f "${STATE}/.marker.apply.done" ] && [ ! -f "${STATE}/.marker.verify" ]; then
      : > "${STATE}/.marker.verify"
      sleep %[6]s
    fi
    ;;
esac

# 3b) 释放锁（EXIT trap / cleanup 路径）延迟（只延迟一次）：
#     构造「cleanup 正在释放锁时再收到信号」的确定性窗口。
case " $args " in
  *lease-release.yaml*)
    if [ "%[9]s" != "0" ] && [ ! -f "${STATE}/.marker.release" ]; then
      : > "${STATE}/.marker.release"
      sleep %[9]s
    fi
    ;;
esac

"${REAL}" "$@"
rc=$?
case " $args " in
  *application.yaml*) : > "${STATE}/.marker.apply.done" ;;
esac
if [ -n "$create_file" ] && [ "%[7]s" = "1" ]; then
  cp "${STATE}/state.json" "${STATE}/state.after-create.json"
fi
exit $rc
`, secs(spec.preflightDelay), strconv.Itoa(spec.staleCreateSeconds), secs(spec.createDelay),
		secs(spec.secretDelay), secs(spec.applyDelay), secs(spec.verifyDelay),
		boolFlag(spec.createSnapshot), secs(spec.migrationApplyDelay), secs(spec.releaseDelay))
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("写 kubectl 包装: %v", err)
	}
}

func boolFlag(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// ---------------------------------------------------------------------------
// 断言辅助
// ---------------------------------------------------------------------------

// leaseWindow 是从输出里解析出来的一条 LEASE_WINDOW 证据行。
type leaseWindow struct {
	stage     string
	renewTime string
	renew     int64
	duration  int
	now       int64
	remaining int
	verdict   string
}

var leaseWindowRe = regexp.MustCompile(
	`LEASE_WINDOW stage=(\S+) renewTime=(\S+) renew_epoch=(\S+) leaseDurationSeconds=(\d+) now_epoch=(\d+) remaining=(\S+) verdict=(\w+)`)

// requireWindow 解析输出里指定阶段的 LEASE_WINDOW 行；找不到时失败。
func requireWindow(t *testing.T, out, stage string) leaseWindow {
	t.Helper()
	for _, m := range leaseWindowRe.FindAllStringSubmatch(out, -1) {
		if m[1] != stage {
			continue
		}
		remaining, err := strconv.Atoi(m[6])
		if err != nil {
			t.Fatalf("LEASE_WINDOW remaining 不是数字：%q\n原始行：%s", m[6], m[0])
		}
		renew, err := strconv.ParseInt(m[3], 10, 64)
		if err != nil {
			t.Fatalf("LEASE_WINDOW renew_epoch 不是数字：%q\n原始行：%s", m[3], m[0])
		}
		now, err := strconv.ParseInt(m[5], 10, 64)
		if err != nil {
			t.Fatalf("LEASE_WINDOW now_epoch 不是数字：%q\n原始行：%s", m[5], m[0])
		}
		duration, err := strconv.Atoi(m[4])
		if err != nil {
			t.Fatalf("LEASE_WINDOW leaseDurationSeconds 不是数字：%q\n原始行：%s", m[4], m[0])
		}
		return leaseWindow{
			stage: m[1], renewTime: m[2], renew: renew, duration: duration,
			now: now, remaining: remaining, verdict: m[7],
		}
	}
	t.Fatalf("输出里没有 stage=%q 的 LEASE_WINDOW 证据行；实际输出:\n%s", stage, out)
	return leaseWindow{}
}

// waitForLeaseExpiry 等服务器上的租约真正过期（renewTime + duration <= now）。
// 这是「续租链真的停了」的客观判据，不是 sleep 固定时长。
func waitForLeaseExpiry(t *testing.T, f *fixture, timeout time.Duration) time.Time {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		lease := f.state().Lease
		if lease != nil {
			if renewEpoch, err := epochOf(lease.RenewTime); err == nil {
				expiry := renewEpoch + int64(lease.DurationSeconds)
				if time.Now().Unix() >= expiry {
					return time.Unix(expiry, 0)
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	lease := f.state().Lease
	t.Fatalf("%s 内租约没有自然过期（续租链可能没有真正停止）；当前 Lease: %+v", timeout, lease)
	return time.Time{}
}

// epochOf 解析 RFC3339（秒级，UTC）。
func epochOf(rfc3339 string) (int64, error) {
	parsed, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return 0, err
	}
	return parsed.Unix(), nil
}

// pauseGroup / resumeGroup 冻结/恢复**整个进程组**（脚本 + 后台续租进程），
// 模拟 Runner 被长 GC / 节点挂起 / 网络分区导致续租链停止。
func (p *process) pauseGroup(t *testing.T) {
	t.Helper()
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGSTOP); err != nil {
		t.Fatalf("SIGSTOP 进程组: %v", err)
	}
}

func (p *process) resumeGroup(t *testing.T) {
	t.Helper()
	if err := syscall.Kill(-p.cmd.Process.Pid, syscall.SIGCONT); err != nil {
		t.Fatalf("SIGCONT 进程组: %v", err)
	}
}

// shortLeaseArgs 返回「短租期 + 短续租间隔」的合法组合：
// interval(2) + 3×(1+1) + backoff(1) + safety(5) = 14 < 15。
// 用于在测试可接受的时间内观察真实的服务端过期。
func shortLeaseArgs() []string {
	return []string{"--lease-duration", "15", "--lease-renew-interval", "2", "--lease-renew-timeout", "1"}
}

// createRequestTimes 返回 timing.log 里所有 lease-create.yaml create 调用的时刻。
func createRequestTimes(f *fixture) []time.Time {
	var out []time.Time
	for _, entry := range f.timings() {
		if strings.Contains(entry.args, "create") && strings.Contains(entry.args, "lease-create.yaml") {
			out = append(out, entry.at)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// B-1 / B-2：预检耗时不得侵蚀创建时的 renewTime
// ---------------------------------------------------------------------------

// TestMultiprocessPreflightDelayKeepsRenewTimeFresh 是 B-1 的端到端回归：
//
//	脚本启动 → 预检耗时 D=16s → 才 create Lease（租期 15s）
//
// 修复前：renewTime 取脚本启动时刻，create 时已过期 1s 以上 → 后台续租第一次
// 就判定「租约已过期」→ kill TERM → exit 3（审计 B-1 的同型失败）。
// 修复后：renewTime 取 create 请求发出前一刻 → 窗口完整 → 发布成功。
//
// 用例故意让 D > lease_duration，使「修复前必失败」成为确定性结果，而不是概率事件。
func TestMultiprocessPreflightDelayKeepsRenewTimeFresh(t *testing.T) {
	t.Parallel()
	const preflight = 16 * time.Second
	f := newFixture(t, fixtureOptions{})
	installWrapper(t, f, wrapperSpec{preflightDelay: preflight})

	// D(16s) > lease_duration(15s)：旧实现必然在 create 时就已经过期。
	res := runDeploy(t, f, append(shortLeaseArgs(),
		"--migration-timeout", "20", "--rollout-timeout", "20")...)

	res.requireSuccess(t)
	res.requireContains(t, "锁时间锚点", "发布锁窗口已锚定在写入完成之后")

	// 证据 1：预检真的花了 D 秒（否则本用例没有证明力）。
	if res.duration < preflight {
		t.Fatalf("整个发布只用了 %s，短于注入的预检延迟 %s：用例没有真正制造 preflight 窗口",
			res.duration, preflight)
	}
	// 证据 2：create 之后服务器上的窗口仍然是**完整租期**（只被 create 请求耗时消耗）。
	created := requireWindow(t, res.output(), "创建-created")
	if created.verdict != "ok" {
		t.Fatalf("create 之后窗口应仍有效，实际 verdict=%s（%s）", created.verdict, created.renewTime)
	}
	// 15s 租期，允许 3s 的进程调度/请求耗时余量；旧实现这里会是 -1 或更小。
	if created.remaining < created.duration-3 {
		t.Fatalf("create 后剩余窗口 = %ds，期望 >= %ds（renewTime 被写成了脚本启动时刻？）",
			created.remaining, created.duration-3)
	}
	// 证据 3：立即续租把锚点推到写入完成之后，窗口再次接近满值。
	renewed := requireWindow(t, res.output(), "创建-anchor-renewed")
	if renewed.remaining < renewed.duration-3 {
		t.Fatalf("即时续租后剩余窗口 = %ds，期望 >= %ds", renewed.remaining, renewed.duration-3)
	}
	if renewed.renew < created.renew {
		t.Fatalf("即时续租必须把 renewTime 向后推进：before=%s after=%s", created.renewTime, renewed.renewTime)
	}
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "" {
		t.Fatalf("发布结束后锁必须被释放，实际: %+v", lease)
	}
}

// TestMultiprocessStaleRenewTimeAtCreateFailsClosed 精确复现修复前的语义并证明
// **新的创建后断言能识别它**：把 create 清单的 renewTime 改写成 now-60s，
// 脚本必须在 apply 任何资源之前 fail-closed（exit 3），并打印可核验的证据。
//
// 这同时覆盖 B-4：过期锁不得被 release 路径"复活"（改写 renewTime 到 now）。
func TestMultiprocessStaleRenewTimeAtCreateFailsClosed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	installWrapper(t, f, wrapperSpec{staleCreateSeconds: 60})

	res := runDeploy(t, f, "--migration-timeout", "20", "--rollout-timeout", "20")

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t,
		"LEASE_ANCHOR_INVALID",
		"LEASE_WINDOW stage=创建-created",
		"verdict=expired",
		"renew_epoch=",
		"leaseDurationSeconds=",
		"now_epoch=",
		"fail-closed",
	)
	// 证据必须自证：renew_epoch + duration <= now_epoch。
	created := requireWindow(t, res.output(), "创建-created")
	if created.renew+int64(created.duration) > created.now {
		t.Fatalf("证据行自相矛盾：renew(%d)+duration(%d) > now(%d)，但 verdict=%s",
			created.renew, created.duration, created.now, created.verdict)
	}
	// 失败必须在任何资源变更之前：不 apply 迁移、不 apply 应用。
	f.requireNoEvent(t, "apply.migration")
	f.requireNoEvent(t, "apply.application")
	// B-4：过期锁不得被释放路径改写 renewTime（那是"给过期锁续命"）。
	f.requireNoEvent(t, "lease.release")
	lease := f.state().Lease
	if lease == nil {
		t.Fatal("创建的 Lease 不应被删除（删除会掩盖证据）")
	}
	if lease.RenewTime != created.renewTime {
		t.Fatalf("过期锁的 renewTime 被改写（不得为过期锁续命）：期望 %q，实际 %q",
			created.renewTime, lease.RenewTime)
	}
}

// TestMultiprocessCreateLatencyKeepsWindow 覆盖 B-2 的另一半：Create 请求本身很慢
// （6s）时，服务器上的 renewTime 只能是 t_req（客户端在返回前观测不到 t_acc），
// 因此**必须**在 create 返回后立即续租一次，把窗口重新锚定到写入完成之后。
func TestMultiprocessCreateLatencyKeepsWindow(t *testing.T) {
	t.Parallel()
	const createDelay = 6 * time.Second
	f := newFixture(t, fixtureOptions{})
	installWrapper(t, f, wrapperSpec{createDelay: createDelay, createSnapshot: true})

	res := runDeploy(t, f, "--migration-timeout", "20", "--rollout-timeout", "20")
	res.requireSuccess(t)

	// 1) create 写入服务器的 renewTime 只能 ≈ t_req（请求发出时刻），不能是未来时刻。
	requests := createRequestTimes(f)
	if len(requests) == 0 {
		t.Fatal("timing.log 里没有 create lease 的请求时刻：包装未生效")
	}
	raw, err := os.ReadFile(filepath.Join(f.stateDir, "state.after-create.json"))
	if err != nil {
		t.Fatalf("读取 create 之后的快照: %v", err)
	}
	var state fakestate.State
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("解析 create 之后的快照: %v", err)
	}
	if state.Lease == nil {
		t.Fatal("create 之后快照里没有 Lease")
	}
	anchor, err := epochOf(state.Lease.RenewTime)
	if err != nil {
		t.Fatalf("解析快照 renewTime: %v", err)
	}
	req := requests[0]
	if delta := time.Unix(anchor, 0).Sub(req); delta > 2*time.Second || delta < -2*time.Second {
		t.Fatalf("create 写入的 renewTime(%s) 与请求时刻(%s) 相差 %s：必须锚定在 t_req，且不得写未来时刻",
			state.Lease.RenewTime, req.Format(time.RFC3339), delta)
	}

	// 2) 创建后的窗口断言通过（6s 延迟没有让租约过期）。
	created := requireWindow(t, res.output(), "创建-created")
	if created.verdict != "ok" {
		t.Fatalf("Create 延迟 6s 后租约不应过期，实际 verdict=%s", created.verdict)
	}

	// 3) 立即续租把 renewTime 推到写入完成之后：窗口重新接近满值
	//    （若只写 t_req 而不立即续租，这里会残留约 createDelay 的损耗）。
	renewed := requireWindow(t, res.output(), "创建-anchor-renewed")
	if renewed.remaining < renewed.duration-int(createDelay.Seconds())-1 {
		t.Fatalf("即时续租后剩余窗口 = %ds，期望 >= %ds（续租没有把锚点推到写入完成之后？）",
			renewed.remaining, renewed.duration-int(createDelay.Seconds())-1)
	}
	// 4) 锚定续租必须发生在第一次迁移 apply 之前（否则首个迁移已经用了一把紧窗口的锁）。
	f.requireEventOrder(t, "lease.acquire", "lease.renew", "apply.migration")
}

// ---------------------------------------------------------------------------
// 续租链停止 → 服务端过期 → 另一个 Runner 接管
// ---------------------------------------------------------------------------

// TestMultiprocessRenewalStallExpiresAndTakeoverSucceeds 用两个真实进程验证：
//
//	A 持锁后被冻结（SIGSTOP 整个进程组，续租链停止）→ 服务端租约过期
//	→ B 在自己的 --lock-wait-timeout 内观察到过期并接管 → B 完成发布
//	→ A 恢复后必须停止（exit 3），不得继续变更资源。
func TestMultiprocessRenewalStallExpiresAndTakeoverSucceeds(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	installWrapper(t, f, wrapperSpec{secretDelay: 4 * time.Second})

	a := f.start(append(shortLeaseArgs(),
		"--migration-timeout", "30", "--rollout-timeout", "20")...)
	// 冻结在「已持锁、尚未开始迁移」的确定位置：secret 复检的请求正在途中。
	f.waitForFile(t, filepath.Join(f.stateDir, ".marker.secret"), 30*time.Second)
	time.Sleep(300 * time.Millisecond) // 让脚本真正进入 sleep（请求在途）
	a.pauseGroup(t)

	// 续租链已经停止：等服务端的 renewTime + duration 真正过期（客观判据）。
	waitForLeaseExpiry(t, f, 30*time.Second)

	b := runDeploy(t, f, "--lock-wait-timeout", "40", "--migration-timeout", "20", "--rollout-timeout", "20")
	b.requireSuccess(t)
	b.requireContains(t, "发布锁已过期")

	// A 恢复：必须停止，且不再发出应用变更。
	a.resumeGroup(t)
	res := a.wait(t, 90*time.Second)
	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	if !strings.Contains(res.output(), "已不属于本 Runner") &&
		!strings.Contains(res.output(), "已过期") &&
		!strings.Contains(res.output(), "发布锁续租失败") {
		t.Fatalf("恢复后的 Runner 必须明确报告失锁/过期；实际输出:\n%s", res.output())
	}
	if got := f.countEvents("apply.application"); got != 1 {
		t.Fatalf("只允许接管方（B）执行一次 Application apply，实际 %d 次\n事件:\n%s",
			got, strings.Join(f.events(), "\n"))
	}
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "" {
		t.Fatalf("发布结束后锁必须被 B 释放（A 不得覆盖），实际: %+v", lease)
	}
}

// TestSignalsDuringCleanupDoNotCorruptCleanup 是「清理阶段被信号打断」的回归守卫。
//
// 背景：T2-06.1.4 集成期间观察到 2 次（仅在四路 Agent 并发跑测试的高负载下）
// `TestMultiprocessRenewalStallExpiresAndTakeoverSucceeds` 以 **exit 2** 失败，
// stderr 为：
//
//	deploy-k8s.sh: trap: line 2: unexpected EOF while looking for matching `)'
//
// 即 bash 在求值 trap 时报语法错误，用 2 覆盖了本应返回的 3（失锁）。
// 该失败在 12+ 轮全量绿跑（含 4 路并发压测）中无法复现，**根因未得到实验确认**
// （状态 NOT_VERIFIED，见 T2-06.1.4 交付报告）。
//
// 调用链分析（结论：**确实存在重入**，来源是外部信号而不是续租子 shell）：
//   - cleanup 只由 exit 触发，bash 不会重跑 EXIT trap；
//   - 后台续租子 shell **不继承** EXIT trap（实测：EXIT trap 只在主 PID 打印一次）；
//   - stop_lease_renewer 先写 `stop` 再 kill，而续租子 shell 在失败分支里会先查
//     `stop`，因此「cleanup 已开始」时它不会再去 kill 主 shell；
//   - **但外部信号可以**：cleanup 阻塞在 lease_release 的 kubectl replace 上时收到
//     SIGTERM/SIGINT，bash 会在该子进程返回后的下一个命令边界执行信号 trap，于是
//     on_signal 在 cleanup 内部重入。实测（加保护之前）stderr 在**同一进程**里先后
//     出现完整的「收到 SIGTERM」「收到 SIGINT」两轮处理，且 TMP_DIR 残留 ——
//     on_signal 的 exit 把 cleanup 截断了。
//
// 因此脚本新增 CLEANUP_ACTIVE / DEFERRED_SIGNAL_CODE：清理阶段收到信号只记录意图，
// 由 cleanup 收尾时决定退出码（非 0 的既有结论不被覆盖）。
//
// 因果性对照实验（同一台机器、同一用例、各 4 轮）：
//   - 无保护                    → 4/4 失败：TMP_DIR 残留（cleanup 被 on_signal 的 exit 截断）
//   - 保护分支被改成 `(( 0 && …))`（保持文件长度/时序不变，仅令其惰性）
//     → 4/4 失败：TMP_DIR 残留（排除「多几行导致的时序漂移」）
//   - 保护生效                  → 4/4 通过：清理完成、锁只释放一次、TMP_DIR 已删除
//
// 这证明失败原因是重入而非时序噪声。**但**原始 exit 2 / `trap: line 2: unexpected EOF`
// 的形态仍未能复现，根因未得到实验确认（NOT_VERIFIED）。
//
// 断言：
//  1. 退出码保持 143：既有信号结论不得被 bash 的 trap 求值错误（2）或 INT（130）覆盖；
//  2. 信号被延后处理（stderr 出现「清理进行中」），即保护分支确实被走到；
//  3. 清理完成：锁被释放（holder 为空）；
//  4. 释放锁只发生一次（lease_release 幂等：入口 LOCK_HELD 守卫 + 每条分支置 0）；
//  5. TMP_DIR 被删除（清理走到了 `rm -rf` 之后）；
//  6. stderr 不得出现 bash 的 trap 求值错误。
func TestSignalsDuringCleanupDoNotCorruptCleanup(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	// secretDelay：把 Runner 停在「已持锁、未开始迁移」，保证 cleanup 会去释放锁。
	// releaseDelay：把「释放锁」请求延迟 3s，把 cleanup 撑成可投递信号的确定窗口。
	installWrapper(t, f, wrapperSpec{secretDelay: 4 * time.Second, releaseDelay: 3 * time.Second})

	a := f.start(append(shortLeaseArgs(),
		"--migration-timeout", "20", "--rollout-timeout", "20")...)
	f.waitForFile(t, filepath.Join(f.stateDir, ".marker.secret"), 30*time.Second)
	time.Sleep(300 * time.Millisecond) // 让脚本真正进入 sleep（kubectl 请求在途）

	// 第一个信号：触发 on_signal → exit 143 → 进入 cleanup。
	if err := syscall.Kill(a.cmd.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatalf("发送第一个 SIGTERM: %v", err)
	}
	// 等 cleanup 真的在发「释放锁」请求（窗口已打开）。
	f.waitForFile(t, filepath.Join(f.stateDir, ".marker.release"), 30*time.Second)

	// 清理期间连续投递信号（含不同类型），覆盖整个窗口。
	// 进程可能已退出（ESRCH）不算失败：这只说明窗口已结束。
	for i := 0; i < 12; i++ {
		if err := syscall.Kill(a.cmd.Process.Pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			t.Fatalf("清理期间发送 SIGTERM: %v", err)
		}
		if i%3 == 0 {
			if err := syscall.Kill(a.cmd.Process.Pid, syscall.SIGINT); err != nil && err != syscall.ESRCH {
				t.Fatalf("清理期间发送 SIGINT: %v", err)
			}
		}
		time.Sleep(150 * time.Millisecond)
	}

	res := a.wait(t, 90*time.Second)

	// 1) 退出码语义不变。
	res.requireCode(t, 143)

	// 2) 信号被延后处理：保护分支必须被走到。
	// 注意 log_warn 写 stdout、log_error 写 stderr，因此这里必须看合并输出。
	if !strings.Contains(res.output(), "清理进行中") {
		t.Fatalf("清理期间收到信号必须走延后处理分支（否则 cleanup 会被 on_signal 的 exit 截断）；实际输出:\n%s", res.output())
	}
	// 6) 不得出现 bash 的 trap 求值错误（原始失败的表征）。
	if strings.Contains(res.stderr, "unexpected EOF") || strings.Contains(res.stderr, "trap:") {
		t.Fatalf("清理阶段出现 bash trap 求值错误（这正是 T2-06.1.4 观察到但未复现的缺陷）:\n%s", res.stderr)
	}

	// 2) 清理完成：锁已释放。
	lease := f.state().Lease
	if lease == nil {
		t.Fatal("锁对象丢失：cleanup 未完成")
	}
	if lease.HolderIdentity != "" {
		t.Fatalf("锁未被释放（holder=%q）：清理被信号中断了", lease.HolderIdentity)
	}
	// 3) 不重复释放。
	if got := f.countEvents("lease.write kind=lease-release.yaml"); got != 1 {
		t.Fatalf("释放锁的写入次数 = %d，期望恰好 1 次（不重复释放）；事件:\n%s",
			got, strings.Join(f.events(), "\n"))
	}

	// 4) TMP_DIR 已删除。
	entries, err := os.ReadDir(filepath.Join(f.root, "tmp"))
	if err != nil {
		t.Fatalf("读取 TMPDIR: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("TMP_DIR 未清理，残留 %d 项：%v\n--- stderr ---\n%s", len(entries), entries, res.stderr)
	}
}

// TestMultiprocessTakeoverRaceHasSingleWinner 覆盖接管竞争：两个真实进程同时
// 抢一把已过期的锁，CAS 只能有一个成功。
func TestMultiprocessTakeoverRaceHasSingleWinner(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.seedExpiredLease("NO_MIGRATION", "", "")
	// 让赢家持锁足够久（≈6s），使输家在 --lock-wait-timeout=4s 内先退出，
	// 避免「赢家释放后输家再接管」把用例变成两个成功。
	longMigration(f)

	args := []string{"--lock-wait-timeout", "4", "--migration-timeout", "20", "--rollout-timeout", "20"}
	p1 := f.start(args...)
	p2 := f.start(args...)
	r1 := p1.wait(t, 120*time.Second)
	r2 := p2.wait(t, 120*time.Second)

	if got := f.countEvents("lease.acquire"); got != 1 {
		t.Fatalf("接管竞争的赢家必须恰好 1 个，实际 lease.acquire = %d\n事件:\n%s",
			got, strings.Join(f.events(), "\n"))
	}
	winners := 0
	for _, r := range []result{r1, r2} {
		if r.code == 0 {
			winners++
			if !strings.Contains(r.stdout, "DEPLOY_K8S_SUCCESS") {
				t.Fatalf("成功的一方必须打印 DEPLOY_K8S_SUCCESS；stdout:\n%s", r.stdout)
			}
		}
	}
	if winners != 1 {
		t.Fatalf("必须恰好一个 Runner 成功，实际 %d\n--- A ---\n%s\n%s\n--- B ---\n%s\n%s",
			winners, r1.stdout, r1.stderr, r2.stdout, r2.stderr)
	}
	loser := r1
	if r1.code == 0 {
		loser = r2
	}
	loser.requireCode(t, 3)
	loser.requireNoSuccess(t)
}

// ---------------------------------------------------------------------------
// 已知风险：冻结的 Runner 恢复后仍可能写入（AUD-02 / AUD-05 = NOT_CLOSED）
// ---------------------------------------------------------------------------

// TestMultiprocessPausedRunnerWriteLandsAfterTakeover **不断言已修复**，而是断言
// 「风险仍然存在」：Runner A 的 Application apply 请求已经发出、在途中被冻结；
// B 接管并完成发布（甚至已经释放锁）之后，A 恢复执行，它**已经发出的**请求仍然
// 会被 API Server 接受并生效。
//
// 这就是为什么：
//   - 脚本只能阻止「恢复之后的新请求」（A 随后以 exit 3 + APPLICATION_OUTCOME_UNKNOWN 结束）；
//   - 不能声称解决了在途写入（AUD-02 = NOT_CLOSED、AUD-05 = NOT_CLOSED）；
//   - 协作式 Lease 不是 fencing。
func TestMultiprocessPausedRunnerWriteLandsAfterTakeover(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	installWrapper(t, f, wrapperSpec{applyDelay: 6 * time.Second})

	a := f.start("--migration-timeout", "20", "--rollout-timeout", "20")
	// A 的 Application apply 已经发出（marker 写在 sleep 之前）→ 请求在途。
	f.waitForFile(t, filepath.Join(f.stateDir, ".marker.apply"), 60*time.Second)
	time.Sleep(300 * time.Millisecond)
	a.pauseGroup(t)

	// A 的租约过期 → B 合法接管（A 的迁移记录已经是 COMPLETE，Job 也已 Complete）。
	f.expireLease(time.Minute)
	b := runDeploy(t, f, "--lock-wait-timeout", "30", "--migration-timeout", "20", "--rollout-timeout", "20")
	b.requireSuccess(t)
	appliedBeforeResume := f.countEvents("apply.application")
	if appliedBeforeResume != 1 {
		t.Fatalf("B 应当完成一次 Application apply，实际 %d 次\n事件:\n%s",
			appliedBeforeResume, strings.Join(f.events(), "\n"))
	}
	if lease := f.state().Lease; lease == nil || lease.HolderIdentity != "" {
		t.Fatalf("B 结束时应已释放锁（A 的写入紧接着要发生在这个时刻之后），实际: %+v", lease)
	}

	// A 恢复：它已经发出的请求仍然会被接受 —— 这正是未关闭的风险。
	a.resumeGroup(t)
	res := a.wait(t, 120*time.Second)
	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t, "APPLICATION_OUTCOME_UNKNOWN")

	if got := f.countEvents("apply.application"); got != appliedBeforeResume+1 {
		t.Fatalf("A 的在途请求必须在接管之后仍然生效（这是已知未关闭风险）；"+
			"期望 apply.application = %d，实际 %d\n事件:\n%s",
			appliedBeforeResume+1, got, strings.Join(f.events(), "\n"))
	}
	if !f.state().Applied {
		t.Fatal("A 的在途 apply 已经生效，fake 状态必须反映这一事实（不能假装没发生）")
	}
}

// ---------------------------------------------------------------------------
// B-11：apply 已返回、结果尚未核实时的信号必须落到「未知」
// ---------------------------------------------------------------------------

// TestMultiprocessSignalAfterApplyReturnedIsUnknown 覆盖 B-11 的核心场景：
//
//	apply 返回 0（客户端认为成功）→ 信号到达（此时 verify_release 还没跑完）
//
// 正确输出：APPLICATION_OUTCOME_UNKNOWN（"结果尚未核实"）。
// 错误输出（本用例禁止）：
//   - DEPLOY_K8S_SUCCESS / "发布成功"：没有验证，不能声称成功；
//   - "应用清单未被本 Runner 应用" / "未更新"：请求已发出，可能已生效，不能声称未应用。
func TestMultiprocessSignalAfterApplyReturnedIsUnknown(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	installWrapper(t, f, wrapperSpec{verifyDelay: 5 * time.Second})

	p := f.start("--migration-timeout", "20", "--rollout-timeout", "20")
	// marker.verify 在「apply 已返回、验证阶段的 get deployment」的 sleep 之前写下，
	// 因此此刻 APPLICATION_APPLY_RETURNED=1 且 APPLICATION_OUTCOME_VERIFIED=0。
	f.waitForFile(t, filepath.Join(f.stateDir, ".marker.verify"), 60*time.Second)
	p.signal(t, syscall.SIGTERM)
	res := p.wait(t, 90*time.Second)

	res.requireCode(t, 143)
	res.requireNoSuccess(t)
	res.requireContains(t, "APPLICATION_OUTCOME_UNKNOWN", "结果尚未核实")
	// 只匹配"结论性"措辞：脚本自身的说明文字里会出现「不能声称『发布成功』」，
	// 那是解释而不是结论。
	res.requireNotContains(t,
		"DEPLOY_K8S_SUCCESS",
		"发布完成：镜像",
		"APPLICATION_OUTCOME_VERIFIED",
		"应用清单未被本 Runner 应用",
	)
}

// TestMultiprocessObserveAfterRequestDetectsExpiredLease 覆盖 T2-06.1.4 / B-10：
// 「请求发出后」的归属观察不能只看 holderIdentity。租约过期后 holder 仍写着本 Runner，
// 但其他 Runner 此刻已经可以合法接管；只看 holder 会得出「一切正常」的错误结论。
func TestMultiprocessObserveAfterRequestDetectsExpiredLease(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	installWrapper(t, f, wrapperSpec{applyDelay: 3 * time.Second})

	p := f.start("--migration-timeout", "20", "--rollout-timeout", "20")
	// marker 在 sleep 之前写出：Application apply 已经发出（请求在途）。
	f.waitForFile(t, filepath.Join(f.stateDir, ".marker.apply"), 60*time.Second)
	time.Sleep(200 * time.Millisecond)
	f.expireLease(2 * time.Minute) // 服务端租约过期，但 holder 仍是本 Runner

	res := p.wait(t, 120*time.Second)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t,
		"APPLICATION_OUTCOME_UNKNOWN",
		"请求发出后本 Runner 的租约已过期",
		"归属不等于互斥",
	)
	// 已失锁：不得再写 Lease（包括释放）。
	f.requireNoEvent(t, "lease.release")
}

// TestMultiprocessMigrationApplyLockLossMarksUnknown 覆盖 D-1/D-2/B-13：
// 迁移 apply 的请求已经发出、期间失锁（租约过期），不能靠客户端返回码推断 Job 是否创建、
// DDL 是否开始；必须显式标记 MIGRATION_OUTCOME_UNKNOWN，且不做"补偿"（不删 Job、不改记录）。
func TestMultiprocessMigrationApplyLockLossMarksUnknown(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	installWrapper(t, f, wrapperSpec{migrationApplyDelay: 3 * time.Second})

	p := f.start("--migration-timeout", "20", "--rollout-timeout", "20")
	f.waitForFile(t, filepath.Join(f.stateDir, ".marker.migapply"), 60*time.Second)
	time.Sleep(200 * time.Millisecond)
	f.expireLease(2 * time.Minute) // 迁移 apply 在途时租约过期

	res := p.wait(t, 120*time.Second)

	res.requireCode(t, 3)
	res.requireNoSuccess(t)
	res.requireContains(t,
		"MIGRATION_OUTCOME_UNKNOWN",
		"迁移 apply 之后本 Runner 已失去发布锁",
		"请求不可撤销",
	)
	res.requireNotContains(t, "DEPLOY_K8S_SUCCESS")
	// 不得继续写记录 / 应用 / 释放；也不得删除已发出的 Job（请求不可撤销）。
	if lease := f.state().Lease; lease != nil && lease.HolderIdentity == "" {
		t.Fatalf("失锁路径不得释放/清空他人的锁，实际: %+v", lease)
	}
	f.requireNoEvent(t, "apply.application")
}
