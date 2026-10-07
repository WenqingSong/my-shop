# Delivery Verification

## Milestone and Target
- Milestone：文章功能（用户侧 CMS）V1 核心闭环
- Delivery Target：`origin/feature/cms` @ `eaa916d5fcbfabd4de8a0d45b47abffabdd4b79b`（feature_head，本地 HEAD == 远程）
- Cleaner Review Target：`496dcd060832b17d2f27bf0232e22199e1b32e75`
- Target Match：YES（`review.target` == `owner.review_target` == `496dcd0`；`review.target..feature_head` 之间仅 review-neutral/delivery-neutral 元数据提交，无实质代码变更）

## Environment
- OS：Linux x86_64（kernel 5.4.241，tlinux4）
- Go：1.24.1 linux/amd64
- MySQL：8.0.46（docker `mysql:8.0`，容器 `my-shop-mysql`，端口 3306，库 `my_shop`）
- Redis：7.4.11（docker `redis:7-alpine`，容器 `my-shop-redis`，端口 6379）
- Docker：29.6.2；Docker Compose v5.3.1
- 配置来源：`manifest/config/config.yaml` 开发默认值（DB `127.0.0.1:3306 root/root`，Redis `127.0.0.1:6379`，JWT secret 开发默认值）；未记录/未注入任何生产 Secret
- 测试数据隔离：复用开发 MySQL/Redis（Task Verification 指定 `docker compose up -d` 即可）；集成测试自建隔离用户（`smokea*/smokeb*` 等）并在用例内清理文章表；运行结束已停止 serve 进程并清理 `storage/` 运行时产物，`git status` 恢复 clean

## Verification
| Check | Result | Evidence |
|---|---|---|
| Build | PASS | `go build ./...` exit 0；`go vet ./...` exit 0；`gofmt -l`（article 相关目录）无输出；`go build -o bin/my-shop .` 成功 |
| Unit/Integration Test | PASS | `go test -p 1 ./...` exit 0，全部包 `ok`（含 `internal/cmd` 38s、`internal/migrations` 16s、`internal/controller/iam` 等），无回归 |
| Race Test | PASS | `go test -race -run 'TestArticle' ./internal/cmd/...` exit 0（33.9s，含并发点赞唯一约束用例） |
| Migration | PASS | `migrate version` = `20261001000017 (dirty=false)`；`migrate up` 幂等（"没有待执行的 migration"）；`articles`/`article_likes`/`article_favorites` 三表存在 |
| Startup + Health | PASS | `serve` 启动成功；`GET /health` → HTTP 200 `{code:0,data:{status:ok}}` |
| Core Flow (API Smoke) | PASS | 真实 HTTP 主链路 28 项断言全过（见下 Acceptance Evidence） |
| Data | PASS | 删除后无孤儿数据（`orphan_likes=0`/`orphan_favorites=0`）；`uk_user_article(user_id,article_id)` 唯一约束真实存在（`Non_unique=0`） |
| Registry 一致性 | PASS | `origin/develop` 已 `RESERVED`：`16000-16999 | article-cms-v1`、`20261001000017 | articles | article-cms-v1`；实现 `16001/16002`、`17.up.sql` 与之对齐 |

## Acceptance Evidence
| AC / INV | 本次运行结果 |
|---|---|
| AC-001 发布文章（作者取自 Principal） | 真实 HTTP：A 发布成功，`author_username`=A；伪造 `author_id=999999/user_id=888888` 被忽略 |
| AC-002 修改文章（越权隔离） | 非作者 B `PUT` → 404/16001 且文章内容不变；作者修改成功 |
| AC-003/AC-010 删除 + 关联清理 | 作者删除 → 200；删后 `like/count`=0、详情 404/16001；DB 无孤儿 `article_likes`/`article_favorites` |
| AC-004/AC-005 公开列表/详情 | 无需 token 200；列表按 `id DESC`、分页正确；详情内容/作者用户名正确 |
| AC-006 我的文章 | A 的 `/my/articles` 含本人文章，B 的为空（不泄露） |
| AC-007 点赞 | 点赞→`liked:true`；重复点赞幂等仅 1 条；公开计数=1；取消回落；并发 8 连仅 1 条（`-race`） |
| AC-008 收藏 | 收藏→`favorited:true`；`/favorite/check` 正确；`/my/articles/favorites` 含该文章；取消幂等 |
| AC-009 详情点赞/收藏态 | 独立鉴权接口 `like/check`、`favorite/check` 生效，详情保持公开无 token（Contract D2） |
| AC-011 鉴权边界 | 未认证 `POST /articles`、`GET /my/articles` → 401/1002 且无写入；非法 token → 401；公开读 200 |
| AC-012 数据模型与迁移 | 三表迁移至 version 17，`migrate up` 幂等，`migrations_test.go` 通过 |
| AC-013 长期设计 | `docs/design/article.md` 存在（9282B），与 Contract/实现一致 |
| INV-001 归属绑定 | 落库 `author_id` 取自 `Principal.UserID`，伪造字段被忽略 |
| INV-002 越权 + 防枚举 | 非作者改/删 → 404/16001 零写入 |
| INV-003 唯一 + 幂等 | `uk_user_article` 兜底，重复/并发仅 1 条 |
| INV-004 删除清理 | 事务清理，DB 无孤儿数据 |
| INV-005 鉴权边界 | 写/状态接口 401；公开读 200；商品点赞/收藏无回归（全量测试通过） |

## Not Executed
| Check | Reason | Risk |
|---|---|---|
| 商品点赞/收藏独立手动回归 | 全量 `go test -p 1 ./...` 已覆盖既有模块无回归，无需额外手动项 | 无 |
| 恢复/回滚/性能 | Task/Contract 未要求；文章为同步写、无异步 | 无 |

## Remaining Risks
- Contract Open Risks（非本里程碑阻塞，交付后由 Owner 知悉）：删除事务与并发点赞的极小 TOCTOU 竞态窗口；文章硬删不可恢复、无审计；`author_username` LEFT JOIN 依赖 `users` 存在；migration version 15/16 为并行任务 RESERVED 的空隙（`latestMigrationVersion`=17 跳过 15/16，golang-migrate 容忍不连续）。
- 上述均为 Contract 已声明、与既有模块同立场的既有取舍，不改变本次 PASS 结论。

## Result
PASS
