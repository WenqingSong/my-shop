package cmd

// 本文件通过真实路由（RegisterFrontendRoutes）+ 真实 MySQL/Redis 锁定「商品收藏」核心闭环。
// 覆盖 product-favorite-v1 的 AC-001 至 AC-008 与关键不变量。

import (
	"context"
	"fmt"
	"sync"
	"testing"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"
)

// favoriteItemData 是收藏条目（添加/列表返回）。
type favoriteItemData struct {
	Id                int64  `json:"id"`
	ProductId         int64  `json:"product_id"`
	ProductName       string `json:"product_name"`
	ProductMainImage  string `json:"product_main_image"`
	ProductPrice      *int64 `json:"product_price"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason"`
}

// favoriteListData 是收藏列表响应。
type favoriteListData struct {
	Items []favoriteItemData `json:"items"`
	Total int                `json:"total"`
	Page  int                `json:"page"`
	Size  int                `json:"size"`
}

// favoriteCheckData 是是否已收藏响应。
type favoriteCheckData struct {
	Favorited bool `json:"favorited"`
}

// setupFavoriteServer 建立隔离的收藏测试环境：复用订单域初始化后清空 favorites 表。
func setupFavoriteServer(t *testing.T) string {
	t.Helper()
	base := setupOrderServer(t)
	if _, err := g.DB().Exec(context.Background(), "DELETE FROM favorites"); err != nil {
		t.Fatalf("clean favorites: %v", err)
	}
	return base
}

// favoriteUser 创建前台用户并登录，返回 (userID, token)。
func favoriteUser(t *testing.T, base, username string) (int64, string) {
	t.Helper()
	isoInsertUser(t, username, "favoritepass123")
	token, _ := isoFrontendLogin(t, base, username, "favoritepass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	return userID, token
}

// favoriteCount 统计 favorites 表当前行数。
func favoriteCount(t *testing.T) int {
	t.Helper()
	n, err := g.DB().Model("favorites").Ctx(context.Background()).Count()
	if err != nil {
		t.Fatalf("count favorites: %v", err)
	}
	return n
}

// TestFavoriteAddAndList 覆盖 AC-001/AC-004 与 INV-001/INV-005：
// 登录用户收藏 on_shelf 商品落库且归属本人、列表可见，实时联查商品信息与收藏时间。
func TestFavoriteAddAndList(t *testing.T) {
	base := setupFavoriteServer(t)
	categoryID := orderInsertCategory(t, "收藏分类")
	productID := orderInsertProduct(t, categoryID, "收藏商品", 1)

	userID, token := favoriteUser(t, base, "favuser1")

	res := reviewCall(t, base, "POST", "/favorites", token, map[string]any{"product_id": productID})
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("add: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	item := decodeData[favoriteItemData](t, res.Data)
	if item.ProductId != productID {
		t.Fatalf("product_id=%d want %d", item.ProductId, productID)
	}
	if !item.Available || item.UnavailableReason != "" {
		t.Fatalf("available=%t reason=%q want true/empty", item.Available, item.UnavailableReason)
	}
	if item.ProductName == "" || item.ProductPrice == nil {
		t.Fatalf("product info not joined: name=%q price=%v", item.ProductName, item.ProductPrice)
	}

	// 归属本人。
	uid, err := g.DB().Model("favorites").Ctx(context.Background()).Fields("user_id").Where("product_id", productID).Value()
	if err != nil || uid == nil || uid.Int64() != userID {
		t.Fatalf("favorite user_id=%v want %d", uid, userID)
	}

	// 列表可见、分页字段正确。
	lr := reviewCall(t, base, "GET", "/favorites", token, nil)
	if lr.Status != 200 || lr.Code != 0 {
		t.Fatalf("list: status=%d code=%d", lr.Status, lr.Code)
	}
	ld := decodeData[favoriteListData](t, lr.Data)
	if ld.Total != 1 || len(ld.Items) != 1 {
		t.Fatalf("total=%d items=%d want 1", ld.Total, len(ld.Items))
	}
	if ld.Items[0].ProductId != productID || !ld.Items[0].Available || ld.Items[0].Id != item.Id {
		t.Fatalf("list item mismatch: %+v", ld.Items[0])
	}
}

// TestFavoriteDuplicateIdempotent 覆盖 AC-002/INV-002：重复收藏幂等成功，仅一条。
func TestFavoriteDuplicateIdempotent(t *testing.T) {
	base := setupFavoriteServer(t)
	categoryID := orderInsertCategory(t, "收藏重复分类")
	productID := orderInsertProduct(t, categoryID, "收藏重复商品", 1)

	_, token := favoriteUser(t, base, "favuser2")
	body := map[string]any{"product_id": productID}
	if first := reviewCall(t, base, "POST", "/favorites", token, body); first.Status != 200 || first.Code != 0 {
		t.Fatalf("first: status=%d code=%d", first.Status, first.Code)
	}
	if second := reviewCall(t, base, "POST", "/favorites", token, body); second.Status != 200 || second.Code != 0 {
		t.Fatalf("dup: status=%d code=%d", second.Status, second.Code)
	}
	if favoriteCount(t) != 1 {
		t.Fatalf("count=%d want 1", favoriteCount(t))
	}
}

// TestFavoriteConcurrentDuplicateSingleRow 覆盖 AC-002/INV-002（并发）：
// 并发收藏同一商品均幂等成功（区别于 review 的「仅一次成功」），且至多一条记录。
func TestFavoriteConcurrentDuplicateSingleRow(t *testing.T) {
	base := setupFavoriteServer(t)
	categoryID := orderInsertCategory(t, "收藏并发分类")
	productID := orderInsertProduct(t, categoryID, "收藏并发商品", 1)

	_, token := favoriteUser(t, base, "favuser3")

	const n = 8
	var wg sync.WaitGroup
	results := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := reviewCall(t, base, "POST", "/favorites", token, map[string]any{"product_id": productID})
			results <- (res.Status == 200 && res.Code == 0)
		}()
	}
	wg.Wait()
	close(results)
	for r := range results {
		if !r {
			t.Fatalf("concurrent add should all be idempotent success")
		}
	}
	if favoriteCount(t) != 1 {
		t.Fatalf("count=%d want 1", favoriteCount(t))
	}
}

// TestFavoriteRemoveAndCheck 覆盖 AC-003/AC-005 与 INV-004：
// 收藏后 check=true、取消后 check=false 且库中删除；取消未收藏商品幂等成功。
func TestFavoriteRemoveAndCheck(t *testing.T) {
	base := setupFavoriteServer(t)
	categoryID := orderInsertCategory(t, "收藏取消分类")
	productID := orderInsertProduct(t, categoryID, "收藏取消商品", 1)

	_, token := favoriteUser(t, base, "favuser4")

	// 未收藏时 check=false。
	c0 := reviewCall(t, base, "GET", fmt.Sprintf("/favorites/check?product_id=%d", productID), token, nil)
	if c0.Status != 200 || c0.Code != 0 {
		t.Fatalf("check before: status=%d code=%d", c0.Status, c0.Code)
	}
	if cd := decodeData[favoriteCheckData](t, c0.Data); cd.Favorited {
		t.Fatalf("check before add should be false")
	}

	// 收藏后 check=true。
	if a := reviewCall(t, base, "POST", "/favorites", token, map[string]any{"product_id": productID}); a.Status != 200 {
		t.Fatalf("add: %d", a.Status)
	}
	c1 := reviewCall(t, base, "GET", fmt.Sprintf("/favorites/check?product_id=%d", productID), token, nil)
	if cd := decodeData[favoriteCheckData](t, c1.Data); !cd.Favorited {
		t.Fatalf("check after add should be true")
	}

	// 取消收藏 → 200，库中删除。
	rm := reviewCall(t, base, "DELETE", fmt.Sprintf("/favorites/%d", productID), token, nil)
	if rm.Status != 200 || rm.Code != 0 {
		t.Fatalf("remove: status=%d code=%d", rm.Status, rm.Code)
	}
	if favoriteCount(t) != 0 {
		t.Fatalf("after remove count=%d want 0", favoriteCount(t))
	}

	// 取消后 check=false。
	c2 := reviewCall(t, base, "GET", fmt.Sprintf("/favorites/check?product_id=%d", productID), token, nil)
	if cd := decodeData[favoriteCheckData](t, c2.Data); cd.Favorited {
		t.Fatalf("check after remove should be false")
	}

	// 取消未收藏的商品 → 幂等成功（no-op）。
	rm2 := reviewCall(t, base, "DELETE", fmt.Sprintf("/favorites/%d", productID), token, nil)
	if rm2.Status != 200 || rm2.Code != 0 {
		t.Fatalf("remove nonexistent: status=%d code=%d", rm2.Status, rm2.Code)
	}
}

// TestFavoriteUserIsolation 覆盖 AC-006/INV-001：用户 B 列表为空、check=false，
// 取消 A 的收藏幂等成功但不删除 A 的记录（越权无写入）。
func TestFavoriteUserIsolation(t *testing.T) {
	base := setupFavoriteServer(t)
	categoryID := orderInsertCategory(t, "收藏隔离分类")
	productID := orderInsertProduct(t, categoryID, "收藏隔离商品", 1)

	ownerID, ownerToken := favoriteUser(t, base, "favowner")
	if a := reviewCall(t, base, "POST", "/favorites", ownerToken, map[string]any{"product_id": productID}); a.Status != 200 {
		t.Fatalf("owner add: %d", a.Status)
	}

	_, intruderToken := favoriteUser(t, base, "favintruder")

	// B 列表为空。
	lr := reviewCall(t, base, "GET", "/favorites", intruderToken, nil)
	if ld := decodeData[favoriteListData](t, lr.Data); ld.Total != 0 || len(ld.Items) != 0 {
		t.Fatalf("intruder list should be empty, total=%d", ld.Total)
	}
	// B check=false。
	c := reviewCall(t, base, "GET", fmt.Sprintf("/favorites/check?product_id=%d", productID), intruderToken, nil)
	if cd := decodeData[favoriteCheckData](t, c.Data); cd.Favorited {
		t.Fatalf("intruder check should be false")
	}
	// B 取消 A 的收藏 → 幂等成功（不报错），但不删除 A 的记录。
	rm := reviewCall(t, base, "DELETE", fmt.Sprintf("/favorites/%d", productID), intruderToken, nil)
	if rm.Status != 200 || rm.Code != 0 {
		t.Fatalf("intruder remove: status=%d code=%d", rm.Status, rm.Code)
	}
	if favoriteCount(t) != 1 {
		t.Fatalf("intruder must not delete owner favorite, count=%d want 1", favoriteCount(t))
	}
	uid, err := g.DB().Model("favorites").Ctx(context.Background()).Fields("user_id").Where("product_id", productID).Value()
	if err != nil || uid == nil || uid.Int64() != ownerID {
		t.Fatalf("favorite still owned by %v want %d", uid, ownerID)
	}
}

// TestFavoriteProductValidation 覆盖 AC-007/INV-003：不存在 404/4001、off_shelf/draft 409/11001、
// 非法 product_id 400/1001，均无写入。
func TestFavoriteProductValidation(t *testing.T) {
	base := setupFavoriteServer(t)
	categoryID := orderInsertCategory(t, "收藏校验分类")
	draftID := orderInsertProduct(t, categoryID, "草稿商品", 0)
	offID := orderInsertProduct(t, categoryID, "下架商品", 2)

	_, token := favoriteUser(t, base, "favvalid")

	miss := reviewCall(t, base, "POST", "/favorites", token, map[string]any{"product_id": 999999})
	if miss.Status != 404 || miss.Code != 4001 {
		t.Fatalf("nonexistent: status=%d code=%d want 404/4001", miss.Status, miss.Code)
	}
	off := reviewCall(t, base, "POST", "/favorites", token, map[string]any{"product_id": offID})
	if off.Status != 409 || off.Code != 11001 {
		t.Fatalf("off_shelf: status=%d code=%d want 409/11001", off.Status, off.Code)
	}
	draft := reviewCall(t, base, "POST", "/favorites", token, map[string]any{"product_id": draftID})
	if draft.Status != 409 || draft.Code != 11001 {
		t.Fatalf("draft: status=%d code=%d want 409/11001", draft.Status, draft.Code)
	}
	bad := reviewCall(t, base, "POST", "/favorites", token, map[string]any{"product_id": 0})
	if bad.Status != 400 || bad.Code != 1001 {
		t.Fatalf("bad product_id: status=%d code=%d want 400/1001", bad.Status, bad.Code)
	}
	if favoriteCount(t) != 0 {
		t.Fatalf("invalid must not write, count=%d", favoriteCount(t))
	}
}

// TestFavoriteRequiresAuth 覆盖 AC-008：无 token/非法 token 访问收藏接口均 401/1002 且无数据。
func TestFavoriteRequiresAuth(t *testing.T) {
	base := setupFavoriteServer(t)
	categoryID := orderInsertCategory(t, "收藏认证分类")
	productID := orderInsertProduct(t, categoryID, "收藏认证商品", 1)

	cases := []struct {
		method string
		path   string
		body   any
	}{
		{"POST", "/favorites", map[string]any{"product_id": productID}},
		{"GET", "/favorites", nil},
		{"GET", fmt.Sprintf("/favorites/check?product_id=%d", productID), nil},
		{"DELETE", fmt.Sprintf("/favorites/%d", productID), nil},
	}
	for _, tc := range cases {
		res := reviewCall(t, base, tc.method, tc.path, "", tc.body)
		if res.Status != 401 || res.Code != 1002 {
			t.Fatalf("%s %s: status=%d code=%d want 401/1002", tc.method, tc.path, res.Status, res.Code)
		}
	}
	if favoriteCount(t) != 0 {
		t.Fatalf("unauthenticated must not write, count=%d", favoriteCount(t))
	}

	// 非法 token。
	res := reviewCall(t, base, "GET", "/favorites", "invalid-token", nil)
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("invalid token: status=%d code=%d want 401/1002", res.Status, res.Code)
	}
}

// TestFavoriteUnavailableFlag 覆盖 INV-005：商品下架/删除后收藏保留，列表实时标识 off_shelf/product_deleted。
func TestFavoriteUnavailableFlag(t *testing.T) {
	base := setupFavoriteServer(t)
	categoryID := orderInsertCategory(t, "收藏可用性分类")
	productID := orderInsertProduct(t, categoryID, "收藏可用性商品", 1)

	_, token := favoriteUser(t, base, "favavail")
	if a := reviewCall(t, base, "POST", "/favorites", token, map[string]any{"product_id": productID}); a.Status != 200 {
		t.Fatalf("add: %d", a.Status)
	}

	// 下架 → off_shelf。
	if _, err := g.DB().Model("products").Ctx(context.Background()).Where("id", productID).Data(g.Map{"status": 2}).Update(); err != nil {
		t.Fatalf("off shelf product: %v", err)
	}
	lr := reviewCall(t, base, "GET", "/favorites", token, nil)
	ld := decodeData[favoriteListData](t, lr.Data)
	if len(ld.Items) != 1 || ld.Items[0].Available || ld.Items[0].UnavailableReason != "off_shelf" {
		t.Fatalf("off_shelf flag mismatch: %+v", ld.Items)
	}

	// 删除 → product_deleted，商品信息为空/价格为 null。
	if _, err := g.DB().Exec(context.Background(), "DELETE FROM products WHERE id = ?", productID); err != nil {
		t.Fatalf("delete product: %v", err)
	}
	lr = reviewCall(t, base, "GET", "/favorites", token, nil)
	ld = decodeData[favoriteListData](t, lr.Data)
	if len(ld.Items) != 1 || ld.Items[0].Available || ld.Items[0].UnavailableReason != "product_deleted" {
		t.Fatalf("deleted flag mismatch: %+v", ld.Items)
	}
	if ld.Items[0].ProductPrice != nil || ld.Items[0].ProductName != "" {
		t.Fatalf("deleted product should have empty name/nil price: %+v", ld.Items[0])
	}
}
