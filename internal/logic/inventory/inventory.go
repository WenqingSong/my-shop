// Package inventory 实现「普通库存」业务逻辑：查询、增加/初始化、条件扣减与流水。
package inventory

import (
	"context"
	"fmt"

	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/inventory/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// 变更类型（DB TINYINT）。
	changeTypeIncrease = 1
	changeTypeDeduct   = 2
)

type sInventory struct{}

func init() {
	service.RegisterInventory(New())
}

// New 创建并返回库存服务实现。
func New() *sInventory {
	return &sInventory{}
}

// inventory 是 inventories 表的一条记录。
type inventory struct {
	Id        int64       `json:"id"`
	SkuId     int64       `json:"sku_id"`
	Quantity  int64       `json:"quantity"`
	CreatedAt *gtime.Time `json:"created_at"`
	UpdatedAt *gtime.Time `json:"updated_at"`
}

// inventoryLog 是 inventory_logs 表的一条记录。
type inventoryLog struct {
	Id              int64       `json:"id"`
	SkuId           int64       `json:"sku_id"`
	ChangeType      int         `json:"change_type"`
	ChangeQty       int64       `json:"change_qty"`
	BeforeQty       int64       `json:"before_qty"`
	AfterQty        int64       `json:"after_qty"`
	OperatorAdminId *int64      `json:"operator_admin_id"`
	Reason          string      `json:"reason"`
	CreatedAt       *gtime.Time `json:"created_at"`
}

// Get 查询指定 SKU 的当前库存数量；SKU 不存在返回 5001（404），无库存记录视为 0。
func (s *sInventory) Get(ctx context.Context, skuID int64) (*v1.Inventory, error) {
	if err := s.ensureSkuExists(ctx, skuID); err != nil {
		return nil, err
	}
	rec, err := s.findOne(ctx, skuID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return &v1.Inventory{SkuId: skuID, Quantity: 0}, nil
	}
	return toInventory(rec), nil
}

// Increase 增加/初始化库存：事务内原子 upsert（INSERT ... ON DUPLICATE KEY UPDATE
// quantity = quantity + N），保证首次创建/并发 increase 不重复、不丢增量；
// 同一事务内读回最新库存作为 after_qty，写入一条「增加」流水。
func (s *sInventory) Increase(ctx context.Context, skuID, qty int64, operatorID *int64) (*v1.Inventory, error) {
	if qty < 1 {
		return nil, codes.New(codes.CodeInventoryInvalidQuantity)
	}
	if err := s.ensureSkuExists(ctx, skuID); err != nil {
		return nil, err
	}

	var inv *v1.Inventory
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		if _, e := tx.Ctx(ctx).Exec(
			"INSERT INTO inventories (sku_id, quantity) VALUES (?, ?) ON DUPLICATE KEY UPDATE quantity = quantity + VALUES(quantity)",
			skuID, qty,
		); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("增加库存: %w", e))
		}
		rec, e := s.findOneInTx(ctx, tx, skuID)
		if e != nil {
			return e
		}
		if rec == nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("增加库存后未找到库存记录"))
		}
		if e := s.insertLog(ctx, tx, skuID, changeTypeIncrease, qty, rec.Quantity-qty, rec.Quantity, operatorID); e != nil {
			return e
		}
		inv = toInventory(rec)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return inv, nil
}

// Deduct 条件扣减库存：事务内条件更新 UPDATE ... SET quantity = quantity - N
// WHERE sku_id = ? AND quantity >= N + 核对 RowsAffected；命中 0 行返回 6001 且不写流水。
func (s *sInventory) Deduct(ctx context.Context, skuID, qty int64, operatorID *int64) (*v1.Inventory, error) {
	if qty < 1 {
		return nil, codes.New(codes.CodeInventoryInvalidQuantity)
	}
	if err := s.ensureSkuExists(ctx, skuID); err != nil {
		return nil, err
	}

	var inv *v1.Inventory
	err := g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		result, e := tx.Ctx(ctx).Exec(
			"UPDATE inventories SET quantity = quantity - ? WHERE sku_id = ? AND quantity >= ?",
			qty, skuID, qty,
		)
		if e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("扣减库存: %w", e))
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			return codes.New(codes.CodeInventoryInsufficient)
		}
		rec, e := s.findOneInTx(ctx, tx, skuID)
		if e != nil {
			return e
		}
		if rec == nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("扣减库存后未找到库存记录"))
		}
		if e := s.insertLog(ctx, tx, skuID, changeTypeDeduct, qty, rec.Quantity+qty, rec.Quantity, operatorID); e != nil {
			return e
		}
		inv = toInventory(rec)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return inv, nil
}

// ListLogs 查询指定 SKU 的库存流水（id 倒序）；SKU 不存在返回 5001（404）。
func (s *sInventory) ListLogs(ctx context.Context, skuID int64) ([]*v1.Log, error) {
	if err := s.ensureSkuExists(ctx, skuID); err != nil {
		return nil, err
	}
	var records []*inventoryLog
	if err := g.DB().Model("inventory_logs").Ctx(ctx).Where("sku_id", skuID).Order("id DESC").Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询库存流水: %w", err))
	}
	items := make([]*v1.Log, 0, len(records))
	for _, r := range records {
		items = append(items, toLog(r))
	}
	return items, nil
}

// ensureSkuExists 校验 sku_id 指向存在的 SKU，否则返回 5001（404）且后续无写入。
func (s *sInventory) ensureSkuExists(ctx context.Context, skuID int64) error {
	exists, err := service.Sku().Exists(ctx, skuID)
	if err != nil {
		return err
	}
	if !exists {
		return codes.New(codes.CodeSkuNotFound)
	}
	return nil
}

// findOne 按 sku_id 查询库存记录，无记录返回 nil。
func (s *sInventory) findOne(ctx context.Context, skuID int64) (*inventory, error) {
	var records []*inventory
	if err := g.DB().Model("inventories").Ctx(ctx).Where("sku_id", skuID).Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 sku_id 查询库存: %w", err))
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

// findOneInTx 在事务内按 sku_id 查询库存记录（读取本次变更后的最新值）。
func (s *sInventory) findOneInTx(ctx context.Context, tx gdb.TX, skuID int64) (*inventory, error) {
	var records []*inventory
	if err := tx.Model("inventories").Ctx(ctx).Where("sku_id", skuID).Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("事务内按 sku_id 查询库存: %w", err))
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

// insertLog 在事务内写入一条库存变更流水；operatorID 为 nil 时 operator_admin_id 落 NULL。
func (s *sInventory) insertLog(ctx context.Context, tx gdb.TX, skuID int64, changeType int, changeQty, before, after int64, operatorID *int64) error {
	data := g.Map{
		"sku_id":      skuID,
		"change_type": changeType,
		"change_qty":  changeQty,
		"before_qty":  before,
		"after_qty":   after,
		"reason":      "",
	}
	if operatorID != nil {
		data["operator_admin_id"] = *operatorID
	}
	if _, err := tx.Model("inventory_logs").Ctx(ctx).Data(data).Insert(); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入库存流水: %w", err))
	}
	return nil
}

// toInventory 将内部库存记录转换为对外结构。
func toInventory(r *inventory) *v1.Inventory {
	return &v1.Inventory{
		SkuId:     r.SkuId,
		Quantity:  r.Quantity,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

// toLog 将内部流水记录转换为对外结构。
func toLog(r *inventoryLog) *v1.Log {
	return &v1.Log{
		Id:              r.Id,
		SkuId:           r.SkuId,
		ChangeType:      changeTypeToString(r.ChangeType),
		ChangeQty:       r.ChangeQty,
		BeforeQty:       r.BeforeQty,
		AfterQty:        r.AfterQty,
		OperatorAdminId: r.OperatorAdminId,
		Reason:          r.Reason,
		CreatedAt:       r.CreatedAt,
	}
}

// changeTypeToString 将 DB 变更类型映射为 API 字符串枚举。
func changeTypeToString(t int) string {
	if t == changeTypeIncrease {
		return v1.ChangeTypeIncrease
	}
	return v1.ChangeTypeDeduct
}
