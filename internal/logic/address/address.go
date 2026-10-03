// Package address 实现前台用户「收货地址」业务逻辑：增删改查、默认地址与数据隔离。
package address

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
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/address/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// maxRecipientNameLen 收货人姓名最大字符数（trim 后）。
	maxRecipientNameLen = 32
	// maxDetailLen 详细地址最大字符数（trim 后）。
	maxDetailLen = 255
	// maxRegionLen 省/市/区最大字符数（trim 后）。
	maxRegionLen = 32
	// defaultFlag DB 中 is_default=1 表示默认地址。
	defaultFlag = 1
)

// phonePattern 手机号格式：中国大陆 11 位，1 开头、第二位 3-9。
var phonePattern = regexp.MustCompile(`^1[3-9]\d{9}$`)

type sAddress struct{}

func init() {
	service.RegisterAddress(New())
}

// New 创建并返回收货地址服务实现。
func New() *sAddress {
	return &sAddress{}
}

// address 是 addresses 表的一条记录。
type address struct {
	Id            int64       `json:"id"`
	UserId        int64       `json:"user_id"`
	RecipientName string      `json:"recipient_name"`
	Phone         string      `json:"phone"`
	Province      string      `json:"province"`
	City          string      `json:"city"`
	District      string      `json:"district"`
	Detail        string      `json:"detail"`
	IsDefault     int         `json:"is_default"`
	CreatedAt     *gtime.Time `json:"created_at"`
	UpdatedAt     *gtime.Time `json:"updated_at"`
}

// Create 校验后写入地址：用户首条地址自动置默认；显式设默认时同事务先取消旧默认再置新默认。
// 默认唯一性由 DB 生成列 + uk_user_default 唯一索引兜底，并发落败方返回 7002（409）。
func (s *sAddress) Create(ctx context.Context, userID int64, req *v1.CreateReq) (*v1.CreateRes, error) {
	recipientName, err := validateRecipientName(req.RecipientName)
	if err != nil {
		return nil, err
	}
	phone, err := validatePhone(req.Phone)
	if err != nil {
		return nil, err
	}
	province, err := validateRegion(req.Province)
	if err != nil {
		return nil, err
	}
	city, err := validateRegion(req.City)
	if err != nil {
		return nil, err
	}
	district, err := validateRegion(req.District)
	if err != nil {
		return nil, err
	}
	detail, err := validateDetail(req.Detail)
	if err != nil {
		return nil, err
	}

	var created *address
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		count, e := tx.Model("addresses").Ctx(ctx).Where("user_id", userID).Count()
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("统计用户地址数量: %w", e))
		}

		isDefault := 0
		if count == 0 {
			// 首条地址自动置为默认，无论客户端是否传 is_default。
			isDefault = defaultFlag
		} else if req.IsDefault != nil && *req.IsDefault {
			isDefault = defaultFlag
		}

		if isDefault == defaultFlag {
			if e := s.clearDefault(ctx, tx, userID, 0); e != nil {
				return e
			}
		}

		id, e := tx.Model("addresses").Ctx(ctx).Data(g.Map{
			"user_id":        userID,
			"recipient_name": recipientName,
			"phone":          phone,
			"province":       province,
			"city":           city,
			"district":       district,
			"detail":         detail,
			"is_default":     isDefault,
		}).InsertAndGetId()
		if e != nil {
			if isDuplicateKeyError(e) {
				return codes.New(codes.CodeAddressDefaultConflict)
			}
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入地址: %w", e))
		}

		rec, e := findOneInTx(ctx, tx, userID, id)
		if e != nil {
			return e
		}
		if rec == nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入地址后未找到记录"))
		}
		created = rec
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &v1.CreateRes{Address: *toAddress(created)}, nil
}

// List 查询 userID 的地址列表（仅本人，按 id 升序）。
func (s *sAddress) List(ctx context.Context, userID int64) (*v1.ListRes, error) {
	var records []*address
	if err := g.DB().Model("addresses").Ctx(ctx).Where("user_id", userID).Order("id").Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询地址列表: %w", err))
	}
	items := make([]*v1.Address, 0, len(records))
	for _, r := range records {
		items = append(items, toAddress(r))
	}
	return &v1.ListRes{Items: items}, nil
}

// Detail 查询 userID 的单个地址详情；不存在或非本人统一返回 7001（404），不泄露存在性/归属。
func (s *sAddress) Detail(ctx context.Context, userID, id int64) (*v1.DetailRes, error) {
	rec, err := findOne(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, codes.New(codes.CodeAddressNotFound)
	}
	return &v1.DetailRes{Address: *toAddress(rec)}, nil
}

// Update 更新 userID 的地址：字段按提交覆盖，is_default 允许设为默认（同事务先取消旧默认）
// 或取消默认（允许无默认）。不存在或非本人统一返回 7001（404）。
func (s *sAddress) Update(ctx context.Context, userID int64, req *v1.UpdateReq) (*v1.UpdateRes, error) {
	old, err := findOne(ctx, userID, req.Id)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, codes.New(codes.CodeAddressNotFound)
	}

	recipientName := old.RecipientName
	if req.RecipientName != nil {
		if recipientName, err = validateRecipientName(*req.RecipientName); err != nil {
			return nil, err
		}
	}
	phone := old.Phone
	if req.Phone != nil {
		if phone, err = validatePhone(*req.Phone); err != nil {
			return nil, err
		}
	}
	province := old.Province
	if req.Province != nil {
		if province, err = validateRegion(*req.Province); err != nil {
			return nil, err
		}
	}
	city := old.City
	if req.City != nil {
		if city, err = validateRegion(*req.City); err != nil {
			return nil, err
		}
	}
	district := old.District
	if req.District != nil {
		if district, err = validateRegion(*req.District); err != nil {
			return nil, err
		}
	}
	detail := old.Detail
	if req.Detail != nil {
		if detail, err = validateDetail(*req.Detail); err != nil {
			return nil, err
		}
	}
	isDefault := old.IsDefault
	if req.IsDefault != nil {
		isDefault = boolToInt(*req.IsDefault)
	}

	var updated *address
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if isDefault == defaultFlag {
			// 设默认：同事务先取消该用户其它默认地址，再置本地址为默认。
			if e := s.clearDefault(ctx, tx, userID, req.Id); e != nil {
				return e
			}
		}
		if _, e := tx.Model("addresses").Ctx(ctx).
			Where("id", req.Id).
			Where("user_id", userID).
			Data(g.Map{
				"recipient_name": recipientName,
				"phone":          phone,
				"province":       province,
				"city":           city,
				"district":       district,
				"detail":         detail,
				"is_default":     isDefault,
			}).Update(); e != nil {
			if isDuplicateKeyError(e) {
				return codes.New(codes.CodeAddressDefaultConflict)
			}
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新地址: %w", e))
		}

		// 注意：UPDATE 的 RowsAffected 在「值未变化」时返回 0，不能据此判断是否存在。
		// 存在性已在事务前 findOne 校验；这里回读用于捕获并发删除（不存在则 7001）。
		rec, e := findOneInTx(ctx, tx, userID, req.Id)
		if e != nil {
			return e
		}
		if rec == nil {
			return codes.New(codes.CodeAddressNotFound)
		}
		updated = rec
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &v1.UpdateRes{Address: *toAddress(updated)}, nil
}

// Delete 删除 userID 的地址：条件删除 + 核对 RowsAffected，0 行（不存在或非本人）统一 7001（404）。
// 删除默认地址后允许无默认，不自动提升其它地址为默认。
func (s *sAddress) Delete(ctx context.Context, userID, id int64) error {
	result, err := g.DB().Model("addresses").Ctx(ctx).Where("id", id).Where("user_id", userID).Delete()
	if err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除地址: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return codes.New(codes.CodeAddressNotFound)
	}
	return nil
}

// clearDefault 取消 userID 的旧默认地址；excludeID 非 0 时跳过该行（更新场景避免把目标地址自身取消）。
func (s *sAddress) clearDefault(ctx context.Context, tx gdb.TX, userID, excludeID int64) error {
	model := tx.Model("addresses").Ctx(ctx).Where("user_id", userID).Where("is_default", defaultFlag)
	if excludeID != 0 {
		model = model.Where("id <> ?", excludeID)
	}
	if _, err := model.Data(g.Map{"is_default": 0}).Update(); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("取消旧默认地址: %w", err))
	}
	return nil
}

// findOne 按 id 且归属 userID 查询地址，不存在返回 nil（区分「不存在」与「他人地址」不在此层）。
func findOne(ctx context.Context, userID, id int64) (*address, error) {
	var records []*address
	if err := g.DB().Model("addresses").Ctx(ctx).Where("id", id).Where("user_id", userID).Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询地址: %w", err))
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

// findOneInTx 在事务内按 id 且归属 userID 查询地址。
func findOneInTx(ctx context.Context, tx gdb.TX, userID, id int64) (*address, error) {
	var records []*address
	if err := tx.Model("addresses").Ctx(ctx).Where("id", id).Where("user_id", userID).Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("事务内按 id 查询地址: %w", err))
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

// toAddress 将内部记录转换为对外结构。
func toAddress(r *address) *v1.Address {
	return &v1.Address{
		Id:            r.Id,
		RecipientName: r.RecipientName,
		Phone:         r.Phone,
		Province:      r.Province,
		City:          r.City,
		District:      r.District,
		Detail:        r.Detail,
		IsDefault:     r.IsDefault == defaultFlag,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

func boolToInt(b bool) int {
	if b {
		return defaultFlag
	}
	return 0
}

func validateRecipientName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	if utf8.RuneCountInString(s) > maxRecipientNameLen {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	return s, nil
}

func validatePhone(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !phonePattern.MatchString(s) {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	return s, nil
}

func validateRegion(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	if utf8.RuneCountInString(s) > maxRegionLen {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	return s, nil
}

func validateDetail(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	if utf8.RuneCountInString(s) > maxDetailLen {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	return s, nil
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062），用于默认地址并发冲突映射为 7002。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
