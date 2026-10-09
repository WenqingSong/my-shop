// Package v1 定义文件上传 / 对象存储（七牛云）V1 的公开 API 契约。
package v1

import (
	"github.com/gogf/gf/v2/frame/g"
)

// FrontendTokenReq 前台签发七牛云上传凭证请求（Auth：登录用户）。
// filename 与 content_type 均可选：filename 含扩展名，用于扩展名预校验与生成 key；
// content_type 为客户端声明 MIME，用于预校验。大小上限固化进 token 的 fsizeLimit 由七牛服务端执行。
type FrontendTokenReq struct {
	g.Meta      `path:"/qiniu/upload/token" method:"get" tags:"上传" summary:"前台签发七牛云上传凭证"`
	Filename    string `json:"filename" dc:"文件名（含扩展名，可选）"`
	ContentType string `json:"content_type" dc:"客户端声明 MIME 类型（可选）"`
}

// AdminTokenReq 后台签发七牛云上传凭证请求（AdminAuth：管理员）。
type AdminTokenReq struct {
	g.Meta      `path:"/admin/qiniu/upload/token" method:"get" tags:"上传" summary:"后台签发七牛云上传凭证"`
	Filename    string `json:"filename" dc:"文件名（含扩展名，可选）"`
	ContentType string `json:"content_type" dc:"客户端声明 MIME 类型（可选）"`
}

// TokenRes 上传凭证响应：客户端用 token 向 upload_url 直传文件，上传成功后访问 final_url。
type TokenRes struct {
	Token     string `json:"token" dc:"七牛云上传凭证（客户端直传用）"`
	Key       string `json:"key" dc:"后端预生成的存储 key（写入 token scope）"`
	UploadURL string `json:"upload_url" dc:"上传主机（按 region 映射）"`
	Domain    string `json:"domain" dc:"对外访问域名"`
	FinalURL  string `json:"final_url" dc:"上传成功后可直接访问的地址（domain/key）"`
	ExpiresAt int64  `json:"expires_at" dc:"凭证过期时间（unix 秒）"`
}
