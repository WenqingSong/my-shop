# Delivery Verification

## Milestone and Target

- Milestone：`product-spu-v1` 最终交付验收（COMPLEX，Contract `APPROVED`，Cleaner 结论 `CLEAN`）。
- Delivery Target：HEAD `8f5ae87`（`test(product): 补齐 update 侧与权限端到端测试`，分支 `feat/spu`，工作区干净）。
- Cleaner Review Target：HEAD `64f3e75` + 工作区未提交 `internal/controller/product/product_test.go`（+199 行）。
- Target Match：YES。Cleaner 复审时未提交的 `product_test.go` 现已在 HEAD `8f5ae87` 提交；生产代码自初审版本 `c51273c` 起未变（`git diff c51273c..HEAD` 仅 `product_test.go` 与两个文档文件，无生产代码改动）。

## Environment

- OS：Linux（CNB DinD）。
- 语言运行时：Go 1.24.1 linux/amd64（module 要求 `go 1.23.0`，满足 `≥1.23`）。
- 依赖：GoFrame v2.10.3、golang-migrate v4.19.0、go-sql-driver/mysql v1.7.1。
- 数据依赖（docker compose 容器，均为 healthy）：MySQL 8.0.46（`mysql:8.0`）、Redis 7（`redis:7-alpine`）。
- 配置来源：`manifest/config/config.yaml` + 环境变量覆盖；数据库 `my_shop`（root/root，开发默认）；冒烟时以环境变量注入 `ADMIN_SUPER_PASSWORD`（值不记录）。
- 数据隔离与清理：验收前将开发库 `my_shop` DROP 后 `CREATE`，再执行全部 migration（开发环境、每日重置、数据可丢弃），随后 `my-shop migrate up` 应用到 `20261001000002`。冒烟结束后已停止服务进程，数据保留在开发库（可丢弃）。

## Verification

| Check | Result | Evidence |
|---|---|---|
| Build（`go build ./...`） | PASS | exit 0，无输出。另 `go build -o bin/my-shop .` 成功。 |
| Vet（`go vet ./...`） | PASS | exit 0，无输出。 |
| 全量测试（`go test -p 1 ./...`） | PASS | 全部包 ok，含 `controller/product` 4.823s、`migrations` 5.950s、`boot` 2.283s。 |
| Race Test（`go test -race -p 1 ./internal/controller/product/`） | PASS | ok，25.426s，无 data race。 |
| Migration（`my-shop migrate up`） | PASS | 干净库应用到 `version: 20261001000002 (dirty=false)`；`products`/`product_images` 已建。 |
| 服务启动与健康检查 | PASS | `my-shop serve` 启动，`GET /health` 返回 `{"code":0,"data":{"status":"ok"}}`；`/products`、`/admin/products` 等路由已注册。 |
| 核心 HTTP 主链路（Smoke） | PASS | 见下「Acceptance Evidence」。 |
| 最终数据与 FK | PASS | `products` 存整数分 `price=1234`、`status` TINYINT；`product_images` 2 行 `sort=0/1`；真实库含 `CONSTRAINT fk_products_category ... ON DELETE RESTRICT`。 |

## Acceptance Evidence

以下为 Deliverer 本次独立运行的 HTTP 冒烟 + 数据库证据（非 Coder/Cleaner 报告）：

| 主链路 / 不变量 | 本次结果 |
|---|---|
| 管理员登录 → Token | `POST /admin/login` 返回 `code:0` + `access_token`（`type=admin`，`expires_in=3600`）。 |
| 建叶子分类 | `POST /categories` → `code:0, id=1`。 |
| 创建商品（强制 draft + 整数分 + 图片） | `POST /admin/products`（price=1234，images 2 张）→ `code:0`，`status="draft"`，`price=1234`。 |
| 前台可见性（draft 不可见） | 上架前 `GET /products` → `total=0`；`GET /products/1` → `code:4001`（404）。 |
| 上架（draft→on_shelf） | `POST /admin/products/1/on-shelf` → `code:0`，`status="on_shelf"`。 |
| 前台可见性（on_shelf 可见） | `GET /products` → `total=1`；`GET /products/1` → `code:0`。后台 `GET /admin/products` 可见全部状态。 |
| 非法迁移（on_shelf→on_shelf） | `POST .../on-shelf` → `code:4005`（409），状态不变。 |
| 下架（on_shelf→off_shelf） | `POST .../off-shelf` → `code:0`，`status="off_shelf"`；前台详情再查 `code:4001`（不可见）。 |
| 分类删除保护（有商品） | `DELETE /categories/1` → `code:3005`（409）。 |
| 关键词搜索 + 转义 | `keyword=iPhone` → `total=1`；`keyword=%`（字面 `%`）→ `total=0`（转义生效）。 |
| 分类精确筛选 | `category_id=1` → `total=1`。 |
| 排序白名单 + size 钳制 | `sort=price&order=asc&size=9999` → `size=100`（钳制到上限）。 |
| 价格非法拒绝（无写入） | `POST /admin/products` price=-1 → `code:4002`（400），查库 `products` 仍为 1 行。 |
| 最终数据 | `products`: 1 行（price=1234、status=2/off_shelf）；`product_images`: 2 行（sort=0/1）。 |

并发迁移（AC-010）、RBAC 401/403/超管放行（AC-019/020/021）、图片事务回滚（AC-018）、keyword 反斜杠转义、上架分类重校验等，由本次独立执行的 `go test -p 1 ./...`（含 `TestProductConcurrentTransition`、`TestProductAuthorization`、`TestProductCreateWithGrantedPermission`、`TestProductUpdate`、`TestProductKeywordEscaping`、`TestProductOnShelfCategoryRevalidation`）与 `-race` 通过作为证据。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| `go test ./...`（不加 `-p 1`） | 项目既有环境特性：跨包共享同一 MySQL 清表竞争导致 flaky，官方入口为 `scripts/test.sh`（`-p 1` 串行）。 | 无（非本任务引入，交付前已有并记录）。 |
| 真实 HTTP 并发压力/性能（QPS/P95） | Task/Contract 未要求性能指标；AC-010 并发正确性已由 `TestProductConcurrentTransition` + `-race` 覆盖。 | 无。 |
| 重启/恢复/回滚演练 | Task/Contract 未要求。 | 无。 |
| 并发「创建商品 + 删分类」FK 1451 兜底路径 | 应用层 3005 已实测；FK `ON DELETE RESTRICT` 已确认存在于真实库，1451→409 映射经代码审查（`isForeignKeyError`）确认，未做确定性并发注入。 | 低（非核心主链路，FK 兜底为最终一致性，现有 `TestCategoryDeleteProtection` 覆盖应用层路径）。 |

## Remaining Risks

- `product-spu-v1/task.md` 仍为旧状态（`READY_FOR_ANALYST`、「SPU 暂停」等描述），与「已实现 + Contract APPROVED」现状不一致。该同步项在 `contract.md` 待办中标记为 Task Builder 未完成，属文档卫生问题，不影响技术交付正确性（AC/INV 均以 Contract 与已实现代码为准）。
- 开发库在验收前被 DROP 重建以取得干净证据；数据为可丢弃的开发数据，不影响结论。

## Result

PASS

所有关键交付检查（构建、静态检查、全量测试、竞态检测、迁移、服务启动与健康、核心 HTTP 主链路、最终数据与 FK）均有本次独立运行证据并通过，无已知阻塞问题。
