package freight

import (
	"math"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// int64Ptr 返回 int64 指针，用于构造「已绑定模板」的行项目。
func int64Ptr(v int64) *int64 { return &v }

func TestFeeByPieces(t *testing.T) {
	rule := fareRule{
		RegionID:           3,
		FirstQuantity:      2,
		FirstFee:           500,
		AdditionalQuantity: 3,
		AdditionalFee:      200,
	}
	cases := []struct {
		name string
		qty  int64
		want int64
	}{
		{"单件走首件", 1, 500},
		{"首件数量边界", 2, 500},
		{"超出首件一件", 3, 700},
		{"恰好一个续件单位", 5, 700},
		{"不足一个续件单位向上取整", 6, 900},
		{"连续多个续件单位", 8, 900},
		{"多续件单位", 9, 1100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := feeByPieces(tc.qty, rule)
			if err != nil {
				t.Fatalf("feeByPieces(%d) 返回错误: %v", tc.qty, err)
			}
			if got != tc.want {
				t.Fatalf("feeByPieces(%d) = %d，期望 %d", tc.qty, got, tc.want)
			}
		})
	}
}

func TestFeeByPiecesZeroAdditionalFee(t *testing.T) {
	rule := fareRule{FirstQuantity: 1, FirstFee: 800, AdditionalQuantity: 1, AdditionalFee: 0}
	got, err := feeByPieces(10, rule)
	if err != nil {
		t.Fatalf("续件费用为 0 应可正常计算，实际错误: %v", err)
	}
	if got != 800 {
		t.Fatalf("续件费用为 0 时运费应为 800，实际 %d", got)
	}
}

func TestFeeByPiecesInvalidInputs(t *testing.T) {
	cases := []struct {
		name     string
		quantity int64
		rule     fareRule
		wantCode codes.Code
	}{
		{
			name:     "数量为 0",
			quantity: 0,
			rule:     fareRule{FirstQuantity: 1, AdditionalQuantity: 1},
			wantCode: codes.CodeFreightRuleInvalid,
		},
		{
			name:     "数量为负",
			quantity: -3,
			rule:     fareRule{FirstQuantity: 1, AdditionalQuantity: 1},
			wantCode: codes.CodeFreightRuleInvalid,
		},
		{
			name:     "首件数量为 0",
			quantity: 1,
			rule:     fareRule{FirstQuantity: 0, AdditionalQuantity: 1},
			wantCode: codes.CodeFreightRuleInvalid,
		},
		{
			name:     "续件数量为 0",
			quantity: 5,
			rule:     fareRule{FirstQuantity: 1, AdditionalQuantity: 0, AdditionalFee: 100},
			wantCode: codes.CodeFreightRuleInvalid,
		},
		{
			name:     "首件费用为负",
			quantity: 1,
			rule:     fareRule{FirstQuantity: 1, FirstFee: -1, AdditionalQuantity: 1},
			wantCode: codes.CodePricingInvalidAmount,
		},
		{
			name:     "续件费用为负",
			quantity: 3,
			rule:     fareRule{FirstQuantity: 1, AdditionalQuantity: 1, AdditionalFee: -100},
			wantCode: codes.CodePricingInvalidAmount,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := feeByPieces(tc.quantity, tc.rule)
			if err == nil {
				t.Fatal("期望返回错误，实际为 nil")
			}
			if got := codes.FromError(err); got != tc.wantCode {
				t.Fatalf("错误码 = %d，期望 %d", got, tc.wantCode)
			}
		})
	}
}

func TestFeeByPiecesOverflow(t *testing.T) {
	rule := fareRule{
		FirstQuantity:      1,
		FirstFee:           math.MaxInt64 - 10,
		AdditionalQuantity: 1,
		AdditionalFee:      1,
	}
	if _, err := feeByPieces(1000, rule); err == nil {
		t.Fatal("期望整数溢出错误，实际为 nil")
	} else if got := codes.FromError(err); got != codes.CodePricingAmountOverflow {
		t.Fatalf("错误码 = %d，期望 %d", got, codes.CodePricingAmountOverflow)
	}
}

func TestMatchRuleFallsBackToParentRegion(t *testing.T) {
	rules := []fareRule{
		{RegionID: 1, FirstQuantity: 1, FirstFee: 900, AdditionalQuantity: 1, AdditionalFee: 900},
		{RegionID: 10, FirstQuantity: 1, FirstFee: 600, AdditionalQuantity: 1, AdditionalFee: 600},
	}
	chain := []int64{100, 10, 1} // 区 100 无规则 → 市 10 命中

	rule, ok := matchRule(chain, rules)
	if !ok {
		t.Fatal("期望命中市级规则，实际未命中")
	}
	if rule.RegionID != 10 {
		t.Fatalf("命中 region_id = %d，期望 10（就近优先）", rule.RegionID)
	}
	if rule.FirstFee != 600 {
		t.Fatalf("命中首件费用 = %d，期望 600", rule.FirstFee)
	}
}

func TestMatchRuleFallsBackToProvince(t *testing.T) {
	rules := []fareRule{
		{RegionID: 1, FirstQuantity: 3, FirstFee: 1200, AdditionalQuantity: 2, AdditionalFee: 300},
	}
	chain := []int64{100, 10, 1}

	rule, ok := matchRule(chain, rules)
	if !ok {
		t.Fatal("期望回退到省级规则，实际未命中")
	}
	if rule.RegionID != 1 {
		t.Fatalf("命中 region_id = %d，期望 1", rule.RegionID)
	}
}

func TestMatchRuleNoMatch(t *testing.T) {
	rules := []fareRule{{RegionID: 999, FirstQuantity: 1, AdditionalQuantity: 1}}
	if _, ok := matchRule([]int64{100, 10, 1}, rules); ok {
		t.Fatal("期望未命中任何规则，实际命中了")
	}
	if _, ok := matchRule(nil, rules); ok {
		t.Fatal("空地区链不应命中任何规则")
	}
	if _, ok := matchRule([]int64{100}, nil); ok {
		t.Fatal("空规则集不应命中任何规则")
	}
}

func TestComputeShippingFeeScenarios(t *testing.T) {
	enabledTemplate := &templateSnapshot{ID: 1, Status: templateStatusEnabled, IsFree: 0, FreeThreshold: 0}
	baseRule := fareRule{RegionID: 10, FirstQuantity: 1, FirstFee: 500, AdditionalQuantity: 2, AdditionalFee: 100}

	cases := []struct {
		name      string
		chain     []int64
		groups    []*templateGroup
		templates map[int64]*templateSnapshot
		rules     map[int64][]fareRule
		wantFee   int64
		wantCode  codes.Code
	}{
		{
			name:      "首件计费",
			chain:     []int64{10},
			groups:    []*templateGroup{{TemplateID: 1, Quantity: 1, Amount: 1000}},
			templates: map[int64]*templateSnapshot{1: enabledTemplate},
			rules:     map[int64][]fareRule{1: {baseRule}},
			wantFee:   500,
		},
		{
			name:      "续件计费",
			chain:     []int64{10},
			groups:    []*templateGroup{{TemplateID: 1, Quantity: 2, Amount: 1000}},
			templates: map[int64]*templateSnapshot{1: enabledTemplate},
			rules:     map[int64][]fareRule{1: {baseRule}},
			wantFee:   600,
		},
		{
			name:      "多续件向上取整",
			chain:     []int64{10},
			groups:    []*templateGroup{{TemplateID: 1, Quantity: 5, Amount: 1000}},
			templates: map[int64]*templateSnapshot{1: enabledTemplate},
			rules:     map[int64][]fareRule{1: {baseRule}},
			wantFee:   700, // 500 + ceil(4/2)*100
		},
		{
			name:  "多模板求和",
			chain: []int64{10},
			groups: []*templateGroup{
				{TemplateID: 1, Quantity: 1, Amount: 1000},
				{TemplateID: 2, Quantity: 3, Amount: 1000},
			},
			templates: map[int64]*templateSnapshot{
				1: enabledTemplate,
				2: {ID: 2, Status: templateStatusEnabled},
			},
			rules: map[int64][]fareRule{
				1: {baseRule},
				2: {{RegionID: 10, FirstQuantity: 1, FirstFee: 300, AdditionalQuantity: 1, AdditionalFee: 50}},
			},
			wantFee: 900, // 500 + (300 + 2*50)
		},
		{
			name:      "模板级包邮",
			chain:     []int64{10},
			groups:    []*templateGroup{{TemplateID: 1, Quantity: 9, Amount: 100}},
			templates: map[int64]*templateSnapshot{1: {ID: 1, Status: templateStatusEnabled, IsFree: 1}},
			rules:     map[int64][]fareRule{1: {baseRule}},
			wantFee:   0,
		},
		{
			name:      "模板满额免邮",
			chain:     []int64{10},
			groups:    []*templateGroup{{TemplateID: 1, Quantity: 3, Amount: 5000}},
			templates: map[int64]*templateSnapshot{1: {ID: 1, Status: templateStatusEnabled, FreeThreshold: 5000}},
			rules:     map[int64][]fareRule{1: {baseRule}},
			wantFee:   0,
		},
		{
			name:      "未达免邮阈值仍计费",
			chain:     []int64{10},
			groups:    []*templateGroup{{TemplateID: 1, Quantity: 3, Amount: 4999}},
			templates: map[int64]*templateSnapshot{1: {ID: 1, Status: templateStatusEnabled, FreeThreshold: 5000}},
			rules:     map[int64][]fareRule{1: {baseRule}},
			wantFee:   600,
		},
		{
			name:      "区域阈值覆盖模板阈值",
			chain:     []int64{10},
			groups:    []*templateGroup{{TemplateID: 1, Quantity: 3, Amount: 6000}},
			templates: map[int64]*templateSnapshot{1: {ID: 1, Status: templateStatusEnabled, FreeThreshold: 5000}},
			rules:     map[int64][]fareRule{1: {{RegionID: 10, FirstQuantity: 1, FirstFee: 500, AdditionalQuantity: 2, AdditionalFee: 100, FreeThreshold: 8000}}},
			wantFee:   600,
		},
		{
			name:      "模板不存在",
			chain:     []int64{10},
			groups:    []*templateGroup{{TemplateID: 1, Quantity: 1, Amount: 100}},
			templates: map[int64]*templateSnapshot{},
			rules:     map[int64][]fareRule{},
			wantCode:  codes.CodeFreightTemplateNotFound,
		},
		{
			name:      "模板停用",
			chain:     []int64{10},
			groups:    []*templateGroup{{TemplateID: 1, Quantity: 1, Amount: 100}},
			templates: map[int64]*templateSnapshot{1: {ID: 1, Status: templateStatusDisabled}},
			rules:     map[int64][]fareRule{1: {baseRule}},
			wantCode:  codes.CodeFreightTemplateDisabled,
		},
		{
			name:      "无匹配区域规则",
			chain:     []int64{100, 10, 1},
			groups:    []*templateGroup{{TemplateID: 1, Quantity: 1, Amount: 100}},
			templates: map[int64]*templateSnapshot{1: enabledTemplate},
			rules:     map[int64][]fareRule{1: {{RegionID: 999, FirstQuantity: 1, FirstFee: 100, AdditionalQuantity: 1}}},
			wantCode:  codes.CodeFreightRegionRuleMissing,
		},
		{
			name:      "无地区链",
			chain:     nil,
			groups:    []*templateGroup{{TemplateID: 1, Quantity: 1, Amount: 100}},
			templates: map[int64]*templateSnapshot{1: enabledTemplate},
			rules:     map[int64][]fareRule{1: {baseRule}},
			wantCode:  codes.CodeFreightRegionRuleMissing,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fee, err := computeShippingFee(tc.chain, tc.groups, tc.templates, tc.rules)
			if tc.wantCode != codes.CodeOK {
				if err == nil {
					t.Fatalf("期望错误码 %d，实际成功（fee=%d）", tc.wantCode, fee)
				}
				if got := codes.FromError(err); got != tc.wantCode {
					t.Fatalf("错误码 = %d，期望 %d", got, tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("期望成功，实际错误: %v", err)
			}
			if fee != tc.wantFee {
				t.Fatalf("运费 = %d，期望 %d", fee, tc.wantFee)
			}
		})
	}
}

func TestComputeShippingFeeOverflow(t *testing.T) {
	templates := map[int64]*templateSnapshot{
		1: {ID: 1, Status: templateStatusEnabled},
		2: {ID: 2, Status: templateStatusEnabled},
	}
	groups := []*templateGroup{
		{TemplateID: 1, Quantity: 1, Amount: 100},
		{TemplateID: 2, Quantity: 1, Amount: 100},
	}
	rules := map[int64][]fareRule{
		1: {{RegionID: 10, FirstQuantity: 1, FirstFee: math.MaxInt64 - 1, AdditionalQuantity: 1}},
		2: {{RegionID: 10, FirstQuantity: 1, FirstFee: 10, AdditionalQuantity: 1}},
	}
	if _, err := computeShippingFee([]int64{10}, groups, templates, rules); err == nil {
		t.Fatal("期望合计溢出错误，实际为 nil")
	} else if got := codes.FromError(err); got != codes.CodePricingAmountOverflow {
		t.Fatalf("错误码 = %d，期望 %d", got, codes.CodePricingAmountOverflow)
	}
}

func TestGroupItems(t *testing.T) {
	t.Run("未绑定模板被跳过", func(t *testing.T) {
		groups, err := groupItems([]service.FreightItem{
			{FreightTemplateID: nil, Quantity: 3, Amount: 1000},
		})
		if err != nil {
			t.Fatalf("未绑定模板不应报错: %v", err)
		}
		if len(groups) != 0 {
			t.Fatalf("未绑定模板不应产生分组，实际 %d 组", len(groups))
		}
	})

	t.Run("同模板合并数量与金额", func(t *testing.T) {
		groups, err := groupItems([]service.FreightItem{
			{FreightTemplateID: int64Ptr(7), Quantity: 2, Amount: 1000},
			{FreightTemplateID: int64Ptr(7), Quantity: 3, Amount: 2500},
		})
		if err != nil {
			t.Fatalf("分组失败: %v", err)
		}
		if len(groups) != 1 {
			t.Fatalf("期望 1 组，实际 %d 组", len(groups))
		}
		if groups[0].Quantity != 5 || groups[0].Amount != 3500 {
			t.Fatalf("合并结果 = qty:%d amount:%d，期望 qty:5 amount:3500", groups[0].Quantity, groups[0].Amount)
		}
	})

	t.Run("非法模板 id", func(t *testing.T) {
		_, err := groupItems([]service.FreightItem{
			{FreightTemplateID: int64Ptr(0), Quantity: 1, Amount: 100},
		})
		if err == nil || codes.FromError(err) != codes.CodeFreightTemplateNotFound {
			t.Fatalf("期望 20001，实际 %v", err)
		}
	})

	t.Run("数量为 0", func(t *testing.T) {
		_, err := groupItems([]service.FreightItem{
			{FreightTemplateID: int64Ptr(1), Quantity: 0, Amount: 100},
		})
		if err == nil || codes.FromError(err) != codes.CodeFreightRuleInvalid {
			t.Fatalf("期望 20007，实际 %v", err)
		}
	})

	t.Run("金额为负", func(t *testing.T) {
		_, err := groupItems([]service.FreightItem{
			{FreightTemplateID: int64Ptr(1), Quantity: 1, Amount: -1},
		})
		if err == nil || codes.FromError(err) != codes.CodePricingInvalidAmount {
			t.Fatalf("期望 20010，实际 %v", err)
		}
	})

	t.Run("金额累加溢出", func(t *testing.T) {
		_, err := groupItems([]service.FreightItem{
			{FreightTemplateID: int64Ptr(1), Quantity: 1, Amount: math.MaxInt64},
			{FreightTemplateID: int64Ptr(1), Quantity: 1, Amount: 1},
		})
		if err == nil || codes.FromError(err) != codes.CodePricingAmountOverflow {
			t.Fatalf("期望 20012，实际 %v", err)
		}
	})
}

func TestEffectiveThreshold(t *testing.T) {
	if got := effectiveThreshold(fareRule{FreeThreshold: 0}, 5000); got != 5000 {
		t.Fatalf("区域阈值为 0 时应继承模板阈值 5000，实际 %d", got)
	}
	if got := effectiveThreshold(fareRule{FreeThreshold: 8000}, 5000); got != 8000 {
		t.Fatalf("区域阈值应覆盖模板阈值，实际 %d", got)
	}
}
