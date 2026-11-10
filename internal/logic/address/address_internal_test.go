package address

import (
	"strings"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// TestValidateAddressFields 覆盖地址字段的纯校验逻辑（trim、长度、手机号格式）。
func TestValidateAddressFields(t *testing.T) {
	if got, err := validateRecipientName("  张三  "); err != nil || got != "张三" {
		t.Fatalf("recipient trim: got=%q err=%v", got, err)
	}
	for _, s := range []string{"", "   ", strings.Repeat("名", 33)} {
		if _, err := validateRecipientName(s); codes.FromError(err) != codes.CodeInvalidArgument {
			t.Fatalf("recipient %q should be invalid, err=%v", s, err)
		}
	}

	if got, err := validatePhone(" 13800138000 "); err != nil || got != "13800138000" {
		t.Fatalf("phone trim: got=%q err=%v", got, err)
	}
	for _, s := range []string{"", "12345", "12800138000", "1380013800a", "+8613800138000"} {
		if _, err := validatePhone(s); codes.FromError(err) != codes.CodeInvalidArgument {
			t.Fatalf("phone %q should be invalid, err=%v", s, err)
		}
	}

	if got, err := validateRegion("  广东省 "); err != nil || got != "广东省" {
		t.Fatalf("region trim: got=%q err=%v", got, err)
	}
	for _, s := range []string{"", "  ", strings.Repeat("省", 33)} {
		if _, err := validateRegion(s); codes.FromError(err) != codes.CodeInvalidArgument {
			t.Fatalf("region %q should be invalid, err=%v", s, err)
		}
	}

	if got, err := validateDetail("  科技园 1 号 "); err != nil || got != "科技园 1 号" {
		t.Fatalf("detail trim: got=%q err=%v", got, err)
	}
	for _, s := range []string{"", "  ", strings.Repeat("路", 256)} {
		if _, err := validateDetail(s); codes.FromError(err) != codes.CodeInvalidArgument {
			t.Fatalf("detail %q should be invalid, err=%v", s, err)
		}
	}

	if boolToInt(true) != defaultFlag || boolToInt(false) != 0 {
		t.Fatalf("boolToInt mismatch")
	}
}
