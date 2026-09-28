package middleware

import (
	"strconv"
	"strings"

	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/os/glog"

	"cnb.cool/go-cloud-devops/my-shop/internal/auth"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

const bearerPrefix = "Bearer "

// Auth 解析 Authorization: Bearer <token>，校验签名与有效期后，再按 JWT 内的 sid
// 校验会话有效性（存在、未撤销且 user_id 与 sub 一致），成功后注入 Principal{UserID, Sid}。
// 缺失/非法/签名无效/过期/会话无效统一返回 401；Redis 查询失败 fail-closed 返回 401。
func Auth(r *ghttp.Request) {
	p, ok := authenticate(r)
	if !ok {
		return
	}

	valid, err := auth.ValidateSession(r.Context(), p.Sid, p.UserID)
	if err != nil {
		// Redis 不可用/查询失败：安全优先，fail-closed 拒绝放行，服务端记录底层错误。
		glog.Errorf(r.Context(), "会话校验失败（fail-closed）: %v", err)
		r.SetError(codes.New(codes.CodeUnauthorized))
		return
	}
	if !valid {
		r.SetError(codes.New(codes.CodeUnauthorized))
		return
	}

	r.SetCtxVar(principalKey, p)
	r.Middleware.Next()
}

// AuthSignatureOnly 仅校验签名与有效期（不查会话），注入 Principal{UserID, Sid}。
// 用于 /logout 这类需要幂等撤销的受保护接口，保证重复登出即使 session 已撤销/缺失也能到达 handler。
func AuthSignatureOnly(r *ghttp.Request) {
	p, ok := authenticate(r)
	if !ok {
		return
	}
	r.SetCtxVar(principalKey, p)
	r.Middleware.Next()
}

// authenticate 提取 Bearer token、验签 + 校验过期，解析 sub 与 sid，返回 Principal。
// 失败时已 SetError（401），返回 ok=false。
func authenticate(r *ghttp.Request) (*Principal, bool) {
	token, ok := bearerToken(r)
	if !ok {
		r.SetError(codes.New(codes.CodeUnauthorized))
		return nil, false
	}

	claims, err := auth.Parse(r.Context(), token)
	if err != nil {
		r.SetError(codes.New(codes.CodeUnauthorized))
		return nil, false
	}

	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || userID <= 0 {
		r.SetError(codes.New(codes.CodeUnauthorized))
		return nil, false
	}
	if claims.Sid == "" {
		// 有状态升级后，token 必须携带 sid，旧的无 sid token 不再放行。
		r.SetError(codes.New(codes.CodeUnauthorized))
		return nil, false
	}

	return &Principal{UserID: userID, Sid: claims.Sid}, true
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
