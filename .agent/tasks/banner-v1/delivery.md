# Delivery Verification

## Milestone and Target
- Milestone：banner-v1 轮播图 V1 核心闭环（公开列表 + 后台管理 + 本地图片存储）
- Delivery Target：`27db2fc38beb7505dd1528ef083080c19e2592c0`（Cleaner CLEAN 的 immutable implementation Evidence Commit C1）
- Cleaner Review Target：`27db2fc38beb7505dd1528ef083080c19e2592c0`
- Target Match：YES

## Environment
- OS：Debian GNU/Linux 12 (bookworm)，内核 5.4.241-1-tlinux4，x86_64
- 语言运行时：Go 1.24.1
- Docker 29.6.2；Docker Compose v5.3.1
- MySQL：8.0.46（容器 `my-shop-mysql`，image `mysql:8.0`，healthcheck healthy）
- Redis：7（容器 `my-shop-redis`，image `redis:7-alpine`；redis-cli 7.4.11，healthcheck healthy）
- 服务：`bin/my-shop serve`（`migrate up` 后），监听 `:8000`
- 配置来源：`manifest/config/config.yaml` 开发默认值 + 环境变量覆盖（本次验收注入 `ADMIN_SUPER_PASSWORD`、`STORAGE_LOCAL_ROOT`）；未记录任何 Secret 值
- 隔离与清理：本地存储使用临时目录 `/tmp/banner-delivery-storage`（验证后删除）；Smoke 数据写入开发库 `my_shop` 的 `banners`/`admins`，验收结束后已删除所建数据（`banners` 恢复 0 行、普通管理员已清理），占位图由启动 seed 幂等生成

## Verification
| Check | Result | Evidence |
|---|---|---|
| Build | PASS | `go build ./...` 与 `go build -o bin/my-shop .` 均成功，无错误 |
| Static | PASS | `go vet ./...` 无告警 |
| Unit / Integration Test | PASS | `go test -p 1 ./...` 全包通过；轮播图 7 个集成测试（`TestBanner*`）与迁移测试（`TestUpCreatesSchemaAndIsIdempotent`/`TestSchemaStructureMatchesBaseline`/`TestUpAppliesOnlyPendingMigration`/`TestConcurrentUp`）均 PASS |
| 迁移 | PASS | `migrate up` 幂等（无待执行 migration）；`migrate version` = `20261001000014 (dirty=false)`；`banners` 表结构与 `idx_status_sort(status,sort)` 索引与 Contract 一致 |
| 服务启动 | PASS | `bin/my-shop serve` 启动成功，`GET /health` 返回 200 |
| 公开列表 | PASS | `GET /banners`（无 token）仅返回启用项、字段完整、按 sort 升序 |
| 图片可访问 | PASS | 3 张占位图 `GET /storage/banners/banner-{1,2,3}.png` 均 200 + `image/png` |
| 后台 CRUD | PASS | 创建/更新/删除/详情均 200，响应与最终 DB 数据一致 |
| 权限边界 | PASS | 普通管理员 403/1003、未认证 401/1002，且无写入 |
| 输入校验 | PASS | 空标题 400/14002，无写入 |
| 删除语义 | PASS | 删除后公开/后台均消失；重复删除 404/14001 |

## Acceptance Evidence
- AC-001（公开列表）：Smoke 创建 1 启用 + 1 禁用，`GET /banners` 仅返回 1 条启用项、字段完整；排序由 `TestBannerPublicListEnabledAndSorted` 覆盖（sort 升序、同值 id 升序）
- AC-002（图片可访问）：3 张占位图 HTTP 200 + `image/png`
- AC-003（后台创建与权限）：超管创建落库；普通管理员 403/1003、未认证 401/1002，均无写入（DB 行数不增）
- AC-004（后台更新与排序）：更新标题/排序/状态后详情与公开列表反映新值；部分更新保留未提交字段（`image_url`/`link_url` 不变）
- AC-005（后台删除）：删除后公开与后台列表均不再出现
- AC-006（状态过滤）：禁用项不出现在公开列表，后台列表可见全部状态
- AC-007（数据模型与迁移）：`banners` 表结构、索引与 Contract 一致；迁移幂等；`latestMigrationVersion = 20261001000014`
- AC-008（长期设计）：`docs/design/banner.md` 与 APPROVED Contract、最终实现一致（本次复核关键字段/索引/错误码/权限码均一致）

## Not Executed
（无：本次里程碑所需检查均已执行并有本次证据）

## Remaining Risks
- `image_url` 为软引用，V1 不做「路径命中本地文件」硬校验（Contract Open Risks 已声明）
- 公开列表返回相对路径 `/storage/banners/...`，消费者需拼接服务源站（Contract Open Risks 已声明）
- 3 张占位图由启动 seed 写入本地存储目录，部署时需保证该目录可写（Contract Open Risks 已声明）

## Result
PASS
