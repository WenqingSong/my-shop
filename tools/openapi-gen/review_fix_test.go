package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// ---- R3：未引用组件治理 ----

// buildTestComponentGraph 构造一个与真实契约同构的最小文档：
//   - paths 引用 schemas/A；
//   - schemas/A 通过本地 #/B 嵌套引用 schemas/B；
//   - operation 的 security 引用 securitySchemes/bearerAuth；
//   - schemas/C 没有被任何地方引用。
func buildTestComponentGraph(t *testing.T) *componentGraph {
	t.Helper()
	main := map[string]any{
		"paths": map[string]any{
			"/a": map[string]any{
				"get": map[string]any{
					"security": []any{map[string]any{"bearerAuth": []any{}}},
					"responses": map[string]any{
						"200": map[string]any{
							"content": map[string]any{
								"application/json": map[string]any{
									"schema": map[string]any{"$ref": "#/components/schemas/A"},
								},
							},
						},
					},
				},
			},
		},
		"components": map[string]any{
			"schemas": map[string]any{
				"A": map[string]any{"$ref": "./components/schemas.yaml#/A"},
				"B": map[string]any{"$ref": "./components/schemas.yaml#/B"},
				"C": map[string]any{"$ref": "./components/schemas.yaml#/C"},
			},
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{"$ref": "./components/security.yaml#/bearerAuth"},
			},
		},
	}
	files := map[string]map[string]any{
		"schemas.yaml": {
			"A": map[string]any{"allOf": []any{map[string]any{"$ref": "#/B"}}},
			"B": map[string]any{"type": "object"},
			"C": map[string]any{"type": "object"},
		},
		"security.yaml": {
			"bearerAuth": map[string]any{"type": "http"},
		},
	}
	g, err := buildComponentGraph(main, files)
	if err != nil {
		t.Fatalf("buildComponentGraph: %v", err)
	}
	return g
}

func TestComponentReachability(t *testing.T) {
	g := buildTestComponentGraph(t)

	reach := g.reachable()
	for _, want := range []componentNode{
		{Type: "schemas", Name: "A"},                  // paths 直接引用
		{Type: "schemas", Name: "B"},                  // 组件嵌套引用
		{Type: "securitySchemes", Name: "bearerAuth"}, // security requirement 引用
	} {
		if !reach[want] {
			t.Errorf("%s 应判定为已引用", want)
		}
	}

	unused := g.unused()
	if len(unused) != 1 || unused[0] != (componentNode{Type: "schemas", Name: "C"}) {
		t.Fatalf("未引用组件应为 [schemas/C]，实际 %v", unused)
	}
}

func TestUnusedComponentAllowlist(t *testing.T) {
	g := buildTestComponentGraph(t)

	// 合法保留项：未引用但已登记理由 -> 通过，并出现在 retained 中。
	problems, retained := validateComponentUsage(g, []retainedComponent{
		{Type: "schemas", Name: "C", Reason: "DTO 覆盖审计"},
	})
	if len(problems) != 0 {
		t.Fatalf("合法保留项不应报错，实际 %v", problems)
	}
	if len(retained) != 1 || !strings.Contains(retained[0], "schemas/C") {
		t.Errorf("retained = %v", retained)
	}

	// 新增未引用组件（不在清单）-> 失败。
	problems, _ = validateComponentUsage(g, nil)
	if len(problems) != 1 || !strings.Contains(problems[0], "schemas/C") {
		t.Fatalf("未登记理由的未引用组件必须失败，实际 %v", problems)
	}

	// 过期白名单项（组件已不存在）-> 失败。
	problems, _ = validateComponentUsage(g, []retainedComponent{
		{Type: "schemas", Name: "C", Reason: "r"},
		{Type: "schemas", Name: "Removed", Reason: "r"},
	})
	if len(problems) != 1 || !strings.Contains(problems[0], "过期记录") {
		t.Fatalf("过期白名单项必须失败，实际 %v", problems)
	}

	// 已被正常引用的组件留在白名单 -> 失败。
	problems, _ = validateComponentUsage(g, []retainedComponent{
		{Type: "schemas", Name: "C", Reason: "r"},
		{Type: "schemas", Name: "A", Reason: "r"},
	})
	if len(problems) != 1 || !strings.Contains(problems[0], "实际已被引用") {
		t.Fatalf("已引用组件留在白名单必须失败，实际 %v", problems)
	}

	// 缺理由 -> 失败。
	problems, _ = validateComponentUsage(g, []retainedComponent{
		{Type: "schemas", Name: "C", Reason: "  "},
	})
	if len(problems) != 2 {
		t.Fatalf("缺理由应同时报「缺理由」与「未登记」，实际 %v", problems)
	}
}

func TestUnusedComponentsOnRepo(t *testing.T) {
	root := repoRoot(t)
	raw, err := readYAML(filepath.Join(root, "openapi", "surgecart.openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc, ok := raw.(map[string]any)
	if !ok {
		t.Fatal("主文档顶层不是映射")
	}
	problems, retained, err := checkUnusedComponents(root, doc)
	if err != nil {
		t.Fatalf("checkUnusedComponents: %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("仓库内未引用组件检查失败：%v", problems)
	}
	allow, err := loadUnusedAllowlist(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(retained) != len(allow) {
		t.Errorf("retained=%d，白名单条目=%d，二者必须一致", len(retained), len(allow))
	}
	if len(retained) == 0 {
		t.Error("白名单不应为空（成功响应模板与分页参数依赖它）")
	}
}

func TestAllowlistLoaderRejectsWildcard(t *testing.T) {
	var f allowlistFile
	f.Version = 1
	f.Groups = []allowlistGroup{{
		Type: "schemas", Category: "x", Reason: "r", Names: []string{"*Request"},
	}}
	if err := validateAllowlistStruct(&f); err == nil {
		t.Fatal("通配名称必须被拒绝")
	}
}

// ---- R4：主文件与分域 Paths 一致性 ----

func TestVerifyPathViews(t *testing.T) {
	mainPaths := map[string]any{
		"/a":       map[string]any{"get": map[string]any{"operationId": "a"}},
		"/b":       map[string]any{"post": map[string]any{"operationId": "b"}},
		"/planned": map[string]any{"get": map[string]any{"x-status": "planned", "operationId": "p"}},
	}
	okViews := []pathView{
		{File: "a.yaml", Paths: map[string]any{"/a": map[string]any{"get": map[string]any{"operationId": "a"}}}},
		{File: "b.yaml", Paths: map[string]any{"/b": map[string]any{"post": map[string]any{"operationId": "b"}}}},
	}
	rep := verifyPathViews(mainPaths, okViews)
	if len(rep.Problems) != 0 {
		t.Fatalf("一致场景不应报错：%v", rep.Problems)
	}
	if rep.Planned != 1 || rep.Viewed != 2 || rep.MainPaths != 3 {
		t.Errorf("统计错误：%+v", rep)
	}

	// 主文件有、分域缺失。
	rep = verifyPathViews(mainPaths, okViews[:1])
	if !containsSubstring(rep.Problems, "所有分域视图都缺少该路径") {
		t.Errorf("必须发现主文件多出的路径：%v", rep.Problems)
	}

	// 分域有、主文件缺失。
	rep = verifyPathViews(mainPaths, append(okViews, pathView{
		File: "c.yaml", Paths: map[string]any{"/ghost": map[string]any{"get": map[string]any{}}},
	}))
	if !containsSubstring(rep.Problems, "主文件缺少该路径") {
		t.Errorf("必须发现分域多出的路径：%v", rep.Problems)
	}

	// 同一路径出现在两个分域文件。
	rep = verifyPathViews(mainPaths, append(okViews, pathView{
		File: "dup.yaml", Paths: map[string]any{"/a": mainPaths["/a"]},
	}))
	if !containsSubstring(rep.Problems, "同时出现在分域文件") {
		t.Errorf("必须发现重复路径：%v", rep.Problems)
	}

	// 同一路径内容不一致（只比数量是发现不了的）。
	drift := []pathView{
		{File: "a.yaml", Paths: map[string]any{"/a": map[string]any{"get": map[string]any{"operationId": "changed"}}}},
		okViews[1],
	}
	rep = verifyPathViews(mainPaths, drift)
	if !containsSubstring(rep.Problems, "不一致") {
		t.Errorf("必须发现 PathItem 内容漂移：%v", rep.Problems)
	}
}

func TestPathViewsOnRepo(t *testing.T) {
	root := repoRoot(t)
	raw, err := readYAML(filepath.Join(root, "openapi", "surgecart.openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc := raw.(map[string]any)
	rep, err := checkPathViews(filepath.Join(root, "openapi"), doc)
	if err != nil {
		t.Fatalf("checkPathViews: %v", err)
	}
	if len(rep.Problems) != 0 {
		t.Fatalf("主文件与分域视图不一致：%v", rep.Problems)
	}
	if rep.Viewed == 0 || rep.Planned == 0 {
		t.Errorf("统计异常（分域路径 %d，planned %d）", rep.Viewed, rep.Planned)
	}
}

// ---- R1：D1 已知问题集合与 DTO required 字段一一对应 ----

func TestD1KnownIssuesCoverAllRequiredFields(t *testing.T) {
	root := repoRoot(t)
	routes, err := parseRouteFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := parseBoundControllers(root)
	if err != nil {
		t.Fatal(err)
	}
	defs, _, err := parseDTOs(root)
	if err != nil {
		t.Fatal(err)
	}
	cms, err := parseControllers(root)
	if err != nil {
		t.Fatal(err)
	}
	apis, err := assembleAPIs(routes, bound, defs, cms)
	if err != nil {
		t.Fatal(err)
	}

	// GoFrame 在 controller 执行前完成请求绑定与 gvalid 校验，
	// 因此任何「非 path 的 required 字段」缺失都会落进 code 51 → HTTP 500。
	affected := map[string]bool{}
	for _, a := range apis {
		for _, f := range a.Req.Fields {
			if f.Embedded || !f.Required() || f.In == "path" {
				continue
			}
			affected[a.Key()] = true
			break
		}
	}

	marked := map[string]bool{}
	for key, m := range opMetaByKey {
		for _, i := range m.KnownIssues {
			if v, _ := i.Get("id"); v == "B1-02-D1" {
				marked[key] = true
			}
		}
	}

	for key := range affected {
		if !marked[key] {
			t.Errorf("%s 存在非 path 的 required 字段，但契约未标注 B1-02-D1", key)
		}
	}
	for key := range marked {
		if !affected[key] {
			t.Errorf("%s 标注了 B1-02-D1，但 DTO 中不存在非 path 的 required 字段", key)
		}
	}
	if d1AffectedCount() != len(affected) {
		t.Errorf("d1AffectedCount=%d，实际受影响接口=%d", d1AffectedCount(), len(affected))
	}
	if len(affected) == 0 {
		t.Fatal("未解析到任何 required 字段，解析器可能失效")
	}
}

func containsSubstring(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// ---- R5：跨包错误码补充登记 ----

func TestCrossPackageRegistryMatchesRepo(t *testing.T) {
	root := repoRoot(t)
	routes, err := parseRouteFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := parseBoundControllers(root)
	if err != nil {
		t.Fatal(err)
	}
	defs, _, err := parseDTOs(root)
	if err != nil {
		t.Fatal(err)
	}
	cms, err := parseControllers(root)
	if err != nil {
		t.Fatal(err)
	}
	apis, err := assembleAPIs(routes, bound, defs, cms)
	if err != nil {
		t.Fatal(err)
	}
	if problems := validateCrossPackageRegistry(root, cms, apis); len(problems) != 0 {
		t.Fatalf("补充登记表与代码事实不一致：%v", problems)
	}
	if len(crossPackageErrorCodes) == 0 {
		t.Fatal("补充登记表不应为空（当前已确认存在跨包遗漏）")
	}
}

func TestCrossPackageRegistryRejectsFakeEntries(t *testing.T) {
	root := repoRoot(t)
	routes, _ := parseRouteFiles(root)
	bound, _ := parseBoundControllers(root)
	defs, _, _ := parseDTOs(root)
	cms, _ := parseControllers(root)
	apis, err := assembleAPIs(routes, bound, defs, cms)
	if err != nil {
		t.Fatal(err)
	}

	saved := crossPackageErrorCodes
	defer func() { crossPackageErrorCodes = saved }()

	crossPackageErrorCodes = map[string][]crossPkgCode{
		// 不存在的接口。
		"POST /no-such-api": {{Service: "Inventory", Method: "DeductInTx", Code: "CodeInventoryInsufficient", Why: "x"}},
		// 不存在的错误码常量。
		"POST /orders": {{Service: "Inventory", Method: "DeductInTx", Code: "CodeNotDefined", Why: "x"}},
	}
	problems := validateCrossPackageRegistry(root, cms, apis)
	if !containsSubstring(problems, "不存在的接口") {
		t.Errorf("必须发现虚构接口：%v", problems)
	}
	if !containsSubstring(problems, "禁止虚构") {
		t.Errorf("必须发现虚构错误码：%v", problems)
	}
}

func TestCrossPackageSupplementsAppearInContract(t *testing.T) {
	root := repoRoot(t)
	raw, err := readYAML(filepath.Join(root, "openapi", "surgecart.openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	doc := raw.(map[string]any)
	paths := doc["paths"].(map[string]any)

	codeList, err := parseErrorCodes(root)
	if err != nil {
		t.Fatal(err)
	}
	codeByConst := map[string]int{}
	for _, c := range codeList {
		codeByConst[c.Constant] = c.Code
	}

	checked := 0
	for apiKey, entries := range crossPackageErrorCodes {
		parts := strings.SplitN(apiKey, " ", 2)
		method, path := strings.ToLower(parts[0]), toOpenAPIPath(parts[1])
		item, ok := paths[path].(map[string]any)
		if !ok {
			t.Fatalf("契约中缺少路径 %s", path)
		}
		op, ok := item[method].(map[string]any)
		if !ok {
			t.Fatalf("契约中缺少 %s", apiKey)
		}
		// x-error-codes 由 buildResponses 写在 responses 对象内（沿用 B1-02 既有位置）。
		resp, ok := op["responses"].(map[string]any)
		if !ok {
			t.Fatalf("%s 缺少 responses", apiKey)
		}
		meta, ok := resp["x-error-codes"].(map[string]any)
		if !ok {
			t.Fatalf("%s 缺少 responses.x-error-codes", apiKey)
		}
		declared := map[int]bool{}
		for _, v := range asSlice(meta["codes"]) {
			declared[asInt(v)] = true
		}
		for _, e := range entries {
			code, ok := codeByConst[e.Code]
			if !ok {
				t.Fatalf("%s 的 %s 未在 codes.go 中定义", apiKey, e.Code)
			}
			if !declared[code] {
				t.Errorf("%s 的 x-error-codes 缺少补充码 %d（%s）", apiKey, code, e.Code)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("未校验任何补充错误码")
	}
}
