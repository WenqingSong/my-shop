package middleware

import (
	"github.com/gogf/gf/v2/net/ghttp"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// Response 是项目级统一响应中间件，保持 {code,message,data} 格式。
// 成功时输出 {code:0,message:"OK",data:...}；失败时按业务错误码映射 HTTP 状态，
// 输出 {code,message,data:null}。客户端靠 code 判型，不依赖 message 文本。
func Response(r *ghttp.Request) {
	r.Middleware.Next()

	// 已有缓冲或已写入内容（如流式响应）则不再处理。
	if r.Response.BufferLength() > 0 || r.Response.BytesWritten() > 0 {
		return
	}

	res := r.GetHandlerResponse()
	err := r.GetError()
	if err == nil {
		r.Response.WriteJson(ResponseBody{
			Code:    codes.CodeOK,
			Message: codes.Message(codes.CodeOK),
			Data:    res,
		})
		return
	}

	code := codes.FromError(err)
	r.Response.WriteHeader(codes.HTTPStatus(code))
	r.Response.WriteJson(ResponseBody{
		Code:    code,
		Message: codes.Message(code),
		Data:    nil,
	})
}

// ResponseBody 是统一响应结构。
type ResponseBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}
