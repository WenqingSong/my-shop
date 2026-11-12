package sku

// 纯内存单元测试：规格组合指纹、SKU 编码派生与笛卡尔积生成。
// 不连接 MySQL/Redis，由 scripts/test-unit.sh 登记执行。

import (
	"crypto/md5"
	"encoding/hex"
	"testing"

	specv1 "cnb.cool/go-cloud-devops/my-shop/api/spec/v1"
)

// TestSpecKeyStableAndSorted 覆盖：指纹与规格值提交顺序无关、按 spec_id 升序拼接、32 位小写 hex。
func TestSpecKeyStableAndSorted(t *testing.T) {
	a := &specv1.SpecValue{Id: 11, SpecId: 1, Value: "红色"}
	b := &specv1.SpecValue{Id: 21, SpecId: 2, Value: "S"}

	keyAB := specKey([]*specv1.SpecValue{a, b})
	keyBA := specKey([]*specv1.SpecValue{b, a})
	if keyAB != keyBA {
		t.Fatalf("指纹应与规格值顺序无关: %q != %q", keyAB, keyBA)
	}

	sum := md5.Sum([]byte("1:11,2:21"))
	want := hex.EncodeToString(sum[:])
	if keyAB != want {
		t.Fatalf("指纹算法不符: got %q want %q", keyAB, want)
	}
	if len(keyAB) != 32 {
		t.Fatalf("指纹长度=%d want 32", len(keyAB))
	}

	// 不同组合 → 不同指纹。
	other := specKey([]*specv1.SpecValue{
		{Id: 11, SpecId: 1},
		{Id: 22, SpecId: 2},
	})
	if other == keyAB {
		t.Fatalf("不同规格组合不应产生相同指纹")
	}
}

// TestSkuCodeDerivation 覆盖：编码由商品 + 指纹稳定派生，跨商品/跨组合互不冲突。
func TestSkuCodeDerivation(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef"
	code := generatedSkuCode(7, key)
	if code != "SKU-P7-0123456789AB" {
		t.Fatalf("编码派生不符: %q", code)
	}
	if again := generatedSkuCode(7, key); again != code {
		t.Fatalf("同一组合编码必须稳定: %q != %q", again, code)
	}
	if other := generatedSkuCode(8, key); other == code {
		t.Fatalf("不同商品不应共用编码")
	}
	if other := generatedSkuCode(7, "ffffffffffffffffffffffffffffffff"); other == code {
		t.Fatalf("不同组合不应共用编码")
	}

	random := randomSkuCode(7)
	if random == randomSkuCode(7) {
		t.Fatalf("手动创建 SKU 的随机编码不应重复")
	}
	if len(random) > 64 {
		t.Fatalf("编码长度超限: %d", len(random))
	}
}

// TestCombinationsCartesianProduct 覆盖多维规格的笛卡尔积数量与组合完整性。
func TestCombinationsCartesianProduct(t *testing.T) {
	two := []*specv1.SpecGroup{
		{Id: 1, Name: "颜色", Values: []*specv1.SpecValue{
			{Id: 11, SpecId: 1, Value: "红色"},
			{Id: 12, SpecId: 1, Value: "蓝色"},
		}},
		{Id: 2, Name: "尺码", Values: []*specv1.SpecValue{
			{Id: 21, SpecId: 2, Value: "S"},
			{Id: 22, SpecId: 2, Value: "M"},
			{Id: 23, SpecId: 2, Value: "L"},
		}},
	}
	combos := combinations(two)
	if len(combos) != 6 {
		t.Fatalf("2x3 组合数=%d want 6", len(combos))
	}
	seen := map[string]bool{}
	for _, combo := range combos {
		if len(combo) != 2 {
			t.Fatalf("组合维度=%d want 2", len(combo))
		}
		text := comboText(combo)
		if seen[text] {
			t.Fatalf("组合重复: %q", text)
		}
		seen[text] = true
		if key := specKey(combo); seen["key:"+key] {
			t.Fatalf("组合指纹重复: %q", key)
		} else {
			seen["key:"+key] = true
		}
	}
	for _, want := range []string{"红色,S", "红色,M", "红色,L", "蓝色,S", "蓝色,M", "蓝色,L"} {
		if !seen[want] {
			t.Fatalf("缺少组合 %q，实际 %v", want, seen)
		}
	}

	three := append([]*specv1.SpecGroup{}, two...)
	three = append(three, &specv1.SpecGroup{Id: 3, Name: "材质", Values: []*specv1.SpecValue{
		{Id: 31, SpecId: 3, Value: "棉"},
		{Id: 32, SpecId: 3, Value: "麻"},
	}})
	if got := len(combinations(three)); got != 12 {
		t.Fatalf("2x3x2 组合数=%d want 12", got)
	}
}
