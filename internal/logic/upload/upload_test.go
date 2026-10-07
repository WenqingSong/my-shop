package upload

import (
	"context"
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
