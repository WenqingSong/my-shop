package profile

import "testing"

// TestParseBirthday 覆盖 YYYY-MM-DD 严格解析：拒绝未补零、非法日期与溢出日期。
func TestParseBirthday(t *testing.T) {
	valid := []string{"1990-05-20", "2000-01-01", "2024-02-29"}
	for _, s := range valid {
		got, err := parseBirthday(s)
		if err != nil {
			t.Fatalf("parseBirthday(%q) unexpected error: %v", s, err)
		}
		if got.Format(birthdayLayout) != s {
			t.Fatalf("parseBirthday(%q) round trip = %q", s, got.Format(birthdayLayout))
		}
	}

	invalid := []string{"", "1990-2-3", "1990-02-30", "2023-02-29", "abc", "1990/05/20", "1990-05-20 00:00:00", "0000-00-00"}
	for _, s := range invalid {
		if _, err := parseBirthday(s); err == nil {
			t.Fatalf("parseBirthday(%q) should fail", s)
		}
	}
}

// TestIsHTTPURL 覆盖头像地址协议白名单，拒绝脚本/数据协议与相对地址。
func TestIsHTTPURL(t *testing.T) {
	valid := []string{
		"http://example.com/a.png",
		"https://cdn.example.com/avatar/a.png?x=1",
	}
	for _, s := range valid {
		if !isHTTPURL(s) {
			t.Fatalf("isHTTPURL(%q) should be true", s)
		}
	}
	invalid := []string{
		"",
		"javascript:alert(1)",
		"data:image/png;base64,AAAA",
		"ftp://example.com/a.png",
		"/relative/path.png",
		"http://",
		"://bad",
	}
	for _, s := range invalid {
		if isHTTPURL(s) {
			t.Fatalf("isHTTPURL(%q) should be false", s)
		}
	}
}
