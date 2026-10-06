// Package banner 实现「轮播图」业务逻辑：公开列表（仅启用、按展示顺序）、
// 后台创建/更新/删除。图片经独立 Storage 边界引用（image_url 为软引用，V1 不校验文件存在）。
package banner

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/banner/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	statusEnabled  = 1 // 启用（公开）
	statusDisabled = 0 // 禁用（下线，后台可见）

	maxTitleLen = 64  // 标题最大字符数（trim 后）
	maxImageLen = 255 // 图片地址最大字符数
	maxLinkLen  = 512 // 跳转目标最大字符数
)

type sBanner struct{}

func init() {
	service.RegisterBanner(New())
}

// New 创建并返回轮播图服务实现。
func New() *sBanner {
	return &sBanner{}
}

// bannerRow 是 banners 表的一条记录。
type bannerRow struct {
	Id        int64       `json:"id"`
	Title     string      `json:"title"`
	ImageUrl  string      `json:"image_url"`
	LinkUrl   string      `json:"link_url"`
	Sort      int         `json:"sort"`
	Status    int         `json:"status"`
	CreatedAt *gtime.Time `json:"created_at"`
	UpdatedAt *gtime.Time `json:"updated_at"`
}

// List 返回公开启用的轮播图列表（status=1），按 sort 升序、同值按 id 升序。
func (s *sBanner) List(ctx context.Context) (*v1.ListRes, error) {
	var rows []*bannerRow
	if err := g.DB().Model("banners").Ctx(ctx).
		Where("status", statusEnabled).
		Order("sort", "id").
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询公开轮播图列表: %w", err))
	}

	items := make([]*v1.BannerItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, &v1.BannerItem{
			Id:       r.Id,
			Title:    r.Title,
			ImageUrl: r.ImageUrl,
			LinkUrl:  r.LinkUrl,
			Sort:     r.Sort,
		})
	}
	return &v1.ListRes{Items: items}, nil
}

// AdminList 返回后台全部轮播图列表（全部状态），按 sort 升序、同值按 id 升序。
func (s *sBanner) AdminList(ctx context.Context) (*v1.AdminListRes, error) {
	var rows []*bannerRow
	if err := g.DB().Model("banners").Ctx(ctx).
		Order("sort", "id").
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询后台轮播图列表: %w", err))
	}

	items := make([]*v1.Banner, 0, len(rows))
	for _, r := range rows {
		items = append(items, toBanner(r))
	}
	return &v1.AdminListRes{Items: items}, nil
}

// AdminDetail 返回单个轮播图完整字段（全部状态）；不存在返回 14001（404）。
func (s *sBanner) AdminDetail(ctx context.Context, id int64) (*v1.AdminDetailRes, error) {
	row, err := s.findOne(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeBannerNotFound)
	}
	return &v1.AdminDetailRes{Banner: *toBanner(row)}, nil
}

// Create 校验并持久化新轮播图：单条 INSERT，link_url 为空时不写入（列默认 NULL，即无跳转）。
func (s *sBanner) Create(ctx context.Context, req *v1.CreateReq) (*v1.CreateRes, error) {
	title, err := validateTitle(req.Title)
	if err != nil {
		return nil, err
	}
	imageURL, err := validateImageURL(req.ImageUrl)
	if err != nil {
		return nil, err
	}
	linkURL, err := validateLinkURL(req.LinkUrl)
	if err != nil {
		return nil, err
	}
	status := statusEnabled
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return nil, err
		}
		status = *req.Status
	}

	data := g.Map{
		"title":     title,
		"image_url": imageURL,
		"sort":      req.Sort,
		"status":    status,
	}
	if linkURL != "" {
		data["link_url"] = linkURL
	}

	id, err := g.DB().Model("banners").Ctx(ctx).Data(data).InsertAndGetId()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入轮播图: %w", err))
	}

	row, err := s.findOne(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入轮播图后未找到记录"))
	}
	return &v1.CreateRes{Banner: *toBanner(row)}, nil
}

// Update 按提交字段更新轮播图：仅更新提交字段，核对 RowsAffected（0 → 14001）。
// link_url 提交为空字符串时清空（写 NULL）；未提交字段保持不变。
func (s *sBanner) Update(ctx context.Context, req *v1.UpdateReq) (*v1.UpdateRes, error) {
	old, err := s.findOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, codes.New(codes.CodeBannerNotFound)
	}

	data := g.Map{}
	if req.Title != nil {
		title, err := validateTitle(*req.Title)
		if err != nil {
			return nil, err
		}
		data["title"] = title
	}
	if req.ImageUrl != nil {
		imageURL, err := validateImageURL(*req.ImageUrl)
		if err != nil {
			return nil, err
		}
		data["image_url"] = imageURL
	}
	if req.LinkUrl != nil {
		linkURL, err := validateLinkURL(*req.LinkUrl)
		if err != nil {
			return nil, err
		}
		if linkURL == "" {
			data["link_url"] = nil // 清空跳转
		} else {
			data["link_url"] = linkURL
		}
	}
	if req.Sort != nil {
		data["sort"] = *req.Sort
	}
	if req.Status != nil {
		if err := validateStatus(*req.Status); err != nil {
			return nil, err
		}
		data["status"] = *req.Status
	}

	// 未提交任何字段：视为幂等成功，返回当前值。
	if len(data) == 0 {
		return &v1.UpdateRes{Banner: *toBanner(old)}, nil
	}

	result, err := g.DB().Model("banners").Ctx(ctx).Where("id", req.Id).Data(data).Update()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新轮播图: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil, codes.New(codes.CodeBannerNotFound)
	}

	row, err := s.findOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新轮播图后未找到记录"))
	}
	return &v1.UpdateRes{Banner: *toBanner(row)}, nil
}

// Delete 物理删除轮播图：仅删 DB 记录（不触达文件系统），核对 RowsAffected。
func (s *sBanner) Delete(ctx context.Context, id int64) error {
	result, err := g.DB().Model("banners").Ctx(ctx).Where("id", id).Delete()
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除轮播图: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return codes.New(codes.CodeBannerNotFound)
	}
	return nil
}

// findOne 按 id 查询单条轮播图，未命中返回 nil。
func (s *sBanner) findOne(ctx context.Context, id int64) (*bannerRow, error) {
	var rows []*bannerRow
	if err := g.DB().Model("banners").Ctx(ctx).Where("id", id).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询轮播图: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// toBanner 将轮播图行转换为完整对外结构。
func toBanner(r *bannerRow) *v1.Banner {
	return &v1.Banner{
		Id:        r.Id,
		Title:     r.Title,
		ImageUrl:  r.ImageUrl,
		LinkUrl:   r.LinkUrl,
		Sort:      r.Sort,
		Status:    r.Status,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// validateTitle trim 后校验非空与长度，返回规范化标题。
func validateTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", codes.New(codes.CodeBannerInvalidInput)
	}
	if utf8.RuneCountInString(title) > maxTitleLen {
		return "", codes.New(codes.CodeBannerInvalidInput)
	}
	return title, nil
}

// validateImageURL trim 后校验非空与长度，返回规范化图片地址。
func validateImageURL(imageURL string) (string, error) {
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return "", codes.New(codes.CodeBannerInvalidInput)
	}
	if utf8.RuneCountInString(imageURL) > maxImageLen {
		return "", codes.New(codes.CodeBannerInvalidInput)
	}
	return imageURL, nil
}

// validateLinkURL trim 后校验长度（可空），返回规范化跳转目标。
func validateLinkURL(linkURL string) (string, error) {
	linkURL = strings.TrimSpace(linkURL)
	if utf8.RuneCountInString(linkURL) > maxLinkLen {
		return "", codes.New(codes.CodeBannerInvalidInput)
	}
	return linkURL, nil
}

// validateStatus 校验状态枚举值（1 启用 / 0 禁用）。
func validateStatus(status int) error {
	if status != statusEnabled && status != statusDisabled {
		return codes.New(codes.CodeBannerInvalidInput)
	}
	return nil
}
