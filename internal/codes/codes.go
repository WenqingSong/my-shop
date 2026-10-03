// Package codes 集中定义项目级业务错误码、HTTP 状态与用户安全 message。
// 客户端依靠 code 判断错误类型，不依赖 message 文本。
package codes

import (
	"net/http"

	"github.com/gogf/gf/v2/errors/gcode"
	"github.com/gogf/gf/v2/errors/gerror"
)

// Code 是项目统一的业务错误码（int）。
type Code = int

const (
	CodeOK Code = 0 // 成功

	// 通用/系统域 1000-1999，与 HTTP 语义对齐，全项目复用。
	CodeInternalError      Code = 1000 // 内部错误 → 500
	CodeInvalidArgument    Code = 1001 // 参数校验失败 → 400
	CodeUnauthorized       Code = 1002 // 未授权 → 401
	CodeForbidden          Code = 1003 // 禁止访问 → 403
	CodeNotFound           Code = 1004 // 未找到 → 404
	CodeServiceUnavailable Code = 1005 // 服务不可用 → 503

	// 认证/用户域（IAM）2000-2999。
	CodeUsernameExists     Code = 2001 // 用户名已存在 → 409
	CodeInvalidCredentials Code = 2002 // 用户名或密码错误 → 401
	CodeSessionNotFound    Code = 2011 // 会话不存在或不属于当前用户 → 404

	// refresh token（IAM 段扩展，仅前台用户域）。
	CodeRefreshTokenInvalid Code = 2012 // refresh token 无效/未知/篡改/已撤销（不泄露存在性）→ 401
	CodeRefreshTokenExpired Code = 2013 // refresh token 已过期 → 401
	CodeRefreshTokenReuse   Code = 2014 // refresh token 被重放（已轮换 token 再次提交）→ 401

	// 后台管理员身份与 RBAC（IAM 段扩展）。
	CodeAdminNotFound          Code = 2003 // 管理员不存在 → 404
	CodeAdminUsernameExists    Code = 2004 // 管理员用户名已存在 → 409
	CodeSuperAdminProtected    Code = 2005 // 超级管理员受保护 → 403
	CodeSelfOperationForbidden Code = 2006 // 不能操作自身 → 403
	CodeRoleNotFound           Code = 2007 // 角色不存在 → 404
	CodeRoleNameExists         Code = 2008 // 角色名已存在 → 409
	CodePermissionNotFound     Code = 2009 // 权限不存在 → 404
	CodePermissionCodeExists   Code = 2010 // 权限 code 已存在 → 409

	// 商品分类域（Category）3000-3999。
	CodeCategoryNotFound      Code = 3001 // 分类不存在 → 404
	CodeCategoryNameExists    Code = 3002 // 同级重名 → 409
	CodeCategoryHasChildren   Code = 3003 // 有子分类不能删除 → 409
	CodeCategoryInvalidParent Code = 3004 // 父分类非法 → 400
	CodeCategoryHasProducts   Code = 3005 // 分类下存在商品不能删除 → 409

	// 商品域（Product）4000-4999。
	CodeProductNotFound                Code = 4001 // 商品不存在 → 404
	CodeProductInvalidPrice            Code = 4002 // 价格非法（负数/非整数/超上限）→ 400
	CodeProductInvalidCategory         Code = 4003 // 分类不存在或非法 → 400
	CodeProductCategoryNotLeaf         Code = 4004 // 分类非叶子分类 → 400
	CodeProductInvalidStatusTransition Code = 4005 // 状态迁移非法/并发冲突 → 409
	CodeProductInvalidStatus           Code = 4006 // 未知 status 值 → 400
	CodeProductCategoryDisabled        Code = 4007 // 分类已禁用 → 400

	// SKU 域（Sku）5000-5999。
	CodeSkuNotFound      Code = 5001 // SKU 不存在 → 404
	CodeSkuInvalidPrice  Code = 5002 // 价格非法（负数/非整数/超上限）→ 400
	CodeSkuInvalidStatus Code = 5003 // 未知 status 值 → 400
	CodeSkuNameExists    Code = 5004 // 同商品下 name 已存在 → 409
	CodeSkuHasInventory  Code = 5005 // SKU 存在库存记录，不能删除 → 409

	// 库存域（Inventory）6000-6999。
	CodeInventoryInsufficient    Code = 6001 // 库存不足 → 409
	CodeInventoryInvalidQuantity Code = 6002 // 库存数量非正整数 → 400

	// 收货地址域（Address）7000-7999。
	CodeAddressNotFound        Code = 7001 // 地址不存在或非本人（统一不泄露）→ 404
	CodeAddressDefaultConflict Code = 7002 // 并发设置默认地址唯一冲突 → 409

	// 购物车域（Cart）8000-8999。
	CodeCartItemNotFound    Code = 8001 // 条目不存在或不属于当前用户 → 404
	CodeCartInvalidQuantity Code = 8002 // 数量非正整数或超上限 → 400
	CodeCartSkuUnavailable  Code = 8003 // 商品下架或 SKU 禁用，不可加购 → 409
)

// codeInfo 绑定错误码 → HTTP 状态与用户安全 message。
type codeInfo struct {
	HTTPStatus int
	Message    string
}

var codeTable = map[Code]codeInfo{
	CodeOK:                     {http.StatusOK, "OK"},
	CodeInternalError:          {http.StatusInternalServerError, "内部错误"},
	CodeInvalidArgument:        {http.StatusBadRequest, "参数错误"},
	CodeUnauthorized:           {http.StatusUnauthorized, "未授权"},
	CodeForbidden:              {http.StatusForbidden, "禁止访问"},
	CodeNotFound:               {http.StatusNotFound, "未找到"},
	CodeServiceUnavailable:     {http.StatusServiceUnavailable, "服务不可用"},
	CodeUsernameExists:         {http.StatusConflict, "用户名已存在"},
	CodeInvalidCredentials:     {http.StatusUnauthorized, "用户名或密码错误"},
	CodeSessionNotFound:        {http.StatusNotFound, "会话不存在"},
	CodeRefreshTokenInvalid:    {http.StatusUnauthorized, "refresh token 无效"},
	CodeRefreshTokenExpired:    {http.StatusUnauthorized, "refresh token 已过期"},
	CodeRefreshTokenReuse:      {http.StatusUnauthorized, "refresh token 已失效，请重新登录"},
	CodeAdminNotFound:          {http.StatusNotFound, "管理员不存在"},
	CodeAdminUsernameExists:    {http.StatusConflict, "管理员用户名已存在"},
	CodeSuperAdminProtected:    {http.StatusForbidden, "超级管理员受保护"},
	CodeSelfOperationForbidden: {http.StatusForbidden, "不能操作自身"},
	CodeRoleNotFound:           {http.StatusNotFound, "角色不存在"},
	CodeRoleNameExists:         {http.StatusConflict, "角色名已存在"},
	CodePermissionNotFound:     {http.StatusNotFound, "权限不存在"},
	CodePermissionCodeExists:   {http.StatusConflict, "权限 code 已存在"},
	CodeCategoryNotFound:       {http.StatusNotFound, "分类不存在"},
	CodeCategoryNameExists:     {http.StatusConflict, "同级重名"},
	CodeCategoryHasChildren:    {http.StatusConflict, "有子分类不能删除"},
	CodeCategoryInvalidParent:  {http.StatusBadRequest, "父分类非法"},
	CodeCategoryHasProducts:    {http.StatusConflict, "分类下存在商品，不能删除"},

	CodeProductNotFound:                {http.StatusNotFound, "商品不存在"},
	CodeProductInvalidPrice:            {http.StatusBadRequest, "价格非法"},
	CodeProductInvalidCategory:         {http.StatusBadRequest, "分类非法"},
	CodeProductCategoryNotLeaf:         {http.StatusBadRequest, "分类不是叶子分类"},
	CodeProductInvalidStatusTransition: {http.StatusConflict, "商品状态迁移非法"},
	CodeProductInvalidStatus:           {http.StatusBadRequest, "商品状态非法"},
	CodeProductCategoryDisabled:        {http.StatusBadRequest, "分类已禁用"},

	CodeSkuNotFound:      {http.StatusNotFound, "SKU 不存在"},
	CodeSkuInvalidPrice:  {http.StatusBadRequest, "价格非法"},
	CodeSkuInvalidStatus: {http.StatusBadRequest, "SKU 状态非法"},
	CodeSkuNameExists:    {http.StatusConflict, "SKU 名称已存在"},
	CodeSkuHasInventory:  {http.StatusConflict, "SKU 存在库存记录，不能删除"},

	CodeInventoryInsufficient:    {http.StatusConflict, "库存不足"},
	CodeInventoryInvalidQuantity: {http.StatusBadRequest, "库存数量非法"},

	CodeAddressNotFound:        {http.StatusNotFound, "地址不存在"},
	CodeAddressDefaultConflict: {http.StatusConflict, "默认地址冲突"},

	CodeCartItemNotFound:    {http.StatusNotFound, "购物车条目不存在"},
	CodeCartInvalidQuantity: {http.StatusBadRequest, "数量非法"},
	CodeCartSkuUnavailable:  {http.StatusConflict, "商品不可加购"},
}

// HTTPStatus 返回业务错误码对应的 HTTP 状态码；未知码按 500 处理。
func HTTPStatus(code Code) int {
	if info, ok := codeTable[code]; ok {
		return info.HTTPStatus
	}
	return http.StatusInternalServerError
}

// Message 返回业务错误码对应的用户安全 message。
func Message(code Code) string {
	if info, ok := codeTable[code]; ok {
		return info.Message
	}
	return "内部错误"
}

// New 创建携带业务错误码的 error，对外 message 使用码表内安全文案。
func New(code Code) error {
	return gerror.NewCode(gcode.New(code, Message(code), nil))
}

// Wrap 用业务错误码包裹底层错误，保留原始错误链用于日志定位。
// 对外响应仅使用码表安全 message，不泄漏底层技术细节。
func Wrap(code Code, err error) error {
	return gerror.WrapCode(gcode.New(code, Message(code), nil), err)
}

// FromError 从 error 链中提取业务错误码；未识别时返回内部错误码。
func FromError(err error) Code {
	if err == nil {
		return CodeOK
	}
	c := gerror.Code(err)
	if c == gcode.CodeNil {
		return CodeInternalError
	}
	return Code(c.Code())
}
