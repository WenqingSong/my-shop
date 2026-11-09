package manifest

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// 本文件是 T2-07 的**基础设施契约**断言：本地 Kubernetes 里的 MySQL/Redis/Kafka。
//
// 与 manifest_test.go 的分工：
//   - manifest_test.go 断言 Application / Migration 两个发布入口；
//   - 本文件断言第三个入口 infrastructure/（依赖）与 verify/（真实验证），
//     以及「应用工作负载不得执行数据库迁移」这条跨入口不变量。
//
// 所有断言都作用在 `kubectl kustomize` 的真实构建产物上，不做源 YAML 文本匹配。
//
// 明确边界：本文件证明「清单结构与配置正确」，**不**证明「Pod 真的能跑起来」。
// 真实集群行为见 scripts/k8s-infra.sh verify 与 T2-07 交付报告的 NOT_VERIFIED 列表。

const (
	// infraEntrypoint 是本地基础设施部署入口（MySQL/Redis/Kafka + Topic 初始化）。
	infraEntrypoint = "manifest/kubernetes/infrastructure/base"
	// infraVerifyEntrypoint 是集群内真实验证入口（三个一次性 Job）。
	infraVerifyEntrypoint = "manifest/kubernetes/infrastructure/verify"
	// sharedEntrypoint 是 Application / Migration / Infrastructure 三个入口共用的集群级事实
	// （Namespace + 数据库连接非敏感配置）。
	sharedEntrypoint = "manifest/kubernetes/shared"
	// appBaseEntrypoint 是应用与环境无关的基础层（不含 production overlay 的环境专属地址）。
	appBaseEntrypoint = "manifest/kubernetes/base"
)

// infraWorkload 描述一个基础设施工作负载及其固定运行身份。
//
// uid/gid 不是「随便选的数字」，而是官方镜像内置用户的真实 uid/gid（实测）：
//   - mysql:8.0    → mysql(999:999)
//   - redis:7-alpine → redis(999:999)
//   - apache/kafka:3.9.1 → appuser(1000:1000)
//
// 用镜像内置用户而不是「新建一个 10001」的原因：镜像内的目录（/var/lib/mysql 等）
// 属于这些用户，换成其它 uid 需要额外 chown，而 chown 又需要 root 或 CAP_CHOWN，
// 与 restricted PSS 冲突。运行身份与镜像自洽，是这里能保持 runAsNonRoot 的前提。
type infraWorkload struct {
	kind string
	name string
	uid  int
	gid  int
}

var infraWorkloads = []infraWorkload{
	{kind: "Deployment", name: "mysql", uid: 999, gid: 999},
	{kind: "Deployment", name: "redis", uid: 999, gid: 999},
	{kind: "Deployment", name: "kafka", uid: 1000, gid: 1000},
	{kind: "Job", name: "kafka-topics-init", uid: 1000, gid: 1000},
}

// ---------------------------------------------------------------------------
// 入口结构
// ---------------------------------------------------------------------------

// TestInfrastructureEntrypointShape 断言基础设施入口只包含依赖，不包含应用资源。
func TestInfrastructureEntrypointShape(t *testing.T) {
	infra := build(t, infraEntrypoint)

	counts := make(map[string]int)
	for _, doc := range infra {
		counts[kindOf(doc)]++
	}
	want := map[string]int{
		"Namespace":             1,
		"ConfigMap":             2, // surgecart-database-config（共享）+ surgecart-kafka-config
		"Deployment":            3, // mysql / redis / kafka
		"Service":               3,
		"PersistentVolumeClaim": 3,
		"Job":                   1, // kafka-topics-init
	}
	for kind, expected := range want {
		if counts[kind] != expected {
			t.Fatalf("%s 数量 = %d，期望 %d（实际包含: %s）", kind, counts[kind], expected, strings.Join(infra.summary(), ", "))
		}
	}

	// 依赖入口不得包含应用资源：应用发布有独立的入口与发布锁，混在一起会让
	// 「部署依赖」变成「顺手发布应用」。
	for _, forbidden := range []string{"Deployment/surgecart", "Service/surgecart", "PersistentVolumeClaim/surgecart-storage"} {
		if strings.Contains(strings.Join(infra.summary(), ", "), forbidden) {
			t.Fatalf("基础设施入口不得包含应用资源 %s", forbidden)
		}
	}

	// 与另两个入口共享同一份 Namespace 与数据库配置（同一资源，不重复定义）。
	infra.find(t, "Namespace", "surgecart")
	infra.find(t, "ConfigMap", "surgecart-database-config")

	// 镜像版本固定，且与 docker-compose 一致；禁止 latest。
	expectedImages := map[string]string{
		"mysql": "mysql:8.0",
		"redis": "redis:7-alpine",
		"kafka": "apache/kafka:3.9.1",
	}
	for _, doc := range infra.byKind("Deployment") {
		name := nameOf(doc)
		containers := listAt(t, mapAt(t, doc, "spec", "template", "spec"), "containers")
		if len(containers) != 1 {
			t.Fatalf("Deployment/%s 容器数量 = %d，期望 1", name, len(containers))
		}
		image := stringOf(containers[0]["image"])
		if image != expectedImages[name] {
			t.Fatalf("Deployment/%s 镜像 = %q，期望 %q（与 docker-compose 一致，不得升级）", name, image, expectedImages[name])
		}
		if strings.HasSuffix(image, ":latest") {
			t.Fatalf("Deployment/%s 不得使用 latest", name)
		}
	}

	// 依赖入口不得产出 Secret：凭据由 scripts/k8s-infra.sh secrets 在集群外创建，
	// 构建产物里出现 Secret 意味着凭据进了 Git。
	if secrets := infra.byKind("Secret"); len(secrets) != 0 {
		t.Fatalf("基础设施构建产物不得包含 Secret（实际 %d 个）", len(secrets))
	}
}

// TestInfrastructureServicesResolveToPods 断言 Service 能选中对应 Pod，且端口使用命名端口。
func TestInfrastructureServicesResolveToPods(t *testing.T) {
	infra := build(t, infraEntrypoint)

	expected := map[string]struct {
		ports      map[string]int // 名称 → 端口
		targetName map[string]string
	}{
		"mysql": {ports: map[string]int{"mysql": 3306}, targetName: map[string]string{"mysql": "mysql"}},
		"redis": {ports: map[string]int{"redis": 6379}, targetName: map[string]string{"redis": "redis"}},
		"kafka": {
			ports:      map[string]int{"plaintext": 9092, "external": 29092},
			targetName: map[string]string{"plaintext": "plaintext", "external": "external"},
		},
	}

	for _, doc := range infra.byKind("Deployment") {
		name := nameOf(doc)
		want, ok := expected[name]
		if !ok {
			t.Fatalf("未预期的 Deployment/%s", name)
		}
		service := infra.find(t, "Service", name)
		podLabels := mapAt(t, doc, "spec", "template", "metadata", "labels")
		selector := mapAt(t, service, "spec", "selector")
		if len(selector) == 0 {
			t.Fatalf("Service/%s selector 为空", name)
		}
		for key, value := range selector {
			if got := stringOf(podLabels[key]); got != stringOf(value) {
				t.Fatalf("Service/%s selector %s=%v 无法匹配 Pod 标签 %v：Service 将没有端点", name, key, value, podLabels[key])
			}
		}
		if got := stringOf(dig(service, "spec", "type")); got != "ClusterIP" {
			t.Fatalf("Service/%s type = %q，期望 ClusterIP（本地开发不对外暴露）", name, got)
		}

		// 容器端口必须是命名端口，Service targetPort 才能引用名称而不是重复数字。
		containerPorts := make(map[string]int)
		for _, container := range listAt(t, mapAt(t, doc, "spec", "template", "spec"), "containers") {
			for _, port := range listAt(t, container, "ports") {
				portName := stringOf(port["name"])
				if portName == "" {
					t.Fatalf("Deployment/%s 的容器端口缺少 name（Service 无法用命名端口引用）", name)
				}
				containerPorts[portName] = intOf(t, port["containerPort"], "containerPort")
			}
		}

		servicePorts := listAt(t, mapAt(t, service, "spec"), "ports")
		if len(servicePorts) != len(want.ports) {
			t.Fatalf("Service/%s 端口数量 = %d，期望 %d", name, len(servicePorts), len(want.ports))
		}
		for _, port := range servicePorts {
			portName := stringOf(port["name"])
			expectedPort, ok := want.ports[portName]
			if !ok {
				t.Fatalf("Service/%s 出现未预期端口 %q", name, portName)
			}
			if got := intOf(t, port["port"], "service port"); got != expectedPort {
				t.Fatalf("Service/%s 端口 %s = %d，期望 %d", name, portName, got, expectedPort)
			}
			target := stringOf(port["targetPort"])
			if target != want.targetName[portName] {
				t.Fatalf("Service/%s 端口 %s targetPort = %q，期望命名端口 %q", name, portName, target, want.targetName[portName])
			}
			if got, ok := containerPorts[target]; !ok || got != expectedPort {
				t.Fatalf("Service/%s targetPort %q 未指向同名容器端口（实际容器端口: %v）", name, target, containerPorts)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 安全上下文（restricted PSS）
// ---------------------------------------------------------------------------

// TestInfrastructurePodSecurityRestricted 断言依赖与验证 Pod 都满足 restricted PSS，
// 且没有为了跑起来而放宽（hostPath / hostNetwork / 特权 / root）。
func TestInfrastructurePodSecurityRestricted(t *testing.T) {
	infra := build(t, infraEntrypoint)

	for _, workload := range infraWorkloads {
		doc := infra.find(t, workload.kind, workload.name)
		label := workload.kind + "/" + workload.name
		podSpec := mapAt(t, doc, "spec", "template", "spec")
		checkInfraPodSecurity(t, label, podSpec, workload)
		for _, container := range listAt(t, podSpec, "containers") {
			checkContainerSecurity(t, label, container)
			checkInfraResources(t, label, stringOf(container["name"]), container)
		}
		for _, container := range listOptionalContainers(t, podSpec, "initContainers") {
			checkContainerSecurity(t, label+" initContainer", container)
		}
	}

	// 验证 Job 与基础设施容器同等级：验证不能靠「用更宽松的权限」通过。
	verify := build(t, infraVerifyEntrypoint)
	for _, doc := range verify.byKind("Job") {
		label := "Job/" + nameOf(doc)
		podSpec := mapAt(t, doc, "spec", "template", "spec")
		// 验证 Job 的运行身份按镜像固定，不在 infraWorkloads 表里重复登记。
		checkInfraPodSecurity(t, label, podSpec, infraWorkload{kind: "Job", name: nameOf(doc), uid: -1, gid: -1})
		for _, container := range listAt(t, podSpec, "containers") {
			checkContainerSecurity(t, label, container)
			checkInfraResources(t, label, stringOf(container["name"]), container)
		}
	}
}

// checkInfraPodSecurity 断言 Pod 级安全上下文；uid<0 表示只校验「非 root 且身份自洽」。
func checkInfraPodSecurity(t *testing.T, label string, podSpec map[string]any, workload infraWorkload) {
	t.Helper()
	security := mapAt(t, podSpec, "securityContext")

	if !boolOf(t, security["runAsNonRoot"], label+" runAsNonRoot") {
		t.Fatalf("%s 必须 runAsNonRoot: true（restricted PSS）", label)
	}
	uid := intOf(t, security["runAsUser"], label+" runAsUser")
	if uid == 0 {
		t.Fatalf("%s runAsUser 不得为 0（root 不满足 restricted PSS）", label)
	}
	gid := intOf(t, security["runAsGroup"], label+" runAsGroup")
	if workload.uid >= 0 {
		if uid != workload.uid || gid != workload.gid {
			t.Fatalf("%s 运行身份 = %d:%d，期望镜像内置用户 %d:%d", label, uid, gid, workload.uid, workload.gid)
		}
		// fsGroup 必须与运行用户一致，否则 PVC 里的文件对进程不可写。
		if fsGroup, exists := security["fsGroup"]; exists {
			if got := intOf(t, fsGroup, label+" fsGroup"); got != uid {
				t.Fatalf("%s fsGroup = %d，期望 %d（与运行用户一致）", label, got, uid)
			}
		}
	}

	if got := stringOf(dig(security, "seccompProfile", "type")); got != "RuntimeDefault" {
		t.Fatalf("%s seccompProfile = %q，期望 RuntimeDefault", label, got)
	}
	if got := boolOf(t, podSpec["automountServiceAccountToken"], label+" automountServiceAccountToken"); got {
		t.Fatalf("%s 不得自动挂载 ServiceAccount Token", label)
	}

	// 禁用面：这些字段一旦出现，意味着用「放宽隔离」换「能跑起来」。
	for _, field := range []string{"hostNetwork", "hostPID", "hostIPC"} {
		if value, exists := podSpec[field]; exists && value != false {
			t.Fatalf("%s 不得设置 %s", label, field)
		}
	}
	for _, volume := range listAt(t, podSpec, "volumes") {
		if _, exists := volume["hostPath"]; exists {
			t.Fatalf("%s 不得使用 hostPath 卷（%v）", label, volume["name"])
		}
	}
	for _, container := range listAt(t, podSpec, "containers") {
		// 容器可以只声明命令而不声明端口（Topic 初始化 Job / 验证 Job）。
		if container["ports"] == nil {
			continue
		}
		for _, port := range listAt(t, container, "ports") {
			if _, exists := port["hostPort"]; exists {
				t.Fatalf("%s/%s 不得使用 hostPort", label, stringOf(container["name"]))
			}
		}
	}
}

// checkInfraResources 断言 requests/limits 都显式声明且非零（避免 BestEffort 被优先驱逐）。
func checkInfraResources(t *testing.T, label, container string, spec map[string]any) {
	t.Helper()
	resources := mapAt(t, spec, "resources")
	for _, section := range []string{"requests", "limits"} {
		values := mapAt(t, resources, section)
		for _, key := range []string{"cpu", "memory"} {
			raw, exists := values[key]
			if !exists || strings.TrimSpace(stringOf(raw)) == "" {
				t.Fatalf("%s/%s resources.%s 缺少 %s（不允许省略 requests/limits）", label, container, section, key)
			}
			if strings.TrimSpace(stringOf(raw)) == "0" {
				t.Fatalf("%s/%s resources.%s.%s 不得为 0", label, container, section, key)
			}
		}
	}
}

// listOptionalContainers 返回可选容器列表（initContainers 缺失时返回空）。
func listOptionalContainers(t *testing.T, doc map[string]any, key string) []map[string]any {
	t.Helper()
	if doc[key] == nil {
		return nil
	}
	return listAt(t, doc, key)
}

// ---------------------------------------------------------------------------
// Kafka 监听器语义
// ---------------------------------------------------------------------------

// TestKafkaListenerContract 断言「容器内地址 / Service 地址 / 宿主机地址」三者的边界。
//
// 这是 T2-07 最容易出错的地方：把 127.0.0.1:29092 直接搬进集群，客户端能连上 bootstrap，
// 却会拿到 Pod 自身回环地址，produce/consume 静默失败。
func TestKafkaListenerContract(t *testing.T) {
	infra := build(t, infraEntrypoint)
	config := infra.find(t, "ConfigMap", "surgecart-kafka-config")
	data := mapAt(t, config, "data")

	value := func(key string) string {
		t.Helper()
		raw, exists := data[key]
		if !exists {
			t.Fatalf("ConfigMap/surgecart-kafka-config 缺少键 %q", key)
		}
		return stringOf(raw)
	}

	// 1. KRaft 单节点：broker 与 controller 合一，不引入 ZooKeeper。
	if got := value("KAFKA_PROCESS_ROLES"); got != "broker,controller" {
		t.Fatalf("KAFKA_PROCESS_ROLES = %q，期望 broker,controller（KRaft 单节点）", got)
	}
	for key := range data {
		if strings.Contains(strings.ToUpper(key), "ZOOKEEPER") {
			t.Fatalf("不得引入 ZooKeeper 配置（发现键 %q）", key)
		}
	}
	if got := value("KAFKA_NODE_ID"); got != "1" {
		t.Fatalf("KAFKA_NODE_ID = %q，期望 1", got)
	}

	// 2. 监听器绑定 0.0.0.0（容器内地址），不绑定回环：跨 Pod 访问必须能从 eth0 进来。
	listeners := value("KAFKA_LISTENERS")
	for _, expected := range []string{"PLAINTEXT://0.0.0.0:9092", "CONTROLLER://0.0.0.0:9093", "EXTERNAL://0.0.0.0:29092"} {
		if !strings.Contains(listeners, expected) {
			t.Fatalf("KAFKA_LISTENERS 缺少 %q（实际 %q）", expected, listeners)
		}
	}
	if strings.Contains(listeners, "127.0.0.1") {
		t.Fatalf("KAFKA_LISTENERS 不得绑定回环地址：跨 Pod 客户端无法连接（实际 %q）", listeners)
	}

	// 3. 广播地址：集群内必须是 Service DNS，宿主机必须是回环（配合 port-forward）。
	advertised := value("KAFKA_ADVERTISED_LISTENERS")
	if !strings.Contains(advertised, "PLAINTEXT://kafka.surgecart.svc.cluster.local:9092") {
		t.Fatalf("KAFKA_ADVERTISED_LISTENERS 的集群内地址必须是 Service DNS，实际 %q", advertised)
	}
	if !strings.Contains(advertised, "EXTERNAL://127.0.0.1:29092") {
		t.Fatalf("KAFKA_ADVERTISED_LISTENERS 的宿主机地址必须是 127.0.0.1:29092（port-forward），实际 %q", advertised)
	}
	if strings.Contains(advertised, "PLAINTEXT://127.0.0.1") {
		t.Fatalf("集群内广播地址不得为 127.0.0.1：Pod 内的回环不是 Broker（实际 %q）", advertised)
	}

	// 4. bootstrap 键与广播地址同源，避免验证脚本连到另一个地址。
	bootstrap := value("KAFKA_BOOTSTRAP_INTERNAL")
	if bootstrap != "kafka.surgecart.svc.cluster.local:9092" {
		t.Fatalf("KAFKA_BOOTSTRAP_INTERNAL = %q，期望 kafka.surgecart.svc.cluster.local:9092", bootstrap)
	}
	if !strings.Contains(advertised, "PLAINTEXT://"+bootstrap) {
		t.Fatalf("KAFKA_BOOTSTRAP_INTERNAL（%q）与 PLAINTEXT 广播地址不一致：%q", bootstrap, advertised)
	}

	// 5. 控制器 quorum 走 Pod 内回环：单进程自连，不经过 Service（避免 hairpin NAT）。
	if got := value("KAFKA_CONTROLLER_QUORUM_VOTERS"); !strings.HasPrefix(got, "1@127.0.0.1:") {
		t.Fatalf("KAFKA_CONTROLLER_QUORUM_VOTERS = %q，期望 1@127.0.0.1:9093（单节点自连）", got)
	}
	if got := value("KAFKA_CONTROLLER_LISTENER_NAMES"); got != "CONTROLLER" {
		t.Fatalf("KAFKA_CONTROLLER_LISTENER_NAMES = %q，期望 CONTROLLER", got)
	}

	// 6. Topic 必须显式创建：自动建 Topic 会让分区/副本数由客户端决定。
	if got := value("KAFKA_AUTO_CREATE_TOPICS_ENABLE"); got != "false" {
		t.Fatalf("KAFKA_AUTO_CREATE_TOPICS_ENABLE = %q，期望 false", got)
	}

	// 7. 日志目录必须与 Deployment 的 PVC 挂载点一致，否则数据落在只读根文件系统上。
	logDirs := value("KAFKA_LOG_DIRS")
	kafka := infra.find(t, "Deployment", "kafka")
	mounts := listAt(t, containerByName(t, kafka, "kafka"), "volumeMounts")
	dataMount := ""
	for _, mount := range mounts {
		if stringOf(mount["name"]) == "data" {
			dataMount = stringOf(mount["mountPath"])
		}
	}
	if dataMount == "" {
		t.Fatal("Deployment/kafka 未挂载 data 卷")
	}
	if logDirs != dataMount {
		t.Fatalf("KAFKA_LOG_DIRS = %q 与 PVC 挂载点 %q 不一致：数据会写进只读层", logDirs, dataMount)
	}

	// 8. broker 属性确实注入到容器（envFrom）+ 业务 Topic 名称存在。
	podSpec := mapAt(t, kafka, "spec", "template", "spec")
	envFromNames := make([]string, 0, 2)
	containers := listAt(t, podSpec, "containers")
	for _, item := range listAt(t, containers[0], "envFrom") {
		envFromNames = append(envFromNames, stringOf(dig(item, "configMapRef", "name")))
	}
	if strings.Join(envFromNames, ",") != "surgecart-kafka-config" {
		t.Fatalf("Deployment/kafka envFrom = %v，期望只引用 surgecart-kafka-config", envFromNames)
	}
	if got := value("TOPIC_ORDER_REQUESTED"); got != "surgecart.flashsale.order.requested.v1" {
		t.Fatalf("TOPIC_ORDER_REQUESTED = %q，与项目既有 Topic 名称不一致", got)
	}
	if got := value("TOPIC_ORDER_REQUESTED_DLQ"); got != "surgecart.flashsale.order.requested.v1.dlq" {
		t.Fatalf("TOPIC_ORDER_REQUESTED_DLQ = %q，与项目既有死信 Topic 不一致", got)
	}
}

// ---------------------------------------------------------------------------
// 与应用配置的一致性
// ---------------------------------------------------------------------------

// TestInfrastructureMatchesApplicationConfig 断言依赖创建的「库/地址」就是应用会连的那个。
//
// 这类不一致是最难排查的发布事故：迁移在 A 库、应用连 B 库；或者基础设施广播的地址
// 应用根本不认识。断言跨入口同值，而不是各自看起来都合法。
func TestInfrastructureMatchesApplicationConfig(t *testing.T) {
	infra := build(t, infraEntrypoint)
	// 用共享层/应用 base 而不是 production overlay：overlay 的职责就是把外部地址替换成
	// 目标环境的值（当前是占位符 REPLACE_WITH_MYSQL_HOST / REPLACE_WITH_REDIS_ENDPOINT，
	// 由发布脚本拒绝部署）。「基础设施创建的服务就是应用默认会连的地址」属于 base 事实。
	shared := build(t, sharedEntrypoint)
	appBase := build(t, appBaseEntrypoint)

	appConfig := appBase.find(t, "ConfigMap", "surgecart-config")
	appDatabase := shared.find(t, "ConfigMap", "surgecart-database-config")
	mysql := infra.find(t, "Deployment", "mysql")

	// 1. 数据库地址必须是 MySQL Service 的 FQDN。
	host := stringOf(dig(appDatabase, "data", "DATABASE_DEFAULT_HOST"))
	if want := "mysql.surgecart.svc.cluster.local"; host != want {
		t.Fatalf("共享层 DATABASE_DEFAULT_HOST = %q，期望与基础设施 Service DNS 一致：%q", host, want)
	}

	// 2. MySQL 容器创建的就是应用要用的库与账号（同一份 ConfigMap/Secret 的键）。
	mysqlEnv := envByName(t, containerByName(t, mysql, "mysql"))
	assertEnvFromRef(t, mysqlEnv, "MYSQL_DATABASE", "configMapKeyRef", "surgecart-database-config", "DATABASE_DEFAULT_NAME")
	assertEnvFromRef(t, mysqlEnv, "MYSQL_USER", "configMapKeyRef", "surgecart-database-config", "DATABASE_DEFAULT_USER")
	assertEnvFromRef(t, mysqlEnv, "MYSQL_PASSWORD", "secretKeyRef", "surgecart-db-credentials", "DATABASE_DEFAULT_PASS")
	assertEnvFromRef(t, mysqlEnv, "MYSQL_ROOT_PASSWORD", "secretKeyRef", "surgecart-db-credentials", "MYSQL_ROOT_PASSWORD")

	// 3. Redis 地址：应用配置与验证 Job 必须指向同一个 Service DNS。
	redisAddress := stringOf(dig(appConfig, "data", "REDIS_DEFAULT_ADDRESS"))
	if want := "redis.surgecart.svc.cluster.local:6379"; redisAddress != want {
		t.Fatalf("应用 REDIS_DEFAULT_ADDRESS = %q，期望 %q", redisAddress, want)
	}
	verify := build(t, infraVerifyEntrypoint)
	redisVerify := verify.find(t, "Job", "verify-redis")
	redisVerifyEnv := envByName(t, containerByName(t, redisVerify, "verify-redis"))
	if got := stringOf(dig(redisVerifyEnv["REDIS_DEFAULT_ADDRESS"], "value")); got != redisAddress {
		t.Fatalf("verify-redis 的 REDIS_DEFAULT_ADDRESS = %q 与应用配置 %q 不一致", got, redisAddress)
	}

	// 4. Kafka bootstrap：应用配置 == 基础设施 broadcast == 验证 Job 使用的地址。
	bootstrap := stringOf(dig(infra.find(t, "ConfigMap", "surgecart-kafka-config"), "data", "KAFKA_BOOTSTRAP_INTERNAL"))
	appKafka := stringOf(dig(appConfig, "data", "FLASH_SALE_KAFKA_BROKERS"))
	if appKafka != bootstrap {
		t.Fatalf("应用 FLASH_SALE_KAFKA_BROKERS = %q，期望 %q（Service DNS）", appKafka, bootstrap)
	}
	if strings.Contains(appKafka, "127.0.0.1") {
		t.Fatalf("应用 Kafka 地址不得为回环地址：Pod 内的 127.0.0.1 不是 Broker（实际 %q）", appKafka)
	}

	// 5. Topic 初始化 Job 与 Kafka 容器使用同一份 Topic 名称来源。
	topicsJob := infra.find(t, "Job", "kafka-topics-init")
	podSpec := mapAt(t, topicsJob, "spec", "template", "spec")
	containers := listAt(t, podSpec, "containers")
	names := make([]string, 0, 1)
	for _, item := range listAt(t, containers[0], "envFrom") {
		names = append(names, stringOf(dig(item, "configMapRef", "name")))
	}
	if strings.Join(names, ",") != "surgecart-kafka-config" {
		t.Fatalf("kafka-topics-init envFrom = %v，期望只引用 surgecart-kafka-config", names)
	}
}

// envByName 把容器的 env 列表转成 name → 条目 的映射。
func envByName(t *testing.T, container map[string]any) map[string]map[string]any {
	t.Helper()
	out := make(map[string]map[string]any)
	if container["env"] == nil {
		return out
	}
	for _, item := range listAt(t, container, "env") {
		out[stringOf(item["name"])] = item
	}
	return out
}

// assertEnvFromRef 断言 env 项通过指定的 configMapKeyRef / secretKeyRef 取值。
func assertEnvFromRef(t *testing.T, env map[string]map[string]any, envName, refType, refName, refKey string) {
	t.Helper()
	item, ok := env[envName]
	if !ok {
		t.Fatalf("容器缺少环境变量 %s", envName)
	}
	ref, ok := dig(item, "valueFrom", refType).(map[string]any)
	if !ok {
		t.Fatalf("%s 必须使用 %s 注入（实际 %v）", envName, refType, item)
	}
	if got := stringOf(ref["name"]); got != refName {
		t.Fatalf("%s.%s.name = %q，期望 %q", envName, refType, got, refName)
	}
	if got := stringOf(ref["key"]); got != refKey {
		t.Fatalf("%s.%s.key = %q，期望 %q", envName, refType, got, refKey)
	}
}

// ---------------------------------------------------------------------------
// Secret 边界
// ---------------------------------------------------------------------------

// TestInfrastructureConfigMapsCarryNoSecrets 断言依赖的 ConfigMap 不含敏感项与占位符。
func TestInfrastructureConfigMapsCarryNoSecrets(t *testing.T) {
	infra := build(t, infraEntrypoint)

	suspicious := []string{"PASSWORD", "SECRET", "TOKEN", "ACCESS_KEY", "SECRET_KEY"}
	for _, doc := range infra.byKind("ConfigMap") {
		for key := range mapAt(t, doc, "data") {
			upper := strings.ToUpper(key)
			for _, needle := range suspicious {
				if strings.Contains(upper, needle) {
					t.Fatalf("ConfigMap/%s 不得承载敏感键 %q（必须走 Secret）", nameOf(doc), key)
				}
			}
		}
	}

	// 构建产物里不得出现模板占位符：占位符被 apply 上集群等于「用假凭据启动」。
	var leaked []string
	for _, doc := range infra {
		walkStrings(doc, func(path, value string) {
			if strings.Contains(value, "REPLACE_WITH") {
				leaked = append(leaked, kindOf(doc)+"/"+nameOf(doc)+path)
			}
		})
	}
	sort.Strings(leaked)
	if len(leaked) != 0 {
		t.Fatalf("基础设施构建产物出现占位符：%v", leaked)
	}

	// Secret 模板必须覆盖 MySQL 需要的两个键，且只含占位符。
	template := docRead(t, "manifest/kubernetes/examples/secrets/database-secret.example.yaml")
	for _, key := range []string{"MYSQL_ROOT_PASSWORD:", "DATABASE_DEFAULT_PASS:"} {
		if !strings.Contains(template, key) {
			t.Fatalf("database-secret.example.yaml 缺少键 %s：k8s-infra.sh secrets 的契约不完整", key)
		}
	}
	if !strings.Contains(template, "REPLACE_WITH") {
		t.Fatal("database-secret.example.yaml 必须是占位符模板，不得包含真实口令")
	}
}

// ---------------------------------------------------------------------------
// 迁移职责（跨入口）
// ---------------------------------------------------------------------------

// TestApplicationWorkloadsNeverRunMigrations 断言应用工作负载不执行数据库迁移。
//
// 事实链：
//  1. 镜像 ENTRYPOINT 是 /app/docker-entrypoint.sh，它先 `migrate up` 再 `exec serve`；
//  2. 因此任何沿用镜像 ENTRYPOINT 的工作负载都会在启动时迁移；
//  3. 所以 Application 的 Deployment 必须显式覆盖 command/args；
//  4. 迁移只能由 Migration 入口的 Job 执行（command=/app/my-shop, args=[migrate up]）。
func TestApplicationWorkloadsNeverRunMigrations(t *testing.T) {
	// 1. 镜像入口语义（源文件事实）。
	dockerfile := docRead(t, "Dockerfile")
	if !strings.Contains(dockerfile, `ENTRYPOINT ["/app/docker-entrypoint.sh"]`) {
		t.Fatal("Dockerfile 的 ENTRYPOINT 不再是 docker-entrypoint.sh：本断言的迁移风险前提已改变，需重新评估")
	}
	entrypoint := docRead(t, "scripts/docker-entrypoint.sh")
	if !strings.Contains(entrypoint, "migrate up") || !strings.Contains(entrypoint, "exec /app/my-shop serve") {
		t.Fatal("scripts/docker-entrypoint.sh 不再是「migrate up → exec serve」：迁移职责断言需同步")
	}

	app := build(t, appEntrypoint)
	deployment := appDeployment(t)
	appContainer := containerByName(t, deployment, "app")

	command := optStringSlice(t, appContainer["command"], "Deployment 容器 command")
	args := optStringSlice(t, appContainer["args"], "Deployment 容器 args")
	if strings.Join(command, " ") != "/app/my-shop" {
		t.Fatalf("Deployment 容器 command = %v，必须显式覆盖镜像 ENTRYPOINT 为 /app/my-shop", command)
	}
	if strings.Join(args, " ") != "serve" {
		t.Fatalf("Deployment 容器 args = %v，期望 serve（不得包含 migrate）", args)
	}

	// 2. 任何工作负载都不得引用 entrypoint 脚本或携带 migrate 子命令。
	for _, set := range []struct {
		name string
		docs manifestSet
	}{{"Application", app}, {"Migration", build(t, migrationEntrypoint)}, {"Infrastructure", build(t, infraEntrypoint)}, {"Verify", build(t, infraVerifyEntrypoint)}} {
		for _, doc := range set.docs {
			podSpec, ok := dig(doc, "spec", "template", "spec").(map[string]any)
			if !ok {
				continue
			}
			for _, container := range append(listAt(t, podSpec, "containers"), listOptionalContainers(t, podSpec, "initContainers")...) {
				combined := append(append([]string{}, optStringSlice(t, container["command"], "command")...), optStringSlice(t, container["args"], "args")...)
				joined := strings.Join(combined, " ")
				if strings.Contains(joined, "docker-entrypoint.sh") {
					t.Fatalf("%s 的 %s/%s 沿用了镜像 ENTRYPOINT：启动时会执行 migrate up", set.name, kindOf(doc), nameOf(doc))
				}
				isMigration := strings.Contains(joined, "migrate")
				isMigrationJob := kindOf(doc) == "Job" && strings.Contains(nameOf(doc), "migrate")
				if isMigration && !isMigrationJob {
					t.Fatalf("%s 的 %s/%s 执行了迁移命令（%q）：迁移只能由 Migration Job 负责", set.name, kindOf(doc), nameOf(doc), joined)
				}
			}
		}
	}

	// 3. 迁移 Job 的启动命令仍然是唯一合法的迁移入口。
	job := migrationJob(t)
	jobContainer := containerByName(t, job, "migrate")
	if got := strings.Join(optStringSlice(t, jobContainer["command"], "migrate command"), " "); got != "/app/my-shop" {
		t.Fatalf("迁移 Job command = %q，期望 /app/my-shop", got)
	}
	if got := strings.Join(optStringSlice(t, jobContainer["args"], "migrate args"), " "); got != "migrate up" {
		t.Fatalf("迁移 Job args = %q，期望 \"migrate up\"（不得包含 force）", got)
	}

	// 4. Application 入口不得包含 Job：否则 kubectl apply 会顺手发起迁移。
	if jobs := app.byKind("Job"); len(jobs) != 0 {
		t.Fatalf("Application 入口不得包含 Job（实际 %d 个）", len(jobs))
	}
}

// optStringSlice 读取可选的字符串列表；字段缺失时返回 nil 而不是测试失败。
func optStringSlice(t *testing.T, value any, label string) []string {
	t.Helper()
	if value == nil {
		return nil
	}
	return stringSlice(t, value, label)
}

// ---------------------------------------------------------------------------
// 验证 Job
// ---------------------------------------------------------------------------

// TestVerificationJobsProveProtocols 断言验证 Job 做的是协议级验证，不是「Pod 起来了」。
func TestVerificationJobsProveProtocols(t *testing.T) {
	verify := build(t, infraVerifyEntrypoint)

	expected := map[string][]string{
		// Job 名 → 脚本里必须出现的关键命令（缺一项就说明验证被削弱）。
		"verify-mysql": {"mysql", "SELECT 1", "MYSQL_VERIFY_OK"},
		"verify-redis": {"redis-cli", "PONG", "REDIS_VERIFY_OK"},
		"verify-kafka": {"kafka-broker-api-versions.sh", "kafka-console-producer.sh", "kafka-console-consumer.sh", "KAFKA_VERIFY_OK"},
	}
	if len(verify.byKind("Job")) != len(expected) {
		t.Fatalf("验证入口 Job 数量 = %d，期望 %d（实际: %s）", len(verify.byKind("Job")), len(expected), strings.Join(verify.summary(), ", "))
	}

	for name, needles := range expected {
		job := verify.find(t, "Job", name)
		container := listAt(t, mapAt(t, job, "spec", "template", "spec"), "containers")[0]

		// 必须覆盖镜像入口，否则验证容器会启动服务端本身（Kafka 会再起一个 Broker）。
		command := optStringSlice(t, container["command"], name+" command")
		if len(command) == 0 {
			t.Fatalf("Job/%s 必须显式覆盖镜像 command", name)
		}

		script := strings.Join(command, "\n")
		for _, needle := range needles {
			if !strings.Contains(script, needle) {
				t.Fatalf("Job/%s 的验证脚本缺少 %q：验证被削弱（脚本：%s）", name, needle, script)
			}
		}

		// 验证失败必须体现为 Job 失败，而不是被重试掩盖。
		if got := intOf(t, dig(job, "spec", "backoffLimit"), "backoffLimit"); got != 0 {
			t.Fatalf("Job/%s backoffLimit = %d，期望 0（验证失败即失败）", name, got)
		}
		if got := stringOf(dig(job, "spec", "template", "spec", "restartPolicy")); got != "Never" {
			t.Fatalf("Job/%s restartPolicy = %q，期望 Never", name, got)
		}
	}

	// Kafka 验证必须断言「广播地址 == Service DNS」，否则连上 bootstrap 却收发失败时看不出来。
	kafkaJob := verify.find(t, "Job", "verify-kafka")
	kafkaScript := strings.Join(optStringSlice(t, listAt(t, mapAt(t, kafkaJob, "spec", "template", "spec"), "containers")[0]["command"], "command"), "\n")
	if !strings.Contains(kafkaScript, "KAFKA_BOOTSTRAP_INTERNAL") {
		t.Fatal("Job/verify-kafka 必须使用 ConfigMap 中的 KAFKA_BOOTSTRAP_INTERNAL，避免验证地址与生产地址不同源")
	}
	if !strings.Contains(kafkaScript, "grep -qF \"${BOOTSTRAP} (id:\"") {
		t.Fatal("Job/verify-kafka 必须断言 broker 广播地址就是 Service DNS")
	}

	// 验证 Job 不得设置 ttlSecondsAfterFinished：日志要在失败时可供排查。
	for _, doc := range verify.byKind("Job") {
		if dig(doc, "spec", "ttlSecondsAfterFinished") != nil {
			t.Fatalf("Job/%s 不得设置 ttlSecondsAfterFinished：失败日志会被提前清理", nameOf(doc))
		}
	}
}

// TestInfrastructureImageTagsArePinned 断言依赖镜像都带显式标签（不是 latest / 空标签）。
func TestInfrastructureImageTagsArePinned(t *testing.T) {
	sets := []manifestSet{build(t, infraEntrypoint), build(t, infraVerifyEntrypoint)}
	seen := make(map[string]bool)
	for _, set := range sets {
		for _, doc := range set {
			podSpec, ok := dig(doc, "spec", "template", "spec").(map[string]any)
			if !ok {
				continue
			}
			containers := append(listAt(t, podSpec, "containers"), listOptionalContainers(t, podSpec, "initContainers")...)
			for _, container := range containers {
				image := stringOf(container["image"])
				if image == "" {
					t.Fatalf("%s/%s 的容器 %s 缺少 image", kindOf(doc), nameOf(doc), stringOf(container["name"]))
				}
				tag := imageTag(image)
				if tag == "" || tag == "latest" {
					t.Fatalf("%s/%s 镜像 %q 未固定版本（tag=%q）", kindOf(doc), nameOf(doc), image, tag)
				}
				seen[fmt.Sprintf("%s:%s", strings.Split(image, ":")[0], tag)] = true
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("没有检查到任何依赖镜像")
	}
}
