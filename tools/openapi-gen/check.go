package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// check 执行契约结构、引用、覆盖与一致性校验。
// 与生成器共用同一套解析器，避免"检查逻辑"与"生成逻辑"各写一份而互相漂移。
func check(repoRoot string) error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	openapiPath := filepath.Join(repoRoot, "openapi", "surgecart.openapi.yaml")
	raw, err := readYAML(openapiPath)
	if err != nil {
		return err
	}
	doc, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%s 顶层不是映射", openapiPath)
	}

	// ---- 1. 版本与基础结构 ----
	if v := asString(doc["openapi"]); v != "3.1.0" {
		add("OpenAPI 版本必须是 3.1.0，实际为 %q", v)
	}
	for _, key := range []string{"info", "paths", "components", "servers", "tags"} {
		if _, ok := doc[key]; !ok {
			add("缺少顶层字段 %q", key)
		}
	}

	// ---- 2. $ref 完整性 ----
	refProblems := verifyRefs(filepath.Dir(openapiPath), doc)
	problems = append(problems, refProblems...)

	// ---- 3. operation 结构与 operationId 唯一性 ----
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		return fmt.Errorf("paths 不是映射")
	}
	opIDs := map[string]string{}
	actual := map[string]*OMap{} // "METHOD /path" -> operation
	planned := map[string]bool{}
	pagesByKey := map[string]string{}
	authByKey := map[string]string{}
	permByKey := map[string]string{}
	dataShape := map[string]string{}

	for p, itemAny := range paths {
		if strings.HasPrefix(p, "/api/") || strings.Contains(p, "/api/v1") {
			add("path %q 不应包含 /api/v1 前缀", p)
		}
		item, ok := itemAny.(map[string]any)
		if !ok {
			add("path %q 的 PathItem 不是映射", p)
			continue
		}
		for m, opAny := range item {
			method := strings.ToUpper(m)
			if !httpMethods[method] {
				continue
			}
			op, ok := opAny.(map[string]any)
			if !ok {
				add("%s %s 的 operation 不是映射", method, p)
				continue
			}
			key := method + " " + pathToGoFrame(p)
			opID := asString(op["operationId"])
			if opID == "" {
				add("%s %s 缺少 operationId", method, p)
			} else if prev, ok := opIDs[opID]; ok {
				add("operationId %q 重复：%s 与 %s", opID, prev, key)
			} else {
				opIDs[opID] = key
			}
			if asString(op["summary"]) == "" {
				add("%s %s 缺少 summary", method, p)
			}
			if len(asSlice(op["tags"])) == 0 {
				add("%s %s 缺少 tags", method, p)
			}
			if _, ok := op["responses"]; !ok {
				add("%s %s 缺少 responses", method, p)
			}

			status := asString(op["x-status"])
			if status == "planned" {
				planned[key] = true
				if impl, ok := op["x-implemented"].(bool); ok && impl {
					add("%s %s 标记 planned 却声明已实现", method, p)
				}
				continue
			}
			if status != "" {
				add("%s %s 的 x-status 取值非法：%q", method, p, status)
			}
			actual[key] = NewOMap()
			pagesByKey[key] = asString(op["x-pagination-mode"])
			authByKey[key] = asString(op["x-auth"])
			permByKey[key] = asString(op["x-permission"])
			dataShape[key] = successDataShape(op)

			if err := checkPathParams(method, p, op); err != nil {
				add("%v", err)
			}
		}
	}

	// ---- 4. 与路由注册双向比对 ----
	routes, err := parseRouteFiles(repoRoot)
	if err != nil {
		return err
	}
	bound, err := parseBoundControllers(repoRoot)
	if err != nil {
		return err
	}
	cms, err := parseControllers(repoRoot)
	if err != nil {
		return err
	}
	defs, order, err := parseDTOs(repoRoot)
	if err != nil {
		return err
	}
	apis, err := assembleAPIs(routes, bound, defs, cms)
	if err != nil {
		return err
	}
	expected := map[string]*API{}
	for _, a := range apis {
		expected[a.Key()] = a
		if _, ok := actual[a.Key()]; !ok {
			add("路由 %s 未出现在 OpenAPI paths 中", a.Key())
		}
	}
	for key := range actual {
		if _, ok := expected[key]; !ok {
			add("OpenAPI paths 中的 %s 不是实际注册路由（可能是虚构接口）", key)
		}
	}
	for key := range planned {
		if _, ok := expected[key]; ok {
			add("planned 接口 %s 与实际注册路由冲突", key)
		}
	}

	if len(actual) != expectedTotal {
		add("实际 API 数量为 %d，基线为 %d", len(actual), expectedTotal)
	}
	if len(planned) != len(plannedAPIs) {
		add("planned 接口数量为 %d，期望 %d", len(planned), len(plannedAPIs))
	}

	// ---- 5. 认证 / 权限 / 分页 / data 形态 ----
	for key, api := range expected {
		if got := authByKey[key]; got != api.Auth {
			add("%s 的 x-auth 为 %q，实际为 %q", key, got, api.Auth)
		}
		if got := permByKey[key]; got != api.Permission {
			add("%s 的 x-permission 为 %q，实际为 %q", key, got, api.Permission)
		}
		meta := opMetaByKey[key]
		wantPage := "none"
		if meta != nil && meta.Pagination != "" {
			wantPage = meta.Pagination
		}
		if got := pagesByKey[key]; got != wantPage {
			add("%s 的 x-pagination-mode 为 %q，期望 %q", key, got, wantPage)
		}
		wantShape := "ref"
		if meta != nil && meta.NullData {
			wantShape = "null"
		}
		if got := dataShape[key]; got != wantShape {
			add("%s 的成功 data 形态为 %q，期望 %q", key, got, wantShape)
		}
	}

	// ---- 6. 权限码与 seed 三向一致 ----
	seedCodes, err := parsePermissionSeed(repoRoot)
	if err != nil {
		return err
	}
	routeCodes := map[string]bool{}
	for _, a := range apis {
		if a.Permission != "" {
			routeCodes[a.Permission] = true
		}
	}
	openapiCodes := map[string]bool{}
	for _, v := range permByKey {
		if v != "" {
			openapiCodes[v] = true
		}
	}
	compareSets(add, "路由权限码", "seed 权限码", routeCodes, seedCodes)
	compareSets(add, "OpenAPI 权限码", "路由权限码", openapiCodes, routeCodes)
	for _, a := range apis {
		if a.Auth == "admin" && a.Permission != "" && !seedCodes[a.Permission] {
			add("%s 使用的权限码 %q 不在 seed 清单中", a.Key(), a.Permission)
		}
	}

	// ---- 7. 错误码注册表与 codes.go 一致 ----
	errPath := filepath.Join(repoRoot, "openapi", "components", "error-codes.yaml")
	errRaw, err := readYAML(errPath)
	if err != nil {
		return err
	}
	codeList, err := parseErrorCodes(repoRoot)
	if err != nil {
		return err
	}
	crossDomain := crossDomainUsage(codeList, mustAnalyzeLogic(repoRoot))
	expectedErr := canonical(buildErrorCodeRegistry(codeList, crossDomain))
	if !reflect.DeepEqual(canonical(errRaw), expectedErr) {
		add("openapi/components/error-codes.yaml 与 internal/codes/codes.go 不一致（请重新运行生成器）")
	}

	// ---- 8. Schema 与 DTO 一致 ----
	schemas, err := buildSchemas(defs, order)
	if err != nil {
		return err
	}
	schemas.Set(successSchema, buildSuccessSchema())
	schemas.Set(errorSchema, buildErrorSchema())
	schemaRaw, err := readYAML(filepath.Join(repoRoot, "openapi", "components", "schemas.yaml"))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(canonical(schemaRaw), canonical(schemas)) {
		add("openapi/components/schemas.yaml 与 Go DTO 不一致（请重新运行生成器）")
	}

	// 每个 Schema 都必须在主文件 components.schemas 中登记。
	comp, _ := doc["components"].(map[string]any)
	compSchemas, _ := comp["schemas"].(map[string]any)
	for _, name := range schemas.Keys() {
		if _, ok := compSchemas[name]; !ok {
			add("components.schemas 缺少 %q", name)
		}
	}

	// ---- 9. 统计口径 ----
	if cnt, ok := doc["x-api-count"].(map[string]any); ok {
		if got := asInt(cnt["total"]); got != len(actual) {
			add("x-api-count.total 为 %d，实际 operation 数为 %d", got, len(actual))
		}
		if got := asInt(cnt["conditional"]); got != 1 {
			add("x-api-count.conditional 应为 1（/metrics）")
		}
		if got := asInt(cnt["static"]); got != 1 {
			add("x-api-count.static 应为 1（/storage/banners/*）")
		}
	} else {
		add("缺少 x-api-count")
	}
	if _, ok := doc["x-conditional-routes"]; !ok {
		add("缺少 x-conditional-routes（/metrics 条件路由必须显式登记）")
	}
	if _, ok := doc["x-static-routes"]; !ok {
		add("缺少 x-static-routes（/storage/banners/* 静态路由必须显式登记）")
	}

	// ---- 10. 未引用组件治理（替代 Spectral oas3-unused-component） ----
	unusedProblems, retained, err := checkUnusedComponents(repoRoot, doc)
	if err != nil {
		return err
	}
	problems = append(problems, unusedProblems...)

	// ---- 11. 主文件 paths 与分域视图结构化一致 ----
	viewRep, err := checkPathViews(filepath.Dir(openapiPath), doc)
	if err != nil {
		return err
	}
	problems = append(problems, viewRep.Problems...)

	// ---- 12. 跨包错误码补充登记表自校验 ----
	problems = append(problems, validateCrossPackageRegistry(repoRoot, cms, apis)...)

	if len(problems) > 0 {
		sort.Strings(problems)
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "契约检查失败:", p)
		}
		return fmt.Errorf("契约检查未通过（%d 项）", len(problems))
	}
	fmt.Printf("契约检查通过：%d 条常驻 API + %d 条 planned 占位，%d 个 Schema，%d 个未引用组件（均已在白名单登记）\n",
		len(actual), len(planned), schemas.Len(), len(retained))
	fmt.Printf("Paths 分域一致性：%s\n", viewRep.Summary())
	return nil
}

// successDataShape 提取 200 响应中 data 的形态（null 或 $ref）。
func successDataShape(op map[string]any) string {
	resp, _ := op["responses"].(map[string]any)
	ok200, _ := resp["200"].(map[string]any)
	content, _ := ok200["content"].(map[string]any)
	jsonContent, _ := content["application/json"].(map[string]any)
	schema, _ := jsonContent["schema"].(map[string]any)
	allOf, _ := schema["allOf"].([]any)
	for _, part := range allOf {
		m, ok := part.(map[string]any)
		if !ok {
			continue
		}
		props, ok := m["properties"].(map[string]any)
		if !ok {
			continue
		}
		data, ok := props["data"].(map[string]any)
		if !ok {
			continue
		}
		if asString(data["type"]) == "null" {
			return "null"
		}
		if asString(data["$ref"]) != "" {
			return "ref"
		}
	}
	return "unknown"
}

// checkPathParams 校验 path 模板中的参数都在 parameters 中声明为 path。
func checkPathParams(method, path string, op map[string]any) error {
	names := map[string]bool{}
	for _, seg := range strings.Split(path, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			names[strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")] = true
		}
	}
	declared := map[string]bool{}
	for _, p := range asSlice(op["parameters"]) {
		m, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if asString(m["in"]) == "path" {
			declared[asString(m["name"])] = true
		}
	}
	for n := range names {
		if !declared[n] {
			return fmt.Errorf("%s %s: 路径参数 {%s} 未声明为 path parameter", method, path, n)
		}
	}
	for n := range declared {
		if !names[n] {
			return fmt.Errorf("%s %s: 声明了 path parameter %q，但路径模板中不存在", method, path, n)
		}
	}
	return nil
}

// verifyRefs 递归验证所有 $ref 可解析。
func verifyRefs(rootDir string, mainDoc map[string]any) []string {
	var problems []string
	type docRef struct {
		dir string
		doc any
	}
	queue := []docRef{{dir: rootDir, doc: mainDoc}}
	visited := map[string]bool{}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		var walk func(v any)
		walk = func(v any) {
			switch t := v.(type) {
			case map[string]any:
				if ref := asString(t["$ref"]); ref != "" {
					file := ref
					fragment := ""
					if i := strings.Index(ref, "#"); i >= 0 {
						file = ref[:i]
						fragment = ref[i+1:]
					}
					targetDoc := cur.doc
					targetDir := cur.dir
					if file != "" {
						full := filepath.Clean(filepath.Join(cur.dir, file))
						raw, err := readYAML(full)
						if err != nil {
							problems = append(problems, fmt.Sprintf("$ref %q 无法解析：%v", ref, err))
							return
						}
						if !visited[full] {
							visited[full] = true
							queue = append(queue, docRef{dir: filepath.Dir(full), doc: raw})
						}
						targetDoc = raw
						targetDir = filepath.Dir(full)
					}
					_ = targetDir
					if _, err := resolveJSONPointer(targetDoc, fragment); err != nil {
						problems = append(problems, fmt.Sprintf("$ref %q 的 JSON Pointer 无法解析：%v", ref, err))
					}
				}
				for _, vv := range t {
					walk(vv)
				}
			case []any:
				for _, vv := range t {
					walk(vv)
				}
			}
		}
		walk(cur.doc)
	}
	return problems
}

// resolveJSONPointer 解析 RFC 6901 JSON Pointer（fragment 不含 '#'）。
func resolveJSONPointer(doc any, fragment string) (any, error) {
	if fragment == "" || fragment == "/" {
		return doc, nil
	}
	parts := strings.Split(strings.TrimPrefix(fragment, "/"), "/")
	cur := doc
	for _, part := range parts {
		part = strings.ReplaceAll(part, "~1", "/")
		part = strings.ReplaceAll(part, "~0", "~")
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[part]
			if !ok {
				return nil, fmt.Errorf("缺少键 %q", part)
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil, fmt.Errorf("数组下标 %q 非法", part)
			}
			cur = node[i]
		default:
			return nil, fmt.Errorf("无法在 %T 上继续解析 %q", cur, part)
		}
	}
	return cur, nil
}

// pathToGoFrame 把 OpenAPI 路径转回 GoFrame 的 :param 形式，用于与路由表对齐。
func pathToGoFrame(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			parts[i] = ":" + strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
		}
	}
	return strings.Join(parts, "/")
}

// parsePermissionSeed 解析 internal/boot/seed.go 的权限码清单。
func parsePermissionSeed(repoRoot string) (map[string]bool, error) {
	path := filepath.Join(repoRoot, "internal", "boot", "seed.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{Code:") {
			continue
		}
		start := strings.Index(line, `"`)
		if start < 0 {
			continue
		}
		end := strings.Index(line[start+1:], `"`)
		if end < 0 {
			continue
		}
		out[line[start+1:start+1+end]] = true
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s 中未解析到任何权限码", path)
	}
	return out, nil
}

func compareSets(add func(string, ...any), leftName, rightName string, left, right map[string]bool) {
	for k := range left {
		if !right[k] {
			add("%s 中的 %q 不在 %s 中", leftName, k, rightName)
		}
	}
	for k := range right {
		if !left[k] {
			add("%s 中的 %q 不在 %s 中", rightName, k, leftName)
		}
	}
}

func mustAnalyzeLogic(repoRoot string) *codeGraph {
	refs, err := analyzeLogic(repoRoot)
	if err != nil {
		return &codeGraph{
			funcCodes: map[string]map[string]map[string]bool{},
			calls:     map[string]map[string]map[string]bool{},
			all:       map[string]map[string]bool{},
		}
	}
	return refs
}

func crossDomainUsage(codeList []*ErrorCode, refs *codeGraph) map[int][]string {
	out := map[int][]string{}
	for _, c := range codeList {
		var users []string
		for domain, constants := range refs.all {
			if constants[c.Constant] && domain != c.Domain {
				users = append(users, domain)
			}
		}
		if len(users) > 0 {
			sort.Strings(users)
			out[c.Code] = users
		}
	}
	return out
}

// canonical 通过 JSON 往返归一化数值类型，便于深比较。
func canonical(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return v
	}
	return out
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	}
	return -1
}
