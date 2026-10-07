package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/upload/v1"
)

// IUpload 定义文件上传（七牛云直传凭证签发）服务。
type IUpload interface {
	// ValidateConfig 校验七牛云非机密结构配置（region/ttl/大小/白名单），供启动时 fail-fast。
	ValidateConfig(ctx context.Context) error
	// IssueToken 校验请求声明（扩展名/MIME）并签发七牛云直传凭证；无状态、不落库。
	IssueToken(ctx context.Context, filename, contentType string) (*v1.TokenRes, error)
}

var localUpload IUpload

// Upload 返回文件上传服务实现。
func Upload() IUpload {
	if localUpload == nil {
		panic("implement not found for interface IUpload, forgot register?")
	}
	return localUpload
}

// RegisterUpload 注册文件上传服务实现。
func RegisterUpload(s IUpload) {
	localUpload = s
}
