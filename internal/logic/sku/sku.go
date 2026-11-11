package sku

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/sku/v1"
	specv1 "cnb.cool/go-cloud-devops/my-shop/api/spec/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// maxNameLen SKU 名称最大字符数（trim 后）。
	maxNameLen = 128
	// maxImageLen SKU 规格图 URL 最大字符数（与 skus.image_url 一致）。
	maxImageLen = 512
	// maxPrice 价格上限（整数分，= ¥999,999.99）。
	maxPrice = 99_999_999
	// maxSkuCodeRetry 随机 sku_code 撞唯一键时的重试次数（概率极低，仅防御）。
	maxSkuCodeRetry = 3

	// statusEnabled / statusDisabled SKU 启用/禁用状态（DB 存 TINYINT）。
	statusEnabled  = 1
	statusDisabled = 0
)

type sSku struct{}

func init() {
	service.RegisterSku(New())
}

// New 创建并返回 SKU 服务实现。
func New() *sSku {
	return &sSku{}
}

// sku 是 skus 表的一条记录。
type sku struct {
	Id            int64       `json:"id"`
	SkuCode       *string     `json:"sku_code"`
	ProductId     int64       `json:"product_id"`
	Name          string      `json:"name"`
	ImageUrl      string      `json:"image_url"`
	Price         int64       `json:"price"`
	OriginalPrice int64       `json:"original_price"`
	Status        int         `json:"status"`
	SpecSignature *string     `json:"spec_signature"`
	CreatedAt     *gtime.Time `json:"created_at"`
	UpdatedAt     *gtime.Time `json:"updated_at"`
}

// skuSpecRow 是 sku_spec_values 联查规格组/规格值的结果行。
type skuSpecRow struct {
	SkuId       int64  `orm:"sku_id"`
	SpecId      int64  `orm:"spec_id"`
	SpecName    string `orm:"spec_name"`
	SpecSort    int    `orm:"spec_sort"`
	SpecValueId int64  `orm:"spec_value_id"`
	Value       string `orm:"value"`
}

// Create 校验后写入 SKU：product_id 必须指向存在商品（否则 4001），name 同商品唯一由
// 复合唯一约束兜底（撞名 5004），price 为整数分，status 默认 enabled。
// sku_code 由服务端生成（唯一约束兜底）；original_price 缺省等于售价。
func (s *sSku) Create(ctx context.Context, req *v1.CreateReq) (*v1.CreateRes, error) {
	name, err := validateName(req.Name)
	if err != nil {
		return nil, err
	}
	imageURL, err := validateImageURL(req.ImageUrl)
	if err != nil {
		return nil, err
	}
	price, err := parsePrice(req.Price)
	if err != nil {
		return nil, err
	}
	originalPrice, err := parseOriginalPrice(req.OriginalPrice, price)
	if err != nil {
		return nil, err
	}
	status := statusEnabled
	if req.Status != nil {
		if status, err = parseStatus(*req.Status); err != nil {
			return nil, err
		}
	}

	exists, err := service.Product().Exists(ctx, req.ProductId)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, codes.New(codes.CodeProductNotFound)
	}

	data := g.Map{
		"product_id":     req.ProductId,
		"name":           name,
		"image_url":      imageURL,
		"price":          price,
		"original_price": originalPrice,
		"status":         status,
	}
	id, err := insertSkuWithCode(ctx, g.DB().Model("skus").Ctx(ctx), data, req.ProductId)
	if err != nil {
		if duplicateKeyContains(err, "uk_sku_code") {
			return nil, codes.New(codes.CodeSkuCodeExists)
		}
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeSkuNameExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入 SKU: %w", err))
	}

	return s.loadCreateRes(ctx, id)
}

// Update 按提交字段更新 SKU 的 name/image_url/price/original_price/status（product_id 与 sku_code 不可变）；
// 不存在返回 5001（404），撞名返回 5004（409），非法值在写入前拒绝且不改变原值。
func (s *sSku) Update(ctx context.Context, req *v1.UpdateReq) (*v1.UpdateRes, error) {
	old, err := s.findOne(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, codes.New(codes.CodeSkuNotFound)
	}

	name := old.Name
	if req.Name != nil {
		if name, err = validateName(*req.Name); err != nil {
			return nil, err
		}
	}
	imageURL := old.ImageUrl
	if req.ImageUrl != nil {
		if imageURL, err = validateImageURL(*req.ImageUrl); err != nil {
			return nil, err
		}
	}
	price := old.Price
	if req.Price != nil {
		if price, err = parsePrice(*req.Price); err != nil {
			return nil, err
		}
	}
	originalPrice := old.OriginalPrice
	if req.OriginalPrice != nil {
		if originalPrice, err = parsePrice(*req.OriginalPrice); err != nil {
			return nil, err
		}
	}
	status := old.Status
	if req.Status != nil {
		if status, err = parseStatus(*req.Status); err != nil {
			return nil, err
		}
	}

	if _, err := g.DB().Model("skus").Ctx(ctx).
		Where("id", req.Id).
		Data(g.Map{
			"name":           name,
			"image_url":      imageURL,
			"price":          price,
			"original_price": originalPrice,
			"status":         status,
		}).Update(); err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeSkuNameExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新 SKU: %w", err))
	}

	return s.loadUpdateRes(ctx, req.Id)
}

// Delete 物理删除 SKU：不存在/已删除返回 5001（404）；存在库存记录/流水时返回 5005（409，
// 由 FK ON DELETE RESTRICT 兜底）；核对 RowsAffected 兜底并发删除。删除不级联、不影响所属商品与同商品其他 SKU。
// 组合生成流程不调用本方法，因此重新生成组合不会删除已被购物车/订单引用的 SKU。
func (s *sSku) Delete(ctx context.Context, id int64) error {
	rec, err := s.findOne(ctx, id)
	if err != nil {
		return err
	}
	if rec == nil {
		return codes.New(codes.CodeSkuNotFound)
	}

	result, err := g.DB().Model("skus").Ctx(ctx).Where("id", id).Delete()
	if err != nil {
		if isForeignKeyError(err) {
			return codes.New(codes.CodeSkuHasInventory)
		}
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("删除 SKU: %w", err))
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return codes.New(codes.CodeSkuNotFound)
	}
	return nil
}

// GetByID 按 id 查询单个 SKU 全字段（含 status、规格取值），不存在返回 nil。
func (s *sSku) GetByID(ctx context.Context, id int64) (*v1.Sku, error) {
	rec, err := s.findOne(ctx, id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}
	specs, err := s.loadSpecValues(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	return toSku(rec, specs[id]), nil
}

// GetByCode 按 sku_code 查询单个 SKU（含规格取值），不存在或编码为空返回 nil。
// 下单链路按该编码定位 SKU，再以 skus.price 作为唯一权威定价。
func (s *sSku) GetByCode(ctx context.Context, code string) (*v1.Sku, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, nil
	}
	var records []*sku
	if err := g.DB().Model("skus").Ctx(ctx).Where("sku_code", code).Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按编码查询 SKU: %w", err))
	}
	if len(records) == 0 {
		return nil, nil
	}
	specs, err := s.loadSpecValues(ctx, []int64{records[0].Id})
	if err != nil {
		return nil, err
	}
	return toSku(records[0], specs[records[0].Id]), nil
}

// Exists 判断 SKU 是否存在（供库存校验 sku_id 存在性）。
func (s *sSku) Exists(ctx context.Context, id int64) (bool, error) {
	n, err := g.DB().Model("skus").Ctx(ctx).Where("id", id).Count()
	if err != nil {
		return false, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询 SKU 存在性: %w", err))
	}
	return n > 0, nil
}

// ListByProduct 按商品查询 SKU（供商品详情组合），按 id 升序；
// onlyEnabled=true 时仅返回 enabled SKU（前台可见性）。批量加载规格取值，无 N+1。
func (s *sSku) ListByProduct(ctx context.Context, productID int64, onlyEnabled bool) ([]*v1.Sku, error) {
	model := g.DB().Model("skus").Ctx(ctx).Where("product_id", productID)
	if onlyEnabled {
		model = model.Where("status", statusEnabled)
	}
	var records []*sku
	if err := model.Order("id").Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询商品 SKU: %w", err))
	}
	ids := make([]int64, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.Id)
	}
	specs, err := s.loadSpecValues(ctx, ids)
	if err != nil {
		return nil, err
	}
	items := make([]*v1.Sku, 0, len(records))
	for _, r := range records {
		items = append(items, toSku(r, specs[r.Id]))
	}
	return items, nil
}

// Generate 按商品规格组 × 规格值生成笛卡尔积组合并幂等落库：
//   - 组合由服务端生成，不接受客户端枚举；
//   - spec_key 由规格值按 spec_id 升序拼 "spec_id:spec_value_id" 后取 MD5 得到，
//     同一商品下唯一（uk_product_signature 兜底并发），不同商品互不影响；
//   - 已存在的组合保留原 SKU 与 sku_code（身份稳定），不覆盖其价格/状态/图片；
//   - 本方法不删除任何 SKU，已被购物车/订单引用的 SKU 不会因重新生成而丢失；
//   - 库存不在 SKU 表重复建立，新 SKU 库存仍由 Inventory 模块按 sku_id 维护（无库存记录 = 0）。
func (s *sSku) Generate(ctx context.Context, req *v1.GenerateReq) (*v1.GenerateRes, error) {
	product, err := service.Product().GetByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if product == nil {
		return nil, codes.New(codes.CodeProductNotFound)
	}

	groups, err := service.Spec().ListByProduct(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, codes.New(codes.CodeSkuSpecMissing)
	}
	for _, group := range groups {
		if len(group.Values) == 0 {
			return nil, codes.New(codes.CodeSkuSpecMissing)
		}
	}

	price := product.Price
	if req.Price != nil {
		if price, err = parsePrice(*req.Price); err != nil {
			return nil, err
		}
	}
	originalPrice, err := parseOriginalPrice(req.OriginalPrice, price)
	if err != nil {
		return nil, err
	}
	imageURL, err := validateImageURL(req.ImageUrl)
	if err != nil {
		return nil, err
	}
	status := statusEnabled
	if req.Status != nil {
		if status, err = parseStatus(*req.Status); err != nil {
			return nil, err
		}
	}

	combos := combinations(groups)

	var created, kept int
	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		// 事务内读取既有指纹：并发下仍可能出现两侧同时读不到旧行，唯一约束是最终防线。
		var existing []*sku
		if e := tx.Model("skus").Ctx(ctx).Fields("id", "spec_signature").
			Where("product_id", req.Id).Scan(&existing); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询既有 SKU 组合: %w", e))
		}
		known := make(map[string]bool, len(existing))
		for _, r := range existing {
			if r.SpecSignature != nil {
				known[*r.SpecSignature] = true
			}
		}

		for _, combo := range combos {
			key := specKey(combo)
			if known[key] {
				kept++
				continue
			}
			name := comboText(combo)
			id, e := tx.Model("skus").Ctx(ctx).Data(g.Map{
				"sku_code":       generatedSkuCode(req.Id, key),
				"product_id":     req.Id,
				"name":           name,
				"image_url":      imageURL,
				"price":          price,
				"original_price": originalPrice,
				"status":         status,
				"spec_signature": key,
			}).InsertAndGetId()
			if e != nil {
				if isDuplicateKeyError(e) {
					// 并发生成同一组合：唯一约束拒绝，视为已存在并保留（不改动既有 SKU）。
					if duplicateKeyContains(e, "uk_product_signature") || duplicateKeyContains(e, "uk_sku_code") {
						known[key] = true
						kept++
						continue
					}
					return codes.New(codes.CodeSkuNameExists)
				}
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入组合 SKU: %w", e))
			}
			for _, choice := range combo {
				if _, e := tx.Model("sku_spec_values").Ctx(ctx).Data(g.Map{
					"sku_id":        id,
					"spec_id":       choice.SpecId,
					"spec_value_id": choice.Id,
				}).Insert(); e != nil {
					if isDuplicateKeyError(e) {
						return codes.New(codes.CodeSkuSpecDuplicate)
					}
					return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入 SKU 规格关联: %w", e))
				}
			}
			known[key] = true
			created++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	items, err := s.ListByProduct(ctx, req.Id, false)
	if err != nil {
		return nil, err
	}
	return &v1.GenerateRes{Created: created, Kept: kept, Items: items}, nil
}

// findOne 按 id 查询 SKU，不存在返回 nil。
func (s *sSku) findOne(ctx context.Context, id int64) (*sku, error) {
	var records []*sku
	if err := g.DB().Model("skus").Ctx(ctx).Where("id", id).Scan(&records); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询 SKU: %w", err))
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

// loadSpecValues 批量加载多个 SKU 的规格取值（按规格组 sort/id、规格值 sort/id 升序）。
func (s *sSku) loadSpecValues(ctx context.Context, skuIDs []int64) (map[int64][]*v1.SkuSpecValue, error) {
	result := make(map[int64][]*v1.SkuSpecValue, len(skuIDs))
	if len(skuIDs) == 0 {
		return result, nil
	}
	var rows []*skuSpecRow
	if err := g.DB().Ctx(ctx).Model("sku_spec_values ssv").
		Fields("ssv.sku_id", "ssv.spec_id", "ps.name AS spec_name", "ps.sort AS spec_sort",
			"ssv.spec_value_id", "psv.value").
		LeftJoin("product_specs ps", "ps.id = ssv.spec_id").
		LeftJoin("product_spec_values psv", "psv.id = ssv.spec_value_id").
		WhereIn("ssv.sku_id", skuIDs).
		Order("ps.sort", "ps.id", "psv.sort", "psv.id").
		Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询 SKU 规格取值: %w", err))
	}
	for _, r := range rows {
		result[r.SkuId] = append(result[r.SkuId], &v1.SkuSpecValue{
			SpecId:      r.SpecId,
			SpecName:    r.SpecName,
			SpecValueId: r.SpecValueId,
			Value:       r.Value,
			Sort:        r.SpecSort,
		})
	}
	return result, nil
}

// loadCreateRes 按 id 重新加载 SKU 并包装为创建响应。
func (s *sSku) loadCreateRes(ctx context.Context, id int64) (*v1.CreateRes, error) {
	item, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, codes.New(codes.CodeSkuNotFound)
	}
	return &v1.CreateRes{Sku: *item}, nil
}

// loadUpdateRes 按 id 重新加载 SKU 并包装为更新响应。
func (s *sSku) loadUpdateRes(ctx context.Context, id int64) (*v1.UpdateRes, error) {
	item, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, codes.New(codes.CodeSkuNotFound)
	}
	return &v1.UpdateRes{Sku: *item}, nil
}

// toSku 将内部记录转换为对外 SKU 结构。
func toSku(r *sku, specs []*v1.SkuSpecValue) *v1.Sku {
	if specs == nil {
		specs = []*v1.SkuSpecValue{}
	}
	code := ""
	if r.SkuCode != nil {
		code = *r.SkuCode
	}
	key := ""
	if r.SpecSignature != nil {
		key = *r.SpecSignature
	}
	return &v1.Sku{
		Id:            r.Id,
		SkuCode:       code,
		ProductId:     r.ProductId,
		Name:          r.Name,
		ImageUrl:      r.ImageUrl,
		Price:         r.Price,
		OriginalPrice: r.OriginalPrice,
		Status:        statusToString(r.Status),
		SpecKey:       key,
		SpecValues:    specs,
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

// insertSkuWithCode 以服务端生成的 sku_code 插入 SKU；随机编码撞唯一键时重试（上限 maxSkuCodeRetry）。
// model 允许传入事务内的 Model，保证组合生成与手动创建复用同一插入语义。
func insertSkuWithCode(ctx context.Context, model *gdb.Model, data g.Map, productID int64) (int64, error) {
	var lastErr error
	for attempt := 0; attempt < maxSkuCodeRetry; attempt++ {
		data["sku_code"] = randomSkuCode(productID)
		id, err := model.Data(data).InsertAndGetId()
		if err == nil {
			return id, nil
		}
		lastErr = err
		if isDuplicateKeyError(err) && duplicateKeyContains(err, "uk_sku_code") {
			continue
		}
		return 0, err
	}
	return 0, lastErr
}

// randomSkuCode 生成手动创建 SKU 的编码：SKU-P<商品 id>-<16 hex>，由唯一约束兜底去重。
func randomSkuCode(productID int64) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("SKU-P%d-%s", productID, hex.EncodeToString(b[:]))
}

// generatedSkuCode 由商品 id + 规格组合指纹派生编码：同一组合始终得到同一编码，
// 保证「重新生成组合不会更换既有 SKU 编码」，且不同组合不会共用编码。
func generatedSkuCode(productID int64, key string) string {
	return fmt.Sprintf("SKU-P%d-%s", productID, strings.ToUpper(key[:12]))
}

// specKey 计算规格组合指纹：按 spec_id 升序拼 "<spec_id>:<spec_value_id>"，以 "," 连接后取 MD5，
// 输出 32 位小写 hex（与 docs/design/sku.md 及历史迁移数据一致）。
func specKey(combo []*specv1.SpecValue) string {
	sorted := make([]*specv1.SpecValue, len(combo))
	copy(sorted, combo)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].SpecId < sorted[j].SpecId })
	parts := make([]string, 0, len(sorted))
	for _, v := range sorted {
		parts = append(parts, fmt.Sprintf("%d:%d", v.SpecId, v.Id))
	}
	sum := md5.Sum([]byte(strings.Join(parts, ",")))
	return hex.EncodeToString(sum[:])
}

// comboText 将一组规格值按规格组顺序拼成展示文本（逗号分隔），用于新 SKU 名称。
func comboText(combo []*specv1.SpecValue) string {
	values := make([]string, 0, len(combo))
	for _, v := range combo {
		values = append(values, v.Value)
	}
	return strings.Join(values, ",")
}

// combinations 生成规格组 × 规格值的笛卡尔积；groups 顺序即规格组 sort 顺序。
func combinations(groups []*specv1.SpecGroup) [][]*specv1.SpecValue {
	result := make([][]*specv1.SpecValue, 0)
	var walk func(idx int, current []*specv1.SpecValue)
	walk = func(idx int, current []*specv1.SpecValue) {
		if idx == len(groups) {
			combo := make([]*specv1.SpecValue, len(current))
			copy(combo, current)
			result = append(result, combo)
			return
		}
		for _, value := range groups[idx].Values {
			walk(idx+1, append(current, value))
		}
	}
	walk(0, nil)
	return result
}

// statusToString 将 DB 状态映射为 API 字符串枚举。
func statusToString(s int) string {
	if s == statusEnabled {
		return v1.StatusEnabled
	}
	return v1.StatusDisabled
}

// parseStatus 将 API 字符串状态枚举解析为 DB TINYINT，非法值返回 5003。
func parseStatus(status string) (int, error) {
	switch status {
	case v1.StatusEnabled:
		return statusEnabled, nil
	case v1.StatusDisabled:
		return statusDisabled, nil
	default:
		return 0, codes.New(codes.CodeSkuInvalidStatus)
	}
}

// parsePrice 校验价格为合法整数分（整数、非负、不超上限），非法返回 5002。
func parsePrice(n json.Number) (int64, error) {
	v, err := n.Int64()
	if err != nil {
		return 0, codes.New(codes.CodeSkuInvalidPrice)
	}
	if v < 0 || v > maxPrice {
		return 0, codes.New(codes.CodeSkuInvalidPrice)
	}
	return v, nil
}

// parseOriginalPrice 解析原价：缺省回退售价；显式提交则按整数分校验。
func parseOriginalPrice(n *json.Number, price int64) (int64, error) {
	if n == nil {
		return price, nil
	}
	return parsePrice(*n)
}

// validateName trim 后校验非空与长度，返回规范化后的 SKU 名称。
func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	if utf8.RuneCountInString(name) > maxNameLen {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	return name, nil
}

// validateImageURL trim 后校验规格图 URL 长度。
func validateImageURL(url string) (string, error) {
	url = strings.TrimSpace(url)
	if utf8.RuneCountInString(url) > maxImageLen {
		return "", codes.New(codes.CodeInvalidArgument)
	}
	return url, nil
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062）。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

// duplicateKeyContains 判断唯一约束冲突的键名是否包含指定索引名（MySQL 错误信息含键名）。
func duplicateKeyContains(err error, index string) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 && strings.Contains(mysqlErr.Message, index)
}

// isForeignKeyError 判断是否为 MySQL 外键约束失败（1451，ON DELETE RESTRICT）。
// SKU 删除的兜底：已存在库存记录/流水时 DELETE 命中 1451，映射为「SKU 有库存」而非 500。
func isForeignKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1451
}
