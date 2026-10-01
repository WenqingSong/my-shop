# Delivery Verification

## Milestone and Target

- Milestone：SKU / 商品规格 V1（`sku-v1`）最终交付验收
- Delivery Target：HEAD `ba85057`（`docs(sku): 补充核心逻辑说明与 Cleaner 审查结论`）
- Cleaner Review Target：`c9388e2`（`feat(sku): 新增 SKU 接口与商品详情组合`）
- Target Match：YES
  - `ba85057` 相对 `c9388e2` 仅新增 `.agent/tasks/sku-v1/core-logic.md` 与 `.agent/tasks/sku-v1/findings.md` 两个文档文件（+121/-3），生产代码、测试、配置、迁移无任何差异。
  - 工作区干净（`git status --short` 为空），无未提交修改、无本机绝对路径依赖。

## Environment

- OS：Linux（amd64）
- 语言运行时：Go 1.24.1
- MySQL：8.0（Docker 容器 `my-shop-mysql`，healthy）
- Redis：7-alpine（Docker 容器 `my-shop-redis`，healthy）
- 编排：Docker Compose（`docker-compose.yml`）
- MQ：无（本任务不含 MQ，符合 Contract）
- 配置来源：`manifest/config/config.yaml` + 环境变量覆盖（数据库 `root@127.0.0.1:3306/my_shop`、Redis `127.0.0.1:6379`）；超级管理员初始密码经环境变量 `ADMIN_SUPER_PASSWORD` 注入（不记录实际值），JWT 密钥使用开发默认值（不记录）。
- 测试数据与清理：Smoke 数据使用 `dv-smoke-` 前缀命名，验收结束后经 SQL 删除（`skus`/`product_images`/`products`/`categories`），最终核对三者计数均为 0；服务进程已优雅关闭。

## Verification

| Check | Result | Evidence |
|---|---|---|
| Build | PASS | `go build ./...` exit 0；`go build -o /tmp/my-shop .` 成功产出可运行二进制，并成功启动真实 HTTP 服务。 |
| go vet | PASS | `go vet ./...` exit 0，无告警。 |
| gofmt | PASS | `gofmt -l .` 无输出（无未格式化文件）。 |
| Unit/Integration Test | PASS | `go test ./... -p 1 -count=1` 全量串行通过，含 `internal/controller/sku`（5.1s）、`internal/migrations`（8.6s）、`internal/boot`（3.2s）、`internal/cmd`、`internal/controller/admin|categories|iam|product` 等。 |
| Migration DDL | PASS | `SHOW CREATE TABLE skus` 核对：`id BIGINT UNSIGNED AUTO_INCREMENT` 主键、`product_id BIGINT UNSIGNED NOT NULL`、`price INT UNSIGNED NOT NULL`、`status TINYINT NOT NULL DEFAULT 1`、`UNIQUE KEY uk_product_name (product_id, name)`、`KEY idx_product_id`、FK `fk_skus_product ... ON DELETE RESTRICT`、无 `stock` 列、`ENGINE=InnoDB utf8mb4`。`schema_migrations` 版本 `20261001000003`、`dirty=0`。 |
| 服务启动 + 健康检查 | PASS | 启动后 `/health` 返回 `{"code":0,"message":"OK","data":{"status":"ok",...}}`；启动依赖校验（MySQL/Redis）通过。 |
| Core Flow（HTTP Smoke） | PASS | 见 Acceptance Evidence，主链路经真实 HTTP 请求与最终查库核对。 |
| Data 一致性 | PASS | 更新后查库 `price=459900, status=0` 与响应一致；删除后 `skus` 行消失；前台/后台详情 SKU 数量与可见性符合预期。 |

## Acceptance Evidence

本次运行（Deliverer 独立执行，非引用 Coder/Cleaner 报告）的关键 AC/INV 结果：

- AC-001 / INV-001（归属正确）：`POST /admin/skus` 合法创建成功，`product_id=1` 归属正确，落库核对 `name/price/status` 一致。
- AC-002 / INV-002（价格整数分）：创建 `price=-1` → `400/5002`，无写入；合法 `499900` 整数分存储与出参正确。
- AC-004（商品不存在拒绝）：集成测试 `TestSkuCreateAndValidation` 覆盖 `product_id` 不存在 → `404/4001` 且无写入（本次 `go test` 通过）。
- AC-005（更新）：`PUT /admin/skus/1` 改 `price=459900, status=disabled` 成功，查库反映新值；更新不存在 → `404/5001`。
- AC-006（删除）：`DELETE /admin/skus/2` → 200，重复删除 → `404/5001`。
- AC-007（删除不影响 SPU）：集成测试 `TestSkuDelete` 覆盖（本次通过）；Smoke 删除后商品仍在。
- AC-008（一对多/归属隔离）：集成测试 `TestSkuOneToManyIsolation` 覆盖（本次通过）。
- AC-009 / INV-007（详情组合 + 可见性）：Smoke 实测——上架商品前台详情 `skus` 仅返回 1 个 `enabled` SKU；后台详情返回全部 2 个（含 `disabled`）；draft 商品前台 404 由集成测试 `TestSkuDetailVisibility` 覆盖（本次通过）。
- AC-010 / INV-004（状态校验）：非法 `status` → `5003`（集成测试覆盖）；默认 `enabled`、显式 `disabled` 均落库正确。
- AC-011 / INV-005（RBAC）：无 token 写 SKU → `401/1002`（Smoke 实测）；无权限管理员 403、前台用户 token 403、超管成功由 `TestSkuAuthorization` 覆盖（本次通过）。
- AC-012 / INV-006（稳定引用键）：DDL 核对 `id` 自增主键；`TestSkuDelete` 覆盖删除后自增不重用（本次通过）。
- INV-008（同商品 name 唯一）：Smoke 实测同商品同名 → `409/5004`；并发兜底由 `TestSkuConcurrentCreateSameName`（10 并发恰好 1 成功 + 9 冲突）覆盖（本次通过）。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| `go test -race`（Race Test） | Contract 未要求；并发正确性已由业务并发测试 `TestSkuConcurrentCreateSameName` 覆盖（唯一约束兜底，业务语义验证优先于内存竞态检测）。 | 低：本任务并发风险是唯一性/归属，非内存共享竞态，已由业务测试闭环。 |

重启/恢复/回滚/性能检查不在本任务 Task/Contract 范围内，未执行、也不作为验收项。

## Remaining Risks

- Cleaner 4 个 P3 观察（`CLEAN-001`~`CLEAN-004`）均 OPEN，非阻塞：迁移结构等价测试未覆盖 `skus` 列级结构、更新撞名无显式测试、详情 `skus[].name/price` 未显式断言、两处过期注释。交由 Owner 决定是否后续处理，Deliverer 不关闭 Finding。
- 物理删除 + 自增不重用：订单模块引入 FK/应用层校验前，历史 SKU 删除后其 `id` 不被重用，已存在订单引用该 SKU 会失去关联。属 deferred requirement，已明确接受（延后订单模块补齐）。
- 库存展示缺口：SKU V1 详情不含库存，前台库存展示需等待 4.3 库存域。属 Owner 已确认的库存解耦取舍。

## Result

PASS
