// Package upload 实现「文件上传 / 对象存储（七牛云）」业务逻辑：后端签发七牛云直传凭证
// （upload token），文件本体由客户端直传七牛，后端不承载上传流量、不感知上传最终结果。
// V1 为无状态签发、不落库；七牛云凭据经配置安全注入，不硬编码、不进日志/响应。
package upload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/gogf/gf/v2/frame/g"

	qauth "github.com/qiniu/go-sdk/v7/auth"
	qstorage "github.com/qiniu/go-sdk/v7/storage"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/upload/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// 结构配置默认值（与 manifest/config/config.yaml 保持一致）。
	defaultRegion      = "z2"
	defaultTokenTTL    = 3600
	defaultMaxFileSize = 10 * 1024 * 1024 // 10MB

	// keyRandomBytes 是存储 key 随机段的字节数（hex 编码后长度翻倍）。
	keyRandomBytes = 16
)

// 白名单默认值（与 config.yaml 一致）。测试环境不加载 config.yaml，靠代码默认值兜底。
var (
	defaultAllowedExtensions = []string{"jpg", "jpeg", "png", "gif", "webp"}
	defaultAllowedMimeTypes  = []string{"image/jpeg", "image/png", "image/gif", "image/webp"}
)

type sUpload struct{}

func init() {
	service.RegisterUpload(New())
}

// New 创建并返回文件上传服务实现。
func New() *sUpload {
	return &sUpload{}
}

// qiniuConfig 是 qiniu 段配置的运行时快照。
// access_key/secret_key/bucket/domain 无默认值，缺失由 validateCredentials 在签发时拒绝；
// region/token_ttl/max_file_size 与白名单有安全默认值，格式非法由 validateStructural 在启动时拒绝。
type qiniuConfig struct {
	accessKey         string
	secretKey         string
	bucket            string
	domain            string
	region            string
	tokenTTL          int64
	maxFileSize       int64
	allowedExtensions []string
	allowedMimeTypes  []string
}

// ValidateConfig 校验七牛云非机密结构配置（region/ttl/大小/白名单），供启动时 fail-fast。
// 凭据（AK/SK）与 bucket/domain 属运行时懒校验，缺失不阻塞启动（见 IssueToken）。
func (s *sUpload) ValidateConfig(ctx context.Context) error {
	return loadConfig(ctx).validateStructural()
}

// IssueToken 校验请求声明（扩展名/MIME）并签发七牛云直传凭证。
// 凭据或 bucket/domain 缺失/非法时返回稳定错误码 17002，不泄漏任何凭据细节。
func (s *sUpload) IssueToken(ctx context.Context, filename, contentType string) (*v1.TokenRes, error) {
	cfg := loadConfig(ctx)
	if err := cfg.validateCredentials(); err != nil {
		return nil, err
	}

	ext, err := validateExtension(filename, cfg.allowedExtensions)
	if err != nil {
		return nil, err
	}
	if err := validateContentType(contentType, cfg.allowedMimeTypes); err != nil {
		return nil, err
	}

	key, err := generateKey(ext)
	if err != nil {
		return nil, codes.Wrap(codes.CodeUploadTokenFailed, fmt.Errorf("生成存储 key: %w", err))
	}
	uploadURL, ok := uploadHost(cfg.region)
	if !ok {
		return nil, codes.Wrap(codes.CodeUploadConfigInvalid, fmt.Errorf("region %q 无效", cfg.region))
	}

	now := time.Now()
	expiresAt := now.Unix() + cfg.tokenTTL
	// 凭据只用于签名，绝不进入日志、响应或错误信息。
	policy := qstorage.PutPolicy{
		Scope:      cfg.bucket + ":" + key,
		Expires:    uint64(cfg.tokenTTL), // 相对时长，SDK 会加当前时间得到 deadline
		MimeLimit:  strings.Join(cfg.allowedMimeTypes, ";"),
		FsizeLimit: cfg.maxFileSize,
	}
	token := policy.UploadToken(qauth.New(cfg.accessKey, cfg.secretKey))

	domain := strings.TrimRight(cfg.domain, "/")
	return &v1.TokenRes{
		Token:     token,
		Key:       key,
		UploadURL: uploadURL,
		Domain:    cfg.domain,
		FinalURL:  domain + "/" + key,
		ExpiresAt: expiresAt,
	}, nil
}

// loadConfig 读取 qiniu 段配置。配置读取失败时按安全默认值/空值处理，后续校验负责拒绝。
func loadConfig(ctx context.Context) qiniuConfig {
	return qiniuConfig{
		accessKey:         cfgString(ctx, "qiniu.access_key", ""),
		secretKey:         cfgString(ctx, "qiniu.secret_key", ""),
		bucket:            cfgString(ctx, "qiniu.bucket", ""),
		domain:            cfgString(ctx, "qiniu.domain", ""),
		region:            cfgString(ctx, "qiniu.region", defaultRegion),
		tokenTTL:          cfgInt64(ctx, "qiniu.token_ttl", defaultTokenTTL),
		maxFileSize:       cfgInt64(ctx, "qiniu.max_file_size", defaultMaxFileSize),
		allowedExtensions: cfgStrings(ctx, "qiniu.allowed_extensions", defaultAllowedExtensions),
		allowedMimeTypes:  cfgStrings(ctx, "qiniu.allowed_mime_types", defaultAllowedMimeTypes),
	}
}

// validateStructural 校验非机密结构配置的格式，供启动 fail-fast。
// bucket/domain 与凭据不在此校验（属运行时懒校验，见 validateCredentials）。
func (c qiniuConfig) validateStructural() error {
	if _, ok := qstorage.GetRegionByID(qstorage.RegionID(c.region)); !ok {
		return fmt.Errorf("qiniu.region %q 不是有效的七牛区域", c.region)
	}
	if c.tokenTTL <= 0 {
		return fmt.Errorf("qiniu.token_ttl 必须大于 0，当前 %d", c.tokenTTL)
	}
	if c.maxFileSize <= 0 {
		return fmt.Errorf("qiniu.max_file_size 必须大于 0，当前 %d", c.maxFileSize)
	}
	if len(c.allowedExtensions) == 0 {
		return fmt.Errorf("qiniu.allowed_extensions 不能为空")
	}
	if len(c.allowedMimeTypes) == 0 {
		return fmt.Errorf("qiniu.allowed_mime_types 不能为空")
	}
	return nil
}

// validateCredentials 校验签发所需的凭据与桶/域名是否齐全；缺失返回稳定 17002（不泄漏细节）。
func (c qiniuConfig) validateCredentials() error {
	if c.accessKey == "" || c.secretKey == "" || c.bucket == "" || c.domain == "" {
		return codes.New(codes.CodeUploadConfigInvalid)
	}
	return nil
}

// validateExtension 校验 filename 声明的扩展名是否在白名单内，返回规范化小写扩展名（无扩展名返回空串）。
// filename 为空表示未声明，跳过校验；声明了但无扩展名或不在白名单内均拒绝（17001）。
func validateExtension(filename string, allowed []string) (string, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		return "", nil
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	if ext == "" {
		return "", codes.New(codes.CodeUploadInvalidInput)
	}
	if !contains(allowed, ext) {
		return "", codes.New(codes.CodeUploadInvalidInput)
	}
	return ext, nil
}

// validateContentType 校验客户端声明的 MIME 是否在白名单内；空表示未声明，跳过校验。
func validateContentType(contentType string, allowed []string) error {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return nil
	}
	if !contains(allowed, contentType) {
		return codes.New(codes.CodeUploadInvalidInput)
	}
	return nil
}

// generateKey 预生成存储 key：upload/{yyyyMMdd}/{随机hex}.{ext}（ext 为空时省略后缀）。
func generateKey(ext string) (string, error) {
	segment, err := randomHex(keyRandomBytes)
	if err != nil {
		return "", err
	}
	key := "upload/" + time.Now().Format("20060102") + "/" + segment
	if ext != "" {
		key += "." + ext
	}
	return key, nil
}

// uploadHost 按七牛 region 映射上传主机（https 源站入口），region 无效返回 false。
func uploadHost(region string) (string, bool) {
	r, ok := qstorage.GetRegionByID(qstorage.RegionID(region))
	if !ok || len(r.SrcUpHosts) == 0 {
		return "", false
	}
	return "https://" + r.SrcUpHosts[0], true
}

// randomHex 生成 n 字节密码学随机数的 hex 编码。
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// contains 判断字符串是否在列表中（白名单精确匹配）。
func contains(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}

func cfgString(ctx context.Context, key, def string) string {
	v, err := g.Cfg().GetEffective(ctx, key, def)
	if err != nil || v == nil {
		return def
	}
	return v.String()
}

func cfgInt64(ctx context.Context, key string, def int64) int64 {
	v, err := g.Cfg().GetEffective(ctx, key, def)
	if err != nil || v == nil {
		return def
	}
	return v.Int64()
}

func cfgStrings(ctx context.Context, key string, def []string) []string {
	v, err := g.Cfg().GetEffective(ctx, key)
	if err != nil || v == nil || v.IsEmpty() {
		return def
	}
	s := v.Strings()
	if len(s) == 0 {
		return def
	}
	return s
}
