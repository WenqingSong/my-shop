package wallet_test

// 钱包 HTTP 契约测试：真实路由 + 真实中间件 + 真实 MySQL（+ Redis 会话）。
//
// 安全护栏：只允许在专用测试库上运行（DATABASE_DEFAULT_NAME 以 `_test` 结尾），否则跳过；
// 清理只针对本用例创建的用户与其资金数据，不做全表 DELETE / DROP TABLE。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/gclient"
	"github.com/gogf/gf/v2/net/ghttp"
	"github.com/gogf/gf/v2/util/guid"
	"golang.org/x/crypto/bcrypt"

	"cnb.cool/go-cloud-devops/my-shop/internal/boot"
	"cnb.cool/go-cloud-devops/my-shop/internal/cmd"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic"
	"cnb.cool/go-cloud-devops/my-shop/internal/middleware"
	"cnb.cool/go-cloud-devops/my-shop/internal/migrations"
)

const (
	testJWTSecret     = "test-secret-0123456789-0123456789-0123456789" // >= 32 bytes
	testAdminPassword = "test-admin-password-123"
)

type apiResult struct {
	Status  int
	Code    int
	Message string
	Data    json.RawMessage
}

type accountJSON struct {
	Balance   int64  `json:"balance"`
	UpdatedAt string `json:"updated_at"`
}

type transactionListJSON struct {
	Items []struct {
		Id            int64  `json:"id"`
		TxType        string `json:"tx_type"`
		Direction     string `json:"direction"`
		Amount        int64  `json:"amount"`
		BalanceBefore int64  `json:"balance_before"`
		BalanceAfter  int64  `json:"balance_after"`
		BizType       string `json:"biz_type"`
		BizId         *int64 `json:"biz_id"`
	} `json:"items"`
	Total int `json:"total"`
	Page  int `json:"page"`
	Size  int `json:"size"`
}

type rechargeJSON struct {
	Id      int64  `json:"id"`
	OrderNo string `json:"order_no"`
	Amount  int64  `json:"amount"`
	Status  string `json:"status"`
	PayType string `json:"pay_type"`
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

func request(t *testing.T, base, method, path string, body any, headers map[string]string) apiResult {
	t.Helper()
	c := g.Client()
	if len(headers) > 0 {
		c = c.Header(headers)
	}
	ctx := context.Background()
	var (
		r   *gclient.Response
		err error
	)
	switch method {
	case "GET":
		r, err = c.Get(ctx, base+path)
	case "POST":
		r, err = c.ContentJson().Post(ctx, base+path, body)
	default:
		t.Fatalf("unsupported method %s", method)
	}
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer r.Close()
	return decode(r)
}

func authHeader(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// setupWalletServer 迁移 + Bootstrap + 启动真实路由，返回 base URL。
func setupWalletServer(t *testing.T) string {
	t.Helper()
	name := strings.TrimSpace(os.Getenv("DATABASE_DEFAULT_NAME"))
	if !strings.HasSuffix(name, "_test") {
		t.Skipf("钱包 HTTP 测试需要专用测试库（DATABASE_DEFAULT_NAME 以 _test 结尾，当前 %q）", name)
	}
	t.Setenv("AUTH_JWT_SECRET", testJWTSecret)
	t.Setenv("ADMIN_SUPER_PASSWORD", testAdminPassword)
	t.Setenv("REDIS_DEFAULT_DB", "5")
	t.Setenv("WALLET_MOCK_RECHARGE_ENABLED", "true")

	ctx := context.Background()
	if err := migrations.Up(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if err := boot.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if err := g.Redis().FlushDB(ctx); err != nil {
		t.Fatalf("clean redis: %v", err)
	}

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

// createUser 创建用户并登录，返回 (userID, token)。
func createUser(t *testing.T, base, username string) (int64, string) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte("userpass123"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	userID, err := g.DB().Model("users").Ctx(context.Background()).Data(g.Map{
		"username":      username,
		"password_hash": string(hash),
	}).InsertAndGetId()
	if err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, sql := range []string{
			"DELETE FROM wallet_transactions WHERE user_id = ?",
			"DELETE FROM wallet_accounts WHERE user_id = ?",
			"DELETE FROM recharge_orders WHERE user_id = ?",
			"DELETE FROM users WHERE id = ?",
		} {
			_, _ = g.DB().Exec(ctx, sql, userID)
		}
	})
	res := request(t, base, "POST", "/login", map[string]any{"username": username, "password": "userpass123"}, nil)
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("login: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	var d struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(res.Data, &d); err != nil || d.AccessToken == "" {
		t.Fatalf("解析登录响应失败: %v", err)
	}
	return userID, d.AccessToken
}

func parseAccount(t *testing.T, res apiResult) accountJSON {
	t.Helper()
	var a accountJSON
	if err := json.Unmarshal(res.Data, &a); err != nil {
		t.Fatalf("解析余额响应: %v", err)
	}
	return a
}

func parseTransactions(t *testing.T, res apiResult) transactionListJSON {
	t.Helper()
	var tl transactionListJSON
	if err := json.Unmarshal(res.Data, &tl); err != nil {
		t.Fatalf("解析流水响应: %v", err)
	}
	return tl
}

func parseRecharge(t *testing.T, res apiResult) rechargeJSON {
	t.Helper()
	var r rechargeJSON
	if err := json.Unmarshal(res.Data, &r); err != nil {
		t.Fatalf("解析充值响应: %v", err)
	}
	return r
}

// TestWalletHTTPFlow 覆盖：匿名拒绝、余额查询、Mock 充值、流水分页、幂等与用户隔离。
func TestWalletHTTPFlow(t *testing.T) {
	base := setupWalletServer(t)
	suffix := time.Now().UnixNano()

	// 匿名访问一律 401。
	for _, path := range []string{"/wallet", "/wallet/transactions"} {
		res := request(t, base, "GET", path, nil, nil)
		if res.Status != 401 || res.Code != 1002 {
			t.Fatalf("匿名 %s: status=%d code=%d", path, res.Status, res.Code)
		}
	}
	anonymous := request(t, base, "POST", "/wallet/recharges", map[string]any{"amount": 100, "idempotency_key": "anon"}, nil)
	if anonymous.Status != 401 || anonymous.Code != 1002 {
		t.Fatalf("匿名充值: status=%d code=%d", anonymous.Status, anonymous.Code)
	}

	userID, token := createUser(t, base, fmt.Sprintf("w%d", suffix%1000000000000))
	otherID, otherToken := createUser(t, base, fmt.Sprintf("wo%d", suffix%1000000000000))
	_ = userID
	_ = otherID

	// 初始余额 0（账户按需初始化）。
	res := request(t, base, "GET", "/wallet", nil, authHeader(token))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("查询余额: status=%d code=%d", res.Status, res.Code)
	}
	if got := parseAccount(t, res); got.Balance != 0 {
		t.Fatalf("初始余额应为 0，实际 %d", got.Balance)
	}

	// Mock 充值 10000 分。
	res = request(t, base, "POST", "/wallet/recharges", map[string]any{
		"amount": 10000, "idempotency_key": "http-key-1",
	}, authHeader(token))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("充值: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	first := parseRecharge(t, res)
	if first.Status != "succeeded" || first.Amount != 10000 || first.PayType != "mock" || first.OrderNo == "" {
		t.Fatalf("充值响应不符: %+v", first)
	}

	res = request(t, base, "GET", "/wallet", nil, authHeader(token))
	if got := parseAccount(t, res); got.Balance != 10000 {
		t.Fatalf("充值后余额应为 10000，实际 %d", got.Balance)
	}

	// 重复充值：幂等，不重复入账。
	res = request(t, base, "POST", "/wallet/recharges", map[string]any{
		"amount": 10000, "idempotency_key": "http-key-1",
	}, authHeader(token))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("重复充值应幂等成功: status=%d code=%d", res.Status, res.Code)
	}
	if again := parseRecharge(t, res); again.Id != first.Id {
		t.Fatalf("重复充值应命中同一充值单: %d != %d", again.Id, first.Id)
	}
	res = request(t, base, "GET", "/wallet", nil, authHeader(token))
	if got := parseAccount(t, res); got.Balance != 10000 {
		t.Fatalf("重复充值后余额应仍为 10000，实际 %d", got.Balance)
	}

	// 流水：1 条，字段完整且不泄露内部账户 id。
	res = request(t, base, "GET", "/wallet/transactions", nil, authHeader(token))
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("查询流水: status=%d code=%d", res.Status, res.Code)
	}
	tl := parseTransactions(t, res)
	if tl.Total != 1 || len(tl.Items) != 1 {
		t.Fatalf("流水应为 1 条，实际 total=%d len=%d", tl.Total, len(tl.Items))
	}
	item := tl.Items[0]
	if item.TxType != "recharge" || item.Direction != "credit" || item.Amount != 10000 ||
		item.BalanceBefore != 0 || item.BalanceAfter != 10000 || item.BizId == nil {
		t.Fatalf("流水字段不符: %+v", item)
	}

	// 用户隔离：他人余额 0、流水为空。
	res = request(t, base, "GET", "/wallet", nil, authHeader(otherToken))
	if got := parseAccount(t, res); got.Balance != 0 {
		t.Fatalf("他人余额应为 0，实际 %d", got.Balance)
	}
	res = request(t, base, "GET", "/wallet/transactions", nil, authHeader(otherToken))
	if got := parseTransactions(t, res); got.Total != 0 {
		t.Fatalf("不得看到他人流水，实际 %d 条", got.Total)
	}

	// 同键不同金额：22008。
	res = request(t, base, "POST", "/wallet/recharges", map[string]any{
		"amount": 9999, "idempotency_key": "http-key-1",
	}, authHeader(token))
	if res.Status != 409 || res.Code != 22008 {
		t.Fatalf("同键不同金额应 409/22008，实际 %d/%d", res.Status, res.Code)
	}
}

// TestWalletHTTPValidation 覆盖：金额非法、分页非法、流水类型非法与 Mock 开关关闭。
func TestWalletHTTPValidation(t *testing.T) {
	base := setupWalletServer(t)
	suffix := time.Now().UnixNano()
	_, token := createUser(t, base, fmt.Sprintf("wi%d", suffix%1000000000000))

	bodies := []map[string]any{
		{"amount": 0, "idempotency_key": "zero"},
		{"amount": -1, "idempotency_key": "negative"},
		{"amount": 1000001, "idempotency_key": "too-large"},
	}
	for _, body := range bodies {
		res := request(t, base, "POST", "/wallet/recharges", body, authHeader(token))
		if res.Status != 400 || res.Code != 22003 {
			t.Fatalf("非法金额 %v 应 400/22003，实际 %d/%d", body["amount"], res.Status, res.Code)
		}
	}

	for _, path := range []string{
		"/wallet/transactions?page=0",
		"/wallet/transactions?size=101",
		"/wallet/transactions?tx_type=unknown",
	} {
		res := request(t, base, "GET", path, nil, authHeader(token))
		if res.Status != 400 || res.Code != 1001 {
			t.Fatalf("%s 应 400/1001，实际 %d/%d", path, res.Status, res.Code)
		}
	}

	// 关闭 Mock 充值开关：能力必须立刻不可用（403/22009）。
	t.Setenv("WALLET_MOCK_RECHARGE_ENABLED", "false")
	res := request(t, base, "POST", "/wallet/recharges", map[string]any{
		"amount": 100, "idempotency_key": "disabled",
	}, authHeader(token))
	if res.Status != 403 || res.Code != 22009 {
		t.Fatalf("开关关闭应 403/22009，实际 %d/%d", res.Status, res.Code)
	}
}
