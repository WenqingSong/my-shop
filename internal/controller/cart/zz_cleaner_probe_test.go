package cart_test

import (
	"fmt"
	"testing"
)

// Cleaner 临时探针：验证「设置与当前相同的数量/勾选状态」是否错误返回 404。
func TestCleanerProbeSameValueUpdate(t *testing.T) {
	base, adminToken := setupCartServer(t)
	userToken, _ := registerLoginUser(t, base, "probeuser", "password123")
	skuID, _ := newAddableSku(t, base, adminToken)

	assertOK(t, addCart(t, base, userToken, skuID, 5), "add")
	items := listCart(t, base, userToken)
	itemID := items[0].Id

	// 相同数量。
	res := doRequest(t, base, "PUT", fmt.Sprintf("/cart/items/%d", itemID), map[string]any{"quantity": 5}, authHeader(userToken))
	t.Logf("same-quantity update -> status=%d code=%d msg=%q", res.Status, res.Code, res.Message)

	// 相同勾选状态（当前 selected=true，再设 true）。
	res = doRequest(t, base, "PUT", fmt.Sprintf("/cart/items/%d/selected", itemID), map[string]any{"selected": true}, authHeader(userToken))
	t.Logf("same-selected update -> status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
}
