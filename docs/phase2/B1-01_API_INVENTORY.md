# B1-01 SurgeCart Go 后端 API 全量盘点与契约事实提取

> 阶段：B1（后端工程审查与 API 契约冻结）
> 任务：B1-01 API 全量盘点
> 分支：`feat/p2-backend`
> 被审查代码版本（HEAD）：`82243c0fbce614e03c4ff57dcc829b5f68fb08f2`
> 报告生成时间：2026-11-10

**阅读约定**

- 本文所有路由、字段、错误码均来自当前源码的实际阅读结果，不以路由注册文件的名字推断功能。
- 每张清单表的 `Req` / `Resp` 只写关键字段与 DTO 类型；完整字段表见 §5。
- 无法从源码确认的内容一律标注 `UNKNOWN` 并给出原因，不做补全。
- 本次未修改任何 Go 业务代码、路由、迁移、配置、依赖或 Git 状态。

---

## 1. Git 状态与审查基线

| 项 | 值 | 证据 |
| --- | --- | --- |
| 仓库根 | `/root/projects/my-shop` | `git rev-parse --show-toplevel` |
| 当前分支 | `feat/p2-backend` | `git branch --show-current` |
| HEAD | `82243c0fbce614e03c4ff57dcc829b5f68fb08f2`（docs(report): 新增 main 集成与项目整理交付报告，2026-11-09 23:17:09 +0800） | `git log -1` |
| 上游跟踪 | `origin/feat/p2-backend` | `git status -sb` |
| 领先 / 落后 | `0 / 0` | `git rev-list --left-right --count origin/feat/p2-backend...HEAD` |
| 工作区 | 干净（`git status --porcelain` 无输出） | `git status --porcelain=v1` |
| Go 工具链 | `go1.26.0 linux/amd64`；`go.mod` 声明 `go 1.26.0` | `go version`、`go.mod:3` |
| 框架 | GoFrame v2 `v2.10.3`（`gogf/gf/v2`） | `go.mod` |
| 数据库 / 缓存 | MySQL 8 驱动、Redis 驱动；Kafka 客户端 `franz-go`；对象存储 `qiniu/go-sdk/v7` | `go.mod` |

**结论**：分支正确、工作区干净、与远端完全同步，满足本次审查前置条件，未做任何切分支/清理/覆盖操作。

**报告落盘后的状态**：`git status --porcelain` 仅显示 `?? docs/phase2/`（即本报告），`git diff --stat` 为空，HEAD 仍为 `82243c0`，无任何业务代码/路由/迁移/配置/依赖变更。

**运行环境事实**（影响 §13 测试结论）：

| 检查 | 结果 |
| --- | --- |
| 环境变量 `DATABASE_DEFAULT_PASS` / `REDIS_*` / `AUTH_JWT_SECRET` | 均未设置 |
| `.env` 文件 | 不存在（仅 `.env.example`） |
| `127.0.0.1:3306` / `6379` / `9092` / `8000` | 全部 CLOSED |
| Docker 容器 | `docker ps` 无输出 |

因此：涉及真实 MySQL/Redis/Kafka 的测试**无法安全执行**，本次未启动依赖、未执行 migration、未写入或删除任何数据。

---

## 2. Go 工程结构概览

```text
main.go                     入口，委托 internal/cmd.Main（serve / migrate / qiniu / admin / legacy-migrate）
api/<domain>/v1/*.go        API 契约 DTO（g.Meta path+method+tags+summary，字段 dc 说明）
internal/cmd/               路由注册 + 进程生命周期（routes_frontend.go / routes_admin.go / cmd.go）
internal/controller/<domain>/  薄控制器：解析 Principal → 调用 service
internal/service/           service 接口注册表（21 个接口，panic-on-missing 实现）
internal/logic/<domain>/    业务实现（事务、状态机、SQL、校验）
internal/middleware/        RequestID、Response 统一包装、Auth/AdminAuth/RequirePermission
internal/codes/             93 个码常量（含 CodeOK=0）+ 码表（HTTP 状态 / 安全 message）
internal/auth/              JWT（HS256，iss=surgecart）、Redis 会话、refresh token 轮换
internal/boot/              配置校验 → DB/Redis 装配 → schema 就绪检查 → seed 权限/超管 → 路由
internal/config/            统一配置契约注册表（默认值 + 环境变量映射 + 严格模式）
internal/migrations/        golang-migrate，25 个 up 迁移，`migrate up` 显式执行（serve 不自动迁移）
internal/worker/            秒杀后台 Runtime（db_scanner / kafka）、订单超时扫描
internal/mq/kafka/          Kafka 生产者/消费者
internal/metrics/           Prometheus 指标与 /metrics 处理
internal/storage/           本地存储（轮播图占位图，静态路由 /storage/banners）
internal/termination/       终止预算契约（preStop + drain + shutdown + 余量）
test/e2e/                   HTTP 级 E2E（真实路由 + 真实 MySQL/Redis）
test/manifest, test/deployk8s  部署清单 / K8s 静态与仿真测试
```

规模：88 个 Go 包、261 个 `.go` 文件（`api` 21、`internal` 179）、117 个 `_test.go`、802 个 `Test/Benchmark/Fuzz` 函数。

---

## 3. API 总量统计

### 3.1 总量

| 分类 | 数量 | 说明 |
| --- | --- | --- |
| 前台路由（`RegisterFrontendRoutes`） | **67** | 公开 14 + 登录用户 52 + 仅验签 1 |
| 后台路由（`RegisterAdminRoutes`） | **73** | 公开 1 + 仅验签 1 + 仅认证 10 + 认证+权限 61 |
| 健康检查（`root.Bind(health.NewV1())`） | **2** | `GET /health`、`GET /ready` |
| **常驻 API 路由合计** | **142** | 与历史统计「约 142」逐条核对一致 |
| 条件路由 | **+1** | `GET /metrics`，仅 `flash_sale.metrics.enabled=true` 时注册 |
| 非 API 静态路由 | 1 | `/storage/banners`（本地图片静态目录，非 JSON API） |

- 路由注册证据：`internal/cmd/routes_frontend.go:52-157`、`internal/cmd/routes_admin.go:52-188`、`internal/cmd/cmd.go:172-181`、`internal/cmd/cmd.go:224-231`。
- 逐条计数结果：前台文件 67 个注册调用、后台文件 73 个、健康 2 个；`GET /metrics` 在 `cmd.go:228` 单独绑定。

### 3.2 方法分布

| 方法 | 前台 | 后台 | 健康 | 合计 |
| --- | --- | --- | --- | --- |
| GET | 31 | 26 | 2 | **59** |
| POST | 22 | 26 | 0 | **48** |
| PUT | 5 | 11 | 0 | **16** |
| DELETE | 9 | 10 | 0 | **19** |
| 合计 | 67 | 73 | 2 | **142** |

### 3.3 认证分布

| 认证等级 | 数量 | 说明 |
| --- | --- | --- |
| 公开（无 token） | 17 | 前台公开 14 + `/admin/login` + `/health` + `/ready` |
| 用户 `middleware.Auth` | 52 | 校验签名 + JWT type=user + Redis 会话 + users.status + auth_epoch |
| 用户 `AuthSignatureOnly` | 1 | `POST /logout` |
| 管理员 `AdminAuth`（无权限码） | 10 | 见 §9.3 |
| 管理员 `AdminAuth + RequirePermission` | 61 | 覆盖 49 个不同权限码 |
| 管理员 `AdminAuthSignatureOnly` | 1 | `POST /admin/logout` |

### 3.4 业务域分布

| # | 业务域 | 接口数 | 前台 | 后台 |
| --- | --- | --- | --- | --- |
| 1 | 用户与认证（IAM 前台） | 9 | 9 | 0 |
| 2 | 收货地址 | 5 | 5 | 0 |
| 3 | 管理员认证与 RBAC | 20 | 0 | 20 |
| 4 | 商品（SPU） | 8 | 2 | 6 |
| 5 | 商品分类 | 5 | 2 | 3 |
| 6 | SKU | 3 | 0 | 3 |
| 7 | 库存 | 4 | 0 | 4 |
| 8 | 购物车 | 5 | 5 | 0 |
| 9 | 订单 | 10 | 6 | 4 |
| 10 | 退款 | 10 | 4 | 6 |
| 11 | 优惠券 | 5 | 2 | 3 |
| 12 | 秒杀 | 6 | 2 | 4 |
| 13 | 内容-轮播图 | 6 | 1 | 5 |
| 14 | 内容-推荐位 | 9 | 1 | 8 |
| 15 | 内容-文章（UGC） | 14 | 14 | 0 |
| 16 | 评价 | 6 | 5 | 1 |
| 17 | 收藏 | 4 | 4 | 0 |
| 18 | 点赞 | 4 | 4 | 0 |
| 19 | 文件上传（七牛直传） | 2 | 1 | 1 |
| 20 | 运营数据大屏 | 5 | 0 | 5 |
| 21 | 健康检查 | 2 | 0 | 0 |
| | **合计** | **142** | **67** | **73** |

---

## 4. 按业务域分类的 API 清单

### 4.0 图例

| 列 | 取值 |
| --- | --- |
| `Auth` | `P` 公开 / `U` 用户 Auth / `US` 用户仅验签 / `A` 管理员 AdminAuth / `AS` 管理员仅验签 |
| `Perm` | `-` 无权限码；否则为 `RequirePermission` 的权限 code |
| `Req` / `Resp` | DTO 名（字段全量见 §5） |
| `Err` | **主要业务码**（源码可见路径）；所有接口另可能返回 `1000` 内部错误、`1001` 参数错误（服务层显式校验时）、`1002` 未认证、`1003` 禁止访问 |
| `Page` | `N` 无分页；`Y` 分页（items/total/page/size，参数越界**静默归一**）；`Y*` 分页但越界返回 `1001`；`U` 无分页但返回无界全量集合 |
| `端` | `商` 用户商城 / `后` 管理后台 / `内` 内部或基础设施 |
| `Tests` | `ctrl` 控制器测试、`logic` 逻辑层测试、`e2e` HTTP 端到端测试、`—` 未找到直接用例（均为文件级覆盖，不代表逐字段断言） |
| `Impl` | `完整` handler→service→logic→持久化均存在；`部分` 存在但业务能力未闭环 |

### 4.1 用户与认证（前台 IAM，9）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | POST | `/register` | `iam.Register` | P | - | `RegisterReq` | `RegisterRes{id,username}` | 2001,1001 | N | 商 | ctrl,e2e | 完整 |
| 2 | POST | `/login` | `iam.Login` | P | - | `LoginReq` | `LoginRes{access_token,refresh_token,token_type,expires_in}` | 2002 | N | 商 | ctrl,e2e | 完整 |
| 3 | POST | `/refresh` | `iam.Refresh` | P | - | `RefreshReq` | `RefreshRes`（同 LoginRes） | 2012,2013,2014 | N | 商 | ctrl,e2e | 完整 |
| 4 | GET | `/me` | `iam.Me` | U | - | — | `MeRes{id,username}` | 1002 | N | 商 | ctrl,e2e | 完整 |
| 5 | POST | `/logout` | `iam.Logout` | US | - | — | `LogoutRes{}`（data=null） | 1002 | N | 商 | ctrl | 完整 |
| 6 | GET | `/sessions` | `iam.ListSessions` | U | - | — | `ListSessionsRes{items[]Session}` | 1000 | U | 商 | ctrl | 完整 |
| 7 | DELETE | `/sessions/:sid` | `iam.RevokeSession` | U | - | `RevokeSessionReq` | `RevokeSessionRes{}` | 2011 | N | 商 | ctrl | 完整 |
| 8 | POST | `/sessions/revoke-others` | `iam.RevokeOthers` | U | - | — | `{}` | 1000 | N | 商 | ctrl | 完整 |
| 9 | POST | `/sessions/revoke-all` | `iam.RevokeAll` | U | - | — | `{}` | 1000 | N | 商 | ctrl | 完整 |

源码：`api/iam/v1/iam.go:6-108`、`internal/controller/iam/iam.go:22-113`、`internal/logic/iam/iam.go`、`internal/logic/iam/refresh.go`。

### 4.2 收货地址（5）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 10 | POST | `/addresses` | `address.Create` | U | - | `CreateReq` | `CreateRes{Address}` | 1001,7002 | N | 商 | ctrl | 完整 |
| 11 | GET | `/addresses` | `address.List` | U | - | — | `ListRes{items[]Address}` | 1000 | U | 商 | ctrl | 完整 |
| 12 | GET | `/addresses/:id` | `address.Detail` | U | - | `DetailReq` | `DetailRes{Address}` | 7001 | N | 商 | ctrl | 完整 |
| 13 | PUT | `/addresses/:id` | `address.Update` | U | - | `UpdateReq` | `UpdateRes{Address}` | 7001,7002,1001 | N | 商 | ctrl | 完整 |
| 14 | DELETE | `/addresses/:id` | `address.Delete` | U | - | `DeleteReq` | `DeleteRes{}` | 7001 | N | 商 | ctrl | 完整 |

源码：`api/address/v1/address.go`、`internal/logic/address/address.go`。地址请求体**不接受** `user_id`，归属取自 Principal（`api/address/v1/address.go:22`）。

### 4.3 管理员认证与 RBAC（20）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 15 | POST | `/admin/login` | `admin.Login` | P | - | `LoginReq{username,password}` | `LoginRes{access_token,token_type,expires_in}` | 2002 | N | 后 | ctrl,e2e | 完整 |
| 16 | POST | `/admin/logout` | `admin.Logout` | AS | - | — | `{}` | 1002 | N | 后 | ctrl | 完整 |
| 17 | GET | `/admin/me` | `admin.Me` | A | - | — | `MeRes{id,username,is_super,roles[]}` | 1002 | N | 后 | ctrl,e2e | 完整 |
| 18 | GET | `/admin/users/:id/status` | `iam.GetUserStatus` | A | `user:read` | `GetUserStatusReq` | `{id,status}` | 2015 | N | 后 | e2e | 完整 |
| 19 | PUT | `/admin/users/:id/status` | `iam.UpdateUserStatus` | A | `user:status` | `{id,status(0/1),reason}` | `{}` | 1001,2015 | N | 后 | e2e | 完整 |
| 20 | POST | `/admin/admins` | `admin.CreateAdmin` | A | `admin:create` | `{username,password}` | `{id,username}` | 2004 | N | 后 | ctrl | 完整 |
| 21 | PUT | `/admin/admins/:id/status` | `admin.UpdateAdminStatus` | A | `admin:disable` | `{id,status}` | `{}` | 2003,2005,2006 | N | 后 | ctrl | 完整 |
| 22 | DELETE | `/admin/admins/:id` | `admin.DeleteAdmin` | A | `admin:delete` | `{id}` | `{}` | 2003,2005,2006 | N | 后 | ctrl | 完整 |
| 23 | POST | `/admin/admins/:id/roles` | `admin.AssignAdminRole` | A | `admin:assign_role` | `{id,role_id}` | `{}` | 2003,2007 | N | 后 | ctrl | 完整 |
| 24 | DELETE | `/admin/admins/:id/roles/:role_id` | `admin.RemoveAdminRole` | A | `admin:assign_role` | path 双参 | `{}` | 2003,2007 | N | 后 | ctrl | 完整 |
| 25 | POST | `/admin/roles` | `admin.CreateRole` | A | `role:create` | `{name,description}` | `{id}` | 2008 | N | 后 | ctrl | 完整 |
| 26 | GET | `/admin/roles` | `admin.ListRole` | A | `role:list` | — | `{items[]Role}` | 1000 | U | 后 | ctrl | 完整 |
| 27 | PUT | `/admin/roles/:id` | `admin.UpdateRole` | A | `role:update` | `{name?,description?}` | `Role` | 2007,2008 | N | 后 | ctrl | 完整 |
| 28 | DELETE | `/admin/roles/:id` | `admin.DeleteRole` | A | `role:delete` | `{id}` | `{}` | 2007 | N | 后 | ctrl | 完整 |
| 29 | POST | `/admin/roles/:id/permissions` | `admin.AssignRolePermission` | A | `role:assign_permission` | `{id,permission_id}` | `{}` | 2007,2009 | N | 后 | ctrl | 完整 |
| 30 | DELETE | `/admin/roles/:id/permissions/:permission_id` | `admin.RemoveRolePermission` | A | `role:assign_permission` | path 双参 | `{}` | 2007,2009 | N | 后 | ctrl | 完整 |
| 31 | POST | `/admin/permissions` | `admin.CreatePermission` | A | `permission:create` | `{code,name,description}` | `{id}` | 2010 | N | 后 | ctrl | 完整 |
| 32 | GET | `/admin/permissions` | `admin.ListPermission` | A | `permission:list` | — | `{items[]Permission}` | 1000 | U | 后 | ctrl | 完整 |
| 33 | PUT | `/admin/permissions/:id` | `admin.UpdatePermission` | A | `permission:update` | `{code?,name?,description?}` | `Permission` | 2009,2010 | N | 后 | ctrl | 完整 |
| 34 | DELETE | `/admin/permissions/:id` | `admin.DeletePermission` | A | `permission:delete` | `{id}` | `{}` | 2009 | N | 后 | ctrl | 完整 |

源码：`api/admin/v1/admin.go`、`api/iam/v1/iam.go:110-133`、`internal/logic/admin/admin.go`。
注意：**不存在** `GET /admin/admins`（管理员列表）接口，后台无法在 UI 中枚举管理员。

### 4.4 商品 SPU（8）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 35 | GET | `/products` | `product.List` | P | - | `ListReq` | `{items[]Product,total,page,size}` | 1000 | Y | 商 | ctrl | 完整 |
| 36 | GET | `/products/:id` | `product.Detail` | P | - | `{id}` | `{Product,skus[]Sku}` | 4001 | N | 商 | ctrl | 完整 |
| 37 | GET | `/admin/products` | `product.AdminList` | A | - | `AdminListReq` | `{items[]Product,total,page,size}` | 1000 | Y | 后 | ctrl | 完整 |
| 38 | GET | `/admin/products/:id` | `product.AdminDetail` | A | - | `{id}` | `{Product,skus[]Sku}` | 4001 | N | 后 | ctrl | 完整 |
| 39 | POST | `/admin/products` | `product.Create` | A | `product:create` | `CreateReq` | `CreateRes{Product}` | 4002,4003,4004,4007 | N | 后 | ctrl | 完整 |
| 40 | PUT | `/admin/products/:id` | `product.Update` | A | `product:update` | `UpdateReq` | `UpdateRes{Product}` | 4001,4002,4003,4004,4005,4006,4007 | N | 后 | ctrl | 完整 |
| 41 | POST | `/admin/products/:id/on-shelf` | `product.OnShelf` | A | `product:on_shelf` | `{id}` | `OnShelfRes{Product}` | 4001,4005 | N | 后 | ctrl | 完整 |
| 42 | POST | `/admin/products/:id/off-shelf` | `product.OffShelf` | A | `product:off_shelf` | `{id}` | `OffShelfRes{Product}` | 4001,4005 | N | 后 | ctrl | 完整 |

源码：`api/product/v1/product.go`、`internal/logic/product/product.go`。
事实：前台列表仅 `status=on_shelf`；详情通过 `UPDATE ... WHERE id=? AND status=on_shelf` 合并可见性校验与 `view_count+1`（`internal/logic/product/product.go:78-103`）——**GET 有写副作用**。

### 4.5 商品分类（5）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 43 | GET | `/categories` | `categories.List` | P | - | — | `{items[]Category(树)}` | 1000 | N | 商 | ctrl,logic | 完整 |
| 44 | GET | `/categories/:id` | `categories.Detail` | P | - | `{id}` | `{Category}` | 3001 | N | 商 | ctrl,logic | 完整 |
| 45 | POST | `/categories` | `categories.Create` | A | `category:create` | `{parent_id,name,sort,status?}` | `{id}` | 1001,3002,3004 | N | 后 | ctrl,logic | 完整 |
| 46 | PUT | `/categories/:id` | `categories.Update` | A | `category:update` | `{parent_id?,name?,sort?,status?}` | `{Category}` | 1001,3001,3002,3004 | N | 后 | ctrl,logic | 完整 |
| 47 | DELETE | `/categories/:id` | `categories.Delete` | A | `category:delete` | `{id}` | `{}` | 3001,3003,3005 | N | 后 | ctrl,logic | 完整 |

源码：`api/categories/v1/categories.go`、`internal/logic/categories/categories.go:44-62`。
事实：`GET /categories` **只返回启用项**（禁用父节点整棵子树隐藏）；`GET /categories/:id` **不校验启用状态**，可公开读到禁用分类（列表/详情可见性不一致）。

### 4.6 SKU（3）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 48 | POST | `/admin/skus` | `sku.Create` | A | `sku:create` | `{product_id,name,price,status?}` | `{Sku}` | 4001,5002,5003,5004 | N | 后 | ctrl | 完整 |
| 49 | PUT | `/admin/skus/:id` | `sku.Update` | A | `sku:update` | `{name?,price?,status?}` | `{Sku}` | 5001,5002,5003,5004 | N | 后 | ctrl | 完整 |
| 50 | DELETE | `/admin/skus/:id` | `sku.Delete` | A | `sku:delete` | `{id}` | `{}` | 5001,5005 | N | 后 | ctrl | 完整 |

源码：`api/sku/v1/sku.go`、`internal/logic/sku/sku.go`。
事实：**没有** `GET /admin/skus` 或 `GET /skus/:id`；SKU 只能通过商品详情内嵌读取。`Sku` **不含库存字段、不含图片字段**。

### 4.7 库存（4）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 51 | GET | `/admin/inventories/:sku_id` | `inventory.Get` | A | - | `{sku_id}` | `{sku_id,quantity,created_at,updated_at}` | 5001 | N | 后 | ctrl | 完整 |
| 52 | GET | `/admin/inventories/:sku_id/logs` | `inventory.ListLogs` | A | - | `{sku_id,page,size}` | `{items[]Log,total,page,size}` | 5001 | Y | 后 | ctrl | 完整 |
| 53 | POST | `/admin/inventories/:sku_id/increase` | `inventory.Increase` | A | `inventory:increase` | `{sku_id,quantity}` | `{Inventory}` | 5001,6002 | N | 后 | ctrl | 完整 |
| 54 | POST | `/admin/inventories/:sku_id/deduct` | `inventory.Deduct` | A | `inventory:deduct` | `{sku_id,quantity}` | `{Inventory}` | 5001,6001,6002 | N | 后 | ctrl | 完整 |

源码：`api/inventory/v1/inventory.go`、`internal/logic/inventory/inventory.go:168-205`。
事实：**前台无任何库存查询接口**；`Product`/`Sku` DTO 均无库存字段。用户只有在购物车列表里才能看到 `stock`。

### 4.8 购物车（5）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 55 | GET | `/cart` | `cart.List` | U | - | — | `{items[]CartItem}` | 1000 | U | 商 | ctrl | 完整 |
| 56 | POST | `/cart/items` | `cart.Add` | U | - | `{sku_id,quantity?}` | `{CartItem}` | 5001,8002,8003 | N | 商 | ctrl | 完整 |
| 57 | PUT | `/cart/items/:id` | `cart.UpdateQuantity` | U | - | `{id,quantity}` | `{CartItem}` | 8001,8002 | N | 商 | ctrl | 完整 |
| 58 | PUT | `/cart/items/:id/selected` | `cart.UpdateSelected` | U | - | `{id,selected}` | `{CartItem}` | 8001 | N | 商 | ctrl | 完整 |
| 59 | DELETE | `/cart/items/:id` | `cart.Delete` | U | - | `{id}` | `{}` | 8001 | N | 商 | ctrl | 完整 |

源码：`api/cart/v1/cart.go`、`internal/logic/cart/cart.go:66-223`。
事实：加购只校验 SKU enabled + 商品 on_shelf，**不校验库存**；重复加购原子累加，累加后 >999 整体回滚（`cart.go:108-134`）。

### 4.9 订单（10）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 60 | POST | `/orders` | `order.Create` | U | - | `CreateReq` | `CreateRes{Order}` | 7001,5001,9003,9004,9005,9006,19007,6001 | N | 商 | e2e | 完整 |
| 61 | GET | `/orders` | `order.List` | U | - | — | `{items[]Order}` | 1000 | U | 商 | e2e | 部分 |
| 62 | GET | `/orders/:id` | `order.Detail` | U | - | `{id}` | `{Order}` | 9001 | N | 商 | e2e | 完整 |
| 63 | POST | `/orders/:id/pay` | `order.Pay` | U | - | `{id}` | `{Order}` | 9001,9002 | N | 商 | e2e | 完整 |
| 64 | POST | `/orders/:id/cancel` | `order.Cancel` | U | - | `{id}` | `{Order}` | 9001,9002 | N | 商 | e2e | 完整 |
| 65 | POST | `/orders/:id/receive` | `order.Receive` | U | - | `{id}` | `{Order}` | 9001,9002 | N | 商 | e2e | 完整 |
| 66 | POST | `/admin/orders/:id/ship` | `order.AdminControllerV1.Ship` | A | `order:ship` | `{id}` | `{Order}` | 9001,9002,18005 | N | 后 | e2e | 部分 |
| 67 | GET | `/admin/orders` | `order.AdminList` | A | `order:list` | `AdminListReq` | `{items[]AdminOrderListItem,total,page,size}` | 1001 | Y* | 后 | e2e | 完整 |
| 68 | GET | `/admin/orders/stats` | `order.AdminStats` | A | `order:list` | — | `{total,stats[{status,count}]}` | 1000 | N | 后 | e2e | 完整 |
| 69 | GET | `/admin/orders/:id` | `order.AdminDetail` | A | `order:view` | `{id}` | `{Order}` | 9001 | N | 后 | e2e | 完整 |

源码：`api/order/v1/order.go`、`internal/logic/order/order.go:132-217/406-419/997-1107`。
事实：
- `GET /orders` 拉取本人**全部**订单并按单加载明细（N+1），无分页/状态筛选（`order.go:406-419`）。
- 创建订单在单事务内写 orders + order_items + 扣库存 + 预占券 + 清购物车（`order.go:292-383`）。
- 后台列表严格校验 page/size/sort/order/时间范围（`order.go:1110-1171`），越界返回 1001。
- `Ship` 请求体只有 `id`，**无运单号/承运商字段**。
- `Pay` 为 Mock：无支付方式、无第三方流水号、无支付单实体。

### 4.10 退款（10）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 70 | POST | `/orders/:id/refund` | `refund.Apply` | U | - | `{id,reason}` | `ApplyRes{Refund}` | 1001,18001,18003,18004 | N | 商 | e2e | 完整 |
| 71 | GET | `/refunds` | `refund.List` | U | - | `{order_id?}` | `{items[]Refund}` | 1000 | U | 商 | e2e | 部分 |
| 72 | GET | `/refunds/:id` | `refund.Detail` | U | - | `{id}` | `{Refund}` | 18001 | N | 商 | e2e | 完整 |
| 73 | POST | `/refunds/:id/revoke` | `refund.Revoke` | U | - | `{id}` | `{Refund}` | 18001,18002 | N | 商 | e2e | 完整 |
| 74 | GET | `/admin/refunds` | `refund.AdminList` | A | `refund:list` | `AdminListReq` | `{items[]Refund,total,page,size}` | 1001 | Y* | 后 | e2e | 完整 |
| 75 | GET | `/admin/refunds/:id` | `refund.AdminDetail` | A | `refund:view` | `{id}` | `{Refund}` | 18001 | N | 后 | e2e | 完整 |
| 76 | GET | `/admin/refunds/:id/audits` | `refund.ListAudits` | A | `refund:view` | `{id}` | `{items[]RefundAudit}` | 18001 | U | 后 | e2e | 完整 |
| 77 | POST | `/admin/refunds/:id/review` | `refund.Review` | A | `refund:review` | `{id,result,reason}` | `{Refund}` | 18001,18002 | N | 后 | e2e | 完整 |
| 78 | POST | `/admin/refunds/:id/execute` | `refund.Execute` | A | `refund:execute` | `{id,result,reason}` | `{Refund}` | 18001,18002 | N | 后 | e2e | 完整 |
| 79 | POST | `/admin/refunds/:id/close` | `refund.Close` | A | `refund:execute` | `{id,reason}` | `{Refund}` | 18001,18002 | N | 后 | e2e | 完整 |

源码：`api/refund/v1/refund.go`、`internal/logic/refund/refund.go:1-40/60-80`。
状态机（DB TINYINT / API string）：10 applying → 20 pending_refund → 30 refund_failed →（60 refund_succeeded | 70 closed）；40 review_rejected、50 revoked 为终态。退款与发货通过同一 `orders` 行锁互斥。

### 4.11 优惠券（5）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 80 | POST | `/coupons/:template_id/claim` | `coupon.Claim` | U | - | `{template_id}` | `{UserCoupon}` | 19001,19004,19005,19006 | N | 商 | e2e | 完整 |
| 81 | GET | `/my/coupons` | `coupon.ListMine` | U | - | — | `{items[]UserCoupon}` | 1000 | U | 商 | e2e | 部分 |
| 82 | POST | `/admin/coupons` | `coupon.AdminControllerV1.AdminCreate` | A | `coupon:create` | `AdminCreateReq` | `{Template}` | 19002 | N | 后 | e2e | 完整 |
| 83 | GET | `/admin/coupons` | `coupon.AdminControllerV1.AdminList` | A | `coupon:list` | — | `{items[]Template}` | 1000 | U | 后 | e2e | 完整 |
| 84 | POST | `/admin/coupons/:id/stop` | `coupon.AdminControllerV1.AdminStop` | A | `coupon:stop` | `{id}` | `{Template}` | 19001 | N | 后 | e2e | 完整 |

源码：`api/coupon/v1/coupon.go`、`internal/logic/coupon/coupon.go:89-278`。
事实：**没有前台可领取券列表接口**（用户必须已知 `template_id` 才能领取）；**没有"订单可用券"查询接口**；`GetDiscount/LockInTx/MarkUsedInTx/ReleaseInTx` 仅供订单内部调用，不暴露 HTTP。

### 4.12 秒杀（6）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 85 | POST | `/flash-sales/:id/orders` | `flashsale.CreateOrder` | U | - | `{id,sku_id,idempotency_key}` | `{status,idempotency_key,activity_id,sku_id}` | 12001,12002,12003,12004,12005,12009,12010,1005,1001 | N | 商 | e2e,logic | 完整 |
| 86 | GET | `/flash-sales/:id/orders/result` | `flashsale.GetOrderResult` | U | - | `{id,idempotency_key(query)}` | `{status,order?,fail_code?}` | 1004 | N | 商 | e2e,logic | 完整 |
| 87 | POST | `/admin/flash-sales` | `flashsale.AdminControllerV1.Create` | A | `flash_sale:create` | `CreateReq` | `{Activity}` | 12007 | N | 后 | e2e | 完整 |
| 88 | PUT | `/admin/flash-sales/:id` | `flashsale.AdminControllerV1.Update` | A | `flash_sale:update` | `UpdateReq` | `{Activity}` | 12001,12007 | N | 后 | e2e | 完整 |
| 89 | POST | `/admin/flash-sales/requests/:id/repair` | `flashsale.AdminControllerV1.RepairRequest` | A | `flash_sale:repair` | `{id,target_status,reason}` | `{id,status}` | 12008 | N | 后 | e2e | 完整 |
| 90 | GET | `/admin/flash-sales/requests/:id/audits` | `flashsale.AdminControllerV1.ListRequestAudits` | A | `flash_sale:repair` | `{id}` | `{items[]RequestAudit}` | 1000 | U | 后 | e2e | 完整 |

源码：`api/flashsale/v1/flash_sale.go`、`internal/logic/flashsale/flashsale.go`（`CreateOrder` 见 337-410）、`internal/logic/flashsale/consume.go`、`outbox.go`。
事实：下单为**异步受理**（返回 `queued`），订单在后台消费器事务内落库；`FlashOrder.Quantity` 恒为 1；**没有**前台秒杀活动列表/详情接口，也**没有**后台活动列表/详情接口。

### 4.13 轮播图（6）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 91 | GET | `/banners` | `banner.List` | P | - | — | `{items[]BannerItem}` | 1000 | N | 商 | e2e | 完整 |
| 92 | GET | `/admin/banners` | `banner.AdminList` | A | - | — | `{items[]Banner}` | 1000 | U | 后 | e2e | 完整 |
| 93 | GET | `/admin/banners/:id` | `banner.AdminDetail` | A | - | `{id}` | `{Banner}` | 14001 | N | 后 | e2e | 完整 |
| 94 | POST | `/admin/banners` | `banner.Create` | A | `banner:create` | `CreateReq` | `{Banner}` | 14002 | N | 后 | e2e | 完整 |
| 95 | PUT | `/admin/banners/:id` | `banner.Update` | A | `banner:update` | `UpdateReq` | `{Banner}` | 14001,14002 | N | 后 | e2e | 完整 |
| 96 | DELETE | `/admin/banners/:id` | `banner.Delete` | A | `banner:delete` | `{id}` | `{}` | 14001 | N | 后 | e2e | 完整 |

源码：`api/banner/v1/banner.go`、`internal/logic/banner/banner.go:44-64`。公开列表仅 `status=1`，按 `sort,id` 升序。

### 4.14 推荐位（9）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 97 | GET | `/recommendations/:code` | `recommendation.Frontend` | P | - | `{code}` | `{code,name,items[]ItemSnapshot}` | 1000 | N | 商 | e2e | 完整 |
| 98 | GET | `/admin/recommend-positions` | `recommendation.AdminList` | A | - | — | `{items[]Position}` | 1000 | U | 后 | e2e | 完整 |
| 99 | GET | `/admin/recommend-positions/:id` | `recommendation.AdminDetail` | A | - | `{id}` | `{Position,items[]Item}` | 15001 | N | 后 | e2e | 完整 |
| 100 | POST | `/admin/recommend-positions` | `recommendation.Create` | A | `recommend:create` | `{code,name,status?}` | `{Position}` | 15002,15003 | N | 后 | e2e | 完整 |
| 101 | PUT | `/admin/recommend-positions/:id` | `recommendation.Update` | A | `recommend:update` | `{id,name?,status?}` | `{Position}` | 15001,15003 | N | 后 | e2e | 完整 |
| 102 | DELETE | `/admin/recommend-positions/:id` | `recommendation.Delete` | A | `recommend:delete` | `{id}` | `{}` | 15001 | N | 后 | e2e | 完整 |
| 103 | POST | `/admin/recommend-positions/:id/items` | `recommendation.AddItem` | A | `recommend:item` | `{id,product_id,sort?}` | `{Item}` | 15001,15003,4001,15005 | N | 后 | e2e | 完整 |
| 104 | DELETE | `/admin/recommend-positions/:id/items/:product_id` | `recommendation.RemoveItem` | A | `recommend:item` | path 双参 | `{}` | 15001,15004 | N | 后 | e2e | 完整 |
| 105 | PUT | `/admin/recommend-positions/:id/items/sort` | `recommendation.UpdateSort` | A | `recommend:item` | `{id,product_ids[]}` | `{items[]Item}` | 15001,15003,15006 | N | 后 | e2e | 完整 |

源码：`api/recommendation/v1/recommendation.go`、`internal/logic/recommendation/recommendation.go:103-160`。
事实：推荐位不存在/禁用时前台返回 `code="" name="" items=[]`（200，不报 404）；只返回 `on_shelf` 商品快照。

### 4.15 文章 UGC（14）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 106 | GET | `/articles` | `article.List` | P | - | `{page,size}` | `{items[]ArticleItem,total,page,size}` | 1000 | Y | 商 | e2e | 完整 |
| 107 | GET | `/articles/:id` | `article.Detail` | P | - | `{id}` | `{Article}` | 16001 | N | 商 | e2e | 完整 |
| 108 | GET | `/articles/:id/like/count` | `article.LikeCount` | P | - | `{id}` | `{count}` | 1000 | N | 商 | e2e | 完整 |
| 109 | POST | `/articles` | `article.Create` | U | - | `{title,content}` | `{Article}` | 16002 | N | 商 | e2e | 完整 |
| 110 | PUT | `/articles/:id` | `article.Update` | U | - | `{id,title,content}` | `{Article}` | 16001,16002 | N | 商 | e2e | 完整 |
| 111 | DELETE | `/articles/:id` | `article.Delete` | U | - | `{id}` | `{}` | 16001 | N | 商 | e2e | 完整 |
| 112 | GET | `/my/articles` | `article.MyList` | U | - | `{page,size}` | `{items[]Article,total,page,size}` | 1000 | Y | 商 | e2e | 完整 |
| 113 | POST | `/articles/:id/like` | `article.Like` | U | - | `{id}` | `{liked}` | 16001 | N | 商 | e2e | 完整 |
| 114 | DELETE | `/articles/:id/like` | `article.Unlike` | U | - | `{id}` | `{}` | 16001 | N | 商 | e2e | 完整 |
| 115 | GET | `/articles/:id/like/check` | `article.LikeCheck` | U | - | `{id}` | `{liked}` | 16001 | N | 商 | e2e | 完整 |
| 116 | POST | `/articles/:id/favorite` | `article.Favorite` | U | - | `{id}` | `{favorited}` | 16001 | N | 商 | e2e | 完整 |
| 117 | DELETE | `/articles/:id/favorite` | `article.Unfavorite` | U | - | `{id}` | `{}` | 16001 | N | 商 | e2e | 完整 |
| 118 | GET | `/articles/:id/favorite/check` | `article.FavoriteCheck` | U | - | `{id}` | `{favorited}` | 16001 | N | 商 | e2e | 完整 |
| 119 | GET | `/my/articles/favorites` | `article.MyFavorites` | U | - | `{page,size}` | `{items[]ArticleItem,total,page,size}` | 1000 | Y | 商 | e2e | 完整 |

源码：`api/article/v1/article.go`、`internal/logic/article/article.go`。文章**无状态/无审核字段**，删除为硬删除并事务清理点赞/收藏。

### 4.16 评价（6）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 120 | GET | `/products/:id/reviews` | `review.ListProduct` | P | - | `{id,page,size}` | `{items[]ReviewItem,page,size,total,avg_rating,count}` | 1000 | Y | 商 | e2e | 完整 |
| 121 | POST | `/reviews` | `review.Create` | U | - | `{order_item_id,rating,content}` | `{Review}` | 1001,10002,10003,10004 | N | 商 | e2e | 完整 |
| 122 | GET | `/my/reviews` | `review.MyList` | U | - | `{page,size}` | `{items[]Review,total,page,size}` | 1000 | Y | 商 | e2e | 完整 |
| 123 | PUT | `/reviews/:id` | `review.Update` | U | - | `{id,rating,content}` | `{Review}` | 10001,10004 | N | 商 | e2e | 完整 |
| 124 | DELETE | `/reviews/:id` | `review.Delete` | U | - | `{id}` | `{Review}` | 10001 | N | 商 | e2e | 完整 |
| 125 | POST | `/admin/reviews/:id/take-down` | `review.AdminControllerV1.TakeDown` | A | `review:take_down` | `{id}` | `{Review}` | 10001 | N | 后 | e2e | 完整 |

源码：`api/review/v1/review.go`、`internal/logic/review/review.go`（资格判定 `loadOwnedCompletedItem`：订单项属于本人且 `orders.status=50`）。
事实：后台**没有评价列表/审核队列接口**，只能在已知评价 id 时下架。

### 4.17 收藏（4）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 126 | POST | `/favorites` | `favorite.Add` | U | - | `{product_id}` | `{FavoriteItem}` | 4001,11001 | N | 商 | e2e | 完整 |
| 127 | DELETE | `/favorites/:product_id` | `favorite.Remove` | U | - | `{product_id}` | `{}` | 1000 | N | 商 | e2e | 完整 |
| 128 | GET | `/favorites` | `favorite.List` | U | - | `{page,size}` | `{items[]FavoriteItem,total,page,size}` | 1000 | Y | 商 | e2e | 完整 |
| 129 | GET | `/favorites/check` | `favorite.Check` | U | - | `{product_id(query)}` | `{favorited}` | 1001 | N | 商 | e2e | 完整 |

源码：`api/favorite/v1/favorite.go`、`internal/logic/favorite/favorite.go:80-125`。重复收藏幂等成功；商品不存在返回 **4001（商品域码）**。

### 4.18 点赞（4）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 130 | GET | `/likes/count` | `like.Count` | P | - | `{product_id(query)}` | `{count}` | 1001 | N | 商 | e2e | 完整 |
| 131 | POST | `/likes` | `like.Like` | U | - | `{product_id}` | `{liked}` | 4001,13001 | N | 商 | e2e | 完整 |
| 132 | DELETE | `/likes/:product_id` | `like.Cancel` | U | - | `{product_id}` | `{}` | 1000 | N | 商 | e2e | 完整 |
| 133 | GET | `/likes/check` | `like.Check` | U | - | `{product_id(query)}` | `{liked}` | 1001 | N | 商 | e2e | 完整 |

源码：`api/like/v1/like.go`、`internal/logic/like/like.go`。

### 4.19 文件上传（2）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 134 | GET | `/qiniu/upload/token` | `upload.FrontendToken` | U | - | `{filename?,content_type?}` | `TokenRes` | 17001,17002,17003 | N | 商 | logic,e2e | 完整 |
| 135 | GET | `/admin/qiniu/upload/token` | `upload.AdminToken` | A | - | `{filename?,content_type?}` | `TokenRes` | 17001,17002,17003 | N | 后 | logic,e2e | 完整 |

源码：`api/upload/v1/upload.go`、`internal/logic/upload/upload.go:84-140`。
事实：后端只**签发七牛直传凭证**（token/key/upload_url/domain/final_url/expires_at），不接收文件、不落库、不感知上传结果；无删除/列表接口。

### 4.20 运营数据大屏（5）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 136 | GET | `/admin/dashboard/overview` | `dashboard.Overview` | A | `dashboard:view` | — | `{user_total,today_new_users,product_total,order_total,today_new_orders,today_order_amount}` | 1000 | N | 后 | logic,e2e | 完整 |
| 137 | GET | `/admin/dashboard/orders/trend` | `dashboard.Trend` | A | `dashboard:view` | — | `{days[{date,order_count}]}` | 1000 | N | 后 | logic,e2e | 完整 |
| 138 | GET | `/admin/dashboard/orders/status` | `dashboard.Status` | A | `dashboard:view` | `{start_time?,end_time?}` | `{total,status_distribution[{status(int),count}]}` | 1001 | N | 后 | logic,e2e | 完整 |
| 139 | GET | `/admin/dashboard/products/top` | `dashboard.TopProducts` | A | `dashboard:view` | `{limit(string)?}` | `{items[{rank,product_id,product_name,sales}]}` | 1001 | N | 后 | logic,e2e | 完整 |
| 140 | GET | `/admin/dashboard/flash-sales` | `dashboard.FlashSales` | A | `dashboard:view` | — | `{activity_total,activity_status,request_status,current_queued,success_orders}` | 1000 | N | 后 | logic,e2e | 完整 |

源码：`api/dashboard/v1/dashboard.go`、`internal/logic/dashboard/dashboard.go:37-143`。
事实：`status_distribution.status` 为**整数**（10/20/30/40/50/60/70），与订单接口的字符串状态枚举冲突（见 §15）。

### 4.21 健康检查（2，内部）

| # | Method | Path | Handler | Auth | Perm | Req | Resp | Err | Page | 端 | Tests | Impl |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 141 | GET | `/health` | `health.Liveness` | P | - | — | `{status:"ok",time}` | - | N | 内 | ctrl,logic | 完整 |
| 142 | GET | `/ready` | `health.Readiness` | P | - | — | `{status:"ready"}` | 1005 | N | 内 | ctrl,logic | 完整 |

源码：`api/health/v1/health.go`、`internal/logic/health/health.go:1-80`。
`/ready` 仅探测 MySQL PingMaster + Redis PING（1s 超时、3s 结果缓存），不探测 Kafka；失败返回 `1005/503`。

### 4.22 条件接口与静态路由（不计入 142）

| Method | Path | 说明 | 证据 |
| --- | --- | --- | --- |
| GET | `/metrics` | Prometheus 文本格式；`flash_sale.metrics.enabled=true` 才注册；**不经** JSON 包装 | `internal/cmd/cmd.go:224-231` |
| GET | `/storage/banners/*` | 本地轮播图占位图静态目录 | `internal/cmd/cmd.go:209-220` |

---

## 5. 各 API 的实际请求与响应结构

> 本节字段全部来自 `api/*/v1/*.go` 的 struct tag 与 logic 赋值路径。Go 类型 `*gtime.Time` 的 JSON 表现为字符串（格式见 §11.3）。

### 5.1 Product / Sku / Category

**Product**（`api/product/v1/product.go:20-33`）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| id | int64 | 商品 id |
| name | string | 商品名 |
| brand | string | 品牌 |
| category_id | int64 | 分类 id |
| price | int64 | 价格（整数分） |
| main_image | string | 主图 URL |
| detail | string | 详情 |
| status | string | `draft` / `on_shelf` / `off_shelf` |
| view_count | int64 | 浏览量（只读） |
| images | []string | 图片 URL（按 sort 升序） |
| created_at / updated_at | time | 创建/更新时间 |

**Sku**（`api/sku/v1/sku.go:17-25`）：`id`、`product_id`、`name`、`price(int64 分)`、`status(enabled/disabled)`、`created_at`、`updated_at`。**无 stock、无 image、无 attributes**。

**Category**（`api/categories/v1/categories.go:9-18`）：`id`、`parent_id`、`name`、`sort(int)`、`status(int 0/1)`、`created_at`、`updated_at`、`children[]`（树形时填充，`omitempty`）。

**Product Create/Update 请求**（`api/product/v1/product.go:98-126`）

| 字段 | 类型 | 约束 |
| --- | --- | --- |
| name | string | 非空 ≤128 |
| brand | string | ≤64 |
| category_id | int64 | 存在且启用的**叶子**分类 |
| price | `json.Number` | 整数分 0~99999999 |
| main_image | string | ≤512 |
| detail | string | 可选 |
| images | []string | 可选；Update 提供则全量替换 |
| status | `*string`（仅 Update） | 普通更新忽略合法值、拒绝非法值 |

### 5.2 CartItem

`api/cart/v1/cart.go:13-31`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| id | int64 | 条目 id |
| sku_id | int64 | SKU id |
| quantity | int64 | 数量 |
| selected | bool | 勾选状态 |
| price_snapshot | int64 | 加购时价格快照（分） |
| product_id | *int64 | SKU 删除时为 null |
| product_name / product_main_image / sku_name | string | 联查快照 |
| current_price | *int64 | 当前 SKU 价格 |
| price_changed | bool | 与快照是否不一致 |
| available | bool | SKU enabled 且商品 on_shelf |
| unavailable_reason | string | `""` / `sku_deleted` / `disabled` / `off_shelf` |
| stock | *int64 | 当前库存（无记录为 0） |
| insufficient | bool | quantity > stock |
| created_at / updated_at | time | — |

请求：`AddReq{sku_id int64, quantity *json.Number}`（默认 1，上限 999）；`UpdateQuantityReq{quantity json.Number}`；`UpdateSelectedReq{selected bool}`。

### 5.3 Order / OrderItem

`api/order/v1/order.go:32-72`

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| id | int64 | 订单 id |
| order_no | string | 全局唯一订单号 |
| user_id | int64 | 归属用户 |
| status | string | 7 态字符串枚举 |
| total_amount | int64 | 应付（分）= original - discount |
| original_amount | int64 | 优惠前（分） |
| discount_amount | int64 | 优惠金额（分） |
| user_coupon_id | *int64 | 使用的用户券 id |
| idempotency_key | string | 幂等键 |
| recipient_name / phone / province / city / district / detail | string | 地址快照 |
| address_id | *int64 | 地址软引用 |
| expire_at | time | 支付截止 |
| cancel_reason | *int | 1 用户 / 2 超时 |
| paid_at / shipped_at / received_at / completed_at / cancelled_at / refunded_at | time | 状态时间 |
| created_at / updated_at | time | — |
| items | []OrderItem | 订单项 |

**OrderItem**：`id`、`sku_id`、`product_id`、`sku_name`、`product_name`、`product_main_image`、`price(int64 分)`、`quantity(int64)`。

**CreateReq**（`api/order/v1/order.go:76-84`）

| 字段 | 类型 | 约束 |
| --- | --- | --- |
| source | string | `v:"required"`，`cart` / `direct` |
| address_id | int64 | `v:"required"`，须属于当前用户 |
| idempotency_key | string | `v:"required"`；同键同内容幂等，同键不同内容 9006 |
| sku_id | int64 | `source=direct` 必填 |
| quantity | int64 | `source=direct` 必填，正整数 |
| coupon_id | int64 | 用户券 id（0/缺省不用券） |

**AdminListReq**（`api/order/v1/order.go:157-168`）：`page *int`、`size *int`、`order_no string`、`user_id *int64`、`status string`、`start_time string`（RFC3339）、`end_time string`（RFC3339）、`sort string`（`id`/`created_at`）、`order string`（`asc`/`desc`）。

**AdminOrderListItem**：`id`、`order_no`、`user_id`、`status(string)`、`total_amount`、`created_at`（轻量，无 items）。

**AdminOrderStatsRes**：`total(int64)`、`stats[7]{status(string), count(int64)}`。

### 5.4 Refund / RefundAudit

`api/refund/v1/refund.go:33-48`

| 字段 | 类型 |
| --- | --- |
| id, order_id, user_id | int64 |
| status | string（7 态） |
| amount | int64（分，= 订单 total_amount 快照） |
| reason / review_reason / execute_reason | string |
| reviewed_by / executed_by | *int64 |
| reviewed_at / executed_at / created_at / updated_at | time |

**RefundAudit**：`id`、`refund_id`、`action(string)`、`operator_type(int 1=user/2=admin)`、`operator_id`、`operator_username`、`before_status(int)`、`after_status(int)`、`reason`、`created_at`。

请求：`ApplyReq{reason string}`（服务层要求 trim 非空）、`ReviewReq{result,reason}`（`v:"required"`）、`ExecuteReq{result,reason}`（`v:"required"`）、`CloseReq{reason}`（`v:"required"`）、`AdminListReq{page,size,order_id,user_id,status}`。

### 5.5 Coupon

**Template**（`api/coupon/v1/coupon.go:24-38`）：`id`、`name`、`threshold_amount(int64 分)`、`discount_amount(int64 分)`、`total_quantity`、`issued_quantity`、`claim_start_time`、`claim_end_time`、`use_start_time`、`use_end_time`、`status(int 1/0)`、`created_at`、`updated_at`。

**UserCoupon**（`:41-54`）：`id`、`template_id`、`status(string available/locked/used)`、`order_id(*int64)`、`received_at`、`locked_at`、`used_at`、`name`、`threshold_amount`、`discount_amount`、`use_start_time`、`use_end_time`。

请求：`AdminCreateReq{name, threshold_amount, discount_amount, total_quantity, claim_*, use_*}`（均为 int64/时间，无 `v` 标签，服务层校验 19002）。

### 5.6 FlashSale

**Activity**：`id`、`name`、`status(string enabled/disabled)`、`start_time`、`end_time`、`skus[]ActivitySku`、`created_at`、`updated_at`。
**ActivitySku**：`id`、`sku_id`、`flash_price(int64 分)`、`total_stock`、`sold`。
**FlashOrder**：`id`、`order_no`、`user_id`、`activity_id`、`sku_id`、`product_id`、`sku_name`、`product_name`、`product_main_image`、`flash_price`、`quantity(恒 1)`、`idempotency_key`、`created_at`、`updated_at`。
**RequestAudit**：`id`、`request_id`、`operator_admin_id`、`operator_username`、`action`、`before_status(int)`、`after_status(int)`、`reason`、`created_at`。

请求：
- `CreateOrderReq{sku_id int64, idempotency_key string}`（两者服务层校验，无 `v` 标签）。
- `GetOrderResultReq{idempotency_key(query)}`。
- 后台 `CreateReq{name,start_time,end_time,skus[]ActivitySkuInput{sku_id,flash_price(json.Number),total_stock}}`；
- `UpdateReq{name?,status?,start_time?,end_time?,skus?}`（skus 仅更新已存在绑定，不可增删）。

### 5.7 Banner / Recommendation

**BannerItem**：`id`、`title`、`image_url`、`link_url`、`sort`。
**Banner**：+ `status(int 1/0)`、`created_at`、`updated_at`。
**Position**：`id`、`code`、`name`、`status(int 1/0)`、`created_at`、`updated_at`。
**Item**：`id`、`position_id`、`product_id`、`sort`、`created_at`、`updated_at`。
**ItemSnapshot**：`product_id`、`name`、`main_image`、`price(int64 分)`、`sort`。

请求：`CreateReq{code,name,status?}`、`UpdateReq{name?,status?}`、`AddItemReq{product_id,sort?}`、`UpdateSortReq{product_ids[]int64}`。

### 5.8 Article / Review

**Article**：`id`、`author_id`、`author_username`、`title`、`content`、`created_at`、`updated_at`。
**ArticleItem**：同上但**无 content**。
**ReviewItem**（公开）：`id`、`product_id`、`sku_id`、`rating(int)`、`content`、`user_id`、`created_at`。
**Review**（本人/写接口）：`id`、`user_id`、`order_item_id`、`product_id`、`sku_id`、`rating`、`content`、`status(string)`、`created_at`。

请求：文章 `{title ≤64, content ≤10000}`；评价 `CreateReq{order_item_id, rating(1-5), content(≤500)}`、`UpdateReq{rating,content}`。

### 5.9 Address / Inventory / Session / Upload / Dashboard

**Address**：`id`、`recipient_name`、`phone`、`province`、`city`、`district`、`detail`、`is_default(bool)`、`created_at`、`updated_at`。
**Inventory**：`sku_id`、`quantity`、`created_at`、`updated_at`（无记录 quantity=0、时间为 null）。
**Inventory Log**：`id`、`sku_id`、`change_type(string increase/deduct)`、`change_qty`、`before_qty`、`after_qty`、`operator_admin_id(*int64)`、`reason`、`created_at`。
**Session**：`sid`、`login_at(int64 unix 秒)`、`user_agent`、`ip`、`current(bool)`。
**TokenRes**：`token`、`key`、`upload_url`、`domain`、`final_url`、`expires_at(int64 unix 秒)`。
**Admin MeRes**：`id`、`username`、`is_super(bool)`、`roles([]string)`。
**Role**：`id`、`name`、`description`；**Permission**：`id`、`code`、`name`、`description`。

**Dashboard**
- Overview 6 字段见 §4.20。
- `TrendRes.days[]`：`date(YYYY-MM-DD)`、`order_count(int64)`。
- `StatusRes`：`total(int64)`、`status_distribution[]`：`status(int)`、`count(int64)`。
- `TopRes.items[]`：`rank(int)`、`product_id(int64)`、`product_name`、`sales(int64)`。
- `FlashSalesRes`：`activity_total`、`activity_status{enabled,disabled}`、`request_status{queued,success,failed,dead}`、`current_queued`、`success_orders`（均 int64）。

### 5.10 其余请求 DTO 字段速查（全量，供 OpenAPI 建 schema 用）

| DTO | 字段（JSON 名: 类型） | 来源 |
| --- | --- | --- |
| `iam.RegisterReq` | `username:string`、`password:string` | `api/iam/v1/iam.go:6-10` |
| `iam.LoginReq` | `username:string`、`password:string` | `:19-23` |
| `iam.RefreshReq` | `refresh_token:string` | `:34-37` |
| `iam.RevokeSessionReq` | `sid:string`（path） | `:86-89` |
| `iam.GetUserStatusReq` | `id:int64`（path） | `:112-115` |
| `iam.UpdateUserStatusReq` | `id:int64`、`status:int`、`reason:string` | `:125-130` |
| `admin.LoginReq` | `username:string`、`password:string`（均 `required`） | `api/admin/v1/admin.go:6-10` |
| `admin.CreateAdminReq` | `username:string`、`password:string` | `:41-45` |
| `admin.UpdateAdminStatusReq` | `id:int64`、`status:int` | `:54-58` |
| `admin.DeleteAdminReq` | `id:int64` | `:64-67` |
| `admin.AssignAdminRoleReq` | `id:int64`、`role_id:int64` | `:73-77` |
| `admin.RemoveAdminRoleReq` | `id:int64`、`role_id:int64`（均 path） | `:83-87` |
| `admin.CreateRoleReq` | `name:string`、`description:string` | `:100-104` |
| `admin.UpdateRoleReq` | `id:int64`、`name:*string`、`description:*string` | `:122-127` |
| `admin.AssignRolePermissionReq` | `id:int64`、`permission_id:int64` | `:144-148` |
| `admin.RemoveRolePermissionReq` | `id:int64`、`permission_id:int64`（均 path） | `:154-158` |
| `admin.CreatePermissionReq` | `code:string`、`name:string`、`description:string` | `:172-177` |
| `admin.UpdatePermissionReq` | `id:int64`、`code:*string`、`name:*string`、`description:*string` | `:195-201` |
| `address.CreateReq` | `recipient_name:string`、`phone:string`、`province:string`、`city:string`、`district:string`、`detail:string`、`is_default:*bool` | `api/address/v1/address.go:23-32` |
| `address.UpdateReq` | `id:int64` + 上述字段全部 `*string`/`*bool` | `:61-71` |
| `product.ListReq` / `AdminListReq` | `page:int`、`size:int`、`category_id:*int64`、`keyword:string`、`sort:string`、`order:string` | `api/product/v1/product.go:36-44`、`:67-75` |
| `categories.CreateReq` | `parent_id:int64`、`name:string`、`sort:int`、`status:*int` | `api/categories/v1/categories.go:42-48` |
| `categories.UpdateReq` | `id:int64`、`parent_id:*int64`、`name:*string`、`sort:*int`、`status:*int` | `:56-63` |
| `sku.CreateReq` | `product_id:int64`、`name:string`、`price:json.Number`、`status:*string` | `api/sku/v1/sku.go:28-34` |
| `sku.UpdateReq` | `id:int64`、`name:*string`、`price:*json.Number`、`status:*string` | `:42-48` |
| `cart.AddReq` | `sku_id:int64`、`quantity:*json.Number` | `api/cart/v1/cart.go:44-48` |
| `cart.UpdateQuantityReq` | `id:int64`、`quantity:json.Number` | `:56-60` |
| `cart.UpdateSelectedReq` | `id:int64`、`selected:bool` | `:68-72` |
| `order.PayReq` / `CancelReq` / `ReceiveReq` / `ShipReq` | `id:int64`（path） | `api/order/v1/order.go:113-149` |
| `order.AdminOrderStatsReq` / `AdminOrderDetailReq` | 无参数 / `id:int64` | `:189-209` |
| `refund.ApplyReq` | `id:int64`、`reason:string` | `api/refund/v1/refund.go:51-55` |
| `refund.ListReq` | `order_id:int64`（query，≤0 忽略） | `:85-88` |
| `refund.AdminListReq` | `page:*int`、`size:*int`、`order_id:*int64`、`user_id:*int64`、`status:string` | `:96-103` |
| `refund.ReviewReq` / `ExecuteReq` | `id:int64`、`result:string`、`reason:string` | `:150-168` |
| `refund.CloseReq` | `id:int64`、`reason:string` | `:176-180` |
| `coupon.ClaimReq` | `template_id:int64`（path） | `api/coupon/v1/coupon.go:96-99` |
| `coupon.AdminCreateReq` | `name:string`、`threshold_amount:int64`、`discount_amount:int64`、`total_quantity:int64`、`claim_start_time:time`、`claim_end_time:time`、`use_start_time:time`、`use_end_time:time` | `:57-67` |
| `flashsale.CreateOrderReq` | `id:int64`（path）、`sku_id:int64`、`idempotency_key:string` | `api/flashsale/v1/flash_sale.go:104-109` |
| `flashsale.GetOrderResultReq` | `id:int64`（path）、`idempotency_key:string`（query） | `:120-124` |
| `flashsale.CreateReq` | `name:string`、`start_time:time`、`end_time:time`、`skus:[]ActivitySkuInput` | `:74-80` |
| `flashsale.UpdateReq` | `id:int64`、`name:*string`、`status:*string`、`start_time:time`、`end_time:time`、`skus:[]ActivitySkuInput` | `:88-96` |
| `flashsale.ActivitySkuInput` | `sku_id:int64`、`flash_price:json.Number`、`total_stock:int64` | `:67-71` |
| `flashsale.RepairRequestReq` | `id:int64`、`target_status:string`、`reason:string` | `:134-139` |
| `banner.CreateReq` | `title:string`、`image_url:string`、`link_url:string`、`sort:int`、`status:*int` | `api/banner/v1/banner.go:62-69` |
| `banner.UpdateReq` | `id:int64`、`title/image_url/link_url:*string`、`sort/status:*int` | `:77-85` |
| `recommendation.FrontendReq` | `code:string`（path） | `api/recommendation/v1/recommendation.go:46-49` |
| `recommendation.CreateReq` | `code:string`、`name:string`、`status:*int` | `:81-86` |
| `recommendation.UpdateReq` | `id:int64`、`name:*string`、`status:*int` | `:94-99` |
| `recommendation.AddItemReq` | `id:int64`、`product_id:int64`、`sort:*int` | `:116-121` |
| `recommendation.RemoveItemReq` | `id:int64`、`product_id:int64`（均 path） | `:129-133` |
| `recommendation.UpdateSortReq` | `id:int64`、`product_ids:[]int64` | `:139-143` |
| `article.CreateReq` | `title:string`、`content:string` | `api/article/v1/article.go:57-61` |
| `article.UpdateReq` | `id:int64`、`title:string`、`content:string` | `:69-74` |
| `article.ListReq` / `MyListReq` / `MyFavoritesReq` | `page:int`、`size:int` | `:31-35`、`:91-95`、`:179-183` |
| `review.CreateReq` | `order_item_id:int64`、`rating:int`、`content:string` | `api/review/v1/review.go:59-64` |
| `review.UpdateReq` | `id:int64`、`rating:int`、`content:string` | `:87-92` |
| `review.ListReq` / `MyListReq` | `page:int`、`size:int`（List 另含 `id:int64` path） | `:40-45`、`:72-76` |
| `favorite.AddReq` | `product_id:int64` | `api/favorite/v1/favorite.go:23-26` |
| `favorite.ListReq` | `page:int`、`size:int` | `:43-47` |
| `favorite.CheckReq` | `product_id:int64`（query） | `:58-61` |
| `like.LikeReq` | `product_id:int64` | `api/like/v1/like.go:7-10` |
| `like.CheckReq` / `CountReq` | `product_id:int64`（query） | `:27-30`、`:38-41` |
| `inventory.IncreaseReq` / `DeductReq` | `sku_id:int64`（path）、`quantity:json.Number` | `api/inventory/v1/inventory.go:49-65` |
| `inventory.ListLogsReq` | `sku_id:int64`（path）、`page:int`、`size:int`（query，默认 1/20） | `:73-78` |
| `dashboard.StatusReq` | `start_time:string`、`end_time:string`（RFC3339，同传） | `api/dashboard/v1/dashboard.go:43-47` |
| `dashboard.TopReq` | `limit:string`（query，正整数、默认 10、上限 50） | `:64-67` |
| `upload.FrontendTokenReq` / `AdminTokenReq` | `filename:string`、`content_type:string` | `api/upload/v1/upload.go:11-22` |

> 说明：`req.Id`（path 参数）在 GoFrame 中同时受 `v:"required"` 约束；对 `int64` 而言缺失/为 0 会触发 §15.1 的校验错误行为。

---

## 6. 用户商城 API 清单（A 类）

共 **67** 条（= 前台 `RegisterFrontendRoutes` 全部路由）。

| 状态 | 数量 | 含义 |
| --- | --- | --- |
| 可直接使用 | 58 | 契约字段足够支撑页面 |
| 需增强 | 9 | 路由存在，但字段/分页/校验不足 |
| 完全缺失（不计入 67） | 3 项能力 | 见 §14 |

### 6.1 需增强的 9 条

| # | Path | 缺口（源码事实） |
| --- | --- | --- |
| 35 | `GET /products` | 无库存/销量字段；`sort` 白名单仅 `id/price/created_at/updated_at`；筛选仅分类+关键词 |
| 36 | `GET /products/:id` | 不返回库存；GET 触发 `view_count+1` 写操作 |
| 55 | `GET /cart` | 无选中合计金额/件数汇总，前端需自行计算 |
| 56 | `POST /cart/items` | 不校验库存，可加入超过库存的数量 |
| 60 | `POST /orders` | 无法查询"可用券"，`coupon_id` 语义与响应 `user_coupon_id` 不一致 |
| 61 | `GET /orders` | 无分页/状态筛选，返回本人全部订单且逐单加载明细 |
| 71 | `GET /refunds` | 无分页，仅支持 `order_id` 过滤 |
| 80 | `POST /coupons/:template_id/claim` | 无"可领取券列表"前置能力，用户无法发现 `template_id` |
| 81 | `GET /my/coupons` | 无分页、无状态筛选 |

### 6.2 其余 58 条（可直接使用）

`POST /register`、`POST /login`、`POST /refresh`、`GET /me`、`POST /logout`、4×sessions、
5×addresses、2×categories、3×cart（改数量/勾选/删除）、4×orders（详情/支付/取消/收货）、
3×refund（申请/详情/撤销）、2×flash-sale（下单/结果）、`GET /banners`、`GET /recommendations/:code`、
14×articles、5×review、4×favorites、4×likes、`GET /qiniu/upload/token`。

> 说明：文章 14 条属于 UGC 能力，是否进入商城 MVP 由产品决定；技术上可直接使用。

---

## 7. 管理后台 API 清单（B 类）

共 **73** 条（= `RegisterAdminRoutes` 全部路由）。

| 分组 | 数量 | 接口 |
| --- | --- | --- |
| 认证 | 3 | `POST /admin/login`、`POST /admin/logout`、`GET /admin/me` |
| 用户状态 | 2 | `GET|PUT /admin/users/:id/status` |
| 管理员 | 5 | `POST /admin/admins`、`PUT /admin/admins/:id/status`、`DELETE /admin/admins/:id`、`POST|DELETE /admin/admins/:id/roles...` |
| 角色 | 6 | `POST|GET /admin/roles`、`PUT|DELETE /admin/roles/:id`、`POST|DELETE /admin/roles/:id/permissions...` |
| 权限 | 4 | `POST|GET /admin/permissions`、`PUT|DELETE /admin/permissions/:id` |
| 分类 | 3 | `POST /categories`、`PUT|DELETE /categories/:id`（**非 /admin 前缀**） |
| 商品 | 6 | `GET /admin/products`、`GET /admin/products/:id`、`POST|PUT`、`POST .../on-shelf|off-shelf` |
| SKU | 3 | `POST /admin/skus`、`PUT|DELETE /admin/skus/:id` |
| 库存 | 4 | `GET /admin/inventories/:sku_id`、`.../logs`、`.../increase`、`.../deduct` |
| 订单 | 4 | `GET /admin/orders`、`GET /admin/orders/stats`、`GET /admin/orders/:id`、`POST /admin/orders/:id/ship` |
| 退款 | 6 | 列表/详情/审计/审核/执行/关闭 |
| 秒杀 | 4 | 创建/更新/修复/审计 |
| 评价 | 1 | `POST /admin/reviews/:id/take-down` |
| 轮播图 | 5 | 列表/详情/创建/更新/删除 |
| 推荐位 | 8 | 列表/详情/创建/更新/删除/加商品/移商品/排序 |
| 大屏 | 5 | overview/trend/status/top/flash-sales |
| 优惠券 | 3 | 创建/列表/停止 |
| 上传 | 1 | `GET /admin/qiniu/upload/token` |

**需要增强的 7 条**

| # | Path | 缺口 |
| --- | --- | --- |
| 66 | `POST /admin/orders/:id/ship` | 无运单号/承运商，无法对接真实物流 |
| 37 | `GET /admin/products` | 无 `status` 筛选，无法按草稿/上架/下架过滤 |
| 40 | `PUT /admin/products/:id` | 无删除/批量操作；`status` 只能通过 on/off-shelf 变更 |
| 67 | `GET /admin/orders` | 无订单导出；无 `order_no` 模糊匹配（仅精确） |
| 74 | `GET /admin/refunds` | 无时间范围筛选、无导出 |
| 136 | `GET /admin/dashboard/overview` | 指标口径固定，无时间范围参数 |
| 83 | `GET /admin/coupons` | 无分页/状态筛选 |

**后台能力缺失（不计入 73）**：管理员列表、分类全量列表（含禁用）、评价列表、用户列表、秒杀活动列表/详情、优惠券更新/删除、商品删除、操作审计日志（除退款/秒杀修复）。

---

## 8. 内部 / 基础设施 API 清单（C 类）

| Method | Path | 用途 | 备注 |
| --- | --- | --- | --- |
| GET | `/health` | 存活探针 | 不访问任何依赖，恒 200 |
| GET | `/ready` | 就绪探针 | MySQL + Redis，失败 1005/503 |
| GET | `/metrics` | Prometheus 采集 | 条件注册；Nginx 显式屏蔽 `/metrics`（`nginx/nginx.conf:76-78`） |
| GET | `/storage/banners/*` | 静态占位图 | 非 API |

另有两类"内部语义、后台入口"的接口：`POST /admin/flash-sales/requests/:id/repair` 与 `GET .../audits`（运维修复/审计），归类为 B 类但主要由 SRE/运营使用。

后台无 HTTP 入口的内部机制：秒杀 `db_scanner` / Kafka Consumer、Outbox Publisher、订单超时取消扫描器（`internal/worker`）。

---

## 9. 认证与权限体系

### 9.1 Token

| 项 | 事实 | 证据 |
| --- | --- | --- |
| 算法 | HS256 | `internal/auth/jwt.go` |
| Issuer | `surgecart` | `internal/auth/jwt.go:17-18` |
| Claims | `sub`(id 十进制字符串)、`sid`、`type`、`iss`、`exp` | `internal/auth/jwt.go:31-75` |
| type | `user` / `admin` | `internal/auth/jwt.go:24-27` |
| 有效期 | `ExpiresIn = 3600` 常量（响应 `expires_in` 恒 3600） | `internal/auth/jwt.go:20` |
| 传递方式 | `Authorization: Bearer <token>`，**不支持 Cookie** | `internal/middleware/auth.go:284-293` |
| 用户会话 | Redis `iam:session:{sid}` + users.status + users.auth_epoch | `internal/middleware/auth.go:26-63` |
| 管理员会话 | Redis `iam:admin:session:{sid}` + admins.status | `internal/middleware/auth.go:83-119` |
| Refresh | 30 天（`auth.refresh.ttl`），轮换 + 重放全量撤销 | `internal/logic/iam/refresh.go` |

### 9.2 认证中间件语义

| 中间件 | 行为 | 失败码 |
| --- | --- | --- |
| `Auth` | 验签+exp → type=user → sid 非空 → Redis 会话有效 → users.status=1 且 auth_epoch 匹配 | 401/1002；type≠user → 403/1003；Redis/DB 异常 fail-closed 401 |
| `AuthSignatureOnly` | 仅验签+exp+type，不查会话 | 401/1002 |
| `AdminAuth` | 验签+exp → type=admin → Redis 会话 → admins.status=1，并注入 `is_super` | 同上 |
| `AdminAuthSignatureOnly` | 仅验签+exp+type=admin | 401/1002 |
| `RequirePermission(code)` | 超管直接放行；否则查 `permissions→role_permissions→admin_roles` | 未命中 403/1003；查询失败 500/1000 |

### 9.3 无权限码的 10 个后台接口

`GET /admin/me`、`GET /admin/qiniu/upload/token`、`GET /admin/products`、`GET /admin/products/:id`、`GET /admin/inventories/:sku_id`、`GET /admin/inventories/:sku_id/logs`、`GET /admin/banners`、`GET /admin/banners/:id`、`GET /admin/recommend-positions`、`GET /admin/recommend-positions/:id`。

风险：任何启用状态的管理员（含最小角色）都可读取全部商品/库存/内容数据；RBAC 只有写侧粒度。

### 9.4 权限码清单（49 个，全部有 seed）

`category:create|update|delete`、`product:create|update|on_shelf|off_shelf`、`sku:create|update|delete`、
`inventory:increase|deduct`、`admin:create|disable|delete|assign_role`、`role:create|list|update|delete|assign_permission`、
`permission:create|list|update|delete`、`order:ship|list|view`、`refund:list|view|review|execute`、
`flash_sale:create|update|repair`、`review:take_down`、`banner:create|update|delete`、
`recommend:create|update|delete|item`、`dashboard:view`、`user:read|status`、`coupon:create|list|stop`。

Seed 位置：`internal/boot/seed.go:28-78`（49 项，与路由使用集合完全一致）。

**权限码不对称**：`product:delete`、`sku:list`、`coupon:update|delete`、`admin:list`、`category:list` 不存在（因为对应接口不存在）。

---

## 10. 响应结构及错误码规范

### 10.1 统一成功响应

所有经 `Response` 中间件的业务接口统一为：

```json
{ "code": 0, "message": "OK", "data": <DTO 或 null> }
```

证据：`internal/middleware/response.go:12-45`；`ResponseBody{code,message,data}`。

### 10.2 统一失败响应

```json
{ "code": 1004, "message": "未找到", "data": null }
```

HTTP 状态由 `codes.HTTPStatus(code)` 派生（`internal/codes/codes.go:269-275`），message 由码表固定（不拼接细节）。

### 10.3 非统一响应（契约缺口，已实测）

| 场景 | 实际响应 | 证据 |
| --- | --- | --- |
| 未匹配路由 | `404 text/plain`，body=`Not Found`，**不是** `{code,message,data}` | 本次用 GoFrame v2.10.3 最小服务实测 |
| `/metrics` | Prometheus 文本 | `internal/cmd/cmd.go:228-230` |
| Nginx 413/502/504 | Nginx 静态 HTML 错误页 | `nginx/nginx.conf:105-115` |
| 框架参数校验失败 | `{"code":51,"message":"内部错误"}` + **HTTP 500** | 见 §15.1 |

### 10.4 错误码体系

- 93 个码常量（`CodeOK=0` + 92 个业务错误码），按 1000 一个域段分配：通用 1000-1999、IAM 2000-2999、分类 3000-3999、商品 4000-4999、SKU 5000-5999、库存 6000-6999、地址 7000-7999、购物车 8000-8999、订单 9000-9999、评价 10000-、收藏 11000-、秒杀 12000-、点赞 13000-、轮播图 14000-、推荐位 15000-、文章 16000-、上传 17000-、退款 18000-、优惠券 19000-。
- 域段并非按 1000 递增顺序声明（12000 段在 11000/13000 之间插入），但码值唯一。
- 全部 92 个业务码在非测试代码中均有引用（无死码）。
- 码表缺失任何映射时默认 `500` + `内部错误`（`codes.go:270-283`）——这是 §15.1 的根因。

---

## 11. 分页、金额、时间和 ID 规范

### 11.1 分页（三种并存）

| 形态 | 接口 | 行为 |
| --- | --- | --- |
| A：`items+total+page+size`，越界**静默归一** | 商品列表、商品后台列表、文章列表/我的文章/收藏、评价公开列表/我的评价、收藏列表、库存流水 | `page<1→1`，`size<1→20`，`size>100→100`（`internal/logic/product/product.go:571-583` 等多处复制实现） |
| B：`items+total+page+size`，越界**返回 1001** | 后台订单列表、后台退款列表 | `internal/logic/order/order.go:1110-1126`、`internal/logic/refund/refund.go:834-850` |
| C：仅 `items`，**无分页无上限** | 购物车、地址、会话、用户订单、用户退款、我的券、轮播图、分类树、角色、权限、退款审计、秒杀审计、后台券列表、后台轮播图、后台推荐位 | 例如 `internal/logic/order/order.go:406-419` |

缺失统一能力：`total_pages`、`has_next`、游标分页、统一默认值/上限常量（`defaultSize` 在多包重复定义）。

### 11.2 金额

- 所有金额字段为 `int64` **整数分**：`price`、`total_amount`、`original_amount`、`discount_amount`、`flash_price`、`threshold_amount`、`discount_amount`、`today_order_amount`。
- 请求侧存在 `json.Number`：`product.price`、`sku.price`、`cart.quantity`、`inventory.quantity`、`flash_price`。其 JSON 类型既可为数字也可为字符串（GoFrame 绑定行为），OpenAPI 需明确。
- 无浮点金额、无元单位字段。
- 语义差异：`dashboard.today_order_amount` 是"今日已支付口径订单金额"，不是实收/退款后金额（`api/dashboard/v1/dashboard.go:22`）。

### 11.3 时间

| 场景 | 格式 | 证据 |
| --- | --- | --- |
| 响应中的 `*gtime.Time` | `"YYYY-MM-DD HH:MM:SS"`（无时区偏移，进程本地时区） | `gtime.Time.MarshalJSON → String()`；`gtime_time_wrapper.go:28` |
| 后台订单筛选 | RFC3339 **必须含偏移**（`time.Parse(time.RFC3339)`） | `internal/logic/order/order.go:1152-1181` |
| 大屏 `start_time/end_time` | 同上，RFC3339 | `internal/logic/dashboard/dashboard.go` |
| 秒杀/优惠券请求时间 | `*gtime.Time`（多格式宽松解析） | `api/flashsale/v1/flash_sale.go:77-78`、`api/coupon/v1/coupon.go:63-66` |
| 大屏日期 | `YYYY-MM-DD`（字符串） | `api/dashboard/v1/dashboard.go:32` |
| 会话 `login_at` | unix 秒（int64） | `api/iam/v1/iam.go:69` |
| DB 会话时区 | 按 `dashboard.timezone`（默认 Asia/Shanghai）设 `time_zone` DSN | `internal/boot/boot.go:176-202` |

结论：**输出无偏移、输入要求偏移、部分输入宽松解析**，三种约定并存。

### 11.4 ID

- 业务主键统一 `int64` 自增；JSON 为数字。
- 唯一例外：会话 `sid` 为字符串（`DELETE /sessions/:sid`、`Session.sid`）。
- 无 UUID/ULID；订单另有 `order_no` 字符串业务号。
- 理论风险：`int64 > 2^53` 时 JS 前端会丢精度；当前自增规模不构成实际风险，但 OpenAPI 应声明 `format:int64`。

---

## 12. API 实现完整度

| 结论 | 数量 | 说明 |
| --- | --- | --- |
| 完整（handler→service→logic→SQL/状态机均存在，无 stub/TODO） | 138 | 142 - 4 |
| 部分实现（能力未闭环） | 4 | 见下表 |
| 未确认 | 0 | 未发现无法追踪调用链的接口 |

**部分实现明细**

| # | 接口 | 缺口 |
| --- | --- | --- |
| 61 | `GET /orders` | 无分页/筛选；逐单加载 items（N+1）；无上限 |
| 71 | `GET /refunds` | 无分页 |
| 81 | `GET /my/coupons` | 无分页、无状态筛选 |
| 66 | `POST /admin/orders/:id/ship` | 无运单号/承运商字段，物流信息无处存放 |

**全量核验依据**：`api/*/v1` 21 个文件共 **142 个 Req struct / 141 个 Res struct**（上传两接口共用 `TokenRes`）；142 个路由均能定位到 `internal/controller/*` 的具体方法；控制器全部调用 `service.X()`；`internal/service/*` 21 个接口均有 `internal/logic/*` 的 `init()` 注册；`internal/logic` 中无 `TODO/FIXME/未实现` 标记（除 service 注册缺失时的 panic 防御）。

---

## 13. API 测试覆盖情况

### 13.1 本次执行的命令与结果

| 命令 | 退出码 | 结果 |
| --- | --- | --- |
| `go build ./...` | 0 | 通过 |
| `go vet ./...` | 0 | 通过 |
| `make test-unit`（仓库官方纯内存门禁） | 0 | 15 个包 `ok`，0 失败 |
| `go test ./... -count=1 -timeout 600s` | 1 | 16 包 `ok`、15 包 `FAIL`、343 个用例失败；24 个 `SKIP` 仅出现在"通过包 -v 复跑"中 |

`make test-unit` 覆盖包：`internal/auth`、`internal/config`、`internal/metrics`、`internal/logic/health`、`internal/logic/dashboard`、`internal/controller/health`、`internal/mq/kafka`、`internal/termination`、`internal/worker`、`internal/logic/iam`(纯内存子集)、`internal/logic/flashsale`(纯内存子集)、`internal/logic/legacymigrate`(纯内存子集)、`internal/migrations`(纯内存子集)、`internal/middleware`(RequestID)、`internal/cmd`（含 `TestRouteTable` 路由表锁定）。

单独对 16 个通过包执行 `-v`：**362 PASS / 24 SKIP / 0 FAIL**。

### 13.2 全量测试失败根因（全部环境性）

| 根因 | 次数/影响 |
| --- | --- |
| `未配置 database.default.pass（DATABASE_DEFAULT_PASS）` | 绝大多数失败（migration Preflight 直接拒绝） |
| `dial tcp 127.0.0.1:3306: connect: connection refused` | `internal/migrations` 等 |
| Redis/Kafka 未监听 | 相关集成用例无法执行 |

未伪报通过：**所有依赖 MySQL/Redis 的控制器测试与 E2E 测试在本环境均未通过，不能视为"已验收"**。

### 13.3 测试组织

| 层级 | 位置 | 依赖 | 本次是否可执行 |
| --- | --- | --- | --- |
| 纯内存单测 | 各包 `_test.go` | 无 | ✅（`make test-unit`） |
| 控制器集成测试 | `internal/controller/*_test.go` | MySQL + Redis | ❌ |
| 逻辑层集成测试 | `internal/logic/*_test.go` | MySQL + Redis（部分纯内存） | 部分 ✅ |
| HTTP E2E | `test/e2e/*_test.go` | 真实路由 + MySQL + Redis | ❌ |
| 构建标签测试 | `serve_lifecycle` / `storage_e2e` / `kafka_e2e` | 进程信号 / 真实七牛 / 真实 Kafka | ❌（默认 `go test ./...` 不编译） |
| 部署清单 | `test/manifest`、`test/deployk8s` | 无（静态/仿真） | ✅ |

### 13.4 域级覆盖矩阵

| 业务域 | 控制器测试 | 逻辑测试 | E2E | 备注 |
| --- | --- | --- | --- | --- |
| IAM 前台 | ✅ `iam_test.go`、`refresh_test.go` | ✅ | ✅ `identity_isolation_test.go` | 覆盖注册/登录/刷新/会话/隔离 |
| 地址 | ✅ `address_test.go` | — | — | 仅控制器级 |
| 管理员/RBAC | ✅ `admin_test.go` | — | ✅ `identity_isolation` | 权限矩阵有覆盖 |
| 商品 | ✅ `product_test.go` | — | — | 仅控制器级 |
| 分类 | ✅ `categories_test.go` | ✅ `logic/categories` | — | — |
| SKU | ✅ `sku_test.go` | — | — | — |
| 库存 | ✅ `inventory_test.go` | — | — | — |
| 购物车 | ✅ `cart_test.go` | — | — | — |
| 订单 | — | — | ✅ `order_test.go`、`order_admin_test.go` | 用户+后台 |
| 退款 | — | — | ✅ `refund_test.go` | 全状态机 |
| 优惠券 | — | — | ✅ `coupon_test.go` | — |
| 秒杀 | — | ✅ 8 个测试文件 | ✅ v1/v2/v3/v5 | 覆盖异步/修复/审计 |
| 轮播图 | — | — | ✅ `banner_test.go` | — |
| 推荐位 | — | — | ✅ `recommendation_test.go` | — |
| 文章 | — | — | ✅ `article_test.go` | — |
| 评价 | — | — | ✅ `review_test.go` | — |
| 收藏 | — | — | ✅ `favorite_test.go` | — |
| 点赞 | — | — | ✅ `like_test.go` | — |
| 上传 | — | ✅ `upload_test.go` | ✅ `upload_test.go` | 七牛配置校验 |
| 大屏 | — | ✅ `dashboard_test.go` | ✅ `dashboard_test.go` | 时区/口径 |
| 健康 | ✅ `health_test.go` | ✅ `health_test.go` | ✅ `serve_lifecycle`(tag) | — |

### 13.5 测试盲区

1. `internal/cmd/routes_test.go` 的期望表**未包含**购物车、文章、优惠券、大屏、用户状态等路由（`routes_test.go:46-185`），路由表锁定不完整——新增/删除这些路由不会被该测试发现。
2. 无 OpenAPI / JSON Schema 契约测试；字段改名不会被任何测试捕获（E2E 只断言关键字段）。
3. 无逐字段响应快照测试；`image_url`/`main_image` 等命名漂移无门禁。
4. 无 401/403 全接口矩阵测试（仅部分接口有鉴权断言）。
5. 无 404/405/非 JSON 响应的一致性测试。

---

## 14. 缺失 API 清单

> 判定标准：在 `routes_frontend.go` / `routes_admin.go` 中无对应注册，且 service 层无对等能力。

### 14.1 商城必需（阻断）

| # | 缺失能力 | 影响 | 现状证据 |
| --- | --- | --- | --- |
| M1 | 秒杀活动列表 / 详情（前台） | 用户无法发现活动，只能靠外部渠道拿到 `activity_id` 与 `sku_id` | `api/flashsale/v1` 仅有下单/结果；service 无 List/Detail |
| M2 | 可领取优惠券列表（前台） | 用户无法浏览可领券，`POST /coupons/:template_id/claim` 无法被发现 | `ICoupon` 仅 AdminList/ListMine |
| M3 | 订单可用券查询 | 下单页无法计算/选择券，`coupon_id` 无法正确填写 | 无 `GET /coupons/available` 类路由 |

### 14.2 商城体验（重要）

| # | 缺失能力 | 说明 |
| --- | --- | --- |
| M4 | 商品库存查询（前台） | Product/Sku DTO 无库存；只有购物车返回 stock |
| M5 | 用户订单分页/状态筛选 | `GET /orders` 全量返回 |
| M6 | 购物车清空 / 全选 / 合计 | 仅单条操作 |
| M7 | SKU 独立查询 / SKU 规格属性 | SKU 仅 name+price |
| M8 | 商品搜索建议 / 热搜 | 仅 `keyword` LIKE name/brand |
| M9 | 评价图片 / 追评 | Review 仅 rating+content |
| M10 | 售后退款进度通知 | 无消息/通知接口 |

### 14.3 管理后台

| # | 缺失能力 | 影响 |
| --- | --- | --- |
| B1 | 管理员列表 `GET /admin/admins` | 无法在 UI 管理管理员 |
| B2 | 分类全量列表（含禁用） | 后台只能用前台 `GET /categories`（仅启用） |
| B3 | 秒杀活动列表/详情 | 创建后无法在后台查看/核对 |
| B4 | 评价列表/审核队列 | 只能按 id 下架 |
| B5 | 用户列表/详情 | 只能按 id 查状态 |
| B6 | 商品删除 / 批量上下架 | 仅有单条上下架 |
| B7 | 优惠券模板更新 / 删除 | 仅创建/列表/停止 |
| B8 | 发货物流信息（运单号/承运商） | 无法对接物流 |
| B9 | 订单导出 | 无 |
| B10 | 管理员操作审计日志 | 仅退款/秒杀修复有审计 |
| B11 | 文章审核/管理 | 文章无状态、无后台入口 |

### 14.4 平台能力

支付网关与支付单、真实退款网关、消息通知、文件删除/列表、搜索服务、库存预占查询、批量库存、CORS 配置、API 版本前缀（当前无 `/api/v1`，见 `routes_test.go:192-238`）。

---

## 15. 不一致和潜在兼容性问题

> 本节只记录事实，**不修改任何实现**。按对 OpenAPI 契约的影响排序。

### 15.1 【高危】框架参数校验错误返回 HTTP 500 + code 51

- 链路：DTO 的 `v:"required"` → GoFrame `gvalid` 返回 `gcode.CodeValidationFailed(51)` → `codes.FromError` 得 51 → `codes.HTTPStatus(51)` 查表未命中 → **500**，message `内部错误`。
- 证据：`internal/codes/codes.go:269-283`、`internal/middleware/response.go:31-37`；GoFrame v2.10.3 `gvalid` 实测 `code=51`（本次用最小程序验证）。
- 受影响（请求体含 `v:"required"`）：`POST /admin/login`、`POST /admin/admins`、`PUT /admin/admins/:id/status`、`POST /admin/admins/:id/roles`、`POST /admin/roles`、`POST /admin/permissions`、`PUT /admin/users/:id/status`、`POST /categories`、`POST /orders`、`POST /admin/skus`、`POST /admin/refunds/:id/review|execute|close`。
- 反向证据：代码中已显式规避——`internal/logic/cart/cart.go:81-85` 注释「避免 gvalid 的非业务 gcode（51）被透出为 500」。
- 契约影响：同一"缺必填字段"在不同接口分别得到 `400/1001` 与 `500/51`，无法用统一 error schema 描述。

### 15.2 【高危】同一状态概念存在 string / int 两套表示

| 资源 | 字符串表示 | 整数表示 |
| --- | --- | --- |
| 订单 | `GET /orders`、`GET /admin/orders` 的 `status` | `GET /admin/dashboard/orders/status` 的 `status` |
| 退款 | `Refund.status` | `RefundAudit.before_status/after_status` |
| 秒杀请求 | `GET /flash-sales/:id/orders/result` 的 `status` | `RequestAudit.before_status/after_status` |

### 15.3 【高危】枚举类型混用

| 资源 | string 枚举 | int 枚举 |
| --- | --- | --- |
| 订单 | `Order.status` | `dashboard.status_distribution.status` |
| 退款 | `Refund.status` | `RefundAudit.before/after_status` |
| 优惠券 | `UserCoupon.status` | `Template.status` |
| 商品 | `Product.status` | — |
| SKU | `Sku.status` | — |
| 秒杀 | `Activity.status`、`RequestStatus` | `RequestAudit.before/after_status` |
| 轮播图 / 推荐位 | — | `status` 0/1（int） |
| 用户 / 管理员 | — | `status` 0/1（int，用户状态响应与管理员状态请求） |
| 评价 | `Review.status` | — |

### 15.4 【高】分页策略冲突

- 同样是 `page/size`：商品/文章/评价/收藏**静默钳制**；订单/退款后台**返回 400**。同一个错误输入在两组接口得到不同结果。
- `page/size` 存在 15+ 个无分页的列表接口（§11.1-C），返回体无 `total`，前端无法预判数据量。
- 无统一 `maxSize` 常量：各域重复定义（`product.maxSize`、`article.maxSize`、`review.maxSize`…）。

### 15.5 【高】时间格式冲突

- 响应 `"2026-11-10 11:27:26"`（无偏移）；筛选参数要求 `"2026-11-10T11:27:26+08:00"`；秒杀/优惠券请求用 `gtime` 宽松解析；大屏日期 `"2026-11-10"`。
- 前端需同时实现两套解析/序列化逻辑。

### 15.6 【高】跨域错误码复用

| 场景 | 返回码 | 归属域 |
| --- | --- | --- |
| 收藏/点赞不存在的商品 | `4001` | 商品域 |
| 推荐位添加不存在的商品 | `4001` | 商品域 |
| 订单下单 SKU 不存在 | `5001` | SKU 域 |
| 订单地址不存在 | `7001` | 地址域 |
| 文章点赞/收藏目标不存在 | `16001` | 文章域 |
| 秒杀结果不存在 | `1004` | 通用域（其他 404 用域码） |

同一 code 在不同接口代表不同资源，客户端无法只靠 code 判定资源类型。

### 15.7 【中】字段命名/语义不一致

| 概念 | 字段差异 |
| --- | --- |
| 商品主图 | `Product.main_image` / `OrderItem.product_main_image` / `CartItem.product_main_image` / `ItemSnapshot.main_image` / `Banner.image_url` |
| 优惠券 id | 请求 `coupon_id` = 用户券 id；响应 `user_coupon_id` |
| 路径参数 | 多数 `:id`；券领取 `:template_id`；会话 `:sid`；库存 `:sku_id`；收藏/点赞 `:product_id` |
| 列表容器 | `items` / `days` / `stats` / `status_distribution` |
| 分页 total | `int`（商品/文章）与 `int64`（订单统计） |
| limit | `GET /admin/dashboard/products/top` 用 **string** 传整数 |

### 15.8 【中】HTTP 语义问题

- `GET /products/:id` 写库（`view_count+1`），破坏幂等/缓存语义。
- 未匹配路由返回 `404 text/plain`，非统一 JSON。
- 无 CORS 中间件（`internal/middleware` 无 CORS，Nginx 也未加头）：React/Vue 本地开发跨域直连会被浏览器拦截，必须走代理。
- 无 HTTP 层限流（秒杀限流仅在业务层，默认关闭）；Nginx `client_max_body_size 1m`。

### 15.9 【中】认证/权限不对称

- 10 个后台只读接口无权限码（§9.3）。
- 分类写接口挂在 `/categories`（非 `/admin`），按路径前缀做前端路由/网关分流会误判。
- `expires_in` 恒 3600，但会话 TTL 可配置（`auth.session.ttl`）；若改配置，响应字段会失真。
- 无 refresh token 撤销/列表接口（只能 logout/revoke-all 间接触发）。

### 15.10 【低】响应细节

- 无 `request_id` 响应体字段（仅在响应头 `X-Request-ID`）。
- 成功空操作返回 `data: null`（`Logout/Revoke*` 返回 nil），而删除类返回 `data: {}`（空 struct）——同一"无内容"语义两种表现。
- `GET /admin/orders/stats` 的 `total` 与 `stats` 之和口径一致，但 `dashboard.status_distribution` 覆盖 7 态且不含 `total` 校验说明。
- `Order.user_id` 出现在用户自己的订单响应中（冗余）。
- `Refund.user_id` 在用户侧响应中回显（冗余但无害）。

### 15.11 【低】订单状态 `received(40)` 实际不可观测

`Receive` 在同一事务内 `shipped(30) → received(40) → completed(50)`（`internal/logic/order/order.go:552-590`），`received` 只是中间态，API 对外暴露 7 态但实际只有 6 态可被查询到。

---

## 16. OpenAPI 3.1 建设建议

### 16.1 现有基础（有利条件）

1. **DTO 完整**：142 个 Req / 141 个 Res 均为具名 struct，字段有 `json` 与 `dc` 标签，可直接映射 `description`。
2. **路由元数据齐全**：每个 DTO 的 `g.Meta` 带 `path/method/tags/summary`（`api/*/v1/*.go` 全文）。
3. **错误码集中**：`internal/codes/codes.go` 可作为 `x-error-codes` 的唯一事实源。
4. **GoFrame 内置 OpenAPI 可用**：本次用等价注册方式（显式 path + 结构体 handler）在 GoFrame v2.10.3 最小服务中实测，`SetOpenApiPath` 能正确产出 paths、query 参数与 `$ref` schema。
5. **路由表有测试锁定**：`internal/cmd/routes_test.go` 可作为契约回归基线（但需补全，见 §13.5）。

### 16.2 现有基础（不利条件）

1. GoFrame 输出为 **OpenAPI 3.0.0**（实测 `"openapi":"3.0.0"`），不是 3.1；3.1 的 `type: [x,"null"]`、`jsonSchemaDialect`、`const`、数值 `exclusiveMinimum` 需转换。
2. Schema 名来自 Go 包路径（实测 `main.HelloReq`），**不稳定**，需重命名为业务名（`Order`、`OrderItem`）。
3. 生成结果**只有 200 响应**，无错误响应、无 `securitySchemes`、无 `x-permission`、无分页参数规范。
4. 统一响应包装层（`{code,message,data}`）不会自动出现在生成结果中，必须手工套 `allOf`/wrapper schema。
5. 错误码与 HTTP 状态的映射存在 §15.1 缺陷，直接生成会产出错误契约。

### 16.3 建议路径（B1-02 执行）

| 步骤 | 内容 | 产出 |
| --- | --- | --- |
| 1 | **先冻结 5 项决策**：①校验错误映射（51→400/1001 或新增码）②枚举统一为 string ③分页统一 `items/total/page/size` + 严格校验 ④时间统一 RFC3339（响应带偏移）⑤幂等键位置统一 | `docs/phase2/B1-02_CONTRACT_DECISIONS.md` |
| 2 | 以 DTO 为源生成 schema（Go AST 或 GoFrame 生成后转换），schema 名 = 业务名，金额标注 `type: integer, format: int64, x-unit: cent` | `openapi/components/*.yaml` |
| 3 | 统一包装：`Success<T>{code:0,message:"OK",data:T}`、`Error{code:int,message:string,data:null}`；声明 400/401/403/404/409/429/500/503 | `components/responses.yaml` |
| 4 | 安全方案：`bearerAuth`（JWT HS256）；每个 operation 加 `x-auth: public|user|admin`、`x-permission: <code>`；超管绕过权限的行为写入 `description` | 全局 `security` + 扩展字段 |
| 5 | 分页参数组件：`PageParam`、`SizeParam`（默认 20、上限 100、越界行为随决策①） | `components/parameters.yaml` |
| 6 | 错误码注册表：从 `codes.go` 生成 `x-error-codes`（code → http/message/domain），并标注跨域复用（§15.6） | `components/error-codes.yaml` |
| 7 | 逐域评审：先认证→商品→购物车→订单→退款→优惠券→秒杀（闭环主链路），内容类后置 | 各域 `paths/*.yaml` |
| 8 | 契约测试：路由表 ↔ OpenAPI path 双向比对；DTO 字段 ↔ schema 属性双向比对；禁止未登记差异 | CI 脚本（B1-02 新建） |

### 16.4 明确不建议

- 不建议直接把 GoFrame `openapi.json` 当交付物：3.0.0、schema 名不稳定、缺错误/安全/包装。
- 不建议在 B1-01 阶段修改任何 `v:` 标签或中间件来"迁就"生成器（本次已禁止改代码）。
- 不建议为 142 个接口手工编写 YAML；应以 DTO 为唯一事实源生成。

### 16.5 契约成熟度结论

| 维度 | 结论 |
| --- | --- |
| API 事实是否可冻结 | **可以**：142 条路由、DTO、错误码、权限码均已逐条核实 |
| 是否可直接生成可交付的 OpenAPI 3.1 | **不可以**：需先完成 §16.3 步骤 1 的 5 项决策 |
| 阻塞项 | 校验错误 500/51、枚举混用、分页冲突、时间格式、跨域错误码复用 |
| 非阻塞但需登记 | 无 CORS、404 非 JSON、GET 写副作用、缺失 API 清单 |

---

## 17. 下一步 B1-02 实施建议

1. **先决策后生成**：把 §16.3-1 的 5 项决策写成 ADR 并由 Owner 签字；未决策前不产出正式 OpenAPI。
2. **建立"契约事实源"**：`api/*/v1/*.go` 继续作为唯一源；B1-02 只允许"读 DTO + 生成"，不允许在 YAML 里手改字段。
3. **优先覆盖交易主链路**：认证 → 商品/分类/SKU → 购物车 → 订单 → 退款 → 优惠券 → 秒杀；内容域（轮播/推荐/文章/评价/收藏/点赞）第二批。
4. **补齐缺失 API 的契约占位**：对 §14.1 的 M1–M3 先在 OpenAPI 中以 `x-status: planned` 标记，避免前端按现有接口硬编码。
5. **契约回归门禁**：新增 `scripts/contract-check.sh`（B1-02 创建），比对三件事：路由注册表、OpenAPI paths、DTO 字段；任一漂移即失败。
6. **补全路由表测试**：`internal/cmd/routes_test.go` 当前遗漏购物车/文章/优惠券/大屏/用户状态路由，B1-02 应先补齐期望集合（属于测试增强，不属于业务代码）。
7. **测试环境**：B1-02 若需运行 E2E，必须先 `make deps-start` 并显式注入 `DATABASE_DEFAULT_PASS`（本环境无依赖，禁止伪造通过）。
8. **不触碰范围**：错误码映射缺陷（§15.1）属于代码修改，需单独立项；B1-02 只在契约层标注 `x-known-issue`。
9. **交付物建议**：`docs/phase2/B1-02_CONTRACT_DECISIONS.md` + `openapi/surgecart.openapi.yaml`（3.1）+ `scripts/contract-check.sh` + 差异报告。
10. **冻结基线**：以本次 HEAD `82243c0` 为契约基线；任何后续分支改动必须重新跑 §13.1 的构建/vet/单测。

---

## 附录 A：证据索引（关键文件与位置）

| 主题 | 文件:行 |
| --- | --- |
| 前台路由 | `internal/cmd/routes_frontend.go:52-70`（公开）、`73-151`（Auth）、`154-157`（仅验签） |
| 后台路由 | `internal/cmd/routes_admin.go:52`（登录）、`55-58`（登出）、`61-66`（AdminAuth）、`69-73`（require 帮助函数）、`76-187`（业务） |
| 路由挂载与中间件 | `internal/cmd/cmd.go:172-181` |
| 条件 /metrics | `internal/cmd/cmd.go:224-231` |
| 统一响应 | `internal/middleware/response.go:12-45` |
| 认证 | `internal/middleware/auth.go:26-217` |
| 权限校验 | `internal/middleware/auth.go:139-163`、`267-281` |
| 错误码与码表 | `internal/codes/codes.go:15-151`、`159-267`、`269-306` |
| 权限 seed（49 项） | `internal/boot/seed.go:28-78` |
| JWT | `internal/auth/jwt.go:17-99` |
| 请求 ID | `internal/middleware/requestid.go:29-53` |
| 订单逻辑 | `internal/logic/order/order.go:132-217`（创建）、`406-419`（用户列表）、`997-1107`（后台）、`1110-1171`（严格参数） |
| 退款逻辑 | `internal/logic/refund/refund.go:1-40`（状态机说明）、`834-850`（严格分页） |
| 秒杀下单 | `internal/logic/flashsale/flashsale.go:337-410` |
| 商品详情副作用 | `internal/logic/product/product.go:78-103` |
| 分类可见性 | `internal/logic/categories/categories.go:44-62` |
| 购物车加购 | `internal/logic/cart/cart.go:80-144` |
| 优惠券接口能力 | `internal/service/coupon.go`（无前台模板列表） |
| 秒杀接口能力 | `internal/service/flashsale.go`（无活动列表/详情） |
| gtime JSON 格式 | GoFrame `os/gtime/gtime_time.go:523`、`gtime_time_wrapper.go:28` |
| DB 时区对齐 | `internal/boot/boot.go:176-202` |
| Nginx 边界 | `nginx/nginx.conf:52-103` |
| 路由表测试（不完整） | `internal/cmd/routes_test.go:46-185` |

## 附录 B：本次未执行的检查（如实声明）

| 检查 | 原因 |
| --- | --- |
| `make test-integration` / `go test ./test/e2e` 实际通过 | 无 MySQL/Redis；Preflight 直接失败 |
| `make test-kafka-e2e` | 无 Kafka broker |
| `make test-serve-lifecycle` | 需真实进程信号环境，且依赖构建标签 |
| `make test-storage` | 需真实七牛凭据（仓库无凭据） |
| 真实数据库 Schema 校验 | 未连接数据库；字段来源为 DTO 与 SQL 迁移文件，而非线上表结构 |
| `git diff` 代码变更核对 | 本次无代码变更，工作区保持干净 |
