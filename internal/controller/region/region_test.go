package region_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/gclient"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/cmd"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
)

// safeTestDB 本任务专用独立测试库；地区字典为只读数据，测试不 DELETE regions。
const safeTestDB = "my_shop_b104_test"

const (
	testJWTSecret     = "test-secret-0123456789-0123456789-0123456789"
	testAdminPassword = "test-admin-password-123"
	// expectedRegionTotal B1-03-04 导入的省/市/区节点总数。
	expectedRegionTotal = 3938
)

type apiResult struct {
	Status  int
	Code    int
	Message string
	Data    json.RawMessage
}

type regionJSON struct {
	Id       int64  `json:"id"`
	ParentId int64  `json:"parent_id"`
	Level    int    `json:"level"`
	Name     string `json:"name"`
}

type treeNodeJSON struct {
	regionJSON
	Children []*treeNodeJSON `json:"children"`
}

type treeJSON struct {
	Items []*treeNodeJSON `json:"items"`
	Total int             `json:"total"`
}

func decode(r *gclient.Response) apiResult {
	var env struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(r.ReadAll(), &env)
	return apiResult{Status: r.StatusCode, Code: env.Code, Message: env.Message, Data: env.Data}
}

func doGet(t *testing.T, base, path string) apiResult {
	t.Helper()
	r, err := g.Client().Get(context.Background(), base+path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer r.Close()
	return decode(r)
}

func assertOK(t *testing.T, res apiResult, op string) {
	t.Helper()
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("%s: status=%d code=%d msg=%q", op, res.Status, res.Code, res.Message)
	}
}

// requireIsolatedTestDB 写库（Bootstrap seed/migration）前核实目标库。
func requireIsolatedTestDB(t *testing.T) {
	t.Helper()
	if got := os.Getenv("DATABASE_DEFAULT_NAME"); got != safeTestDB {
		t.Fatalf("拒绝在非测试库执行：DATABASE_DEFAULT_NAME=%q，必须为 %q", got, safeTestDB)
	}
}

func requireActualDB(t *testing.T) {
	t.Helper()
	v, err := g.DB().GetValue(context.Background(), "SELECT DATABASE()")
	if err != nil {
		t.Fatalf("查询当前数据库失败: %v", err)
	}
	if got := v.String(); got != safeTestDB {
		t.Fatalf("拒绝在非测试库执行：实际连接库为 %q，不是 %q", got, safeTestDB)
	}
}

// setupRegionServer 迁移 + Bootstrap（地区数据已由 B1-03-04 导入同一测试库），挂载真实路由。
func setupRegionServer(t *testing.T) string {
	t.Helper()
	requireIsolatedTestDB(t)
	t.Setenv("AUTH_JWT_SECRET", testJWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", testAdminPassword)
	t.Setenv("REDIS_DEFAULT_DB", "4")

	ctx := context.Background()
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	requireActualDB(t)

	s := g.Server(guid.S())
	s.SetAddr(":0")
	s.Group("/", func(group *ghttp.RouterGroup) {
		group.Middleware(middleware.Response)
		cmd.RegisterFrontendRoutes(group)
	})
	s.SetDumpRouterMap(false)
	s.Start()
	t.Cleanup(func() { _ = s.Shutdown() })

	time.Sleep(100 * time.Millisecond)
	return fmt.Sprintf("http://127.0.0.1:%d", s.GetListenedPort())
}

// decodeList 解析地区列表响应。
func decodeList(t *testing.T, res apiResult) []regionJSON {
	t.Helper()
	var d struct {
		Items []regionJSON `json:"items"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal region list: %v", err)
	}
	return d.Items
}

// TestRegionListByParent 覆盖省/市/区县查询、空父级、无效父级与稳定排序。
func TestRegionListByParent(t *testing.T) {
	base := setupRegionServer(t)

	// 省级：缺省 parent_id 与显式 0 等价，均为 34 个省份。
	provinces := decodeList(t, assertGetOK(t, base, "/regions"))
	if len(provinces) != 34 {
		t.Fatalf("expected 34 provinces, got %d", len(provinces))
	}
	provincesZero := decodeList(t, assertGetOK(t, base, "/regions?parent_id=0"))
	if len(provincesZero) != 34 {
		t.Fatalf("expected 34 provinces for parent_id=0, got %d", len(provincesZero))
	}
	for i, p := range provinces {
		if p.ParentId != 0 || p.Level != 1 || p.Name == "" {
			t.Fatalf("invalid province node: %+v", p)
		}
		if i > 0 && provinces[i-1].Id >= p.Id {
			t.Fatalf("province list not ascending by id: %d then %d", provinces[i-1].Id, p.Id)
		}
	}

	// 市级：北京市(id=1) 的直接下级。
	cities := decodeList(t, assertGetOK(t, base, "/regions?parent_id=1"))
	if len(cities) == 0 {
		t.Fatalf("expected cities under province 1")
	}
	for _, c := range cities {
		if c.ParentId != 1 || c.Level != 2 {
			t.Fatalf("invalid city node: %+v", c)
		}
	}

	// 区县级：北京市(id=2) 的直接下级。
	districts := decodeList(t, assertGetOK(t, base, "/regions?parent_id=2"))
	if len(districts) == 0 {
		t.Fatalf("expected districts under city 2")
	}
	for _, d := range districts {
		if d.ParentId != 2 || d.Level != 3 {
			t.Fatalf("invalid district node: %+v", d)
		}
	}

	// 无效父级：负数 → 1001；不存在的正数 → 空列表（200）。
	res := doGet(t, base, "/regions?parent_id=-1")
	if res.Status != 400 || res.Code != 1001 {
		t.Fatalf("negative parent_id: status=%d code=%d", res.Status, res.Code)
	}
	res = doGet(t, base, "/regions?parent_id=99999999")
	assertOK(t, res, "nonexistent parent")
	if items := decodeList(t, res); len(items) != 0 {
		t.Fatalf("expected empty list for nonexistent parent, got %d", len(items))
	}

	// 排序稳定：两次请求结果完全一致。
	first := decodeList(t, assertGetOK(t, base, "/regions?parent_id=2"))
	second := decodeList(t, assertGetOK(t, base, "/regions?parent_id=2"))
	if len(first) != len(second) {
		t.Fatalf("unstable list length: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Id != second[i].Id {
			t.Fatalf("unstable order at %d: %d vs %d", i, first[i].Id, second[i].Id)
		}
	}
}

// assertGetOK 发起 GET 并断言 200/0。
func assertGetOK(t *testing.T, base, path string) apiResult {
	t.Helper()
	res := doGet(t, base, path)
	assertOK(t, res, "GET "+path)
	return res
}

// TestRegionTree 覆盖三级树完整性：3938 节点、父子关系、无孤儿/重复、排序稳定。
func TestRegionTree(t *testing.T) {
	base := setupRegionServer(t)
	res := assertGetOK(t, base, "/regions/tree")
	var tree treeJSON
	if err := json.Unmarshal(res.Data, &tree); err != nil {
		t.Fatalf("unmarshal tree: %v", err)
	}
	if tree.Total != expectedRegionTotal {
		t.Fatalf("expected total=%d, got %d", expectedRegionTotal, tree.Total)
	}
	if len(tree.Items) != 34 {
		t.Fatalf("expected 34 province roots, got %d", len(tree.Items))
	}

	// DB 中的全部 id 集合，用于校验树覆盖完整且无孤儿。
	dbIDs := map[int64]bool{}
	rows, err := g.DB().Model("regions").Ctx(context.Background()).Fields("id").Order("id").All()
	if err != nil {
		t.Fatalf("query region ids: %v", err)
	}
	for _, row := range rows {
		dbIDs[row["id"].Int64()] = true
	}
	if len(dbIDs) != expectedRegionTotal {
		t.Fatalf("expected %d region rows, got %d", expectedRegionTotal, len(dbIDs))
	}

	seen := map[int64]bool{}
	var walk func(nodes []*treeNodeJSON, parent *treeNodeJSON) int
	walk = func(nodes []*treeNodeJSON, parent *treeNodeJSON) int {
		count := 0
		for i, n := range nodes {
			if seen[n.Id] {
				t.Fatalf("duplicate region node id=%d", n.Id)
			}
			seen[n.Id] = true
			if !dbIDs[n.Id] {
				t.Fatalf("tree node %d not present in DB", n.Id)
			}
			if i > 0 && nodes[i-1].Id >= n.Id {
				t.Fatalf("children not ascending by id under parent %v: %d then %d", parent, nodes[i-1].Id, n.Id)
			}
			if parent == nil {
				if n.ParentId != 0 || n.Level != 1 {
					t.Fatalf("invalid root node: %+v", n)
				}
			} else {
				if n.ParentId != parent.Id {
					t.Fatalf("node %d parent_id=%d, want %d", n.Id, n.ParentId, parent.Id)
				}
				if n.Level != parent.Level+1 {
					t.Fatalf("node %d level=%d, parent level=%d", n.Id, n.Level, parent.Level)
				}
				if n.Level > 3 {
					t.Fatalf("node %d exceeds 3 levels", n.Id)
				}
			}
			count++
			count += walk(n.Children, n)
		}
		return count
	}
	walked := walk(tree.Items, nil)
	if walked != expectedRegionTotal {
		t.Fatalf("tree walked %d nodes, want %d", walked, expectedRegionTotal)
	}
	if len(seen) != expectedRegionTotal {
		t.Fatalf("tree covered %d unique nodes, want %d", len(seen), expectedRegionTotal)
	}

	// 无孤儿：树中出现的所有非省级节点的父级都必须在树中（由 walk 的 parent 断言覆盖），
	// 且树覆盖集合与 DB 集合完全一致 → 不存在未被任何父级引用的孤立节点。
	if len(seen) != len(dbIDs) {
		t.Fatalf("tree/DB id set mismatch: tree=%d db=%d", len(seen), len(dbIDs))
	}

	// 层级分布：34 省 + 369 市 + 3535 区县。
	levelCount := map[int]int{}
	var countLevel func(nodes []*treeNodeJSON)
	countLevel = func(nodes []*treeNodeJSON) {
		for _, n := range nodes {
			levelCount[n.Level]++
			countLevel(n.Children)
		}
	}
	countLevel(tree.Items)
	if levelCount[1] != 34 || levelCount[2] != 369 || levelCount[3] != 3535 {
		t.Fatalf("unexpected level distribution: %+v", levelCount)
	}
}

// TestRegionDetail 覆盖地区详情与三级链路。
func TestRegionDetail(t *testing.T) {
	base := setupRegionServer(t)

	// 区县：id=3 东城区，链路 1(北京市) → 2(北京市) → 3(东城区)。
	res := assertGetOK(t, base, "/regions/3")
	var d struct {
		regionJSON
		Province *regionJSON `json:"province"`
		City     *regionJSON `json:"city"`
		District *regionJSON `json:"district"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil {
		t.Fatalf("unmarshal detail: %v", err)
	}
	if d.Id != 3 || d.Level != 3 {
		t.Fatalf("unexpected detail node: %+v", d)
	}
	if d.Province == nil || d.Province.Level != 1 || d.City == nil || d.City.Level != 2 || d.District == nil || d.District.Id != 3 {
		t.Fatalf("incomplete chain: province=%+v city=%+v district=%+v", d.Province, d.City, d.District)
	}
	if d.City.ParentId != d.Province.Id || d.District.ParentId != d.City.Id {
		t.Fatalf("chain parent mismatch: %+v", d)
	}

	// 省级：自身为 province，city/district 为 null。
	res = assertGetOK(t, base, "/regions/1")
	var p struct {
		regionJSON
		Province *regionJSON `json:"province"`
		City     *regionJSON `json:"city"`
		District *regionJSON `json:"district"`
	}
	if err := json.Unmarshal(res.Data, &p); err != nil {
		t.Fatalf("unmarshal province detail: %v", err)
	}
	if p.Province == nil || p.Province.Id != 1 || p.City != nil || p.District != nil {
		t.Fatalf("unexpected province chain: %+v", p)
	}

	// 不存在 → 400/1001。
	res = doGet(t, base, "/regions/99999999")
	if res.Status != 400 || res.Code != 1001 {
		t.Fatalf("nonexistent region: status=%d code=%d", res.Status, res.Code)
	}
}
