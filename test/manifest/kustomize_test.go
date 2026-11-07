// Package manifest 校验 T2-06 的 Kustomize 部署体系。
//
// 校验对象是**真实构建产物**，不是源 YAML 文本：
//
//	kubectl kustomize manifest/kubernetes/overlays/production
//	kubectl kustomize manifest/kubernetes/migration/overlays/production
//
// 因此 overlay 的 patch 是否真正生效、image 是否被替换、Job 名称是否带版本后缀，
// 都由构建结果断言，而不是「文件里写了什么」。
//
// 这里不做真实集群验证（本仓库没有目标集群）：调度、滚动发布、kubectl wait 流转、
// PVC 绑定、镜像拉取凭据均为 NOT_VERIFIED，见 docs/development/kubernetes.md §12。
package manifest

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	// appEntrypoint 是 Application 部署入口（发布顺序中第二步）。
	appEntrypoint = "manifest/kubernetes/overlays/production"
	// migrationEntrypoint 是 Migration 部署入口（发布顺序中第一步）。
	migrationEntrypoint = "manifest/kubernetes/migration/overlays/production"
)

// repoRoot 返回仓库根目录（go test 的工作目录是包目录）。
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("解析仓库根目录: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("无法定位仓库根目录（%s）: %v", root, err)
	}
	return root
}

// kustomizeInvocation 返回可执行的 kustomize 构建命令前缀。
//
// 优先 kubectl（与文档、发布脚本、人工运维命令保持同一条路径），
// 其次独立 kustomize CLI；两者都没有时直接失败——「没有构建工具」不等于「校验通过」。
func kustomizeInvocation(t *testing.T) (string, []string) {
	t.Helper()
	if custom := strings.TrimSpace(os.Getenv("KUSTOMIZE_BIN")); custom != "" {
		fields := strings.Fields(custom)
		return fields[0], fields[1:]
	}
	if path, err := exec.LookPath("kubectl"); err == nil {
		return path, []string{"kustomize"}
	}
	if path, err := exec.LookPath("kustomize"); err == nil {
		return path, []string{"build"}
	}

	// 缺少构建工具时不允许把「没验证」当成「通过」：
	//   - scripts/test-k8s-manifest.sh 会设置 K8S_MANIFEST_REQUIRED=1，直接失败；
	//   - 其它 `go test ./...`（如集成门禁）跳过本包，跳过状态在测试输出中明确可见，
	//     不产生 PASS 结论。
	if os.Getenv("K8S_MANIFEST_REQUIRED") == "1" {
		t.Fatal("未找到 kubectl 或 kustomize：无法验证 Kustomize 构建产物。" +
			"请安装 kubectl（推荐）或独立 Kustomize CLI 后重试；不得以静态文本匹配代替构建")
	}
	t.Skip("未找到 kubectl/kustomize，跳过 Kustomize 构建校验（需设置 K8S_MANIFEST_REQUIRED=1 强制执行）")
	return "", nil
}

// build 执行 Kustomize 构建并解析为多文档资源列表。
func build(t *testing.T, entrypoint string) manifestSet {
	t.Helper()
	bin, argsPrefix := kustomizeInvocation(t)
	args := append(append([]string{}, argsPrefix...), filepath.Join(repoRoot(t), entrypoint))

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %s 失败: %v\n%s", filepath.Base(bin), strings.Join(args, " "), err, stderr.String())
	}
	return parseDocuments(t, entrypoint, stdout.Bytes())
}

// parseDocuments 解析多文档 YAML，空文档被跳过。
func parseDocuments(t *testing.T, name string, raw []byte) manifestSet {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var set manifestSet
	for {
		var doc map[string]any
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("解析 %s 的构建产物失败: %v", name, err)
		}
		if len(doc) == 0 {
			continue
		}
		set = append(set, doc)
	}
	if len(set) == 0 {
		t.Fatalf("%s 的构建产物为空", name)
	}
	return set
}

// manifestSet 是一组已渲染的 Kubernetes 资源。
type manifestSet []map[string]any

// find 按 kind + name 查找资源，找不到即失败。
func (s manifestSet) find(t *testing.T, kind, name string) map[string]any {
	t.Helper()
	for _, doc := range s {
		if kindOf(doc) == kind && nameOf(doc) == name {
			return doc
		}
	}
	t.Fatalf("构建产物中缺少 %s/%s（实际包含: %s）", kind, name, strings.Join(s.summary(), ", "))
	return nil
}

// byKind 返回指定 kind 的全部资源。
func (s manifestSet) byKind(kind string) []map[string]any {
	var out []map[string]any
	for _, doc := range s {
		if kindOf(doc) == kind {
			out = append(out, doc)
		}
	}
	return out
}

// single 断言某 kind 恰好有一个资源并返回。
func (s manifestSet) single(t *testing.T, kind string) map[string]any {
	t.Helper()
	items := s.byKind(kind)
	if len(items) != 1 {
		t.Fatalf("构建产物中 %s 数量为 %d，期望 1（实际包含: %s）", kind, len(items), strings.Join(s.summary(), ", "))
	}
	return items[0]
}

// summary 返回 "Kind/name" 列表，用于失败信息。
func (s manifestSet) summary() []string {
	out := make([]string, 0, len(s))
	for _, doc := range s {
		out = append(out, kindOf(doc)+"/"+nameOf(doc))
	}
	return out
}

// kindOf / nameOf 是安全取值（缺失时返回空串）。
func kindOf(doc map[string]any) string { return stringOf(doc["kind"]) }

func nameOf(doc map[string]any) string {
	metadata, _ := doc["metadata"].(map[string]any)
	return stringOf(metadata["name"])
}

// dig 按路径取出嵌套值，路径缺失或类型不符时返回 nil。
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

// mapAt 返回路径上的 map，缺失时失败。
func mapAt(t *testing.T, doc map[string]any, keys ...string) map[string]any {
	t.Helper()
	value := dig(doc, keys...)
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s 不是 map（实际类型 %T）", strings.Join(keys, "."), value)
	}
	return object
}

// stringOf 把 YAML 标量转为字符串（数字与布尔按 YAML 文本形式）。
func stringOf(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprintf("%d", typed)
	case int64:
		return fmt.Sprintf("%d", typed)
	case float64:
		return strings.TrimSuffix(fmt.Sprintf("%v", typed), ".0")
	default:
		return fmt.Sprintf("%v", typed)
	}
}

// intOf 把 YAML 数值转为 int，缺失或非数值时失败。
func intOf(t *testing.T, value any, label string) int {
	t.Helper()
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		t.Fatalf("%s 不是数值（实际 %T=%v）", label, value, value)
		return 0
	}
}

// boolOf 读取布尔值，缺失或类型不符时失败。
func boolOf(t *testing.T, value any, label string) bool {
	t.Helper()
	typed, ok := value.(bool)
	if !ok {
		t.Fatalf("%s 不是布尔值（实际 %T=%v）", label, value, value)
	}
	return typed
}

// stringSlice 把 YAML 列表转为字符串切片。
func stringSlice(t *testing.T, value any, label string) []string {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("%s 不是列表（实际 %T）", label, value)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, stringOf(item))
	}
	return out
}
