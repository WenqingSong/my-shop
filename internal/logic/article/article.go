// Package article 实现「用户文章」业务逻辑：登录用户发布/修改/删除自己的文章、
// 公开列表与详情、我的文章列表，以及点赞/收藏（幂等 + 唯一约束 + 公开计数 + 独立鉴权状态查询）。
// 点赞/收藏复用商品点赞/收藏的机制，但使用独立表 article_likes/article_favorites，不改动 product_likes/favorites。
package article

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/article/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	maxTitleLen   = 64    // 标题最大字符数（trim 后）
	maxContentLen = 10000 // 正文最大字符数（trim 后）

	defaultPage = 1
	defaultSize = 20
	maxSize     = 100
)

type sArticle struct{}

func init() {
	service.RegisterArticle(New())
}

// New 创建并返回用户文章服务实现。
func New() *sArticle {
	return &sArticle{}
}

// articleRow 是文章条目联查结果（LEFT JOIN users 取 author_username，容忍用户缺失）。
type articleRow struct {
	Id             int64       `orm:"id"`
	AuthorId       int64       `orm:"author_id"`
	AuthorUsername *string     `orm:"author_username"`
	Title          string      `orm:"title"`
	Content        string      `orm:"content"`
	CreatedAt      *gtime.Time `orm:"created_at"`
	UpdatedAt      *gtime.Time `orm:"updated_at"`
}

// List 公开文章列表（无需登录）：按 id 倒序分页，LEFT JOIN users 取作者用户名。
func (s *sArticle) List(ctx context.Context, req *v1.ListReq) (*v1.ListRes, error) {
	page, size := normalizePage(req.Page, req.Size)

	total, err := g.DB().Model("articles").Ctx(ctx).Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计公开文章列表: %w", err))
	}

	var rows []*articleRow
	if err := s.articleQuery(ctx).Order("a.id DESC").Page(page, size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询公开文章列表: %w", err))
	}

	items := make([]*v1.ArticleItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, s.toItem(r))
	}
	return &v1.ListRes{Items: items, Total: int(total), Page: page, Size: size}, nil
}

// Detail 公开文章详情（无需登录）：不存在返回 16001。
func (s *sArticle) Detail(ctx context.Context, id int64) (*v1.DetailRes, error) {
	row, err := s.findOne(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeArticleNotFound)
	}
	return &v1.DetailRes{Article: *s.toArticle(row)}, nil
}

// Create 发布文章：作者身份取自服务端 Principal.UserID（控制器传入），
// 请求体仅 title/content，客户端提交的任何身份字段被忽略（无字段可绑定）。
func (s *sArticle) Create(ctx context.Context, userID int64, req *v1.CreateReq) (*v1.CreateRes, error) {
	title, err := validateTitle(req.Title)
	if err != nil {
		return nil, err
	}
	content, err := validateContent(req.Content)
	if err != nil {
		return nil, err
	}

	id, err := g.DB().Model("articles").Ctx(ctx).Data(g.Map{
		"author_id": userID,
		"title":     title,
		"content":   content,
	}).InsertAndGetId()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入文章: %w", err))
	}

	row, err := s.findOne(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入文章后未找到记录"))
	}
	return &v1.CreateRes{Article: *s.toArticle(row)}, nil
}

// Update 修改本人文章：条件更新 WHERE id AND author_id，核对 RowsAffected，=0 → 16001（防枚举）。
func (s *sArticle) Update(ctx context.Context, userID int64, req *v1.UpdateReq) (*v1.UpdateRes, error) {
	title, err := validateTitle(req.Title)
	if err != nil {
		return nil, err
	}
	content, err := validateContent(req.Content)
	if err != nil {
		return nil, err
	}

	result, err := g.DB().Model("articles").Ctx(ctx).
		Where("id", req.Id).
		Where("author_id", userID).
		Data(g.Map{"title": title, "content": content}).
		Update()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("修改文章: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return nil, codes.New(codes.CodeArticleNotFound)
	}

	row, err := s.findOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("修改文章后未找到记录"))
	}
	return &v1.UpdateRes{Article: *s.toArticle(row)}, nil
}

// Delete 删除本人文章：单事务内先清理点赞/收藏关联，再条件删除文章本体并核对 RowsAffected。
// 任一步失败整体回滚；文章不存在或非本人（RowsAffected=0）回滚并返回 16001，无孤儿残留、无半删状态。
func (s *sArticle) Delete(ctx context.Context, userID, id int64) (*v1.DeleteRes, error) {
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if _, e := tx.Model("article_likes").Ctx(ctx).Where("article_id", id).Delete(); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("清理文章点赞: %w", e))
		}
		if _, e := tx.Model("article_favorites").Ctx(ctx).Where("article_id", id).Delete(); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("清理文章收藏: %w", e))
		}
		result, e := tx.Model("articles").Ctx(ctx).Where("id", id).Where("author_id", userID).Delete()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除文章: %w", e))
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return codes.New(codes.CodeArticleNotFound)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &v1.DeleteRes{}, nil
}

// MyList 查询本人文章列表（仅 author_id == Principal.UserID）。
func (s *sArticle) MyList(ctx context.Context, userID int64, req *v1.MyListReq) (*v1.MyListRes, error) {
	page, size := normalizePage(req.Page, req.Size)

	total, err := g.DB().Model("articles").Ctx(ctx).Where("author_id", userID).Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计本人文章: %w", err))
	}

	var rows []*articleRow
	if err := s.articleQuery(ctx).Where("a.author_id", userID).Order("a.id DESC").Page(page, size).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询本人文章: %w", err))
	}

	items := make([]*v1.Article, 0, len(rows))
	for _, r := range rows {
		items = append(items, s.toArticle(r))
	}
	return &v1.MyListRes{Items: items, Total: int(total), Page: page, Size: size}, nil
}

// Like 点赞文章：校验文章存在后单条 INSERT，命中 uk_user_article（1062）视为幂等成功。
func (s *sArticle) Like(ctx context.Context, userID int64, req *v1.LikeReq) (*v1.LikeRes, error) {
	if req.Id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if err := s.ensureArticleExists(ctx, req.Id); err != nil {
		return nil, err
	}
	if _, err := g.DB().Model("article_likes").Ctx(ctx).Data(g.Map{
		"user_id":    userID,
		"article_id": req.Id,
	}).Insert(); err != nil {
		if !isDuplicateKeyError(err) {
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入文章点赞: %w", err))
		}
		// 重复点赞：唯一约束兜底，幂等成功。
	}
	return &v1.LikeRes{Liked: true}, nil
}

// Unlike 取消点赞：按 article_id AND user_id 物理删除；RowsAffected=0 视为幂等成功（no-op）。
func (s *sArticle) Unlike(ctx context.Context, userID, id int64) (*v1.UnlikeRes, error) {
	if _, err := g.DB().Model("article_likes").Ctx(ctx).
		Where("article_id", id).
		Where("user_id", userID).
		Delete(); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("取消文章点赞: %w", err))
	}
	return &v1.UnlikeRes{}, nil
}

// LikeCheck 查询本人对指定文章是否已点赞。
func (s *sArticle) LikeCheck(ctx context.Context, userID, id int64) (*v1.LikeCheckRes, error) {
	if id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	n, err := g.DB().Model("article_likes").Ctx(ctx).
		Where("user_id", userID).
		Where("article_id", id).
		Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询文章点赞状态: %w", err))
	}
	return &v1.LikeCheckRes{Liked: n > 0}, nil
}

// LikeCount 查询指定文章的公开点赞数（无需登录）：实时 COUNT 聚合，不校验文章存在性（不存在文章点赞数为 0）。
func (s *sArticle) LikeCount(ctx context.Context, id int64) (*v1.LikeCountRes, error) {
	if id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	n, err := g.DB().Model("article_likes").Ctx(ctx).
		Where("article_id", id).
		Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计文章点赞数: %w", err))
	}
	return &v1.LikeCountRes{Count: n}, nil
}

// Favorite 收藏文章：校验文章存在后单条 INSERT，命中 uk_user_article（1062）视为幂等成功。
func (s *sArticle) Favorite(ctx context.Context, userID int64, req *v1.FavoriteReq) (*v1.FavoriteRes, error) {
	if req.Id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if err := s.ensureArticleExists(ctx, req.Id); err != nil {
		return nil, err
	}
	if _, err := g.DB().Model("article_favorites").Ctx(ctx).Data(g.Map{
		"user_id":    userID,
		"article_id": req.Id,
	}).Insert(); err != nil {
		if !isDuplicateKeyError(err) {
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入文章收藏: %w", err))
		}
		// 重复收藏：唯一约束兜底，幂等成功。
	}
	return &v1.FavoriteRes{Favorited: true}, nil
}

// Unfavorite 取消收藏：按 article_id AND user_id 物理删除；RowsAffected=0 视为幂等成功（no-op）。
func (s *sArticle) Unfavorite(ctx context.Context, userID, id int64) (*v1.UnfavoriteRes, error) {
	if _, err := g.DB().Model("article_favorites").Ctx(ctx).
		Where("article_id", id).
		Where("user_id", userID).
		Delete(); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("取消文章收藏: %w", err))
	}
	return &v1.UnfavoriteRes{}, nil
}

// FavoriteCheck 查询本人对指定文章是否已收藏。
func (s *sArticle) FavoriteCheck(ctx context.Context, userID, id int64) (*v1.FavoriteCheckRes, error) {
	if id <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	n, err := g.DB().Model("article_favorites").Ctx(ctx).
		Where("user_id", userID).
		Where("article_id", id).
		Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询文章收藏状态: %w", err))
	}
	return &v1.FavoriteCheckRes{Favorited: n > 0}, nil
}

// MyFavorites 查询本人收藏列表：LEFT JOIN articles/users，按收藏时间倒序分页。
func (s *sArticle) MyFavorites(ctx context.Context, userID int64, req *v1.MyFavoritesReq) (*v1.MyFavoritesRes, error) {
	page, size := normalizePage(req.Page, req.Size)

	total, err := g.DB().Model("article_favorites").Ctx(ctx).Where("user_id", userID).Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计文章收藏列表: %w", err))
	}

	var rows []*articleRow
	if err := g.DB().Ctx(ctx).Model("article_favorites af").
		Fields("a.id", "a.author_id", "a.title", "a.created_at", "a.updated_at", "u.username AS author_username").
		LeftJoin("articles a", "a.id = af.article_id").
		LeftJoin("users u", "u.id = a.author_id").
		Where("af.user_id", userID).
		Order("af.id DESC").
		Page(page, size).
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询文章收藏列表: %w", err))
	}

	items := make([]*v1.ArticleItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, s.toItem(r))
	}
	return &v1.MyFavoritesRes{Items: items, Total: int(total), Page: page, Size: size}, nil
}

// articleQuery 构造文章条目联查（LEFT JOIN users 取 author_username），容忍用户缺失（返回空）。
func (s *sArticle) articleQuery(ctx context.Context) *gdb.Model {
	return g.DB().Ctx(ctx).Model("articles a").
		Fields(
			"a.id", "a.author_id", "a.title", "a.content", "a.created_at", "a.updated_at",
			"u.username AS author_username",
		).
		LeftJoin("users u", "u.id = a.author_id")
}

// findOne 按 id 联查单篇文章，未命中返回 nil。
func (s *sArticle) findOne(ctx context.Context, id int64) (*articleRow, error) {
	var rows []*articleRow
	if err := s.articleQuery(ctx).Where("a.id", id).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询文章: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// ensureArticleExists 校验文章存在（点赞/收藏前置校验），不存在返回 16001。
func (s *sArticle) ensureArticleExists(ctx context.Context, id int64) error {
	n, err := g.DB().Model("articles").Ctx(ctx).Where("id", id).Count()
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("校验文章存在性: %w", err))
	}
	if n == 0 {
		return codes.New(codes.CodeArticleNotFound)
	}
	return nil
}

// toArticle 将文章行转换为完整对外结构（author_username 为空时容忍）。
func (s *sArticle) toArticle(r *articleRow) *v1.Article {
	a := &v1.Article{
		Id:        r.Id,
		AuthorId:  r.AuthorId,
		Title:     r.Title,
		Content:   r.Content,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
	if r.AuthorUsername != nil {
		a.AuthorUsername = *r.AuthorUsername
	}
	return a
}

// toItem 将文章行转换为列表条目（不含正文）。
func (s *sArticle) toItem(r *articleRow) *v1.ArticleItem {
	it := &v1.ArticleItem{
		Id:        r.Id,
		AuthorId:  r.AuthorId,
		Title:     r.Title,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
	if r.AuthorUsername != nil {
		it.AuthorUsername = *r.AuthorUsername
	}
	return it
}

// validateTitle trim 后校验非空与长度，返回规范化标题。
func validateTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", codes.New(codes.CodeArticleInvalidInput)
	}
	if utf8.RuneCountInString(title) > maxTitleLen {
		return "", codes.New(codes.CodeArticleInvalidInput)
	}
	return title, nil
}

// validateContent trim 后校验非空与长度，返回规范化正文。
func validateContent(content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", codes.New(codes.CodeArticleInvalidInput)
	}
	if utf8.RuneCountInString(content) > maxContentLen {
		return "", codes.New(codes.CodeArticleInvalidInput)
	}
	return content, nil
}

// normalizePage 归一化分页参数：page/size 下限 1，size 上限 maxSize。
func normalizePage(page, size int) (int, int) {
	if page < 1 {
		page = defaultPage
	}
	if size < 1 {
		size = defaultSize
	}
	if size > maxSize {
		size = maxSize
	}
	return page, size
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062）。
// 并发点赞/收藏同一 (user_id, article_id) 时，uk_user_article 唯一约束是最终兜底。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
