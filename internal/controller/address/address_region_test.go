package address_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/gogf/gf/v2/frame/g"
)

// addrRegionJSON 带结构化地区字段的地址响应。
type addrRegionJSON struct {
	Id            int64  `json:"id"`
	RecipientName string `json:"recipient_name"`
	Phone         string `json:"phone"`
	Province      string `json:"province"`
	City          string `json:"city"`
	District      string `json:"district"`
	RegionId      *int64 `json:"region_id"`
	Detail        string `json:"detail"`
	IsDefault     bool   `json:"is_default"`
}

// createAddressRegion 创建地址并解析带 region_id 的响应。
func createAddressRegion(t *testing.T, base, token string, body map[string]any) addrRegionJSON {
	t.Helper()
	res := doRequest(t, base, "POST", "/addresses", body, authHeader(token))
	assertOK(t, res, "create address with region")
	var a addrRegionJSON
	if err := json.Unmarshal(res.Data, &a); err != nil {
		t.Fatalf("unmarshal create res: %v", err)
	}
	return a
}

// getAddressRegion 查询地址详情并解析带 region_id 的响应。
func getAddressRegion(t *testing.T, base, token string, id int64) addrRegionJSON {
	t.Helper()
	res := doRequest(t, base, "GET", fmt.Sprintf("/addresses/%d", id), nil, authHeader(token))
	assertOK(t, res, "get address detail")
	var a addrRegionJSON
	if err := json.Unmarshal(res.Data, &a); err != nil {
		t.Fatalf("unmarshal detail res: %v", err)
	}
	return a
}

// dbAddressRegionID 读取地址在 DB 中的 region_id（第二个返回值为 false 表示 NULL）。
func dbAddressRegionID(t *testing.T, id int64) (int64, bool) {
	t.Helper()
	v, err := g.DB().Model("addresses").Ctx(context.Background()).Where("id", id).Value("region_id")
	if err != nil {
		t.Fatalf("query address region_id: %v", err)
	}
	if v.IsNil() {
		return 0, false
	}
	return v.Int64(), true
}

// TestAddressRegionCreateAndUpdate 覆盖：创建/更新指定 region_id，省市区文本以字典为准，
// 列表/详情返回 region_id，传 0 清空结构化引用。
func TestAddressRegionCreateAndUpdate(t *testing.T) {
	base := setupAddressServer(t)
	insertUser(t, "addrregion", "userpass123")
	token := loginUser(t, base, "addrregion", "userpass123")

	// region_id=3（东城区，链路 1 北京市 → 2 北京市 → 3 东城区）。
	// 请求同时提交伪造的地区文本，服务端必须忽略文本、以字典为准。
	body := addrBody("张三", "13800138000")
	body["region_id"] = 3
	body["province"] = "伪造省"
	body["city"] = "伪造市"
	body["district"] = "伪造区"
	created := createAddressRegion(t, base, token, body)
	if created.RegionId == nil || *created.RegionId != 3 {
		t.Fatalf("expected region_id=3, got %+v", created)
	}
	if created.Province != "北京市" || created.City != "北京市" || created.District != "东城区" {
		t.Fatalf("region text must come from dictionary, got %+v", created)
	}
	if got, ok := dbAddressRegionID(t, created.Id); !ok || got != 3 {
		t.Fatalf("db region_id mismatch: got=%d ok=%t", got, ok)
	}

	// 更新为 region_id=203（西城区），文本随之切换。
	res := doRequest(t, base, "PUT", fmt.Sprintf("/addresses/%d", created.Id),
		map[string]any{"region_id": 203, "province": "再次伪造"}, authHeader(token))
	assertOK(t, res, "update address region")
	updated := getAddressRegion(t, base, token, created.Id)
	if updated.RegionId == nil || *updated.RegionId != 203 {
		t.Fatalf("expected region_id=203 after update, got %+v", updated)
	}
	if updated.District != "西城区" || updated.Province != "北京市" {
		t.Fatalf("region text not refreshed from dictionary: %+v", updated)
	}

	// 列表返回 region_id。
	res = doRequest(t, base, "GET", "/addresses", nil, authHeader(token))
	assertOK(t, res, "list addresses")
	var list struct {
		Items []addrRegionJSON `json:"items"`
	}
	if err := json.Unmarshal(res.Data, &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].RegionId == nil || *list.Items[0].RegionId != 203 {
		t.Fatalf("list must return region_id, got %+v", list.Items)
	}

	// region_id=0 显式清空：文本保留，DB region_id 为 NULL。
	res = doRequest(t, base, "PUT", fmt.Sprintf("/addresses/%d", created.Id),
		map[string]any{"region_id": 0}, authHeader(token))
	assertOK(t, res, "clear address region")
	cleared := getAddressRegion(t, base, token, created.Id)
	if cleared.RegionId != nil {
		t.Fatalf("expected region_id NULL after clear, got %+v", cleared)
	}
	if cleared.District != "西城区" {
		t.Fatalf("text should be preserved when only clearing region_id: %+v", cleared)
	}
	if _, ok := dbAddressRegionID(t, created.Id); ok {
		t.Fatalf("db region_id should be NULL after clear")
	}

	// 仅修改自由文本 → 退出结构化模式（region_id 保持 NULL），文本生效。
	res = doRequest(t, base, "PUT", fmt.Sprintf("/addresses/%d", created.Id),
		map[string]any{"district": "自定义区"}, authHeader(token))
	assertOK(t, res, "update free text")
	textUpdated := getAddressRegion(t, base, token, created.Id)
	if textUpdated.RegionId != nil || textUpdated.District != "自定义区" {
		t.Fatalf("free text update should clear region_id: %+v", textUpdated)
	}
}

// TestAddressRegionValidation 覆盖：不存在的 region_id、非区县层级、非法父子链 → 400/1001 且无写入。
func TestAddressRegionValidation(t *testing.T) {
	base := setupAddressServer(t)
	userID := insertUser(t, "addrregionval", "userpass123")
	token := loginUser(t, base, "addrregionval", "userpass123")

	// 构造一个 level=3 但父级为省级的非法节点（父级链路断裂）。
	const brokenRegionID = 900000001
	if _, err := g.DB().Exec(context.Background(),
		"INSERT INTO regions (id, parent_id, level, name) VALUES (?, ?, ?, ?)",
		brokenRegionID, 1, 3, "非法区县"); err != nil {
		t.Fatalf("insert broken region: %v", err)
	}
	t.Cleanup(func() {
		_, _ = g.DB().Exec(context.Background(), "DELETE FROM regions WHERE id = ?", brokenRegionID)
	})

	cases := []struct {
		name     string
		regionID any
	}{
		{"nonexistent", 99999999},
		{"province level", 1},
		{"city level", 2},
		{"broken parent chain", brokenRegionID},
		{"zero", 0},
		{"negative", -3},
	}
	for _, c := range cases {
		body := addrBody("张三", "13800138000")
		body["region_id"] = c.regionID
		res := doRequest(t, base, "POST", "/addresses", body, authHeader(token))
		if res.Status != 400 || res.Code != 1001 {
			t.Fatalf("%s: status=%d code=%d msg=%q", c.name, res.Status, res.Code, res.Message)
		}
	}
	if n := dbAddressCount(t, userID); n != 0 {
		t.Fatalf("invalid region_id must not create address, got %d rows", n)
	}

	// 更新时同样校验：已有地址写入非法 region_id → 400 且原值不变。
	a := createAddressRegion(t, base, token, addrBody("李四", "13900139000"))
	res := doRequest(t, base, "PUT", fmt.Sprintf("/addresses/%d", a.Id),
		map[string]any{"region_id": 2}, authHeader(token))
	if res.Status != 400 || res.Code != 1001 {
		t.Fatalf("update with city-level region: status=%d code=%d", res.Status, res.Code)
	}
	if got := getAddressRegion(t, base, token, a.Id); got.RegionId != nil {
		t.Fatalf("failed update must not change region_id: %+v", got)
	}
}

// TestAddressLegacyNullRegion 覆盖：历史地址 region_id 为 NULL 时正常读取（详情与列表）。
func TestAddressLegacyNullRegion(t *testing.T) {
	base := setupAddressServer(t)
	userID := insertUser(t, "addrlegacy", "userpass123")
	token := loginUser(t, base, "addrlegacy", "userpass123")

	// 直接写入一条历史地址（无 region_id）。
	id, err := g.DB().Model("addresses").Ctx(context.Background()).Data(g.Map{
		"user_id":        userID,
		"recipient_name": "历史用户",
		"phone":          "13700137000",
		"province":       "广东省",
		"city":           "深圳市",
		"district":       "南山区",
		"detail":         "科技园 1 号",
		"is_default":     1,
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert legacy address: %v", err)
	}
	if _, ok := dbAddressRegionID(t, id); ok {
		t.Fatalf("legacy address should have NULL region_id")
	}

	detail := getAddressRegion(t, base, token, id)
	if detail.RegionId != nil {
		t.Fatalf("legacy detail region_id should be null: %+v", detail)
	}
	if detail.Province != "广东省" || detail.District != "南山区" || !detail.IsDefault {
		t.Fatalf("legacy detail content mismatch: %+v", detail)
	}

	res := doRequest(t, base, "GET", "/addresses", nil, authHeader(token))
	assertOK(t, res, "list legacy addresses")
	var list struct {
		Items []addrRegionJSON `json:"items"`
	}
	if err := json.Unmarshal(res.Data, &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].RegionId != nil {
		t.Fatalf("legacy list should return region_id null: %+v", list.Items)
	}
}

// TestAddressDefaultUniqueAcrossUsers 覆盖：创建/切换默认地址后唯一性，且不同用户互不影响。
func TestAddressDefaultUniqueAcrossUsers(t *testing.T) {
	base := setupAddressServer(t)
	userA := insertUser(t, "adefA", "userpass123")
	userB := insertUser(t, "adefB", "userpass123")
	tokenA := loginUser(t, base, "adefA", "userpass123")
	tokenB := loginUser(t, base, "adefB", "userpass123")

	// A 首条自动默认；B 首条也自动默认（互不影响）。
	a1 := createAddressRegion(t, base, tokenA, addrBody("A一", "13800138000"))
	b1 := createAddressRegion(t, base, tokenB, addrBody("B一", "13900139000"))
	if !a1.IsDefault || !b1.IsDefault {
		t.Fatalf("first address of each user should be default: A=%+v B=%+v", a1, b1)
	}

	// A 第二条并显式设默认 → A 的旧默认取消，B 的默认不受影响。
	bodyA2 := addrBody("A二", "13700137000")
	bodyA2["region_id"] = 3
	bodyA2["is_default"] = true
	a2 := createAddressRegion(t, base, tokenA, bodyA2)
	if !a2.IsDefault {
		t.Fatalf("explicit default second address should be default: %+v", a2)
	}
	if got := dbDefaultCount(t, userA); got != 1 {
		t.Fatalf("user A should have exactly 1 default, got %d", got)
	}
	if got := dbDefaultCount(t, userB); got != 1 {
		t.Fatalf("user B default must be untouched, got %d", got)
	}
	if got := getAddressRegion(t, base, tokenA, a1.Id); got.IsDefault {
		t.Fatalf("A first address should no longer be default: %+v", got)
	}

	// A 把第一条再设默认 → 第二条取消，仍恰好 1 个。
	res := doRequest(t, base, "PUT", fmt.Sprintf("/addresses/%d", a1.Id),
		map[string]any{"is_default": true}, authHeader(tokenA))
	assertOK(t, res, "switch default back to a1")
	if got := dbDefaultCount(t, userA); got != 1 {
		t.Fatalf("user A should still have exactly 1 default, got %d", got)
	}
	if got := getAddressRegion(t, base, tokenA, a2.Id); got.IsDefault {
		t.Fatalf("A second address should be non-default: %+v", got)
	}
}

// TestAddressDefaultConcurrencyWithLock 覆盖：并发「创建首条地址」与「切换默认地址」混合场景，
// 最终数据库状态必须满足每用户恰好一个默认地址（不只校验 HTTP 响应）。
func TestAddressDefaultConcurrencyWithLock(t *testing.T) {
	base := setupAddressServer(t)
	userID := insertUser(t, "addrlock", "userpass123")
	token := loginUser(t, base, "addrlock", "userpass123")

	// 阶段一：并发创建首条地址，仅应有一个成为默认（users 行锁串行化 count 判定）。
	const creators = 12
	var wg sync.WaitGroup
	createCodes := make(chan int, creators)
	for i := 0; i < creators; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := addrBody(fmt.Sprintf("并发创建%d", i), fmt.Sprintf("1380013%04d", i))
			r, err := request(base, "POST", "/addresses", body, authHeader(token))
			if err != nil {
				createCodes <- -1
				return
			}
			createCodes <- r.Code
		}(i)
	}
	wg.Wait()
	close(createCodes)
	for code := range createCodes {
		if code != 0 {
			t.Fatalf("concurrent create failed with code %d", code)
		}
	}
	if got := dbAddressCount(t, userID); got != creators {
		t.Fatalf("expected %d addresses, got %d", creators, got)
	}
	if got := dbDefaultCount(t, userID); got != 1 {
		t.Fatalf("expected exactly 1 default after concurrent first-create, got %d", got)
	}

	// 阶段二：取全部地址，并发设置不同地址为默认。
	rows, err := g.DB().Model("addresses").Ctx(context.Background()).Where("user_id", userID).Fields("id").Order("id").All()
	if err != nil {
		t.Fatalf("query address ids: %v", err)
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row["id"].Int64())
	}

	const setters = 24
	var wg2 sync.WaitGroup
	setCodes := make(chan int, setters)
	for i := 0; i < setters; i++ {
		wg2.Add(1)
		go func(i int) {
			defer wg2.Done()
			target := ids[i%len(ids)]
			r, err := request(base, "PUT", fmt.Sprintf("/addresses/%d", target),
				map[string]any{"is_default": true}, authHeader(token))
			if err != nil {
				setCodes <- -1
				return
			}
			setCodes <- r.Code
		}(i)
	}
	wg2.Wait()
	close(setCodes)
	for code := range setCodes {
		if code != 0 && code != 7002 {
			t.Fatalf("unexpected concurrent set-default code %d", code)
		}
	}

	// 最终数据库状态：恰好一个默认地址（DB 唯一约束 + 行锁共同保证）。
	if got := dbDefaultCount(t, userID); got != 1 {
		t.Fatalf("expected exactly 1 default after concurrent switches, got %d", got)
	}
	// 并发后默认地址的 default_key 必须等于 user_id（生成列语义未被破坏）。
	var defaultKeys []string
	rows, err = g.DB().Model("addresses").Ctx(context.Background()).
		Where("user_id", userID).Where("is_default", 1).Fields("default_key").All()
	if err != nil {
		t.Fatalf("query default_key: %v", err)
	}
	for _, row := range rows {
		defaultKeys = append(defaultKeys, row["default_key"].String())
	}
	if len(defaultKeys) != 1 || defaultKeys[0] != fmt.Sprintf("%d", userID) {
		t.Fatalf("default_key should equal user_id for the single default, got %v", defaultKeys)
	}
}
