//go:build storage_e2e

package cmd

// 本文件是「独立真实存储 E2E」入口（AC-002/AC-014，D8/D9）：走真实 HTTP 链路
// 「注册一次性用户 → 登录 → 请求签发接口 → 用返回 token 经七牛 SDK 直传真实 1×1 PNG →
// 校验 final_url HTTP 200 且 Content-Type=image/png → 用返回 key 经七牛 SDK 删除测试对象」，
// 不直接调用 service.Upload().IssueToken。build tag 使本测试默认不进入 go test ./...，
// 仅由 `make test-storage`（go test -tags storage_e2e）驱动；缺真实凭据或任一环节失败 → t.Fatal 非零退出。

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	qauth "github.com/qiniu/go-sdk/v7/auth"
	qstorage "github.com/qiniu/go-sdk/v7/storage"
)

// tinyPNGBase64 是 1×1 透明 PNG（68 字节）的 base64 编码，作为真实最小合法图片内容。
// 不使用 1 字节伪造 Content-Type 冒充 image/png，确保七牛 mimeLimit 的内容侦测能通过。
const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="

// TestStorageE2E 覆盖 AC-002/AC-014 与 INV-009：真实 HTTP 链路 + 真实上传 + final_url 可访问 + 清理。
func TestStorageE2E(t *testing.T) {
	// 真实七牛凭据由 make test-storage 经 scripts/lib.sh 加载 .env 注入；缺失即快速失败（非零退出）。
	accessKey := os.Getenv("QINIU_ACCESS_KEY")
	secretKey := os.Getenv("QINIU_SECRET_KEY")
	bucket := os.Getenv("QINIU_BUCKET")
	region := os.Getenv("QINIU_REGION")
	if region == "" {
		region = "z2"
	}
	if accessKey == "" || secretKey == "" || bucket == "" {
		t.Fatal("缺少真实七牛凭据（QINIU_ACCESS_KEY/QINIU_SECRET_KEY/QINIU_BUCKET 任一为空）：请先执行 make init，在 .env 填入真实值后再执行 make test-storage")
	}

	base := setupIsolationServer(t)

	// 1. 注册一次性用户（真实 HTTP）。
	const (
		username = "e2estorage"
		password = "e2epass123"
	)
	reg := isoDo(t, base, "POST", "/register", map[string]any{"username": username, "password": password}, nil)
	if reg.Status != 200 || reg.Code != 0 {
		t.Fatalf("注册: status=%d code=%d msg=%q", reg.Status, reg.Code, reg.Message)
	}

	// 2. 登录取 access_token（真实 HTTP）。
	token, _ := isoFrontendLogin(t, base, username, password)

	// 3. 请求签发接口（真实 HTTP + Bearer）。
	res := uploadGet(t, base, "/qiniu/upload/token", token, "filename=e2e.png&content_type=image/png")
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("签发: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	d := uploadDecodeData(t, res.Data)
	if d.Token == "" || d.Key == "" || d.FinalURL == "" {
		t.Fatalf("签发响应缺字段: %+v", d)
	}

	// 4. 用返回 token 直传真实 1×1 PNG（不伪造内容类型）。
	png, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if err != nil {
		t.Fatalf("解码内嵌 PNG: %v", err)
	}
	r, ok := qstorage.GetRegionByID(qstorage.RegionID(region))
	if !ok {
		t.Fatalf("QINIU_REGION=%q 不是有效七牛区域", region)
	}
	mac := qauth.New(accessKey, secretKey)
	bm := qstorage.NewBucketManager(mac, &qstorage.Config{Region: &r, UseHTTPS: true})

	var putRet qstorage.PutRet
	if err := qstorage.NewFormUploader(&qstorage.Config{Region: &r, UseHTTPS: true}).Put(
		context.Background(),
		&putRet,
		d.Token,
		d.Key,
		bytes.NewReader(png),
		int64(len(png)),
		&qstorage.PutExtra{MimeType: "image/png"},
	); err != nil {
		t.Fatalf("直传真实 1×1 PNG 到七牛失败: %v", err)
	}

	// 直传成功后注册清理：即使后续 final_url 校验失败，也删除本次测试对象，避免残留（INV-009）。
	deleted := false
	t.Cleanup(func() {
		if deleted {
			return
		}
		if err := bm.Delete(bucket, d.Key); err != nil {
			t.Errorf("清理测试对象 %s 失败: %v", d.Key, err)
		}
	})

	// 5. 校验 final_url HTTP 200 且 Content-Type=image/png。
	httpClient := &http.Client{Timeout: 15 * time.Second}
	httpRes, err := httpClient.Get(d.FinalURL)
	if err != nil {
		t.Fatalf("GET final_url %s 失败: %v", d.FinalURL, err)
	}
	defer httpRes.Body.Close()
	if httpRes.StatusCode != http.StatusOK {
		t.Fatalf("final_url %s 状态=%d，期望 200", d.FinalURL, httpRes.StatusCode)
	}
	if ct := httpRes.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/png") {
		t.Fatalf("final_url Content-Type=%q，期望 image/png", ct)
	}

	// 6. 显式删除并校验（AC-014 删除步骤），成功后标记已删除避免清理阶段重复删除。
	if err := bm.Delete(bucket, d.Key); err != nil {
		t.Fatalf("删除测试对象 %s 失败: %v", d.Key, err)
	}
	deleted = true
}
