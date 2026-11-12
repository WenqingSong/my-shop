// Package freight 实现运费模板后台 API（管理员侧）。
//
// 不做前台运费预览接口：运费只允许在服务端计价链路中计算，
// 前端不参与决定运费金额。
package freight

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/freight/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// AdminControllerV1 实现运费模板后台 v1 API。
type AdminControllerV1 struct{}

// NewAdminV1 创建并返回运费模板后台 v1 控制器。
func NewAdminV1() *AdminControllerV1 {
	return &AdminControllerV1{}
}

// AdminCreate 处理创建运费模板（经 RequirePermission("freight:create")）。
func (c *AdminControllerV1) AdminCreate(ctx context.Context, req *v1.AdminCreateReq) (res *v1.AdminCreateRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Freight().AdminCreate(ctx, req)
}

// AdminList 处理运费模板列表（经 RequirePermission("freight:list")）。
func (c *AdminControllerV1) AdminList(ctx context.Context, req *v1.AdminListReq) (res *v1.AdminListRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Freight().AdminList(ctx)
}

// AdminDetail 处理运费模板详情（经 RequirePermission("freight:view")）。
func (c *AdminControllerV1) AdminDetail(ctx context.Context, req *v1.AdminDetailReq) (res *v1.AdminDetailRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Freight().AdminDetail(ctx, req.Id)
}

// AdminUpdate 处理更新运费模板（经 RequirePermission("freight:update")）。
func (c *AdminControllerV1) AdminUpdate(ctx context.Context, req *v1.AdminUpdateReq) (res *v1.AdminUpdateRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Freight().AdminUpdate(ctx, req)
}

// AdminUpdateStatus 处理启用/停用运费模板（经 RequirePermission("freight:update")）。
func (c *AdminControllerV1) AdminUpdateStatus(ctx context.Context, req *v1.AdminUpdateStatusReq) (res *v1.AdminUpdateStatusRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Freight().AdminUpdateStatus(ctx, req)
}

// AdminListRules 处理查询区域规则（经 RequirePermission("freight:view")）。
func (c *AdminControllerV1) AdminListRules(ctx context.Context, req *v1.AdminListRulesReq) (res *v1.AdminListRulesRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Freight().AdminListRules(ctx, req.Id)
}

// AdminReplaceRules 处理整体替换区域规则（经 RequirePermission("freight:update")）。
func (c *AdminControllerV1) AdminReplaceRules(ctx context.Context, req *v1.AdminReplaceRulesReq) (res *v1.AdminReplaceRulesRes, err error) {
	if _, ok := middleware.AdminPrincipalFromContext(ctx); !ok {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return service.Freight().AdminReplaceRules(ctx, req)
}
