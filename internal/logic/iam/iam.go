package iam

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/glog"
	"golang.org/x/crypto/bcrypt"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/iam/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/auth"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

type sIam struct{}

func init() {
	service.RegisterIam(New())
}

// New 创建并返回 IAM 服务实现。
func New() *sIam {
	return &sIam{}
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9]{3,24}$`)

// dummyPasswordHash 仅用于用户不存在时执行一次等耗时的 bcrypt 比对，
// 以对齐耗时、防止通过响应时间枚举用户名。
var dummyPasswordHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("iam-dummy-password-for-timing"), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return h
}()

// userStatusDisabled / userStatusEnabled 是前台用户账号状态（users.status，对齐 admins.status）。
const (
	userStatusDisabled = 0
	userStatusEnabled  = 1
)

// userRecord 是 users 表的最小查询结果。
type userRecord struct {
	ID           int64
	Username     string
	PasswordHash string
	Status       int
	AuthEpoch    int64
}

// Register 校验 username/password → bcrypt 哈希 → 写入 users。
// 并发同名注册依赖 DB 唯一约束判定（1062 → USERNAME_EXISTS）。
func (s *sIam) Register(ctx context.Context, req *v1.RegisterReq) (*v1.RegisterRes, error) {
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

	id, err := g.DB().Model("users").Ctx(ctx).Data(g.Map{
		"username":      req.Username,
		"password_hash": string(hash),
	}).InsertAndGetId()
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeUsernameExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入用户: %w", err))
	}

	return &v1.RegisterRes{Id: id, Username: req.Username}, nil
}

// Login 按 username 查找用户 → 校验 bcrypt 哈希 → 生成 sid 写 Redis session（含元数据与索引）→ 签发含 sid 的 JWT。
// 不存在用户与密码错误统一返回 INVALID_CREDENTIALS，并对不存在用户做假哈希比对对齐耗时。
// 写 session/索引失败视为登录失败（返回 500，不签发 token），保证「返回的 token 必有有效 session」。
func (s *sIam) Login(ctx context.Context, req *v1.LoginReq, userAgent, ip string) (*v1.LoginRes, error) {
	if err := validateUsername(req.Username); err != nil {
		return nil, err
	}
	if err := validatePassword(req.Password); err != nil {
		return nil, err
	}

	user, err := findUserByUsername(ctx, req.Username)
	if err != nil {
		return nil, err
	}
	if user == nil {
		// 用户不存在也执行一次等耗时 bcrypt 比对，防止用户名枚举。
		_ = bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte(req.Password))
		return nil, codes.New(codes.CodeInvalidCredentials)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		return nil, codes.New(codes.CodeInvalidCredentials)
	}
	// 凭据正确但账号被禁用：拒绝登录（401，不做枚举区分），不签发 token、不写会话。
	if user.Status != userStatusEnabled {
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
	if err := auth.CreateSession(ctx, sid, user.ID, ttl, auth.SessionMeta{
		LoginAt:   time.Now().Unix(),
		UserAgent: userAgent,
		IP:        ip,
		AuthEpoch: user.AuthEpoch,
	}); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入会话: %w", err))
	}

	token, err := auth.Generate(ctx, auth.TypeUser, user.ID, sid)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("签发 access token: %w", err))
	}

	// 生成 refresh token（明文仅本次返回；SHA-256 哈希 + family_id 落库，绑 sid）。
	refreshPlaintext, err := auth.NewRefreshToken()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("生成 refresh token: %w", err))
	}
	familyID, err := auth.NewFamilyID()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("生成 family_id: %w", err))
	}
	refreshTTL, err := auth.RefreshTTL(ctx)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取 refresh TTL: %w", err))
	}
	if err := insertRefreshRoot(ctx, auth.HashRefreshToken(refreshPlaintext), familyID, user.ID, sid, user.AuthEpoch, refreshTTL); err != nil {
		return nil, err
	}

	return &v1.LoginRes{
		AccessToken:  token,
		RefreshToken: refreshPlaintext,
		TokenType:    "Bearer",
		ExpiresIn:    auth.ExpiresIn,
	}, nil
}

// Logout 撤销 sid 对应的会话（幂等），并联动撤销该 sid 对应 refresh family。
// Redis/MySQL 错误返回 500，不吞掉后返回成功。
func (s *sIam) Logout(ctx context.Context, sid string) (*v1.LogoutRes, error) {
	if err := auth.RevokeSession(ctx, sid); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("撤销会话: %w", err))
	}
	if err := revokeFamilyBySid(ctx, sid); err != nil {
		return nil, err
	}
	return &v1.LogoutRes{}, nil
}

// Me 按已认证的 userID 查询用户，返回 id + username。
// 查询不到用户时按 401 处理，不泄露用户存在性。
func (s *sIam) Me(ctx context.Context, userID int64) (*v1.MeRes, error) {
	user, err := findUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, codes.New(codes.CodeUnauthorized)
	}
	return &v1.MeRes{Id: user.ID, Username: user.Username}, nil
}

// ListSessions 返回 userID 的全部有效会话（按登录时间升序），并标识 currentSid 为当前会话。
// 以索引枚举后逐条校验 Session Hash（权威事实）：存在、未撤销且 user_id == 当前用户，
// 逐条过滤不返回任何他人/已撤销/已过期会话；ZSET 中 Hash 已过期/不存在的 sid 视为 stale member 并清理。
// Redis 枚举/读取失败 → 500，绝不返回空列表（防 fail-open）。
func (s *sIam) ListSessions(ctx context.Context, userID int64, currentSid string) (*v1.ListSessionsRes, error) {
	sids, err := auth.ListSessionIndex(ctx, userID)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取会话索引: %w", err))
	}

	items := make([]*v1.Session, 0, len(sids))
	for _, sid := range sids {
		info, err := auth.GetSession(ctx, sid)
		if err != nil {
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询会话: %w", err))
		}
		if info == nil {
			// 索引残留（对应 Hash 已过期/不存在），视为 stale member 并清理；清理失败不影响列表正确性。
			if err := auth.RemoveSessionFromIndex(ctx, userID, sid); err != nil {
				glog.Warningf(ctx, "清理会话索引残留成员失败: %v", err)
			}
			continue
		}
		if info.Revoked {
			continue // 已撤销会话不返回。
		}
		if info.UserID != userID {
			continue // 防御：不属于当前用户的会话绝不返回（INV-001）。
		}
		items = append(items, &v1.Session{
			Sid:       sid,
			LoginAt:   info.LoginAt,
			UserAgent: info.UserAgent,
			IP:        info.IP,
			Current:   sid == currentSid,
		})
	}

	// 按登录时间升序（索引本身按 score 升序，此处以权威 Hash 的 login_at 对齐）。
	sort.Slice(items, func(i, j int) bool { return items[i].LoginAt < items[j].LoginAt })
	return &v1.ListSessionsRes{Items: items}, nil
}

// RevokeSessionByID 撤销 userID 名下指定的一个会话。
// 写前校验归属（Session Hash user_id == 当前用户）；本人 active/已 revoked → 幂等 200；
// 不存在或非本人 → 404/2011，无任何写入，不泄露会话存在性与归属。
func (s *sIam) RevokeSessionByID(ctx context.Context, userID int64, targetSid string) error {
	belongs, err := auth.SessionBelongsTo(ctx, targetSid, userID)
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("校验目标会话归属: %w", err))
	}
	if !belongs {
		return codes.New(codes.CodeSessionNotFound)
	}
	if err := auth.RevokeSession(ctx, targetSid); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("撤销会话: %w", err))
	}
	// 联动撤销该 sid 对应 refresh family。
	if err := revokeFamilyBySid(ctx, targetSid); err != nil {
		return err
	}
	return nil
}

// RevokeOtherSessions 撤销 userID 除 currentSid 外的全部会话（原子，Lua），并联动撤销各被撤销 sid 的 refresh family。
func (s *sIam) RevokeOtherSessions(ctx context.Context, userID int64, currentSid string) error {
	if err := auth.RevokeOtherSessions(ctx, userID, currentSid); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("撤销其他会话: %w", err))
	}
	if err := revokeFamiliesExceptSid(ctx, userID, currentSid); err != nil {
		return err
	}
	return nil
}

// RevokeAllSessions 撤销 userID 的全部会话（含当前，原子，Lua），并联动撤销该用户全部 refresh family。
func (s *sIam) RevokeAllSessions(ctx context.Context, userID int64) error {
	if err := auth.RevokeAllSessions(ctx, userID); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("撤销全部会话: %w", err))
	}
	if err := revokeAllFamiliesByUser(ctx, userID); err != nil {
		return err
	}
	return nil
}

func validateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return codes.New(codes.CodeInvalidArgument)
	}
	return nil
}

func validatePassword(password string) error {
	n := len(password)
	if n < 8 || n > 24 {
		return codes.New(codes.CodeInvalidArgument)
	}
	return nil
}

func findUserByUsername(ctx context.Context, username string) (*userRecord, error) {
	record, err := g.DB().Model("users").Ctx(ctx).
		Fields("id", "username", "password_hash", "status", "auth_epoch").
		Where("username", username).
		One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按用户名查询用户: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return nil, nil
	}
	return &userRecord{
		ID:           record["id"].Int64(),
		Username:     record["username"].String(),
		PasswordHash: record["password_hash"].String(),
		Status:       record["status"].Int(),
		AuthEpoch:    record["auth_epoch"].Int64(),
	}, nil
}

// findUserAuth 按 id 查询前台用户的状态与认证版本（refresh 校验账号状态/版本用），不存在返回 nil。
func findUserAuth(ctx context.Context, id int64) (*userRecord, error) {
	record, err := g.DB().Model("users").Ctx(ctx).
		Fields("id", "status", "auth_epoch").
		Where("id", id).
		One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询用户状态: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return nil, nil
	}
	return &userRecord{
		ID:        record["id"].Int64(),
		Status:    record["status"].Int(),
		AuthEpoch: record["auth_epoch"].Int64(),
	}, nil
}

func findUserByID(ctx context.Context, id int64) (*userRecord, error) {
	record, err := g.DB().Model("users").Ctx(ctx).
		Fields("id", "username").
		Where("id", id).
		One()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询用户: %w", err))
	}
	if record == nil || record.IsEmpty() {
		return nil, nil
	}
	return &userRecord{
		ID:       record["id"].Int64(),
		Username: record["username"].String(),
	}, nil
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062）。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1062
	}
	return false
}
