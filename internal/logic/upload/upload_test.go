package upload

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// 本文件覆盖上传业务纯逻辑（扩展名/MIME 校验、key 生成、结构配置校验、region 映射），
// 不依赖 MySQL/Redis 与真实七牛凭据。凭证签发端到端行为由 internal/cmd/upload_test.go 覆盖。

func TestValidateExtension(t *testing.T) {
	allowed := []string{"jpg", "jpeg", "png", "gif", "webp"}
	cases := []struct {
		name     string
		filename string
		wantExt  string
		wantErr  bool
	}{
		{"空文件名跳过", "", "", false},
		{"仅空白跳过", "   ", "", false},
		{"合法小写", "photo.jpg", "jpg", false},
		{"合法大写归一化", "PHOTO.PNG", "png", false},
		{"多段取最后一段", "a.tar.jpg", "jpg", false},
		{"不在白名单", "evil.exe", "", true},
		{"无扩展名", "readme", "", true},
		{"点结尾无扩展名", "file.", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ext, err := validateExtension(c.filename, allowed)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got ext=%q", ext)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ext != c.wantExt {
				t.Fatalf("ext=%q want %q", ext, c.wantExt)
			}
		})
	}
}

func TestValidateContentType(t *testing.T) {
	allowed := []string{"image/jpeg", "image/png", "image/gif", "image/webp"}
	cases := []struct {
		name        string
		contentType string
		wantErr     bool
	}{
		{"空跳过", "", false},
		{"仅空白跳过", "  ", false},
		{"合法", "image/jpeg", false},
		{"非法", "text/html", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateContentType(c.contentType, allowed)
			if c.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestGenerateKeyUniquenessAndFormat 覆盖 INV-004：key 符合 upload/{yyyyMMdd}/{随机hex}.{ext}，
// 且连续两次生成不同（key 唯一可控）。
func TestGenerateKeyUniquenessAndFormat(t *testing.T) {
	re := regexp.MustCompile(`^upload/\d{8}/[0-9a-f]{32}\.jpg$`)
	seen := map[string]bool{}
	for i := 0; i < 10; i++ {
		key, err := generateKey("jpg")
		if err != nil {
			t.Fatalf("generateKey: %v", err)
		}
		if !re.MatchString(key) {
			t.Fatalf("key %q 不符合 upload/{yyyyMMdd}/{hex32}.jpg 格式", key)
		}
		if seen[key] {
			t.Fatalf("key 重复: %q", key)
		}
		seen[key] = true
	}

	key, err := generateKey("")
	if err != nil {
		t.Fatalf("generateKey(empty): %v", err)
	}
	if !regexp.MustCompile(`^upload/\d{8}/[0-9a-f]{32}$`).MatchString(key) {
		t.Fatalf("无扩展名 key %q 格式错误", key)
	}
}

func TestValidateStructural(t *testing.T) {
	valid := qiniuConfig{
		region:            "z2",
		tokenTTL:          3600,
		maxFileSize:       10485760,
		allowedExtensions: []string{"jpg"},
		allowedMimeTypes:  []string{"image/jpeg"},
	}
	if err := valid.validateStructural(); err != nil {
		t.Fatalf("valid config should pass: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*qiniuConfig)
	}{
		{"region 非法", func(c *qiniuConfig) { c.region = "invalid-region" }},
		{"ttl 非正", func(c *qiniuConfig) { c.tokenTTL = 0 }},
		{"size 非正", func(c *qiniuConfig) { c.maxFileSize = -1 }},
		{"扩展名白名单为空", func(c *qiniuConfig) { c.allowedExtensions = nil }},
		{"MIME 白名单为空", func(c *qiniuConfig) { c.allowedMimeTypes = nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := valid
			c.mutate(&cfg)
			if err := cfg.validateStructural(); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestValidateCredentials(t *testing.T) {
	if err := (qiniuConfig{}).validateCredentials(); err == nil {
		t.Fatal("空凭据应返回错误")
	}
	base := qiniuConfig{accessKey: "ak", secretKey: "sk", bucket: "b", domain: "https://cdn.example.com"}
	for _, mutate := range []func(*qiniuConfig){
		func(c *qiniuConfig) { c.accessKey = "" },
		func(c *qiniuConfig) { c.secretKey = "" },
		func(c *qiniuConfig) { c.bucket = "" },
		func(c *qiniuConfig) { c.domain = "" },
	} {
		cfg := base
		mutate(&cfg)
		if err := cfg.validateCredentials(); err == nil {
			t.Fatal("缺少字段应返回错误")
		}
	}
	if err := base.validateCredentials(); err != nil {
		t.Fatalf("完整凭据应通过: %v", err)
	}
}

func TestUploadHost(t *testing.T) {
	host, ok := uploadHost("z2")
	if !ok || host != "https://up-z2.qiniup.com" {
		t.Fatalf("z2 -> %q (ok=%t) want https://up-z2.qiniup.com", host, ok)
	}
	if _, ok := uploadHost("invalid-region"); ok {
		t.Fatal("非法 region 应返回 false")
	}
}

// TestValidateConfigFromConfig 覆盖启动结构校验：非法结构配置经环境变量覆盖后应 fail-fast。
func TestValidateConfigFromConfig(t *testing.T) {
	s := New()
	t.Setenv("QINIU_REGION", "invalid-region")
	err := s.ValidateConfig(context.Background())
	if err == nil {
		t.Fatal("非法 region 应使 ValidateConfig 失败")
	}
	if !strings.Contains(err.Error(), "qiniu.region") {
		t.Fatalf("错误信息应指出 region，实际: %v", err)
	}
}

// TestValidateRequired 覆盖启动必填校验（INV-006）：AK/SK/bucket/domain 任一缺失即失败，
// 且错误信息指认缺失字段名、不泄漏凭据值。
func TestValidateRequired(t *testing.T) {
	if err := (qiniuConfig{}).validateRequired(); err == nil {
		t.Fatal("空配置应返回错误")
	}
	base := qiniuConfig{accessKey: "ak", secretKey: "sk", bucket: "b", domain: "https://cdn.example.com"}
	if err := base.validateRequired(); err != nil {
		t.Fatalf("完整配置应通过: %v", err)
	}
	cases := []struct {
		name      string
		mutate    func(*qiniuConfig)
		wantField string
	}{
		{"缺 access_key", func(c *qiniuConfig) { c.accessKey = "" }, "qiniu.access_key"},
		{"缺 secret_key", func(c *qiniuConfig) { c.secretKey = "" }, "qiniu.secret_key"},
		{"缺 bucket", func(c *qiniuConfig) { c.bucket = "" }, "qiniu.bucket"},
		{"缺 domain", func(c *qiniuConfig) { c.domain = "" }, "qiniu.domain"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := base
			c.mutate(&cfg)
			err := cfg.validateRequired()
			if err == nil {
				t.Fatal("缺失字段应返回错误")
			}
			if !strings.Contains(err.Error(), c.wantField) {
				t.Fatalf("错误信息应指认 %s，实际: %v", c.wantField, err)
			}
		})
	}
}

// TestValidateConfigAvailabilityWiring 覆盖启动真实可用性检查接线（INV-006）：注入假实现验证
// 「检查失败 → ValidateConfig 失败」「检查成功 + 配置齐全 → ValidateConfig 通过」。
// 真实网络验证（GetBucketInfo 对真实 bucket）属 E2E（AC-010），此处不发起网络请求。
func TestValidateConfigAvailabilityWiring(t *testing.T) {
	setValidQiniuEnv(t)

	ok := &sUpload{checkBucket: func(context.Context, qiniuConfig) error { return nil }}
	if err := ok.ValidateConfig(context.Background()); err != nil {
		t.Fatalf("配置齐全且可用性检查成功应通过: %v", err)
	}

	fail := &sUpload{checkBucket: func(context.Context, qiniuConfig) error {
		return fmt.Errorf("bucket 不存在")
	}}
	if err := fail.ValidateConfig(context.Background()); err == nil {
		t.Fatal("可用性检查失败应使 ValidateConfig 失败")
	}
}

// setValidQiniuEnv 为测试注入结构合法、required 齐全的七牛环境变量。
func setValidQiniuEnv(t *testing.T) {
	t.Helper()
	t.Setenv("QINIU_ACCESS_KEY", "ak")
	t.Setenv("QINIU_SECRET_KEY", "sk")
	t.Setenv("QINIU_BUCKET", "b")
	t.Setenv("QINIU_DOMAIN", "https://cdn.example.com")
}
