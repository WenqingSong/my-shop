package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const releaseCommit = "ade5dff2880fed9cd081b0f546e7caaae889f2dd"

// withVersionVars 临时替换包级版本变量，返回恢复函数。
func withVersionVars(t *testing.T, v, c, src string) {
	t.Helper()
	prevV, prevC, prevS := version, commit, buildSource
	version, commit, buildSource = v, c, src
	t.Cleanup(func() { version, commit, buildSource = prevV, prevC, prevS })
}

// captureStdout 捕获 fn 执行期间写入 os.Stdout 的内容。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	prev := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = prev }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out)
}

// parseVersionText 把 `key: value` 输出解析成 map，并校验每个非空行都符合格式。
func parseVersionText(t *testing.T, out string) map[string]string {
	t.Helper()
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) != 2 {
			t.Fatalf("版本输出行不是 key: value 形式: %q", line)
		}
		fields[parts[0]] = parts[1]
	}
	return fields
}

// 默认构建（未注入 ldflags）必须显式暴露 dev / unknown，不能伪装成发布资产。
func TestVersionDefaultsToDev(t *testing.T) {
	withVersionVars(t, "dev", "unknown", "dev")

	var code int
	out := captureStdout(t, func() {
		code = run([]string{"version"})
	})
	if code != 0 {
		t.Fatalf("version 期望 exit 0，实际 %d", code)
	}
	fields := parseVersionText(t, out)
	if fields["framework"] != "agent-workflow" {
		t.Fatalf("framework 期望 agent-workflow，实际 %q", fields["framework"])
	}
	if fields["framework_version"] != "dev" || fields["binary_version"] != "dev" {
		t.Fatalf("未注入版本时 framework/binary version 必须为 dev，实际 %q/%q", fields["framework_version"], fields["binary_version"])
	}
	if fields["source_commit"] != "unknown" {
		t.Fatalf("未注入 commit 时必须为 unknown，实际 %q", fields["source_commit"])
	}
	if fields["build_source"] != "dev" {
		t.Fatalf("未注入 build_source 时必须为 dev，实际 %q", fields["build_source"])
	}
	if fields["go_version"] == "" {
		t.Fatalf("go_version 不能为空")
	}
	if !strings.Contains(fields["platform"], "/") {
		t.Fatalf("platform 期望 GOOS/GOARCH，实际 %q", fields["platform"])
	}
}

// 注入版本后必须原样输出，且 Commit 与 Version 是两个独立字段。
func TestVersionInjected(t *testing.T) {
	withVersionVars(t, "3.1.0", releaseCommit, "release")

	var code int
	out := captureStdout(t, func() {
		code = run([]string{"version"})
	})
	if code != 0 {
		t.Fatalf("version 期望 exit 0，实际 %d", code)
	}
	fields := parseVersionText(t, out)
	if fields["framework_version"] != "3.1.0" {
		t.Fatalf("framework_version 期望 3.1.0，实际 %q", fields["framework_version"])
	}
	if fields["binary_version"] != "3.1.0" {
		t.Fatalf("binary_version 期望 3.1.0，实际 %q", fields["binary_version"])
	}
	if fields["source_commit"] != releaseCommit {
		t.Fatalf("source_commit 期望 %s，实际 %q", releaseCommit, fields["source_commit"])
	}
	if fields["build_source"] != "release" {
		t.Fatalf("build_source 期望 release，实际 %q", fields["build_source"])
	}
	if fields["binary"] != "workflow-check" {
		t.Fatalf("binary 期望 workflow-check，实际 %q", fields["binary"])
	}
}

// 空注入值不得变成空字段，必须回退到 dev / unknown。
func TestVersionEmptyInjectionFallsBack(t *testing.T) {
	withVersionVars(t, "", "  ", "")

	var code int
	out := captureStdout(t, func() {
		code = run([]string{"version"})
	})
	if code != 0 {
		t.Fatalf("version 期望 exit 0，实际 %d", code)
	}
	fields := parseVersionText(t, out)
	if fields["framework_version"] != "dev" || fields["source_commit"] != "unknown" || fields["build_source"] != "dev" {
		t.Fatalf("空注入值必须回退 dev/unknown，实际 %v", fields)
	}
}

// --json 必须是稳定可解析结构，字段名与文本模式一致。
func TestVersionJSON(t *testing.T) {
	withVersionVars(t, "3.1.0", releaseCommit, "release")

	var code int
	out := captureStdout(t, func() {
		code = run([]string{"version", "--json"})
	})
	if code != 0 {
		t.Fatalf("version --json 期望 exit 0，实际 %d", code)
	}
	var got versionInfo
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("version --json 不是合法 JSON: %v\n%s", err, out)
	}
	if got.FrameworkVersion != "3.1.0" || got.SourceCommit != releaseCommit || got.BuildSource != "release" {
		t.Fatalf("version --json 字段不匹配: %+v", got)
	}
	if got.Framework != "agent-workflow" || got.Binary != "workflow-check" {
		t.Fatalf("version --json 标识字段不匹配: %+v", got)
	}
}

// version 不能在缺少宿主 .agent/workflow.yaml、且当前目录不是 Git 仓库时失败。
func TestVersionDoesNotRequireHostConfig(t *testing.T) {
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(prev) }()

	if _, err := os.Stat(filepath.Join(dir, ".agent", "workflow.yaml")); !os.IsNotExist(err) {
		t.Fatalf("测试前提不成立：临时目录不应存在 workflow.yaml")
	}
	withVersionVars(t, "3.1.0", releaseCommit, "release")

	var code int
	out := captureStdout(t, func() {
		code = run([]string{"version"})
	})
	if code != 0 {
		t.Fatalf("version 在无宿主配置目录期望 exit 0，实际 %d", code)
	}
	if fields := parseVersionText(t, out); fields["framework_version"] != "3.1.0" {
		t.Fatalf("version 输出被宿主环境污染: %v", fields)
	}
}

// version 的非法参数保持 exit 2（输入错误），与 Gate 的 0/1/2 语义不冲突。
func TestVersionUnknownFlag(t *testing.T) {
	if code := run([]string{"version", "--bogus"}); code != 2 {
		t.Fatalf("version 非法参数期望 exit 2，实际 %d", code)
	}
}

// 新增子命令不得改变既有 Gate 调用的 exit code 语义。
func TestVersionDoesNotChangeGateSemantics(t *testing.T) {
	if code := run([]string{"gate"}); code != 2 {
		t.Fatalf("缺 gate/task 期望 exit 2，实际 %d", code)
	}
	if code := run([]string{"validate", "x"}); code != 2 {
		t.Fatalf("非 gate 子命令期望 exit 2，实际 %d", code)
	}
	dir := cliRepo(t, v2PassState)
	if code := run([]string{"gate", "coder-start", ".agent/tasks/demo", "--root", dir}); code != 0 {
		t.Fatalf("合法 coder-start 期望 exit 0，实际 %d", code)
	}
	dir = cliRepo(t, v2ContractPendingState)
	if code := run([]string{"gate", "coder-start", ".agent/tasks/demo", "--root", dir}); code != 1 {
		t.Fatalf("Contract PENDING 期望 exit 1，实际 %d", code)
	}
}
