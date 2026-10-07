package boot

import (
	"context"
	"errors"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/glog"
	"golang.org/x/crypto/bcrypt"
)

const (
	// defaultSuperUsername 是超级管理员的默认用户名（非机密）。
	defaultSuperUsername = "admin"
	// adminStatusEnabled 表示管理员启用状态。
	adminStatusEnabled = 1
)

// permissionSeed 是权限 code 与展示名的清单（细粒度「资源:动作」）。
type permissionSeed struct {
	Code string
	Name string
}

// seedPermissionList 是启动时幂等写入的标准权限（按 code 唯一）。
var seedPermissionList = []permissionSeed{
	{Code: "category:create", Name: "创建分类"},
	{Code: "category:update", Name: "更新分类"},
	{Code: "category:delete", Name: "删除分类"},
	{Code: "product:create", Name: "创建商品"},
	{Code: "product:update", Name: "更新商品"},
	{Code: "product:on_shelf", Name: "上架商品"},
	{Code: "product:off_shelf", Name: "下架商品"},
	{Code: "sku:create", Name: "创建 SKU"},
	{Code: "sku:update", Name: "更新 SKU"},
	{Code: "sku:delete", Name: "删除 SKU"},
	{Code: "inventory:increase", Name: "增加库存"},
	{Code: "inventory:deduct", Name: "扣减库存"},
	{Code: "admin:create", Name: "创建管理员"},
	{Code: "admin:disable", Name: "禁用管理员"},
	{Code: "admin:delete", Name: "删除管理员"},
	{Code: "admin:assign_role", Name: "分配管理员角色"},
	{Code: "role:create", Name: "创建角色"},
	{Code: "role:list", Name: "查看角色"},
	{Code: "role:update", Name: "更新角色"},
	{Code: "role:delete", Name: "删除角色"},
	{Code: "role:assign_permission", Name: "分配角色权限"},
	{Code: "permission:create", Name: "创建权限"},
	{Code: "permission:list", Name: "查看权限"},
	{Code: "permission:update", Name: "更新权限"},
	{Code: "permission:delete", Name: "删除权限"},
	{Code: "order:ship", Name: "订单发货"},
	{Code: "order:refund", Name: "订单退款"},
	{Code: "flash_sale:create", Name: "创建秒杀活动"},
	{Code: "flash_sale:update", Name: "更新秒杀活动"},
	{Code: "flash_sale:repair", Name: "修复秒杀请求"},
	{Code: "review:take_down", Name: "下架评价"},
	{Code: "banner:create", Name: "创建轮播图"},
	{Code: "banner:update", Name: "更新轮播图"},
	{Code: "banner:delete", Name: "删除轮播图"},
	{Code: "recommend:create", Name: "创建推荐位"},
	{Code: "recommend:update", Name: "更新推荐位"},
	{Code: "recommend:delete", Name: "删除推荐位"},
	{Code: "recommend:item", Name: "管理推荐商品"},
}

// seedPermissions 幂等写入标准权限：已存在（code 唯一）则跳过。
// 并发场景下「先查再写」存在竞争窗口，真正兜底是 permissions.code 唯一约束：
// 并发 insert 命中 1062 视为「已被其他实例创建」，跳过。
func seedPermissions(ctx context.Context) error {
	for _, p := range seedPermissionList {
		_, err := g.DB().Model("permissions").Ctx(ctx).Data(g.Map{
			"code": p.Code, "name": p.Name,
		}).Insert()
		if err != nil {
			if isDuplicateKeyError(err) {
				continue
			}
			return gerror.Wrapf(err, "seed 权限 %s", p.Code)
		}
	}
	glog.Info(ctx, "权限 seed 已就绪")
	return nil
}

// seedSuperAdmin 幂等创建唯一超级管理员（is_super=1）：
// 已存在则跳过且不覆盖密码；不存在且未配置初始密码时启动 fail-fast。
//
// 并发场景（多实例同时启动）下「先查再写」存在竞争窗口，真正兜底的是
// admins.username 唯一约束：并发 insert 命中 1062 视为「已被其他实例创建」，跳过。
func seedSuperAdmin(ctx context.Context) error {
	username := cfgString(ctx, "admin.super.username", defaultSuperUsername)
	if username == "" {
		return errors.New("超级管理员用户名不能为空（admin.super.username / ADMIN_SUPER_USERNAME）")
	}
	password := cfgString(ctx, "admin.super.password", "")

	n, err := g.DB().Model("admins").Ctx(ctx).Where("is_super", 1).Count()
	if err != nil {
		return gerror.Wrap(err, "查询超级管理员")
	}
	if n > 0 {
		glog.Info(ctx, "超级管理员已存在，跳过 seed")
		return nil
	}

	if password == "" {
		return errors.New("未配置超级管理员初始密码 admin.super.password（ADMIN_SUPER_PASSWORD），无法创建超级管理员")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return gerror.Wrap(err, "哈希超级管理员密码")
	}

	if _, err := g.DB().Model("admins").Ctx(ctx).Data(g.Map{
		"username":      username,
		"password_hash": string(hash),
		"status":        adminStatusEnabled,
		"is_super":      1,
	}).Insert(); err != nil {
		if isDuplicateKeyError(err) {
			// 并发 seed：另一个实例已抢先创建，视为幂等成功，不覆盖密码。
			glog.Info(ctx, "超级管理员已被并发创建，跳过 seed")
			return nil
		}
		return gerror.Wrap(err, "创建超级管理员")
	}
	glog.Info(ctx, "超级管理员已创建")
	return nil
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062）。
// 并发写入同一唯一键时，DB 唯一约束是最终兜底，不能只依赖「先查再写」。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
