package main

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// pathView 是一个分域视图文件（openapi/paths/<domain>.yaml）的内容。
type pathView struct {
	File  string
	Paths map[string]any
}

// pathViewReport 记录主/分域 Paths 一致性比对结果。
type pathViewReport struct {
	MainPaths int
	Viewed    int
	Planned   int
	Problems  []string
}

// verifyPathViews 结构化比较主文件 paths 与所有分域视图。
//
// 比较口径（不只比数量）：
//  1. 主文件中标记 x-status: planned 的路径属于「仅主文件」的契约占位，不参与比对，单独计数；
//  2. 同一条路径不得出现在多个分域文件中（避免评审视图拆散 PathItem）；
//  3. 主文件存在的非 planned 路径必须在分域文件中出现，反之亦然；
//  4. 同一路径的 PathItem 内容必须逐字段一致（JSON 归一化后深比较）。
func verifyPathViews(mainPaths map[string]any, views []pathView) pathViewReport {
	rep := pathViewReport{MainPaths: len(mainPaths)}

	// 分域视图：path -> 首次出现的文件；重复声明直接报错。
	domainPaths := map[string]string{}
	for _, v := range views {
		for p, item := range v.Paths {
			if prev, ok := domainPaths[p]; ok {
				rep.Problems = append(rep.Problems, fmt.Sprintf(
					"路径 %s 同时出现在分域文件 %s 与 %s（PathItem 被拆散）", p, prev, v.File))
				continue
			}
			domainPaths[p] = v.File
			rep.Viewed++
			if _, ok := item.(map[string]any); ok {
				continue
			}
			rep.Problems = append(rep.Problems, fmt.Sprintf("分域文件 %s 的 %s PathItem 不是映射", v.File, p))
		}
	}

	var mainReal []string
	for p, item := range mainPaths {
		m, ok := item.(map[string]any)
		if !ok {
			rep.Problems = append(rep.Problems, fmt.Sprintf("主文件 %s 的 PathItem 不是映射", p))
			continue
		}
		if isPlannedPathItem(m) {
			rep.Planned++
			continue
		}
		mainReal = append(mainReal, p)
	}
	sort.Strings(mainReal)

	for _, p := range mainReal {
		file, ok := domainPaths[p]
		if !ok {
			rep.Problems = append(rep.Problems, fmt.Sprintf("主文件存在路径 %s，但所有分域视图都缺少该路径", p))
			continue
		}
		mainItem := canonical(mainPaths[p])
		var domainItem any
		for _, v := range views {
			if v.File == file {
				domainItem = v.Paths[p]
				break
			}
		}
		if diff := diffCanonical(mainItem, canonical(domainItem)); diff != "" {
			rep.Problems = append(rep.Problems, fmt.Sprintf(
				"路径 %s 的主文件 PathItem 与分域文件 %s 不一致：%s", p, file, diff))
		}
	}

	for _, p := range sortedStringKeys(domainPaths) {
		if _, ok := mainPaths[p]; !ok {
			rep.Problems = append(rep.Problems, fmt.Sprintf(
				"分域文件 %s 存在路径 %s，但主文件缺少该路径", domainPaths[p], p))
		}
	}
	return rep
}

// isPlannedPathItem 判断 PathItem 是否为 planned 占位（x-status 声明在 operation 上）。
func isPlannedPathItem(item map[string]any) bool {
	for m, opAny := range item {
		if !httpMethods[strings.ToUpper(m)] {
			continue
		}
		op, ok := opAny.(map[string]any)
		if !ok {
			continue
		}
		if asString(op["x-status"]) == "planned" {
			return true
		}
	}
	return false
}

// diffCanonical 比较两份归一化 JSON 值，返回首个差异的可读描述（空表示一致）。
func diffCanonical(want, got any) string {
	if equalJSON(want, got) {
		return ""
	}
	wm, wok := want.(map[string]any)
	gm, gok := got.(map[string]any)
	if wok && gok {
		for _, k := range sortedMapKeys(wm) {
			gv, ok := gm[k]
			if !ok {
				return fmt.Sprintf("缺少键 %q", k)
			}
			if d := diffCanonical(wm[k], gv); d != "" {
				return k + "." + d
			}
		}
		for _, k := range sortedMapKeys(gm) {
			if _, ok := wm[k]; !ok {
				return fmt.Sprintf("多出键 %q", k)
			}
		}
	}
	return fmt.Sprintf("值不同（主文件 %v，分域 %v）", brief(want), brief(got))
}

func brief(v any) string {
	s := fmt.Sprintf("%v", v)
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}

func equalJSON(a, b any) bool {
	return reflect.DeepEqual(a, b)
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// loadPathViews 读取 openapi/paths/*.yaml 分域视图。
func loadPathViews(openapiDir string) ([]pathView, error) {
	files, err := filepath.Glob(filepath.Join(openapiDir, "paths", "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	var out []pathView
	for _, f := range files {
		raw, err := readYAML(f)
		if err != nil {
			return nil, err
		}
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: 顶层不是映射", f)
		}
		out = append(out, pathView{File: filepath.Base(f), Paths: m})
	}
	return out, nil
}

// checkPathViews 执行主/分域 Paths 一致性检查（供 -check 使用）。
func checkPathViews(openapiDir string, doc map[string]any) (pathViewReport, error) {
	mainPaths, ok := doc["paths"].(map[string]any)
	if !ok {
		return pathViewReport{}, fmt.Errorf("主文件 paths 不是映射")
	}
	views, err := loadPathViews(openapiDir)
	if err != nil {
		return pathViewReport{}, err
	}
	if len(views) == 0 {
		return pathViewReport{}, fmt.Errorf("openapi/paths 下没有分域视图文件")
	}
	return verifyPathViews(mainPaths, views), nil
}

// pathViewsSummary 生成检查成功时的统计说明。
func (r pathViewReport) Summary() string {
	return strings.TrimSpace(fmt.Sprintf(
		"主文件 %d 条路径（planned %d 条）与 %d 条分域路径逐字段一致",
		r.MainPaths, r.Planned, r.Viewed))
}
