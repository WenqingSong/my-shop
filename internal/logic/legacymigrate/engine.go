package legacymigrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	go_mysql "github.com/go-sql-driver/mysql"
)

// SourceName 是迁移来源标识，写入 legacy_id_map.source。
const SourceName = "shopping.sql"

// Engine 执行历史数据迁移：从 staging 库（source）读历史表，写入目标库（target）。
// source 与 target 必须指向不同的数据库 schema（staging vs 目标），目标库绝不执行
// SET FOREIGN_KEY_CHECKS=0（INV-004）。
type Engine struct {
	source *sql.DB
	target *sql.DB
}

// NewEngine 构造迁移引擎。
func NewEngine(source, target *sql.DB) *Engine {
	return &Engine{source: source, target: target}
}

// Run 按依赖顺序执行全部迁移域。dryRun=true 时仅解析/映射/校验并统计将执行的变更，不写目标库。
//
// 数据导入顺序（代码可并行开发，但运行必须遵守依赖）：
// 地区 → 分类/商品/SKU/库存 → 用户 → 地址 → 优惠券 → 订单/明细/状态流水
// → 钱包 → 积分 → 评价 → 收藏。
func (e *Engine) Run(ctx context.Context, dryRun bool) (*Result, error) {
	result := &Result{DryRun: dryRun}

	ids, err := loadIDMap(ctx, e.target)
	if err != nil {
		return nil, err
	}

	dc := &domainContext{
		source: e.source,
		target: e.target,
		dryRun: dryRun,
		ids:    ids,
		result: result,
	}

	for _, runner := range domainRunners() {
		if err := runner.run(ctx, dc); err != nil {
			return nil, fmt.Errorf("迁移域 %s 失败: %w", runner.name(), err)
		}
	}

	result.finalize()
	return result, nil
}

// domainRunners 返回固定执行顺序的迁移域列表。
func domainRunners() []domainRunner {
	return []domainRunner{
		regionDomain{},
		productDomain{},
		userDomain{},
		addressDomain{},
		couponDomain{},
		orderDomain{},
		walletDomain{},
		pointsDomain{},
		reviewDomain{},
		favoriteDomain{},
	}
}

// idMap 是 legacy_id_map 的进程内视图；键为 entity_type + ':' + old_id。
type idMap struct {
	m map[string]int64
}

// entityKey 构造幂等映射键（source 固定，故只需 entity_type + old_id）。
func entityKey(entityType string, oldID int64) string {
	return entityType + ":" + strconv.FormatInt(oldID, 10)
}

func (im *idMap) get(entityType string, oldID int64) (int64, bool) {
	v, ok := im.m[entityKey(entityType, oldID)]
	return v, ok
}

func (im *idMap) put(entityType string, oldID, newID int64) {
	im.m[entityKey(entityType, oldID)] = newID
}

// loadIDMap 读取目标库 legacy_id_map 中本来源的全部映射。
func loadIDMap(ctx context.Context, target *sql.DB) (*idMap, error) {
	rows, err := target.QueryContext(ctx,
		"SELECT entity_type, old_id, new_id FROM legacy_id_map WHERE source = ?", SourceName)
	if err != nil {
		if isTableNotExist(err) {
			return nil, fmt.Errorf("目标库缺少 legacy_id_map 表，请先执行 `my-shop migrate up`")
		}
		return nil, fmt.Errorf("读取 legacy_id_map: %w", err)
	}
	defer rows.Close()

	im := &idMap{m: make(map[string]int64)}
	for rows.Next() {
		var entityType string
		var oldID, newID int64
		if err := rows.Scan(&entityType, &oldID, &newID); err != nil {
			return nil, fmt.Errorf("扫描 legacy_id_map: %w", err)
		}
		im.put(entityType, oldID, newID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 legacy_id_map: %w", err)
	}
	return im, nil
}

// isTableNotExist 判断是否为 MySQL「表不存在」错误（1146）。
func isTableNotExist(err error) bool {
	var mysqlErr *go_mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1146
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062），用于逐条跳过。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *go_mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

// isForeignKeyError 判断是否为 MySQL 外键约束失败（1451 父行被引用 / 1452 子行引用缺失）。
func isForeignKeyError(err error) bool {
	var mysqlErr *go_mysql.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	return mysqlErr.Number == 1451 || mysqlErr.Number == 1452
}
