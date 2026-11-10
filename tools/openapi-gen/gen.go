package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 契约基线（B1-01 冻结的 HEAD）。
const contractBaseline = "82243c0fbce614e03c4ff57dcc829b5f68fb08f2"

// 142 条常驻 API 的期望分布（B1-01 §3.1）。
const (
	expectedTotal    = 142
	expectedFrontend = 67
	expectedAdmin    = 73
	expectedHealth   = 2
)

var codesByValue = map[int]*ErrorCode{}

// pathFileOrder 固定分域视图文件的输出顺序。
var pathFileOrder = []string{
	"iam", "admin", "categories", "product", "sku", "inventory", "address",
	"cart", "order", "refund", "coupon", "flashsale", "banner", "recommendation",
	"article", "review", "favorite", "like", "upload", "dashboard", "health",
}

// tagDescriptions 是 tag 的稳定说明与展示顺序。
var tagDescriptions = []struct {
	Name string
	Desc string
}{
	{"IAM", "用户注册、登录、刷新、会话管理与后台用户状态"},
	{"收货地址", "用户收货地址 CRUD（仅本人）"},
	{"后台认证", "管理员登录/登出/当前身份"},
	{"管理员", "管理员账号与角色分配"},
	{"角色", "角色 CRUD 与角色权限分配"},
	{"权限", "权限 CRUD"},
	{"分类", "商品分类树查询与后台写操作"},
	{"商品", "商品 SPU 查询与后台写操作"},
	{"SKU", "SKU 写操作"},
	{"库存", "库存查询、增减与流水"},
	{"购物车", "用户购物车条目管理"},
	{"订单", "订单创建、支付、取消、收货与后台管理"},
	{"退款", "退款申请、撤销、审核、执行与审计"},
	{"优惠券", "优惠券模板管理与用户领取"},
	{"秒杀", "秒杀活动管理与下单"},
	{"轮播图", "轮播图查询与后台管理"},
	{"推荐位", "推荐位查询与后台管理"},
	{"文章", "用户文章与互动"},
	{"评价", "商品评价"},
	{"收藏", "商品收藏"},
	{"点赞", "商品点赞"},
	{"上传", "七牛云直传凭证签发"},
	{"运营数据大屏", "后台运营数据聚合"},
	{"Health", "存活与就绪探针"},
}

// run 执行完整生成。
func run(repoRoot string) error {
	defs, order, err := parseDTOs(repoRoot)
	if err != nil {
		return err
	}
	cms, err := parseControllers(repoRoot)
	if err != nil {
		return err
	}
	routes, err := parseRouteFiles(repoRoot)
	if err != nil {
		return err
	}
	bound, err := parseBoundControllers(repoRoot)
	if err != nil {
		return err
	}
	codeList, err := parseErrorCodes(repoRoot)
	if err != nil {
		return err
	}
	refs, err := analyzeLogic(repoRoot)
	if err != nil {
		return err
	}

	codesByName := map[string]*ErrorCode{}
	codesByValue = map[int]*ErrorCode{}
	for _, c := range codeList {
		codesByName[c.Constant] = c
		codesByValue[c.Code] = c
	}

	apis, err := assembleAPIs(routes, bound, defs, cms)
	if err != nil {
		return err
	}
	if err := validateAPIs(apis); err != nil {
		return err
	}

	schemas, err := buildSchemas(defs, order)
	if err != nil {
		return err
	}
	// 统一响应包装 Schema（契约基础设施，不来自 DTO）。
	schemas.Set(successSchema, buildSuccessSchema())
	schemas.Set(errorSchema, buildErrorSchema())

	crossDomain := map[int][]string{}
	for _, c := range codeList {
		var users []string
		for domain, constants := range refs.all {
			if constants[c.Constant] && domain != c.Domain {
				users = append(users, domain)
			}
		}
		if len(users) > 0 {
			sort.Strings(users)
			crossDomain[c.Code] = users
		}
	}

	outRoot := filepath.Join(repoRoot, "openapi")
	if err := writeAll(outRoot, apis, schemas, codeList, crossDomain, defs, order, codesByName, refs, cms); err != nil {
		return err
	}
	if err := writeInventory(repoRoot, defs, order, apis); err != nil {
		return err
	}
	return nil
}

// assembleAPIs 把路由事实、DTO 与 controller 签名组装为 API 列表。
func assembleAPIs(routes []*API, boundDomains []string, defs map[string]*StructDef, cms map[string]controllerMethod) ([]*API, error) {
	usedReq := map[string]bool{}
	var out []*API

	fill := func(api *API) error {
		cm, ok := cms[api.Domain+"."+api.Handler]
		if !ok {
			return fmt.Errorf("%s %s: controller 中找不到方法 %s.%s",
				api.Method, api.Path, api.Domain, api.Handler)
		}
		reqKey := api.Domain + "." + cm.ReqType
		resKey := api.Domain + "." + cm.ResType
		req, ok := defs[reqKey]
		if !ok {
			return fmt.Errorf("%s %s: 请求 DTO %s 不存在", api.Method, api.Path, reqKey)
		}
		res, ok := defs[resKey]
		if !ok {
			return fmt.Errorf("%s %s: 响应 DTO %s 不存在", api.Method, api.Path, resKey)
		}
		if req.Meta.Path != api.Path || req.Meta.Method != api.Method {
			return fmt.Errorf("%s %s: DTO g.Meta 与路由注册不一致（g.Meta=%s %s）",
				api.Method, api.Path, req.Meta.Method, req.Meta.Path)
		}
		api.Req = req
		api.Res = res
		api.Summary = req.Meta.Summary
		api.Tags = req.Meta.Tags
		opID, err := operationIDFor(api.Domain, api.Handler)
		if err != nil {
			return err
		}
		api.OperationID = opID
		if usedReq[reqKey] {
			return fmt.Errorf("%s %s: 请求 DTO %s 被多条路由复用，契约无法区分",
				api.Method, api.Path, reqKey)
		}
		usedReq[reqKey] = true
		if api.Summary == "" || api.Tags == "" {
			return fmt.Errorf("%s %s: 请求 DTO 缺少 tags/summary", api.Method, api.Path)
		}
		return nil
	}

	for _, r := range routes {
		if err := fill(r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}

	for _, domain := range boundDomains {
		var keys []string
		for k := range cms {
			if strings.HasPrefix(k, domain+".") {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			handler := strings.TrimPrefix(k, domain+".")
			cm := cms[k]
			req := defs[domain+"."+cm.ReqType]
			if req == nil || req.Meta.Path == "" {
				return nil, fmt.Errorf("%s.%s: 自动绑定路由的请求 DTO 缺少 g.Meta path/method", domain, handler)
			}
			api := &API{
				Method:  req.Meta.Method,
				Path:    req.Meta.Path,
				Handler: handler,
				Domain:  domain,
				Auth:    "public",
				Source:  "internal/cmd/cmd.go",
			}
			if err := fill(api); err != nil {
				return nil, err
			}
			out = append(out, api)
		}
	}

	// 反向校验：所有带 g.Meta 的 Req 必须对应一条路由。
	for key, def := range defs {
		if def.Meta.Path == "" {
			continue
		}
		if strings.HasSuffix(def.Name, "Req") && !usedReq[key] {
			return nil, fmt.Errorf("请求 DTO %s（%s %s）已定义但未在 routes_*.go 注册",
				key, def.Meta.Method, def.Meta.Path)
		}
	}
	return out, nil
}

// validateAPIs 校验 operationId 唯一性与路径参数一致性。
func validateAPIs(apis []*API) error {
	opIDs := map[string]string{}
	keys := map[string]bool{}
	for _, a := range apis {
		if prev, ok := opIDs[a.OperationID]; ok {
			return fmt.Errorf("operationId %q 冲突：%s 与 %s", a.OperationID, prev, a.Key())
		}
		opIDs[a.OperationID] = a.Key()
		if keys[a.Key()] {
			return fmt.Errorf("路由重复注册：%s", a.Key())
		}
		keys[a.Key()] = true
	}
	return nil
}

// writeAll 写入 openapi/ 目录下的全部生成物。
func writeAll(
	outRoot string,
	apis []*API,
	schemas *OMap,
	codeList []*ErrorCode,
	crossDomain map[int][]string,
	defs map[string]*StructDef,
	order []string,
	codesByName map[string]*ErrorCode,
	refs *codeGraph,
	cms map[string]controllerMethod,
) error {
	componentsDir := filepath.Join(outRoot, "components")

	// ---- components/schemas.yaml ----
	if err := writeYAMLWithHeader(
		filepath.Join(componentsDir, "schemas.yaml"),
		[]string{
			"# 本文件由 tools/openapi-gen 从 api/*/v1/*.go 自动生成，禁止手工编辑。",
			"# 字段事实源：Go DTO 的 json / dc / v / in 标签；schema 名为稳定业务名。",
			"# 生成命令：go run ./tools/openapi-gen -repo .",
		},
		schemas,
	); err != nil {
		return err
	}

	// ---- components/error-codes.yaml ----
	if err := writeYAMLWithHeader(
		filepath.Join(componentsDir, "error-codes.yaml"),
		[]string{
			"# 本文件由 tools/openapi-gen 从 internal/codes/codes.go 自动生成，禁止手工编辑。",
			"# 错误码、HTTP 状态与 message 全部来自 codes.go；未做任何补全或推测。",
		},
		buildErrorCodeRegistry(codeList, crossDomain),
	); err != nil {
		return err
	}

	// ---- components/responses.yaml ----
	if err := writeYAMLWithHeader(
		filepath.Join(componentsDir, "responses.yaml"),
		[]string{"# 通用响应组件（由 tools/openapi-gen 生成）。"},
		buildResponsesComponent(),
	); err != nil {
		return err
	}

	// ---- components/parameters.yaml ----
	if err := writeYAMLWithHeader(
		filepath.Join(componentsDir, "parameters.yaml"),
		[]string{"# 通用参数组件（由 tools/openapi-gen 生成）。"},
		buildParametersComponent(),
	); err != nil {
		return err
	}

	// ---- components/security.yaml ----
	if err := writeYAMLWithHeader(
		filepath.Join(componentsDir, "security.yaml"),
		[]string{"# 认证方案（由 tools/openapi-gen 生成）。"},
		buildSecurityComponent(),
	); err != nil {
		return err
	}

	// ---- paths/*.yaml：分域视图 ----
	byPath := map[string][]*API{}
	pathOrder := []string{}
	for _, a := range apis {
		if _, ok := byPath[a.Path]; !ok {
			pathOrder = append(pathOrder, a.Path)
		}
		byPath[a.Path] = append(byPath[a.Path], a)
	}

	// 同一 path 必须归属同一文件，否则分域视图会拆散 PathItem。
	pathFile := map[string]string{}
	for _, p := range pathOrder {
		f := pathFileFor(byPath[p][0])
		for _, a := range byPath[p][1:] {
			if pathFileFor(a) != f {
				return fmt.Errorf("path %s 的多个 method 被分到不同文件（%s / %s），请调整 pathFileFor",
					p, f, pathFileFor(a))
			}
		}
		pathFile[p] = f
	}

	filePaths := map[string][]string{}
	for _, p := range pathOrder {
		filePaths[pathFile[p]] = append(filePaths[pathFile[p]], p)
	}

	for _, name := range pathFileOrder {
		paths, ok := filePaths[name]
		if !ok {
			continue
		}
		view := NewOMap()
		for _, p := range paths {
			item, err := buildPathItem(byPath[p], codesByName, refs, cms)
			if err != nil {
				return err
			}
			view.Set(toOpenAPIPath(p), item)
		}
		if err := writeYAMLWithHeader(
			filepath.Join(outRoot, "paths", name+".yaml"),
			[]string{
				"# 本文件是 openapi/surgecart.openapi.yaml 的分域视图，由 tools/openapi-gen 生成，禁止手工编辑。",
				"# OpenAPI 3.1 的 Path Item 不支持 $ref，因此 paths 必须内联在主文件；本文件用于分域评审与一致性校验。",
			},
			view,
		); err != nil {
			return err
		}
	}
	for name := range filePaths {
		if !contains(pathFileOrder, name) {
			return fmt.Errorf("分域视图文件 %s 未在 pathFileOrder 中登记", name)
		}
	}

	// ---- 主文件 ----
	main := buildMainDocument(apis, schemas, pathOrder, byPath, codesByName, refs, cms)
	return writeYAMLWithHeader(
		filepath.Join(outRoot, "surgecart.openapi.yaml"),
		[]string{
			"# SurgeCart 唯一 OpenAPI 3.1 契约入口。",
			"# 本文件由 tools/openapi-gen 生成，禁止手工编辑；字段事实源为 Go DTO。",
			"# 生成命令：go run ./tools/openapi-gen -repo .",
			fmt.Sprintf("# 契约基线：%s", contractBaseline),
		},
		main,
	)
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// buildMainDocument 组装主 OpenAPI 文档。
func buildMainDocument(
	apis []*API,
	schemas *OMap,
	pathOrder []string,
	byPath map[string][]*API,
	codes map[string]*ErrorCode,
	refs *codeGraph,
	cms map[string]controllerMethod,
) *OMap {
	frontend, admin, health := 0, 0, 0
	for _, a := range apis {
		switch a.Source {
		case "routes_frontend.go":
			frontend++
		case "routes_admin.go":
			admin++
		case "internal/cmd/cmd.go":
			health++
		}
	}
	if len(apis) != expectedTotal || frontend != expectedFrontend || admin != expectedAdmin || health != expectedHealth {
		fmt.Fprintf(os.Stderr,
			"警告：API 数量偏离基线（实际 total=%d frontend=%d admin=%d health=%d，期望 %d/%d/%d/%d）\n",
			len(apis), frontend, admin, health, expectedTotal, expectedFrontend, expectedAdmin, expectedHealth)
	}

	doc := NewOMap()
	doc.Set("openapi", "3.1.0")
	doc.Set("jsonSchemaDialect", "https://json-schema.org/draft/2020-12/schema")
	doc.Set("info", NewOMap().
		Set("title", "SurgeCart API").
		Set("version", "1.0.0").
		Set("description", "SurgeCart 商城后端 HTTP JSON API 契约。"+
			"统一响应包装为 {code,message,data}；成功 code=0，失败 code 见 components/error-codes.yaml。"+
			"本文档描述的是当前代码的实际行为，目标规范与现状的差异通过 x-known-issue / x-* 扩展字段标注。").
		Set("contact", NewOMap().Set("name", "SurgeCart Backend")))

	doc.Set("servers", []any{
		NewOMap().Set("url", "/").Set("description", "同源部署（Nginx 入口，无 /api/v1 版本前缀）"),
	})

	doc.Set("tags", buildTags(apis))

	// paths 必须内联（OpenAPI 3.1 的 Path Item 不支持 $ref）。
	paths := NewOMap()
	for _, p := range pathOrder {
		item, err := buildPathItem(byPath[p], codes, refs, cms)
		if err != nil {
			// buildPathItem 的错误已在写分域视图时暴露；此处不应失败。
			fmt.Fprintf(os.Stderr, "内部错误：%s: %v\n", p, err)
			continue
		}
		paths.Set(toOpenAPIPath(p), item)
	}
	// planned 接口：proposed 路径，标 x-status=planned，不参与 142 条统计。
	for _, p := range plannedAPIs {
		paths.Set(p.Path, buildPlannedPathItem(p))
	}
	doc.Set("paths", paths)

	// components：逐项 $ref 到被拆分的组件文件。
	comp := NewOMap()
	schemaRefs := NewOMap()
	for _, name := range schemas.Keys() {
		schemaRefs.Set(name, NewOMap().Set("$ref", "./components/schemas.yaml#/"+name))
	}
	comp.Set("schemas", schemaRefs)

	responseRefs := NewOMap()
	for _, name := range responsesComponentNames {
		responseRefs.Set(name, NewOMap().Set("$ref", "./components/responses.yaml#/"+name))
	}
	comp.Set("responses", responseRefs)

	paramRefs := NewOMap()
	for _, name := range parametersComponentNames {
		paramRefs.Set(name, NewOMap().Set("$ref", "./components/parameters.yaml#/"+name))
	}
	comp.Set("parameters", paramRefs)
	comp.Set("securitySchemes", NewOMap().
		Set("bearerAuth", NewOMap().Set("$ref", "./components/security.yaml#/bearerAuth")))
	doc.Set("components", comp)

	doc.Set("x-contract-baseline", contractBaseline)
	doc.Set("x-api-count", NewOMap().
		Set("total", len(apis)).
		Set("frontend", frontend).
		Set("admin", admin).
		Set("health", health).
		Set("conditional", 1).
		Set("static", 1))
	doc.Set("x-error-codes", NewOMap().Set("$ref", "./components/error-codes.yaml#/codes"))
	doc.Set("x-conditional-routes", []any{
		NewOMap().
			Set("method", "GET").
			Set("path", "/metrics").
			Set("condition", "flash_sale.metrics.enabled=true").
			Set("response_content_type", "text/plain; version=0.0.4; charset=utf-8").
			Set("wrapped_by_response_middleware", false).
			Set("counted_in_api_total", false).
			Set("note", "Prometheus 指标端点，注册在统一响应中间件之外；未开启时不注册。"),
	})
	doc.Set("x-static-routes", []any{
		NewOMap().
			Set("method", "GET").
			Set("path", "/storage/banners/*").
			Set("type", "static-files").
			Set("description", "本地轮播图占位图片目录（storage.LocalStorage），非 JSON API。").
			Set("counted_in_api_total", false),
	})
	doc.Set("x-planned-apis", buildPlannedIndex())
	doc.Set("x-known-issues", buildGlobalKnownIssues())
	return doc
}

var responsesComponentNames = []string{
	"SuccessResponse", "ErrorResponse",
	"BadRequest", "Unauthorized", "Forbidden", "NotFound",
	"Conflict", "TooManyRequests", "InternalError", "ServiceUnavailable",
}

var parametersComponentNames = []string{
	"PageStrictParam", "SizeStrictParam",
	"PageClampParam", "SizeClampParam",
	"IdempotencyKeyHeader",
}

// buildTags 收集实际使用的 tag，按稳定顺序输出。
func buildTags(apis []*API) []any {
	used := map[string]bool{}
	for _, a := range apis {
		used[a.Tags] = true
	}
	var out []any
	emitted := map[string]bool{}
	for _, t := range tagDescriptions {
		if used[t.Name] {
			out = append(out, NewOMap().Set("name", t.Name).Set("description", t.Desc))
			emitted[t.Name] = true
		}
	}
	for _, name := range sortedKeys(used) {
		if emitted[name] {
			continue
		}
		out = append(out, NewOMap().Set("name", name))
	}
	return out
}

// buildPlannedPathItem 构建 planned 接口占位（不虚构响应结构）。
func buildPlannedPathItem(p plannedAPI) *OMap {
	op := NewOMap()
	op.Set("tags", []any{p.Tag})
	op.Set("summary", p.Summary)
	op.Set("description", p.Description)
	op.Set("operationId", p.OperationID)
	op.Set("security", []any{})
	op.Set("x-status", "planned")
	op.Set("x-proposed", true)
	op.Set("x-implemented", false)
	op.Set("x-missing-ref", p.Missing)
	op.Set("x-auth", "public")
	params := plannedPathParams(p.Path)
	for _, q := range p.Query {
		params = append(params, q)
	}
	if len(params) > 0 {
		op.Set("parameters", params)
	}
	op.Set("responses", NewOMap().
		Set("200", NewOMap().
			Set("description", "规划中的成功响应。后端未实现，响应结构未冻结，此条目不含 schema，"+
				"禁止据此联调或生成客户端类型。")).
		Set("501", NewOMap().
			Set("description", "尚未实现。该路径为 proposed 契约占位，后端无对应路由；"+
				"当前访问不会返回此响应，而是命中框架 404 text/plain。")))
	return NewOMap().Set(strings.ToLower(p.Method), op)
}

// plannedPathParams 从 proposed 路径生成 path 参数声明。
// 仅 M1/M3 的 {id} 占位，类型为 int64（活动 id / 订单 id）。
func plannedPathParams(path string) []any {
	var out []any
	for _, seg := range strings.Split(path, "/") {
		if !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
		out = append(out, NewOMap().
			Set("name", name).
			Set("in", "path").
			Set("required", true).
			Set("schema", NewOMap().Set("type", "integer").Set("format", "int64")).
			Set("x-proposed", true))
	}
	return out
}

func buildPlannedIndex() []any {
	out := make([]any, 0, len(plannedAPIs))
	for _, p := range plannedAPIs {
		out = append(out, NewOMap().
			Set("missing_ref", p.Missing).
			Set("method", p.Method).
			Set("path", p.Path).
			Set("operationId", p.OperationID).
			Set("implemented", false).
			Set("counted_in_api_total", false))
	}
	return out
}

// buildGlobalKnownIssues 汇总跨接口的契约已知问题。
func buildGlobalKnownIssues() []any {
	return []any{
		NewOMap().
			Set("id", "B1-02-D1").
			Set("summary", "DTO gvalid 校验失败返回 HTTP 500 + code 51，与目标规范 400 + 1001 不一致").
			Set("affected", "13 条带 v:\"required\" 的写接口，见各 operation 的 x-known-issue").
			Set("remediation", "需单独立项修复错误码映射；B1-02 仅在契约层标注"),
		NewOMap().
			Set("id", "B1-01-H15.4").
			Set("summary", "分页三种形态并存：clamp（静默归一）/ strict（越界 400）/ none（无分页）").
			Set("contract_representation", "operation 级 x-pagination-mode"),
		NewOMap().
			Set("id", "B1-01-H15.5").
			Set("summary", "响应时间格式为 YYYY-MM-DD HH:MM:SS 且不含时区偏移，不是 RFC3339").
			Set("contract_representation", "字段级 x-format: gtime-local 与 pattern；请求侧 x-format: gtime-lenient"),
		NewOMap().
			Set("id", "B1-01-H15.2").
			Set("summary", "同一状态概念存在 string 与 int 两套 JSON 表示（订单/退款/秒杀/优惠券）").
			Set("contract_representation", "按字段实际类型声明 enum，并用 x-enum-labels 给出数值含义"),
		NewOMap().
			Set("id", "B1-02-D5").
			Set("summary", "幂等键仅支持 JSON body 字段，不支持 Idempotency-Key Header").
			Set("contract_representation", "operation 级 x-idempotency（mode=body/query/none）"),
		NewOMap().
			Set("id", "B1-01-H15.9").
			Set("summary", "10 个后台只读接口仅要求管理员身份，未绑定权限码").
			Set("affected", "GET /admin/me、GET /admin/qiniu/upload/token、GET /admin/products、GET /admin/products/:id、"+
				"GET /admin/inventories/:sku_id、GET /admin/inventories/:sku_id/logs、GET /admin/banners、"+
				"GET /admin/banners/:id、GET /admin/recommend-positions、GET /admin/recommend-positions/:id").
			Set("contract_representation", "operation 仅声明 x-auth: admin，不声明 x-permission"),
		NewOMap().
			Set("id", "B1-01-H15.8").
			Set("summary", "未匹配路由返回 404 text/plain \"Not Found\"，不经过统一 JSON 响应包装").
			Set("remediation", "需在服务层增加 404 兜底中间件；B1-02 不修改运行时行为"),
		NewOMap().
			Set("id", "B1-01-H15.3").
			Set("summary", "无 CORS 中间件，浏览器跨域直连会被拦截，必须走同源代理").
			Set("remediation", "B1-03 开发代理/Nginx 同源入口解决；不在 B1-02 修改"),
		NewOMap().
			Set("id", "B1-01-H15.10").
			Set("summary", "成功空操作 data 语义二义：部分接口返回 null，部分返回 {}").
			Set("contract_representation", "按 handler 实际返回逐条声明（data: null 或 $ref 空对象）"),
	}
}
