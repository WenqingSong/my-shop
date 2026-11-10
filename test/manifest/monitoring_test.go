package manifest

// T4-03：Prometheus 监控清单的静态契约。
//
// 校验对象同样是**真实构建产物**：
//
//	kubectl kustomize manifest/kubernetes/monitoring/prometheus/overlays/local
//
// 覆盖四类「一旦写错就会静默失效」的点：
//  1. 结构：监控入口只包含监控资源，不混入业务工作负载与 RBAC；
//  2. 安全：restricted PSS 未被放宽（非 root / 只读根 / drop ALL / seccomp / 无 hostPath）；
//  3. 抓取：目标必须是集群内 Service DNS（不是 localhost、宿主机 IP、NodePort）；
//  4. 有状态性：TSDB 走 PVC，Service 是 ClusterIP，镜像版本固定。
//
// 真实运行时行为（Target UP、PromQL 有数据）由 T4-03 报告中的集群验证记录承载，
// 静态测试不冒充运行时验证。

import (
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

const (
	// prometheusEntrypoint 是本地 Prometheus 部署入口。
	prometheusEntrypoint = "manifest/kubernetes/monitoring/prometheus/overlays/local"
	// applicationServiceDNS 是抓取目标必须使用的集群内 Service 地址。
	applicationServiceDNS = "surgecart.surgecart.svc.cluster.local:8000"
	// prometheusImage 是本任务固定的镜像版本（不得漂移为 latest 或其它大版本）。
	prometheusImage = "prom/prometheus:v2.53.0"
)

// prometheusScrapeConfig 只映射本任务关心的字段。
type prometheusScrapeConfig struct {
	Global struct {
		ScrapeInterval string `yaml:"scrape_interval"`
		ScrapeTimeout  string `yaml:"scrape_timeout"`
	} `yaml:"global"`
	ScrapeConfigs []struct {
		JobName        string `yaml:"job_name"`
		MetricsPath    string `yaml:"metrics_path"`
		ScrapeInterval string `yaml:"scrape_interval"`
		StaticConfigs  []struct {
			Targets []string `yaml:"targets"`
		} `yaml:"static_configs"`
	} `yaml:"scrape_configs"`
}

// TestPrometheusEntrypointShape 断言监控入口只有监控资源，没有业务工作负载与 RBAC。
func TestPrometheusEntrypointShape(t *testing.T) {
	set := build(t, prometheusEntrypoint)

	counts := map[string]int{}
	for _, doc := range set {
		counts[kindOf(doc)]++
	}
	expected := map[string]int{
		"Namespace":             1,
		"ConfigMap":             1,
		"PersistentVolumeClaim": 1,
		"Deployment":            1,
		"Service":               1,
	}
	for kind, want := range expected {
		if counts[kind] != want {
			t.Fatalf("%s 数量=%d，期望 %d（实际: %s）", kind, counts[kind], want, strings.Join(set.summary(), ", "))
		}
	}
	for _, forbidden := range []string{"ClusterRole", "ClusterRoleBinding", "Role", "RoleBinding", "ServiceAccount", "Job", "Secret", "Ingress", "StatefulSet"} {
		if counts[forbidden] > 0 {
			t.Fatalf("监控入口不得包含 %s（本任务不引入 RBAC 与额外组件，实际: %s）", forbidden, strings.Join(set.summary(), ", "))
		}
	}

	set.find(t, "Deployment", "prometheus")
	set.find(t, "Service", "prometheus")
	set.find(t, "PersistentVolumeClaim", "prometheus-data")
	set.find(t, "ConfigMap", "prometheus-config")
}

// TestPrometheusNamespaceEnforcesRestrictedPSS 断言命名空间自带 PSS 强制标签。
func TestPrometheusNamespaceEnforcesRestrictedPSS(t *testing.T) {
	set := build(t, prometheusEntrypoint)
	namespace := set.find(t, "Namespace", "monitoring")
	labels := mapAt(t, namespace, "metadata", "labels")
	if got := stringOf(labels["pod-security.kubernetes.io/enforce"]); got != "restricted" {
		t.Fatalf("monitoring 命名空间 pod-security enforce=%q，期望 restricted", got)
	}
}

// TestPrometheusPodSecurityRestricted 断言工作负载没有为「跑起来」放宽安全基线。
func TestPrometheusPodSecurityRestricted(t *testing.T) {
	set := build(t, prometheusEntrypoint)
	deployment := set.find(t, "Deployment", "prometheus")

	podSpec := mapAt(t, deployment, "spec", "template", "spec")
	// 不访问 Kubernetes API（静态配置），因此不需要 ServiceAccount Token。
	if got, ok := podSpec["automountServiceAccountToken"]; ok && boolOf(t, got, "automountServiceAccountToken") {
		t.Fatal("Prometheus 不需要访问 API Server，不得挂载 ServiceAccount Token")
	}
	for _, forbidden := range []string{"hostNetwork", "hostPID", "hostIPC"} {
		if host, ok := podSpec[forbidden]; ok && boolOf(t, host, forbidden) {
			t.Fatalf("restricted PSS 下不得启用 %s", forbidden)
		}
	}

	podSecurity := mapAt(t, podSpec, "securityContext")
	if !boolOf(t, podSecurity["runAsNonRoot"], "runAsNonRoot") {
		t.Fatal("Pod 必须 runAsNonRoot=true")
	}
	if uid := intOf(t, podSecurity["runAsUser"], "runAsUser"); uid == 0 {
		t.Fatal("Pod 不得以 root（uid 0）运行")
	}
	if got := stringOf(dig(podSecurity, "seccompProfile", "type")); got != "RuntimeDefault" {
		t.Fatalf("seccompProfile.type=%q，期望 RuntimeDefault", got)
	}

	containers := podSpec["containers"].([]any)
	if len(containers) != 1 {
		t.Fatalf("容器数量=%d，期望 1", len(containers))
	}
	container := containers[0].(map[string]any)
	containerSecurity := mapAt(t, container, "securityContext")
	if allow, ok := containerSecurity["allowPrivilegeEscalation"].(bool); !ok || allow {
		t.Fatal("容器必须 allowPrivilegeEscalation=false")
	}
	if privileged, ok := containerSecurity["privileged"]; ok && boolOf(t, privileged, "privileged") {
		t.Fatal("容器不得 privileged")
	}
	if !boolOf(t, containerSecurity["readOnlyRootFilesystem"], "readOnlyRootFilesystem") {
		t.Fatal("容器必须 readOnlyRootFilesystem=true")
	}
	dropped := stringSlice(t, dig(containerSecurity, "capabilities", "drop"), "capabilities.drop")
	if len(dropped) != 1 || !strings.EqualFold(dropped[0], "ALL") {
		t.Fatalf("capabilities.drop=%v，期望 [ALL]", dropped)
	}

	// 资源规格必须两端都给（缺 requests 会变 BestEffort，节点压力下先被驱逐）。
	resources := mapAt(t, container, "resources")
	if dig(resources, "requests", "cpu") == nil || dig(resources, "requests", "memory") == nil {
		t.Fatal("必须声明 CPU/Memory requests")
	}
	if dig(resources, "limits", "cpu") == nil || dig(resources, "limits", "memory") == nil {
		t.Fatal("必须声明 CPU/Memory limits")
	}

	// 卷不得是 hostPath：restricted PSS 与「监控数据不落到宿主机任意路径」双重原因。
	for _, volume := range podSpec["volumes"].([]any) {
		typed := volume.(map[string]any)
		if _, ok := typed["hostPath"]; ok {
			t.Fatalf("不得使用 hostPath 卷：%v", typed)
		}
	}
}

// TestPrometheusScrapeTargetUsesServiceDNS 断言抓取目标走集群内 Service DNS，
// 且抓取参数与 T4-02 的指标语义配套（15s 间隔、/metrics 路径）。
func TestPrometheusScrapeTargetUsesServiceDNS(t *testing.T) {
	set := build(t, prometheusEntrypoint)
	configMap := set.find(t, "ConfigMap", "prometheus-config")
	raw := stringOf(mapAt(t, configMap, "data")["prometheus.yml"])
	if strings.TrimSpace(raw) == "" {
		t.Fatal("prometheus-config 缺少 prometheus.yml")
	}

	var config prometheusScrapeConfig
	if err := yaml.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("解析 prometheus.yml 失败: %v", err)
	}

	var surgecart *struct {
		JobName        string `yaml:"job_name"`
		MetricsPath    string `yaml:"metrics_path"`
		ScrapeInterval string `yaml:"scrape_interval"`
		StaticConfigs  []struct {
			Targets []string `yaml:"targets"`
		} `yaml:"static_configs"`
	}
	for index := range config.ScrapeConfigs {
		if config.ScrapeConfigs[index].JobName == "surgecart" {
			surgecart = &config.ScrapeConfigs[index]
			break
		}
	}
	if surgecart == nil {
		t.Fatalf("prometheus.yml 缺少 job_name=surgecart 的抓取任务")
	}
	if surgecart.MetricsPath != "/metrics" {
		t.Fatalf("metrics_path=%q，期望 /metrics", surgecart.MetricsPath)
	}
	if surgecart.ScrapeInterval != "15s" {
		t.Fatalf("scrape_interval=%q，期望 15s", surgecart.ScrapeInterval)
	}
	if len(surgecart.StaticConfigs) != 1 || len(surgecart.StaticConfigs[0].Targets) != 1 {
		t.Fatalf("surgecart 抓取任务必须有且只有一个静态目标：%+v", surgecart.StaticConfigs)
	}
	if got := surgecart.StaticConfigs[0].Targets[0]; got != applicationServiceDNS {
		t.Fatalf("抓取目标=%q，期望集群内 Service DNS %q", got, applicationServiceDNS)
	}
	// 目标不得是回环/宿主机地址/NodePort（否则要么抓不到，要么绕过 Service 摘流语义）。
	for _, forbidden := range []string{"localhost", "127.0.0.1", "host.docker.internal", "0.0.0.0"} {
		if strings.Contains(applicationServiceDNS, forbidden) {
			t.Fatalf("抓取目标包含被禁止的地址形式 %q", forbidden)
		}
	}
	// scrape_timeout 必须小于 scrape_interval，否则抓取会堆积。
	if config.Global.ScrapeTimeout != "" && config.Global.ScrapeInterval != "" {
		if config.Global.ScrapeTimeout >= config.Global.ScrapeInterval {
			t.Fatalf("global.scrape_timeout(%s) 必须小于 scrape_interval(%s)",
				config.Global.ScrapeTimeout, config.Global.ScrapeInterval)
		}
	}
}

// TestPrometheusStatefulContract 断言 TSDB 持久化、ClusterIP 暴露、镜像版本固定与探针。
func TestPrometheusStatefulContract(t *testing.T) {
	set := build(t, prometheusEntrypoint)

	pvc := set.find(t, "PersistentVolumeClaim", "prometheus-data")
	accessModes := stringSlice(t, dig(pvc, "spec", "accessModes"), "accessModes")
	if len(accessModes) != 1 || accessModes[0] != "ReadWriteOnce" {
		t.Fatalf("accessModes=%v，期望 [ReadWriteOnce]（TSDB 单写者）", accessModes)
	}
	if storage := stringOf(dig(pvc, "spec", "resources", "requests", "storage")); storage == "" {
		t.Fatal("PVC 必须声明 storage request，否则无法调度")
	}

	service := set.find(t, "Service", "prometheus")
	if got := stringOf(dig(service, "spec", "type")); got != "ClusterIP" {
		t.Fatalf("Service type=%q，期望 ClusterIP（不得 NodePort/LoadBalancer）", got)
	}
	ports := dig(service, "spec", "ports").([]any)
	port := ports[0].(map[string]any)
	if intOf(t, port["port"], "service.port") != 9090 {
		t.Fatalf("Service port=%v，期望 9090", port["port"])
	}

	deployment := set.find(t, "Deployment", "prometheus")
	container := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	if got := stringOf(container["image"]); got != prometheusImage {
		t.Fatalf("镜像=%q，期望固定版本 %q（不得使用 latest）", got, prometheusImage)
	}
	if got := stringOf(container["imagePullPolicy"]); got == "" {
		t.Fatal("必须显式声明 imagePullPolicy")
	}

	// 保留期由 local overlay 覆盖：断言 patch 真的生效（构建产物而非源文件）。
	args := stringSlice(t, container["args"], "args")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--config.file=/etc/prometheus/prometheus.yml") {
		t.Fatalf("缺少 --config.file：%v", args)
	}
	if !strings.Contains(joined, "--storage.tsdb.path=/prometheus") {
		t.Fatalf("缺少 --storage.tsdb.path：%v", args)
	}
	if !strings.Contains(joined, "--storage.tsdb.retention.time=7d") {
		t.Fatalf("local overlay 的保留期覆盖未生效（期望 7d）：%v", args)
	}

	// 探针：存活不看就绪（避免「查询慢 → 重启」），就绪用 /-/ready。
	liveness := mapAt(t, container, "livenessProbe")
	if got := stringOf(dig(liveness, "httpGet", "path")); got != "/-/healthy" {
		t.Fatalf("livenessProbe path=%q，期望 /-/healthy", got)
	}
	readiness := mapAt(t, container, "readinessProbe")
	if got := stringOf(dig(readiness, "httpGet", "path")); got != "/-/ready" {
		t.Fatalf("readinessProbe path=%q，期望 /-/ready", got)
	}
	if dig(container, "startupProbe") == nil {
		t.Fatal("必须声明 startupProbe，避免首次加载 TSDB 被 liveness 判死")
	}
}
