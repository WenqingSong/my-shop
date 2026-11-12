package main

import (
	"fmt"
	"sort"
	"strings"
)

const (
	successSchema = "Success"
	errorSchema   = "Error"
)

// pathFileFor 决定 operation 归入哪个分域视图文件。
func pathFileFor(api *API) string {
	if strings.HasPrefix(api.Path, "/admin") && (api.Domain == "admin" || api.Domain == "iam") {
		return "admin"
	}
	if api.Domain == "categories" {
		return "categories"
	}
	return api.Domain
}

// toOpenAPIPath 把 GoFrame 的 :param 语法转为 OpenAPI 的 {param} 语法。
func toOpenAPIPath(p string) string {
	parts := strings.Split(p, "/")
	for i, seg := range parts {
		if strings.HasPrefix(seg, ":") {
			parts[i] = "{" + strings.TrimPrefix(seg, ":") + "}"
		}
	}
	return strings.Join(parts, "/")
}

// pathParamNames 提取 GoFrame 路径中的参数名（按出现顺序）。
func pathParamNames(p string) []string {
	var out []string
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ":") && len(seg) > 1 {
			out = append(out, seg[1:])
		}
	}
	return out
}

// buildPathItem 构建单个 path 的 PathItem。
// 3.1 的 Path Item 支持 $ref，本生成器仍选择内联：单一入口自洽 + 门禁差异可定位，
// 详见 gen.go 中 writeAll 写分域视图时的说明。
func buildPathItem(apis []*API, codes map[string]*ErrorCode, graph *codeGraph, cms map[string]controllerMethod) (*OMap, error) {
	item := NewOMap()
	for _, api := range apis {
		op, err := buildOperation(api, codes, graph, cms)
		if err != nil {
			return nil, err
		}
		item.Set(strings.ToLower(api.Method), op)
	}
	return item, nil
}

// buildOperation 构建单个 operation。
func buildOperation(api *API, codes map[string]*ErrorCode, graph *codeGraph, cms map[string]controllerMethod) (*OMap, error) {
	meta := opMetaByKey[api.Key()]
	if meta == nil {
		meta = &opMeta{}
	}
	if desc, ok := logoutDescriptions[api.Key()]; ok {
		meta.Description = desc
	}

	op := NewOMap()
	op.Set("tags", []any{api.Tags})
	op.Set("summary", api.Summary)
	op.Set("description", buildDescription(api, meta))
	op.Set("operationId", api.OperationID)

	if api.Auth == "public" {
		op.Set("security", []any{})
	} else {
		op.Set("security", []any{
			NewOMap().Set("bearerAuth", []any{}),
		})
	}
	op.Set("x-auth", api.Auth)
	if api.AuthMode != "" {
		op.Set("x-auth-mode", api.AuthMode)
	}
	if api.Permission != "" {
		op.Set("x-permission", api.Permission)
	}
	op.Set("x-handler", api.Domain+"."+api.Handler)
	op.Set("x-source", api.Source)

	params, body, err := buildParams(api)
	if err != nil {
		return nil, err
	}
	if len(params) > 0 {
		op.Set("parameters", params)
	}
	if body != nil {
		op.Set("requestBody", body)
	}

	pagination := meta.Pagination
	if pagination == "" {
		pagination = "none"
	}
	op.Set("x-pagination-mode", pagination)
	if meta.Pagination == "clamp" || meta.Pagination == "strict" {
		op.Set("x-pagination-contract", paginationNote(meta.Pagination))
	}
	if meta.Idempotency != nil {
		op.Set("x-idempotency", meta.Idempotency)
	} else {
		op.Set("x-idempotency", NewOMap().Set("mode", "none"))
	}
	if len(meta.KnownIssues) > 0 {
		issues := make([]any, 0, len(meta.KnownIssues))
		for _, i := range meta.KnownIssues {
			issues = append(issues, i)
		}
		op.Set("x-known-issue", issues)
	}

	op.Set("responses", buildResponses(api, meta, codes, graph, cms))
	return op, nil
}

func paginationNote(mode string) string {
	if mode == "clamp" {
		return "历史行为：page<1 归一为 1；size<1 归一为 20；size>100 归一为 100；不返回 400。"
	}
	return "严格行为：page/size 非正整数或越界返回 HTTP 400 + code 1001。"
}

// buildDescription 生成 operation 的人类可读说明（认证 / 权限 / 特殊行为）。
func buildDescription(api *API, meta *opMeta) string {
	var b strings.Builder
	if api.AuthMode == "signature_only" {
		fmt.Fprintf(&b, "认证方式：仅校验 JWT 签名、有效期与 type=%s，不查询 Redis 会话；", api.Auth)
		b.WriteString("会话已撤销时仍可到达 handler（用于幂等登出）。")
	} else {
		switch api.Auth {
		case "public":
			b.WriteString("公开接口，无需认证。")
		case "user":
			b.WriteString("认证方式：用户 JWT。校验签名、有效期、type=user、Redis 会话有效、")
			b.WriteString("users.status=1 且 auth_epoch 匹配；任一失败返回 401/1002（身份类型不符为 403/1003）。")
		case "admin":
			b.WriteString("认证方式：管理员 JWT（AdminAuth）。校验签名、有效期、type=admin、Redis 管理员会话、")
			b.WriteString("admins.status=1；失败返回 401/1002（身份类型不符为 403/1003）。")
			if api.Permission != "" {
				fmt.Fprintf(&b, "权限码：`%s`；超级管理员（is_super=1）直接绕过该权限码校验，", api.Permission)
				b.WriteString("其余管理员按 roles→role_permissions→permissions 校验，未命中返回 403/1003。")
			} else {
				b.WriteString("该接口仅要求管理员身份，未绑定权限码（RBAC 只覆盖写侧粒度）。")
			}
		}
	}

	if meta.Description != "" {
		b.WriteString(" ")
		b.WriteString(meta.Description)
	}
	return b.String()
}

// buildParams 生成 path/query 参数与 requestBody。
func buildParams(api *API) ([]any, *OMap, error) {
	req := api.Req
	if req == nil {
		return nil, nil, fmt.Errorf("%s: 缺少请求 DTO", api.Key())
	}

	pathNames := pathParamNames(api.Path)
	pathFields := map[string]*Field{}
	var queryFields []*Field
	var bodyFields []*Field
	var pathParams []any

	for _, f := range req.Fields {
		if f.Embedded {
			continue
		}
		switch {
		case f.In == "path":
			if _, ok := pathFields[f.JSONName]; ok {
				return nil, nil, fmt.Errorf("%s: 路径参数 %q 重复", api.Key(), f.JSONName)
			}
			pathFields[f.JSONName] = f
		case f.In == "query":
			queryFields = append(queryFields, f)
		case api.Method == "GET" || api.Method == "DELETE" || api.Method == "HEAD":
			queryFields = append(queryFields, f)
		default:
			bodyFields = append(bodyFields, f)
		}
	}

	if len(pathFields) != len(pathNames) {
		return nil, nil, fmt.Errorf(
			"%s: 路径参数不匹配（path=%v, dto=%d 个 in:path）", api.Key(), pathNames, len(pathFields))
	}

	for _, name := range pathNames {
		f, ok := pathFields[name]
		if !ok {
			return nil, nil, fmt.Errorf("%s: 路径参数 %q 在请求 DTO 中缺少 in:\"path\" 字段", api.Key(), name)
		}
		schema := typeToSchema(f.Type, schemaMainRefPrefix)
		if f.Type.Pointer {
			// 路径参数必然存在，不声明 null。
			schema = typeToSchema(&TypeRef{Kind: f.Type.Kind, Scalar: f.Type.Scalar, Ref: f.Type.Ref, Elem: f.Type.Elem}, schemaMainRefPrefix)
		}
		p := NewOMap().
			Set("name", name).
			Set("in", "path").
			Set("required", true).
			Set("schema", schema)
		if f.Dc != "" {
			p.Set("description", f.Dc)
		}
		pathParams = append(pathParams, p)
	}

	params := pathParams
	for _, f := range queryFields {
		schema := typeToSchema(f.Type, schemaMainRefPrefix)
		if f.Type.Pointer {
			schema = typeToSchema(&TypeRef{Kind: f.Type.Kind, Scalar: f.Type.Scalar, Ref: f.Type.Ref, Elem: f.Type.Elem}, schemaMainRefPrefix)
		}
		if f.Default != "" {
			schema.Set("default", coerceDefault(f.Default, f.Type))
		}
		if e, ok := enumRegistry[req.SchemaName+"."+f.JSONName]; ok && len(e.Values) > 0 {
			values := make(FlowList, 0, len(e.Values))
			values = append(values, e.Values...)
			schema.Set("enum", values)
		}
		p := NewOMap().
			Set("name", f.JSONName).
			Set("in", "query").
			Set("required", f.Required()).
			Set("schema", schema)
		if f.Dc != "" {
			p.Set("description", f.Dc)
		}
		if api.PaginationOverride() != "" && (f.JSONName == "page" || f.JSONName == "size") {
			p.Set("x-pagination-mode", api.PaginationOverride())
		}
		params = append(params, p)
	}

	var body *OMap
	if len(bodyFields) > 0 {
		bodySchema := NewOMap().Set("type", "object")
		props := NewOMap()
		var required []any
		for _, f := range bodyFields {
			fs, err := buildFieldSchema(req, f, schemaMainRefPrefix)
			if err != nil {
				return nil, nil, fmt.Errorf("%s: 字段 %s: %w", api.Key(), f.JSONName, err)
			}
			props.Set(f.JSONName, fs)
			if f.Required() {
				required = append(required, f.JSONName)
			}
		}
		bodySchema.Set("properties", props)
		if len(required) > 0 {
			bodySchema.Set("required", required)
		}
		body = NewOMap().
			Set("required", true).
			Set("content", NewOMap().Set("application/json", NewOMap().Set("schema", bodySchema)))
	}
	return params, body, nil
}

// PaginationOverride 返回该 operation 声明的分页形态（用于参数级标注）。
func (a *API) PaginationOverride() string {
	if m := opMetaByKey[a.Key()]; m != nil {
		return m.Pagination
	}
	return ""
}

// buildResponses 构建 responses，并保证业务错误码与实际实现一致。
func buildResponses(api *API, meta *opMeta, codes map[string]*ErrorCode, graph *codeGraph, cms map[string]controllerMethod) *OMap {
	res := NewOMap()

	// 200 成功：统一 {code,message,data} 包装，data 为实际 DTO。
	dataSchema := any(nil)
	if meta.NullData {
		dataSchema = NewOMap().Set("type", "null")
	} else if api.Res != nil {
		dataSchema = NewOMap().Set("$ref", schemaMainRefPrefix+api.Res.SchemaName)
	}
	successAllOf := []any{
		NewOMap().Set("$ref", schemaMainRefPrefix+successSchema),
		NewOMap().
			Set("type", "object").
			Set("properties", NewOMap().Set("data", dataSchema)),
	}
	successDesc := "成功。响应体为 {code:0,message:\"OK\",data:<业务数据>}。"
	if meta.NullData {
		successDesc = "成功。handler 显式返回 nil，data 为 null（不是空对象 {}）。"
	}
	res.Set("200", NewOMap().
		Set("description", successDesc).
		Set("content", NewOMap().Set("application/json", NewOMap().Set("schema", NewOMap().Set("allOf", successAllOf)))))

	// 业务错误码集合。
	errCodes := errorCodesFor(api, codes, graph, cms)
	statusSet := map[int]bool{}
	for _, c := range errCodes {
		statusSet[c.HTTP] = true
	}
	statuses := make([]int, 0, len(statusSet))
	for s := range statusSet {
		if s == 200 {
			continue
		}
		statuses = append(statuses, s)
	}
	sort.Ints(statuses)

	codeValues := make([]any, 0, len(errCodes))
	for _, c := range errCodes {
		codeValues = append(codeValues, c.Code)
	}
	if len(codeValues) > 0 {
		apiMeta := NewOMap().
			Set("codes", codeValues).
			Set("scope", "static-call-chain").
			Set("note", "controller→service 与 logic 同包调用链中静态可达的业务错误码 + 经代码核实的跨包补充登记 + 通用码；"+
				"这是静态上界，不表示运行时必然触发，也不覆盖按数据内容动态分派的分支。码值/HTTP/message 见 components/error-codes.yaml。")
		res.Set("x-error-codes", apiMeta)
	}

	for _, status := range statuses {
		name := responseNameFor(status)
		if name == "" {
			continue
		}
		res.Set(fmt.Sprintf("%d", status), NewOMap().Set("$ref", "#/components/responses/"+name))
	}
	return res
}

func responseNameFor(status int) string {
	switch status {
	case 400:
		return "BadRequest"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 404:
		return "NotFound"
	case 409:
		return "Conflict"
	case 429:
		return "TooManyRequests"
	case 500:
		return "InternalError"
	case 503:
		return "ServiceUnavailable"
	}
	return ""
}

// errorCodesFor 计算 operation 可能返回的业务错误码：
// 通用码 + 该 operation 的 controller→service→logic 调用链中实际引用的码 + 认证相关码。
func errorCodesFor(api *API, codes map[string]*ErrorCode, graph *codeGraph, cms map[string]controllerMethod) []*ErrorCode {
	set := map[int]bool{}
	// 所有接口都可能因内部错误或参数错误失败。
	set[1000] = true
	if api.Domain != "health" {
		set[1001] = true
	}
	if api.Auth != "public" {
		set[1002] = true
		set[1003] = true
	}
	if api.Domain == "health" {
		set[1005] = true
	}

	cm := cms[api.Domain+"."+api.Handler]
	constants := graph.constantsFor(api.Domain, cm.ServiceCalls)
	for _, name := range cm.Codes {
		constants[name] = true
	}
	// 找不到函数级入口时退化为按域全量，并已被 x-error-code-scope 声明。
	if len(constants) == 0 {
		constants = graph.allConstants(api.Domain)
	}
	for name := range constants {
		c, ok := codes[name]
		if !ok || c.Code == 0 {
			continue
		}
		set[c.Code] = true
	}
	// 跨包补充登记：logic 内再调 service.<Other>() 时对方的业务错误码，
	// 同包调用图看不到，靠人工核实后逐条登记（errcodes_crosspkg.go）。
	for _, name := range supplementalErrorCodes(api.Key()) {
		if c, ok := codes[name]; ok && c.Code != 0 {
			set[c.Code] = true
		}
	}

	values := make([]int, 0, len(set))
	for v := range set {
		values = append(values, v)
	}
	sort.Ints(values)

	out := make([]*ErrorCode, 0, len(values))
	for _, v := range values {
		if c, ok := codesByValue[v]; ok {
			out = append(out, c)
		}
	}
	return out
}
