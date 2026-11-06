// Package upload 实现「文件上传 / 对象存储（七牛云）」业务逻辑：后端签发七牛云直传凭证
// （upload token），文件本体由客户端直传七牛，后端不承载上传流量、不感知上传最终结果。
// V1 为无状态签发、不落库；七牛云凭据经配置安全注入，不硬编码、不进日志/响应。
package upload

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gogf/gf/v2/os/glog"

	qauth "github.com/qiniu/go-sdk/v7/auth"
	qclient "github.com/qiniu/go-sdk/v7/client"
	qstorage "github.com/qiniu/go-sdk/v7/storage"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/upload/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/config"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// 说明：qiniu 配置的默认值统一由 internal/config 契约注册表持有（含区域/凭证 TTL/
// 文件大小上限/扩展名与 MIME 白名单），本包不再维护第二份默认值。
const (
	// keyRandomBytes 是存储 key 随机段的字节数（hex 编码后长度翻倍）。
	keyRandomBytes = 16

	// bucketCheckTimeout 是启动时七牛 bucket 可用性检查的单次 HTTP 超时上限。
	// SDK 的 GetBucketInfo 内部使用 context.Background()、不接受外部 ctx，
	// 因此通过注入带 Timeout 的 http.Client 约束超时。
	bucketCheckTimeout = 10 * time.Second
)

type sUpload struct {
	// checkBucket 是启动时七牛 bucket 真实可用性检查的薄函数，测试可注入假实现验证接线；
	// 生产默认 checkBucketAvailable（GetBucketInfo，指定 bucket 最小权限只读）。
	checkBucket func(ctx context.Context, cfg qiniuConfig) error
}

func init() {
	service.RegisterUpload(New())
}

// New 创建并返回文件上传服务实现。
func New() *sUpload {
	return &sUpload{checkBucket: checkBucketAvailable}
}

// qiniuConfig 是 qiniu 段配置的运行时快照。
// access_key/secret_key/bucket/domain 无默认值，为启动 required dependency，缺失由
// validateRequired 在启动 fail-fast；region/token_ttl/max_file_size 与白名单有安全默认值，
// 格式非法由 validateStructural 在启动拒绝。签发路径另有 validateCredentials 运行期守卫（17002）。
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

// ValidateConfig 在 serve 启动时校验七牛云 required dependency（fail-fast）：
// 结构校验（region/ttl/大小/白名单）→ 存在性校验（AK/SK/bucket/domain）→
// 对指定 bucket 的真实只读可用性检查（GetBucketInfo）。任一失败返回错误，进程非零退出。
// 错误信息只指认字段名/失败原因，不泄漏凭据值、内部路径或堆栈。
func (s *sUpload) ValidateConfig(ctx context.Context) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}
	if err := cfg.validateStructural(); err != nil {
		return err
	}
	if err := cfg.validateRequired(); err != nil {
		return err
	}
	return s.checkBucket(ctx, cfg)
}

// IssueToken 校验请求声明（扩展名/MIME）并签发七牛云直传凭证。
// 凭据或 bucket/domain 缺失/非法时返回稳定错误码 17002，不泄漏任何凭据细节。
func (s *sUpload) IssueToken(ctx context.Context, filename, contentType string) (*v1.TokenRes, error) {
	cfg, err := loadConfig(ctx)
	if err != nil {
		// 配置读取失败按「配置不可用」拒绝签发（17002），不把内部路径/格式细节回给调用方。
		glog.Errorf(ctx, "读取七牛配置失败: %v", err)
		return nil, codes.New(codes.CodeUploadConfigInvalid)
	}
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

// loadConfig 读取 qiniu 段配置（环境变量 > config.yaml > 代码默认值）。
// 配置读取失败（非法类型）返回错误，由调用方决定 fail-fast 或 17002，不静默使用空值。
func loadConfig(ctx context.Context) (qiniuConfig, error) {
	read := &qiniuConfigReader{ctx: ctx}
	cfg := qiniuConfig{
		accessKey:         read.str(config.KeyQiniuAccessKey),
		secretKey:         read.str(config.KeyQiniuSecretKey),
		bucket:            read.str(config.KeyQiniuBucket),
		domain:            read.str(config.KeyQiniuDomain),
		region:            read.str(config.KeyQiniuRegion),
		tokenTTL:          read.int64Value(config.KeyQiniuTokenTTL),
		maxFileSize:       read.int64Value(config.KeyQiniuMaxFileSize),
		allowedExtensions: read.list(config.KeyQiniuAllowedExtensions),
		allowedMimeTypes:  read.list(config.KeyQiniuAllowedMimeTypes),
	}
	if err := read.err; err != nil {
		return qiniuConfig{}, err
	}
	return cfg, nil
}

// qiniuConfigReader 顺序读取 qiniu 段配置并保留首个错误。
type qiniuConfigReader struct {
	ctx context.Context
	err error
}

// str 读取字符串配置项。
func (r *qiniuConfigReader) str(key config.Key) string {
	if r.err != nil {
		return ""
	}
	value, err := config.String(r.ctx, key)
	if err != nil {
		r.err = err
	}
	return value
}

// int64Value 读取 int64 配置项。
func (r *qiniuConfigReader) int64Value(key config.Key) int64 {
	if r.err != nil {
		return 0
	}
	value, err := config.Int64(r.ctx, key)
	if err != nil {
		r.err = err
	}
	return value
}

// list 读取字符串列表配置项。
func (r *qiniuConfigReader) list(key config.Key) []string {
	if r.err != nil {
		return nil
	}
	value, err := config.Strings(r.ctx, key)
	if err != nil {
		r.err = err
	}
	return value
}

// validateStructural 校验非机密结构配置（region/ttl/大小/白名单）的格式，供启动 fail-fast。
// 凭据与 bucket/domain 的存在性由 validateRequired 在启动校验，签发路径由 validateCredentials 守卫。
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

// validateRequired 校验启动 required dependency 的七牛配置存在性（AK/SK/bucket/domain）。
// 与 validateCredentials（运行期 17002 守卫）不同：它直接指认缺失字段名，用于启动 fail-fast，
// 不返回业务错误码、不泄漏凭据值。
func (c qiniuConfig) validateRequired() error {
	switch {
	case c.accessKey == "":
		return fmt.Errorf("qiniu.access_key 未配置")
	case c.secretKey == "":
		return fmt.Errorf("qiniu.secret_key 未配置")
	case c.bucket == "":
		return fmt.Errorf("qiniu.bucket 未配置")
	case c.domain == "":
		return fmt.Errorf("qiniu.domain 未配置")
	}
	return nil
}

// checkBucketAvailable 用七牛 SDK 对指定 bucket 做最小权限只读可用性检查：BucketManager.GetBucketInfo(bucket)
// （POST /v2/bucketInfo?bucket=），仅验证「bucket 存在 + AK/SK 可访问」，不使用 Buckets() 以避免
// 「列出整个账户全部 bucket」的额外权限。SDK 内部使用 context.Background()、不接受外部 ctx，
// 这里通过带 Timeout 的 http.Client 约束单次请求超时上限；错误只保留 bucket 名，不泄漏凭据值。
func checkBucketAvailable(_ context.Context, cfg qiniuConfig) error {
	clt := &qclient.Client{Client: &http.Client{
		Transport: qclient.DefaultTransport,
		Timeout:   bucketCheckTimeout,
	}}
	manager := qstorage.NewBucketManagerEx(
		qauth.New(cfg.accessKey, cfg.secretKey),
		&qstorage.Config{UseHTTPS: true},
		clt,
	)
	if _, err := manager.GetBucketInfo(cfg.bucket); err != nil {
		return fmt.Errorf("七牛 bucket %q 可用性检查失败: %w", cfg.bucket, err)
	}
	return nil
}

// validateCredentials 是签发路径的运行期防御性守卫：校验签发所需凭据与桶/域名是否齐全；
// 缺失返回稳定 17002（不泄漏细节）。启动阶段的必填校验见 validateRequired（fail-fast）。
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
