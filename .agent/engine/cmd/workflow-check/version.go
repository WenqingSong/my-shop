package main

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strings"
)

// 版本信息在发布构建时通过确定性的 ldflags 注入：
//
//	go build -trimpath -buildvcs=false \
//	  -ldflags "-X main.version=3.1.0 -X main.commit=<40-hex> -X main.buildSource=release"
//
// 未注入时保持 dev / unknown，避免开发构建被误认为正式发布资产。
// 这里不注入构建时间，发布资产必须可复现。
const frameworkName = "agent-workflow"

var (
	version     = "dev"
	commit      = "unknown"
	buildSource = "dev"
)

// versionInfo 是 `workflow-check version` 的稳定输出结构。
// 字段名同时用于文本 key: value 输出与 --json 输出。
type versionInfo struct {
	Framework        string `json:"framework"`
	FrameworkVersion string `json:"framework_version"`
	Binary           string `json:"binary"`
	BinaryVersion    string `json:"binary_version"`
	SourceCommit     string `json:"source_commit"`
	BuildSource      string `json:"build_source"`
	GoVersion        string `json:"go_version"`
	Platform         string `json:"platform"`
	GOOS             string `json:"goos"`
	GOARCH           string `json:"goarch"`
}

func normalizeVersionValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func currentVersionInfo() versionInfo {
	v := normalizeVersionValue(version, "dev")
	c := normalizeVersionValue(commit, "unknown")
	b := normalizeVersionValue(buildSource, "dev")
	return versionInfo{
		Framework:        frameworkName,
		FrameworkVersion: v,
		Binary:           "workflow-check",
		BinaryVersion:    v,
		SourceCommit:     c,
		BuildSource:      b,
		GoVersion:        runtime.Version(),
		Platform:         runtime.GOOS + "/" + runtime.GOARCH,
		GOOS:             runtime.GOOS,
		GOARCH:           runtime.GOARCH,
	}
}

// ordering 固定文本输出顺序，便于脚本与人工核对。
func (v versionInfo) ordering() [][2]string {
	return [][2]string{
		{"framework", v.Framework},
		{"framework_version", v.FrameworkVersion},
		{"binary", v.Binary},
		{"binary_version", v.BinaryVersion},
		{"source_commit", v.SourceCommit},
		{"build_source", v.BuildSource},
		{"go_version", v.GoVersion},
		{"platform", v.Platform},
		{"goos", v.GOOS},
		{"goarch", v.GOARCH},
	}
}

func writeVersionText(w io.Writer) error {
	info := currentVersionInfo()
	for _, kv := range info.ordering() {
		if _, err := fmt.Fprintf(w, "%s: %s\n", kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

func writeVersionJSON(w io.Writer) error {
	info := currentVersionInfo()
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	return nil
}

// runVersion 处理 `workflow-check version [--json]`。
// 它不读取 .agent/workflow.yaml、不解析仓库根、不触碰 Git，
// 因此在任意目录下都能输出二进制自身的版本事实。
func runVersion(args []string, stdout, stderr io.Writer) int {
	jsonOut := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		default:
			fmt.Fprintf(stderr, "error: version 不支持参数 %q\n", a)
			fmt.Fprintln(stderr, "usage: workflow-check version [--json]")
			return 2
		}
	}
	var err error
	if jsonOut {
		err = writeVersionJSON(stdout)
	} else {
		err = writeVersionText(stdout)
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: 输出失败: %v\n", err)
		return 2
	}
	return 0
}
