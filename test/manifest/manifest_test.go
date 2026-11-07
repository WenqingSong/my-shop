package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"cnb.cool/go-cloud-devops/my-shop/internal/config"
	"cnb.cool/go-cloud-devops/my-shop/internal/termination"
)

// 本文件断言 T2-06 的部署契约。每一条断言都作用在 `kubectl kustomize` 的真实输出上：
// overlay 没有生效、patch 打错对象、镜像没被替换，都会在这里失败。
//
// 断言分组：
//   - 入口独立：Application 不含 Job，Migration 不含 Deployment/Service/PVC；
//   - 版本：同一不可变镜像、Job 名称带同一版本后缀；
//   - 迁移语义：只 `migrate up`、只注入数据库凭据、Job 生命周期有界；
//   - 应用语义：单副本、Recreate、探针、终止预算、存储挂载；
//   - 安全：T2-04 的 SecurityContext 完整继承，无 hostPath / 特权 / Token 挂载；
//   - 配置：所有 ConfigMap 键都必须在 T2-01 契约注册表中，敏感项不得进 ConfigMap；
//   - Secret：模板只有占位符，覆盖全部敏感配置项。

// placeholderTag 是生产 overlay 中的发布版本占位值：仓库内保留它是为了让构建可复现，
// 真实发布必须替换。发布脚本必须显式拒绝该值（见 requireReleaseGuard）。
const placeholderTag = "0000000"

// ---------------------------------------------------------------------------
// 入口独立性
// ---------------------------------------------------------------------------

// TestDeploymentEntriesAreIndependent 断言两个部署入口的职责互不重叠。
func TestDeploymentEntriesAreIndependent(t *testing.T) {
	app := build(t, appEntrypoint)
	migration := build(t, migrationEntrypoint)

	for _, kind := range []string{"Deployment", "Service", "PersistentVolumeClaim"} {
		if len(app.byKind(kind)) == 0 {
			t.Fatalf("Application 入口缺少 %s（实际包含: %s）", kind, strings.Join(app.summary(), ", "))
		}
		if len(migration.byKind(kind)) != 0 {
			t.Fatalf("Migration 入口不得包含 %s：迁移不启动 HTTP/静态资源", kind)
		}
	}
	if len(app.byKind("Job")) != 0 {
		t.Fatal("Application 入口不得包含 Job：迁移必须独立执行，不能在应用发布时被顺带创建")
	}
	if len(migration.byKind("Job")) != 1 {
		t.Fatalf("Migration 入口必须且只能包含 1 个 Job，实际 %d 个", len(migration.byKind("Job")))
	}

	// 两个入口共享同一个 Namespace 与同一份数据库配置（同一资源，不重复定义）。
	for _, set := range []manifestSet{app, migration} {
		set.find(t, "Namespace", "surgecart")
		set.find(t, "ConfigMap", "surgecart-database-config")
	}
	if len(app.byKind("Namespace")) != 1 || len(migration.byKind("Namespace")) != 1 {
		t.Fatal("两个入口都必须且只能产出一个 Namespace 资源")
	}
}

// ---------------------------------------------------------------------------
// 镜像版本与 Job 更新机制
// ---------------------------------------------------------------------------

// TestVersionedImmutableImage 断言：镜像不可变、两个入口版本一致、Job 名称带版本后缀。
func TestVersionedImmutableImage(t *testing.T) {
	appImage := containerImage(t, appDeployment(t), "app")
	job := migrationJob(t)
	jobImage := containerImage(t, job, "migrate")

	if appImage != jobImage {
		t.Fatalf("应用与迁移镜像版本不一致：app=%q migration=%q；迁移与应用必须来自同一次发布", appImage, jobImage)
	}

	const repository = "docker.cnb.cool/framework-station/my-shop"
	if !strings.HasPrefix(appImage, repository+":") && !strings.HasPrefix(appImage, repository+"@") {
		t.Fatalf("镜像仓库必须是 %s，实际 %q", repository, appImage)
	}

	tag := imageTag(appImage)
	for _, forbidden := range []string{"latest", "unreleased"} {
		if tag == forbidden {
			t.Fatalf("镜像标签 %q 不是可追溯的发布版本：请替换为短 SHA 或 digest", tag)
		}
	}
	if !isImmutableReference(appImage) {
		t.Fatalf("镜像引用 %q 不是不可变形式（期望短 SHA/digest，禁止浮动标签）", appImage)
	}
	if tag == placeholderTag {
		// 仓库内保留占位标签是为了让构建可复现；真实发布必须替换。
		// 这条「必须有人拦截占位值」的约束由发布脚本承担，避免把模板值直接发到集群。
		requireReleaseGuard(t)
		t.Logf("镜像标签为占位值 %q：发布前必须替换为真实短 SHA；scripts/deploy-k8s.sh 会拒绝该值", tag)
	}

	// Job 名称必须携带同一版本后缀：新版本 = 新 Job 名称，绕开 Job 模板不可变限制。
	wantJobName := "surgecart-migrate-" + tag
	if got := nameOf(job); got != wantJobName {
		t.Fatalf("迁移 Job 名称 = %q，期望 %q（版本后缀必须与镜像标签一致）", got, wantJobName)
	}
}

// ---------------------------------------------------------------------------
// Migration Job
// ---------------------------------------------------------------------------

// TestMigrationJobContract 断言迁移 Job 的执行语义与 T2-05 交付一致。
func TestMigrationJobContract(t *testing.T) {
	job := migrationJob(t)
	spec := mapAt(t, job, "spec")

	if got := intOf(t, spec["parallelism"], "job.spec.parallelism"); got != 1 {
		t.Fatalf("parallelism = %d，期望 1（并发保护来自数据库锁，不靠并行度）", got)
	}
	if got := intOf(t, spec["completions"], "job.spec.completions"); got != 1 {
		t.Fatalf("completions = %d，期望 1", got)
	}
	if got := intOf(t, spec["backoffLimit"], "job.spec.backoffLimit"); got != 4 {
		t.Fatalf("backoffLimit = %d，期望 4（T2-05 行为）", got)
	}
	if got := intOf(t, spec["activeDeadlineSeconds"], "job.spec.activeDeadlineSeconds"); got != 900 {
		t.Fatalf("activeDeadlineSeconds = %d，期望 900（避免发布流程无限挂起）", got)
	}
	if got := intOf(t, spec["ttlSecondsAfterFinished"], "job.spec.ttlSecondsAfterFinished"); got != 86400 {
		t.Fatalf("ttlSecondsAfterFinished = %d，期望 86400（保留 24h 排障窗口后清理）", got)
	}

	podSpec := mapAt(t, job, "spec", "template", "spec")
	if got := stringOf(podSpec["restartPolicy"]); got != "Never" {
		t.Fatalf("restartPolicy = %q，期望 Never（重试由 Job controller 以新 Pod 执行）", got)
	}
	if got := boolOf(t, podSpec["automountServiceAccountToken"], "job automountServiceAccountToken"); got {
		t.Fatal("迁移 Job 不得挂载 ServiceAccount Token（不访问 Kubernetes API）")
	}

	container := containerByName(t, job, "migrate")
	if got := stringSlice(t, container["command"], "job container command"); strings.Join(got, " ") != "/app/my-shop" {
		t.Fatalf("command = %v，期望 [/app/my-shop]（绕过 docker-entrypoint.sh，只迁移）", got)
	}
	if got := stringSlice(t, container["args"], "job container args"); strings.Join(got, " ") != "migrate up" {
		t.Fatalf("args = %v，期望 [migrate up]", got)
	}
	for _, forbidden := range []string{"force", "serve"} {
		if strings.Contains(strings.Join(stringSlice(t, container["args"], "args"), " "), forbidden) {
			t.Fatalf("迁移 Job 的 args 不得包含 %q：force 是人工风险操作，serve 会重复启动应用", forbidden)
		}
	}

	// 只允许数据库相关的配置与凭据：迁移不需要 Redis / JWT / 超管 / 七牛。
	configMaps, secrets := envFromNames(t, container)
	if strings.Join(configMaps, ",") != "surgecart-database-config" {
		t.Fatalf("迁移 Job 的 ConfigMap 引用 = %v，期望只有 surgecart-database-config", configMaps)
	}
	if strings.Join(secrets, ",") != "surgecart-db-credentials" {
		t.Fatalf("迁移 Job 的 Secret 引用 = %v，期望只有 surgecart-db-credentials", secrets)
	}

	env := envValues(t, container)
	if env["DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS"] != "true" {
		t.Fatalf("迁移 Job 必须显式启用严格模式，实际 %q", env["DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS"])
	}

	// 迁移进程无长连接，30s 足够释放数据库锁并退出。
	if got := intOf(t, podSpec["terminationGracePeriodSeconds"], "job terminationGracePeriodSeconds"); got != 30 {
		t.Fatalf("迁移 Job 的 terminationGracePeriodSeconds = %d，期望 30（T2-05 行为）", got)
	}

	// 只读根文件系统下唯一的可写路径。
	if !hasVolume(podSpec, "tmp", "Memory") {
		t.Fatal("迁移 Job 的 /tmp 必须是内存型 emptyDir（只读根文件系统下唯一可写路径）")
	}
}

// ---------------------------------------------------------------------------
// Application Deployment
// ---------------------------------------------------------------------------

// TestApplicationDeploymentContract 断言应用 Deployment 的部署形态。
func TestApplicationDeploymentContract(t *testing.T) {
	deployment := appDeployment(t)
	spec := mapAt(t, deployment, "spec")

	if got := intOf(t, spec["replicas"], "deployment.spec.replicas"); got != 1 {
		t.Fatalf("生产副本数 = %d，期望 1（当前存储模型不支持多副本，见 docs/development/kubernetes.md §7）", got)
	}
	if got := stringOf(dig(deployment, "spec", "strategy", "type")); got != "Recreate" {
		t.Fatalf("strategy = %q，期望 Recreate（RWO PVC 无法滚动挂载）", got)
	}

	// selector 不可变：必须与 Pod 模板标签一致，overlay 不得改写。
	selector := mapAt(t, deployment, "spec", "selector", "matchLabels")
	labels := mapAt(t, deployment, "spec", "template", "metadata", "labels")
	for key, want := range selector {
		if got := stringOf(labels[key]); got != stringOf(want) {
			t.Fatalf("Deployment selector %s=%v 与 Pod 模板标签 %v 不一致（selector 不可变）", key, want, labels[key])
		}
	}

	container := containerByName(t, deployment, "app")

	// 命名端口：Service targetPort 与探针都引用 http。
	ports := listAt(t, container, "ports")
	if len(ports) != 1 {
		t.Fatalf("容器端口数量 = %d，期望 1（只暴露 HTTP 8000）", len(ports))
	}
	if got := stringOf(ports[0]["name"]); got != "http" {
		t.Fatalf("容器端口名 = %q，期望 http", got)
	}
	if got := intOf(t, ports[0]["containerPort"], "containerPort"); got != 8000 {
		t.Fatalf("containerPort = %d，期望 8000", got)
	}

	// 配置边界：数据库配置与迁移共用；应用额外取自身配置与两套 Secret。
	configMaps, secrets := envFromNames(t, container)
	if strings.Join(configMaps, ",") != "surgecart-database-config,surgecart-config" {
		t.Fatalf("应用 ConfigMap 引用 = %v，期望 [surgecart-database-config surgecart-config]", configMaps)
	}
	if strings.Join(secrets, ",") != "surgecart-db-credentials,surgecart-app-credentials" {
		t.Fatalf("应用 Secret 引用 = %v，期望 [surgecart-db-credentials surgecart-app-credentials]", secrets)
	}
	if env := envValues(t, container); env["DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS"] != "true" {
		t.Fatalf("应用必须显式启用严格模式，实际 %q", env["DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS"])
	}

	// 资源规格不得省略（省略会变成 BestEffort，节点压力下优先被驱逐）。
	resources := mapAt(t, container, "resources")
	for _, section := range []string{"requests", "limits"} {
		values := mapAt(t, resources, section)
		if len(values) == 0 {
			t.Fatalf("resources.%s 不得为空", section)
		}
	}
}

// TestProbeSemantics 断言探针路径与参数，防止 T2-02 冻结语义被扩散。
func TestProbeSemantics(t *testing.T) {
	app := build(t, appEntrypoint)
	container := containerByName(t, app.single(t, "Deployment"), "app")

	liveness := mapAt(t, container, "livenessProbe")
	readiness := mapAt(t, container, "readinessProbe")
	startup := mapAt(t, container, "startupProbe")

	if got := stringOf(dig(liveness, "httpGet", "path")); got != "/health" {
		t.Fatalf("liveness 路径 = %q，期望 /health（只判进程存活）", got)
	}
	if got := stringOf(dig(readiness, "httpGet", "path")); got != "/ready" {
		t.Fatalf("readiness 路径 = %q，期望 /ready（只判 MySQL/Redis 可达）", got)
	}
	if got := stringOf(dig(startup, "httpGet", "path")); got != "/health" {
		t.Fatalf("startup 路径 = %q，期望 /health", got)
	}

	// 端口必须引用命名端口，避免容器端口号在多处漂移。
	for name, probe := range map[string]map[string]any{"liveness": liveness, "readiness": readiness, "startup": startup} {
		if got := stringOf(dig(probe, "httpGet", "port")); got != "http" {
			t.Fatalf("%s 探针端口 = %q，期望命名端口 http", name, got)
		}
	}

	// 存活判定容忍窗口：连续失败 30s 才重启，且不依赖外部组件。
	livenessWindow := intOf(t, liveness["periodSeconds"], "liveness periodSeconds") *
		intOf(t, liveness["failureThreshold"], "liveness failureThreshold")
	if livenessWindow < 30 {
		t.Fatalf("liveness 失败容忍窗口 = %ds，期望 >= 30s，避免瞬时抖动触发重启", livenessWindow)
	}

	// 就绪摘流窗口：>= 10s（preStop 5s 的传播窗口同量级）。
	readinessWindow := intOf(t, readiness["periodSeconds"], "readiness periodSeconds") *
		intOf(t, readiness["failureThreshold"], "readiness failureThreshold")
	if readinessWindow < 10 {
		t.Fatalf("readiness 失败容忍窗口 = %ds，期望 >= 10s", readinessWindow)
	}

	// 超时必须大于应用内的依赖探测超时（READINESS_PROBE_TIMEOUT，默认 1s），否则探针总在超时。
	env := resolvedEnv(t, app, container)
	probeTimeout, err := strconv.Atoi(env["READINESS_PROBE_TIMEOUT"])
	if err != nil {
		t.Fatalf("READINESS_PROBE_TIMEOUT 不是整数: %q", env["READINESS_PROBE_TIMEOUT"])
	}
	if got := intOf(t, readiness["timeoutSeconds"], "readiness timeoutSeconds"); got <= probeTimeout {
		t.Fatalf("readiness timeoutSeconds = %ds，必须大于应用内探测超时 %ds", got, probeTimeout)
	}

	// 启动窗口必须覆盖依赖重试总预算，否则「启动慢」会被误判为「启动失败」。
	startupWindow := intOf(t, startup["periodSeconds"], "startup periodSeconds") *
		intOf(t, startup["failureThreshold"], "startup failureThreshold")
	dependencyTimeout, err := strconv.Atoi(env["STARTUP_DEPENDENCY_TIMEOUT"])
	if err != nil {
		t.Fatalf("STARTUP_DEPENDENCY_TIMEOUT 不是整数: %q", env["STARTUP_DEPENDENCY_TIMEOUT"])
	}
	if startupWindow < dependencyTimeout {
		t.Fatalf("startupProbe 窗口 = %ds，必须 >= STARTUP_DEPENDENCY_TIMEOUT %ds", startupWindow, dependencyTimeout)
	}

	// Kafka/Outbox 不得进入健康判定：liveness 不能指向 /ready。
	if stringOf(dig(liveness, "httpGet", "path")) == "/ready" {
		t.Fatal("liveness 不得使用 /ready：依赖抖动会让 Kafka/DB 故障升级为进程反复重启")
	}
}

// TestTerminationBudgetMapping 断言 T2-03 终止预算在 Kubernetes 侧的完整映射。
func TestTerminationBudgetMapping(t *testing.T) {
	app := build(t, appEntrypoint)
	deployment := app.single(t, "Deployment")
	podSpec := mapAt(t, deployment, "spec", "template", "spec")
	container := containerByName(t, deployment, "app")

	grace := time.Duration(intOf(t, podSpec["terminationGracePeriodSeconds"], "terminationGracePeriodSeconds")) * time.Second
	if grace != termination.DefaultTerminationGracePeriod {
		t.Fatalf("terminationGracePeriodSeconds = %s，期望 %s（internal/termination 冻结值）", grace, termination.DefaultTerminationGracePeriod)
	}

	// preStop 必须实际执行 5s 摘流等待，且命令必须能在 Alpine/busybox 运行镜像中执行。
	command := stringSlice(t, dig(container, "lifecycle", "preStop", "exec", "command"), "preStop command")
	if len(command) != 3 || command[0] != "/bin/sh" || command[1] != "-c" {
		t.Fatalf("preStop command = %v，期望 [/bin/sh -c \"sleep 5\"]（busybox 保证存在）", command)
	}
	preStopSeconds, err := preStopSleepSeconds(command[2])
	if err != nil {
		t.Fatalf("解析 preStop sleep: %v", err)
	}
	if got := time.Duration(preStopSeconds) * time.Second; got != termination.DefaultPreStopDelay {
		t.Fatalf("preStop 等待 = %s，期望 %s（T2-03 冻结值）", got, termination.DefaultPreStopDelay)
	}

	// 环境变量必须与 preStop / 宽限期同源，否则进程内预算自检会 fail-fast 或预算被悄悄放宽。
	env := resolvedEnv(t, app, container)
	if got := env["LIFECYCLE_PRE_STOP_DELAY"]; got != "5s" {
		t.Fatalf("LIFECYCLE_PRE_STOP_DELAY = %q，期望 5s", got)
	}
	if got := env["LIFECYCLE_TERMINATION_GRACE_PERIOD"]; got != "60s" {
		t.Fatalf("LIFECYCLE_TERMINATION_GRACE_PERIOD = %q，期望 60s", got)
	}

	httpDrain, err := strconv.Atoi(env["SERVER_GRACEFUL_TIMEOUT"])
	if err != nil {
		t.Fatalf("SERVER_GRACEFUL_TIMEOUT 不是整数: %q", env["SERVER_GRACEFUL_TIMEOUT"])
	}
	if got := time.Duration(httpDrain) * time.Second; got != termination.DefaultHTTPDrainTimeout {
		t.Fatalf("SERVER_GRACEFUL_TIMEOUT = %s，期望 %s", got, termination.DefaultHTTPDrainTimeout)
	}

	runtimeShutdown, err := time.ParseDuration(env["FLASH_SALE_SHUTDOWN_TIMEOUT"])
	if err != nil {
		t.Fatalf("FLASH_SALE_SHUTDOWN_TIMEOUT 非法: %v", err)
	}
	if runtimeShutdown != termination.DefaultRuntimeShutdownTimeout {
		t.Fatalf("FLASH_SALE_SHUTDOWN_TIMEOUT = %s，期望 %s", runtimeShutdown, termination.DefaultRuntimeShutdownTimeout)
	}

	budget := termination.Budget{
		PreStopDelay:           time.Duration(preStopSeconds) * time.Second,
		HTTPDrainTimeout:       time.Duration(httpDrain) * time.Second,
		RuntimeShutdownTimeout: runtimeShutdown,
		TerminationGracePeriod: grace,
	}
	if err := budget.Validate(); err != nil {
		t.Fatalf("Kubernetes 侧终止预算不满足契约: %v", err)
	}
	if got := budget.SafetyMargin(); got != termination.DefaultSafetyMargin {
		t.Fatalf("安全余量 = %s，期望 %s（宽限期必须留有 SIGKILL 之前的余量）", got, termination.DefaultSafetyMargin)
	}
}

// TestStorageMounts 断言存储模型：可写路径只有 /tmp 与 /app/storage。
func TestStorageMounts(t *testing.T) {
	app := build(t, appEntrypoint)
	deployment := app.single(t, "Deployment")
	podSpec := mapAt(t, deployment, "spec", "template", "spec")
	container := containerByName(t, deployment, "app")

	env := resolvedEnv(t, app, container)
	storageRoot := env["STORAGE_LOCAL_ROOT"]
	if storageRoot == "" {
		t.Fatal("缺少 STORAGE_LOCAL_ROOT：本地存储目录必须显式指向持久化挂载点")
	}

	mounts := listAt(t, container, "volumeMounts")
	storageMount, ok := mountByName(mounts, "storage")
	if !ok {
		t.Fatal("缺少 storage 挂载：/app/storage 必须在只读根文件系统下可写")
	}
	if got := stringOf(storageMount["mountPath"]); got != storageRoot {
		t.Fatalf("storage 挂载点 = %q，必须等于 STORAGE_LOCAL_ROOT=%q", got, storageRoot)
	}

	if !hasVolume(podSpec, "storage", "") {
		t.Fatal("缺少 storage 卷")
	}
	claim := stringOf(dig(findVolume(t, podSpec, "storage"), "persistentVolumeClaim", "claimName"))
	if claim != "surgecart-storage" {
		t.Fatalf("storage 卷使用 PVC %q，期望 surgecart-storage", claim)
	}

	// PVC：单副本 RWO；不写死 StorageClass（由集群默认或 overlay patch 提供）。
	pvc := build(t, appEntrypoint).find(t, "PersistentVolumeClaim", "surgecart-storage")
	accessModes := stringSlice(t, dig(pvc, "spec", "accessModes"), "pvc accessModes")
	if strings.Join(accessModes, ",") != "ReadWriteOnce" {
		t.Fatalf("PVC accessModes = %v，期望 [ReadWriteOnce]（单副本模型）", accessModes)
	}
	if dig(pvc, "spec", "storageClassName") != nil {
		t.Fatalf("PVC 不得写死 storageClassName=%v：应由集群默认 StorageClass 或 overlay patch 提供", dig(pvc, "spec", "storageClassName"))
	}

	// /tmp 必须是内存型 emptyDir：只读根文件系统下 Go 运行期临时文件需要可写目录。
	if !hasVolume(podSpec, "tmp", "Memory") {
		t.Fatal("/tmp 必须是 medium=Memory 的 emptyDir")
	}
	if _, ok := mountByName(mounts, "tmp"); !ok {
		t.Fatal("/tmp 必须挂载到容器")
	}
}

// ---------------------------------------------------------------------------
// 安全上下文
// ---------------------------------------------------------------------------

// TestSecurityContextInherited 断言 T2-04 的加固策略在 Pod 与容器两级完整继承。
func TestSecurityContextInherited(t *testing.T) {
	app := build(t, appEntrypoint)
	job := migrationJob(t)

	checkPodSecurity(t, "Deployment", mapAt(t, appDeployment(t), "spec", "template", "spec"))
	checkPodSecurity(t, "Job", mapAt(t, job, "spec", "template", "spec"))

	for _, item := range []struct {
		label    string
		resource map[string]any
	}{{"Deployment/app", appDeployment(t)}, {"Job/迁移", job}} {
		for _, container := range listAt(t, mapAt(t, item.resource, "spec", "template", "spec"), "containers") {
			checkContainerSecurity(t, item.label, container)
		}
	}

	// 禁用面：hostNetwork / hostPID / hostIPC / Docker Socket / hostPath 一律不允许。
	for _, set := range []manifestSet{app, jobSet(job)} {
		for _, doc := range set {
			podSpec, ok := dig(doc, "spec", "template", "spec").(map[string]any)
			if !ok {
				continue
			}
			for _, field := range []string{"hostNetwork", "hostPID", "hostIPC"} {
				if value, exists := podSpec[field]; exists && value != false {
					t.Fatalf("%s/%s 不得设置 %s", kindOf(doc), nameOf(doc), field)
				}
			}
			for _, volume := range listAt(t, podSpec, "volumes") {
				if _, exists := volume["hostPath"]; exists {
					t.Fatalf("%s/%s 不得使用 hostPath 卷（%v）", kindOf(doc), nameOf(doc), volume["name"])
				}
			}
		}
	}
}

// TestConfigMapsOnlyCarryContractKeys 断言 ConfigMap 键全部来自 T2-01 契约，且不含敏感项。
func TestConfigMapsOnlyCarryContractKeys(t *testing.T) {
	specs := make(map[string]config.Spec, len(config.Specs()))
	for _, spec := range config.Specs() {
		specs[spec.Env] = spec
	}
	sensitive := make(map[string]bool)
	for _, spec := range config.Specs() {
		if spec.Sensitive {
			sensitive[spec.Env] = true
		}
	}

	app := build(t, appEntrypoint)
	migration := build(t, migrationEntrypoint)

	seen := make(map[string]string)
	for _, set := range []manifestSet{app, migration} {
		for _, doc := range set.byKind("ConfigMap") {
			data := mapAt(t, doc, "data")
			for key := range data {
				if _, ok := specs[key]; !ok {
					t.Fatalf("ConfigMap/%s 的键 %q 未登记在 internal/config 契约注册表：环境变量不会生效", nameOf(doc), key)
				}
				if sensitive[key] {
					t.Fatalf("ConfigMap/%s 不得承载敏感配置 %q（必须走 Secret）", nameOf(doc), key)
				}
				seen[key] = nameOf(doc)
			}
		}
	}

	required := []string{
		"DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS",
		"DATABASE_DEFAULT_HOST", "DATABASE_DEFAULT_PORT", "DATABASE_DEFAULT_USER", "DATABASE_DEFAULT_NAME",
		"REDIS_DEFAULT_ADDRESS",
		"SERVER_ADDRESS", "SERVER_GRACEFUL_TIMEOUT",
		"LIFECYCLE_PRE_STOP_DELAY", "LIFECYCLE_TERMINATION_GRACE_PERIOD",
		"STORAGE_LOCAL_ROOT",
	}
	for _, key := range required {
		if _, ok := seen[key]; !ok {
			t.Fatalf("构建产物缺少必需的配置项 %q", key)
		}
	}

	// 严格模式：开关为 true，且地址不得回退本机。
	appConfig := app.find(t, "ConfigMap", "surgecart-config")
	databaseConfig := app.find(t, "ConfigMap", "surgecart-database-config")
	if got := stringOf(dig(databaseConfig, "data", "DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS")); got != "true" {
		t.Fatalf("DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS = %q，生产必须为 true", got)
	}
	migrationDatabase := migration.find(t, "ConfigMap", "surgecart-database-config")
	appHost := stringOf(dig(databaseConfig, "data", "DATABASE_DEFAULT_HOST"))
	migrationHost := stringOf(dig(migrationDatabase, "data", "DATABASE_DEFAULT_HOST"))
	if appHost != migrationHost {
		t.Fatalf("应用与迁移的数据库地址不一致：app=%q migration=%q", appHost, migrationHost)
	}
	if appHost == "" {
		t.Fatal("DATABASE_DEFAULT_HOST 不得为空（严格模式要求显式外部地址）")
	}
	redisAddress := stringOf(dig(appConfig, "data", "REDIS_DEFAULT_ADDRESS"))
	if redisAddress == "" {
		t.Fatal("REDIS_DEFAULT_ADDRESS 不得为空（严格模式要求显式外部地址）")
	}

	// 生产清单不得出现回环地址与浮动镜像标签。
	for _, set := range []manifestSet{app, migration} {
		for _, doc := range set {
			walkStrings(doc, func(path, value string) {
				lower := strings.ToLower(value)
				for _, forbidden := range []string{"localhost", "127.0.0.1", "::1", ":latest", ":unreleased"} {
					if strings.Contains(lower, forbidden) {
						t.Fatalf("%s 的 %s 出现生产禁止值 %q：%q", kindOf(doc)+"/"+nameOf(doc), path, forbidden, value)
					}
				}
			})
		}
	}
}

// TestServiceSelectorsMatchPodLabels 断言 Service 能选中应用 Pod，且端口使用命名端口。
func TestServiceSelectorsMatchPodLabels(t *testing.T) {
	app := build(t, appEntrypoint)
	service := app.single(t, "Service")
	deployment := appDeployment(t)

	selector := mapAt(t, service, "spec", "selector")
	podLabels := mapAt(t, deployment, "spec", "template", "metadata", "labels")
	for key, want := range selector {
		if got := stringOf(podLabels[key]); got != stringOf(want) {
			t.Fatalf("Service selector %s=%v 无法匹配 Pod 标签 %v：Service 将没有端点", key, want, podLabels[key])
		}
	}
	if len(selector) == 0 {
		t.Fatal("Service selector 不得为空")
	}

	if got := stringOf(dig(service, "spec", "type")); got != "ClusterIP" {
		t.Fatalf("Service type = %q，期望 ClusterIP（无 Ingress/TLS 方案时不虚构对外入口）", got)
	}
	ports := listAt(t, mapAt(t, service, "spec"), "ports")
	if len(ports) != 1 {
		t.Fatalf("Service 端口数量 = %d，期望 1（只暴露 HTTP 8000）", len(ports))
	}
	if got := stringOf(ports[0]["targetPort"]); got != "http" {
		t.Fatalf("Service targetPort = %q，期望命名端口 http", got)
	}
	if got := intOf(t, ports[0]["port"], "service port"); got != 8000 {
		t.Fatalf("Service port = %d，期望 8000", got)
	}
}

// TestSecretTemplatesCoverSensitiveConfig 断言 Secret 模板只含占位符，且覆盖全部敏感项。
func TestSecretTemplatesCoverSensitiveConfig(t *testing.T) {
	// 构建产物中永远不出现 Secret：凭据由部署环境创建。
	for _, entrypoint := range []string{appEntrypoint, migrationEntrypoint} {
		if secrets := build(t, entrypoint).byKind("Secret"); len(secrets) != 0 {
			t.Fatalf("%s 构建产物包含 Secret：凭据不得进入版本库或构建产物", entrypoint)
		}
	}

	dir := filepath.Join(repoRoot(t), "manifest", "kubernetes", "examples", "secrets")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取 Secret 模板目录: %v", err)
	}

	covered := make(map[string]bool)
	names := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("读取 %s: %v", entry.Name(), err)
		}
		var doc map[string]any
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("解析 %s: %v", entry.Name(), err)
		}
		if got := stringOf(doc["kind"]); got != "Secret" {
			t.Fatalf("%s 的 kind = %q，期望 Secret", entry.Name(), got)
		}
		names[nameOf(doc)] = true

		data, ok := dig(doc, "stringData").(map[string]any)
		if !ok || len(data) == 0 {
			t.Fatalf("%s 缺少 stringData", entry.Name())
		}
		for key, value := range data {
			covered[key] = true
			if !strings.Contains(stringOf(value), "REPLACE_WITH") {
				t.Fatalf("%s 的 %s 不是占位符：模板中不得出现可用凭据", entry.Name(), key)
			}
		}
	}

	// 契约中所有敏感项都必须有对应的 Secret 键，否则「新增敏感配置」会悄悄退化成默认值。
	for _, spec := range config.Specs() {
		if spec.Sensitive && !covered[spec.Env] {
			t.Fatalf("敏感配置 %s 没有对应的 Secret 模板键", spec.Env)
		}
	}

	for _, want := range []string{"surgecart-db-credentials", "surgecart-app-credentials"} {
		if !names[want] {
			t.Fatalf("缺少 Secret 模板 %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// appDeployment 返回 Application 入口中的 Deployment。
func appDeployment(t *testing.T) map[string]any {
	t.Helper()
	return build(t, appEntrypoint).single(t, "Deployment")
}

// migrationJob 返回 Migration 入口中唯一的 Job。
func migrationJob(t *testing.T) map[string]any {
	t.Helper()
	return build(t, migrationEntrypoint).single(t, "Job")
}

// jobSet 把单个资源包装成集合，便于复用遍历断言。
func jobSet(job map[string]any) manifestSet { return manifestSet{job} }

// containerByName 返回 Pod 模板中指定名称的容器。
func containerByName(t *testing.T, resource map[string]any, name string) map[string]any {
	t.Helper()
	containers := listAt(t, mapAt(t, resource, "spec", "template", "spec"), "containers")
	for _, container := range containers {
		if stringOf(container["name"]) == name {
			return container
		}
	}
	t.Fatalf("%s/%s 缺少容器 %q", kindOf(resource), nameOf(resource), name)
	return nil
}

// containerImage 返回指定容器镜像。
func containerImage(t *testing.T, resource map[string]any, name string) string {
	t.Helper()
	image := stringOf(containerByName(t, resource, name)["image"])
	if image == "" {
		t.Fatalf("%s/%s 的容器 %s 缺少 image", kindOf(resource), nameOf(resource), name)
	}
	return image
}

// imageTag 返回镜像引用中的标签（无标签时为空）。
func imageTag(image string) string {
	if strings.Contains(image, "@sha256:") {
		return ""
	}
	index := strings.LastIndex(image, ":")
	if index < 0 || index < strings.LastIndex(image, "/") {
		return ""
	}
	return image[index+1:]
}

var (
	shortSHATagPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	digestPattern      = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)
)

// isImmutableReference 判断镜像引用是否为不可变形式（短 SHA 或 digest）。
func isImmutableReference(image string) bool {
	if digestPattern.MatchString(image) {
		return true
	}
	return shortSHATagPattern.MatchString(imageTag(image))
}

// preStopSleepSeconds 从 `sleep N` 命令中解析秒数。
func preStopSleepSeconds(command string) (int, error) {
	fields := strings.Fields(command)
	if len(fields) != 2 || fields[0] != "sleep" {
		return 0, fmt.Errorf("不支持的命令形式 %q（期望 sleep N）", command)
	}
	seconds, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, fmt.Errorf("sleep 参数非法: %w", err)
	}
	return seconds, nil
}

// envFromNames 返回 envFrom 中 ConfigMap 与 Secret 的引用名（保持声明顺序）。
func envFromNames(t *testing.T, container map[string]any) (configMaps []string, secrets []string) {
	t.Helper()
	raw, ok := container["envFrom"].([]any)
	if !ok {
		t.Fatalf("容器 %s 缺少 envFrom", stringOf(container["name"]))
	}
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if ref, ok := entry["configMapRef"].(map[string]any); ok {
			configMaps = append(configMaps, stringOf(ref["name"]))
		}
		if ref, ok := entry["secretRef"].(map[string]any); ok {
			secrets = append(secrets, stringOf(ref["name"]))
		}
	}
	return configMaps, secrets
}

// envValues 返回容器显式 env 的键值对（不含 envFrom 注入项）。
func envValues(t *testing.T, container map[string]any) map[string]string {
	t.Helper()
	values := make(map[string]string)
	raw, ok := container["env"].([]any)
	if !ok {
		return values
	}
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		values[stringOf(entry["name"])] = stringOf(entry["value"])
	}
	return values
}

// resolvedEnv 返回容器**实际生效**的环境变量：envFrom 引用的 ConfigMap 数据 + 显式 env 覆盖。
// 只看显式 env 会漏掉部署真实使用的配置（绝大多数键来自 ConfigMap）。
func resolvedEnv(t *testing.T, set manifestSet, container map[string]any) map[string]string {
	t.Helper()
	values := make(map[string]string)
	configMaps, _ := envFromNames(t, container)
	for _, name := range configMaps {
		data := mapAt(t, set.find(t, "ConfigMap", name), "data")
		for key, value := range data {
			values[key] = stringOf(value)
		}
	}
	for key, value := range envValues(t, container) {
		values[key] = value
	}
	return values
}

// requireReleaseGuard 断言发布脚本会拒绝生产 overlay 中的占位版本。
// 「模板可构建」与「模板可直接发布」是两件事：前者允许占位值，后者必须被拦截。
func requireReleaseGuard(t *testing.T) {
	t.Helper()
	path := filepath.Join(repoRoot(t), "scripts", "deploy-k8s.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取发布脚本 %s: %v", path, err)
	}
	if !strings.Contains(string(raw), placeholderTag) {
		t.Fatalf("生产 overlay 使用占位版本 %q，但 scripts/deploy-k8s.sh 没有拒绝该值：占位版本可能被直接发布", placeholderTag)
	}
}

// listAt 返回路径上的列表，缺失时失败。
func listAt(t *testing.T, doc map[string]any, key string) []map[string]any {
	t.Helper()
	raw, ok := doc[key].([]any)
	if !ok {
		t.Fatalf("%s 不是列表（实际 %T）", key, doc[key])
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("%s 的元素不是 map（实际 %T）", key, item)
		}
		out = append(out, entry)
	}
	return out
}

// findVolume 返回指定名称的卷，缺失时失败。
func findVolume(t *testing.T, podSpec map[string]any, name string) map[string]any {
	t.Helper()
	for _, volume := range listAt(t, podSpec, "volumes") {
		if stringOf(volume["name"]) == name {
			return volume
		}
	}
	t.Fatalf("缺少卷 %q", name)
	return nil
}

// hasVolume 判断卷存在；medium 非空时同时断言 emptyDir.medium。
func hasVolume(podSpec map[string]any, name, medium string) bool {
	volumes, ok := podSpec["volumes"].([]any)
	if !ok {
		return false
	}
	for _, item := range volumes {
		volume, ok := item.(map[string]any)
		if !ok || stringOf(volume["name"]) != name {
			continue
		}
		if medium == "" {
			return true
		}
		emptyDir, ok := volume["emptyDir"].(map[string]any)
		if !ok {
			return false
		}
		return stringOf(emptyDir["medium"]) == medium
	}
	return false
}

// mountByName 返回指定名称的挂载。
func mountByName(mounts []map[string]any, name string) (map[string]any, bool) {
	for _, mount := range mounts {
		if stringOf(mount["name"]) == name {
			return mount, true
		}
	}
	return nil, false
}

// checkPodSecurity 断言 Pod 级安全上下文。
func checkPodSecurity(t *testing.T, label string, podSpec map[string]any) {
	t.Helper()
	security := mapAt(t, podSpec, "securityContext")

	if !boolOf(t, security["runAsNonRoot"], label+" runAsNonRoot") {
		t.Fatalf("%s 必须 runAsNonRoot: true", label)
	}
	if got := intOf(t, security["runAsUser"], label+" runAsUser"); got != 10001 {
		t.Fatalf("%s runAsUser = %d，期望 10001（T2-04 固定身份）", label, got)
	}
	if got := intOf(t, security["runAsGroup"], label+" runAsGroup"); got != 10001 {
		t.Fatalf("%s runAsGroup = %d，期望 10001", label, got)
	}
	if got := stringOf(dig(security, "seccompProfile", "type")); got != "RuntimeDefault" {
		t.Fatalf("%s seccompProfile = %q，期望 RuntimeDefault", label, got)
	}
	if got := boolOf(t, podSpec["automountServiceAccountToken"], label+" automountServiceAccountToken"); got {
		t.Fatalf("%s 不得自动挂载 ServiceAccount Token", label)
	}
}

// checkContainerSecurity 断言容器级安全上下文。
func checkContainerSecurity(t *testing.T, label string, container map[string]any) {
	t.Helper()
	name := stringOf(container["name"])
	security, ok := container["securityContext"].(map[string]any)
	if !ok {
		t.Fatalf("%s 的容器 %s 缺少 securityContext", label, name)
	}
	if boolOf(t, security["allowPrivilegeEscalation"], label+" allowPrivilegeEscalation") {
		t.Fatalf("%s/%s 不得允许提权", label, name)
	}
	if !boolOf(t, security["readOnlyRootFilesystem"], label+" readOnlyRootFilesystem") {
		t.Fatalf("%s/%s 必须 readOnlyRootFilesystem: true（不得靠关闭只读根文件系统解决写入）", label, name)
	}
	if privileged, exists := security["privileged"]; exists && boolOf(t, privileged, label+" privileged") {
		t.Fatalf("%s/%s 不得使用 privileged", label, name)
	}
	capabilities := mapAt(t, security, "capabilities")
	if got := stringSlice(t, capabilities["drop"], label+" capabilities.drop"); strings.Join(got, ",") != "ALL" {
		t.Fatalf("%s/%s capabilities.drop = %v，期望 [ALL]", label, name, got)
	}
}

// walkStrings 递归遍历 YAML 结构中的所有字符串值。
func walkStrings(value any, visit func(path, value string)) {
	var walk func(path string, node any)
	walk = func(path string, node any) {
		switch typed := node.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				walk(path+"."+key, typed[key])
			}
		case []any:
			for index, item := range typed {
				walk(fmt.Sprintf("%s[%d]", path, index), item)
			}
		case string:
			visit(path, typed)
		}
	}
	walk("", value)
}

// ---------------------------------------------------------------------------
// 发布版本标记与发布锁契约（T2-06.1）
// ---------------------------------------------------------------------------

// TestVersionLabelTracksImageTag 断言 app.kubernetes.io/version 与镜像 tag 同值。
//
// 为什么这条断言必须存在：发布脚本用该标签回答「线上跑的是哪个 commit」，
// 并据此决定「允许升级 / 拒绝降级」。标签与镜像不一致时，降级判断会基于错误事实，
// 因此三处版本（镜像 tag / Job 名称后缀 / 版本标签）必须由测试钉死。
func TestVersionLabelTracksImageTag(t *testing.T) {
	deployment := appDeployment(t)
	tag := imageTag(containerImage(t, deployment, "app"))
	if tag == "" {
		t.Fatal("应用 Deployment 镜像没有 tag：无法校验版本标记")
	}

	if got := stringOf(mapAt(t, deployment, "metadata", "labels")["app.kubernetes.io/version"]); got != tag {
		t.Fatalf("Deployment metadata.labels.app.kubernetes.io/version = %q，期望 %q（必须等于镜像 tag）", got, tag)
	}

	podLabels := mapAt(t, deployment, "spec", "template", "metadata", "labels")
	if got := stringOf(podLabels["app.kubernetes.io/version"]); got != tag {
		t.Fatalf("Pod 模板 labels.app.kubernetes.io/version = %q，期望 %q：这是发布脚本读取线上版本的事实来源", got, tag)
	}

	// 迁移 Job 名称后缀必须同为该版本（两个入口来自同一次发布）。
	if got := nameOf(migrationJob(t)); got != "surgecart-migrate-"+tag {
		t.Fatalf("迁移 Job 名称 = %q，期望 %q", got, "surgecart-migrate-"+tag)
	}

	// 版本标签不得进入 selector：selector 不可变，加版本标签会让下一次发布被 API Server 拒绝。
	selector := mapAt(t, deployment, "spec", "selector", "matchLabels")
	if _, ok := selector["app.kubernetes.io/version"]; ok {
		t.Fatal("selector 不得包含 app.kubernetes.io/version：selector 不可变，版本变化会导致 apply 失败")
	}
}

// TestDeployLockContract 断言发布脚本与 RBAC 示例共用同一个锁资源名。
//
// 这是**静态一致性**断言，不能替代行为测试（行为测试在 test/deployk8s）：
// 如果 RBAC 示例里的 resourceNames 与脚本使用的 Lease 名不一致，
// 真实集群上脚本会在权限预检阶段失败，而示例看起来「已经授权」。
func TestDeployLockContract(t *testing.T) {
	scriptPath := filepath.Join(repoRoot(t), "scripts", "deploy-k8s.sh")
	scriptRaw, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("读取发布脚本: %v", err)
	}
	script := string(scriptRaw)

	for _, fragment := range []string{"coordination.k8s.io/v1", "app.kubernetes.io/version", "--allow-downgrade"} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("scripts/deploy-k8s.sh 缺少 %q：发布锁/版本保护契约被削弱", fragment)
		}
	}

	rbacPath := filepath.Join(repoRoot(t), "manifest", "kubernetes", "examples", "deploy-lock", "rbac.yaml")
	rbacRaw, err := os.ReadFile(rbacPath)
	if err != nil {
		t.Fatalf("读取发布锁 RBAC 示例 %s: %v（发布锁必须提供最小权限示例）", rbacPath, err)
	}

	lockNamePattern := regexp.MustCompile(`resourceNames:\s*\[\s*"([^"]+)"\s*\]`)
	match := lockNamePattern.FindStringSubmatch(string(rbacRaw))
	if match == nil {
		t.Fatalf("%s 没有用 resourceNames 限定到具体 Lease：权限范围过大", rbacPath)
	}
	lockName := match[1]
	if !strings.Contains(script, `LOCK_NAME="`+lockName+`"`) {
		t.Fatalf("RBAC 示例授权的是 Lease %q，但 scripts/deploy-k8s.sh 的 LOCK_NAME 不匹配：真实集群上权限预检会失败", lockName)
	}
	for _, verb := range []string{"get", "update"} {
		if !strings.Contains(string(rbacRaw), `"`+verb+`"`) {
			t.Fatalf("%s 缺少 %q 动词：脚本的 create/CAS 更新/释放流程需要它", rbacPath, verb)
		}
	}
}

// TestMigrationTimeoutTracksJobDeadline 断言发布脚本的默认迁移等待上限与 Job 的
// activeDeadlineSeconds 保持「脚本上限 > Job 截止时间 + 最小余量」的关系（AUD-04/06）。
//
// 这是静态契约，行为验证在 test/deployk8s（脚本会拒绝不合法的组合）。
// 只改一处默认值（Job 或脚本）会让两者再次相等，从而重新引入「超时 vs Deadline」竞态。
func TestMigrationTimeoutTracksJobDeadline(t *testing.T) {
	job := migrationJob(t)
	deadline := intOf(t, mapAt(t, job, "spec")["activeDeadlineSeconds"], "job.spec.activeDeadlineSeconds")

	path := filepath.Join(repoRoot(t), "scripts", "deploy-k8s.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取发布脚本 %s: %v", path, err)
	}
	match := regexp.MustCompile(`(?m)^MIGRATION_TIMEOUT=([0-9]+)$`).FindSubmatch(raw)
	if match == nil {
		t.Fatalf("scripts/deploy-k8s.sh 缺少 MIGRATION_TIMEOUT 默认值")
	}
	timeout, err := strconv.Atoi(string(match[1]))
	if err != nil {
		t.Fatalf("解析 MIGRATION_TIMEOUT: %v", err)
	}
	const minMargin = 60
	if timeout < deadline+minMargin {
		t.Fatalf("MIGRATION_TIMEOUT=%ds 与 Job activeDeadlineSeconds=%ds 的余量不足 %ds：脚本可能先于 Job 自己的截止时间超时",
			timeout, deadline, minMargin)
	}
	t.Logf("迁移等待上限 %ds > Job activeDeadlineSeconds %ds（余量 %ds）", timeout, deadline, timeout-deadline)
}

// TestLeaseRenewBudgetContract 断言发布脚本的默认租期参数满足 AUD-03 不变量：
//
//	renew_interval + renew_request_timeout + safety_margin < lease_duration
//
// 旧默认值（10 + 30 > 30）会让锁在续租请求返回之前就过期。
func TestLeaseRenewBudgetContract(t *testing.T) {
	path := filepath.Join(repoRoot(t), "scripts", "deploy-k8s.sh")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取发布脚本 %s: %v", path, err)
	}
	value := func(name string) int {
		pattern := regexp.MustCompile(`(?m)^` + name + `=([0-9]+)$`)
		match := pattern.FindSubmatch(raw)
		if match == nil {
			t.Fatalf("scripts/deploy-k8s.sh 缺少 %s 默认值", name)
		}
		number, convErr := strconv.Atoi(string(match[1]))
		if convErr != nil {
			t.Fatalf("解析 %s: %v", name, convErr)
		}
		return number
	}

	duration := value("LOCK_LEASE_DURATION")
	interval := value("LOCK_RENEW_INTERVAL")
	requestTimeout := value("LOCK_RENEW_REQUEST_TIMEOUT")
	margin := value("LOCK_SAFETY_MARGIN")
	budget := interval + requestTimeout + margin
	if budget >= duration {
		t.Fatalf("租期预算 %d + %d + %d = %d 必须小于 lease_duration=%d：否则锁会在续租返回前过期",
			interval, requestTimeout, margin, budget, duration)
	}
	t.Logf("租期预算 %d + %d + %d = %d < %d", interval, requestTimeout, margin, budget, duration)
}
