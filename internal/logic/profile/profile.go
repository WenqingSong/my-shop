// Package profile 实现当前登录用户资料（昵称/头像/手机号/生日）的读写。
// 资料字段位于 users 表；本模块不读取也不返回任何认证/敏感字段。
package profile

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/profile/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// maxNicknameLen 昵称最大字符数（trim 后）。
	maxNicknameLen = 32
	// maxAvatarURLLen 头像 URL 最大字符数（trim 后）。
	maxAvatarURLLen = 512
	// birthdayLayout 生日日期语义：YYYY-MM-DD（DATE 列，无时间与时区）。
	birthdayLayout = "2006-01-02"
)

// phonePattern 手机号格式：中国大陆 11 位，1 开头、第二位 3-9。
var phonePattern = regexp.MustCompile(`^1[3-9]\d{9}$`)

type sProfile struct{}

func init() {
	service.RegisterProfile(New())
}

// New 创建并返回用户资料服务实现。
func New() *sProfile {
	return &sProfile{}
}

// profileRecord 是 users 表资料列的最小查询结果（刻意不含 password_hash/auth_epoch）。
type profileRecord struct {
	Id        int64       `json:"id"`
	Username  string      `json:"username"`
	Nickname  string      `json:"nickname"`
	AvatarUrl string      `json:"avatar_url"`
	Phone     *string     `json:"phone"`
	Birthday  *gtime.Time `json:"birthday"`
}

// Get 查询 userID 的资料。用户不存在返回 2015（404）。
func (s *sProfile) Get(ctx context.Context, userID int64) (*v1.GetRes, error) {
	rec, err := findProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, codes.New(codes.CodeUserNotFound)
	}
	return &v1.GetRes{Profile: *toProfile(rec)}, nil
}

// Update 更新 userID 的资料。字段为 nil 表示未提供（不修改），空串表示主动清空：
// nickname/avatar_url 清为空串，phone/birthday 清为 NULL。
// 手机号唯一性由 users.uk_phone 在 DB 层兜底，并发写入重复手机号命中 1062 → 2016（409）。
func (s *sProfile) Update(ctx context.Context, userID int64, req *v1.UpdateReq) (*v1.UpdateRes, error) {
	existing, err := findProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, codes.New(codes.CodeUserNotFound)
	}

	data := g.Map{}
	if req.Nickname != nil {
		nickname := strings.TrimSpace(*req.Nickname)
		if utf8.RuneCountInString(nickname) > maxNicknameLen {
			return nil, codes.New(codes.CodeInvalidArgument)
		}
		data["nickname"] = nickname
	}
	if req.AvatarUrl != nil {
		avatarURL := strings.TrimSpace(*req.AvatarUrl)
		if avatarURL != "" {
			if len(avatarURL) > maxAvatarURLLen || !isHTTPURL(avatarURL) {
				return nil, codes.New(codes.CodeInvalidArgument)
			}
		}
		data["avatar_url"] = avatarURL
	}
	if req.Phone != nil {
		phone := strings.TrimSpace(*req.Phone)
		if phone == "" {
			// 未设置一律写 NULL：uk_phone 允许多个 NULL，但第二个空串会撞唯一键。
			data["phone"] = nil
		} else {
			if !phonePattern.MatchString(phone) {
				return nil, codes.New(codes.CodeInvalidArgument)
			}
			data["phone"] = phone
		}
	}
	if req.Birthday != nil {
		birthday := strings.TrimSpace(*req.Birthday)
		if birthday == "" {
			data["birthday"] = nil
		} else {
			t, err := parseBirthday(birthday)
			if err != nil {
				return nil, codes.New(codes.CodeInvalidArgument)
			}
			data["birthday"] = t.Format(birthdayLayout)
		}
	}

	// 全部字段省略：不产生写操作，直接返回当前资料（幂等）。
	if len(data) > 0 {
		if _, err := g.DB().Model("users").Ctx(ctx).Where("id", userID).Data(data).Update(); err != nil {
			if isDuplicateKeyError(err) {
				return nil, codes.New(codes.CodePhoneExists)
			}
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新用户资料: %w", err))
		}
	}

	rec, err := findProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		// 事务外回读时被并发删除（当前无删除接口，纯防御性）。
		return nil, codes.New(codes.CodeUserNotFound)
	}
	return &v1.UpdateRes{Profile: *toProfile(rec)}, nil
}

// findProfile 按 id 查询资料列，不存在返回 nil。
func findProfile(ctx context.Context, userID int64) (*profileRecord, error) {
	var records []*profileRecord
	if err := g.DB().Model("users").Ctx(ctx).
		Fields("id", "username", "nickname", "avatar_url", "phone", "birthday").
		Where("id", userID).
		Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询用户资料: %w", err))
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

// toProfile 将内部记录转换为对外结构；生日统一输出 YYYY-MM-DD。
func toProfile(r *profileRecord) *v1.Profile {
	p := &v1.Profile{
		Id:        r.Id,
		Username:  r.Username,
		Nickname:  r.Nickname,
		AvatarUrl: r.AvatarUrl,
		Phone:     r.Phone,
	}
	if r.Birthday != nil {
		// gtime.Format 使用 gf 自定义占位符（Y-m-d），日期语义统一走标准库 layout。
		formatted := r.Birthday.Time.Format(birthdayLayout)
		p.Birthday = &formatted
	}
	return p
}

// parseBirthday 严格按 YYYY-MM-DD 解析：拒绝 2026-2-3（未补零）与 2026-02-30（溢出被规范化）等非法值。
func parseBirthday(s string) (time.Time, error) {
	t, err := time.Parse(birthdayLayout, s)
	if err != nil {
		return time.Time{}, err
	}
	if t.Format(birthdayLayout) != s {
		return time.Time{}, fmt.Errorf("非法日期: %s", s)
	}
	return t, nil
}

// isHTTPURL 校验头像地址为 http/https 的绝对 URL（拒绝 javascript:/data: 等协议）。
func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062）。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
