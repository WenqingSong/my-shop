package iam

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/frame/g"
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

// userRecord 是 users 表的最小查询结果。
type userRecord struct {
	ID           int64
	Username     string
	PasswordHash string
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

// Login 按 username 查找用户 → 校验 bcrypt 哈希 → 生成 sid 写 Redis session → 签发含 sid 的 JWT。
// 不存在用户与密码错误统一返回 INVALID_CREDENTIALS，并对不存在用户做假哈希比对对齐耗时。
// 写 session 失败视为登录失败（返回 500，不签发 token），保证「返回的 token 必有有效 session」。
func (s *sIam) Login(ctx context.Context, req *v1.LoginReq) (*v1.LoginRes, error) {
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

	sid, err := auth.NewSid()
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("生成 sid: %w", err))
	}
	ttl, err := auth.SessionTTL(ctx)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("读取会话 TTL: %w", err))
	}
	if err := auth.CreateSession(ctx, sid, user.ID, ttl); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入会话: %w", err))
	}

	token, err := auth.Generate(ctx, user.ID, sid)
	if err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("签发 access token: %w", err))
	}

	return &v1.LoginRes{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   auth.ExpiresIn,
	}, nil
}

// Logout 撤销 sid 对应的会话（幂等）。Redis 错误返回 500，不吞掉后返回成功。
func (s *sIam) Logout(ctx context.Context, sid string) (*v1.LogoutRes, error) {
	if err := auth.RevokeSession(ctx, sid); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("撤销会话: %w", err))
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
		Fields("id", "username", "password_hash").
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
