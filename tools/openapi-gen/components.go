package main

// buildSuccessSchema 定义统一成功包装 Success<T>。
// data 的具体类型由各 operation 的 allOf 覆盖，避免为 142 条接口重复定义包装结构。
func buildSuccessSchema() *OMap {
	return NewOMap().
		Set("type", "object").
		Set("description", "统一成功响应包装：{code:0, message:\"OK\", data:<业务数据>}。"+
			"data 的具体 Schema 由各 operation 的 allOf 覆盖；data 为 null 时表示该操作无返回体。").
		Set("required", []any{"code", "message", "data"}).
		Set("properties", NewOMap().
			Set("code", NewOMap().
				Set("type", "integer").
				Set("const", 0).
				Set("description", "业务码，成功恒为 0")).
			Set("message", NewOMap().
				Set("type", "string").
				Set("const", "OK").
				Set("description", "成功文案，恒为 OK")).
			Set("data", NewOMap().
				Set("description", "业务数据；类型由具体 operation 指定，可为 null")))
}

// buildErrorSchema 定义统一失败包装 Error。
func buildErrorSchema() *OMap {
	return NewOMap().
		Set("type", "object").
		Set("description", "统一失败响应：{code:<业务错误码>, message:<码表文案>, data:null}。"+
			"客户端必须以 code 判定错误类型，不得依赖 message 文本。").
		Set("required", []any{"code", "message", "data"}).
		Set("properties", NewOMap().
			Set("code", NewOMap().
				Set("type", "integer").
				Set("minimum", 1).
				Set("description", "业务错误码；完整注册表见 components/error-codes.yaml")).
			Set("message", NewOMap().
				Set("type", "string").
				Set("description", "码表固定文案，不拼接字段细节")).
			Set("data", NewOMap().
				Set("type", "null").
				Set("description", "失败时恒为 null")))
}

func errorContent(schemaRef string) *OMap {
	return NewOMap().Set("application/json", NewOMap().
		Set("schema", NewOMap().Set("$ref", "schemas.yaml#/"+schemaRef)))
}

func buildResponsesComponent() *OMap {
	out := NewOMap()
	out.Set("SuccessResponse", NewOMap().
		Set("description", "通用成功响应（不含具体 data 类型；具体 operation 使用 allOf 扩展 data）").
		Set("content", errorContent(successSchema)))
	out.Set("ErrorResponse", NewOMap().
		Set("description", "通用失败响应。HTTP 状态由业务码派生，见 components/error-codes.yaml。").
		Set("content", errorContent(errorSchema)))

	out.Set("BadRequest", NewOMap().
		Set("description", "参数校验失败：HTTP 400 + code 1001（参数错误）。"+
			"注意：部分带 v:\"required\" 的接口实际返回 HTTP 500 + code 51，见 operation 的 x-known-issue。").
		Set("content", errorContent(errorSchema)))
	out.Set("Unauthorized", NewOMap().
		Set("description", "未授权：HTTP 401 + code 1002（未授权）。"+
			"包括 token 缺失/签名无效/过期/会话已撤销/refresh token 无效等。").
		Set("content", errorContent(errorSchema)))
	out.Set("Forbidden", NewOMap().
		Set("description", "禁止访问：HTTP 403 + code 1003（禁止访问），或业务域内的 403 码。"+
			"包括身份类型不符、权限码未命中、超级管理员受保护、不能操作自身。").
		Set("content", errorContent(errorSchema)))
	out.Set("NotFound", NewOMap().
		Set("description", "未找到：HTTP 404 + code 1004（未找到）或业务域内的 404 码（如 4001/9001/18001）。"+
			"防枚举接口对「不存在」与「不属于当前用户」返回同一码。").
		Set("content", errorContent(errorSchema)))
	out.Set("Conflict", NewOMap().
		Set("description", "业务冲突：HTTP 409 + 业务域错误码（如 9002 非法状态迁移、9006 幂等键冲突、6001 库存不足）。").
		Set("content", errorContent(errorSchema)))
	out.Set("TooManyRequests", NewOMap().
		Set("description", "限流或排队拒绝：HTTP 429 + code 12009/12010（无副作用，可稍后重试）。").
		Set("content", errorContent(errorSchema)))
	out.Set("InternalError", NewOMap().
		Set("description", "内部错误：HTTP 500 + code 1000（内部错误）。"+
			"已知例外：DTO gvalid 校验失败会以 code 51 + message \"内部错误\" 出现，见 x-known-issue。").
		Set("content", errorContent(errorSchema)))
	out.Set("ServiceUnavailable", NewOMap().
		Set("description", "服务不可用：HTTP 503 + code 1005。就绪探针失败时返回。").
		Set("content", errorContent(errorSchema)))
	return out
}

func intSchema() *OMap {
	return NewOMap().Set("type", "integer").Set("format", "int64")
}

func buildParametersComponent() *OMap {
	out := NewOMap()
	out.Set("PageStrictParam", NewOMap().
		Set("name", "page").
		Set("in", "query").
		Set("required", false).
		Set("description", "页码，默认 1；<1 或非整数返回 HTTP 400 + code 1001。").
		Set("schema", NewOMap().Set("type", "integer").Set("format", "int64").
			Set("minimum", 1).Set("default", 1)).
		Set("x-pagination-mode", "strict"))
	out.Set("SizeStrictParam", NewOMap().
		Set("name", "size").
		Set("in", "query").
		Set("required", false).
		Set("description", "每页数量，默认 20，上限 100；越界或非整数返回 HTTP 400 + code 1001。").
		Set("schema", NewOMap().Set("type", "integer").Set("format", "int64").
			Set("minimum", 1).Set("maximum", 100).Set("default", 20)).
		Set("x-pagination-mode", "strict"))
	out.Set("PageClampParam", NewOMap().
		Set("name", "page").
		Set("in", "query").
		Set("required", false).
		Set("description", "页码；<1 时静默归一为 1（历史行为，不返回 400）。").
		Set("schema", NewOMap().Set("type", "integer").Set("format", "int64").Set("default", 1)).
		Set("x-pagination-mode", "clamp"))
	out.Set("SizeClampParam", NewOMap().
		Set("name", "size").
		Set("in", "query").
		Set("required", false).
		Set("description", "每页数量；<1 归一为 20，>100 归一为 100（历史行为，不返回 400）。").
		Set("schema", NewOMap().Set("type", "integer").Set("format", "int64").Set("default", 20)).
		Set("x-pagination-mode", "clamp"))
	// 注意：不生成 Idempotency-Key Header 组件。
	// D5（Owner 2026-11-10 批准）已否决 Header 方案，幂等键只存在于 JSON body 的 idempotency_key，
	// 服务端不读取该 Header。历史上曾生成过 IdempotencyKeyHeader 草案组件（K-1），已在本任务移除；
	// 若未来重新引入 Header，必须先新增 ADR 决策并同步修改本函数与 DTO。
	return out
}

func buildSecurityComponent() *OMap {
	return NewOMap().Set("bearerAuth", NewOMap().
		Set("type", "http").
		Set("scheme", "bearer").
		Set("bearerFormat", "JWT").
		Set("description", "JWT（HS256，iss=surgecart）。通过 Authorization: Bearer <token> 传递，不支持 Cookie。"+
			"用户 token 的 type=user，管理员 token 的 type=admin，两者身份隔离："+
			"用户 token 不能访问管理端接口，管理员 token 不能访问用户端接口。"+
			"用户接口额外校验 Redis 会话、users.status 与 auth_epoch；管理员接口额外校验 Redis 会话与 admins.status。"+
			"仅验签登出接口（x-auth-mode=signature_only）不查询 Redis 会话。"))
}
