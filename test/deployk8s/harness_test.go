// Package deployk8s 是 scripts/deploy-k8s.sh 的**行为测试**（T2-06.1-05）。
//
// 与 test/manifest 的区别：
//   - test/manifest 断言「清单构建产物长什么样」；
//   - 本包真正执行发布脚本，通过 fake kubectl 观察调用顺序、状态变化与退出码，
//     断言「发布语义」：锁是否互斥、降级是否被拒、迁移失败后应用是否保持不变、
//     发布成功判定是否只看本次目标版本。
//
// 每个用例都在临时目录里重建一个最小仓库（脚本 + 命名空间清单 + 独立 git 历史），
// 因此可以自由构造「升级 / 降级 / 分叉 / 历史缺失」等版本场景，不污染工作区。
package deployk8s

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

const repository = "docker.cnb.cool/framework-station/my-shop"

var (
	fakeKubectlPath string
	repoRootPath    string
	buildOnce       sync.Once
)

func TestMain(m *testing.M) {
	if err := setupFakeKubectl(); err != nil {
		fmt.Fprintf(os.Stderr, "准备 fake kubectl 失败: %v\n", err)
		os.Exit(1)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		fmt.Fprintf(os.Stderr, "解析仓库根目录失败: %v\n", err)
		os.Exit(1)
	}
	repoRootPath = root
	os.Exit(m.Run())
}

// setupFakeKubectl 编译 fake kubectl。测试不省略编译步骤：如果替身编译不过，
// 行为测试必须直接失败，而不是跳过。
func setupFakeKubectl() error {
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "my-shop-fakekubectl-")
		if err != nil {
			buildErr = err
			return
		}
		bin := filepath.Join(dir, "kubectl")
		build := exec.Command("go", "build", "-o", bin, "./fakekubectl")
		out, err := build.CombinedOutput()
		if err != nil {
			buildErr = fmt.Errorf("go build ./fakekubectl: %w\n%s", err, out)
			return
		}
		fakeKubectlPath = bin
		buildDir = dir
	})
	return buildErr
}

var (
	buildErr error
	buildDir string
)

// ---------------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------------

type fixtureOptions struct {
	// noGit 不初始化 git 仓库，用于验证「Git 历史不足时 fail-closed」。
	noGit bool
	// tag 是本次发布的镜像版本；默认取 c3（main 上的最新提交）短 SHA。
	tag string
	// firstDeploy 表示线上还没有 Deployment（首发布）。
	firstDeploy bool
	// currentVersion 是「线上正在运行的版本」；默认 c2 短 SHA。
	currentVersion string
}

type fixture struct {
	t        *testing.T
	root     string
	stateDir string
	shas     map[string]string
	env      []string
}

// newFixture 在临时目录里搭出「可以真实跑发布脚本」的最小环境。
func newFixture(t *testing.T, opt fixtureOptions) *fixture {
	t.Helper()

	root := t.TempDir()
	f := &fixture{t: t, root: root}

	copyFixtureFile(t, filepath.Join(repoRootPath, "scripts", "deploy-k8s.sh"),
		filepath.Join(root, "scripts", "deploy-k8s.sh"), 0o755)
	copyFixtureFile(t, filepath.Join(repoRootPath, "manifest", "kubernetes", "shared", "namespace.yaml"),
		filepath.Join(root, "manifest", "kubernetes", "shared", "namespace.yaml"), 0o644)

	f.shas = map[string]string{}
	if !opt.noGit {
		f.shas = initGitHistory(t, root)
	}

	tag := opt.tag
	if tag == "" {
		if opt.noGit {
			t.Fatal("fixture: noGit 场景必须显式提供 tag")
		}
		tag = f.shas["c3"]
	}
	f.shas["target"] = tag

	currentVersion := opt.currentVersion
	if currentVersion == "" {
		currentVersion = f.shas["c2"]
	}

	f.stateDir = filepath.Join(root, ".fake")
	if err := os.MkdirAll(f.stateDir, 0o755); err != nil {
		t.Fatalf("创建状态目录: %v", err)
	}
	writeFixtureManifests(t, f.stateDir, tag)

	state := defaultState(tag, currentVersion)
	state.Deployment.Exists = !opt.firstDeploy
	if opt.firstDeploy {
		state.Deployment = fakestate.Deployment{Exists: false}
	}
	if err := fakestate.Save(f.stateDir, state); err != nil {
		t.Fatalf("写入初始状态: %v", err)
	}

	binDir := filepath.Join(root, ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("创建 bin 目录: %v", err)
	}
	if err := os.Symlink(fakeKubectlPath, filepath.Join(binDir, "kubectl")); err != nil {
		t.Fatalf("链接 fake kubectl: %v", err)
	}

	tmpDir := filepath.Join(root, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatalf("创建 TMPDIR: %v", err)
	}

	f.env = []string{
		"PATH=" + binDir + string(os.PathListSeparator) + basePath(t),
		"HOME=" + root,
		"TMPDIR=" + tmpDir,
		"FAKE_KUBECTL_STATE=" + f.stateDir,
		"CI=",
		"DEPLOY_K8S_ALLOW_DOWNGRADE_IN_CI=",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + filepath.Join(root, "gitconfig"),
	}
	return f
}

func copyFixtureFile(t *testing.T, from, to string, mode os.FileMode) {
	t.Helper()
	raw, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("读取 %s: %v", from, err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatalf("创建目录 %s: %v", filepath.Dir(to), err)
	}
	if err := os.WriteFile(to, raw, mode); err != nil {
		t.Fatalf("写入 %s: %v", to, err)
	}
}

// basePath 返回包含 bash/git/coreutils 的 PATH，但**不含 kubectl**：
// 测试必须确保脚本用的是 fake kubectl，而不是宿主机上真的 kubectl。
func basePath(t *testing.T) string {
	t.Helper()
	var kept []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "kubectl")); err == nil {
			continue
		}
		kept = append(kept, dir)
	}
	if len(kept) == 0 {
		t.Fatal("PATH 中不存在可用目录")
	}
	return strings.Join(kept, string(os.PathListSeparator))
}

// initGitHistory 构造 main（c1→c2→c3）与分叉分支 side（从 c2 出发）。
func initGitHistory(t *testing.T, root string) map[string]string {
	t.Helper()
	runGit := func(args ...string) string {
		base := []string{
			"-C", root,
			"-c", "user.email=test@example.com",
			"-c", "user.name=deploy-test",
			"-c", "commit.gpgsign=false",
		}
		out, err := exec.Command("git", append(base, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %s 失败: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

	runGit("init", "-q", "-b", "main")
	shas := map[string]string{}
	for _, name := range []string{"c1", "c2", "c3"} {
		runGit("commit", "-q", "--allow-empty", "-m", name)
		shas[name] = runGit("rev-parse", "--short", "HEAD")
	}
	runGit("checkout", "-q", "-b", "side", shas["c2"])
	runGit("commit", "-q", "--allow-empty", "-m", "side")
	shas["side"] = runGit("rev-parse", "--short", "HEAD")
	runGit("checkout", "-q", "main")
	return shas
}

// writeFixtureManifests 写出 fake kubectl 会「构建」出来的两个清单。
// 结构与真实 Kustomize 产物同形：脚本的 awk/grep 解析逻辑不变。
func writeFixtureManifests(t *testing.T, dir, tag string) {
	t.Helper()
	migration := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: surgecart
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: surgecart-database-config
  namespace: surgecart
data:
  DATABASE_DEFAULT_HOST: db.example.com
---
apiVersion: batch/v1
kind: Job
metadata:
  name: surgecart-migrate-%[1]s
  namespace: surgecart
  labels:
    app.kubernetes.io/name: surgecart
    app.kubernetes.io/component: database-migration
spec:
  parallelism: 1
  completions: 1
  activeDeadlineSeconds: 5
  template:
    spec:
      containers:
        - name: migrate
          image: %[2]s:%[1]s
`, tag, repository)

	application := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: surgecart
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: surgecart
  namespace: surgecart
  labels:
    app.kubernetes.io/version: "%[1]s"
spec:
  replicas: 1
  template:
    metadata:
      labels:
        app.kubernetes.io/version: "%[1]s"
    spec:
      containers:
        - name: app
          image: %[2]s:%[1]s
          envFrom:
            - secretRef:
                name: surgecart-db-credentials
            - secretRef:
                name: surgecart-app-credentials
---
apiVersion: v1
kind: Service
metadata:
  name: surgecart
  namespace: surgecart
spec:
  selector:
    app.kubernetes.io/name: surgecart
`, tag, repository)

	if err := os.WriteFile(filepath.Join(dir, fakestate.MigrationManifest), []byte(migration), 0o644); err != nil {
		t.Fatalf("写入迁移清单: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, fakestate.ApplicationManifest), []byte(application), 0o644); err != nil {
		t.Fatalf("写入应用清单: %v", err)
	}
}

// setJobDeadline 重写迁移清单里的 activeDeadlineSeconds（AUD-04：脚本等待上限与
// Job 自身截止时间必须有明确关系）。
func (f *fixture) setJobDeadline(sec int) {
	f.t.Helper()
	path := filepath.Join(f.stateDir, fakestate.MigrationManifest)
	raw, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatalf("读取迁移清单: %v", err)
	}
	re := regexp.MustCompile(`(?m)^  activeDeadlineSeconds: [0-9]+$`)
	if !re.Match(raw) {
		f.t.Fatalf("迁移清单缺少 activeDeadlineSeconds：\n%s", raw)
	}
	updated := re.ReplaceAll(raw, []byte(fmt.Sprintf("  activeDeadlineSeconds: %d", sec)))
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		f.t.Fatalf("写入迁移清单: %v", err)
	}
}

// readyDeployment 返回一个「已收敛」的 Deployment 状态。
func readyDeployment(tag string, generation int) fakestate.Deployment {
	image := repository + ":" + tag
	return fakestate.Deployment{
		Exists:             true,
		Generation:         generation,
		ObservedGeneration: generation,
		Replicas:           1,
		StatusReplicas:     1,
		UpdatedReplicas:    1,
		AvailableReplicas:  1,
		Image:              image,
		Version:            tag,
		Pods: []fakestate.Pod{{
			Name: "surgecart-7d9f6c5b4-xk2mn", Phase: "Running",
			Version: tag, Image: image, Ready: true,
		}},
	}
}

func defaultState(tag, currentVersion string) *fakestate.State {
	current := readyDeployment(currentVersion, 3)
	return &fakestate.State{
		ClusterReachable: true,
		NamespaceExists:  true,
		Permissions:      fakestate.Permissions{Get: true, Create: true, Update: true, Patch: true},
		Secrets:          []string{"surgecart-db-credentials", "surgecart-app-credentials"},
		Job: fakestate.Job{
			Name:     "surgecart-migrate-" + tag,
			UID:      "job-uid-" + tag,
			Exists:   true,
			Complete: true,
			Logs:     "migrate: applied 37 migrations\n",
		},
		Deployment:           current,
		DeploymentAfterApply: []fakestate.Deployment{readyDeployment(tag, 4)},
		ApplyFailures:        map[string]bool{},
		ApplyErrorsApplied:   map[string]bool{},
	}
}

// ---------------------------------------------------------------------------
// 状态与断言
// ---------------------------------------------------------------------------

// retag 改变「本次发布的镜像版本」（重写两个清单），用于在同一份 git 历史里
// 构造升级/降级/分叉等不同目标版本。
func (f *fixture) retag(tag string) {
	f.t.Helper()
	f.shas["target"] = tag
	writeFixtureManifests(f.t, f.stateDir, tag)
}

func (f *fixture) state() *fakestate.State {
	f.t.Helper()
	state, err := fakestate.Load(f.stateDir)
	if err != nil {
		f.t.Fatalf("读取 fake 状态: %v", err)
	}
	return state
}

func (f *fixture) updateState(fn func(*fakestate.State)) {
	f.t.Helper()
	state := f.state()
	fn(state)
	if err := fakestate.Save(f.stateDir, state); err != nil {
		f.t.Fatalf("写回 fake 状态: %v", err)
	}
}

// 迁移生命周期注解的键（与 scripts/deploy-k8s.sh 的数据契约一致）。
const (
	annMigrationState  = "deploy-lock.surgecart.io/migration-state"
	annMigrationJob    = "deploy-lock.surgecart.io/migration-job"
	annMigrationUID    = "deploy-lock.surgecart.io/migration-job-uid"
	annMigrationDetail = "deploy-lock.surgecart.io/migration-detail"
)

// leaseRecord 返回锁上记录的迁移生命周期（state, job, uid）。
func (f *fixture) leaseRecord() (string, string, string) {
	f.t.Helper()
	lease := f.state().Lease
	if lease == nil {
		f.t.Fatal("Lease 不存在：无法读取迁移生命周期记录")
	}
	return lease.Annotations[annMigrationState], lease.Annotations[annMigrationJob],
		lease.Annotations[annMigrationUID]
}

// setLeaseRecord 直接改写锁上的迁移记录，用于构造「上一轮 Runner 的遗留状态」。
func (f *fixture) setLeaseRecord(state, job, uid string) {
	f.t.Helper()
	f.updateState(func(st *fakestate.State) {
		if st.Lease == nil {
			st.Lease = &fakestate.Lease{ResourceVersion: "1", UID: "lease-uid-seed"}
		}
		if st.Lease.Annotations == nil {
			st.Lease.Annotations = map[string]string{}
		}
		st.Lease.Annotations[annMigrationState] = state
		st.Lease.Annotations[annMigrationJob] = job
		st.Lease.Annotations[annMigrationUID] = uid
	})
}

func (f *fixture) events() []string { return fakestate.Read(f.stateDir, fakestate.EventsFileName) }

func (f *fixture) calls() []string { return fakestate.Read(f.stateDir, fakestate.CallsFileName) }

func (f *fixture) requireEvent(t *testing.T, prefix string) {
	t.Helper()
	for _, line := range f.events() {
		if strings.HasPrefix(line, prefix) {
			return
		}
	}
	t.Fatalf("缺少事件 %q；实际事件序列:\n%s", prefix, strings.Join(f.events(), "\n"))
}

func (f *fixture) requireNoEvent(t *testing.T, prefix string) {
	t.Helper()
	for _, line := range f.events() {
		if strings.HasPrefix(line, prefix) {
			t.Fatalf("不应出现事件 %q；实际事件序列:\n%s", prefix, strings.Join(f.events(), "\n"))
		}
	}
}

// requireEventOrder 断言事件按给定前缀顺序出现（子序列，允许中间夹其他事件）。
func (f *fixture) requireEventOrder(t *testing.T, prefixes ...string) {
	t.Helper()
	index := 0
	for _, line := range f.events() {
		if index < len(prefixes) && strings.HasPrefix(line, prefixes[index]) {
			index++
		}
	}
	if index != len(prefixes) {
		t.Fatalf("事件顺序不符合预期：在 %q 处中断，期望顺序 %v；实际事件序列:\n%s",
			prefixes[index], prefixes, strings.Join(f.events(), "\n"))
	}
}

// countEvents 统计某前缀的事件数量。
func (f *fixture) countEvents(prefix string) int {
	count := 0
	for _, line := range f.events() {
		if strings.HasPrefix(line, prefix) {
			count++
		}
	}
	return count
}

// waitForEvent 等待某个事件出现（用于「进程还在运行时」的时序断言）。
func (f *fixture) waitForEvent(t *testing.T, prefix string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, line := range f.events() {
			if strings.HasPrefix(line, prefix) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待事件 %q 超时（%s）；实际事件序列:\n%s", prefix, timeout, strings.Join(f.events(), "\n"))
}

func (f *fixture) tmpDirEmpty(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.root, "tmp"))
	if err != nil {
		t.Fatalf("读取 TMPDIR: %v", err)
	}
	if len(entries) != 0 {
		var names []string
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("发布结束后 TMPDIR 仍有残留：%v（临时清单/锁状态必须清理）", names)
	}
}

// ---------------------------------------------------------------------------
// 执行
// ---------------------------------------------------------------------------

type result struct {
	code     int
	stdout   string
	stderr   string
	duration time.Duration
}

func (r result) output() string { return r.stdout + r.stderr }

// requireCode 断言退出码，并在不匹配时打印完整输出，便于定位。
func (r result) requireCode(t *testing.T, want int) {
	t.Helper()
	if r.code != want {
		t.Fatalf("退出码 = %d，期望 %d\n--- stdout ---\n%s\n--- stderr ---\n%s",
			r.code, want, r.stdout, r.stderr)
	}
}

func (r result) requireSuccess(t *testing.T) {
	t.Helper()
	r.requireCode(t, 0)
	if !strings.Contains(r.stdout, "DEPLOY_K8S_SUCCESS") {
		t.Fatalf("成功发布必须打印 DEPLOY_K8S_SUCCESS；stdout:\n%s", r.stdout)
	}
}

func (r result) requireNoSuccess(t *testing.T) {
	t.Helper()
	if strings.Contains(r.stdout, "DEPLOY_K8S_SUCCESS") {
		t.Fatalf("发布失败时不得打印 DEPLOY_K8S_SUCCESS；stdout:\n%s", r.stdout)
	}
}

func (r result) requireContains(t *testing.T, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(r.output(), fragment) {
			t.Fatalf("输出缺少 %q\n--- stdout ---\n%s\n--- stderr ---\n%s", fragment, r.stdout, r.stderr)
		}
	}
}

func (r result) requireNotContains(t *testing.T, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if strings.Contains(r.output(), fragment) {
			t.Fatalf("输出不应包含 %q\n--- stdout ---\n%s\n--- stderr ---\n%s", fragment, r.stdout, r.stderr)
		}
	}
}

func (f *fixture) run(args ...string) result {
	f.t.Helper()
	process := f.start(args...)
	return process.wait(f.t, 120*time.Second)
}

type process struct {
	fixture *fixture
	cmd     *exec.Cmd
	stdout  *bytes.Buffer
	stderr  *bytes.Buffer
	done    chan error
	started time.Time
	once    sync.Once
}

func (f *fixture) start(args ...string) *process {
	f.t.Helper()
	script := filepath.Join(f.root, "scripts", "deploy-k8s.sh")
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Dir = f.root
	cmd.Env = f.env
	// 独立进程组：崩溃测试需要能一次性终止脚本与其后台续租进程。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		f.t.Fatalf("启动发布脚本: %v", err)
	}
	p := &process{fixture: f, cmd: cmd, stdout: stdout, stderr: stderr, done: make(chan error, 1), started: time.Now()}
	go func() { p.done <- cmd.Wait() }()
	return p
}

func (p *process) wait(t *testing.T, timeout time.Duration) result {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(timeout):
		p.kill()
		t.Fatalf("发布脚本超过 %s 未退出\n--- stdout ---\n%s\n--- stderr ---\n%s",
			timeout, p.stdout.String(), p.stderr.String())
	}
	return result{
		code:     p.cmd.ProcessState.ExitCode(),
		stdout:   p.stdout.String(),
		stderr:   p.stderr.String(),
		duration: time.Since(p.started),
	}
}

// signal 向主进程发送信号（模拟 CI 停止流水线）。
func (p *process) signal(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if err := p.cmd.Process.Signal(sig); err != nil {
		t.Fatalf("发送信号 %v: %v", sig, err)
	}
}

// kill 终止整个进程组（模拟 Runner 崩溃 / 容器被杀）。
func (p *process) kill() {
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
	}
}

// runDeploy 是大多数用例的入口：跑一次发布并返回结果。
func runDeploy(t *testing.T, f *fixture, args ...string) result {
	t.Helper()
	return f.run(args...)
}
