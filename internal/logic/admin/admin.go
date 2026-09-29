// Package admin 实现后台管理员身份与 RBAC 业务逻辑。
package admin

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"golang.org/x/crypto/bcrypt"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/admin/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/auth"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// adminStatusDisabled / adminStatusEnabled 管理员禁用/启用状态（admins.status）。
	adminStatusDisabled = 0
	adminStatusEnabled  = 1
	// superFlag 超级管理员标记（admins.is_super）。
	superFlag = 1

	// maxUsernameLen / maxPasswordLen 管理员用户名/密码长度上限（与 users 一致）。
	maxUsernameLen = 24
	maxPasswordLen = 24

	// maxRoleNameLen / maxDescriptionLen 角色名字/描述长度上限。
	maxRoleNameLen = 64
	maxDescLen     = 255

	// maxPermissionCodeLen / maxPermissionNameLen 权限 code/名称长度上限。
	maxPermissionCodeLen = 64
	maxPermissionNameLen = 64
)

// usernamePattern 管理员用户名：3~24 位大小写字母或数字（与前台用户一致）。
var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9]{3,24}$`)

// dummyPasswordHash 仅用于用户名不存在时执行一次等耗时的 bcrypt 比对，
// 以对齐耗时、防止通过响应时间枚举管理员用户名。
var dummyPasswordHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("admin-dummy-password-for-timing"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return h
}()

type sAdmin struct{}

func init() {
	service.RegisterAdmin(New())
}

// New 创建并返回后台管理员身份与 RBAC 服务实现。
func New() *sAdmin {
	return &sAdmin{}
}

// adminRecord 是 admins 表的一条记录（按需查询部分字段）。
type adminRecord struct {
	ID           int64
	Username     string
	PasswordHash string
	Status       int
	IsSuper      int
}

// roleRecord 是 roles 表的一条记录。
type roleRecord struct {
	ID          int64
	Name        string
	Description string
}

// permissionRecord 是 permissions 表的一条记录。
type permissionRecord struct {
	ID          int64
	Code        string
	Name        string
	Description string
}

// Login 校验管理员凭据 → 校验状态（禁用→401）→ 写管理员会话 → 签发 type=admin 的 JWT。
// 不存在用户与密码错误统一返回 INVALID_CREDENTIALS，并对不存在用户做假哈希比对对齐耗时。
// 禁用管理员（凭据正确但 status=0）返回 UNAUTHORIZED（401），不做枚举区分。
// 写会话失败视为登录失败（500，不签发 token），保证「返回的 token 必有有效会话」。
func (s *sAdmin) Login(ctx context.Context, req *v1.LoginReq) (*v1.LoginRes, error) {
	if err := validateUsername(req.Username); err != nil {
		return nil, err
	}
	if err := validatePassword(req.Password); err != nil {
		return nil, err
	}

	admin, err := s.findAdminByUsername(ctx, req.Username)
	if err != nil {
		return nil, err
	}
	if admin == nil {
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(req.Password))
		return nil, codes.New(codes.CodeInvalidCredentials)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(req.Password)); err != nil {
		return nil, codes.New(codes.CodeInvalidCredentials)
	}
	if admin.Status != adminStatusEnabled {
		return nil, codes.New(codes.CodeUnauthorized)
	}

	sid, err := auth.NewSid()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("生成 sid: %w", err))
	}
	ttl, err := auth.SessionTTL(ctx)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取会话 TTL: %w", err))
	}
	if err := auth.CreateAdminSession(ctx, sid, admin.ID, ttl); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入管理员会话: %w", err))
	}

	token, err := auth.Generate(ctx, auth.TypeAdmin, admin.ID, sid)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("签发 access token: %w", err))
	}
	return &v1.LoginRes{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   auth.ExpiresIn,
	}, nil
}

// Me 按已认证的 adminID 查询管理员，返回 id/username/is_super/所属角色名。
// 查询不到管理员时按 401 处理，不泄露存在性。
func (s *sAdmin) Me(ctx context.Context, adminID int64) (*v1.MeRes, error) {
	admin, err := s.findAdminByID(ctx, adminID)
	if err != nil {
		return nil, err
	}
	if admin == nil {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	roles, err := s.listRoleNames(ctx, adminID)
	if err != nil {
		return nil, err
	}
	return &v1.MeRes{
		Id:       admin.ID,
		Username: admin.Username,
		IsSuper:  admin.IsSuper == superFlag,
		Roles:    roles,
	}, nil
}

// Logout 撤销 sid 对应的管理员会话（幂等）。Redis 错误返回 500，不吞掉后返回成功。
func (s *sAdmin) Logout(ctx context.Context, sid string) (*v1.LogoutRes, error) {
	if err := auth.RevokeAdminSession(ctx, sid); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("撤销管理员会话: %w", err))
	}
	return &v1.LogoutRes{}, nil
}

// CreateAdmin 校验并创建普通管理员（is_super=0、status=1）。
// 并发同名创建依赖 admins.username 唯一约束判定（1062 → ADMIN_USERNAME_EXISTS）。
func (s *sAdmin) CreateAdmin(ctx context.Context, req *v1.CreateAdminReq) (*v1.CreateAdminRes, error) {
	if err := validateUsername(req.Username); err != nil {
		return nil, err
	}
	if err := validatePassword(req.Password); err != nil {
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("哈希密码: %w", err))
	}

	id, err := g.DB().Model("admins").Ctx(ctx).Data(g.Map{
		"username":      req.Username,
		"password_hash": string(hash),
		"status":        adminStatusEnabled,
		"is_super":      0,
	}).InsertAndGetId()
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeAdminUsernameExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入管理员: %w", err))
	}
	return &v1.CreateAdminRes{Id: id, Username: req.Username}, nil
}

// UpdateAdminStatus 禁用/启用目标管理员。
// 超级管理员不可被禁用/启用（2005）；普通管理员不能操作自身（2006）。
func (s *sAdmin) UpdateAdminStatus(ctx context.Context, currentAdminID int64, req *v1.UpdateAdminStatusReq) (*v1.UpdateAdminStatusRes, error) {
	if err := validateStatus(req.Status); err != nil {
		return nil, err
	}
	if err := s.checkTargetAdmin(ctx, currentAdminID, req.Id); err != nil {
		return nil, err
	}

	// checkTargetAdmin 已确认目标存在；Update 对「值未变化」也返回 0 行，故不再用
	// RowsAffected 判存在，避免「重复设为同状态」被误判为 404。
	if _, err := g.DB().Model("admins").Ctx(ctx).Where("id", req.Id).Data(g.Map{"status": req.Status}).Update(); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新管理员状态: %w", err))
	}
	return &v1.UpdateAdminStatusRes{}, nil
}

// DeleteAdmin 删除目标管理员（事务级联删除 admin_roles）。
// 超级管理员不可被删除（2005）；普通管理员不能删除自身（2006）。
func (s *sAdmin) DeleteAdmin(ctx context.Context, currentAdminID int64, req *v1.DeleteAdminReq) error {
	if err := s.checkTargetAdmin(ctx, currentAdminID, req.Id); err != nil {
		return err
	}

	return g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if _, err := tx.Model("admin_roles").Ctx(ctx).Where("admin_id", req.Id).Delete(); err != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除管理员角色关联: %w", err))
		}
		result, err := tx.Model("admins").Ctx(ctx).Where("id", req.Id).Delete()
		if err != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除管理员: %w", err))
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return codes.New(codes.CodeAdminNotFound)
		}
		return nil
	})
}

// AssignAdminRole 为目标管理员分配角色（幂等）。
// 超级管理员不可被改角色（2005）；普通管理员不能改自身角色（2006）。
func (s *sAdmin) AssignAdminRole(ctx context.Context, currentAdminID int64, req *v1.AssignAdminRoleReq) error {
	if err := s.checkTargetAdmin(ctx, currentAdminID, req.Id); err != nil {
		return err
	}
	role, err := s.findRoleByID(ctx, req.RoleId)
	if err != nil {
		return err
	}
	if role == nil {
		return codes.New(codes.CodeRoleNotFound)
	}

	if _, err := g.DB().Model("admin_roles").Ctx(ctx).Data(g.Map{
		"admin_id": req.Id,
		"role_id":  req.RoleId,
	}).Insert(); err != nil {
		if isDuplicateKeyError(err) {
			return nil // 重复分配无副作用
		}
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("分配管理员角色: %w", err))
	}
	return nil
}

// RemoveAdminRole 移除目标管理员的角色（幂等）。
// 超级管理员不可被改角色（2005）；普通管理员不能改自身角色（2006）。
func (s *sAdmin) RemoveAdminRole(ctx context.Context, currentAdminID int64, req *v1.RemoveAdminRoleReq) error {
	if err := s.checkTargetAdmin(ctx, currentAdminID, req.Id); err != nil {
		return err
	}
	if _, err := g.DB().Model("admin_roles").Ctx(ctx).
		Where("admin_id", req.Id).Where("role_id", req.RoleId).Delete(); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("移除管理员角色: %w", err))
	}
	return nil
}

// CreateRole 校验并创建角色。并发同名创建依赖 roles.name 唯一约束（1062 → ROLE_NAME_EXISTS）。
func (s *sAdmin) CreateRole(ctx context.Context, req *v1.CreateRoleReq) (*v1.CreateRoleRes, error) {
	name, err := validateRoleName(req.Name)
	if err != nil {
		return nil, err
	}
	desc, err := validateDescription(req.Description)
	if err != nil {
		return nil, err
	}

	id, err := g.DB().Model("roles").Ctx(ctx).Data(g.Map{
		"name":        name,
		"description": desc,
	}).InsertAndGetId()
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeRoleNameExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入角色: %w", err))
	}
	return &v1.CreateRoleRes{Id: id}, nil
}

// ListRole 返回全部角色，按 id 升序。
func (s *sAdmin) ListRole(ctx context.Context) (*v1.ListRoleRes, error) {
	var records []*roleRecord
	if err := g.DB().Model("roles").Ctx(ctx).Order("id").Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询角色列表: %w", err))
	}
	items := make([]*v1.Role, 0, len(records))
	for _, r := range records {
		items = append(items, toRole(r))
	}
	return &v1.ListRoleRes{Items: items}, nil
}

// UpdateRole 按提交字段更新角色；不存在返回 2007，改名冲突返回 2008。
func (s *sAdmin) UpdateRole(ctx context.Context, req *v1.UpdateRoleReq) (*v1.UpdateRoleRes, error) {
	old, err := s.findRoleByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, codes.New(codes.CodeRoleNotFound)
	}

	name := old.Name
	desc := old.Description
	if req.Name != nil {
		if name, err = validateRoleName(*req.Name); err != nil {
			return nil, err
		}
	}
	if req.Description != nil {
		if desc, err = validateDescription(*req.Description); err != nil {
			return nil, err
		}
	}

	if _, err := g.DB().Model("roles").Ctx(ctx).Where("id", req.Id).
		Data(g.Map{"name": name, "description": desc}).Update(); err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeRoleNameExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新角色: %w", err))
	}

	updated, err := s.findRoleByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, codes.New(codes.CodeRoleNotFound)
	}
	return &v1.UpdateRoleRes{Role: *toRole(updated)}, nil
}

// DeleteRole 删除角色（事务级联删除 role_permissions 与 admin_roles）。
func (s *sAdmin) DeleteRole(ctx context.Context, id int64) error {
	old, err := s.findRoleByID(ctx, id)
	if err != nil {
		return err
	}
	if old == nil {
		return codes.New(codes.CodeRoleNotFound)
	}

	return g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if _, err := tx.Model("role_permissions").Ctx(ctx).Where("role_id", id).Delete(); err != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除角色权限关联: %w", err))
		}
		if _, err := tx.Model("admin_roles").Ctx(ctx).Where("role_id", id).Delete(); err != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除管理员角色关联: %w", err))
		}
		result, err := tx.Model("roles").Ctx(ctx).Where("id", id).Delete()
		if err != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除角色: %w", err))
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return codes.New(codes.CodeRoleNotFound)
		}
		return nil
	})
}

// AssignRolePermission 为角色分配权限（幂等）。
func (s *sAdmin) AssignRolePermission(ctx context.Context, req *v1.AssignRolePermissionReq) error {
	role, err := s.findRoleByID(ctx, req.Id)
	if err != nil {
		return err
	}
	if role == nil {
		return codes.New(codes.CodeRoleNotFound)
	}
	perm, err := s.findPermissionByID(ctx, req.PermissionId)
	if err != nil {
		return err
	}
	if perm == nil {
		return codes.New(codes.CodePermissionNotFound)
	}

	if _, err := g.DB().Model("role_permissions").Ctx(ctx).Data(g.Map{
		"role_id":       req.Id,
		"permission_id": req.PermissionId,
	}).Insert(); err != nil {
		if isDuplicateKeyError(err) {
			return nil // 重复分配无副作用
		}
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("分配角色权限: %w", err))
	}
	return nil
}

// RemoveRolePermission 移除角色权限（幂等）。
func (s *sAdmin) RemoveRolePermission(ctx context.Context, req *v1.RemoveRolePermissionReq) error {
	if _, err := g.DB().Model("role_permissions").Ctx(ctx).
		Where("role_id", req.Id).Where("permission_id", req.PermissionId).Delete(); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("移除角色权限: %w", err))
	}
	return nil
}

// CreatePermission 校验并创建权限。并发同名 code 依赖 permissions.code 唯一约束（1062 → PERMISSION_CODE_EXISTS）。
func (s *sAdmin) CreatePermission(ctx context.Context, req *v1.CreatePermissionReq) (*v1.CreatePermissionRes, error) {
	code, err := validatePermissionCode(req.Code)
	if err != nil {
		return nil, err
	}
	name, err := validatePermissionName(req.Name)
	if err != nil {
		return nil, err
	}
	desc, err := validateDescription(req.Description)
	if err != nil {
		return nil, err
	}

	id, err := g.DB().Model("permissions").Ctx(ctx).Data(g.Map{
		"code":        code,
		"name":        name,
		"description": desc,
	}).InsertAndGetId()
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodePermissionCodeExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入权限: %w", err))
	}
	return &v1.CreatePermissionRes{Id: id}, nil
}

// ListPermission 返回全部权限，按 id 升序。
func (s *sAdmin) ListPermission(ctx context.Context) (*v1.ListPermissionRes, error) {
	var records []*permissionRecord
	if err := g.DB().Model("permissions").Ctx(ctx).Order("id").Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询权限列表: %w", err))
	}
	items := make([]*v1.Permission, 0, len(records))
	for _, r := range records {
		items = append(items, toPermission(r))
	}
	return &v1.ListPermissionRes{Items: items}, nil
}

// UpdatePermission 按提交字段更新权限；不存在返回 2009，code 冲突返回 2010。
func (s *sAdmin) UpdatePermission(ctx context.Context, req *v1.UpdatePermissionReq) (*v1.UpdatePermissionRes, error) {
	old, err := s.findPermissionByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, codes.New(codes.CodePermissionNotFound)
	}

	code := old.Code
	name := old.Name
	desc := old.Description
	if req.Code != nil {
		if code, err = validatePermissionCode(*req.Code); err != nil {
			return nil, err
		}
	}
	if req.Name != nil {
		if name, err = validatePermissionName(*req.Name); err != nil {
			return nil, err
		}
	}
	if req.Description != nil {
		if desc, err = validateDescription(*req.Description); err != nil {
			return nil, err
		}
	}

	if _, err := g.DB().Model("permissions").Ctx(ctx).Where("id", req.Id).
		Data(g.Map{"code": code, "name": name, "description": desc}).Update(); err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodePermissionCodeExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新权限: %w", err))
	}

	updated, err := s.findPermissionByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, codes.New(codes.CodePermissionNotFound)
	}
	return &v1.UpdatePermissionRes{Permission: *toPermission(updated)}, nil
}

// DeletePermission 删除权限（事务级联删除 role_permissions）。
func (s *sAdmin) DeletePermission(ctx context.Context, id int64) error {
	old, err := s.findPermissionByID(ctx, id)
	if err != nil {
		return err
	}
	if old == nil {
		return codes.New(codes.CodePermissionNotFound)
	}

	return g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if _, err := tx.Model("role_permissions").Ctx(ctx).Where("permission_id", id).Delete(); err != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除角色权限关联: %w", err))
		}
		result, err := tx.Model("permissions").Ctx(ctx).Where("id", id).Delete()
		if err != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除权限: %w", err))
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return codes.New(codes.CodePermissionNotFound)
		}
		return nil
	})
}

// checkTargetAdmin 校验目标管理员的保护规则：
// 目标不存在 → 2003；目标为超级管理员 → 2005；目标为调用者自身 → 2006。
// 目标身份取自已认证的 currentAdminID（来自 Principal），不接受请求体指定。
func (s *sAdmin) checkTargetAdmin(ctx context.Context, currentAdminID, targetID int64) error {
	target, err := s.findAdminByID(ctx, targetID)
	if err != nil {
		return err
	}
	if target == nil {
		return codes.New(codes.CodeAdminNotFound)
	}
	if target.IsSuper == superFlag {
		return codes.New(codes.CodeSuperAdminProtected)
	}
	if target.ID == currentAdminID {
		return codes.New(codes.CodeSelfOperationForbidden)
	}
	return nil
}

// findAdminByUsername 按用户名查询管理员（登录所需字段）。
func (s *sAdmin) findAdminByUsername(ctx context.Context, username string) (*adminRecord, error) {
	record, err := g.DB().Model("admins").Ctx(ctx).
		Fields("id", "username", "password_hash", "status", "is_super").
		Where("username", username).
		One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按用户名查询管理员: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return nil, nil
	}
	return &adminRecord{
		ID:           record["id"].Int64(),
		Username:     record["username"].String(),
		PasswordHash: record["password_hash"].String(),
		Status:       record["status"].Int(),
		IsSuper:      record["is_super"].Int(),
	}, nil
}

// findAdminByID 按 id 查询管理员（身份/状态/超级管理员标记）。
func (s *sAdmin) findAdminByID(ctx context.Context, id int64) (*adminRecord, error) {
	record, err := g.DB().Model("admins").Ctx(ctx).
		Fields("id", "username", "status", "is_super").
		Where("id", id).
		One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询管理员: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return nil, nil
	}
	return &adminRecord{
		ID:       record["id"].Int64(),
		Username: record["username"].String(),
		Status:   record["status"].Int(),
		IsSuper:  record["is_super"].Int(),
	}, nil
}

// findRoleByID 按 id 查询角色，不存在返回 nil。
func (s *sAdmin) findRoleByID(ctx context.Context, id int64) (*roleRecord, error) {
	record, err := g.DB().Model("roles").Ctx(ctx).
		Fields("id", "name", "description").
		Where("id", id).
		One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询角色: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return nil, nil
	}
	return &roleRecord{
		ID:          record["id"].Int64(),
		Name:        record["name"].String(),
		Description: record["description"].String(),
	}, nil
}

// findPermissionByID 按 id 查询权限，不存在返回 nil。
func (s *sAdmin) findPermissionByID(ctx context.Context, id int64) (*permissionRecord, error) {
	record, err := g.DB().Model("permissions").Ctx(ctx).
		Fields("id", "code", "name", "description").
		Where("id", id).
		One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询权限: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return nil, nil
	}
	return &permissionRecord{
		ID:          record["id"].Int64(),
		Code:        record["code"].String(),
		Name:        record["name"].String(),
		Description: record["description"].String(),
	}, nil
}

// listRoleNames 返回管理员所属角色名（按角色 id 升序）。
func (s *sAdmin) listRoleNames(ctx context.Context, adminID int64) ([]string, error) {
	var records []struct {
		Name string `json:"name"`
	}
	if err := g.DB().Model("roles r").Ctx(ctx).
		InnerJoin("admin_roles ar", "ar.role_id = r.id").
		Where("ar.admin_id", adminID).
		Fields("r.name").
		Order("r.id").
		Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询管理员角色: %w", err))
	}
	names := make([]string, 0, len(records))
	for _, r := range records {
		names = append(names, r.Name)
	}
	return names, nil
}

func toRole(r *roleRecord) *v1.Role {
	return &v1.Role{Id: r.ID, Name: r.Name, Description: r.Description}
}

func toPermission(r *permissionRecord) *v1.Permission {
	return &v1.Permission{Id: r.ID, Code: r.Code, Name: r.Name, Description: r.Description}
}

func validateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return codes.New(codes.CodeInvalidArgument)
	}
	return nil
}

func validatePassword(password string) error {
	n := len(password)
	if n < 8 || n > maxPasswordLen {
		return codes.New(codes.CodeInvalidArgument)
	}
	return nil
}

func validateStatus(status int) error {
	if status != adminStatusEnabled && status != adminStatusDisabled {
		return codes.New(codes.CodeInvalidArgument)
	}
	return nil
}

// validateRoleName trim 后校验非空与长度，返回规范化后的角色名。
func validateRoleName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	if utf8.RuneCountInString(name) > maxRoleNameLen {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	return name, nil
}

// validatePermissionCode trim 后校验非空与长度，返回规范化后的权限 code。
func validatePermissionCode(code string) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	if utf8.RuneCountInString(code) > maxPermissionCodeLen {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	return code, nil
}

// validatePermissionName trim 后校验非空与长度，返回规范化后的权限名。
func validatePermissionName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	if utf8.RuneCountInString(name) > maxPermissionNameLen {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	return name, nil
}

// validateDescription 校验描述长度（允许空），返回规范化后的描述。
func validateDescription(desc string) (string, error) {
	desc = strings.TrimSpace(desc)
	if utf8.RuneCountInString(desc) > maxDescLen {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	return desc, nil
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062）。
// 并发写入同一唯一键时，DB 唯一约束是最终兜底，不能只依赖「先查再写」。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
