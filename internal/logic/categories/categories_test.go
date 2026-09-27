package categories

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
		ok    bool
	}{
		{"normal", "手机", "手机", true},
		{"trim space", "  手机  ", "手机", true},
		{"max length", strings.Repeat("字", 64), strings.Repeat("字", 64), true},
		{"empty", "", "", false},
		{"only spaces", "   ", "", false},
		{"too long", strings.Repeat("字", 65), "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := validateName(c.value)
			if c.ok && err != nil {
				t.Fatalf("expected valid %q, got error %v", c.value, err)
			}
			if !c.ok && err == nil {
				t.Fatalf("expected error for %q, got nil", c.value)
			}
			if c.ok && got != c.want {
				t.Fatalf("expected trimmed %q, got %q", c.want, got)
			}
		})
	}
}

func TestValidateStatus(t *testing.T) {
	for _, s := range []int{statusEnabled, statusDisabled} {
		if err := validateStatus(s); err != nil {
			t.Fatalf("expected status %d valid, got %v", s, err)
		}
	}
	for _, s := range []int{-1, 2} {
		if err := validateStatus(s); err == nil {
			t.Fatalf("expected status %d invalid, got nil", s)
		}
	}
}

func TestLevelOf(t *testing.T) {
	// 顶级(1) → 二级(2) → 三级(3)
	parentOf := map[int64]int64{
		1: 0,
		2: 1,
		3: 2,
	}
	cases := []struct {
		id   int64
		want int
	}{
		{1, 1},
		{2, 2},
		{3, 3},
	}
	for _, c := range cases {
		got, err := levelOf(parentOf, c.id)
		if err != nil {
			t.Fatalf("levelOf(%d): %v", c.id, err)
		}
		if got != c.want {
			t.Fatalf("levelOf(%d) = %d, want %d", c.id, got, c.want)
		}
	}

	// 父不存在。
	if _, err := levelOf(parentOf, 999); err == nil {
		t.Fatal("expected error for missing parent")
	}
	// 环。
	cycle := map[int64]int64{10: 11, 11: 10}
	if _, err := levelOf(cycle, 10); err == nil {
		t.Fatal("expected error for cycle")
	}
}

func TestIsDescendant(t *testing.T) {
	parentOf := map[int64]int64{
		1: 0,
		2: 1,
		3: 2,
		4: 0,
	}
	cases := []struct {
		ancestor int64
		node     int64
		want     bool
	}{
		{1, 1, true},  // 自身
		{1, 2, true},  // 直接子
		{1, 3, true},  // 孙
		{2, 3, true},  // 子
		{3, 2, false}, // 反向
		{4, 3, false}, // 无关分支
		{1, 4, false},
	}
	for _, c := range cases {
		if got := isDescendant(parentOf, c.ancestor, c.node); got != c.want {
			t.Fatalf("isDescendant(%d, %d) = %v, want %v", c.ancestor, c.node, got, c.want)
		}
	}
}

func TestValidateParent(t *testing.T) {
	// 顶级(1) → 二级(2) → 三级(3)；另有一个顶级(4)。
	parentOf := map[int64]int64{
		1: 0,
		2: 1,
		3: 2,
		4: 0,
	}

	// 合法：顶级、二级、三级。
	if err := validateParent(parentOf, 0, 0); err != nil {
		t.Fatalf("top-level parent should be valid: %v", err)
	}
	if err := validateParent(parentOf, 1, 0); err != nil {
		t.Fatalf("level-1 parent should be valid: %v", err)
	}
	if err := validateParent(parentOf, 2, 0); err != nil {
		t.Fatalf("level-2 parent should be valid: %v", err)
	}

	// 指向自身。
	if err := validateParent(parentOf, 1, 1); err == nil {
		t.Fatal("expected error when parent is self")
	}
	// 指向自身后代。
	if err := validateParent(parentOf, 3, 1); err == nil {
		t.Fatal("expected error when parent is a descendant")
	}
	// 父不存在。
	if err := validateParent(parentOf, 999, 0); err == nil {
		t.Fatal("expected error when parent missing")
	}
	// 超过 3 级：在三级(3)下建子分类。
	if err := validateParent(parentOf, 3, 0); err == nil {
		t.Fatal("expected error when creating under level-3")
	}
	// 跨分支合法：把 4 移到 1 下为二级。
	if err := validateParent(parentOf, 1, 4); err != nil {
		t.Fatalf("reparent 4 under 1 should be valid: %v", err)
	}
}

// TestValidateParentSubtreeDepth 覆盖 CLEAN-001：改父移动含子节点的子树时，
// 即使被移动节点自身层级 ≤3，其后代超过 3 级也必须被拒绝。
func TestValidateParentSubtreeDepth(t *testing.T) {
	// 1=A(顶)、2=B(顶)、3=C(B 的子, L2)、4=D(顶)、5=E(D 的子, L2)。
	parentOf := map[int64]int64{
		1: 0,
		2: 0,
		3: 2,
		4: 0,
		5: 4,
	}

	// 把带子树 B 移到 E(L2) 下：B 自身成为 L3，但 C 会随之下移成为 L4，必须拒绝。
	if err := validateParent(parentOf, 5, 2); err == nil {
		t.Fatal("expected error when moving subtree B under E would push descendant C to level 4")
	}
	// 对照：把叶子 4 移到 1 下仍是二级，合法。
	if err := validateParent(parentOf, 1, 4); err != nil {
		t.Fatalf("reparent leaf 4 under 1 should be valid: %v", err)
	}
	// 把子树 B 移到顶级（parentID=0）合法。
	if err := validateParent(parentOf, 0, 2); err != nil {
		t.Fatalf("reparent subtree B to top should be valid: %v", err)
	}
}

func TestSubtreeDepth(t *testing.T) {
	// 1=A(顶) → 2=B(L2) → 3=C(L3)；4=D(顶)。
	parentOf := map[int64]int64{
		1: 0,
		2: 1,
		3: 2,
		4: 0,
	}
	cases := []struct {
		root int64
		want int
	}{
		{1, 3}, // A 的子树深 3（A→B→C）
		{2, 2}, // B 的子树深 2（B→C）
		{3, 1}, // C 是叶子
		{4, 1}, // D 是叶子
	}
	for _, c := range cases {
		if got := subtreeDepth(parentOf, c.root); got != c.want {
			t.Fatalf("subtreeDepth(%d) = %d, want %d", c.root, got, c.want)
		}
	}
}

func TestBuildTreeSortingAndVisibility(t *testing.T) {
	// 构造：顶级 A(sort=2), B(sort=1)；B 下有 B1(sort=2,id=11), B2(sort=1,id=10)；
	// 禁用分类 C 及其启用子 C1 不应出现。
	records := []*category{
		{Id: 1, ParentId: 0, Name: "A", Sort: 2, Status: 1},
		{Id: 2, ParentId: 0, Name: "B", Sort: 1, Status: 1},
		{Id: 10, ParentId: 2, Name: "B2", Sort: 1, Status: 1},
		{Id: 11, ParentId: 2, Name: "B1", Sort: 2, Status: 1},
		{Id: 3, ParentId: 0, Name: "C", Sort: 0, Status: 0},
		{Id: 4, ParentId: 3, Name: "C1", Sort: 0, Status: 1},
	}

	tree := buildTree(records)

	// 顶级：B(sort=1) 在 A(sort=2) 之前；禁用 C 及其子树不出现。
	if len(tree) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(tree))
	}
	if tree[0].Name != "B" || tree[1].Name != "A" {
		t.Fatalf("unexpected root order: %s, %s", tree[0].Name, tree[1].Name)
	}
	// B 的子级：B2(sort=1) 在 B1(sort=2) 之前。
	children := tree[0].Children
	if len(children) != 2 {
		t.Fatalf("expected 2 children under B, got %d", len(children))
	}
	if children[0].Name != "B2" || children[1].Name != "B1" {
		t.Fatalf("unexpected child order: %s, %s", children[0].Name, children[1].Name)
	}
}
