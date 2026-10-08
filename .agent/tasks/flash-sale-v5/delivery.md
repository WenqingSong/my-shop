# Delivery Verification

## Milestone and Target

- Milestone：秒杀 V5 容量保护与压测闭环
- Delivery Target：`feat/flash-sale-v5` 的 CLEAN 实现 snapshot
- Cleaner Review Target：`35498b0340b7e8688f18e47dd3e64c11fdb9b3a7`
- Target Match：YES（`feature_head=57156db8` 为 `35498b0` + review-neutral tail `97b4d18`/`70e3a14`/`57156db8`，仅含 review/owner 元数据，生产代码未变）

## Environment

- OS：Linux（容器内执行）
- Go：1.24.1
- MySQL：8.0.46（docker-compose，healthy，服务器时区 UTC）
- Redis：7.4.11（docker-compose，healthy，`maxmemory-policy=noeviction`）
- Docker：29.6.2 / Compose v5.3.1
- 交付对象：`go build ./...` 源码构建产物 `bin/my-shop`，无未提交文件 / 本机绝对路径依赖
- 配置来源：`manifest/config/config.yaml` + 环境变量（`ADMIN_SUPER_PASSWORD`、`FLASH_SALE_*` 容量保护开关）
- 隔离与清理：每次验收前清空 `flash_sale_*`/`users`/`skus`/`products`/`categories` 等表 + `redis FLUSHDB`，未触碰共享/生产数据
- 时区说明：Go 进程 +08:00、MySQL UTC；秒杀时间窗代码用 `UNIX_TIMESTAMP`/`NOW()` 规避漂移（非 V5 引入）；验收脚本用固定宽时间窗规避换算歧义

## Verification

| Check | Result | Evidence |
|---|---|---|
| gofmt / go build ./... / go vet ./... | PASS | exit 0，无输出 |
| go test -p 1 ./... | PASS | 全部 package ok（含 internal/logic/flashsale、internal/cmd） |
| go test -race（容量保护测试） | PASS | TestRateLimit/TestQueueCapacity/TestCircuitBreaker/TestMetrics 无竞态 |
| 服务启动 + 健康检查 | PASS | 多次以不同容量保护配置重启，/health 返回 200 |
| 配置加载（四特性 env 开关） | PASS | rate_limit/queue_capacity/circuit_breaker/metrics 经 env 开启后真实生效 |
| AC-001 用户级限流 | PASS | 同用户第 3 次请求 429/12009，remaining/sold/requests 均不变（无副作用） |
| AC-002 活动级限流 | PASS | 单活动第 3 个用户 429/12009，另一活动不受影响（按活动维度计数） |
| AC-003 排队软上限 | PASS | 第 3 个入队请求 429/12010，MySQL `queued=2≤上限`、Redis 计数=2，无副作用 |
| AC-004 熔断降级 | PASS | 闸门连续 2 次 503 → 熔断 Open 快速失败（不预扣/不排队，remaining=10/requests=0）→ 恢复后半开探测自动关闭 |
| AC-005 指标 | PASS | /metrics 返回 Prometheus 文本，queued/rate_limited/gate_rejected/queue_full/error/success 与真实结果对账一致 |
| AC-006 热点 Key | NOT_EXECUTED | 脚本可运行(exit 0)，但 Redis 默认 noeviction 未启用 LFU，无法产出热点结果（CLEAN-002 P3） |
| AC-007 逐级压测脚本 | FAIL | `run.sh` 生成用户名 `fslt_{activity}_{i}` 含下划线，被 IAM 用户名校验 `^[a-zA-Z0-9]{3,24}$` 拒绝，脚本在 `setup_users` 即退出 |
| AC-008 正确性核对 | FAIL（部分） | 手动核对 sold≤total_stock、订单数=sold、无一人一单/幂等违例均通过；但 run.sh verify() 无法执行（被 AC-007 阻塞） |
| AC-009 基线保存 | FAIL（阻塞） | run.sh 无法执行，无法保存基线 JSON |
| AC-010 长期设计 | PASS | docs/design/flash-sale.md §11 与 APPROVED Contract、最终实现一致 |

## Acceptance Evidence

- INV-018（限流无副作用）：12009 请求不预扣库存、不写 `flash_sale_order_requests`（sold/requests 不变）——HTTP 验证通过。
- INV-019（排队软上限）：12010 请求无副作用，`status=queued` 计数 ≤ 上限，无超卖/无重复——HTTP 验证通过。
- INV-020（熔断不破坏不变量）：熔断 Open 快速失败，remaining=10、requests=0 无副作用，恢复后半开探测自动关闭——HTTP 验证通过。
- INV-021（指标可对账）：`/metrics` 计数与 MySQL 订单数、queued/拒绝数对账一致——HTTP 验证通过。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| AC-006 热点 Key 实际产出 | Redis 默认 `noeviction` 未启用 LFU（CLEAN-002 P3） | 无法产出热点结果，需环境显式启用 LFU |
| AC-007/008/009 三级压测与基线 | `run.sh` 用户名含下划线被 IAM 拒绝，脚本无法执行 | 阻塞容量保护压测闭环 |

## Remaining Risks

- 压测脚本 `scripts/flashsale-loadtest/run.sh` 存在阻断性实现缺陷（用户名非法），AC-007/008/009 未闭环（失败分类：`IMPLEMENTATION_DEFECT`）。
- 配置注释与实现不一致：`consume_scan_interval`/`reconcile_scan_interval` 用 `MustGet` 读取（非 env-aware），而 config.yaml 注释声称支持 `FLASH_SALE_CONSUME_SCAN_INTERVAL`/`FLASH_SALE_RECONCILE_SCAN_INTERVAL` 环境变量覆盖——V3/V4 既有，非 V5 引入，不影响 V5 验收结论，供后续知悉。
- 热点 Key 分析依赖 Redis LFU，默认 docker-compose 未启用（CLEAN-002 P3，Owner 已接受为非阻塞）。

## Result

FAIL
