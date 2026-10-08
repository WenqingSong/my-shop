package cmd

// 本文件通过真实路由（RegisterFrontendRoutes + RegisterAdminRoutes）+ 真实 MySQL/Redis
// 锁定「文件上传 / 七牛云直传凭证签发」核心闭环，覆盖 AC-001/AC-003/AC-004/AC-005
// 与关键不变量 INV-001 至 INV-004。AC-002（真实七牛上传取得可访问 URL）需真实凭据，此处 NOT_VERIFIED。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gogf/gf/v2/frame/g"
)

// uploadGet 发起带 Authorization 与 query 的 GET 请求，解码统一响应。
func uploadGet(t *testing.T, base, path, token, query string) isoResult {
	t.Helper()
	url := base + path
	if query != "" {
		url += "?" + query
	}
	c := g.Client()
	if token != "" {
		c = c.Header(map[string]string{"Authorization": "Bearer " + token})
	}
	r, err := c.Get(context.Background(), url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer r.Close()
	return isoDecode(r)
}

// uploadData 是签发凭证响应 data 结构。
type uploadData struct {
	Token     string `json:"token"`
	Key       string `json:"key"`
	UploadURL string `json:"upload_url"`
	Domain    string `json:"domain"`
	FinalURL  string `json:"final_url"`
	ExpiresAt int64  `json:"expires_at"`
}

// uploadDecodeData 将统一响应中的 data 解码为 uploadData。
func uploadDecodeData(t *testing.T, data map[string]any) uploadData {
	t.Helper()
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal upload data: %v", err)
	}
	var out uploadData
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal upload data: %v", err)
	}
	return out
}

// decodePutPolicy 解码七牛 upload token 第三段的 put policy JSON，用于验证 scope/mimeLimit/fsizeLimit/deadline。
func decodePutPolicy(t *testing.T, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ":")
	if len(parts) != 3 {
		t.Fatalf("upload token 格式错误: %q", token)
	}
	raw, err := base64.URLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode put policy: %v", err)
	}
	var policy map[string]any
	if err := json.Unmarshal(raw, &policy); err != nil {
		t.Fatalf("unmarshal put policy: %v", err)
	}
	return policy
}

// TestUploadTokenAuthBoundary 覆盖 INV-001/AC-003：未认证 401、type 不符 403，均不签发凭证。
func TestUploadTokenAuthBoundary(t *testing.T) {
	base := setupIsolationServer(t)

	// 未认证 → 401/1002。
	res := uploadGet(t, base, "/qiniu/upload/token", "", "")
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("前台未认证: status=%d code=%d want 401/1002", res.Status, res.Code)
	}
	res = uploadGet(t, base, "/admin/qiniu/upload/token", "", "")
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("后台未认证: status=%d code=%d want 401/1002", res.Status, res.Code)
	}

	// 构造前台用户与后台管理员 token。
	isoInsertUser(t, "uploaduser", "uploadpass123")
	userToken, _ := isoFrontendLogin(t, base, "uploaduser", "uploadpass123")
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)

	// user token 访问后台端点 → 403/1003。
	res = uploadGet(t, base, "/admin/qiniu/upload/token", userToken, "")
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("user token 打后台: status=%d code=%d want 403/1003", res.Status, res.Code)
	}
	// admin token 访问前台端点 → 403/1003。
	res = uploadGet(t, base, "/qiniu/upload/token", adminToken, "")
	if res.Status != 403 || res.Code != 1003 {
		t.Fatalf("admin token 打前台: status=%d code=%d want 403/1003", res.Status, res.Code)
	}
}

// TestUploadTokenHappyPath 覆盖 AC-001 与 INV-004：签发成功返回最小必要信息，
// token scope 含 bucket:key、deadline、mimeLimit、fsizeLimit，且连续签发 key 不同。
func TestUploadTokenHappyPath(t *testing.T) {
	t.Setenv("QINIU_ACCESS_KEY", "test-access-key")
	t.Setenv("QINIU_SECRET_KEY", "test-secret-key")
	t.Setenv("QINIU_BUCKET", "test-bucket")
	t.Setenv("QINIU_DOMAIN", "https://cdn.example.com")
	// 隔离 region：本用例断言默认 z2 映射到 up-z2，避免运行时 .env 里的 QINIU_REGION=z1 干扰。
	t.Setenv("QINIU_REGION", "z2")

	base := setupIsolationServer(t)
	isoInsertUser(t, "uploaduser2", "uploadpass123")
	userToken, _ := isoFrontendLogin(t, base, "uploaduser2", "uploadpass123")

	res := uploadGet(t, base, "/qiniu/upload/token", userToken, "filename=photo.jpg&content_type=image/jpeg")
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("签发: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	d := uploadDecodeData(t, res.Data)
	if d.Token == "" || d.Key == "" || d.UploadURL == "" || d.Domain == "" || d.FinalURL == "" || d.ExpiresAt == 0 {
		t.Fatalf("响应缺字段: %+v", d)
	}
	if d.UploadURL != "https://up-z2.qiniup.com" {
		t.Fatalf("upload_url=%q want https://up-z2.qiniup.com", d.UploadURL)
	}
	if d.Domain != "https://cdn.example.com" {
		t.Fatalf("domain=%q", d.Domain)
	}
	if d.FinalURL != "https://cdn.example.com/"+d.Key {
		t.Fatalf("final_url=%q want %q", d.FinalURL, "https://cdn.example.com/"+d.Key)
	}
	// key 符合 upload/{yyyyMMdd}/{hex32}.jpg。
	if !regexp.MustCompile(`^upload/\d{8}/[0-9a-f]{32}\.jpg$`).MatchString(d.Key) {
		t.Fatalf("key=%q 格式错误", d.Key)
	}

	// token 内嵌 put policy 与响应一致。
	policy := decodePutPolicy(t, d.Token)
	if scope, _ := policy["scope"].(string); scope != "test-bucket:"+d.Key {
		t.Fatalf("scope=%q want %q", scope, "test-bucket:"+d.Key)
	}
	if ml, _ := policy["mimeLimit"].(string); ml != "image/jpeg;image/png;image/gif;image/webp" {
		t.Fatalf("mimeLimit=%q", ml)
	}
	if fl, _ := policy["fsizeLimit"].(float64); int64(fl) != 10485760 {
		t.Fatalf("fsizeLimit=%v want 10485760", fl)
	}
	if dl, _ := policy["deadline"].(float64); int64(dl) != d.ExpiresAt {
		// SDK 内部以签发时刻 time.Now() 计算 deadline，与响应 expires_at 允许 1 秒内偏差。
		if diff := int64(dl) - d.ExpiresAt; diff > 1 || diff < -1 {
			t.Fatalf("deadline=%v expires_at=%d 偏差过大", dl, d.ExpiresAt)
		}
	}
	// 过期时间约为 ttl(3600) 秒后。
	if d.ExpiresAt < time.Now().Unix()+3590 || d.ExpiresAt > time.Now().Unix()+3610 {
		t.Fatalf("expires_at=%d 不在预期窗口", d.ExpiresAt)
	}

	// INV-004：连续两次签发 key 不同。
	res2 := uploadGet(t, base, "/qiniu/upload/token", userToken, "filename=photo.jpg&content_type=image/jpeg")
	if res2.Status != 200 || res2.Code != 0 {
		t.Fatalf("二次签发: status=%d code=%d", res2.Status, res2.Code)
	}
	d2 := uploadDecodeData(t, res2.Data)
	if d2.Key == d.Key {
		t.Fatalf("连续签发 key 应不同: %q", d.Key)
	}

	// 后台端点同样可用（AC-001 后台主体）。
	adminToken, _ := isoAdminLogin(t, base, isoSuperUsername, isoAdminPassword)
	ares := uploadGet(t, base, "/admin/qiniu/upload/token", adminToken, "filename=avatar.png")
	if ares.Status != 200 || ares.Code != 0 {
		t.Fatalf("后台签发: status=%d code=%d", ares.Status, ares.Code)
	}
}

// TestUploadTokenInvalidInput 覆盖 INV-002/AC-004：非法扩展名/MIME → 400/17001，不签发凭证。
func TestUploadTokenInvalidInput(t *testing.T) {
	t.Setenv("QINIU_ACCESS_KEY", "test-access-key")
	t.Setenv("QINIU_SECRET_KEY", "test-secret-key")
	t.Setenv("QINIU_BUCKET", "test-bucket")
	t.Setenv("QINIU_DOMAIN", "https://cdn.example.com")

	base := setupIsolationServer(t)
	isoInsertUser(t, "uploaduser3", "uploadpass123")
	userToken, _ := isoFrontendLogin(t, base, "uploaduser3", "uploadpass123")

	cases := []struct {
		name  string
		query string
	}{
		{"非法扩展名", "filename=evil.exe"},
		{"非法 MIME", "content_type=text/html"},
		{"文件名无扩展名", "filename=readme"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := uploadGet(t, base, "/qiniu/upload/token", userToken, c.query)
			if res.Status != 400 || res.Code != 17001 {
				t.Fatalf("query=%q: status=%d code=%d want 400/17001", c.query, res.Status, res.Code)
			}
		})
	}
}

// TestUploadTokenMissingConfig 覆盖 INV-003/AC-005：无七牛凭据时签发返回 500/17002，
// 且响应不含凭据细节。
func TestUploadTokenMissingConfig(t *testing.T) {
	// 隔离真实 QINIU_* 环境变量（如本地 .env 注入），置空以确保本用例稳定验证
	// 「无凭据 → 17002」，不依赖运行时是否加载了 .env（否则 `make test` 会因读到真实凭据而误判）。
	t.Setenv("QINIU_ACCESS_KEY", "")
	t.Setenv("QINIU_SECRET_KEY", "")
	t.Setenv("QINIU_BUCKET", "")
	t.Setenv("QINIU_DOMAIN", "")

	base := setupIsolationServer(t)
	isoInsertUser(t, "uploaduser4", "uploadpass123")
	userToken, _ := isoFrontendLogin(t, base, "uploaduser4", "uploadpass123")

	res := uploadGet(t, base, "/qiniu/upload/token", userToken, "")
	if res.Status != 500 || res.Code != 17002 {
		t.Fatalf("缺凭据签发: status=%d code=%d want 500/17002", res.Status, res.Code)
	}
	// 响应 message 为码表安全文案，不含任何凭据。
	if strings.Contains(res.Message, "test-") || strings.Contains(res.Message, "secret") {
		t.Fatalf("响应泄漏凭据: %q", res.Message)
	}
}
