package cmd

// 本文件通过真实路由（RegisterFrontendRoutes）+ 真实 MySQL/Redis 锁定「商品点赞」核心闭环。
// 覆盖 product-like-v1 的 AC-001 至 AC-008 与关键不变量（归属隔离、唯一幂等、可点赞校验、公开计数）。

import (
	"context"
	"fmt"
	"sync"
	"testing"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"
)

// likeResData 是点赞响应。
type likeResData struct {
	Liked bool `json:"liked"`
}

// likeCheckData 是是否已点赞响应。
type likeCheckData struct {
	Liked bool `json:"liked"`
}

// likeCountData 是公开点赞数响应。
type likeCountData struct {
	Count int `json:"count"`
}

// setupLikeServer 建立隔离的点赞测试环境：复用订单域初始化后清空 product_likes 表。
func setupLikeServer(t *testing.T) string {
	t.Helper()
	base := setupOrderServer(t)
	if _, err := g.DB().Exec(context.Background(), "DELETE FROM product_likes"); err != nil {
		t.Fatalf("clean product_likes: %v", err)
	}
	return base
}

// likeUser 创建前台用户并登录，返回 (userID, token)。
func likeUser(t *testing.T, base, username string) (int64, string) {
	t.Helper()
	isoInsertUser(t, username, "likepass12345")
	token, _ := isoFrontendLogin(t, base, username, "likepass12345")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	return userID, token
}

// likeCount 统计 product_likes 表当前行数。
func likeCount(t *testing.T) int {
	t.Helper()
	n, err := g.DB().Model("product_likes").Ctx(context.Background()).Count()
	if err != nil {
		t.Fatalf("count product_likes: %v", err)
	}
	return n
}

// TestLikeAndCheck 覆盖 AC-001/AC-005 与 INV-001：登录用户点赞 on_shelf 商品落库且归属本人、check 返回 true。
func TestLikeAndCheck(t *testing.T) {
	base := setupLikeServer(t)
	categoryID := orderInsertCategory(t, "点赞分类")
	productID := orderInsertProduct(t, categoryID, "点赞商品", 1)

	userID, token := likeUser(t, base, "likeuser1")

	// 未点赞时 check=false。
	c0 := reviewCall(t, base, "GET", fmt.Sprintf("/likes/check?product_id=%d", productID), token, nil)
	if c0.Status != 200 || c0.Code != 0 {
		t.Fatalf("check before: status=%d code=%d", c0.Status, c0.Code)
	}
	if cd := decodeData[likeCheckData](t, c0.Data); cd.Liked {
		t.Fatalf("check before like should be false")
	}

	// 点赞 → 200，liked=true。
	res := reviewCall(t, base, "POST", "/likes", token, map[string]any{"product_id": productID})
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("like: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	if d := decodeData[likeResData](t, res.Data); !d.Liked {
		t.Fatalf("like response liked should be true")
	}

	// 归属本人。
	uid, err := g.DB().Model("product_likes").Ctx(context.Background()).Fields("user_id").Where("product_id", productID).Value()
	if err != nil || uid == nil || uid.Int64() != userID {
		t.Fatalf("like user_id=%v want %d", uid, userID)
	}

	// 点赞后 check=true。
	c1 := reviewCall(t, base, "GET", fmt.Sprintf("/likes/check?product_id=%d", productID), token, nil)
	if cd := decodeData[likeCheckData](t, c1.Data); !cd.Liked {
		t.Fatalf("check after like should be true")
	}
}

// TestLikeDuplicateIdempotent 覆盖 AC-003/INV-002：重复点赞幂等成功，仅一条。
func TestLikeDuplicateIdempotent(t *testing.T) {
	base := setupLikeServer(t)
	categoryID := orderInsertCategory(t, "点赞重复分类")
	productID := orderInsertProduct(t, categoryID, "点赞重复商品", 1)

	_, token := likeUser(t, base, "likeuser2")
	body := map[string]any{"product_id": productID}
	if first := reviewCall(t, base, "POST", "/likes", token, body); first.Status != 200 || first.Code != 0 {
		t.Fatalf("first: status=%d code=%d", first.Status, first.Code)
	}
	if second := reviewCall(t, base, "POST", "/likes", token, body); second.Status != 200 || second.Code != 0 {
		t.Fatalf("dup: status=%d code=%d", second.Status, second.Code)
	}
	if likeCount(t) != 1 {
		t.Fatalf("count=%d want 1", likeCount(t))
	}
}

// TestLikeConcurrentDuplicateSingleRow 覆盖 AC-003/INV-002（并发）：
// 并发点赞同一商品均幂等成功，且至多一条记录（uk_user_product 兜底）。
func TestLikeConcurrentDuplicateSingleRow(t *testing.T) {
	base := setupLikeServer(t)
	categoryID := orderInsertCategory(t, "点赞并发分类")
	productID := orderInsertProduct(t, categoryID, "点赞并发商品", 1)

	_, token := likeUser(t, base, "likeuser3")

	const n = 8
	var wg sync.WaitGroup
	results := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := reviewCall(t, base, "POST", "/likes", token, map[string]any{"product_id": productID})
			results <- (res.Status == 200 && res.Code == 0)
		}()
	}
	wg.Wait()
	close(results)
	for r := range results {
		if !r {
			t.Fatalf("concurrent like should all be idempotent success")
		}
	}
	if likeCount(t) != 1 {
		t.Fatalf("count=%d want 1", likeCount(t))
	}
}

// TestLikeCancelAndCheck 覆盖 AC-002/AC-005 与 INV-004：
// 点赞后取消 → check=false 且库中删除；取消未点赞商品幂等成功。
func TestLikeCancelAndCheck(t *testing.T) {
	base := setupLikeServer(t)
	categoryID := orderInsertCategory(t, "点赞取消分类")
	productID := orderInsertProduct(t, categoryID, "点赞取消商品", 1)

	_, token := likeUser(t, base, "likeuser4")

	if a := reviewCall(t, base, "POST", "/likes", token, map[string]any{"product_id": productID}); a.Status != 200 {
		t.Fatalf("like: %d", a.Status)
	}

	rm := reviewCall(t, base, "DELETE", fmt.Sprintf("/likes/%d", productID), token, nil)
	if rm.Status != 200 || rm.Code != 0 {
		t.Fatalf("cancel: status=%d code=%d", rm.Status, rm.Code)
	}
	if likeCount(t) != 0 {
		t.Fatalf("after cancel count=%d want 0", likeCount(t))
	}

	c := reviewCall(t, base, "GET", fmt.Sprintf("/likes/check?product_id=%d", productID), token, nil)
	if cd := decodeData[likeCheckData](t, c.Data); cd.Liked {
		t.Fatalf("check after cancel should be false")
	}

	// 取消未点赞的商品 → 幂等成功（no-op）。
	rm2 := reviewCall(t, base, "DELETE", fmt.Sprintf("/likes/%d", productID), token, nil)
	if rm2.Status != 200 || rm2.Code != 0 {
		t.Fatalf("cancel nonexistent: status=%d code=%d", rm2.Status, rm2.Code)
	}
}

// TestLikeCountPublic 覆盖 AC-004/INV-006：公开点赞数无需 token，随点赞/取消正确变化，且不存在的商品为 0。
func TestLikeCountPublic(t *testing.T) {
	base := setupLikeServer(t)
	categoryID := orderInsertCategory(t, "点赞计数分类")
	productID := orderInsertProduct(t, categoryID, "点赞计数商品", 1)

	_, tokenA := likeUser(t, base, "likecounta")
	_, tokenB := likeUser(t, base, "likecountb")

	// 初始公开计数 = 0（无 token）。
	c0 := reviewCall(t, base, "GET", fmt.Sprintf("/likes/count?product_id=%d", productID), "", nil)
	if c0.Status != 200 || c0.Code != 0 {
		t.Fatalf("count initial: status=%d code=%d", c0.Status, c0.Code)
	}
	if cd := decodeData[likeCountData](t, c0.Data); cd.Count != 0 {
		t.Fatalf("count initial=%d want 0", cd.Count)
	}

	// A 点赞 → 1。
	reviewCall(t, base, "POST", "/likes", tokenA, map[string]any{"product_id": productID})
	c1 := reviewCall(t, base, "GET", fmt.Sprintf("/likes/count?product_id=%d", productID), "", nil)
	if cd := decodeData[likeCountData](t, c1.Data); cd.Count != 1 {
		t.Fatalf("count after A=%d want 1", cd.Count)
	}

	// B 点赞 → 2。
	reviewCall(t, base, "POST", "/likes", tokenB, map[string]any{"product_id": productID})
	c2 := reviewCall(t, base, "GET", fmt.Sprintf("/likes/count?product_id=%d", productID), "", nil)
	if cd := decodeData[likeCountData](t, c2.Data); cd.Count != 2 {
		t.Fatalf("count after B=%d want 2", cd.Count)
	}

	// A 取消 → 1。
	reviewCall(t, base, "DELETE", fmt.Sprintf("/likes/%d", productID), tokenA, nil)
	c3 := reviewCall(t, base, "GET", fmt.Sprintf("/likes/count?product_id=%d", productID), "", nil)
	if cd := decodeData[likeCountData](t, c3.Data); cd.Count != 1 {
		t.Fatalf("count after cancel=%d want 1", cd.Count)
	}

	// 不存在的商品 → 0（公开计数不校验商品存在性）。
	cn := reviewCall(t, base, "GET", "/likes/count?product_id=999999", "", nil)
	if cn.Status != 200 || cn.Code != 0 {
		t.Fatalf("count nonexistent: status=%d code=%d", cn.Status, cn.Code)
	}
	if cd := decodeData[likeCountData](t, cn.Data); cd.Count != 0 {
		t.Fatalf("count nonexistent=%d want 0", cd.Count)
	}
}

// TestLikeUserIsolation 覆盖 AC-006/INV-001：用户 B check=false，
// 取消 A 的点赞幂等成功但不删除 A 的记录（越权无写入）。
func TestLikeUserIsolation(t *testing.T) {
	base := setupLikeServer(t)
	categoryID := orderInsertCategory(t, "点赞隔离分类")
	productID := orderInsertProduct(t, categoryID, "点赞隔离商品", 1)

	ownerID, ownerToken := likeUser(t, base, "likeowner")
	if a := reviewCall(t, base, "POST", "/likes", ownerToken, map[string]any{"product_id": productID}); a.Status != 200 {
		t.Fatalf("owner like: %d", a.Status)
	}

	_, intruderToken := likeUser(t, base, "likeintruder")

	// B check=false。
	c := reviewCall(t, base, "GET", fmt.Sprintf("/likes/check?product_id=%d", productID), intruderToken, nil)
	if cd := decodeData[likeCheckData](t, c.Data); cd.Liked {
		t.Fatalf("intruder check should be false")
	}

	// B 取消 A 的点赞 → 幂等成功（不报错），但不删除 A 的记录。
	rm := reviewCall(t, base, "DELETE", fmt.Sprintf("/likes/%d", productID), intruderToken, nil)
	if rm.Status != 200 || rm.Code != 0 {
		t.Fatalf("intruder cancel: status=%d code=%d", rm.Status, rm.Code)
	}
	if likeCount(t) != 1 {
		t.Fatalf("intruder must not delete owner like, count=%d want 1", likeCount(t))
	}
	uid, err := g.DB().Model("product_likes").Ctx(context.Background()).Fields("user_id").Where("product_id", productID).Value()
	if err != nil || uid == nil || uid.Int64() != ownerID {
		t.Fatalf("like still owned by %v want %d", uid, ownerID)
	}
}

// TestLikeProductValidation 覆盖 AC-007/INV-003：不存在 404/4001、off_shelf/draft 409/13001、
// 非法 product_id 400/1001，均无写入。
func TestLikeProductValidation(t *testing.T) {
	base := setupLikeServer(t)
	categoryID := orderInsertCategory(t, "点赞校验分类")
	draftID := orderInsertProduct(t, categoryID, "草稿商品", 0)
	offID := orderInsertProduct(t, categoryID, "下架商品", 2)

	_, token := likeUser(t, base, "likevalid")

	miss := reviewCall(t, base, "POST", "/likes", token, map[string]any{"product_id": 999999})
	if miss.Status != 404 || miss.Code != 4001 {
		t.Fatalf("nonexistent: status=%d code=%d want 404/4001", miss.Status, miss.Code)
	}
	off := reviewCall(t, base, "POST", "/likes", token, map[string]any{"product_id": offID})
	if off.Status != 409 || off.Code != 13001 {
		t.Fatalf("off_shelf: status=%d code=%d want 409/13001", off.Status, off.Code)
	}
	draft := reviewCall(t, base, "POST", "/likes", token, map[string]any{"product_id": draftID})
	if draft.Status != 409 || draft.Code != 13001 {
		t.Fatalf("draft: status=%d code=%d want 409/13001", draft.Status, draft.Code)
	}
	bad := reviewCall(t, base, "POST", "/likes", token, map[string]any{"product_id": 0})
	if bad.Status != 400 || bad.Code != 1001 {
		t.Fatalf("bad product_id: status=%d code=%d want 400/1001", bad.Status, bad.Code)
	}
	if likeCount(t) != 0 {
		t.Fatalf("invalid must not write, count=%d", likeCount(t))
	}
}

// TestLikeRequiresAuth 覆盖 AC-008：无 token/非法 token 访问点赞写/本人状态接口均 401/1002 且无写入；
// 公开计数接口无需 token 正常返回。
func TestLikeRequiresAuth(t *testing.T) {
	base := setupLikeServer(t)
	categoryID := orderInsertCategory(t, "点赞认证分类")
	productID := orderInsertProduct(t, categoryID, "点赞认证商品", 1)

	cases := []struct {
		method string
		path   string
		body   any
	}{
		{"POST", "/likes", map[string]any{"product_id": productID}},
		{"GET", fmt.Sprintf("/likes/check?product_id=%d", productID), nil},
		{"DELETE", fmt.Sprintf("/likes/%d", productID), nil},
	}
	for _, tc := range cases {
		res := reviewCall(t, base, tc.method, tc.path, "", tc.body)
		if res.Status != 401 || res.Code != 1002 {
			t.Fatalf("%s %s: status=%d code=%d want 401/1002", tc.method, tc.path, res.Status, res.Code)
		}
	}
	if likeCount(t) != 0 {
		t.Fatalf("unauthenticated must not write, count=%d", likeCount(t))
	}

	// 非法 token。
	res := reviewCall(t, base, "GET", fmt.Sprintf("/likes/check?product_id=%d", productID), "invalid-token", nil)
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("invalid token: status=%d code=%d want 401/1002", res.Status, res.Code)
	}

	// 公开计数无需 token。
	pub := reviewCall(t, base, "GET", fmt.Sprintf("/likes/count?product_id=%d", productID), "", nil)
	if pub.Status != 200 || pub.Code != 0 {
		t.Fatalf("public count: status=%d code=%d want 200/0", pub.Status, pub.Code)
	}
}
