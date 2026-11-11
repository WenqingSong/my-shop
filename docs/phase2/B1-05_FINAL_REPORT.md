# B1-05 后端最终验收报告（B1-05-04：OpenAPI 契约核对与最终交付）

| 项目 | 内容 |
|---|---|
| 任务编号 | B1-05-04 |
| 任务名称 | OpenAPI 契约核对与最终交付报告 |
| 所属阶段 | B1-05 后端最终验收 |
| 指定模型 | DeepSeek-V4.1（标准档） |
| 任务分支 | `feat/b1-05-04-openapi-report` |
| Worktree | `/root/projects/my-shop-b1-05-04-openapi-report` |
| 基础分支 | `feat/p2-backend`（= `origin/feat/p2-backend`） |
| 基础分支 SHA | `d1fa065a5620f7430596de236a60c0613aaac557` |
| 任务分支起点 SHA | `d1fa065a5620f7430596de236a60c0613aaac557` |
| 报告日期 | 2026-11-11 |
| 报告状态 | 契约层 PASS；Migration 实时复核与 B1-05-02/03 证据核对 **BLOCKED / NOT RUN**（见 §7、§10） |

> 本报告只维护一份，不额外创建重复的最终验收报告。
> 全部结论均标注证据来源；未执行的项目一律标 `NOT RUN`，不写成通过。
> 本任务未修改任何生产业务代码（`api/**`、`internal/**` 的 controller/logic/service/migrations 均只读）。

---

## 1. Executive Summary

### 1.1 项目基本信息

| 项 | 内容 |
|---|---|
| 系统 | SurgeCart 商城后端（GoFrame v2 模块化单体，Go 1.26.0） |
| 契约入口 | `openapi/surgecart.openapi.yaml`（OpenAPI 3.1.0，由生成器产出） |
| 契约生成器 | `tools/openapi-gen`（AST 解析 Go DTO / 路由 / 错误码 / 权限） |
| 契约门禁 | `scripts/contract-check.sh`（5 步，CI 的 `contract` stage 调用） |
| 数据库 | MySQL 8.x + 迁移工具（43 个 up 迁移，最新 `20261111000004`） |

### 1.2 当前 Git 基线

| 项 | 值 |
|---|---|
| `git rev-parse --show-toplevel` | `/root/projects/my-shop`（主工作区） |
| 本任务 worktree | `/root/projects/my-shop-b1-05-04-openapi-report` |
| 基线同步方式 | 本地落后 35 提交、领先 0，执行 `git merge --ff-only origin/feat/p2-backend` 快进（未使用 `reset --hard`） |
| 本任务是否 Push | 否 |
| 本任务是否 Merge 回基线 | 否 |

### 1.3 本次验收范围

1. 核对 210 条真实 API 路由、DTO、OpenAPI 契约、错误码与权限描述的一致性。
2. 将 B1-04 新增 API（68 条）纳入生成体系，校正生成器统计与路由测试断言，重新生成 OpenAPI。
3. 依据 B1-03-04 实际交付结果整理历史数据迁移映射矩阵。
4. 执行契约门禁并出具最终交付报告。

### 1.4 总体结论

| 维度 | 结论 |
|---|---|
| 路由 ↔ 契约双向一致 | **PASS**（210/210，缺失与多余均为 0） |
| DTO ↔ Schema 一致 | **PASS**（488 个 DTO 结构 → 490 个 Schema，0 个无法确认字段） |
| 错误码 ↔ codes.go | **PASS**（153 条码表逐项一致） |
| 权限码 三方一致（路由 / seed / OpenAPI） | **PASS**（73 个码，双向零差异） |
| 认证级别描述 | **PASS**（public 25 / user 76 / admin 109，与路由中间件一致） |
| 分页 / 时间 / 金额 / 幂等 / 枚举语义 | **PASS（本次修正 4 类偏差后）**，见 §4.12 与 §9 |
| planned 接口隔离 | **PASS**（4 条仍为 planned，未被伪装成已实现） |
| 契约门禁（`contract-check.sh`） | **PASS**，NOT RUN 0 |
| 历史迁移映射矩阵 | **PASS（静态证据）**；实时数据库复核 **NOT RUN** |
| Migration Up/Down/Up 证据核对 | **BLOCKED**：B1-05-02 交付报告尚未提供 |
| B1-05 整体验收结论 | **尚不具备**宣布通过的条件（见 §10.3） |

一句话结论：**契约、实现与文档在本次核对范围内已形成一致、可复核的证据链；但迁移运行时证据与 B1-05-02/03 的验收材料缺失，整体验收不能标记为 PASS。**

---

## 2. Git 与环境基线

### 2.1 Git 事实

| 项 | 值 |
|---|---|
| 仓库根目录 | `/root/projects/my-shop` |
| 基础分支 | `feat/p2-backend` |
| 基础分支 SHA | `d1fa065a5620f7430596de236a60c0613aaac557` |
| `origin/feat/p2-backend` SHA | `d1fa065a5620f7430596de236a60c0613aaac557` |
| 同步前落后/领先 | `0 35`（本地 0 领先、35 落后） |
| 任务分支 | `feat/b1-05-04-openapi-report` |
| 任务分支起点 HEAD | `d1fa065a5620f7430596de236a60c0613aaac557` |
| 已存在的其他 worktree | `/root/projects/my-shop-b1-05-01`（`feat/b1-05-01-build-unit`，未合并入基线） |
| 工作区初始状态 | 干净（`git status --short` 为空） |

### 2.2 工具链版本

| 工具 | 版本 | 证据 |
|---|---|---|
| Go | `go1.26.0 linux/amd64` | `go version` |
| Spectral | `6.17.1` | `npx --yes @stoplight/spectral-cli@6 --version` |
| git | `2.39.5` | `git --version` |
| MySQL 服务 | **不可用** | `mysql -h127.0.0.1 -uroot -proot -e "SHOW DATABASES"` → `ERROR 2002 (115)` |
| docker | 存在，无运行容器 | `docker ps` 空输出 |

### 2.3 数据库基线

| 项 | 值 |
|---|---|
| 迁移文件数 | 43 个 `.up.sql` / 20 个 `.down.sql` |
| 最新迁移版本 | `20261111000004_referral_graph_guard` |
| B1-03-04 验收时的版本 | `20261110000014`，`dirty=0`（见 `docs/reports/B1-03-04-legacy-data.md` §2） |
| 版本差 | 其后新增 4 个迁移（`20261111000001`～`20261111000004`），均为加列/加表/单例行初始化，**不含业务数据写入**（唯一 `INSERT` 为 `referral_graph_guard(id=1)` 单例） |

> 影响判断：后续 4 个迁移不改变历史迁移的目标行数，因此 B1-03-04 的导入行数可作为 §6 矩阵的有效证据（版本差异已在此显式声明）。

### 2.4 工作区状态

- 本任务全部修改均在 `feat/b1-05-04-openapi-report` 分支的独立 worktree 内完成。
- 修改文件全部落在任务书 §十 授权的文件边界内（清单见 §11.1）。
- 未发现其他并行任务修改本任务专属文件的冲突。

---

## 3. API 路由统计

### 3.1 最终真实数量

| 类别 | 数量 | 证据来源 |
|---|---:|---|
| 常驻 JSON API（合计） | **210** | `TestRouteTable`（真实 GoFrame 路由表，双向严格比对） |
| 前台 API | **98** | `x-api-count.frontend` + `TestRouteCountByScope` |
| 后台 API | **110** | `x-api-count.admin` + `TestRouteCountByScope` |
| 健康检查 | **2**（`GET /health`、`GET /ready`） | `TestRouteCountByScope` |
| 条件注册路由 | **1**（`GET /metrics`，仅 `flash_sale.metrics.enabled=true`） | `x-conditional-routes`，**不计入 210** |
| 非 API 静态路由 | **1**（`/storage/banners/*`） | `x-static-routes`，**不计入 210** |
| Planned 占位 | **4** | `x-planned-apis`、`TestContractPlannedNotRouted` |
| OpenAPI operation 总数 | **214** = 210 常驻 + 4 planned | `grep -c '      operationId:'` |
| OpenAPI path 条目 | **171** = 167 实路径 + 4 planned | `-check` 输出：主文件 171 / 分域 167 |

### 3.2 五方一致核对（任务书 §6.3）

| 口径 | 值 | 结论 |
|---|---|---|
| 真实 Go 路由表（`s.GetRoutes()` 排除 `ALL /*`） | 210 | 基准 |
| `internal/cmd/routes_test.go` 硬编码清单 | 210（`TestRouteTable` 断言 `len(want)==210`） | 一致 |
| OpenAPI 主文件 `x-api-count` | total 210 / frontend 98 / admin 110 / health 2 | 一致 |
| OpenAPI 分域文件（`openapi/paths/*.yaml`，30 个域文件） | 167 条路径逐字段一致 | 一致 |
| 生成器统计常量（`gen.go` `expectedTotal/Frontend/Admin/Health`） | 210 / 98 / 110 / 2 | 一致 |

### 3.3 各业务域路由数量（按 OpenAPI tag，合计 210）

| 域（tag） | 数量 | 域（tag） | 数量 | 域（tag） | 数量 |
|---|---:|---|---:|---|---:|
| 拼团 | 15 | 文章 | 14 | 砍价 | 13 |
| IAM | 11 | 订单 | 11 | 退款 | 10 |
| 推荐位 | 9 | 分销 | 9 | 商品 | 8 |
| 会员 | 8 | 运费 | 7 | 评价 | 6 |
| 轮播图 | 6 | 秒杀 | 6 | 角色 | 6 |
| 商品规格 | 6 | 分类 | 5 | 收货地址 | 5 |
| 购物车 | 5 | 优惠券 | 5 | 管理员 | 5 |
| 运营数据大屏 | 5 | 点赞 | 4 | 收藏 | 4 |
| 权限 | 4 | SKU | 4 | 库存 | 4 |
| 地区 | 3 | 钱包 | 3 | 后台认证 | 3 |
| 用户资料 | 2 | 上传 | 2 | Health | 2 |

### 3.4 认证与权限分布

| 认证形态 | 数量 | 说明 |
|---|---:|---|
| `public` | 25 | 22 条前台公开 + `POST /admin/login` + 2 条健康检查 |
| `user`（含会话校验） | 76 | 75 条 Auth 分组 + 1 条 `POST /logout`（`signature_only`） |
| `admin` | 109 | 含 `POST /admin/logout`（`signature_only`）；其中 98 条带权限码、11 条仅认证 |
| `x-auth-mode: signature_only` | 2 | `POST /logout`、`POST /admin/logout` |
| `require("<code>")` 路由 | 98 | 与 OpenAPI `x-permission` 双向零差异 |

### 3.5 B1-04 新增 68 条的构成（与 210 = 142 + 68 的自洽校验）

| 来源子任务 | 新增 | 分布 |
|---|---:|---|
| B1-04-05 一级分销 | 8 | 前台 4 / 后台 4 |
| B1-04-01 用户资料与地区字典 | 5 | 前台 5（`/profile` 2、`/regions` 3） |
| B1-04-02 商品规格与 SKU 组合生成 | 7 | 后台 7 |
| B1-04-03 运费模板 | 7 | 后台 7 |
| B1-04-04 钱包 | 3 | 前台 3 |
| B1-04-06 拼团 | 15 | 公开 3 / 前台 4 / 后台 8 |
| B1-04-07 砍价 | 13 | 公开 2 / 前台 6 / 后台 5 |
| B1-04-08 会员与佣金结算 | 9 | 前台 3 / 后台 6（含佣金人工结算 1 条） |
| B1-04 集成（订单算价） | 1 | 前台 1 |
| **合计** | **68** | 142 + 68 = **210** |

---

## 4. OpenAPI 契约核对

### 4.1 路由覆盖

- 双向比对由 `TestRouteTableMatchesContract` 完成：解析 `openapi/surgecart.openapi.yaml` 的 `paths`（排除 `x-status: planned`），与真实路由表互查。
- 结果：**真实路由未进契约 = 0；契约中不存在的路由（虚构）= 0**。
- HTTP Method：契约中的 `get/post/put/delete/patch` 与路由注册方法逐一对应（`TestRouteTable` 双向比对覆盖方法）。
- Path 参数完整性：`-check` 的 `checkPathParams` 校验每个 `{param}` 均在 `parameters` 中声明为 `in: path`，且不存在多余 path 参数（0 项问题）。
- operationId 唯一性：214 个 operationId 全部非空且全局唯一（`-check` 步骤 3）。

### 4.2 DTO 与 Schema

| 指标 | 数值 | 证据 |
|---|---:|---|
| Schema 总数 | **490** | `grep -cE '^[A-Za-z][A-Za-z0-9]*:'` |
| Request Schema | 210 | 与 210 条 API 一一对应 |
| Response Schema | 209 | `upload.TokenRes` 被两个 Req 共用 |
| 业务实体 Schema | 69 | `model.go` 的 `entitySchemaNames` 显式登记 |
| 基础设施 Schema | 2 | `Success`、`Error` |
| DTO 结构体（不含基础设施） | 488 | `docs/phase2/B1-02_DTO_SCHEMA_INVENTORY.md` §1 |
| 无法确认字段 | 0 | 未登记类型直接失败，不猜测 |

字段级核对（逐项对应任务书 §5.2 的六类风险）：

| 风险 | 核对结果 |
|---|---|
| `int64` 被误生成成 string | 未发现。`int/int64` 统一生成 `type: integer, format: int64` + `x-go-type`；`json.Number` 生成 `oneOf: [integer, string]`（金额入参） |
| 金额被转成浮点 | 未发现。金额字段一律 `integer` + `x-unit: cent`（当前 83 处），仅 `avg_rating` 为 `number/double` |
| Go JSON 字段名与 OpenAPI 不一致 | 未发现。Schema 字段名来自 AST 的 `json` tag，`-check` 步骤 8 深比较 `schemas.yaml` 与重新解析结果 |
| `omitempty` 与 null 语义混淆 | 请求侧 `required` 仅取 `v:"required"`；响应侧按"非 `omitempty` 即必出现"生成 `required`；`*T` 生成 `type: [T, "null"]` |
| 新增业务枚举描述错误 | **本任务发现并修正 26 个字段缺失 `enum`**（原仅在 description 中列取值），见 §4.12 / N-4 |
| 新增 API 缺 Request 或 Response Schema | 未发现。210 条 API 全部有对应 Req/Res Schema；68 条新增 API 无一例外（见 §5） |

### 4.3 Schema 类型与结构

- 嵌套/数组：`[]*T` → `type: array` + `items: <T>`（元素可空时 `items` 含 null 分支）；匿名嵌入 → `allOf` 字段提升。
- 可空：`*int64`、`*gtime.Time`、`*string` → `type: [x, "null"]`；`*T` 结构体 → `oneOf: [$ref, {type: null}]`。
- 分页结构：所有分页响应为 `items/total/page/size`，`total` 按实际 Go 类型声明（`integer`，`x-go-type` 保留 `int`/`int64` 差异）。

### 4.4 错误码

| 项 | 结果 |
|---|---|
| 事实来源 | `internal/codes/codes.go`（常量 + `codeTable`） |
| 码表条目数 | **153**（独立计数：`codeTable` 153 行；`openapi/components/error-codes.yaml` 153 条，唯一码 153） |
| HTTP Status | 由 `codeTable` 的 `http.StatusXxx` 直接映射，未做补全 |
| 描述一致性 | message 直接取自 `codeTable`，`-check` 步骤 7 深比较 |
| 业务域归属 | `parse_codes.go` 按 `CodeXxx` 前缀映射域名；跨域复用 35 条以 `reused_by_domains` 标注 |
| 码 `51` | **不在** `codes.go` 中（框架码穿透），契约以 `x-known-issue` 如实标注，未虚构为业务码 |
| 新增接口可达码 | operation 级 `x-error-codes` 由 controller→service→logic 静态调用链推导，`scope: static-call-chain`，非运行时穷举（见 O-5） |

### 4.5 HTTP Status 声明

- 成功统一 `200`（统一包装 `{code,message,data}`，业务成功码 0）。
- 失败按码表 HTTP Status 声明 `400 / 401 / 403 / 404 / 409 / 429 / 500 / 503` 的通用响应组件。
- D1 缺陷接口同时声明 `400`（目标规范）与 `500`（实际行为），并以 `x-known-issue` 说明，共 **28** 条（见 §9 O-1）。

### 4.6 认证与权限

| 核对项 | 结果 |
|---|---|
| 路由组中间件 → `x-auth` | 一致（`parse_routes.go` AST 作用域分析，含同名组变量在不同闭包中的区分） |
| `require("<code>")` → `x-permission` | 98/98 完全一致（本任务用独立脚本比对 `routes_*.go` 与主文件，0 差异） |
| seed 权限清单 → 路由权限码 | 73/73 集合相同（`comm` 无差异） |
| OpenAPI → seed | 一致（`-check` 步骤 6 三向比对） |
| 超管绕过 / `auth_epoch` / Redis 会话 | 在 `components/security.yaml` 与 operation description 中说明 |

### 4.7 分页

| 形态 | operation 数 | 语义 | 契约表达 |
|---|---:|---|---|
| `clamp`（静默归一） | 12 | `page<1→1`、`size<1→20`、`size>100→100` | 不声明 `minimum/maximum`，参数与 operation 均带 `x-pagination-mode: clamp` |
| `strict`（严格校验） | 14 | 越界/非正整数 → `400 + 1001` | 参数与 operation 带 `x-pagination-mode: strict` + `x-pagination-contract` |
| `none`（无分页） | 184 | 返回全量或固定集合 | `x-pagination-mode: none`，不声明分页参数 |

修正记录（N-1）：修正前 `strict` 仅 10 条，4 条实际严格分页的接口被误标为 `none`（见 §9）。

### 4.8 时间格式

| 场景 | 契约表达 | 数量/说明 |
|---|---|---|
| 响应 `*gtime.Time` | `type: [string,"null"]` + `pattern: ^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$` + `x-format: gtime-local` | 如实描述"无时区偏移"，**未伪装成 RFC3339** |
| 请求 `*gtime.Time` | `x-format: gtime-lenient` | 宽松解析（秒杀/优惠券/拼团等） |
| RFC3339 请求参数 | `type: string` + 描述"RFC3339（含时区偏移）"，**未声明 `format: date-time`** | 如后台订单 `start_time/end_time`、大屏时间参数（见 O-8） |
| 纯日期 | `format: date` + `pattern` | 大屏 `date` |
| Unix 秒 | `type: integer, format: int64` + `x-unit: unix-second` | `login_at`、`expires_at`（本任务补齐，见 N-7） |
| `format: date-time` 出现在响应 DTO | **0 处**（`components/schemas.yaml` 与主文件均为 0 次） | 与 D4 "不得伪装成 RFC3339" 一致 |

### 4.9 金额

| 项 | 结果 |
|---|---|
| 金额表达 | 一律 `integer` + `x-unit: cent`（整数分），无浮点金额 |
| `x-unit: cent` 覆盖 | **83 处**（修正前 64 处；本次补齐 19 处，见 N-3） |
| 非金额整数字段 | `discount_rate_bp`（万分比）、`*_count`、`quantity`、`sort` 等未被误标为金额 |
| 迁移侧金额转换 | 由 B1-03-04 以定点整数转换（decimal 元 → 分），无 float64（见 §6） |

### 4.10 幂等键

| 接口 | 传输方式 | 字段 | 必填 | 契约声明 |
|---|---|---|---|---|
| `POST /orders` | body | `idempotency_key` | 是 | `x-idempotency.mode: body` |
| `POST /flash-sales/:id/orders` | body | `idempotency_key` | 否 | `mode: body`，`required: false` |
| `GET /flash-sales/:id/orders/result` | query | `idempotency_key` | 否 | `mode: query` |
| `POST /group-buys/:id/teams` | body | `idempotency_key` | 是 | `mode: body` |
| `POST /group-buy-teams/:id/join` | body | `idempotency_key` | 是 | `mode: body` |
| `POST /wallet/recharges` | body | `idempotency_key` | 是 | `mode: body`（**本任务修正**，原误标 `none`） |
| 其余 204 条 | — | — | — | `mode: none` |
| `Idempotency-Key` HTTP Header | **不声明**（D5 已否决） | — | — | 全部 operation 均无该参数 |

### 4.11 Planned API

| 占位接口 | `x-missing-ref` | 状态 | 与真实路由隔离 |
|---|---|---|---|
| `GET /flash-sales` | M1 | planned（`x-status: planned`、`x-proposed: true`、`x-implemented: false`） | 是（`TestContractPlannedNotRouted`） |
| `GET /flash-sales/{id}` | M1 | 同上 | 是 |
| `GET /coupons` | M2 | 同上 | 是 |
| `GET /orders/{id}/coupons` | M3 | 同上 | 是 |

- 4 条均未被 B1-04 或其他任务实现，**继续保持 planned**。
- 响应只声明 `200`（无 schema）与 `501`（说明命中 404 text/plain），**未虚构响应体**。
- `-check` 断言 planned 数量 = 4 且与实际路由无冲突。

### 4.12 本次核对发现并修正的契约偏差

| 编号 | 偏差 | 发现方式 | 修正方式 | 影响面 |
|---|---|---|---|---|
| N-1 | 4 条严格分页接口被标为 `x-pagination-mode: none` | 对比 DTO 的 `page/size` 字段与 `opMetaByKey` 登记 | `opmeta.go` 补登记 `strict`，重新生成 | `GET /wallet/transactions`、`GET /bargain-activities`、`GET /admin/bargain-activities`、`GET /my/bargain-instances` |
| N-2 | `POST /wallet/recharges` 带 `v:"required"` 的 `idempotency_key`，契约却声明 `mode: none` | 对比 DTO 幂等字段与 `x-idempotency` | `opmeta.go` 补 `idemBody("idempotency_key", true)` | 1 条接口 |
| N-3 | 19 处显式"整数分"金额字段缺 `x-unit: cent` | 扫描 `integer` 类型且描述含"整数分"但无 `x-unit` 的字段 | `metadata.go` 的 `moneyExactFields` 补 12 个字段名 | 钱包余额/流水、订单运费与算价明细、运费模板/规则、砍价助力价格 |
| N-4 | B1-04 新增域的 26 个状态/枚举字段只有文字描述、无 `enum`（违反 D2.3 第 5 条） | 扫描 `type: string/integer` 且描述中列举取值但无 `enum` 的字段 | `metadata.go` 的 `enumRegistry` 补 26 条（取值均取自 Go 常量） | 砍价 4、拼团 7、钱包 5、会员 3、运费 5、订单算价 1、秒杀 1 |
| N-5 | 生成器与测试中的统计/注释漂移（142/184/68/2/8、钱包条目重复、`GET /my/commissions` 陈旧元数据） | 逐条比对真实路由与源码注释 | 修正注释与数组；删除陈旧 opMeta 条目 | 见 §11.1 |
| N-6 | 缺少"元数据漂移"守护：陈旧 opMeta 与漏登记分页不会被门禁发现 | 由 N-1/N-5 暴露的根因 | `check.go` 新增 2 条校验（opMeta 键必须对应真实路由；DTO 含 `page/size` 必须登记分页模式） | 防止同类问题回归 |
| N-7 | 2 处 Unix 秒字段缺 `x-unit: unix-second`（D4.3 要求） | 扫描 `integer` 字段描述含"unix 秒"但无单位标注 | `metadata.go` 新增 `unixSecondFields`，`build_schema.go` 标注单位 | `login_at`、`expires_at` |

修正后指标变化：

| 指标 | 修正前 | 修正后 |
|---|---:|---:|
| `x-pagination-mode: strict`（operation 级） | 10 | 14 |
| `x-idempotency.mode: body` | 4 | 5 |
| `components/schemas.yaml` 中 `x-unit: cent` | 64 | 83 |
| `components/schemas.yaml` 中 `x-unit: unix-second` | 0 | 2 |
| `components/schemas.yaml` 中 `enum` 字段 | 44 | 70 |
| 错误码 / 权限码 / API 数 / Schema 数 | 153 / 73 / 210 / 490 | 不变（无业务变更） |

### 4.13 B1-02 已知问题复核

| 编号 | 原问题 | 复核结论 | 处置 |
|---|---|---|---|
| K-1 | 未被引用的 `IdempotencyKeyHeader` 组件 | **已不存在**：`components.go` 的组件定义与登记表均已删除，仅在注释中保留整改说明 | 已关闭（B1-02-review-fix R2 完成，本任务复核确认） |
| I-1 | paths 采用内联，未使用 Path Item `$ref` | 现状未变：主文件内联 171 条 path，`paths/*.yaml` 为分域评审视图，由 `-check` 保证与主文件逐字段一致 | 保留。属工具链工程选择（Spectral 6.17.1 对路径级 `$ref` 误报），报告中保留理由，非规范限制 |
| I-2 | Spectral `oas3-unused-component` 关闭 | 仍关闭，但已有等价替代：`components_reach.go` 递归可达性 + 逐条白名单 + `stale/已引用/新未引用` 三类失败 | 保留。替代方案比原规则更严格（禁止通配） |
| I-3 | 未被引用组件清单及保留理由 | 已升级为可机读清单 `tools/openapi-gen/unused-components-allowlist.yaml`：228 条（210 完整请求 DTO + 12 null-data 响应 + 2 通用响应模板 + 4 分页语义组件），逐条列名并注明理由 | 保留。本任务未删减任何有审计价值的组件（含 Request Schema、null-data Response Schema） |

---

## 5. 新增 API 核对清单（B1-04 共 68 条）

说明：
- "DTO" 列取自 `api/*/v1/*.go` 的 `g.Meta path/method` + 结构体命名；"OpenAPI" 列为生成的 response Schema 与 operationId。
- "真实路由"列全部为"已注册"，依据 `TestRouteTable` 的 210 条双向严格比对（缺失/多余任一都会失败）。
- "错误码"列为该 operation 的 `x-error-codes.codes` 总数与扣除通用码（1000/1001/1002/1003）后的业务码数量，明细见 `components/error-codes.yaml`。
- "权限"列：`user`/`admin` 表示仅认证；反引号内容为 `x-permission` 权限码。

| # | Method | Path | DTO（Req / Res） | 真实路由 | OpenAPI | 错误码 | 权限 | 结果 |
|---:|---|---|---|---|---|---|---|---|
| 1 | POST | `/distribution/referral` | `BindReferralReq` / `DistributionBindReferralResponse` | 已注册 | `distributionBindReferral` | 8 个（业务 4） | user | PASS |
| 2 | GET | `/distribution/me` | `MyInfoReq` / `DistributionMyInfoResponse` | 已注册 | `distributionMyInfo` | 4 个（业务 0） | user | PASS |
| 3 | GET | `/distribution/subordinates` | `ListSubordinatesReq` / `DistributionListSubordinatesResponse` | 已注册 | `distributionListSubordinates` | 4 个（业务 0） | user | PASS |
| 4 | GET | `/distribution/commissions` | `ListCommissionsReq` / `DistributionListCommissionsResponse` | 已注册 | `distributionListMyCommissions` | 4 个（业务 0） | user | PASS |
| 5 | GET | `/admin/distributions/referrals` | `AdminListReferralsReq` / `DistributionAdminListReferralsResponse` | 已注册 | `distributionAdminListReferrals` | 4 个（业务 0） | `distribution:referral_list` | PASS |
| 6 | GET | `/admin/distributions/distributors` | `AdminListDistributorsReq` / `DistributionAdminListDistributorsResponse` | 已注册 | `distributionAdminListDistributors` | 4 个（业务 0） | `distribution:referral_list` | PASS |
| 7 | GET | `/admin/distributions/commissions` | `AdminListCommissionsReq` / `DistributionAdminListCommissionsResponse` | 已注册 | `distributionAdminListCommissions` | 4 个（业务 0） | `distribution:commission_list` | PASS |
| 8 | GET | `/admin/distributions/commissions/stats` | `AdminCommissionStatsReq` / `DistributionAdminCommissionStatsResponse` | 已注册 | `distributionAdminCommissionStats` | 4 个（业务 0） | `distribution:commission_list` | PASS |
| 9 | GET | `/profile` | `GetReq` / `ProfileGetResponse` | 已注册 | `profileGet` | 5 个（业务 1） | user | PASS |
| 10 | PUT | `/profile` | `UpdateReq` / `ProfileUpdateResponse` | 已注册 | `profileUpdate` | 6 个（业务 2） | user | PASS |
| 11 | GET | `/regions` | `ListReq` / `RegionListResponse` | 已注册 | `regionList` | 2 个（业务 0） | public | PASS |
| 12 | GET | `/regions/tree` | `TreeReq` / `RegionTreeResponse` | 已注册 | `regionTree` | 2 个（业务 0） | public | PASS |
| 13 | GET | `/regions/:id` | `DetailReq` / `RegionDetailResponse` | 已注册 | `regionDetail` | 2 个（业务 0） | public | PASS |
| 14 | POST | `/admin/product-specs` | `CreateGroupReq` / `SpecCreateGroupResponse` | 已注册 | `specCreateGroup` | 7 个（业务 3） | `spec:create` | PASS |
| 15 | PUT | `/admin/product-specs/:id` | `UpdateGroupReq` / `SpecUpdateGroupResponse` | 已注册 | `specUpdateGroup` | 6 个（业务 2） | `spec:update` | PASS |
| 16 | DELETE | `/admin/product-specs/:id` | `DeleteGroupReq` / `SpecDeleteGroupResponse` | 已注册 | `specDeleteGroup` | 6 个（业务 2） | `spec:delete` | PASS |
| 17 | POST | `/admin/product-specs/:id/values` | `CreateValueReq` / `SpecCreateValueResponse` | 已注册 | `specCreateValue` | 6 个（业务 2） | `spec:create` | PASS |
| 18 | PUT | `/admin/product-spec-values/:id` | `UpdateValueReq` / `SpecUpdateValueResponse` | 已注册 | `specUpdateValue` | 6 个（业务 2） | `spec:update` | PASS |
| 19 | DELETE | `/admin/product-spec-values/:id` | `DeleteValueReq` / `SpecDeleteValueResponse` | 已注册 | `specDeleteValue` | 6 个（业务 2） | `spec:delete` | PASS |
| 20 | POST | `/admin/products/:id/skus/generate` | `GenerateReq` / `SkuGenerateResponse` | 已注册 | `skuGenerate` | 10 个（业务 6） | `sku:create` | PASS |
| 21 | POST | `/admin/freight-templates` | `AdminCreateReq` / `FreightAdminCreateResponse` | 已注册 | `freightAdminCreate` | 6 个（业务 2） | `freight:create` | PASS |
| 22 | GET | `/admin/freight-templates` | `AdminListReq` / `FreightAdminListResponse` | 已注册 | `freightAdminList` | 4 个（业务 0） | `freight:list` | PASS |
| 23 | GET | `/admin/freight-templates/:id` | `AdminDetailReq` / `FreightAdminDetailResponse` | 已注册 | `freightAdminDetail` | 5 个（业务 1） | `freight:view` | PASS |
| 24 | PUT | `/admin/freight-templates/:id` | `AdminUpdateReq` / `FreightAdminUpdateResponse` | 已注册 | `freightAdminUpdate` | 7 个（业务 3） | `freight:update` | PASS |
| 25 | PUT | `/admin/freight-templates/:id/status` | `AdminUpdateStatusReq` / `FreightAdminUpdateStatusResponse` | 已注册 | `freightAdminUpdateStatus` | 6 个（业务 2） | `freight:update` | PASS |
| 26 | GET | `/admin/freight-templates/:id/rules` | `AdminListRulesReq` / `FreightAdminListRulesResponse` | 已注册 | `freightAdminListRules` | 5 个（业务 1） | `freight:view` | PASS |
| 27 | PUT | `/admin/freight-templates/:id/rules` | `AdminReplaceRulesReq` / `FreightAdminReplaceRulesResponse` | 已注册 | `freightAdminReplaceRules` | 8 个（业务 4） | `freight:update` | PASS |
| 28 | GET | `/wallet` | `AccountReq` / `WalletAccountResponse` | 已注册 | `walletAccount` | 5 个（业务 1） | user | PASS |
| 29 | GET | `/wallet/transactions` | `TransactionListReq` / `WalletTransactionListResponse` | 已注册 | `walletTransactions` | 4 个（业务 0） | user | PASS（本次修正分页模式） |
| 30 | POST | `/wallet/recharges` | `RechargeReq` / `WalletRechargeResponse` | 已注册 | `walletRecharge` | 12 个（业务 8） | user | PASS（本次修正幂等声明） |
| 31 | GET | `/group-buys` | `ListActivitiesReq` / `GroupBuyListActivitiesResponse` | 已注册 | `groupBuyListActivities` | 2 个（业务 0） | public | PASS |
| 32 | GET | `/group-buys/:id` | `ActivityDetailReq` / `GroupBuyActivityDetailResponse` | 已注册 | `groupBuyActivityDetail` | 3 个（业务 1） | public | PASS |
| 33 | GET | `/group-buys/:id/teams` | `ListTeamsReq` / `GroupBuyListTeamsResponse` | 已注册 | `groupBuyListTeams` | 3 个（业务 1） | public | PASS |
| 34 | POST | `/group-buys/:id/teams` | `CreateTeamReq` / `GroupBuyCreateTeamResponse` | 已注册 | `groupBuyCreateTeam` | 8 个（业务 4） | user | PASS |
| 35 | POST | `/group-buy-teams/:id/join` | `JoinTeamReq` / `GroupBuyJoinTeamResponse` | 已注册 | `groupBuyJoinTeam` | 9 个（业务 5） | user | PASS |
| 36 | GET | `/group-buy-teams/:id` | `TeamDetailReq` / `GroupBuyTeamDetailResponse` | 已注册 | `groupBuyTeamDetail` | 5 个（业务 1） | user | PASS |
| 37 | GET | `/my/group-buys` | `MyListReq` / `GroupBuyMyListResponse` | 已注册 | `groupBuyMyList` | 4 个（业务 0） | user | PASS |
| 38 | POST | `/admin/group-buys` | `AdminCreateReq` / `GroupBuyAdminCreateResponse` | 已注册 | `groupBuyAdminCreate` | 8 个（业务 4） | `group_buy:create` | PASS |
| 39 | PUT | `/admin/group-buys/:id` | `AdminUpdateReq` / `GroupBuyAdminUpdateResponse` | 已注册 | `groupBuyAdminUpdate` | 8 个（业务 4） | `group_buy:update` | PASS |
| 40 | POST | `/admin/group-buys/:id/status` | `AdminStatusReq` / `GroupBuyAdminStatusResponse` | 已注册 | `groupBuyAdminStatus` | 5 个（业务 1） | `group_buy:update` | PASS |
| 41 | GET | `/admin/group-buys` | `AdminListReq` / `GroupBuyAdminListResponse` | 已注册 | `groupBuyAdminList` | 4 个（业务 0） | `group_buy:list` | PASS |
| 42 | GET | `/admin/group-buys/:id` | `AdminDetailReq` / `GroupBuyAdminDetailResponse` | 已注册 | `groupBuyAdminDetail` | 5 个（业务 1） | `group_buy:view` | PASS |
| 43 | GET | `/admin/group-buy-teams` | `AdminListTeamsReq` / `GroupBuyAdminListTeamsResponse` | 已注册 | `groupBuyAdminListTeams` | 4 个（业务 0） | `group_buy:list` | PASS |
| 44 | GET | `/admin/group-buy-teams/:id` | `AdminTeamDetailReq` / `GroupBuyAdminTeamDetailResponse` | 已注册 | `groupBuyAdminTeamDetail` | 5 个（业务 1） | `group_buy:view` | PASS |
| 45 | POST | `/admin/group-buy-teams/:id/close` | `AdminCloseTeamReq` / `GroupBuyAdminCloseTeamResponse` | 已注册 | `groupBuyAdminCloseTeam` | 6 个（业务 2） | `group_buy:close` | PASS |
| 46 | GET | `/bargain-activities` | `ListReq` / `BargainListResponse` | 已注册 | `bargainList` | 2 个（业务 0） | public | PASS（本次修正分页模式） |
| 47 | GET | `/bargain-activities/:id` | `DetailReq` / `BargainDetailResponse` | 已注册 | `bargainDetail` | 3 个（业务 1） | public | PASS |
| 48 | POST | `/bargain-activities/:id/instances` | `StartReq` / `BargainStartResponse` | 已注册 | `bargainStart` | 7 个（业务 3） | user | PASS |
| 49 | GET | `/my/bargain-instances` | `MyListReq` / `BargainMyListResponse` | 已注册 | `bargainMyList` | 4 个（业务 0） | user | PASS（本次修正分页模式） |
| 50 | GET | `/bargain-instances/:id` | `InstanceDetailReq` / `BargainInstanceDetailResponse` | 已注册 | `bargainInstanceDetail` | 5 个（业务 1） | user | PASS |
| 51 | GET | `/bargain-instances/:id/helps` | `HelpsReq` / `BargainHelpsResponse` | 已注册 | `bargainHelps` | 5 个（业务 1） | user | PASS |
| 52 | POST | `/bargain-instances/:id/help` | `HelpReq` / `BargainHelpResponse` | 已注册 | `bargainHelp` | 8 个（业务 4） | user | PASS |
| 53 | POST | `/bargain-instances/:id/abandon` | `AbandonReq` / `BargainAbandonResponse` | 已注册 | `bargainAbandon` | 6 个（业务 2） | user | PASS |
| 54 | POST | `/admin/bargain-activities` | `AdminCreateReq` / `BargainAdminCreateResponse` | 已注册 | `bargainAdminCreate` | 5 个（业务 1） | `bargain:create` | PASS |
| 55 | GET | `/admin/bargain-activities` | `AdminListReq` / `BargainAdminListResponse` | 已注册 | `bargainAdminList` | 4 个（业务 0） | `bargain:list` | PASS（本次修正分页模式） |
| 56 | GET | `/admin/bargain-activities/:id` | `AdminDetailReq` / `BargainAdminDetailResponse` | 已注册 | `bargainAdminDetail` | 5 个（业务 1） | `bargain:view` | PASS |
| 57 | PUT | `/admin/bargain-activities/:id` | `AdminUpdateReq` / `BargainAdminUpdateResponse` | 已注册 | `bargainAdminUpdate` | 6 个（业务 2） | `bargain:update` | PASS |
| 58 | POST | `/admin/bargain-activities/:id/status` | `AdminStatusReq` / `BargainAdminStatusResponse` | 已注册 | `bargainAdminStatus` | 6 个（业务 2） | `bargain:update` | PASS |
| 59 | GET | `/members/levels` | `LevelsReq` / `MemberLevelsResponse` | 已注册 | `memberLevels` | 4 个（业务 0） | user | PASS |
| 60 | GET | `/my/membership` | `MyReq` / `MemberMyResponse` | 已注册 | `memberMy` | 4 个（业务 0） | user | PASS |
| 61 | GET | `/my/membership/growth-logs` | `GrowthLogsReq` / `MemberGrowthLogsResponse` | 已注册 | `memberGrowthLogs` | 4 个（业务 0） | user | PASS |
| 62 | GET | `/admin/member-levels` | `AdminListLevelsReq` / `MemberAdminListLevelsResponse` | 已注册 | `memberAdminListLevels` | 4 个（业务 0） | `member:list` | PASS |
| 63 | POST | `/admin/member-levels` | `AdminCreateLevelReq` / `MemberAdminCreateLevelResponse` | 已注册 | `memberAdminCreateLevel` | 6 个（业务 2） | `member:create` | PASS |
| 64 | PUT | `/admin/member-levels/:id` | `AdminUpdateLevelReq` / `MemberAdminUpdateLevelResponse` | 已注册 | `memberAdminUpdateLevel` | 7 个（业务 3） | `member:update` | PASS |
| 65 | DELETE | `/admin/member-levels/:id` | `AdminDeleteLevelReq` / `MemberAdminDeleteLevelResponse` | 已注册 | `memberAdminDeleteLevel` | 6 个（业务 2） | `member:delete` | PASS |
| 66 | GET | `/admin/members/:user_id` | `AdminMemberReq` / `MemberAdminMemberResponse` | 已注册 | `memberAdminMember` | 4 个（业务 0） | `member:view` | PASS |
| 67 | POST | `/admin/distributions/commissions/:id/settle` | `AdminSettleCommissionReq` / `DistributionAdminSettleCommissionResponse` | 已注册 | `distributionAdminSettleCommission` | 6 个（业务 2） | `distribution:settle` | PASS |
| 68 | POST | `/orders/quote` | `QuoteReq` / `OrderQuoteResponse` | 已注册 | `orderQuote` | 10 个（业务 6） | user | PASS |

---

## 6. Migration 数据映射矩阵

### 6.1 证据来源与判定口径

| 项 | 内容 |
|---|---|
| 源库 | `my_shop_staging`（`tmp/shopping.sql` 导入，只读；83 张表） |
| 目标库 | `my_shop_b10304_verify`（独立验证库） |
| 导入工具 | `internal/logic/legacymigrate`（`my-shop legacy-migrate <dry-run\|import>`） |
| 证据文本 | `docs/reports/B1-03-04-legacy-data.md`（§3 源统计、§4 实际导入、§8 完整性） |
| 规划依据 | `docs/phase2/B1-03_FINAL_MIGRATION_AUDIT.md` §9（迁移映射建议） |
| 本次是否实时查询数据库 | **NOT RUN**：本环境无 MySQL 服务（`ERROR 2002`），历史验证库不可访问，源转储 `tmp/` 不在工作区 |
| "PASS"含义 | 源记录数、实际导入数、目标记录数三者自洽，且差异有明确业务解释；**不等于本次重新跑过迁移** |

独立可核查的算术校验（不依赖数据库）：

- 地区：`34 + 369 + 3535 = 3938`，与 `internal/regiondata/regions_v1.tsv` 实际数据行数一致（表头/注释行已排除）。
- `legacy_id_map` 行数：`57(分类) + 55(商品) + 109(SKU) + 20(用户) + 23(地址) + 51(订单) + 56(订单明细) + 2(券模板) + 12(券批次) + 8(用户券) + 13(评价) + 5(收藏) = 411`，与报告 §7 的 411 行一致（110 个 SKU 中 1 个为派生默认 SKU，无历史主键）。

### 6.2 主映射矩阵

| # | 源表 | 目标表 | 转换规则 | 源记录数 | 实际导入数 | 目标记录数 | 差异 | 结果 |
|---:|---|---|---|---:|---:|---:|---:|---|
| 1 | `system_city` | `regions` | `id←city_id`（不重编号）、`parent_id←parent_id`、`level←level+1`（0/1/2→1/2/3）、`name` 直取；导入前校验父存在与层级递增 | 3938 | 3938 | 3938 | 0 | PASS |
| 2 | `store_category` | `categories` | `pid→parent_id`（0→0）、`cate_name→name`、`sort→sort`、`is_show→status`（1→1/0→0）；软删分类保留但置禁用；按 `parent_id+name` 去重避 `uk_parent_name` 冲突 | 57 | 57 | 57 | 0 | PASS |
| 3 | `store_product` | `products` | `store_name→name`；`cate_id`（逗号串）取首个有效分类；`price` decimal 元→整数分；`image→main_image`；`slider_image`→`product_images`；`is_show→status`；`stock` 不写商品表；软删 172 条中仅保留被业务引用的 1 条（`off_shelf`） | 226 | 55 | 55 | −171（171 条未被引用的软删商品按 ARCHIVE 不迁） | PASS |
| 4 | `store_product`（图片） | `product_images` | `slider_image` 逗号串拆分，保持原顺序 | 派生 | 派生 | 派生 | 无独立源行 | PASS |
| 5 | `store_product_attr_result`（主）/`store_product_attr`（回退） | `product_specs` | 以 `attr[].value` 为规格组名，权威来源为 `attr_result`，`attr` 表仅回退 | 52 | 52 | 52 | 0 | PASS |
| 6 | `store_product_attr_result`（主）/`store_product_attr`（回退） | `product_spec_values` | `attr[].detail[]` 拆分规格值，保持原顺序 | 112 | 112 | 112 | 0 | PASS |
| 7 | `store_product_attr_value`（+派生默认 SKU） | `skus` | `unique→sku_code`；规格值按"值多重集合"匹配（不按位置）；`price` 元→分；`spec_signature` = spec_id 升序 + 小写 MD5；单规格商品（`id=21`）派生 1 个无历史主键的 `默认` SKU | 111 | 110 | 110 | −1（跳过 2 条未引用软删商品 SKU，新增 1 条派生 SKU） | PASS |
| 8 | `store_product_attr_value.stock` | `inventories` | 每 SKU 一条库存；商品级/秒杀/拼团库存不入库，避免重复计入 | 111 | 110 | 110 | 同上 | PASS |
| 9 | `store_product_attr_result.value[].detail` | `sku_spec_values` | 规格组/规格值与 SKU 的关联；无法唯一匹配的组合不写关联、不猜测 | 120 | 120 | 120 | 0 | PASS |
| 10 | `eshop_user` | `users` | `phone`（唯一）、`username`、`status`；`is_del=1` 不迁；密码置 `!legacy-md5-forced-reset`（非 bcrypt，强制重置） | 20 | 20 | 20 | 0 | PASS |
| 11 | `user_address` | `addresses` | `uid→user_id`、`real_name→recipient_name`、省市区/详址；`is_default` 重算为 `default_key` 唯一 | 36 | 23 | 23 | −13（5 条软删 + 8 条指向不存在用户） | PASS |
| 12 | `store_coupon` | `coupon_templates` | `title→name`、`coupon_price→discount_amount`（分）、`use_min_price→threshold_amount`、有效期以批次为准 | 2 | 2 | 2 | 0 | PASS |
| 13 | `store_coupon_issue` | `coupon_batches` | `cname→name`、`cid→template_id`、`total_count/remain_count`、起止时间、`is_permanent`、`status`；含 9 条 `is_del=1`（历史用户券仍引用） | 12 | 12 | 12 | 0 | PASS |
| 14 | `store_coupon`(type=1) | `coupon_template_scopes` | 商品范围：模板 `type=1` → 商品 2 | 1 | 1 | 1 | 0 | PASS |
| 15 | `store_coupon_user`（+`store_coupon_issue_user` 定位批次） | `user_coupons` | 多对一合并：`cid→template_id`、批次由 `issue_user(uid,模板,领取时间)` 精确定位；保留券面额快照；`claim_no` 按用户+模板领取顺序生成；`status` 0/2→10、1→30 | 8（+8 领取记录） | 8 | 8 | 0 | PASS |
| 16 | `store_order` | `orders` | `order_id→order_no`、`uid→user_id`、地址四段；`total_price→original_amount`、`pay_price→total_amount`、`coupon_price+deduction_price→discount_amount`、`total_postage→shipping_fee`（均元→分）；`pay_type` `yue→balance`；`status` 由流水重建 | 51 | 51 | 51 | 0 | PASS |
| 17 | `store_order_cart_info` | `order_items` | `cart_info` JSON 拆出 `product_id/sku/qty/price` 行快照；2 条源 SKU `unique` 变更，按商品+规格名回退匹配 | 56 | 56 | 56 | 0 | PASS |
| 18 | `store_order_status` | `order_status_logs` | `change_type→from/to_status`（create→10、pay_success→20、delivery_goods→30、user_take_delivery→40、order_eval→50、remove_order 不改状态仅写 `delete` 审计）；状态只前进 | 132 | 132 | 132 | 0 | PASS |
| 19 | `eshop_user.now_money` | `wallet_accounts` | 仅迁余额快照（期初），不迁 `user_bill` 流水 | 20 | 14 | 14 | −6（余额为 0 的用户不建账户） | PASS |
| 20 | `eshop_user.now_money` | `wallet_transactions` | 每账户 1 条期初流水：`direction=1`、`before=0`、`after=balance`、`idempotency_key=legacy:wallet_opening:<user_id>` | 20 | 14 | 14 | 同上 | PASS |
| 21 | `eshop_user.integral` | `user_points_accounts` | 期初积分快照 | 20 | 7 | 7 | −13（积分为 0 的用户不建账户） | PASS |
| 22 | `eshop_user.integral` | `user_points_logs` | 每账户 1 条 `change_type=legacy_opening` 期初记录 | 20 | 7 | 7 | 同上 | PASS |
| 23 | `store_product_reply` | `reviews` | `uid→user_id`、`oid`+`unique` 定位 `order_item_id`、`product_score→rating`、`pics` 逗号串→`image_urls` JSON | 13 | 13 | 13 | 0 | PASS |
| 24 | `store_product_relation`(`type='collect'`,`is_del=0`) | `favorites` | 仅收藏；去重后写入 | 104 | 5 | 5 | −99（80 条足迹 + 19 条取消收藏） | PASS |
| 25 | （全部上述对象） | `legacy_id_map` | `entity_type + old_id → new_id` 唯一映射，供幂等与回溯 | — | — | 411 | 与 12 类实体求和一致 | PASS |

合计：23 个数据域 + 图片派生域 + 映射表；失败 0，未解释差异 0。

### 6.3 一对多 / 多对一 / 字段转换 / 过滤 / 默认值 / 无法迁移

| 类型 | 具体项 |
|---|---|
| 一对多 | ① `store_product` → `products` + `product_images`；② `store_coupon_issue` → `coupon_batches` + `coupon_template_scopes`；③ `store_order` → `orders` + `order_items` + `order_status_logs`；④ 单个商品 → 多个 `skus` + `sku_spec_values` |
| 多对一 | `store_coupon_issue_user`(8) + `store_coupon_user`(8) → `user_coupons`(8)，批次与领取序号在同一步合并 |
| 字段转换 | 金额 `decimal(8,2) 元 → 整数分`（定点，无 float64）；订单状态 `流水重建 → 10/20/30/40/50`；地区 `level+1`；`pay_type` `yue→balance`；券 `status` `0/2→10`、`1→30`；SKU 规格"值多重集合"匹配；密码 `MD5→强制重置占位` |
| 数据过滤 | `is_del` 软删（商品/分类/地址/收藏）；孤儿引用（8 条地址指向不存在用户）；足迹 80 条；取消收藏 19 条；余额/积分为 0 的用户 |
| 默认值填充 | 单规格商品派生 `默认` SKU（无历史主键）；未支付订单 `pay_type=''`；`transaction_no=''`（不虚构支付流水）；`claim_no` 由领取顺序生成 |
| 无法迁移 | ① 用户密码（无盐 MD5 不可转 bcrypt，强制重置）；② 4 张已使用券无源订单关联（保持 `order_id=NULL`）；③ 8 个 `is_del=1` 订单无 `remove_order` 流水（不伪造）；④ `store_coupon_issue.remain_count` 与领取记录不一致（保留源值）；⑤ 第三方凭据/支付配置/装修 JSON（安全禁止） |

### 6.4 明确未迁移的对象（与矩阵互补，避免"只列主要表"的误读）

| 源表 | 行数 | 处理 | 理由 |
|---|---:|---|---|
| `store_seckill` | 0 | 不迁 | 旧秒杀模型与现 Lua 准入 + 请求表 + Outbox 不同代 |
| `store_combination` | 4 | 不迁 | 拼团活动为 B1-04 新建模型，历史活动未导入（无用户侧实例数据） |
| `store_bargain` / `store_bargain_user` / `store_bargain_user_help` | 1 / 0 / 0 | 不迁 | 同上，砍价为 B1-04 新建模型 |
| `store_pink` | 0 | 不迁 | 无数据 |
| `store_cart` | 78 | 不迁 | 购物车为瞬时态，且含已结束活动的脏钩子 |
| `user_bill` | 429 | 不迁（仅作证据） | 与余额快照二选一会导致重复入账 |
| `user_recharge` | 325 | 不迁 | 同上；且要求"快照优先、流水归档" |
| `user_sign` | 15 | 不迁 | 签到业务未实现 |
| `system_user_level` | 6 | 不迁 | 会员等级由 B1-04-08 另行配置（阈值语义需产品确认） |
| `shipping_templates` / `_region` / `_free` | 5 / 23 / 26 | 不迁 | 运费模板由 B1-04-03 新建模型，需按新结构重配 |
| `article` | 3 | 不迁 | 内容域不在本次交付范围 |
| `express` | 4 | 不迁 | 物流公司字典未接入 |
| `system_config` / `system_group_data` | 52 / 46 | 不迁 | 含历史业务开关与敏感键，白名单迁移未实施 |
| 管理端 28 表（`menu`/`role`/`dept`/`quartz_*`/`gen_*` 等） | — | 不迁 | Go 已有独立 RBAC 与调度模型 |
| `alipay_config` / `email_config` / `qiniu_config` | 1 / 1 / 1 | **强制不迁** | 含第三方私钥/AK/SK/邮箱口令 |

---

## 7. Migration 验证结果

### 7.1 B1-03-04 已交付的验证证据（本次仅核对）

| 项 | 结果 | 证据 |
|---|---|---|
| Migration Up（Schema） | PASS：到 `20261110000014`，`dirty=0` | B1-03-04 §9 |
| 历史数据 Import（首次） | PASS：23 域全部成功，失败 0，无跳过异常 | B1-03-04 §4 |
| 二次执行幂等 | PASS：所有域 `成功=0`、`失败=0`，`legacy_id_map` 仍 411 行，关键表行数不变 | B1-03-04 §7 |
| Dry Run 不写库 | PASS：目标库 `products/users/regions/legacy_id_map` 均为 0 行 | B1-03-04 §7 |
| 数据完整性 | PASS：外键/关联 0 悬空；`spec_signature` 重算 0 不一致；金额等式 0 异常；`Σ wallet.balance = 16,693,420 分`；`Σ points = 3,090` | B1-03-04 §8 |
| 金额等式 | PASS：`original 13,210,500 − discount 200 = total 13,210,300` | B1-03-04 §5.4 |
| 订单状态分布 | PASS：10=14、20=23、30=2、40=3、50=9、60=0、70=0 | B1-03-04 §5.3 |
| 历史数据验证库是否被破坏 | **未破坏**：本任务全程未连接任何数据库，未执行 Down/DROP/清空/测试初始化 | 本报告 §6.1 |

### 7.2 B1-05-02 的 Migration 验证证据

| 期望证据 | 实际获取 | 状态 |
|---|---|---|
| Up 结果（版本、dirty） | 基线中不存在 B1-05-02 交付报告 | **BLOCKED** |
| Down 结果 | 同上 | **BLOCKED** |
| Down 后重新 Up | 同上 | **BLOCKED** |
| 二次执行幂等 | 同上 | **BLOCKED** |
| 数据完整性校验 | 同上 | **BLOCKED** |
| 数据库版本与 dirty 状态 | 同上 | **BLOCKED** |

说明：
- `docs/phase2/` 与 `docs/reports/` 下均无 B1-05-02 交付物（`find docs -name '*B1-05*'` 仅命中 B1-03/B1-04 文档中的引用）。
- 本任务**不重复承担** B1-05-02 的破坏性迁移测试（任务书 §7.4），因此未自行执行 Down/Up。
- 该项是本次验收唯一的硬阻塞：**不得**用 B1-03-04 的幂等证据替代 B1-05-02 的 Up/Down/Up 证据。

### 7.3 本任务实际执行的数据库相关操作

| 操作 | 执行情况 |
|---|---|
| 只读 `SHOW DATABASES` 探测 | 已尝试，连接失败（`ERROR 2002`），未得到任何数据 |
| 任何 Migration Up/Down | 未执行 |
| 任何 `DROP TABLE` / `TRUNCATE` / 清空业务表 | 未执行 |
| 任何测试数据初始化 | 未执行 |

---

## 8. 契约门禁结果

### 8.1 门禁命令与退出码

| # | 命令 | 退出码 | 关键输出 |
|---:|---|---:|---|
| 1 | `go run ./tools/openapi-gen -repo . -check` | **0** | `契约检查通过：210 条常驻 API + 4 条 planned 占位，490 个 Schema，228 个未引用组件（均已在白名单登记）`；`Paths 分域一致性：主文件 171 条路径（planned 4 条）与 167 条分域路径逐字段一致` |
| 2 | `go test ./internal/cmd -run 'TestRouteTable\|TestRouteTableMatchesContract\|TestRouteCountByScope\|TestContractPlannedNotRouted' -count=1` | **0** | `ok cnb.cool/go-cloud-devops/my-shop/internal/cmd 0.093s` |
| 3 | `go test ./tools/openapi-gen -count=1` | **0** | `ok .../tools/openapi-gen 0.263s` |
| 4 | `bash scripts/contract-check.sh` | **0** | 5 步全 PASS：生成一致性、`-check`、路由双向比对、Spectral、生成器单测；`契约检查全部通过（NOT RUN 0 项）` |
| 5 | `go test ./internal/cmd -count=1`（全包，含禁用前缀等约束） | **0** | `ok ... 0.340s` |
| 6 | `bash scripts/test-unit.sh` | **0** | `单元测试全部通过（共 28 组执行单元，均已实际运行，未使用缓存）` |
| 7 | `go build ./...` | **0** | 无输出（成功） |
| 8 | `git diff --check` | **0** | 无输出（无空白符错误） |
| 9 | `git status --short` | **0** | 仅本任务授权文件（见 §11.1） |

### 8.2 门禁覆盖的 12 项检查（任务书 §8.4）

| # | 检查项 | 结果 |
|---:|---|---|
| 1 | 生成前后指纹一致 | PASS（`contract-check.sh` 步骤 1） |
| 2 | OpenAPI 结构合法（3.1.0） | PASS |
| 3 | `$ref` 全部有效（含外部组件文件与 JSON Pointer） | PASS |
| 4 | operationId 唯一 | PASS（214 个） |
| 5 | DTO ↔ Schema 一致 | PASS（深比较 `schemas.yaml`） |
| 6 | 错误码注册表一致 | PASS（153 条） |
| 7 | 权限码三方一致 | PASS（73 个） |
| 8 | planned 正确隔离 | PASS（4 条，未实现、不冲突） |
| 9 | 路由 ↔ OpenAPI 双向一致 | PASS（210/210） |
| 10 | Spectral lint 通过 | PASS（6.17.1，无 error 级问题） |
| 11 | 生成器单测通过 | PASS |
| 12 | NOT RUN = 0 | PASS（脚本汇总 NOT RUN 0 项） |

补充校验（本任务新增，接入 `-check`）：

| 检查 | 结果 |
|---|---|
| `opMetaByKey` 的键必须是真实注册路由（防陈旧元数据） | PASS（删除 `GET /my/commissions` 陈旧条目后 0 项） |
| 请求 DTO 含 `page/size` 必须登记分页模式（双向） | PASS（26 条分页接口全部登记） |

### 8.3 本次未执行的门禁与测试（NOT RUN）

| 项 | 原因 |
|---|---|
| `make test` 完整门禁 | 需 MySQL / Redis / Kafka / kubectl，当前环境无 MySQL |
| `make test-integration`、`test-race`、`test-kafka-e2e`、`test-storage` | 同上 |
| 真实 HTTP 响应冒烟 | 未启动完整应用（依赖数据库与缓存） |
| B1-05-02 的 Migration Up/Down/Up | 证据未提供，且本任务禁止执行破坏性迁移测试 |

> 明确区分：§8.1 的 PASS 是**契约层与静态文档层**的门禁结论；不是真实 HTTP 运行时验证结论。

---

## 9. 已知问题与未决事项

### 9.1 本任务已修复（契约层）

| 编号 | 问题 | 严重程度 | 证据 | 影响最终验收 | 需 Owner 决策 |
|---|---|---|---|---|---|
| N-1 | 4 条严格分页接口误标 `x-pagination-mode: none` | 中 | `wallet.go:475-490`（`normalizePaging` 返回 `CodeInvalidArgument`）、`bargain.go:893-900`（`parsePagination` 同上）vs 修正前 operation 声明 | 已修复，不阻塞 | 否 |
| N-2 | `POST /wallet/recharges` 的 `idempotency_key` 未在契约声明，与 D5 冲突 | 中 | `api/wallet/v1/wallet.go:86`（`v:"required"`）+ 逻辑层 21008 幂等冲突码 | 已修复，不阻塞 | 否 |
| N-3 | 19 处金额字段缺 `x-unit: cent` | 低 | `components/schemas.yaml` 修正前仅 64 处 `x-unit`，而 19 处描述明写"整数分" | 已修复，不阻塞 | 否 |
| N-4 | B1-04 新增域 26 个枚举字段缺 `enum`（违反 D2.3-5） | 中 | 修正前 `schemas.yaml` 仅 44 个 `enum` 字段；新增域状态字段只有文字描述 | 已修复，不阻塞 | 否（属落实已批准的 D2） |
| N-5 | 生成器统计与注释漂移（`142`/`184`/`68`/`2`/`8`、钱包条目重复导致明细和 213≠210、`GET /my/commissions` 陈旧元数据） | 低 | `gen.go`、`components.go`、`unused-components-allowlist.yaml`、`routes_test.go` 的分组注释 | 已修复，不阻塞 | 否 |
| N-6 | 门禁缺少"元数据漂移"守护 | 低 | 由 N-1/N-5 暴露的根因 | 已修复（新增 2 条 `-check` 校验） | 否 |
| N-7 | 2 处 Unix 秒字段缺 `x-unit: unix-second` | 低 | `api/iam/v1/iam.go:69`、`api/upload/v1/upload.go:31` 描述写"unix 秒"但无单位标注 | 已修复，不阻塞 | 否 |

### 9.2 继承的已知问题（本任务不修改生产代码）

| 编号 | 问题 | 严重程度 | 证据 | 影响最终验收 | 需 Owner 决策 |
|---|---|---|---|---|---|
| O-1 | D1 未修复：DTO `v:"required"` 校验失败实际返回 `HTTP 500 + code 51`，与目标 `400 + 1001` 不一致 | 高 | `internal/middleware/response.go`、`internal/codes/codes.go`（码 51 不在码表）；契约中 **28 条** operation 以 `x-known-issue` + 双状态声明如实记录 | 契约已如实描述，不阻塞契约验收；但属产品可见缺陷 | **是**：建议单独立项在 `codes.FromError` 归一 gvalid |
| O-2 | D4 现状未改变：响应 `gtime.Time` 无时区偏移（`YYYY-MM-DD HH:MM:SS`） | 中 | `gtime.Time.MarshalJSON → String()`；契约以 `x-format: gtime-local` + pattern 如实记录 | 不阻塞（已如实描述） | **是**：改为 RFC3339 属破坏性变更，需版本化 |
| O-3 | DTO 文档声明了代码未实现的枚举取值：`MemberGrowthLog.change_type` 的 `admin_adjust`、`biz_type` 的 `manual`、`DistributionCommission.reversal_reason` 的 `cancel` | 低 | `api/member/v1/member.go:52-53`、`api/distribution/v1/distribution.go:38` vs `logic/member`、`logic/distribution` 仅写入 order_reward/refund_deduct/refund | 不阻塞；为避免虚构，本任务**未**为这 3 个字段登记 `enum`（仅保留文字描述） | **是**：确认取值为"保留未实现"还是"从文档删除" |
| O-4 | 4 条 planned 接口仍未实现（秒杀公开列表/详情、可领取券列表、订单可用券） | 中 | `x-planned-apis`、`TestContractPlannedNotRouted` | 不阻塞（已隔离且标注 planned） | 否（属后续需求） |
| O-5 | `x-error-codes` 是静态调用链上界，非运行时穷举；跨包调用不追踪 | 低 | `x-error-codes.scope: static-call-chain` + note | 不阻塞 | 否 |
| O-6 | 无真实 HTTP 运行时验证（未启动完整应用） | 中 | §8.3 | 使"运行时行为"类结论停留在静态证据 | 否（环境限制，已如实标注） |
| O-7 | `RefundAudit.action` 有 `enum` 但无 `x-enum-labels` | 低 | `components/schemas.yaml` | 不阻塞（D2.3 仅要求整数枚举带标签） | 否 |
| O-8 | RFC3339 请求参数声明为 `type: string` + 描述，未声明 `format: date-time`（D4.3 目标表达） | 低 | 主文件 `name: start_time` 参数（后台订单/大屏等）；`grep -c 'format: date-time'` 全文件 = 0 | 不阻塞：实际校验为 `time.Parse(time.RFC3339)`，描述已说明"必须含偏移" | **是**：如需机器可读格式需为查询参数引入格式登记机制（超出本任务范围） |

### 9.3 阻塞项

| 编号 | 阻塞项 | 严重程度 | 证据 | 影响最终验收 | 需 Owner 决策 |
|---|---|---|---|---|---|
| B-1 | **B1-05-02 的 Migration 验证证据缺失**（Up / Down / Up / 二次幂等 / 完整性 / dirty 状态） | 高 | 仓库内无 B1-05-02 交付报告；本任务不得代为执行破坏性测试 | **阻塞 §7.2**，导致"Migration 验证结果"无法核对 | **是**：需 B1-05-02 交付材料 |
| B-2 | B1-05-03 的验收报告缺失 | 中 | `docs/phase2/`、`docs/reports/` 无对应文件 | 阻塞 B1-05 整体验收材料完整性 | **是** |
| B-3 | 历史数据验证库 `my_shop_b10304_verify` 在本环境不可访问 | 中 | `ERROR 2002`；`tmp/shopping.sql` 不在工作区 | 使 §6 的"实时行数复核"只能标 NOT RUN | 否（安全限制，禁止猜测数据） |
| B-4 | B1-05-01 交付报告未随提交入库（仅提交了测试代码） | 低 | `d1fa065` 仅含 3 个测试/脚本文件 | 不影响契约验收，影响整体材料完整性 | 建议补齐 |

---

## 10. 最终结论

### 10.1 分项结论

| 验收项 | 结论 | 依据 |
|---|---|---|
| A1 全部实际 API 路由得到核对 | **PASS** | 210/210 双向一致（§4.1） |
| A2 B1-04 新增 API 全部纳入 | **PASS** | 68/68 有 OpenAPI operation 与 Schema（§5） |
| A3 路由统计与真实路由一致 | **PASS** | 五方一致（§3.2） |
| A4 DTO 与 Schema 一致 | **PASS** | 深比较通过（§4.2） |
| A5 错误码与实际码表一致 | **PASS** | 153/153（§4.4） |
| A6 认证与权限描述准确 | **PASS** | 98 权限码 + 认证分布（§4.6） |
| A7 金额/枚举/分页/时间/幂等语义准确 | **PASS**（修正 N-1～N-4、N-7 后） | §4.7–§4.10、§4.12 |
| A8 Planned API 未被伪装成已实现 | **PASS** | §4.11 |
| A9 OpenAPI 由生成器重新生成 | **PASS** | 生成器执行 + 指纹一致 |
| A10 主文件与分域文件一致 | **PASS** | `-check` 逐字段一致（167 条路径） |
| B1 历史迁移映射矩阵完整 | **PASS（静态证据）** | 23 域 + 未迁移对象（§6） |
| B2 源记录数有实际证据 | **PASS** | B1-03-04 §3 真实 SQL 统计 |
| B3 导入行数有实际证据 | **PASS** | B1-03-04 §4 工具真实输出 |
| B4 目标行数有实际证据 | **PASS** | B1-03-04 §4 目标库行数 |
| B5 差异均有解释 | **PASS** | §6.2 差异列 + §6.3 |
| B6 B1-05-02 Up/Down/Up 结果核对 | **BLOCKED** | 证据缺失（§7.2） |
| B7 历史数据验证库未被破坏 | **PASS** | 未连接、未执行任何写操作（§7.3） |
| C1 `openapi-gen -check` | **PASS** | exit 0 |
| C2 路由测试 | **PASS** | exit 0 |
| C3 生成器单测 | **PASS** | exit 0 |
| C4 `contract-check.sh` | **PASS** | exit 0，NOT RUN 0 |
| C5 NOT RUN 为 0（契约门禁内） | **PASS** | 脚本汇总 |
| D1 最终报告完成 | **PASS** | 本文件 |
| D2 无未登记契约偏差 | **PASS** | N-1～N-7 已登记并修复；O-1～O-8、B-1～B-4 已登记 |
| D3 未解决问题均有记录 | **PASS** | §9 |
| D4 测试结论均有证据 | **PASS** | §8.1 退出码与输出 |
| D5 `git diff --check` | **PASS** | exit 0 |
| D6 修改均在独立任务分支 | **PASS** | `feat/b1-05-04-openapi-report` |
| D7 已提交 Git Commit | **PASS** | 见 §11.3 |
| D8 未擅自合并或推送 | **PASS** | 未 push、未 merge |

### 10.2 明确区分四种状态

- **PASS**：契约结构、路由/DTO/错误码/权限/枚举/金额/分页/幂等/planned 的全部**静态**核对，以及契约门禁 5 步。
- **FAIL**：无。本任务未发现仍存在的契约与实现不一致（发现的 6 类偏差均已修复并重新生成）。
- **BLOCKED**：B1-05-02 的 Migration Up/Down/Up 证据核对；B1-05-03 材料缺失。
- **NOT RUN**：`make test` 全量、集成/race/kafka E2E、真实 HTTP 冒烟、数据库实时行数复核、任何破坏性迁移操作。

### 10.3 是否具备合并条件

| 判断 | 结论 |
|---|---|
| 本任务分支自身可否合并 | **可以**（契约门禁全绿、无生产代码改动、无未登记偏差） |
| 是否可宣布 B1-05 整体验收通过 | **不可以**（B-1/B-2 阻塞） |
| 建议顺序 | 先取得 B1-05-02/03 交付材料并补入本报告 §7.2，再由 Owner 统一安排四个 B1-05 子任务的合并与联合验收 |

---

## 11. 交付清单

### 11.1 修改文件（全部在授权边界内）

| 文件 | 变更性质 | 说明 |
|---|---|---|
| `tools/openapi-gen/metadata.go` | 元数据 | 新增 26 条枚举登记；`moneyExactFields` 新增 12 个金额字段名；新增 `unixSecondFields`（2 个字段） |
| `tools/openapi-gen/build_schema.go` | 生成逻辑 | 为 Unix 秒字段输出 `x-unit: unix-second`（与金额单位互斥） |
| `tools/openapi-gen/opmeta.go` | 元数据 | 补 4 条 strict 分页；补 `POST /wallet/recharges` 幂等声明；删除陈旧 `GET /my/commissions` |
| `tools/openapi-gen/check.go` | 校验 | 新增 opMeta 键存在性校验与"DTO 有 page/size 必须登记分页"校验（含 `requestHasPagingFields`） |
| `tools/openapi-gen/gen.go` | 统计注释 | 修正新增 API 台账（去重复计、明确合计 210）；`142→210` |
| `tools/openapi-gen/components.go` | 统计注释 | `142→210` |
| `tools/openapi-gen/unused-components-allowlist.yaml` | 统计注释 | `184→210`（条目本身未变，仍 228 条） |
| `internal/cmd/routes_test.go` | 注释 | 修正 3 处分组条数（68→75、2→4、8→6）；断言值未变（仍 210/98/110/2） |

### 11.2 生成文件（仅由生成器产出，无手工编辑）

| 文件 | 变更 |
|---|---|
| `openapi/surgecart.openapi.yaml` | +4 条 strict 分页声明、+1 条幂等声明、+26 处 enum、+28 处参数级 `x-pagination-mode`/`enum` |
| `openapi/components/schemas.yaml` | +26 处 `enum`/`x-enum-labels`、+19 处 `x-unit: cent` |
| `openapi/paths/bargain.yaml`、`wallet.yaml`、`groupbuy.yaml`、`member.yaml`、`freight.yaml`、`order.yaml`、`flashsale.yaml` | 与主文件同源的分域视图同步 |
| `docs/phase2/B1-02_DTO_SCHEMA_INVENTORY.md` | 枚举列、金额列随 Schema 更新（生成器产出） |

### 11.3 Git

| 项 | 值 |
|---|---|
| 分支 | `feat/b1-05-04-openapi-report` |
| 起点 SHA | `d1fa065a5620f7430596de236a60c0613aaac557` |
| 提交信息 | `test(contract): finalize B1-05-04 OpenAPI audit` |
| 提交 SHA | 见提交后回填（本报告 §11.3 的 SHA 由提交动作决定，避免自引用） |
| Push / Merge | 均未执行 |

### 11.4 测试结果汇总

| 命令 | 结果 |
|---|---|
| `go build ./...` | PASS（exit 0） |
| `go run ./tools/openapi-gen -repo . -check` | PASS（exit 0） |
| `go test ./internal/cmd -count=1` | PASS（exit 0） |
| `go test ./tools/openapi-gen -count=1` | PASS（exit 0） |
| `bash scripts/contract-check.sh` | PASS（exit 0，NOT RUN 0） |
| `bash scripts/test-unit.sh` | PASS（28 组，exit 0） |
| `git diff --check` | PASS（exit 0） |
| `make test` / 集成 / race / Kafka E2E / 真实 HTTP 冒烟 | **NOT RUN** |

### 11.5 遗留问题（移交 Owner）

1. **B-1（阻塞）**：B1-05-02 的 Migration Up/Down/Up 与二次幂等证据未提供，§7.2 无法核对。
2. **B-2**：B1-05-03 验收报告缺失。
3. **B-4**：B1-05-01 报告未入库。
4. **O-1**：gvalid → `500/51` 的 D1 缺陷仍存在于运行时（契约已如实标注 28 条）。
5. **O-2**：响应时间仍无时区偏移（D4 现状）。
6. **O-3**：3 个 DTO 字段声明了未实现的枚举取值，需产品确认。
7. **O-6**：真实 HTTP 运行时验证未执行（环境缺少 MySQL/Redis）。
8. **O-8**：RFC3339 查询参数未声明 `format: date-time`（当前以描述表达），如需机器可读格式需引入参数级格式登记。
9. **B-3**：历史验证库不可访问，§6 目标行数依赖 B1-03-04 已验收报告，未做实时复核。

---

## 附录 A：关键命令与原始输出摘录

```text
$ git rev-parse --show-toplevel
/root/projects/my-shop
$ git branch --show-current
feat/p2-backend
$ git rev-list --left-right --count feat/p2-backend...origin/feat/p2-backend
0	35
$ git merge --ff-only origin/feat/p2-backend
$ git rev-parse HEAD
d1fa065a5620f7430596de236a60c0613aaac557
```

```text
$ go run ./tools/openapi-gen -repo . -check
契约检查通过：210 条常驻 API + 4 条 planned 占位，490 个 Schema，228 个未引用组件（均已在白名单登记）
Paths 分域一致性：主文件 171 条路径（planned 4 条）与 167 条分域路径逐字段一致
```

```text
$ bash scripts/contract-check.sh
== 1. 生成结果一致性 ==            PASS  生成器输出与仓库中的契约文件完全一致（无漂移）
== 2. 契约结构与一致性 ==          PASS  （6 项）
== 3. 路由 ↔ OpenAPI 双向比对 ==   PASS  210 条常驻路由与契约双向一致；planned 接口未被实现
== 4. Spectral lint ==             PASS  Spectral 无 error 级问题
== 5. 生成器单元测试 ==            PASS
契约检查全部通过（NOT RUN 0 项）
```

```text
$ mysql -h127.0.0.1 -uroot -proot -e "SHOW DATABASES"
ERROR 2002 (HY000): Can't connect to server on '127.0.0.1' (115)
```

## 附录 B：数据安全声明

- 本任务未连接任何业务数据库，未读取、未修改、未导出任何历史业务数据。
- 未在 `my_shop_b10304_verify` 或任何承载历史迁移数据的库上执行 Migration Down、`DROP TABLE`、清空业务表或测试数据初始化。
- 未读取或输出任何用户密码哈希、第三方凭据或密钥。
- 本报告中所有行数均引用 B1-03-04 已验收报告或仓库内数据文件的静态计算（地区 3938 行已用 `regions_v1.tsv` 独立复核）。
