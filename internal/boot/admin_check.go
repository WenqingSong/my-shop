package boot

import (
	"context"
	"errors"

	"github.com/gogf/gf/v2/errors/gerror"
	"github.com/gogf/gf/v2/os/glog"
)

// CheckSuperAdminCondition 校验超级管理员创建条件，供 `my-shop admin check` / `make up` 启动前预检调用。
// 复用现有数据库访问与超管判断逻辑（superAdminExists），不写 Shell SQL、不修改任何数据：
//   - 数据库已存在 is_super=1 的超管 → 通过（幂等，不覆盖既有密码）；
//   - 不存在且未配置 admin.super.password → 返回指认 ADMIN_SUPER_PASSWORD 的错误；
//   - 不存在且已配置密码 → 通过（后续 seedSuperAdmin 在启动时创建）。
//
// admins 表不存在（尚未迁移）经 superAdminExists 视为「不存在」；其它数据库错误 fail-closed。
// 错误信息只指认键名/依赖名，不泄漏密码值、内部路径或堆栈。
func CheckSuperAdminCondition(ctx context.Context) error {
	if err := applyDatabaseConfig(ctx); err != nil {
		return gerror.Wrap(err, "应用数据库配置")
	}

	exists, err := superAdminExists(ctx)
	if err != nil {
		return gerror.Wrap(err, "查询超级管理员是否存在（请确认数据库依赖已就绪）")
	}
	if exists {
		glog.Info(ctx, "超级管理员已存在，跳过创建条件校验")
		return nil
	}

	password := cfgString(ctx, "admin.super.password", "")
	if password == "" {
		return errors.New("未配置超级管理员初始密码 admin.super.password（ADMIN_SUPER_PASSWORD）；数据库尚无超级管理员，首次启动需创建，请先在 .env 中填写后再重试")
	}
	glog.Info(ctx, "超级管理员不存在且已配置初始密码，将在启动时创建")
	return nil
}
