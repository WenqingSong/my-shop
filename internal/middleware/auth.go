package middleware

import (
	"strconv"
	"strings"

	"github.com/gogf/gf/v2/net/ghttp"

	"cnb.cool/go-cloud-devops/my-shop/internal/auth"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

const bearerPrefix = "Bearer "

// Auth 解析 Authorization: Bearer <token>，校验签名与有效期，
// 成功后注入 Principal{UserID}。缺失/非法/签名无效/过期统一返回 401 UNAUTHORIZED。
func Auth(r *ghttp.Request) {
	token, ok := bearerToken(r)
	if !ok {
		r.SetError(codes.New(codes.CodeUnauthorized))
		return
	}

	claims, err := auth.Parse(r.Context(), token)
	if err != nil {
		r.SetError(codes.New(codes.CodeUnauthorized))
		return
	}

	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || userID <= 0 {
		r.SetError(codes.New(codes.CodeUnauthorized))
		return
	}

	r.SetCtxVar(principalKey, &Principal{UserID: userID})
	r.Middleware.Next()
}

// bearerToken 从 Authorization 头提取 Bearer token，格式非法返回 false。
func bearerToken(r *ghttp.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, bearerPrefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(h, bearerPrefix))
	if token == "" {
		return "", false
	}
	return token, true
}
