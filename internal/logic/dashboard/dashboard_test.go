package dashboard

import (
	"testing"
	"time"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// TestParseLimit 覆盖商品销量排行 limit 参数解析与校验：
// 空/未提供 → 默认 10；合法正整数 → 原样返回；非整数（含小数/非数字）、0、负数、超上限 → 400/1001。
// 该测试能区分「拒绝非法输入」与「静默按默认值/截断处理」（CLEAN-001 回归）。
func TestParseLimit(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    int
		wantErr bool
	}{
		{name: "未提供(空串)", in: "", want: defaultTopLimit},
		{name: "空白", in: "  ", want: defaultTopLimit},
		{name: "默认值 10", in: "10", want: 10},
		{name: "上限 50", in: "50", want: 50},
		{name: "最小值 1", in: "1", want: 1},
		{name: "非数字 abc", in: "abc", wantErr: true},
		{name: "小数 12.5", in: "12.5", wantErr: true},
		{name: "0", in: "0", wantErr: true},
		{name: "负数 -1", in: "-1", wantErr: true},
		{name: "超上限 51", in: "51", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseLimit(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parseLimit(%q) 应返回错误，却得到 %d", c.in, got)
				}
				if code := codes.FromError(err); code != codes.CodeInvalidArgument {
					t.Fatalf("parseLimit(%q) 错误码 = %d，want %d", c.in, code, codes.CodeInvalidArgument)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLimit(%q) 意外错误: %v", c.in, err)
			}
			if got != c.want {
				t.Fatalf("parseLimit(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// TestDashboardHasDST 覆盖 CLEAN-002：无 DST 时区（Asia/Shanghai）应判定为 false，
// 含 DST 时区（America/New_York）应判定为 true，保证启动时能拒绝 DST 时区。
func TestDashboardHasDST(t *testing.T) {
	sh, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("系统缺少 Asia/Shanghai 时区数据: %v", err)
	}
	if hasDST(sh) {
		t.Fatal("Asia/Shanghai 不应判定为含 DST")
	}

	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("系统缺少 America/New_York 时区数据: %v", err)
	}
	if !hasDST(ny) {
		t.Fatal("America/New_York 应判定为含 DST")
	}
}
