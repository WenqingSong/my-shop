package main

// opMeta 是 DTO 无法表达的 operation 级契约事实（全部来自 B1-01 的逐条核实）。
type opMeta struct {
	NullData    bool   // 成功时 data 为 null（handler 显式返回 nil）
	Pagination  string // clamp | strict | none
	Idempotency *OMap
	KnownIssues []*OMap
	Description string
}

// D1 缺陷的固定描述：13 条接口的实际行为。
func d1Issue() *OMap {
	return NewOMap().
		Set("id", "B1-02-D1").
		Set("summary", "DTO gvalid 校验失败实际返回 HTTP 500 + code 51（框架码穿透），不是 400 + 1001").
		Set("actual_http_status", 500).
		Set("actual_business_code", 51).
		Set("remediation", "需单独立项在 codes.FromError / Response 中间件中把 gvalid 归一为 1001；B1-02 不修改错误码映射")
}

func idemBody(field string, required bool) *OMap {
	return NewOMap().
		Set("mode", "body").
		Set("field", field).
		Set("required", required).
		Set("header_supported", false)
}

// opMetaByKey 以 "METHOD /path" 为键登记 operation 级事实。
// 未登记的接口使用默认值：Pagination=none、NullData=false、无幂等。
// 同一接口的多项事实必须写在同一条记录里（Go map 字面量不允许重复键）。
var opMetaByKey = map[string]*opMeta{
	// ---- D1：gvalid 500/51 实际行为 ----
	// B1-01 §15.1 与 D1.2 表列为 13 条；B1-02-review-fix R1 复核全部 DTO 的 v:"required"
	// 字段位置后确认第 14 条 POST /admin/roles/:id/permissions（body.permission_id 必填）
	// 同样会在绑定阶段触发 gvalid → 51 → 500，契约按实际行为补标（不改生产代码）。
	"POST /admin/login":                 {KnownIssues: []*OMap{d1Issue()}},
	"POST /admin/admins":                {KnownIssues: []*OMap{d1Issue()}},
	"PUT /admin/admins/:id/status":      {KnownIssues: []*OMap{d1Issue()}},
	"POST /admin/admins/:id/roles":      {KnownIssues: []*OMap{d1Issue()}, NullData: true},
	"POST /admin/roles":                 {KnownIssues: []*OMap{d1Issue()}},
	"POST /admin/permissions":           {KnownIssues: []*OMap{d1Issue()}},
	"PUT /admin/users/:id/status":       {KnownIssues: []*OMap{d1Issue()}},
	"POST /categories":                  {KnownIssues: []*OMap{d1Issue()}},
	"POST /admin/skus":                  {KnownIssues: []*OMap{d1Issue()}},
	"POST /admin/refunds/:id/review":    {KnownIssues: []*OMap{d1Issue()}},
	"POST /admin/refunds/:id/execute":   {KnownIssues: []*OMap{d1Issue()}},
	"POST /admin/refunds/:id/close":     {KnownIssues: []*OMap{d1Issue()}},
	"POST /admin/roles/:id/permissions": {KnownIssues: []*OMap{d1Issue()}, NullData: true},

	// ---- data 为 null 的成功响应（controller 显式 return nil, nil）----
	"POST /logout":                                       {NullData: true},
	"DELETE /sessions/:sid":                              {NullData: true},
	"POST /sessions/revoke-others":                       {NullData: true},
	"POST /sessions/revoke-all":                          {NullData: true},
	"POST /admin/logout":                                 {NullData: true},
	"DELETE /admin/admins/:id":                           {NullData: true},
	"DELETE /admin/admins/:id/roles/:role_id":            {NullData: true},
	"DELETE /admin/roles/:id":                            {NullData: true},
	"DELETE /admin/roles/:id/permissions/:permission_id": {NullData: true},
	"DELETE /admin/permissions/:id":                      {NullData: true},

	// ---- 分页：形态 A（静默归一）----
	"GET /products":                       {Pagination: "clamp"},
	"GET /admin/products":                 {Pagination: "clamp"},
	"GET /articles":                       {Pagination: "clamp"},
	"GET /my/articles":                    {Pagination: "clamp"},
	"GET /my/articles/favorites":          {Pagination: "clamp"},
	"GET /products/:id/reviews":           {Pagination: "clamp"},
	"GET /my/reviews":                     {Pagination: "clamp"},
	"GET /favorites":                      {Pagination: "clamp"},
	"GET /admin/inventories/:sku_id/logs": {Pagination: "clamp"},

	// ---- 分页：形态 B（严格校验）----
	"GET /admin/orders":  {Pagination: "strict"},
	"GET /admin/refunds": {Pagination: "strict"},

	// ---- 幂等：body 字段（现状，未支持 Header）----
	"POST /orders": {
		KnownIssues: []*OMap{d1Issue()},
		Idempotency: idemBody("idempotency_key", true),
	},
	"POST /flash-sales/:id/orders": {
		Idempotency: idemBody("idempotency_key", false),
	},
	"GET /flash-sales/:id/orders/result": {
		Idempotency: NewOMap().
			Set("mode", "query").
			Set("field", "idempotency_key").
			Set("required", false).
			Set("header_supported", false),
	},

	// ---- 需要显式声明的行为事实 ----
	"GET /products/:id": {
		KnownIssues: []*OMap{
			NewOMap().
				Set("id", "B1-01-H15.8").
				Set("summary", "读接口在服务端执行 products.view_count + 1 写库，破坏幂等与缓存语义"),
		},
		Description: "前台商品详情（仅 on_shelf 可见）。注意：该 GET 请求会触发 view_count 自增写库。",
	},
}

// logoutDescriptions 单独维护，避免与 NullData 记录重复键。
var logoutDescriptions = map[string]string{
	"POST /logout": "仅验签登出：JWT 签名与 type 合法即可到达 handler，不查询 Redis 会话，" +
		"因此会话已撤销时重复登出仍返回成功（幂等）。",
	"POST /admin/logout": "仅验签登出（管理员）：不查询 Redis 会话，重复登出幂等成功。",
}

// plannedAPI 是 M1～M3 的契约占位（后端尚未实现，禁止当作可用接口）。
type plannedAPI struct {
	Method      string
	Path        string
	OperationID string
	Tag         string
	Summary     string
	Description string
	Missing     string
	Query       []*OMap
}

// plannedAPIs 来自 B1-01 §14.1 的 M1～M3。
// 路径为 proposed，不代表现有后端路由；不写入 paths 的实际 API 统计。
var plannedAPIs = []plannedAPI{
	{
		Method:      "GET",
		Path:        "/flash-sales",
		OperationID: "flashSaleListPublic",
		Tag:         "秒杀",
		Summary:     "秒杀活动公开列表（规划中，尚未实现）",
		Missing:     "M1",
		Description: "B1-01 §14.1 M1：前台缺少秒杀活动列表，用户只能从外部渠道获得 activity_id。" +
			"本条目仅为契约占位，后端无对应路由与 service 能力。路径为 proposed，未在 routes_frontend.go 注册。",
		Query: []*OMap{
			proposedQuery("page", "integer", "页码，默认 1（目标规范：严格校验，越界 400）"),
			proposedQuery("size", "integer", "每页数量，默认 20，最大 100（目标规范：越界 400）"),
			proposedQuery("status", "string", "活动状态过滤，目标取值 enabled/disabled"),
		},
	},
	{
		Method:      "GET",
		Path:        "/flash-sales/{id}",
		OperationID: "flashSaleDetailPublic",
		Tag:         "秒杀",
		Summary:     "秒杀活动公开详情（规划中，尚未实现）",
		Missing:     "M1",
		Description: "B1-01 §14.1 M1：前台缺少秒杀活动详情（含 SKU 绑定与库存）。" +
			"本条目仅为契约占位，后端无对应路由；路径为 proposed。",
	},
	{
		Method:      "GET",
		Path:        "/coupons",
		OperationID: "couponListClaimable",
		Tag:         "优惠券",
		Summary:     "可领取优惠券列表（规划中，尚未实现）",
		Missing:     "M2",
		Description: "B1-01 §14.1 M2：前台无法浏览可领取优惠券模板，导致 POST /coupons/:template_id/claim 不可发现。" +
			"本条目仅为契约占位，后端 ICoupon 仅提供 AdminList/ListMine；路径为 proposed。",
		Query: []*OMap{
			proposedQuery("page", "integer", "页码，默认 1（目标规范：严格校验）"),
			proposedQuery("size", "integer", "每页数量，默认 20，最大 100"),
		},
	},
	{
		Method:      "GET",
		Path:        "/orders/{id}/coupons",
		OperationID: "orderAvailableCoupons",
		Tag:         "订单",
		Summary:     "订单可用优惠券查询（规划中，尚未实现）",
		Missing:     "M3",
		Description: "B1-01 §14.1 M3：下单页无法计算/选择可用券，coupon_id 无法正确填写。" +
			"本条目仅为契约占位，后端无对应路由；路径为 proposed。",
	},
}

func proposedQuery(name, typ, desc string) *OMap {
	return NewOMap().
		Set("name", name).
		Set("in", "query").
		Set("required", false).
		Set("schema", NewOMap().Set("type", typ)).
		Set("description", desc).
		Set("x-proposed", true)
}
