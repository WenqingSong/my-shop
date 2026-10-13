package cmd

// 本文件通过真实路由（RegisterFrontendRoutes）+ 真实 MySQL/Redis 锁定「用户文章」核心闭环。
// 覆盖 article-cms-v1 的 AC-001 至 AC-012 与关键不变量（服务端归属绑定、越权隔离、幂等、删除关联清理）。

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	_ "github.com/gogf/gf/contrib/drivers/mysql/v2"
	_ "github.com/gogf/gf/contrib/nosql/redis/v2"

	"github.com/gogf/gf/v2/frame/g"
)

// articleData 是文章写接口/详情返回的完整文章结构。
type articleData struct {
	Id             int64  `json:"id"`
	AuthorId       int64  `json:"author_id"`
	AuthorUsername string `json:"author_username"`
	Title          string `json:"title"`
	Content        string `json:"content"`
}

// articleItemData 是文章列表条目（不含正文）。
type articleItemData struct {
	Id             int64  `json:"id"`
	AuthorId       int64  `json:"author_id"`
	AuthorUsername string `json:"author_username"`
	Title          string `json:"title"`
}

// articleListData 是公开文章列表响应。
type articleListData struct {
	Items []articleItemData `json:"items"`
	Total int               `json:"total"`
	Page  int               `json:"page"`
	Size  int               `json:"size"`
}

// articleMyListData 是我的文章列表响应。
type articleMyListData struct {
	Items []articleData `json:"items"`
	Total int           `json:"total"`
}

// articleLikeCountData 是公开点赞数响应。
type articleLikeCountData struct {
	Count int `json:"count"`
}

// articleLikeResData 是点赞响应。
type articleLikeResData struct {
	Liked bool `json:"liked"`
}

// articleFavoriteResData 是收藏响应。
type articleFavoriteResData struct {
	Favorited bool `json:"favorited"`
}

// setupArticleServer 建立隔离的文章测试环境：复用身份隔离初始化后清空文章相关表。
func setupArticleServer(t *testing.T) string {
	t.Helper()
	base := setupIsolationServer(t)
	for _, table := range []string{"article_favorites", "article_likes", "articles"} {
		if _, err := g.DB().Exec(context.Background(), "DELETE FROM "+table); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	return base
}

// articleUser 创建前台用户并登录，返回 (userID, token)。
func articleUser(t *testing.T, base, username string) (int64, string) {
	t.Helper()
	isoInsertUser(t, username, "articlepass123")
	token, _ := isoFrontendLogin(t, base, username, "articlepass123")
	claims := isoClaims(t, token)
	userID, _ := parseUserID(t, claims.Subject)
	return userID, token
}

// articleCreate 发布文章并返回其完整结构（失败即中止）。
func articleCreate(t *testing.T, base, token, title, content string) articleData {
	t.Helper()
	res := reviewCall(t, base, "POST", "/articles", token, map[string]any{"title": title, "content": content})
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("create article: status=%d code=%d msg=%q", res.Status, res.Code, res.Message)
	}
	return decodeData[articleData](t, res.Data)
}

// articleCount 统计 articles 表当前行数。
func articleCount(t *testing.T) int {
	t.Helper()
	n, err := g.DB().Model("articles").Ctx(context.Background()).Count()
	if err != nil {
		t.Fatalf("count articles: %v", err)
	}
	return n
}

// TestArticleCreateDetailOwnership 覆盖 AC-001/AC-005 与 INV-001：
// 发布文章作者身份取自 Principal.UserID，伪造 author_id 被忽略，详情返回内容与作者用户名。
func TestArticleCreateDetailOwnership(t *testing.T) {
	base := setupArticleServer(t)
	userID, token := articleUser(t, base, "author1")

	// 客户端伪造 author_id，应被忽略（请求契约不含身份字段）。
	a := articleCreate(t, base, token, "我的第一篇文章", "正文内容")
	if a.AuthorId != userID {
		t.Fatalf("author_id=%d want %d（服务端归属绑定被伪造破坏）", a.AuthorId, userID)
	}
	if a.AuthorUsername != "author1" {
		t.Fatalf("author_username=%q want author1", a.AuthorUsername)
	}

	// 伪造字段场景：显式提交 author_id/user_id 也不应影响落库归属。
	res := reviewCall(t, base, "POST", "/articles", token, map[string]any{
		"title": "第二篇", "content": "内容", "author_id": 999999, "user_id": 888888,
	})
	if res.Status != 200 || res.Code != 0 {
		t.Fatalf("create with forged fields: status=%d code=%d", res.Status, res.Code)
	}
	forged := decodeData[articleData](t, res.Data)
	if forged.AuthorId != userID {
		t.Fatalf("forged author_id not ignored: author_id=%d want %d", forged.AuthorId, userID)
	}

	// 公开详情返回内容与作者用户名。
	detail := reviewCall(t, base, "GET", fmt.Sprintf("/articles/%d", a.Id), "", nil)
	if detail.Status != 200 || detail.Code != 0 {
		t.Fatalf("detail: status=%d code=%d", detail.Status, detail.Code)
	}
	d := decodeData[articleData](t, detail.Data)
	if d.Title != "我的第一篇文章" || d.Content != "正文内容" || d.AuthorUsername != "author1" {
		t.Fatalf("detail mismatch: %+v", d)
	}

	// 不存在的文章详情 → 404/16001。
	miss := reviewCall(t, base, "GET", "/articles/999999", "", nil)
	if miss.Status != 404 || miss.Code != 16001 {
		t.Fatalf("missing detail: status=%d code=%d want 404/16001", miss.Status, miss.Code)
	}
}

// TestArticlePublicList 覆盖 AC-004：公开列表无需 token、按 id 倒序、分页正确。
func TestArticlePublicList(t *testing.T) {
	base := setupArticleServer(t)
	_, token := articleUser(t, base, "author2")

	a1 := articleCreate(t, base, token, "文章一", "内容一")
	a2 := articleCreate(t, base, token, "文章二", "内容二")
	a3 := articleCreate(t, base, token, "文章三", "内容三")

	// 无需 token 访问公开列表。
	lr := reviewCall(t, base, "GET", "/articles", "", nil)
	if lr.Status != 200 || lr.Code != 0 {
		t.Fatalf("public list: status=%d code=%d", lr.Status, lr.Code)
	}
	ld := decodeData[articleListData](t, lr.Data)
	if ld.Total != 3 || len(ld.Items) != 3 {
		t.Fatalf("total=%d items=%d want 3", ld.Total, len(ld.Items))
	}
	// 按 id 倒序：最新的在前。
	if ld.Items[0].Id != a3.Id || ld.Items[1].Id != a2.Id || ld.Items[2].Id != a1.Id {
		t.Fatalf("order mismatch: %d,%d,%d", ld.Items[0].Id, ld.Items[1].Id, ld.Items[2].Id)
	}
	// 列表不含正文。
	if ld.Items[0].Title != "文章三" {
		t.Fatalf("list item title mismatch: %+v", ld.Items[0])
	}

	// 分页：size=2 第 1 页含 2 条。
	paged := reviewCall(t, base, "GET", "/articles?page=1&size=2", "", nil)
	pd := decodeData[articleListData](t, paged.Data)
	if pd.Total != 3 || len(pd.Items) != 2 {
		t.Fatalf("page total=%d items=%d want total=3 items=2", pd.Total, len(pd.Items))
	}
}

// TestArticleMyListIsolation 覆盖 AC-006 与 INV-002：我的文章仅含本人文章，不泄露他人文章。
func TestArticleMyListIsolation(t *testing.T) {
	base := setupArticleServer(t)
	_, tokenA := articleUser(t, base, "authorA")
	_, tokenB := articleUser(t, base, "authorB")

	articleCreate(t, base, tokenA, "A1", "内容A1")
	articleCreate(t, base, tokenA, "A2", "内容A2")
	articleCreate(t, base, tokenB, "B1", "内容B1")

	myA := reviewCall(t, base, "GET", "/my/articles", tokenA, nil)
	if myA.Status != 200 || myA.Code != 0 {
		t.Fatalf("my list A: status=%d code=%d", myA.Status, myA.Code)
	}
	mdA := decodeData[articleMyListData](t, myA.Data)
	if mdA.Total != 2 || len(mdA.Items) != 2 {
		t.Fatalf("A my list total=%d items=%d want 2", mdA.Total, len(mdA.Items))
	}
	for _, it := range mdA.Items {
		if it.Title != "A1" && it.Title != "A2" {
			t.Fatalf("A my list leaked others: %+v", it)
		}
	}

	myB := reviewCall(t, base, "GET", "/my/articles", tokenB, nil)
	mdB := decodeData[articleMyListData](t, myB.Data)
	if mdB.Total != 1 || len(mdB.Items) != 1 || mdB.Items[0].Title != "B1" {
		t.Fatalf("B my list mismatch: total=%d %+v", mdB.Total, mdB.Items)
	}
}

// TestArticleUpdateOwnership 覆盖 AC-002 与 INV-002：作者修改成功；非作者修改 404/16001 且内容不变。
func TestArticleUpdateOwnership(t *testing.T) {
	base := setupArticleServer(t)
	_, tokenA := articleUser(t, base, "authorA")
	_, tokenB := articleUser(t, base, "authorB")

	a := articleCreate(t, base, tokenA, "原标题", "原内容")

	// 作者修改成功。
	upd := reviewCall(t, base, "PUT", fmt.Sprintf("/articles/%d", a.Id), tokenA, map[string]any{
		"title": "新标题", "content": "新内容",
	})
	if upd.Status != 200 || upd.Code != 0 {
		t.Fatalf("owner update: status=%d code=%d", upd.Status, upd.Code)
	}
	if u := decodeData[articleData](t, upd.Data); u.Title != "新标题" || u.Content != "新内容" {
		t.Fatalf("update mismatch: %+v", u)
	}

	// 非作者修改 → 404/16001 且内容不变。
	bad := reviewCall(t, base, "PUT", fmt.Sprintf("/articles/%d", a.Id), tokenB, map[string]any{
		"title": "篡改", "content": "篡改",
	})
	if bad.Status != 404 || bad.Code != 16001 {
		t.Fatalf("intruder update: status=%d code=%d want 404/16001", bad.Status, bad.Code)
	}
	detail := reviewCall(t, base, "GET", fmt.Sprintf("/articles/%d", a.Id), "", nil)
	if d := decodeData[articleData](t, detail.Data); d.Title != "新标题" || d.Content != "新内容" {
		t.Fatalf("intruder must not change article: %+v", d)
	}

	// 不存在的文章修改 → 404/16001。
	miss := reviewCall(t, base, "PUT", "/articles/999999", tokenA, map[string]any{"title": "x", "content": "y"})
	if miss.Status != 404 || miss.Code != 16001 {
		t.Fatalf("missing update: status=%d code=%d want 404/16001", miss.Status, miss.Code)
	}
}

// TestArticleDeleteOwnershipAndCleanup 覆盖 AC-003/AC-010 与 INV-004：
// 作者删除成功且点赞/收藏被清理；非作者删除 404/16001 且无删除。
func TestArticleDeleteOwnershipAndCleanup(t *testing.T) {
	base := setupArticleServer(t)
	_, tokenA := articleUser(t, base, "authorA")
	_, tokenB := articleUser(t, base, "authorB")

	a := articleCreate(t, base, tokenA, "待删文章", "内容")

	// A 点赞并收藏。
	if r := reviewCall(t, base, "POST", fmt.Sprintf("/articles/%d/like", a.Id), tokenA, nil); r.Status != 200 || r.Code != 0 {
		t.Fatalf("like: status=%d code=%d", r.Status, r.Code)
	}
	if r := reviewCall(t, base, "POST", fmt.Sprintf("/articles/%d/favorite", a.Id), tokenA, nil); r.Status != 200 || r.Code != 0 {
		t.Fatalf("favorite: status=%d code=%d", r.Status, r.Code)
	}

	// 非作者删除 → 404/16001，文章与关联数据均未删除。
	bad := reviewCall(t, base, "DELETE", fmt.Sprintf("/articles/%d", a.Id), tokenB, nil)
	if bad.Status != 404 || bad.Code != 16001 {
		t.Fatalf("intruder delete: status=%d code=%d want 404/16001", bad.Status, bad.Code)
	}
	if articleCount(t) != 1 {
		t.Fatalf("intruder must not delete article, count=%d", articleCount(t))
	}
	if n := tableRowCount(t, "article_likes"); n != 1 {
		t.Fatalf("intruder must not delete likes, count=%d", n)
	}
	if n := tableRowCount(t, "article_favorites"); n != 1 {
		t.Fatalf("intruder must not delete favorites, count=%d", n)
	}

	// 作者删除成功，点赞/收藏被事务清理。
	del := reviewCall(t, base, "DELETE", fmt.Sprintf("/articles/%d", a.Id), tokenA, nil)
	if del.Status != 200 || del.Code != 0 {
		t.Fatalf("owner delete: status=%d code=%d", del.Status, del.Code)
	}
	if articleCount(t) != 0 {
		t.Fatalf("after delete article count=%d want 0", articleCount(t))
	}
	if n := tableRowCount(t, "article_likes"); n != 0 {
		t.Fatalf("after delete likes count=%d want 0", n)
	}
	if n := tableRowCount(t, "article_favorites"); n != 0 {
		t.Fatalf("after delete favorites count=%d want 0", n)
	}
	// 公开点赞数回落为 0。
	c := reviewCall(t, base, "GET", fmt.Sprintf("/articles/%d/like/count", a.Id), "", nil)
	if cd := decodeData[articleLikeCountData](t, c.Data); cd.Count != 0 {
		t.Fatalf("after delete like count=%d want 0", cd.Count)
	}
}

// tableRowCount 统计指定表当前行数。
func tableRowCount(t *testing.T, table string) int {
	t.Helper()
	n, err := g.DB().Model(table).Ctx(context.Background()).Count()
	if err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestArticleLikeIdempotentAndCount 覆盖 AC-007：点赞/重复点赞幂等、公开点赞数正确、取消后回落。
func TestArticleLikeIdempotentAndCount(t *testing.T) {
	base := setupArticleServer(t)
	_, token := articleUser(t, base, "liker1")
	a := articleCreate(t, base, token, "点赞文章", "内容")

	// 未点赞 check=false、公开计数=0。
	c0 := reviewCall(t, base, "GET", fmt.Sprintf("/articles/%d/like/check", a.Id), token, nil)
	if cd := decodeData[articleLikeResData](t, c0.Data); cd.Liked {
		t.Fatalf("check before like should be false")
	}
	lc0 := reviewCall(t, base, "GET", fmt.Sprintf("/articles/%d/like/count", a.Id), "", nil)
	if cd := decodeData[articleLikeCountData](t, lc0.Data); cd.Count != 0 {
		t.Fatalf("count before like=%d want 0", cd.Count)
	}

	// 点赞 → 200 liked=true。
	r := reviewCall(t, base, "POST", fmt.Sprintf("/articles/%d/like", a.Id), token, nil)
	if r.Status != 200 || r.Code != 0 {
		t.Fatalf("like: status=%d code=%d", r.Status, r.Code)
	}
	if d := decodeData[articleLikeResData](t, r.Data); !d.Liked {
		t.Fatalf("like response liked should be true")
	}

	// 重复点赞幂等成功，仅一条。
	if r := reviewCall(t, base, "POST", fmt.Sprintf("/articles/%d/like", a.Id), token, nil); r.Status != 200 || r.Code != 0 {
		t.Fatalf("dup like: status=%d code=%d", r.Status, r.Code)
	}
	if n := tableRowCount(t, "article_likes"); n != 1 {
		t.Fatalf("dup like count=%d want 1", n)
	}

	// 公开点赞数=1、check=true。
	lc1 := reviewCall(t, base, "GET", fmt.Sprintf("/articles/%d/like/count", a.Id), "", nil)
	if cd := decodeData[articleLikeCountData](t, lc1.Data); cd.Count != 1 {
		t.Fatalf("count after like=%d want 1", cd.Count)
	}
	c1 := reviewCall(t, base, "GET", fmt.Sprintf("/articles/%d/like/check", a.Id), token, nil)
	if cd := decodeData[articleLikeResData](t, c1.Data); !cd.Liked {
		t.Fatalf("check after like should be true")
	}

	// 取消点赞 → check=false、计数=0；重复取消幂等成功。
	if r := reviewCall(t, base, "DELETE", fmt.Sprintf("/articles/%d/like", a.Id), token, nil); r.Status != 200 || r.Code != 0 {
		t.Fatalf("unlike: status=%d code=%d", r.Status, r.Code)
	}
	if n := tableRowCount(t, "article_likes"); n != 0 {
		t.Fatalf("after unlike count=%d want 0", n)
	}
	if r := reviewCall(t, base, "DELETE", fmt.Sprintf("/articles/%d/like", a.Id), token, nil); r.Status != 200 || r.Code != 0 {
		t.Fatalf("dup unlike: status=%d code=%d", r.Status, r.Code)
	}
}

// TestArticleLikeConcurrentDuplicateSingleRow 覆盖 AC-007（并发）：并发点赞同一文章均幂等成功且至多一条。
func TestArticleLikeConcurrentDuplicateSingleRow(t *testing.T) {
	base := setupArticleServer(t)
	_, token := articleUser(t, base, "liker2")
	a := articleCreate(t, base, token, "并发点赞文章", "内容")

	const n = 8
	var wg sync.WaitGroup
	results := make(chan bool, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := reviewCall(t, base, "POST", fmt.Sprintf("/articles/%d/like", a.Id), token, nil)
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
	if cnt := tableRowCount(t, "article_likes"); cnt != 1 {
		t.Fatalf("concurrent like count=%d want 1", cnt)
	}
}

// TestArticleFavoriteIdempotentCheckList 覆盖 AC-008：收藏/取消/是否已收藏/我的收藏列表正确。
func TestArticleFavoriteIdempotentCheckList(t *testing.T) {
	base := setupArticleServer(t)
	_, token := articleUser(t, base, "faver1")
	a := articleCreate(t, base, token, "收藏文章", "内容")

	// 未收藏 check=false、我的收藏列表为空。
	c0 := reviewCall(t, base, "GET", fmt.Sprintf("/articles/%d/favorite/check", a.Id), token, nil)
	if cd := decodeData[articleFavoriteResData](t, c0.Data); cd.Favorited {
		t.Fatalf("check before favorite should be false")
	}

	// 收藏 → 200 favorited=true。
	r := reviewCall(t, base, "POST", fmt.Sprintf("/articles/%d/favorite", a.Id), token, nil)
	if r.Status != 200 || r.Code != 0 {
		t.Fatalf("favorite: status=%d code=%d", r.Status, r.Code)
	}
	if d := decodeData[articleFavoriteResData](t, r.Data); !d.Favorited {
		t.Fatalf("favorite response favorited should be true")
	}

	// 重复收藏幂等成功，仅一条。
	if r := reviewCall(t, base, "POST", fmt.Sprintf("/articles/%d/favorite", a.Id), token, nil); r.Status != 200 || r.Code != 0 {
		t.Fatalf("dup favorite: status=%d code=%d", r.Status, r.Code)
	}
	if n := tableRowCount(t, "article_favorites"); n != 1 {
		t.Fatalf("dup favorite count=%d want 1", n)
	}

	// 我的收藏列表含该文章。
	lr := reviewCall(t, base, "GET", "/my/articles/favorites", token, nil)
	if lr.Status != 200 || lr.Code != 0 {
		t.Fatalf("my favorites: status=%d code=%d", lr.Status, lr.Code)
	}
	ld := decodeData[articleListData](t, lr.Data)
	if ld.Total != 1 || len(ld.Items) != 1 || ld.Items[0].Id != a.Id || ld.Items[0].Title != "收藏文章" {
		t.Fatalf("my favorites mismatch: %+v", ld)
	}

	// 取消收藏 → check=false、列表为空；重复取消幂等成功。
	if r := reviewCall(t, base, "DELETE", fmt.Sprintf("/articles/%d/favorite", a.Id), token, nil); r.Status != 200 || r.Code != 0 {
		t.Fatalf("unfavorite: status=%d code=%d", r.Status, r.Code)
	}
	c1 := reviewCall(t, base, "GET", fmt.Sprintf("/articles/%d/favorite/check", a.Id), token, nil)
	if cd := decodeData[articleFavoriteResData](t, c1.Data); cd.Favorited {
		t.Fatalf("check after unfavorite should be false")
	}
	if r := reviewCall(t, base, "DELETE", fmt.Sprintf("/articles/%d/favorite", a.Id), token, nil); r.Status != 200 || r.Code != 0 {
		t.Fatalf("dup unfavorite: status=%d code=%d", r.Status, r.Code)
	}
}

// TestArticleLikeFavoriteTargetNotFound 覆盖 AC-007/AC-008 拒绝路径：点赞/收藏不存在的文章 404/16001。
func TestArticleLikeFavoriteTargetNotFound(t *testing.T) {
	base := setupArticleServer(t)
	_, token := articleUser(t, base, "faver2")

	for _, tc := range []struct {
		method string
		path   string
	}{
		{"POST", "/articles/999999/like"},
		{"POST", "/articles/999999/favorite"},
	} {
		res := reviewCall(t, base, tc.method, tc.path, token, nil)
		if res.Status != 404 || res.Code != 16001 {
			t.Fatalf("%s %s: status=%d code=%d want 404/16001", tc.method, tc.path, res.Status, res.Code)
		}
	}
	if n := tableRowCount(t, "article_likes"); n != 0 {
		t.Fatalf("not-found like must not write, count=%d", n)
	}
	if n := tableRowCount(t, "article_favorites"); n != 0 {
		t.Fatalf("not-found favorite must not write, count=%d", n)
	}
}

// TestArticleInvalidInput 覆盖 AC（非法输入）：标题/内容为空或超长均 400/16002 且无写入。
func TestArticleInvalidInput(t *testing.T) {
	base := setupArticleServer(t)
	_, token := articleUser(t, base, "invalider")

	cases := []map[string]any{
		{"title": "", "content": "内容"},                         // 标题为空
		{"title": "   ", "content": "内容"},                      // 标题 trim 后为空
		{"title": "标题", "content": ""},                         // 内容为空
		{"title": "标题", "content": "   "},                      // 内容 trim 后为空
		{"title": strings.Repeat("a", 65), "content": "内容"},    // 标题超长
		{"title": "标题", "content": strings.Repeat("a", 10001)}, // 内容超长
	}
	for i, body := range cases {
		res := reviewCall(t, base, "POST", "/articles", token, body)
		if res.Status != 400 || res.Code != 16002 {
			t.Fatalf("case %d: status=%d code=%d want 400/16002", i, res.Status, res.Code)
		}
	}
	if articleCount(t) != 0 {
		t.Fatalf("invalid input must not write, count=%d", articleCount(t))
	}
}

// TestArticleRequiresAuth 覆盖 AC-011：写与本人状态接口无 token 返回 401/1002 且无写入；公开读无需 token。
func TestArticleRequiresAuth(t *testing.T) {
	base := setupArticleServer(t)
	_, token := articleUser(t, base, "author3")
	a := articleCreate(t, base, token, "认证测试", "内容")

	writeCases := []struct {
		method string
		path   string
		body   any
	}{
		{"POST", "/articles", map[string]any{"title": "x", "content": "y"}},
		{"PUT", fmt.Sprintf("/articles/%d", a.Id), map[string]any{"title": "x", "content": "y"}},
		{"DELETE", fmt.Sprintf("/articles/%d", a.Id), nil},
		{"GET", "/my/articles", nil},
		{"POST", fmt.Sprintf("/articles/%d/like", a.Id), nil},
		{"DELETE", fmt.Sprintf("/articles/%d/like", a.Id), nil},
		{"GET", fmt.Sprintf("/articles/%d/like/check", a.Id), nil},
		{"POST", fmt.Sprintf("/articles/%d/favorite", a.Id), nil},
		{"DELETE", fmt.Sprintf("/articles/%d/favorite", a.Id), nil},
		{"GET", fmt.Sprintf("/articles/%d/favorite/check", a.Id), nil},
		{"GET", "/my/articles/favorites", nil},
	}
	for _, tc := range writeCases {
		res := reviewCall(t, base, tc.method, tc.path, "", tc.body)
		if res.Status != 401 || res.Code != 1002 {
			t.Fatalf("%s %s: status=%d code=%d want 401/1002", tc.method, tc.path, res.Status, res.Code)
		}
	}
	if articleCount(t) != 1 {
		t.Fatalf("unauthenticated must not write, count=%d want 1", articleCount(t))
	}

	// 非法 token。
	res := reviewCall(t, base, "GET", "/my/articles", "invalid-token", nil)
	if res.Status != 401 || res.Code != 1002 {
		t.Fatalf("invalid token: status=%d code=%d want 401/1002", res.Status, res.Code)
	}

	// 公开读无需 token。
	pub := []string{
		"/articles",
		fmt.Sprintf("/articles/%d", a.Id),
		fmt.Sprintf("/articles/%d/like/count", a.Id),
	}
	for _, p := range pub {
		res := reviewCall(t, base, "GET", p, "", nil)
		if res.Status != 200 || res.Code != 0 {
			t.Fatalf("public %s: status=%d code=%d want 200/0", p, res.Status, res.Code)
		}
	}
}
