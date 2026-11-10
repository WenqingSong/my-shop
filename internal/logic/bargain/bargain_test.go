package bargain

import (
	"testing"
	"time"

	"github.com/gogf/gf/v2/os/gtime"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/bargain/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// TestCalcCutAmount 覆盖助力金额算法的边界：名义额、剩余差额补足、已到底价与非法输入。
func TestCalcCutAmount(t *testing.T) {
	cases := []struct {
		name         string
		helpAmount   int64
		currentPrice int64
		floorPrice   int64
		want         int64
	}{
		{"名义额小于剩余差额", 100, 10000, 9500, 100},
		{"名义额等于剩余差额", 500, 10000, 9500, 500},
		{"名义额大于剩余差额时只补足差额", 600, 10000, 9500, 500},
		{"已到底价不再砍减", 100, 9500, 9500, 0},
		{"低于底价（防御）不砍减", 100, 9400, 9500, 0},
		{"名义额为 0 不砍减", 0, 10000, 9500, 0},
		{"名义额为负不砍减", -100, 10000, 9500, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := calcCutAmount(c.helpAmount, c.currentPrice, c.floorPrice); got != c.want {
				t.Fatalf("calcCutAmount(%d,%d,%d) = %d, want %d", c.helpAmount, c.currentPrice, c.floorPrice, got, c.want)
			}
		})
	}
}

// TestHelpsRequired 覆盖「活动可达成性」所需助力次数（向上取整）。
func TestHelpsRequired(t *testing.T) {
	cases := []struct {
		gap, helpAmount, want int64
	}{
		{9000, 100, 90},
		{9000, 9000, 1},
		{9001, 9000, 2},
		{1, 100, 1},
		{0, 100, 0},
		{-10, 100, 0},
		{100, 0, 100}, // 非法助力额：返回 gap，保证后续可达成性校验必然失败
	}
	for _, c := range cases {
		if got := helpsRequired(c.gap, c.helpAmount); got != c.want {
			t.Fatalf("helpsRequired(%d,%d) = %d, want %d", c.gap, c.helpAmount, got, c.want)
		}
	}
}

// TestNormalizeActivityInput 覆盖活动参数校验：合法配置通过，非法价格/时间/助力参数一律 21002。
func TestNormalizeActivityInput(t *testing.T) {
	now := time.Now()
	start := gtime.NewFromTime(now.Add(-time.Hour))
	end := gtime.NewFromTime(now.Add(24 * time.Hour))
	past := gtime.NewFromTime(now.Add(-2 * time.Hour))

	valid := func() (*activityInput, error) {
		return normalizeActivityInput("秋季砍价", 1, 2, 10000, 5000, 100, 50, start, end)
	}

	t.Run("合法配置", func(t *testing.T) {
		in, err := valid()
		if err != nil {
			t.Fatalf("合法配置返回错误: %v", err)
		}
		if in.Name != "秋季砍价" || in.StartPrice != 10000 || in.FloorPrice != 5000 {
			t.Fatalf("规范化结果不符合预期: %+v", in)
		}
	})

	t.Run("名称首尾空白被裁剪", func(t *testing.T) {
		in, err := normalizeActivityInput("  秋季砍价  ", 1, 2, 10000, 5000, 100, 50, start, end)
		if err != nil {
			t.Fatalf("返回错误: %v", err)
		}
		if in.Name != "秋季砍价" {
			t.Fatalf("name = %q, want 秋季砍价", in.Name)
		}
	})

	invalid := []struct {
		name       string
		activity   string
		productID  int64
		skuID      int64
		startPrice int64
		floorPrice int64
		helpAmount int64
		maxHelp    int64
		startTime  *gtime.Time
		endTime    *gtime.Time
	}{
		{"空名称", "", 1, 2, 10000, 5000, 100, 50, start, end},
		{"名称超长", string(make([]rune, 65)), 1, 2, 10000, 5000, 100, 50, start, end},
		{"商品 id 非法", "x", 0, 2, 10000, 5000, 100, 50, start, end},
		{"SKU id 非法", "x", 1, 0, 10000, 5000, 100, 50, start, end},
		{"原始价过低", "x", 1, 2, 1, 1, 100, 50, start, end},
		{"原始价超上限", "x", 1, 2, maxAmount + 1, 5000, 100, 50, start, end},
		{"底价为 0", "x", 1, 2, 10000, 0, 100, 50, start, end},
		{"底价等于原始价", "x", 1, 2, 10000, 10000, 100, 50, start, end},
		{"底价高于原始价", "x", 1, 2, 10000, 10001, 100, 50, start, end},
		{"助力额为 0", "x", 1, 2, 10000, 5000, 0, 50, start, end},
		{"助力额为负", "x", 1, 2, 10000, 5000, -1, 50, start, end},
		{"助力次数为 0", "x", 1, 2, 10000, 5000, 100, 0, start, end},
		{"助力次数不足以砍到底价", "x", 1, 2, 10000, 5000, 100, 49, start, end},
		{"缺少开始时间", "x", 1, 2, 10000, 5000, 100, 50, nil, end},
		{"缺少结束时间", "x", 1, 2, 10000, 5000, 100, 50, start, nil},
		{"结束早于开始", "x", 1, 2, 10000, 5000, 100, 50, end, start},
		{"结束等于开始", "x", 1, 2, 10000, 5000, 100, 50, start, start},
		{"结束时间已过", "x", 1, 2, 10000, 5000, 100, 50, past, past},
	}
	for _, c := range invalid {
		t.Run(c.name, func(t *testing.T) {
			_, err := normalizeActivityInput(c.activity, c.productID, c.skuID, c.startPrice, c.floorPrice,
				c.helpAmount, c.maxHelp, c.startTime, c.endTime)
			if err == nil {
				t.Fatalf("期望 21002，实际通过")
			}
			if got := codes.FromError(err); got != codes.CodeBargainActivityInvalid {
				t.Fatalf("错误码 = %d, want %d", got, codes.CodeBargainActivityInvalid)
			}
		})
	}
}

// TestStatusToString 覆盖 DB 状态到 API 字符串枚举的映射（未知值按进行中兜底）。
func TestStatusToString(t *testing.T) {
	cases := map[int]string{
		instanceInProgress: v1.StatusInProgress,
		instanceSuccess:    v1.StatusSuccess,
		instanceFailed:     v1.StatusFailed,
		instanceExpired:    v1.StatusExpired,
		0:                  v1.StatusInProgress,
		99:                 v1.StatusInProgress,
	}
	for status, want := range cases {
		if got := statusToString(status); got != want {
			t.Fatalf("statusToString(%d) = %q, want %q", status, got, want)
		}
	}
}

// TestParsePagination 覆盖分页默认值、上限与非法值。
func TestParsePagination(t *testing.T) {
	one, twenty, hundred, hundredOne, zero, negative := 1, 20, 100, 101, 0, -1

	page, size, err := parsePagination(nil, nil)
	if err != nil || page != 1 || size == 0 {
		t.Fatalf("默认分页 = (%d,%d,%v), want (1,20,nil)", page, size, err)
	}
	if _, _, err := parsePagination(&one, &hundred); err != nil {
		t.Fatalf("边界分页不应报错: %v", err)
	}

	invalid := []struct {
		name string
		page *int
		size *int
	}{
		{"页码为 0", &zero, nil},
		{"页码为负", &negative, nil},
		{"每页为 0", nil, &zero},
		{"每页超上限", nil, &hundredOne},
	}
	for _, c := range invalid {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := parsePagination(c.page, c.size); err == nil {
				t.Fatalf("期望 1001，实际通过")
			} else if got := codes.FromError(err); got != codes.CodeInvalidArgument {
				t.Fatalf("错误码 = %d, want %d", got, codes.CodeInvalidArgument)
			}
		})
	}

	if _, _, err := parsePagination(&one, &twenty); err != nil {
		t.Fatalf("合法分页返回错误: %v", err)
	}
}
