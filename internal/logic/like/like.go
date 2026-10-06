// Package like 实现「商品点赞」业务逻辑：点赞（商品存在且在售校验 + 唯一约束兜底并发去重）、
// 取消（按 product_id 幂等）、本人是否已点赞、公开点赞数（实时 COUNT 聚合）。
package like

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/frame/g"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/like/v1"
	productv1 "cnb.cool/go-cloud-devops/my-shop/api/product/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

type sLike struct{}

func init() {
	service.RegisterLike(New())
}

// New 创建并返回商品点赞服务实现。
func New() *sLike {
	return &sLike{}
}

// Like 点赞：校验 product_id 为正、商品存在且在售，随后单条 INSERT；
// 命中 uk_user_product（1062）视为幂等成功（no-op），不产生第二条记录。
func (s *sLike) Like(ctx context.Context, userID int64, req *v1.LikeReq) (*v1.LikeRes, error) {
	if req.ProductId <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	product, err := service.Product().GetByID(ctx, req.ProductId)
	if err != nil {
		return nil, err
	}
	if product == nil {
		return nil, codes.New(codes.CodeProductNotFound)
	}
	if product.Status != productv1.StatusOnShelf {
		return nil, codes.New(codes.CodeLikeProductUnavailable)
	}

	if _, err := g.DB().Model("product_likes").Ctx(ctx).Data(g.Map{
		"user_id":    userID,
		"product_id": req.ProductId,
	}).Insert(); err != nil {
		if !isDuplicateKeyError(err) {
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入点赞: %w", err))
		}
		// 重复点赞：唯一约束兜底，幂等成功。
	}
	return &v1.LikeRes{Liked: true}, nil
}

// Cancel 取消点赞：按 product_id AND user_id 物理删除；
// RowsAffected=0（未点赞/不存在）视为幂等成功（no-op），不报错、不区分越权。
func (s *sLike) Cancel(ctx context.Context, userID, productID int64) error {
	if _, err := g.DB().Model("product_likes").Ctx(ctx).
		Where("product_id", productID).
		Where("user_id", userID).
		Delete(); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("取消点赞: %w", err))
	}
	return nil
}

// Check 查询本人对指定商品是否已点赞（供商品详情展示点赞态）。
func (s *sLike) Check(ctx context.Context, userID, productID int64) (*v1.CheckRes, error) {
	if productID <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	n, err := g.DB().Model("product_likes").Ctx(ctx).
		Where("user_id", userID).
		Where("product_id", productID).
		Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询点赞状态: %w", err))
	}
	return &v1.CheckRes{Liked: n > 0}, nil
}

// Count 查询指定商品的公开点赞数：实时 COUNT 聚合，不校验商品存在性（不存在的商品点赞数为 0）。
func (s *sLike) Count(ctx context.Context, productID int64) (*v1.CountRes, error) {
	if productID <= 0 {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	n, err := g.DB().Model("product_likes").Ctx(ctx).
		Where("product_id", productID).
		Count()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计点赞数: %w", err))
	}
	return &v1.CountRes{Count: n}, nil
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062）。
// 并发点赞同一 (user_id, product_id) 时，uk_user_product 唯一约束是最终兜底。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
