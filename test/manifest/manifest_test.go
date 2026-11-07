// Package manifest 对 T2-05 的 Kubernetes 清单做静态校验。
//
// 这里不做真实集群验证（本仓库没有目标集群），只断言「语义上不能出错的字段」：
// Job 生命周期、非 root / 只读根文件系统 / capabilities 限制、Secret 边界、
// 迁移命令只能是 `migrate up`。真实集群行为见 docs/development/migrations.md 的 NOT_VERIFIED 列表。
package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// manifestDir 返回仓库中的 Kubernetes 清单目录。
func manifestDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("解析仓库根目录: %v", err)
	}
	dir := filepath.Join(root, "manifest", "kubernetes")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("Kubernetes 清单目录不存在: %v", err)
	}
	return dir
}

// loadYAML 读取并解析单个 YAML 文件为通用文档。
func loadYAML(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(manifestDir(t), name))
	if err != nil {
		t.Fatalf("读取 %s: %v", name, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("解析 %s: %v", name, err)
	}
	if len(doc) == 0 {
		t.Fatalf("%s 是空文档", name)
	}
	return doc
}

// dig 按路径取出嵌套值，路径缺失时返回 nil。
func dig(doc map[string]any, keys ...string) any {
	var current any = doc
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = object[key]
		if !ok {
			return nil
		}
	}
	return current
}

// requireString 断言路径上的值是非空字符串。
func requireString(t *testing.T, doc map[string]any, want string, keys ...string) {
	t.Helper()
	got, ok := dig(doc, keys...).(string)
	if !ok {
		t.Fatalf("%s 缺失或不是字符串", strings.Join(keys, "."))
	}
	if want != "" && got != want {
		t.Fatalf("%s = %q，期望 %q", strings.Join(keys, "."), got, want)
	}
}

// requireInt 断言路径上的值是指定整数。
func requireInt(t *testing.T, doc map[string]any, want int, keys ...string) {
	t.Helper()
	raw := dig(doc, keys...)
	switch value := raw.(type) {
	case int:
		if value != want {
			t.Fatalf("%s = %d，期望 %d", strings.Join(keys, "."), value, want)
		}
	case int64:
		if int(value) != want {
			t.Fatalf("%s = %d，期望 %d", strings.Join(keys, "."), value, want)
		}
	default:
		t.Fatalf("%s 缺失或不是整数（实际 %T）", strings.Join(keys, "."), raw)
	}
}

// requireBool 断言路径上的值是指定布尔值。
func requireBool(t *testing.T, doc map[string]any, want bool, keys ...string) {
	t.Helper()
	got, ok := dig(doc, keys...).(bool)
	if !ok {
		t.Fatalf("%s 缺失或不是布尔值", strings.Join(keys, "."))
	}
	if got != want {
		t.Fatalf("%s = %t，期望 %t", strings.Join(keys, "."), got, want)
	}
}

// jobContainer 返回 Job 的第一个容器。
func jobContainer(t *testing.T, job map[string]any) map[string]any {
	t.Helper()
	containers, ok := dig(job, "spec", "template", "spec", "containers").([]any)
	if !ok || len(containers) != 1 {
		t.Fatalf("Job 必须且只能有一个容器（实际 %d 个）", len(containers))
	}
	container, ok := containers[0].(map[string]any)
	if !ok {
		t.Fatal("容器定义不是对象")
	}
	return container
}

// TestMigrationJobLifecycle 校验 Job 的生命周期字段：不能无限重试、不能无限运行、
// 完成后必须能被清理。
func TestMigrationJobLifecycle(t *testing.T) {
	job := loadYAML(t, "migration-job.yaml")

	requireString(t, job, "batch/v1", "apiVersion")
	requireString(t, job, "Job", "kind")
	requireString(t, job, "surgecart-migrate", "metadata", "name")
	requireString(t, job, "surgecart", "metadata", "namespace")

	requireInt(t, job, 1, "spec", "parallelism")
	requireInt(t, job, 1, "spec", "completions")
	requireString(t, job, "Never", "spec", "template", "spec", "restartPolicy")

	// backoffLimit：允许因锁等待/数据库暂不可达重试，但必须有界。
	backoff, ok := dig(job, "spec", "backoffLimit").(int)
	if !ok || backoff <= 0 || backoff > 10 {
		t.Fatalf("backoffLimit 必须是 1..10 的有界重试（实际 %v）", dig(job, "spec", "backoffLimit"))
	}
	// activeDeadlineSeconds：必须有上限，避免 MDL/网络问题挂起发布流程。
	if deadline, ok := dig(job, "spec", "activeDeadlineSeconds").(int); !ok || deadline <= 0 {
		t.Fatalf("activeDeadlineSeconds 必须为正整数（实际 %v）", dig(job, "spec", "activeDeadlineSeconds"))
	}
	// ttlSecondsAfterFinished：完成后必须自动清理，且保留足够长的排障窗口（>= 1h）。
	if ttl, ok := dig(job, "spec", "ttlSecondsAfterFinished").(int); !ok || ttl < 3600 {
		t.Fatalf("ttlSecondsAfterFinished 必须 >= 3600（实际 %v）", dig(job, "spec", "ttlSecondsAfterFinished"))
	}
}

// TestMigrationJobSecurityContext 校验 T2-04 加固要求被完整继承。
func TestMigrationJobSecurityContext(t *testing.T) {
	job := loadYAML(t, "migration-job.yaml")

	requireBool(t, job, true, "spec", "template", "spec", "securityContext", "runAsNonRoot")
	requireInt(t, job, 10001, "spec", "template", "spec", "securityContext", "runAsUser")
	requireInt(t, job, 10001, "spec", "template", "spec", "securityContext", "runAsGroup")
	requireString(t, job, "RuntimeDefault", "spec", "template", "spec", "securityContext", "seccompProfile", "type")
	requireBool(t, job, false, "spec", "template", "spec", "automountServiceAccountToken")
	requireBool(t, job, false, "spec", "template", "spec", "enableServiceLinks")

	container := jobContainer(t, job)
	requireBool(t, container, false, "securityContext", "allowPrivilegeEscalation")
	requireBool(t, container, true, "securityContext", "readOnlyRootFilesystem")
	requireBool(t, container, false, "securityContext", "privileged")

	drops, ok := dig(container, "securityContext", "capabilities", "drop").([]any)
	if !ok || len(drops) != 1 || drops[0] != "ALL" {
		t.Fatalf("必须 drop 全部 capabilities（实际 %v）", dig(container, "securityContext", "capabilities", "drop"))
	}

	for _, resource := range []string{"requests", "limits"} {
		values, ok := dig(container, "resources", resource).(map[string]any)
		if !ok {
			t.Fatalf("resources.%s 缺失", resource)
		}
		for _, key := range []string{"cpu", "memory"} {
			if value, ok := values[key].(string); !ok || value == "" {
				t.Fatalf("resources.%s.%s 缺失", resource, key)
			}
		}
	}

	// 只读根文件系统下必须有可写的 /tmp（Go 运行期临时文件）。
	mounts, ok := dig(container, "volumeMounts").([]any)
	if !ok {
		t.Fatal("volumeMounts 缺失")
	}
	hasTmp := false
	for _, item := range mounts {
		if object, ok := item.(map[string]any); ok && object["mountPath"] == "/tmp" {
			hasTmp = true
		}
	}
	if !hasTmp {
		t.Fatal("只读根文件系统下必须挂载可写的 /tmp")
	}
}

// TestMigrationJobRunsOnlyMigrateUp 校验命令契约：只执行 migrate up，
// 不得出现 force（强制标记版本属于人工风险操作）。
func TestMigrationJobRunsOnlyMigrateUp(t *testing.T) {
	job := loadYAML(t, "migration-job.yaml")
	container := jobContainer(t, job)

	command, _ := dig(container, "command").([]any)
	args, _ := dig(container, "args").([]any)
	combined := make([]string, 0, len(command)+len(args))
	for _, item := range append(append([]any{}, command...), args...) {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("command/args 必须是字符串数组")
		}
		combined = append(combined, text)
	}
	if got, want := strings.Join(combined, " "), "/app/my-shop migrate up"; got != want {
		t.Fatalf("Job 命令 = %q，期望 %q", got, want)
	}
	for _, forbidden := range []string{"force", "serve", "down"} {
		for _, part := range combined {
			if part == forbidden {
				t.Fatalf("迁移 Job 不得执行 %q", forbidden)
			}
		}
	}
}

// TestMigrationJobUsesConfigMapAndSecret 校验配置与凭据边界：
// 非敏感配置来自 ConfigMap，数据库密码只能来自 Secret。
func TestMigrationJobUsesConfigMapAndSecret(t *testing.T) {
	job := loadYAML(t, "migration-job.yaml")
	container := jobContainer(t, job)

	envFrom, ok := dig(container, "envFrom").([]any)
	if !ok || len(envFrom) == 0 {
		t.Fatal("envFrom 缺失：配置与凭据必须来自 ConfigMap/Secret")
	}
	var configMapName, secretName string
	for _, item := range envFrom {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if ref, ok := object["configMapRef"].(map[string]any); ok {
			configMapName, _ = ref["name"].(string)
		}
		if ref, ok := object["secretRef"].(map[string]any); ok {
			secretName, _ = ref["name"].(string)
		}
	}
	if configMapName != "surgecart-migration-config" {
		t.Fatalf("ConfigMap 引用 = %q，期望 surgecart-migration-config", configMapName)
	}
	if secretName != "surgecart-db-credentials" {
		t.Fatalf("Secret 引用 = %q，期望 surgecart-db-credentials", secretName)
	}

	// 严格模式必须在 Job 内显式声明（环境变量优先于 ConfigMap，防止漏配时降级）。
	strict := false
	if env, ok := dig(container, "env").([]any); ok {
		for _, item := range env {
			object, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if object["name"] == "DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS" && object["value"] == "true" {
				strict = true
			}
		}
	}
	if !strict {
		t.Fatal("Job 必须显式设置 DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS=true")
	}

	// 镜像必须是不可变标签，禁止 latest。
	image, _ := dig(container, "image").(string)
	if image == "" {
		t.Fatal("镜像地址缺失")
	}
	if strings.HasSuffix(image, ":latest") || !strings.Contains(image, ":") {
		t.Fatalf("迁移镜像必须使用不可变版本标签（实际 %q）", image)
	}
}

// TestMigrationConfigMapBoundary 校验 ConfigMap 只承载非敏感配置。
func TestMigrationConfigMapBoundary(t *testing.T) {
	configMap := loadYAML(t, "migration-configmap.yaml")

	requireString(t, configMap, "ConfigMap", "kind")
	requireString(t, configMap, "surgecart-migration-config", "metadata", "name")

	data, ok := dig(configMap, "data").(map[string]any)
	if !ok {
		t.Fatal("ConfigMap 缺少 data")
	}
	if got, _ := data["DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS"].(string); got != "true" {
		t.Fatalf("ConfigMap 必须开启严格模式（实际 %v）", data["DEPLOYMENT_REQUIRE_EXTERNAL_ENDPOINTS"])
	}
	for _, key := range []string{
		"DATABASE_DEFAULT_HOST",
		"DATABASE_DEFAULT_PORT",
		"DATABASE_DEFAULT_USER",
		"DATABASE_DEFAULT_NAME",
		"DATABASE_DEFAULT_CHARSET",
	} {
		if value, ok := data[key].(string); !ok || value == "" {
			t.Fatalf("ConfigMap 缺少 %s", key)
		}
	}
	for key := range data {
		if strings.Contains(strings.ToUpper(key), "PASS") {
			t.Fatalf("ConfigMap 不得承载密码类配置（发现 %s）", key)
		}
	}
	// 生产迁移不得回退本机地址。
	host, _ := data["DATABASE_DEFAULT_HOST"].(string)
	for _, loopback := range []string{"localhost", "127.", "::1"} {
		if strings.HasPrefix(host, loopback) {
			t.Fatalf("ConfigMap 的数据库主机不得是回环地址（实际 %q）", host)
		}
	}
}

// TestMigrationSecretIsTemplate 校验 Secret 文件中只有占位符，没有真实凭据。
func TestMigrationSecretIsTemplate(t *testing.T) {
	secret := loadYAML(t, "migration-secret.example.yaml")

	requireString(t, secret, "Secret", "kind")
	data, ok := dig(secret, "stringData").(map[string]any)
	if !ok {
		t.Fatal("Secret 缺少 stringData")
	}
	pass, ok := data["DATABASE_DEFAULT_PASS"].(string)
	if !ok || pass == "" {
		t.Fatal("Secret 必须提供 DATABASE_DEFAULT_PASS")
	}
	if !strings.Contains(pass, "REPLACE_WITH") {
		t.Fatalf("Secret 示例必须是占位符，实际值为真实口令形状: %q", pass)
	}
}

// TestNoPlaintextCredentialsInManifests 兜底扫描：清单目录里不得出现真实凭据字样。
func TestNoPlaintextCredentialsInManifests(t *testing.T) {
	dir := manifestDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取清单目录: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("读取 %s: %v", entry.Name(), err)
		}
		content := string(raw)
		for _, forbidden := range []string{"MYSQL_ROOT_PASSWORD=", "password: root", "PASS: root"} {
			if strings.Contains(content, forbidden) {
				t.Fatalf("%s 出现疑似真实凭据: %q", entry.Name(), forbidden)
			}
		}
		if strings.Contains(content, "DATABASE_DEFAULT_PASS") &&
			!strings.Contains(content, "REPLACE_WITH") &&
			entry.Name() != "migration-secret.example.yaml" {
			t.Fatalf("%s 引用了 DATABASE_DEFAULT_PASS 但不是 Secret 模板", entry.Name())
		}
	}
}
