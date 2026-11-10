package manifest

// T4-04：Grafana 监控看板清单的静态契约。
//
// 校验对象是**真实构建产物**：
//
//	kubectl kustomize manifest/kubernetes/monitoring/grafana/overlays/local
//
// 覆盖五类「一旦写错就会静默失效」的点（这些问题都不会让 Pod 起不来）：
//  1. 结构：入口只包含 Grafana 资源，不混入业务工作负载、RBAC 与 Secret；
//  2. 安全：restricted PSS 未被为「能跑起来」而放宽，且只读根文件系统下仍有必需的可写目录；
//  3. 数据源：必须是集群内 Service DNS + 固定 uid，不能是 localhost / 宿主机 IP / NodePort；
//  4. 看板：Dashboard JSON 可解析、UID 固定、面板引用的数据源 uid 与 Provisioning 一致、
//     面板 PromQL 只使用 T4-02 真实存在的指标名；
//  5. 有状态性：Grafana 数据库走 PVC，Service 是 ClusterIP，镜像版本固定。
//
// 真实运行时行为（Pod Ready、Data Source 健康、PromQL 有数据、Pod 重建后配置保留）
// 由 T4-04 报告中的集群验证记录承载，静态测试不冒充运行时验证。

import (
	"encoding/json"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

const (
	// grafanaEntrypoint 是本地 Grafana 部署入口。
	grafanaEntrypoint = "manifest/kubernetes/monitoring/grafana/overlays/local"
	// prometheusServiceDNS 是 Grafana 数据源必须使用的集群内 Service 地址。
	prometheusServiceDNS = "http://prometheus.monitoring.svc.cluster.local:9090"
	// grafanaDataSourceUID 是 Provisioning 与 Dashboard 共享的数据源 uid。
	grafanaDataSourceUID = "prometheus"
	// grafanaDashboardUID 是固定 Dashboard uid（重复部署不得产生新副本）。
	grafanaDashboardUID = "surgecart-overview"
	// grafanaImage 是本任务固定的 Grafana 版本。
	grafanaImage = "grafana/grafana:11.2.2"
)

// grafanaDatasourceConfig 只映射本任务关心的 Provisioning 字段。
type grafanaDatasourceConfig struct {
	APIVersion  int `yaml:"apiVersion"`
	Datasources []struct {
		Name      string         `yaml:"name"`
		UID       string         `yaml:"uid"`
		Type      string         `yaml:"type"`
		Access    string         `yaml:"access"`
		URL       string         `yaml:"url"`
		IsDefault bool           `yaml:"isDefault"`
		Editable  bool           `yaml:"editable"`
		JSONData  map[string]any `yaml:"jsonData"`
	} `yaml:"datasources"`
}

// grafanaDashboardJSON 只映射本任务关心的 Dashboard 字段。
type grafanaDashboardJSON struct {
	UID    string `json:"uid"`
	Title  string `json:"title"`
	Panels []struct {
		ID         int    `json:"id"`
		Title      string `json:"title"`
		Type       string `json:"type"`
		Datasource struct {
			UID string `json:"uid"`
		} `json:"datasource"`
		Targets []struct {
			Expr string `json:"expr"`
		} `json:"targets"`
	} `json:"panels"`
}

// TestGrafanaEntrypointShape 断言 Grafana 入口只有 Grafana 资源。
func TestGrafanaEntrypointShape(t *testing.T) {
	set := build(t, grafanaEntrypoint)

	counts := map[string]int{}
	for _, doc := range set {
		counts[kindOf(doc)]++
	}
	expected := map[string]int{
		"ConfigMap":             3, // 数据源 + Dashboard Provider + Dashboard
		"PersistentVolumeClaim": 1,
		"Deployment":            1,
		"Service":               1,
	}
	for kind, want := range expected {
		if counts[kind] != want {
			t.Fatalf("%s 数量=%d，期望 %d（实际: %s）", kind, counts[kind], want, strings.Join(set.summary(), ", "))
		}
	}
	for _, forbidden := range []string{"Namespace", "ClusterRole", "ClusterRoleBinding", "Role", "RoleBinding", "ServiceAccount", "Secret", "Ingress", "StatefulSet", "NodePort"} {
		if counts[forbidden] > 0 {
			t.Fatalf("Grafana 入口不得包含 %s（本任务不引入 RBAC/Secret/额外暴露面，实际: %s）", forbidden, strings.Join(set.summary(), ", "))
		}
	}

	set.find(t, "Deployment", "grafana")
	set.find(t, "Service", "grafana")
	set.find(t, "PersistentVolumeClaim", "grafana-data")
	set.find(t, "ConfigMap", "grafana-datasources")
	set.find(t, "ConfigMap", "grafana-dashboard-provider")
	set.find(t, "ConfigMap", "grafana-dashboard-surgecart")
}

// TestGrafanaPodSecurityRestricted 断言只读根文件系统下仍满足 restricted PSS，且可写目录齐备。
func TestGrafanaPodSecurityRestricted(t *testing.T) {
	set := build(t, grafanaEntrypoint)
	deployment := set.find(t, "Deployment", "grafana")
	podSpec := mapAt(t, deployment, "spec", "template", "spec")

	// Grafana 只访问 Prometheus HTTP 接口，不访问 API Server。
	if got, ok := podSpec["automountServiceAccountToken"]; ok && boolOf(t, got, "automountServiceAccountToken") {
		t.Fatal("Grafana 不需要访问 API Server，不得挂载 ServiceAccount Token")
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
	workload := containers[0].(map[string]any)
	containerSecurity := mapAt(t, workload, "securityContext")
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

	resources := mapAt(t, workload, "resources")
	if dig(resources, "requests", "cpu") == nil || dig(resources, "requests", "memory") == nil {
		t.Fatal("必须声明 CPU/Memory requests")
	}
	if dig(resources, "limits", "cpu") == nil || dig(resources, "limits", "memory") == nil {
		t.Fatal("必须声明 CPU/Memory limits")
	}

	// 只读根文件系统下，Grafana 仍然必须能写数据库、日志与临时目录。
	// 这里断言「可写目录确实被挂载」，而不是只看 securityContext。
	mounts := map[string]string{}
	for _, item := range workload["volumeMounts"].([]any) {
		mount := item.(map[string]any)
		mounts[stringOf(mount["name"])] = stringOf(mount["mountPath"])
	}
	volumes := map[string]map[string]any{}
	for _, item := range podSpec["volumes"].([]any) {
		volume := item.(map[string]any)
		if _, ok := volume["hostPath"]; ok {
			t.Fatalf("不得使用 hostPath 卷：%v", volume)
		}
		volumes[stringOf(volume["name"])] = volume
	}

	// Grafana 数据库（grafana.db）与插件必须在持久卷上，否则 Pod 重建即丢配置。
	dataVolume := volumes["data"]
	if dataVolume == nil {
		t.Fatal("缺少 data 卷")
	}
	if got := stringOf(dig(dataVolume, "persistentVolumeClaim", "claimName")); got != "grafana-data" {
		t.Fatalf("data 卷 claimName=%q，期望 grafana-data", got)
	}
	if got := mounts["data"]; got != "/var/lib/grafana" {
		t.Fatalf("data 卷挂载点=%q，期望 /var/lib/grafana", got)
	}

	for mountPath, reason := range map[string]string{
		"/var/log/grafana": "日志目录（只读根文件系统下需要 emptyDir）",
		"/tmp":             "临时目录（只读根文件系统下需要 emptyDir）",
	} {
		found := false
		for _, path := range mounts {
			if path == mountPath {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("缺少可写目录挂载 %s（%s）", mountPath, reason)
		}
	}

	// Provisioning 与 Dashboard 必须以只读方式挂载：配置由 Git 提供，运行期不得被改写。
	for _, name := range []string{"provisioning-datasources", "provisioning-dashboards", "dashboards"} {
		for _, item := range workload["volumeMounts"].([]any) {
			mount := item.(map[string]any)
			if stringOf(mount["name"]) != name {
				continue
			}
			if readOnly, ok := mount["readOnly"].(bool); !ok || !readOnly {
				t.Fatalf("%s 必须 readOnly=true", name)
			}
		}
	}
}

// TestGrafanaDatasourceUsesClusterDNS 断言数据源指向集群内 Prometheus Service。
func TestGrafanaDatasourceUsesClusterDNS(t *testing.T) {
	set := build(t, grafanaEntrypoint)
	configMap := set.find(t, "ConfigMap", "grafana-datasources")
	raw := stringOf(mapAt(t, configMap, "data")["datasource.yaml"])
	if strings.TrimSpace(raw) == "" {
		t.Fatal("grafana-datasources 缺少 datasource.yaml")
	}

	var config grafanaDatasourceConfig
	if err := yaml.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("解析 datasource.yaml 失败: %v", err)
	}
	if len(config.Datasources) != 1 {
		t.Fatalf("数据源数量=%d，期望 1", len(config.Datasources))
	}
	ds := config.Datasources[0]
	if ds.Type != "prometheus" {
		t.Fatalf("数据源 type=%q，期望 prometheus", ds.Type)
	}
	if ds.UID != grafanaDataSourceUID {
		t.Fatalf("数据源 uid=%q，期望固定 %q（Dashboard 面板按 uid 引用）", ds.UID, grafanaDataSourceUID)
	}
	if !ds.IsDefault {
		t.Fatal("数据源必须 isDefault=true")
	}
	if ds.Editable {
		t.Fatal("数据源必须 editable=false（配置以 Git 为准）")
	}
	if ds.URL != prometheusServiceDNS {
		t.Fatalf("数据源 url=%q，期望集群内 Service DNS %q", ds.URL, prometheusServiceDNS)
	}
	// 回环/宿主机地址/NodePort 在 Pod 内都不成立：localhost 会指向 Grafana 自身。
	for _, forbidden := range []string{"localhost", "127.0.0.1", "host.docker.internal", "0.0.0.0", ":30090"} {
		if strings.Contains(ds.URL, forbidden) {
			t.Fatalf("数据源地址包含被禁止的形式 %q", forbidden)
		}
	}
	// 与 Prometheus 的抓取间隔一致，否则 Grafana 会按更细的步长重复取样。
	if got := stringOf(ds.JSONData["timeInterval"]); got != "15s" {
		t.Fatalf("jsonData.timeInterval=%q，期望 15s（对齐 scrape_interval）", got)
	}
}

// TestGrafanaDashboardProvisioning 断言 Provider 配置与挂载路径、Dashboard JSON 互相一致。
func TestGrafanaDashboardProvisioning(t *testing.T) {
	set := build(t, grafanaEntrypoint)

	providerConfig := set.find(t, "ConfigMap", "grafana-dashboard-provider")
	raw := stringOf(mapAt(t, providerConfig, "data")["dashboard-provider.yaml"])
	if strings.TrimSpace(raw) == "" {
		t.Fatal("grafana-dashboard-provider 缺少 dashboard-provider.yaml")
	}
	var provider struct {
		Providers []struct {
			Name                  string `yaml:"name"`
			Folder                string `yaml:"folder"`
			DisableDeletion       bool   `yaml:"disableDeletion"`
			AllowUIUpdates        bool   `yaml:"allowUiUpdates"`
			UpdateIntervalSeconds int    `yaml:"updateIntervalSeconds"`
			Options               struct {
				Path string `yaml:"path"`
			} `yaml:"options"`
		} `yaml:"providers"`
	}
	if err := yaml.Unmarshal([]byte(raw), &provider); err != nil {
		t.Fatalf("解析 dashboard-provider.yaml 失败: %v", err)
	}
	if len(provider.Providers) != 1 {
		t.Fatalf("Provider 数量=%d，期望 1", len(provider.Providers))
	}
	p := provider.Providers[0]
	if p.Options.Path != "/var/lib/grafana/dashboards" {
		t.Fatalf("Provider path=%q，期望 /var/lib/grafana/dashboards（与 Deployment 挂载点一致）", p.Options.Path)
	}
	if p.AllowUIUpdates {
		t.Fatal("allowUiUpdates 必须为 false：Dashboard 以 ConfigMap 为唯一事实来源")
	}
	if !p.DisableDeletion {
		t.Fatal("disableDeletion 必须为 true：避免通过 API/UI 误删 Provision 的 Dashboard")
	}
	if p.UpdateIntervalSeconds <= 0 || p.UpdateIntervalSeconds > 60 {
		t.Fatalf("updateIntervalSeconds=%d，期望 (0, 60] 区间", p.UpdateIntervalSeconds)
	}

	dashboardConfig := set.find(t, "ConfigMap", "grafana-dashboard-surgecart")
	dashboardRaw := stringOf(mapAt(t, dashboardConfig, "data")["surgecart-overview.json"])
	if strings.TrimSpace(dashboardRaw) == "" {
		t.Fatal("grafana-dashboard-surgecart 缺少 surgecart-overview.json")
	}
	var dashboard grafanaDashboardJSON
	if err := json.Unmarshal([]byte(dashboardRaw), &dashboard); err != nil {
		t.Fatalf("Dashboard JSON 解析失败: %v", err)
	}
	if dashboard.UID != grafanaDashboardUID {
		t.Fatalf("Dashboard uid=%q，期望固定 %q", dashboard.UID, grafanaDashboardUID)
	}
	if dashboard.Title != "SurgeCart Overview" {
		t.Fatalf("Dashboard title=%q，期望 SurgeCart Overview", dashboard.Title)
	}
	if len(dashboard.Panels) < 10 || len(dashboard.Panels) > 14 {
		t.Fatalf("面板数量=%d，期望 10~14（避免堆砌面板）", len(dashboard.Panels))
	}

	seen := map[int]bool{}
	exprs := make([]string, 0, len(dashboard.Panels))
	for _, panel := range dashboard.Panels {
		if seen[panel.ID] {
			t.Fatalf("面板 id 重复：%d", panel.ID)
		}
		seen[panel.ID] = true
		if strings.TrimSpace(panel.Title) == "" {
			t.Fatalf("面板 %d 缺少标题", panel.ID)
		}
		// 面板必须引用 Provisioning 的数据源 uid，否则 Grafana 会落到「默认数据源」的隐式依赖上。
		if panel.Datasource.UID != grafanaDataSourceUID {
			t.Fatalf("面板 %q 数据源 uid=%q，期望 %q", panel.Title, panel.Datasource.UID, grafanaDataSourceUID)
		}
		if len(panel.Targets) == 0 {
			t.Fatalf("面板 %q 没有查询目标", panel.Title)
		}
		for _, target := range panel.Targets {
			if strings.TrimSpace(target.Expr) == "" {
				t.Fatalf("面板 %q 存在空 PromQL", panel.Title)
			}
			exprs = append(exprs, target.Expr)
		}
	}
	joined := strings.Join(exprs, "\n")

	// 只允许使用 T4-02 实际暴露的指标名：编造指标只会得到空面板。
	for _, metric := range []string{
		"surgecart_http_requests_total",
		"surgecart_http_request_duration_seconds_bucket",
		"surgecart_http_requests_in_flight",
		"go_goroutines",
		"go_memstats_heap_alloc_bytes",
		"surgecart_kafka_consumer_lag",
		"flashsale_outbox_backlog_pending",
		"surgecart_outbox_oldest_age_seconds",
		"surgecart_sampler_up",
	} {
		if !strings.Contains(joined, metric) {
			t.Fatalf("Dashboard 未使用指标 %q（四个维度必须有真实指标支撑）", metric)
		}
	}

	// 错误率必须区分 4xx 与 5xx：把 4xx 全算成服务端故障会误报秒杀限流。
	if !strings.Contains(joined, `status=~"5.."`) || !strings.Contains(joined, `status=~"4.."`) {
		t.Fatal("错误率面板必须分别针对 5xx 与 4xx 计算，且不使用原始 URL 作为维度")
	}
	// 延迟必须走 Histogram bucket（quantile），不得用 _sum/_count 的均值冒充 P95。
	if !strings.Contains(joined, "histogram_quantile") || !strings.Contains(joined, "_bucket") {
		t.Fatal("延迟面板必须使用 histogram_quantile 与 _bucket 序列")
	}
	// 所有查询都必须限定 job=surgecart：Prometheus 自身也会暴露 go_* 指标，
	// 不限定 job 会把 Prometheus 的 runtime 数据混进业务面板。
	for _, expr := range exprs {
		if strings.Contains(expr, "go_") || strings.Contains(expr, "process_cpu") {
			if !strings.Contains(expr, `job="surgecart"`) {
				t.Fatalf("Runtime 指标查询必须限定 job=\"surgecart\"：%s", expr)
			}
		}
	}
}

// TestGrafanaStatefulContract 断言 PVC、ClusterIP 暴露、镜像版本固定与健康探针。
func TestGrafanaStatefulContract(t *testing.T) {
	set := build(t, grafanaEntrypoint)

	pvc := set.find(t, "PersistentVolumeClaim", "grafana-data")
	accessModes := stringSlice(t, dig(pvc, "spec", "accessModes"), "accessModes")
	if len(accessModes) != 1 || accessModes[0] != "ReadWriteOnce" {
		t.Fatalf("accessModes=%v，期望 [ReadWriteOnce]（SQLite 单写者）", accessModes)
	}
	if storage := stringOf(dig(pvc, "spec", "resources", "requests", "storage")); storage == "" {
		t.Fatal("PVC 必须声明 storage request，否则无法调度")
	}

	service := set.find(t, "Service", "grafana")
	if got := stringOf(dig(service, "spec", "type")); got != "ClusterIP" {
		t.Fatalf("Service type=%q，期望 ClusterIP（不得 NodePort/LoadBalancer）", got)
	}
	ports := dig(service, "spec", "ports").([]any)
	port := ports[0].(map[string]any)
	if intOf(t, port["port"], "service.port") != 3000 {
		t.Fatalf("Service port=%v，期望 3000", port["port"])
	}

	deployment := set.find(t, "Deployment", "grafana")
	container := deployment["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	if got := stringOf(container["image"]); got != grafanaImage {
		t.Fatalf("镜像=%q，期望固定版本 %q（不得使用 latest 或浮动 tag）", got, grafanaImage)
	}
	if got := stringOf(container["imagePullPolicy"]); got == "" {
		t.Fatal("必须显式声明 imagePullPolicy")
	}

	for _, probe := range []string{"startupProbe", "livenessProbe", "readinessProbe"} {
		config := mapAt(t, container, probe)
		if got := stringOf(dig(config, "httpGet", "path")); got != "/api/health" {
			t.Fatalf("%s path=%q，期望 /api/health", probe, got)
		}
	}
}
