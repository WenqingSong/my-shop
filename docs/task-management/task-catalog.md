# Task 目录（51 项）

- 快照日期：2026-10-18
- 状态口径与权威边界见 [README.md](README.md#3-状态词汇本目录专用)。
- 证据列中的路径均相对仓库根；完整命令输出见 [治理报告](../reports/2026-10-18-agent-task-governance.md)。
- **不得**根据目录名或本表推断未列出的状态；State 缺失或不可解析时以表内 `UNKNOWN` / `DONE/DOC` / `INCONSISTENT` 为准。

## 1. 活动 / 待处理（11）

这 11 项当前仍有未完成动作或无法核验，优先查看。

| Task ID | 领域 | 状态 | 关系 | 主要交付 | 需要处理 | 证据 |
|---|---|---|---|---|---|---|
| `ci-image-publish-v1` | 工程化 / CI-CD | ACTIVE | — | main 更新后自动测试、Docker 构建与镜像发布；保留既有流水线 | 是：Owner 决定 main 集成后的发布验收（4 项 AC 未执行） | `state.yaml`（delivery=BLOCKED）、`delivery.md` |
| `test-quality-gate-v1` | 工程化 / 质量门禁 | ACTIVE | — | 自动化测试与质量门禁（vet / test / race / registry） | 是：Owner 接受或拒绝；当前无 ACCEPT | `state.yaml`（owner=PENDING）、`owner-decision.md` 占位、`delivery.md` |
| `flash-sale-v1` | 秒杀 | INCONSISTENT | 演进链首版 → `flash-sale-v2` | 单体 MySQL 秒杀闭环（一人一单、幂等、条件扣减） | 是：只读归档或 T1；State 与 `delivery.md` 矛盾 | `state.yaml`（schema 0）、`delivery.md`（Result=PASS） |
| `flash-sale-v3` | 秒杀 | INCONSISTENT | v2 → **v3** → v4 | Redis 闸门 + MySQL 出队表异步下单 | 是：Owner 决定是否受控合并 `a905e1c` | `develop` State（delivery=NOT_RUN）、`origin/feat/flash-sale-v3` |
| `product-review-v1` | 商品 / 评价 | INCONSISTENT | 业务前置：订单闭环 | 已购商品评价、评分汇总、公开列表 | 是：只读归档或 T1；State 与 `delivery.md` 矛盾 | `state.yaml`（schema 0）、`delivery.md`（Result=PASS） |
| `admin-identity-rbac` | 身份与权限 | INCONSISTENT | 业务关联：`identity-domain-isolation`；`iam-v5` 复用其 RBAC | 管理员登录/CRUD、角色权限、超管、401/403 | 是：只读归档或 T1；交付快照基于未提交工作区 | `delivery.md`、`f5d481a` |
| `agent-workflow-owner-confirmation` | Workflow 治理 | UNKNOWN | 同域：`agent-workflow-state-machine-v1` | Owner 确认与里程碑的机读状态 | 是：归档或 T1；无交付验收 | `delivery.md` 占位 |
| `agent-workflow-state-machine-v1` | Workflow 治理 | UNKNOWN | 同域：`agent-workflow-owner-confirmation` | Agent Workflow 状态机与 Validator 契约 | 是：归档或 T1；无交付验收 | `delivery.md` 占位 |
| `categories-v1` | 商品 / 分类 | UNKNOWN | 业务关联：`product-spu-v1` | 3 级分类树、公开查询、受保护写 | 是：归档或 T1；代码存在仅为线索，不能据此认定完成 | `task.md`、`delivery.md` 占位 |
| `error-code-namespace-expansion` | 基础设施 / 错误码 | UNKNOWN | 业务关联：`.agent/registry/error-codes.md` | 错误码 Namespace 扩展规则 | 是：归档或 T1；无交付验收 | `delivery.md` 占位 |
| `global-resource-reservation` | Workflow 治理 / 资源 | UNKNOWN | 业务关联：migration / 错误码 Registry | 跨任务全局唯一资源预留机制 | 是：归档或 T1；无交付验收 | `delivery.md` 占位 |

## 2. 已完成（40）

`DONE/STATE` = State `delivery=PASS` 且 `feature_head` 已在 `develop` 历史中；`DONE/DOC` = 无 State 或 schema 0，但 `delivery.md` 有 PASS 证据且实现已进入 `develop`。

| Task ID | 领域 | 状态 | 关系 | 主要交付 | 证据 |
|---|---|---|---|---|---|
| `admin-order-management` | 后台管理 / 订单 | DONE/STATE | 前置：`order-v1`（业务） | 后台订单分页、筛选、详情（只读） | `state.yaml` |
| `article-cms-v1` | 运营内容 / 文章 | DONE/STATE | 业务关联：`iam-v5` | 文章 CRUD、点赞、收藏 | `state.yaml` |
| `backend-config-hardening-v1` | 基础设施 / 配置 | DONE/STATE | 业务关联：`dev-environment-setup` | 强制凭据、utf8mb4 统一 | `state.yaml` |
| `backend-container-v1` | 基础设施 / 容器 | DONE/STATE | 演进起点 → `container-runtime-hardening-v1` | Compose 容器化、统一启动 | `state.yaml` |
| `banner-v1` | 运营内容 / 轮播图 | DONE/STATE | — | 轮播图 CRUD、公开列表、本地图片 | `state.yaml` |
| `cart-v1` | 交易 / 购物车 | DONE/DOC | 前置：`sku-v1`；被 `order-v1` 依赖 | 购物车 CRUD、勾选、异常状态 | `delivery.md` |
| `container-runtime-hardening-v1` | 基础设施 / 容器 | DONE/STATE | 演进：`backend-container-v1` → 本任务 | 非 root、liveness/readiness、优雅退出 | `state.yaml` |
| `coupon-v1` | 交易 / 优惠券 | DONE/STATE | 前置：`order-v1` | 模板、领取、预占、核销、释放 | `state.yaml` |
| `dashboard-v1` | 后台管理 / 运营大屏 | DONE/STATE | 业务关联：`order-v1`、`flash-sale-v5` | 运营概览、趋势、排行、秒杀统计 | `state.yaml` |
| `db-migration` | 基础设施 / 数据库 | DONE/DOC | 前置：全部业务表；关联 `legacy-data-migration` | golang-migrate 版本化迁移 | `delivery.md`、合并 `c1598e3` |
| `design-debt-backfill` | 文档治理 | DONE/DOC | 业务关联：`design-doc-governance` | 补 db / SPU / SKU / Inventory 长期 Design | `delivery.md` |
| `design-doc-governance` | 文档治理 | DONE/DOC | 业务关联：`design-debt-backfill` | Design Impact 与四者一致治理机制 | `delivery.md` |
| `dev-environment-setup` | 基础设施 / 开发环境 | DONE/STATE | 业务关联：`backend-config-hardening-v1` | make init/up、.env 一致性 | `state.yaml` |
| `flash-sale-v2` | 秒杀 | DONE/STATE | 演进：v1 → **v2** → v3 | Redis + Lua 闸门、预热、对账 | `state.yaml` |
| `flash-sale-v4` | 秒杀 | DONE/STATE | 演进：v3 → **v4** → v5 | 故障恢复、补偿、对账、人工修复审计 | `state.yaml` |
| `flash-sale-v5` | 秒杀 | DONE/STATE | 演进：v4 → **v5** | 限流、排队上限、熔断、指标 | `state.yaml` |
| `flashsale-p95-p99-query` | 可观测性 | DONE/STATE | 业务关联：`prometheus-integration`、`verify-metrics-histogram` | p95 / p99 recording rules | `state.yaml` |
| `flashsale-performance-baseline` | 秒杀 / 性能 | DONE/STATE | 前置：`flashsale-v5-loadtest` | 可复现 JSON 压测基线 | `state.yaml` |
| `flashsale-v5-loadtest` | 秒杀 / 性能 | DONE/STATE | 前置：`flash-sale-v5` | 低/中/高三级压测报告 | `state.yaml` |
| `iam-v2` | 身份与权限 | DONE/DOC | 演进起点 → v3 | Redis 有状态会话、logout | `delivery.md` |
| `iam-v3` | 身份与权限 | DONE/DOC | 演进：v2 → **v3** → v4 | 多设备会话查看与撤销 | `delivery.md`、合并 `d0c4bb2` |
| `iam-v4` | 身份与权限 | DONE/DOC | 演进：v3 → **v4** → v5 | Refresh Token 轮换、Token Family、重放检测 | `delivery.md`、合并 `9836f5e` |
| `iam-v5` | 身份与权限 | DONE/STATE | 演进：v4 → **v5** | 用户禁用/启用、Token 失效、审计 | `state.yaml` |
| `identity-domain-isolation` | 身份与权限 | DONE/DOC | 业务关联：`admin-identity-rbac`、`iam-v2..v5` | 前后台身份域隔离与集成测试 | `delivery.md` |
| `inventory-v1` | 商品 / 库存 | DONE/DOC | 前置：`sku-v1` | 条件扣减、防负库存、库存流水 | `delivery.md` |
| `legacy-data-migration` | 数据迁移 | DONE/STATE | 业务关联：`db-migration` | `shopping.sql` 导入隔离测试库 | `state.yaml` |
| `nginx-reverse-proxy-v1` | 基础设施 / 网关 | DONE/STATE | 前置（事实）：`backend-container-v1` | Nginx 唯一入口、超时与体积限制 | `state.yaml` |
| `object-storage-upload` | 基础设施 / 对象存储 | DONE/STATE | 业务关联：Admin RBAC | 七牛直传凭证、类型与大小约束 | `state.yaml` |
| `order-refund-v1` | 交易 / 退款 | DONE/STATE | 前置：`order-v1` | 仅退款申请、审核、执行、审计 | `state.yaml` |
| `order-v1` | 交易 / 订单 | DONE/DOC | 前置：`cart-v1`、`inventory-v1`、`shipping-address-v1`；被 `coupon-v1` / `order-refund-v1` / `admin-order-management` 依赖 | 订单状态机、幂等、事务扣库存 | `delivery.md` |
| `product-favorite-v1` | 商品 / 互动 | DONE/DOC | 业务关联：`product-like-v1`、`product-view-count-v1` | 收藏、取消、列表、状态 | `delivery.md`（State schema 0） |
| `product-like-v1` | 商品 / 互动 | DONE/STATE | 业务关联：`product-favorite-v1`、`product-view-count-v1` | 点赞、取消、计数 | `state.yaml` |
| `product-spu-v1` | 商品 / SPU | DONE/DOC | 前置：被 `sku-v1` 依赖 | SPU CRUD、搜索、上下架、RBAC | `delivery.md` |
| `product-view-count-v1` | 商品 / 互动 | DONE/STATE | 业务关联：`product-like-v1`、`product-favorite-v1` | 浏览量累计与展示 | `state.yaml` |
| `project-baseline-cleanup-v1` | 工程化 / 基线 | DONE/STATE | — | 配置、文档、Registry 一致性 | `state.yaml` |
| `prometheus-integration` | 可观测性 | DONE/STATE | 业务关联：`verify-metrics-histogram` | Prometheus 抓取 `/metrics` | `state.yaml` |
| `recommendation-v1` | 运营内容 / 推荐位 | DONE/STATE | — | 推荐位 CRUD、排序、前台查询 | `state.yaml`；SHA 不可追溯，见 [owner-decisions](owner-decisions.md#5-recommendation-v1-的-sha-不可追溯) |
| `shipping-address-v1` | 用户 / 地址 | DONE/DOC | 前置：被 `order-v1` 依赖 | 地址 CRUD、默认地址、用户隔离 | `delivery.md` |
| `sku-v1` | 商品 / SKU | DONE/DOC | 前置：`product-spu-v1`；被 `inventory-v1` 依赖 | SKU 1:N、价格、状态、RBAC | `delivery.md` |
| `verify-metrics-histogram` | 可观测性 | DONE/STATE | 业务关联：`prometheus-integration` | Histogram 标签与分桶修正 | `state.yaml` |

## 3. 按领域索引

| 领域 | Task |
|---|---|
| 秒杀 | `flash-sale-v1..v5`、`flashsale-p95-p99-query`、`flashsale-performance-baseline`、`flashsale-v5-loadtest` |
| 身份与权限 | `iam-v2..v5`、`identity-domain-isolation`、`admin-identity-rbac` |
| 商品 | `product-spu-v1`、`sku-v1`、`categories-v1`、`inventory-v1`、`product-like-v1`、`product-favorite-v1`、`product-review-v1`、`product-view-count-v1` |
| 交易 | `cart-v1`、`order-v1`、`coupon-v1`、`order-refund-v1`、`shipping-address-v1` |
| 运营内容 | `banner-v1`、`article-cms-v1`、`recommendation-v1`、`dashboard-v1` |
| 基础设施 | `db-migration`、`backend-config-hardening-v1`、`backend-container-v1`、`container-runtime-hardening-v1`、`dev-environment-setup`、`nginx-reverse-proxy-v1`、`object-storage-upload`、`ci-image-publish-v1`、`legacy-data-migration` |
| 可观测性与质量 | `prometheus-integration`、`verify-metrics-histogram`、`test-quality-gate-v1` |
| Workflow / 文档治理 | `agent-workflow-owner-confirmation`、`agent-workflow-state-machine-v1`、`global-resource-reservation`、`error-code-namespace-expansion`、`design-debt-backfill`、`design-doc-governance`、`project-baseline-cleanup-v1` |

## 4. 数据来源说明

- 分类、状态、关系、交付摘要来自 `.agent/tasks/*` 的 `task.md` / `state.yaml` / `delivery.md`、官方索引和 Git 历史，详见治理报告。
- 无 State 的 19 项不写 `DONE/STATE`；其中 14 项因 `delivery.md` 的 `Result=PASS` 与合并事实标为 `DONE/DOC`，另 5 项标 `UNKNOWN`。
- `flash-sale-v1` 与 `product-review-v1` 的 State 为 schema 0 且与 `delivery.md` 矛盾，标 `INCONSISTENT`，不补写。
- `recommendation-v1` 标 `DONE/STATE`（内容已交付），但 Evidence SHA 不可追溯，处理见 [owner-decisions](owner-decisions.md)。
- 本表不包含 `docs/tasks/` 下的早期非 Engine 记录（`iam-v1`、`make-lifecycle`、`project-init`）。
