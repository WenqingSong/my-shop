package freight

import (
	"math"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// 模板状态（DB TINYINT，见 api/freight/v1 与 docs/design/freight.md）。
const (
	templateStatusDisabled = 0
	templateStatusEnabled  = 1
)

// maxAmount 是单笔金额上限（整数分，= ¥999,999.99），与项目其他金额列约定一致。
const maxAmount int64 = 99_999_999

// fareRule 是参与运费计算的区域规则快照（与 DB 行解耦，便于纯函数单元测试）。
type fareRule struct {
	RegionID           int64
	FirstQuantity      int64
	FirstFee           int64
	AdditionalQuantity int64
	AdditionalFee      int64
	FreeThreshold      int64
}

// templateSnapshot 是参与运费计算的模板快照。
type templateSnapshot struct {
	ID            int64
	Status        int
	IsFree        int
	FreeThreshold int64
}

// templateGroup 是同一运费模板下的商品聚合：按件计费依赖数量合计，免邮依赖金额合计。
type templateGroup struct {
	TemplateID int64
	Quantity   int64
	Amount     int64
}

// feeByPieces 按件计费内核：
//
//	quantity <= firstQuantity  → firstFee
//	quantity >  firstQuantity  → firstFee + ceil((quantity-firstQuantity)/additionalQuantity) × additionalFee
//
// 使用整数除法实现向上取整，不引入浮点。任何非法规则（首件/续件数量 <= 0）与溢出都返回明确错误，
// 不返回「0 运费」这种会被误认为包邮的结果。
func feeByPieces(quantity int64, rule fareRule) (int64, error) {
	if quantity < 1 {
		return 0, codes.New(codes.CodeFreightRuleInvalid)
	}
	if rule.FirstQuantity < 1 || rule.AdditionalQuantity < 1 {
		return 0, codes.New(codes.CodeFreightRuleInvalid)
	}
	if rule.FirstFee < 0 || rule.AdditionalFee < 0 {
		return 0, codes.New(codes.CodePricingInvalidAmount)
	}
	if quantity <= rule.FirstQuantity {
		return rule.FirstFee, nil
	}

	extra := quantity - rule.FirstQuantity
	units := extra / rule.AdditionalQuantity
	if extra%rule.AdditionalQuantity != 0 {
		units++ // 向上取整：不足一个续件单位也按一个续件单位收费
	}
	if rule.AdditionalFee > 0 && units > (math.MaxInt64-rule.FirstFee)/rule.AdditionalFee {
		return 0, codes.New(codes.CodePricingAmountOverflow)
	}
	return rule.FirstFee + units*rule.AdditionalFee, nil
}

// matchRule 沿地区链由近及远（区 → 市 → 省）返回首个命中的规则。
//
// 只在 region_id 维度匹配，绝不按地区名称模糊匹配；chain 由调用方从 regions 表按 parent_id 上溯得到。
func matchRule(chain []int64, rules []fareRule) (fareRule, bool) {
	if len(chain) == 0 || len(rules) == 0 {
		return fareRule{}, false
	}
	byRegion := make(map[int64]fareRule, len(rules))
	for _, rule := range rules {
		byRegion[rule.RegionID] = rule
	}
	for _, regionID := range chain {
		if rule, ok := byRegion[regionID]; ok {
			return rule, true
		}
	}
	return fareRule{}, false
}

// effectiveThreshold 返回该规则实际生效的满额免邮阈值：区域阈值 > 0 时覆盖模板阈值。
func effectiveThreshold(rule fareRule, templateThreshold int64) int64 {
	if rule.FreeThreshold > 0 {
		return rule.FreeThreshold
	}
	return templateThreshold
}

// groupItems 把行项目按运费模板聚合，未绑定模板的行直接跳过（零运费历史兼容）。
func groupItems(items []service.FreightItem) ([]*templateGroup, error) {
	order := make([]int64, 0, len(items))
	index := make(map[int64]*templateGroup, len(items))

	for _, item := range items {
		if item.FreightTemplateID == nil {
			continue
		}
		templateID := *item.FreightTemplateID
		if templateID <= 0 {
			return nil, codes.New(codes.CodeFreightTemplateNotFound)
		}
		if item.Quantity < 1 {
			return nil, codes.New(codes.CodeFreightRuleInvalid)
		}
		if item.Amount < 0 {
			return nil, codes.New(codes.CodePricingInvalidAmount)
		}

		group, ok := index[templateID]
		if !ok {
			group = &templateGroup{TemplateID: templateID}
			index[templateID] = group
			order = append(order, templateID)
		}
		if item.Quantity > math.MaxInt64-group.Quantity {
			return nil, codes.New(codes.CodePricingAmountOverflow)
		}
		group.Quantity += item.Quantity
		if item.Amount > math.MaxInt64-group.Amount {
			return nil, codes.New(codes.CodePricingAmountOverflow)
		}
		group.Amount += item.Amount
	}

	groups := make([]*templateGroup, 0, len(order))
	for _, templateID := range order {
		groups = append(groups, index[templateID])
	}
	return groups, nil
}

// computeShippingFee 是运费合计内核：按模板分组各自计费后求和。
//
// 语义边界：
//   - 模板不存在 → 20001；模板停用 → 20004（不静默按 0 处理）；
//   - 模板级包邮 → 该组 0；
//   - 无匹配区域规则 → 20009（不静默按 0 处理）；
//   - 命中满额免邮 → 该组 0；
//   - 其余按件计费。
func computeShippingFee(
	regionChain []int64,
	groups []*templateGroup,
	templates map[int64]*templateSnapshot,
	rules map[int64][]fareRule,
) (int64, error) {
	total := int64(0)
	for _, group := range groups {
		template, ok := templates[group.TemplateID]
		if !ok {
			return 0, codes.New(codes.CodeFreightTemplateNotFound)
		}
		if template.Status != templateStatusEnabled {
			return 0, codes.New(codes.CodeFreightTemplateDisabled)
		}
		if template.IsFree == 1 {
			continue
		}

		rule, ok := matchRule(regionChain, rules[group.TemplateID])
		if !ok {
			return 0, codes.New(codes.CodeFreightRegionRuleMissing)
		}
		if threshold := effectiveThreshold(rule, template.FreeThreshold); threshold > 0 && group.Amount >= threshold {
			continue
		}

		fee, err := feeByPieces(group.Quantity, rule)
		if err != nil {
			return 0, err
		}
		if fee > math.MaxInt64-total {
			return 0, codes.New(codes.CodePricingAmountOverflow)
		}
		total += fee
	}
	return total, nil
}
