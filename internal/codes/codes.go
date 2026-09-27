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
)

// codeInfo 绑定错误码 → HTTP 状态与用户安全 message。
type codeInfo struct {
	HTTPStatus int
	Message    string
}

var codeTable = map[Code]codeInfo{
	CodeOK:                 {http.StatusOK, "OK"},
	CodeInternalError:      {http.StatusInternalServerError, "内部错误"},
	CodeInvalidArgument:    {http.StatusBadRequest, "参数错误"},
	CodeUnauthorized:       {http.StatusUnauthorized, "未授权"},
	CodeForbidden:          {http.StatusForbidden, "禁止访问"},
	CodeNotFound:           {http.StatusNotFound, "未找到"},
	CodeServiceUnavailable: {http.StatusServiceUnavailable, "服务不可用"},
	CodeUsernameExists:     {http.StatusConflict, "用户名已存在"},
	CodeInvalidCredentials: {http.StatusUnauthorized, "用户名或密码错误"},
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
