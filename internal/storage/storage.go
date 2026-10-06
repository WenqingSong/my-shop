// Package storage 定义静态资源的存储边界。V1 唯一实现为 LocalStorage（本地目录 + 静态服务），
// 后续替换 MinIO/OSS/S3 时实现同一接口即可，轮播图业务逻辑不感知具体存储介质。
package storage

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

// BannerURLPrefix 是轮播图图片对外的静态路由前缀。
// banners.image_url 存储 /storage/banners/<file> 形式的相对路径，由 Storage 边界统一约定。
const BannerURLPrefix = "/storage/banners"

// placeholderColors 是 3 张占位图的文件名与填充色（不同占位位以颜色区分）。
var placeholderColors = []struct {
	name  string
	color color.RGBA
}{
	{name: "banner-1.png", color: color.RGBA{R: 0xC0, G: 0xC0, B: 0xC0, A: 0xFF}}, // 灰
	{name: "banner-2.png", color: color.RGBA{R: 0x8A, G: 0xB4, B: 0xF8, A: 0xFF}}, // 蓝
	{name: "banner-3.png", color: color.RGBA{R: 0xF8, G: 0xC5, B: 0x8A, A: 0xFF}}, // 橙
}

// Storage 抽象静态资源的存储边界。V1 唯一实现为 LocalStorage。
type Storage interface {
	// PrepareBannerPlaceholders 幂等确保轮播图占位图就绪，返回其对外相对 URL 路径。
	PrepareBannerPlaceholders(ctx context.Context) ([]string, error)
}

// Local 是 V1 的本地文件存储实现（LocalStorage）。
type Local struct {
	root string
}

// 编译期校验 Local 实现 Storage 接口。
var _ Storage = (*Local)(nil)

// NewLocal 创建本地文件存储实现；root 为本地存储根目录（可配置）。
func NewLocal(root string) *Local {
	return &Local{root: root}
}

// BannerDir 返回轮播图图片的本地目录绝对路径（供 HTTP 静态服务映射，LocalStorage 语义）。
func (l *Local) BannerDir() string {
	return filepath.Join(l.root, "banners")
}

// PrepareBannerPlaceholders 幂等创建 banner 目录与 3 张占位图，返回其对外相对 URL。
// 已存在则跳过（不覆盖），保证启动 seed 可重复执行。
func (l *Local) PrepareBannerPlaceholders(ctx context.Context) ([]string, error) {
	dir := l.BannerDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建轮播图图片目录: %w", err)
	}
	urls := make([]string, 0, len(placeholderColors))
	for _, p := range placeholderColors {
		path := filepath.Join(dir, p.name)
		if _, err := os.Stat(path); err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("检查轮播图占位图 %s: %w", p.name, err)
			}
			if err := writePlaceholderPNG(path, p.color); err != nil {
				return nil, fmt.Errorf("写入轮播图占位图 %s: %w", p.name, err)
			}
		}
		urls = append(urls, BannerURLPrefix+"/"+p.name)
	}
	return urls, nil
}

// BannerPlaceholderURLs 返回 3 张占位图对外的相对 URL 路径（无需落盘即可取得）。
func BannerPlaceholderURLs() []string {
	urls := make([]string, 0, len(placeholderColors))
	for _, p := range placeholderColors {
		urls = append(urls, BannerURLPrefix+"/"+p.name)
	}
	return urls
}

// writePlaceholderPNG 生成一张指定填充色的极简 PNG 占位图。
func writePlaceholderPNG(path string, c color.RGBA) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	const size = 16
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return png.Encode(f, img)
}
