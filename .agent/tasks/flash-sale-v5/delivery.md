# Delivery Verification

## Milestone and Target

- Milestone：秒杀 V5 容量保护与压测闭环
- Delivery Target：`feat/flash-sale-v5` 的 CLEAN 实现 snapshot（复审后）
- Cleaner Review Target：`2deecfda31d8f7effd8512d21fd68eb90d67ea35`
- Target Match：YES（`feature_head=f461f98` 为 `2deecfd` + review-neutral tail `a8caaba`/`f461f98`，仅含 review/owner 元数据；`2deecfd` 仅改 `scripts/flashsale-loadtest/run.sh`，未改 Go 生产代码）

## Environment

- OS：Linux（容器内执行）
- Go：1.24.1
- MySQL：8.0.46（docker-compose，healthy，服务器时区 UTC）
- Redis：7.4.11（docker-compose，healthy，`maxmemory-policy=noeviction`）
- Docker：29.6.2 / Compose v5.3.1
- 交付对象：`go build ./...` 源码构建产物 `bin/my-shop`，无未提交文件 / 本机绝对路径依赖
- 配置来源：`manifest/config/config.yaml` + 环境变量（`ADMIN_SUPER_PASSWORD`、`FLASH_SALE_*` 容量保护开关）
- 隔离与清理：验收前清空 `flash_sale_*`/`users`/`skus`/`products`/`categories` 等表 + `redis FLUSHDB`，未触碰共享/生产数据；压测基线为 gitignore 的运行时产物

## Re-verification（本次复审背景）

上次 `delivery.status=FAIL`：`run.sh` 用户名含下划线被 IAM 拒绝导致 AC-007/008/009 无法执行。Coder 在 `2deecfd` 修复 4 处（用户名 `fslt{activity}{i}`、worker.sh 并发、`printf` 换行、空值假 PASS），Cleaner 复审 CLEAN、Owner 重新 ACCEPT。本次重跑原失败场景及受影响主链路。

## Verification

| Check | Result | Evidence |
|---|---|---|
| `delivery-start` Gate | PASS | `.agent/bin/workflow-check gate delivery-start` exit 0 |
| gofmt / go build ./... / go vet ./... | PASS | exit 0（Go 代码未变，复验） |
| 服务启动 + 健康检查 + 容量保护配置加载 | PASS | 多次以不同容量保护 env 重启，/health 返回 200 |
| AC-007 逐级压测（重验） | PASS | normal(60 请求全受理)/low_stock(5 受理+55 售罄)/flood(50 受理+100 售罄)，脚本可重复执行，输出吞吐与 p50/p95/p99 |
| AC-008 正确性核对（重验） | PASS | 消费后 sold≤total_stock、订单数=sold、无一人一单/幂等违例、queued 归零、Max_used_connections=50 远低于上限 |
| AC-009 基线与对比（重验） | PASS | 4 份 JSON 基线已保存；compare.sh 对比输出正常；限流参数调整前后对比（无限流 rejected=100 售罄 vs 有限流 rejected=50 限流） |
| AC-001 用户级限流（本轮新证据） | PASS | 限流对比场景 user max=1，每个用户第 2 请求 429/12009，`rate_limited` 指标=50 与拒绝数对账一致 |
| AC-002 活动级限流 | PASS | 上轮已验（单活动第 3 用户 12009，跨活动独立）；Go 代码未变 |
| AC-003 排队软上限 | PASS | 上轮已验（12010，queued=2≤上限，无副作用）；Go 代码未变 |
| AC-004 熔断降级 | PASS | 上轮已验（连续失败→Open 快速失败无副作用→半开探测自动关闭）；Go 代码未变 |
| AC-005 指标 | PASS | 上轮已验 + 本轮 `/metrics` 与 MySQL/拒绝数对账（queued/rate_limited 等） |
| AC-006 热点 Key | NOT_EXECUTED | 脚本可运行，但 Redis 默认 `noeviction` 未启用 LFU，无法产出热点结果（CLEAN-002 P3） |
| AC-010 长期设计 | PASS | `docs/design/flash-sale.md` §11 与 Contract/实现一致（Go 代码未变，复验） |

## Acceptance Evidence

- AC-007/008/009（原失败项，本次重验通过）：
  - 三级压测脚本 `run.sh` 正常注册用户、并发下单、输出吞吐/时延分布、保存基线，无用户名/并发/换行/假 PASS 缺陷。
  - 消费完成后数据核对：`sold ≤ total_stock`（60≤100、5≤5、50≤50）、订单数=`sold`、一人一单/幂等违例均为 0、`queued` 归零、`Max_used_connections=50`。
  - 基线 4 份 JSON 可查询、`compare.sh` 可对比；限流参数调整前后对比成功（`rate_limited=50` 与拒绝数对账一致）。
- INV-018/019/020/021 在上轮 HTTP 验证已通过，本轮未改动相关 Go 代码，结论保持。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| AC-006 热点 Key 实际产出 | Redis 默认 `noeviction` 未启用 LFU（CLEAN-002 P3） | 无法产出热点结果，需环境显式启用 LFU |

## Remaining Risks

- CLEAN-001（P3，OPEN）：`run.sh` `verify()` 未将「queued 有界 / 连接数阈值」纳入自动判定，本次由 Deliverer 人工核对（queued 归零、连接数=50 远低于上限）；不影响正确性，自动化闭环不完整。
- CLEAN-002（P3，OPEN）：热点 Key 分析依赖 Redis LFU，默认 docker-compose 未启用。
- 容量保护四特性默认 `enabled: false`，压测/生产需显式开启（部署/配置决策）。

## Result

PASS
