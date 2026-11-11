// Package freight 实现运费模板管理（后台）与运费计算（服务端唯一入口）。
//
// 本批次只实现「按件计费 + 区域差异化 + 满额免邮」：
//   - 运费 = 首件费用 + ceil((数量-首件数量)/续件数量) × 续件费用；
//   - 区域按 regions 的 parent_id 层级由近及远匹配（区 → 市 → 省），不按名称模糊匹配；
//   - 商品未绑定模板（products.freight_template_id IS NULL）保持零运费历史兼容；
//   - 已绑定模板但模板停用/不存在/当前地区无规则时明确报错，绝不静默按 0 处理。
//
// 不实现：重量计费、体积计费、第三方快递轨迹与物流查询。
package freight

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/freight/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

const (
	// maxNameLen 模板名最大字符数（trim 后）。
	maxNameLen = 64
	// maxRemarkLen 备注最大字符数。
	maxRemarkLen = 255
	// maxRegionDepth 地区链上溯深度上限：省/市/区三级，留余量并防止脏数据 parent_id 成环。
	maxRegionDepth = 8
)

type sFreight struct{}

func init() {
	service.RegisterFreight(New())
}

// New 创建并返回运费服务实现。
func New() *sFreight {
	return &sFreight{}
}

// templateRow 是 freight_templates 表的一条记录。
type templateRow struct {
	Id            int64       `json:"id"`
	Name          string      `json:"name"`
	IsFree        int         `json:"is_free"`
	FreeThreshold int64       `json:"free_threshold"`
	Status        int         `json:"status"`
	Remark        string      `json:"remark"`
	CreatedAt     *gtime.Time `json:"created_at"`
	UpdatedAt     *gtime.Time `json:"updated_at"`
}

// ruleRow 是 freight_template_rules 表的一条记录。
type ruleRow struct {
	Id                 int64       `json:"id"`
	TemplateId         int64       `json:"template_id"`
	RegionId           int64       `json:"region_id"`
	FirstQuantity      int64       `json:"first_quantity"`
	FirstFee           int64       `json:"first_fee"`
	AdditionalQuantity int64       `json:"additional_quantity"`
	AdditionalFee      int64       `json:"additional_fee"`
	FreeThreshold      int64       `json:"free_threshold"`
	CreatedAt          *gtime.Time `json:"created_at"`
	UpdatedAt          *gtime.Time `json:"updated_at"`
}

// regionRow 是 regions 表参与层级上溯的最小列集合。
type regionRow struct {
	Id       int64 `json:"id"`
	ParentId int64 `json:"parent_id"`
}

// AdminCreate 创建运费模板：校验名称/包邮标记/免邮阈值/备注后落库；重名返回 20002。
func (s *sFreight) AdminCreate(ctx context.Context, req *v1.AdminCreateReq) (*v1.AdminCreateRes, error) {
	if req == nil {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	name, err := validateTemplateName(req.Name)
	if err != nil {
		return nil, err
	}
	isFree, err := validateIsFree(req.IsFree)
	if err != nil {
		return nil, err
	}
	if err := validateThreshold(req.FreeThreshold); err != nil {
		return nil, err
	}
	remark, err := validateRemark(req.Remark)
	if err != nil {
		return nil, err
	}

	id, err := g.DB().Model("freight_templates").Ctx(ctx).Data(g.Map{
		"name":           name,
		"is_free":        isFree,
		"free_threshold": req.FreeThreshold,
		"status":         v1.TemplateStatusEnabled,
		"remark":         remark,
	}).InsertAndGetId()
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeFreightTemplateNameExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入运费模板: %w", err))
	}

	row, err := s.findTemplate(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("创建运费模板后未找到记录"))
	}
	return &v1.AdminCreateRes{Template: *toTemplate(row)}, nil
}

// AdminList 查询全部运费模板（按 id 倒序）。
func (s *sFreight) AdminList(ctx context.Context) (*v1.AdminListRes, error) {
	var rows []*templateRow
	if err := g.DB().Model("freight_templates").Ctx(ctx).Order("id DESC").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询运费模板列表: %w", err))
	}
	items := make([]*v1.Template, 0, len(rows))
	for _, row := range rows {
		items = append(items, toTemplate(row))
	}
	return &v1.AdminListRes{Items: items}, nil
}

// AdminDetail 查询模板详情（含区域规则）；模板不存在返回 20001。
func (s *sFreight) AdminDetail(ctx context.Context, id int64) (*v1.AdminDetailRes, error) {
	row, err := s.findTemplate(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeFreightTemplateNotFound)
	}
	rules, err := s.listRules(ctx, id)
	if err != nil {
		return nil, err
	}
	return &v1.AdminDetailRes{Template: *toTemplate(row), Rules: rules}, nil
}

// ValidateTemplateForBinding 校验模板可被商品绑定：必须存在且启用。
// 用于商品管理写入 products.freight_template_id 前的有效性校验，避免把商品绑到不存在/停用的模板上。
func (s *sFreight) ValidateTemplateForBinding(ctx context.Context, id int64) error {
	if id <= 0 {
		return codes.New(codes.CodeInvalidArgument)
	}
	row, err := s.findTemplate(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return codes.New(codes.CodeFreightTemplateNotFound)
	}
	if row.Status != templateStatusEnabled {
		return codes.New(codes.CodeFreightTemplateDisabled)
	}
	return nil
}

// AdminUpdate 更新模板基础信息（不改状态与规则）；不存在 20001，重名 20002。
func (s *sFreight) AdminUpdate(ctx context.Context, req *v1.AdminUpdateReq) (*v1.AdminUpdateRes, error) {
	if req == nil {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	row, err := s.findTemplate(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeFreightTemplateNotFound)
	}

	name, err := validateTemplateName(req.Name)
	if err != nil {
		return nil, err
	}
	isFree, err := validateIsFree(req.IsFree)
	if err != nil {
		return nil, err
	}
	if req.IsFree == nil {
		isFree = row.IsFree
	}
	if err := validateThreshold(req.FreeThreshold); err != nil {
		return nil, err
	}
	remark, err := validateRemark(req.Remark)
	if err != nil {
		return nil, err
	}

	if _, err := g.DB().Model("freight_templates").Ctx(ctx).
		Where("id", req.Id).
		Data(g.Map{
			"name":           name,
			"is_free":        isFree,
			"free_threshold": req.FreeThreshold,
			"remark":         remark,
		}).Update(); err != nil {
		if isDuplicateKeyError(err) {
			return nil, codes.New(codes.CodeFreightTemplateNameExists)
		}
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新运费模板: %w", err))
	}

	updated, err := s.findTemplate(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, codes.New(codes.CodeFreightTemplateNotFound)
	}
	return &v1.AdminUpdateRes{Template: *toTemplate(updated)}, nil
}

// AdminUpdateStatus 启用/停用模板；不存在 20001，非法状态 20003。
//
// 停用只影响后续计价（计价时返回 20004），不删除既有规则，也不回改历史订单运费快照。
func (s *sFreight) AdminUpdateStatus(ctx context.Context, req *v1.AdminUpdateStatusReq) (*v1.AdminUpdateStatusRes, error) {
	if req == nil || req.Status == nil {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	if *req.Status != v1.TemplateStatusEnabled && *req.Status != v1.TemplateStatusDisabled {
		return nil, codes.New(codes.CodeFreightTemplateInvalid)
	}
	row, err := s.findTemplate(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeFreightTemplateNotFound)
	}

	if _, err := g.DB().Model("freight_templates").Ctx(ctx).
		Where("id", req.Id).
		Data(g.Map{"status": *req.Status}).
		Update(); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("更新运费模板状态: %w", err))
	}

	updated, err := s.findTemplate(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, codes.New(codes.CodeFreightTemplateNotFound)
	}
	return &v1.AdminUpdateStatusRes{Template: *toTemplate(updated)}, nil
}

// AdminListRules 查询模板的区域规则；模板不存在 20001。
func (s *sFreight) AdminListRules(ctx context.Context, id int64) (*v1.AdminListRulesRes, error) {
	row, err := s.findTemplate(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, codes.New(codes.CodeFreightTemplateNotFound)
	}
	rules, err := s.listRules(ctx, id)
	if err != nil {
		return nil, err
	}
	return &v1.AdminListRulesRes{Items: rules}, nil
}

// AdminReplaceRules 单事务整体替换模板的区域规则。
//
// 整体替换而非逐条增删，是为了让「同一模板同一区域至多一条规则」在单事务内成立，
// 避免逐条删除/新增过程中出现中间态。空列表表示清空全部规则。
func (s *sFreight) AdminReplaceRules(ctx context.Context, req *v1.AdminReplaceRulesReq) (*v1.AdminReplaceRulesRes, error) {
	if req == nil {
		return nil, codes.New(codes.CodeInvalidArgument)
	}
	inputs, err := validateRuleInputs(req.Rules)
	if err != nil {
		return nil, err
	}

	err = g.DB().Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		var templates []*templateRow
		if e := tx.Model("freight_templates").Ctx(ctx).Where("id", req.Id).Scan(&templates); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询运费模板: %w", e))
		}
		if len(templates) == 0 {
			return codes.New(codes.CodeFreightTemplateNotFound)
		}

		if e := ensureRegionsExist(ctx, tx, inputs); e != nil {
			return e
		}

		if _, e := tx.Model("freight_template_rules").Ctx(ctx).
			Where("template_id", req.Id).Delete(); e != nil {
			return codes.Wrap(codes.CodeInternalError, fmt.Errorf("清理旧的运费区域规则: %w", e))
		}
		for _, input := range inputs {
			if _, e := tx.Model("freight_template_rules").Ctx(ctx).Data(g.Map{
				"template_id":         req.Id,
				"region_id":           input.RegionId,
				"first_quantity":      input.FirstQuantity,
				"first_fee":           input.FirstFee,
				"additional_quantity": input.AdditionalQuantity,
				"additional_fee":      input.AdditionalFee,
				"free_threshold":      input.FreeThreshold,
			}).Insert(); e != nil {
				if isDuplicateKeyError(e) {
					return codes.New(codes.CodeFreightRuleDuplicate)
				}
				return codes.Wrap(codes.CodeInternalError, fmt.Errorf("写入运费区域规则: %w", e))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	rules, err := s.listRules(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return &v1.AdminReplaceRulesRes{Items: rules}, nil
}

// findTemplate 按 id 查询模板行，未命中返回 nil。
func (s *sFreight) findTemplate(ctx context.Context, id int64) (*templateRow, error) {
	if id <= 0 {
		return nil, nil
	}
	var rows []*templateRow
	if err := g.DB().Model("freight_templates").Ctx(ctx).Where("id", id).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("按 id 查询运费模板: %w", err))
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// listRules 查询模板的全部区域规则（按 region_id 升序，便于人工核对）。
func (s *sFreight) listRules(ctx context.Context, templateID int64) ([]*v1.Rule, error) {
	var rows []*ruleRow
	if err := g.DB().Model("freight_template_rules").Ctx(ctx).
		Where("template_id", templateID).Order("region_id ASC").Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询运费区域规则: %w", err))
	}
	items := make([]*v1.Rule, 0, len(rows))
	for _, row := range rows {
		items = append(items, toRule(row))
	}
	return items, nil
}

// ensureRegionsExist 批量校验 region_id 均存在于 regions，任一缺失返回 20008。
func ensureRegionsExist(ctx context.Context, tx gdb.TX, inputs []*v1.RuleInput) error {
	if len(inputs) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(inputs))
	for _, input := range inputs {
		ids = append(ids, input.RegionId)
	}
	var rows []*regionRow
	if err := tx.Model("regions").Ctx(ctx).Fields("id").WhereIn("id", ids).Scan(&rows); err != nil {
		return codes.Wrap(codes.CodeInternalError, fmt.Errorf("校验地区: %w", err))
	}
	if len(rows) != len(ids) {
		return codes.New(codes.CodeFreightRegionNotFound)
	}
	return nil
}

// validateTemplateName 归一化并校验模板名（trim 后 1..64 字符）。
func validateTemplateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || utf8.RuneCountInString(name) > maxNameLen {
		return "", codes.New(codes.CodeFreightTemplateInvalid)
	}
	return name, nil
}

// validateIsFree 校验模板级包邮标记（0/1），nil 返回 0。
func validateIsFree(raw *int) (int, error) {
	if raw == nil {
		return 0, nil
	}
	if *raw != 0 && *raw != 1 {
		return 0, codes.New(codes.CodeFreightTemplateInvalid)
	}
	return *raw, nil
}

// validateThreshold 校验满额免邮阈值（0..maxAmount）。
func validateThreshold(threshold int64) error {
	if threshold < 0 || threshold > maxAmount {
		return codes.New(codes.CodeFreightTemplateInvalid)
	}
	return nil
}

// validateRemark 校验备注长度（<=255 字符）。
func validateRemark(raw string) (string, error) {
	remark := strings.TrimSpace(raw)
	if utf8.RuneCountInString(remark) > maxRemarkLen {
		return "", codes.New(codes.CodeFreightTemplateInvalid)
	}
	return remark, nil
}

// validateRuleInputs 校验规则入参：区域必填且不重复、首件/续件数量 >=1、金额在 [0,maxAmount]。
func validateRuleInputs(inputs []*v1.RuleInput) ([]*v1.RuleInput, error) {
	seen := make(map[int64]bool, len(inputs))
	out := make([]*v1.RuleInput, 0, len(inputs))
	for _, input := range inputs {
		if input == nil {
			return nil, codes.New(codes.CodeFreightRuleInvalid)
		}
		if input.RegionId <= 0 {
			return nil, codes.New(codes.CodeFreightRuleInvalid)
		}
		if seen[input.RegionId] {
			return nil, codes.New(codes.CodeFreightRuleDuplicate)
		}
		seen[input.RegionId] = true
		if input.FirstQuantity < 1 || input.AdditionalQuantity < 1 {
			return nil, codes.New(codes.CodeFreightRuleInvalid)
		}
		if input.FirstFee < 0 || input.FirstFee > maxAmount ||
			input.AdditionalFee < 0 || input.AdditionalFee > maxAmount ||
			input.FreeThreshold < 0 || input.FreeThreshold > maxAmount {
			return nil, codes.New(codes.CodeFreightRuleInvalid)
		}
		out = append(out, input)
	}
	return out, nil
}

// toTemplate 把 DB 行转换为 API 结构。
func toTemplate(row *templateRow) *v1.Template {
	return &v1.Template{
		Id:            row.Id,
		Name:          row.Name,
		IsFree:        row.IsFree,
		FreeThreshold: row.FreeThreshold,
		Status:        row.Status,
		Remark:        row.Remark,
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
}

// toRule 把 DB 行转换为 API 结构。
func toRule(row *ruleRow) *v1.Rule {
	return &v1.Rule{
		Id:                 row.Id,
		TemplateId:         row.TemplateId,
		RegionId:           row.RegionId,
		FirstQuantity:      row.FirstQuantity,
		FirstFee:           row.FirstFee,
		AdditionalQuantity: row.AdditionalQuantity,
		AdditionalFee:      row.AdditionalFee,
		FreeThreshold:      row.FreeThreshold,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
}

// isDuplicateKeyError 判断是否为 MySQL 唯一约束冲突（1062）。
func isDuplicateKeyError(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
