package freight

import (
	"context"
	"fmt"
	"math"

	"github.com/gogf/gf/v2/frame/g"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// Quote 计算一笔订单的运费（服务端唯一入口）。
//
// 读取路径全部走服务端权威数据：regions 层级、freight_templates、freight_template_rules；
// 不读取客户端传入的金额或地区名称。全部商品都未绑定模板时直接返回 0，不要求地区有效
// （历史商品零运费兼容），避免为不需要运费计算的订单引入额外失败点。
func (s *sFreight) Quote(ctx context.Context, req *service.FreightQuoteReq) (*service.FreightQuoteRes, error) {
	if req == nil {
		return nil, codes.New(codes.CodeInvalidArgument)
	}

	groups, err := groupItems(req.Items)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return &service.FreightQuoteRes{ShippingFee: 0}, nil
	}

	regionChain, err := s.loadRegionChain(ctx, req.RegionID)
	if err != nil {
		return nil, err
	}

	templateIDs := make([]int64, 0, len(groups))
	for _, group := range groups {
		templateIDs = append(templateIDs, group.TemplateID)
	}
	templates, err := s.loadTemplateSnapshots(ctx, templateIDs)
	if err != nil {
		return nil, err
	}
	rules, err := s.loadRulesByTemplate(ctx, templateIDs)
	if err != nil {
		return nil, err
	}

	fee, err := computeShippingFee(regionChain, groups, templates, rules)
	if err != nil {
		return nil, err
	}
	if fee > math.MaxUint32 {
		return nil, codes.New(codes.CodePricingAmountOverflow)
	}
	return &service.FreightQuoteRes{ShippingFee: fee}, nil
}

// loadRegionChain 从 region_id 沿 parent_id 上溯，返回 [本级, 上级, ...]。
//
// 本级不存在返回 20008（避免把「地址地区脏数据」静默当成无规则）；
// 上级缺失只停止上溯，已收集的层级仍然有效；parent_id 成环时由 seen 与深度上限截断。
func (s *sFreight) loadRegionChain(ctx context.Context, regionID int64) ([]int64, error) {
	if regionID <= 0 {
		return nil, codes.New(codes.CodeFreightRegionNotFound)
	}
	chain := make([]int64, 0, 3)
	seen := make(map[int64]bool, 3)
	current := regionID
	for depth := 0; depth < maxRegionDepth && current > 0; depth++ {
		if seen[current] {
			break
		}
		seen[current] = true

		var rows []*regionRow
		if err := g.DB().Model("regions").Ctx(ctx).
			Fields("id", "parent_id").Where("id", current).Scan(&rows); err != nil {
			return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询地区层级: %w", err))
		}
		if len(rows) == 0 {
			if depth == 0 {
				return nil, codes.New(codes.CodeFreightRegionNotFound)
			}
			break
		}
		chain = append(chain, rows[0].Id)
		current = rows[0].ParentId
	}
	return chain, nil
}

// loadTemplateSnapshots 批量加载模板快照；不存在的模板不会出现在 map 中，由计算内核报 20001。
func (s *sFreight) loadTemplateSnapshots(ctx context.Context, ids []int64) (map[int64]*templateSnapshot, error) {
	var rows []*templateRow
	if err := g.DB().Model("freight_templates").Ctx(ctx).WhereIn("id", ids).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询运费模板: %w", err))
	}
	out := make(map[int64]*templateSnapshot, len(rows))
	for _, row := range rows {
		out[row.Id] = &templateSnapshot{
			ID:            row.Id,
			Status:        row.Status,
			IsFree:        row.IsFree,
			FreeThreshold: row.FreeThreshold,
		}
	}
	return out, nil
}

// loadRulesByTemplate 批量加载模板的区域规则，按模板 id 分组。
func (s *sFreight) loadRulesByTemplate(ctx context.Context, templateIDs []int64) (map[int64][]fareRule, error) {
	var rows []*ruleRow
	if err := g.DB().Model("freight_template_rules").Ctx(ctx).
		WhereIn("template_id", templateIDs).Scan(&rows); err != nil {
		return nil, codes.Wrap(codes.CodeInternalError, fmt.Errorf("查询运费区域规则: %w", err))
	}
	out := make(map[int64][]fareRule, len(templateIDs))
	for _, row := range rows {
		out[row.TemplateId] = append(out[row.TemplateId], fareRule{
			RegionID:           row.RegionId,
			FirstQuantity:      row.FirstQuantity,
			FirstFee:           row.FirstFee,
			AdditionalQuantity: row.AdditionalQuantity,
			AdditionalFee:      row.AdditionalFee,
			FreeThreshold:      row.FreeThreshold,
		})
	}
	return out, nil
}
