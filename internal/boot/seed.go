package boot

import (
	"context"
	"errors"

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

// seedSuperAdmin 幂等创建唯一超级管理员（is_super=1）：
// 已存在则跳过且不覆盖密码；不存在且未配置初始密码时启动 fail-fast。
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
		return gerror.Wrap(err, "创建超级管理员")
	}
	glog.Info(ctx, "超级管理员已创建")
	return nil
}
