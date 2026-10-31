# Delivery Verification

## Milestone and Target

- Milestone：`surgecart-baseline-audit-v1`（SurgeCart 秒杀链路工程基线审计报告）
- Delivery Target：feature 快照 `fd605ad3bfa4a69f2cbd171c0d96d1d9de6f9b3b`；交付物 `docs/reports/surgecart-baseline-audit-v1/baseline-audit-report.md`；报告所锚定的被审计代码版本 `7ba5a31077a7b8977473765c2d9a7d402ddadb8c`
- Cleaner Review Target：`1ca5445ef3d9cc04a7f11f78eb44638376d1eb78`
- Target Match：YES
- 版本关系（本次核验）：
  - `git rev-parse HEAD` = `git ls-remote --heads origin refs/heads/task/surgecart-baseline-audit-v1` = `fd605ad…`（验收开始时）
  - `review.target` `1ca5445…` 是当前 feature HEAD 的祖先；`owner.review_target == review.target`，`owner.status=ACCEPTED`
  - `7ba5a31…` 是任务初始化提交（`8540230…` 的下一提交），`git diff --stat 7ba5a31 HEAD -- internal api internal/migrations Dockerfile .cnb.yml prometheus docs/design` 输出为空，因此被审计代码与当前交付快照的生产代码完全一致
  - `delivery.develop_base` = `origin/develop` = `85402302622d059918e1a87b83bbb55b400d1a85`（`git ls-remote` 实核）

## Environment

- 宿主机 / 运行时：Linux `5.4.241-1-tlinux4-0025.10` x86_64；`Debian GNU/Linux 12 (bookworm)`；Go `1.24.1 linux/amd64`
- Docker：`29.6.2-7`（Alpine Linux v3.24，容器内运行）
- MySQL：`mysql:8.0` 容器，实际版本 `8.0.46`，`127.0.0.1:3306`
- Redis：`redis:7-alpine` 容器，实际版本 `7.4.12`，`127.0.0.1:6379`，无密码
- 依赖来源：`make deps-start`（`docker compose up -d mysql redis` + healthcheck 等待），同一次验收内 `make down` 清理容器（保留数据卷）
- 配置来源：仓库无 `.env`；compose 的 `MYSQL_ROOT_PASSWORD` / `AUTH_JWT_SECRET` 由本次运行环境变量注入（本地临时测试值，不记录 Secret 值）；`make test-integration` 按脚本默认注入 `DATABASE_DEFAULT_PASS`。未使用任何真实凭据或生产配置
- 测试数据与隔离：使用 compose 命名卷 `my-shop_mysql_data` / `my-shop_redis_data` 与默认库，测试自身建表、清表；验收结束后容器已删除、卷保留，未接触生产数据
- 环境差异：与报告 §0.1 记录的环境同类（Go 1.24.1 / Docker 29.6.2-7 / mysql:8.0 / redis:7-alpine），但 MySQL 具体补丁版本为 8.0.46、Redis 为 7.4.12；均为同一大版本镜像，不影响本报告的静态审计结论

## Verification

| Check | Result | Evidence |
| --- | --- | --- |
| Build `go build ./...` | PASS | exit 0，无输出（首次构建拉取模块依赖） |
| Static `go vet ./...` | PASS | exit 0，无输出 |
| Unit `make test-unit` | PASS | exit 0；纯内存包全部 `ok`（含 `internal/cmd 0.005s [no tests to run]`） |
| Race `make test-race` | PASS | exit 0；`internal/logic/flashsale 1.039s` |
| Integration `make test-integration` | PASS | exit 0；`internal/logic/flashsale 3.802s`、`internal/worker 0.011s`、`internal/migrations 27.795s`、`test/e2e 92.000s` |
| Prometheus `make test-prometheus` | PASS（含跳过项） | exit 0；静态断言通过；`promtool` / `prometheus` 镜像缺失 → recording rules 语法校验 `NOT_EXECUTED`（脚本自身 WARN） |
| Container `make test-container` | PASS | exit 0；凭据分离 / 构建上下文 / 镜像 / 生命周期 / 编排断言通过 |
| Full gate `make test` | PASS | exit 0；`check-registry.sh`「未发现 Reservation 重复或 Registry ↔ 实现明显不一致」；`nginx -t` 因镜像缺失跳过 |
| 变更范围 `git diff --stat 7ba5a31 HEAD -- internal api internal/migrations Dockerfile .cnb.yml prometheus docs/design` | PASS | 输出为空 |
| 交付快照文件清单 `git diff --name-only 7ba5a31 HEAD` | PASS | 仅 `docs/reports/surgecart-baseline-audit-v1/baseline-audit-report.md` 与任务目录 5 个 artifact；无 Kafka 新增代码 |
| 报告锚点抽查 1（Redis Lua 准入） | PASS | `internal/logic/flashsale/redis.go:207` 为 `g.Redis().Do(ctx, "EVAL", flashSaleGateScript, 6, …)` |
| 报告锚点抽查 2（消费者领取） | PASS | `internal/logic/flashsale/consume.go:84` 为 `LockUpdateSkipLocked()`，其前为 `status=queued` + 到期条件 + `ORDER BY id LIMIT 1` |
| 报告锚点抽查 3（库存条件扣减） | PASS | `internal/logic/flashsale/flashsale.go:530-540` 为 `UPDATE … WHERE sold < total_stock` + `RowsAffected()==0` 返回库存不足 |
| 报告数据层抽查（migration） | PASS | `20261001000015_flash_sale_order_requests.up.sql:18,26,27`（`status` / `uk_request_idempotency` / `idx_dequeue`）；`20261001000011_flash_sale.up.sql:56-58` 三个订单唯一键 |
| 报告配置抽查 | PASS | `manifest/config/config.yaml` 中 `rate_limit` / `queue_capacity` / `circuit_breaker` / `metrics` 四处 `enabled: false`（105/120/130/141 行） |
| 报告测试计数抽查 | PASS | `test/e2e` 中 `func TestFlashSale*` 去重计数 = 20，与报告修正后的「10+4+4+2 = 20」一致 |
| 报告章节完整性 | PASS | §0 锚点、§2–§3 调用链、§4 数据层、§5 请求表职责、§6 测试与门禁、§7 四类事实分区、§8 T02/T03 输入、§9 AC 对照、§10 限制均存在 |

## Acceptance Evidence

- AC-001：报告 §0 记录仓库、分支、完整 40 位 SHA `7ba5a31…` 与工作区状态，并声明锚点自引用与等价核验命令；本次核实 `7ba5a31…` 为真实提交且属于当前 feature 历史，其与 HEAD 之间无生产代码差异。
- AC-002：§3 覆盖入口路由、参数校验、限流/容量、Redis Lua 准入、请求写入、消费领取、订单创建、库存扣减、结果查询、对账与修复；本次抽查 3 跳（Lua `EVAL`、`SKIP LOCKED` 领取、条件扣减 + `RowsAffected`）均在标注行命中。
- AC-003：§4 的表、状态、事务边界、唯一约束、索引与条件更新可在 migration `11/15/18` 与逻辑层 SQL 中找到同名定义；本次抽查键名与行号一致。
- AC-004：§5 的「状态机 + 出队队列」判定与源码一致；技术失败退避、`retry_count=4` 落 `dead`、无 `processing` 租约、崩溃回滚重领均可在 `consume.go` 定位；与 `docs/design/flash-sale.md` 的差异逐条列出。
- AC-005：报告 §6.1 的 E1–E14 由本次独立重跑复现：`go build` / `go vet` / `make test-unit` / `make test-race` / `make test-prometheus` / `make test-container` / `make test-integration` / `make test` 全部 exit 0；跳过项与 `NOT_EXECUTED` 标注一致。
- AC-006：§7 四分区与 §9 计数一致；未执行项在 §6.2 / §10 标 `NOT_EXECUTED`，未见把设计意图写成已实现事实（本次抽查 F 区锚点均可定位）。
- AC-007：§8 给出可复核证据清单与 14 个待专项核查问题，并明确 `T02` / `T03` 为 Owner 后续任务编号、未推断其范围。
- AC-008：本轮交付只新增 `delivery.md` 与 `state.yaml` 的 `delivery.*`；受保护路径 diff 为空，无 Kafka 相关代码。

本次证据与 Cleaner 结论的区分：Cleaner 的 `CLEAN`（`findings.md`）与 8/8 AC PASS 是审查输入；上表构建、测试、命令退出码、锚点抽查与 SHA 核验均为本次 Deliverer 独立执行/核对的结果。

## Not Executed

| Check | Reason | Risk |
| --- | --- | --- |
| `promtool check rules prometheus/rules/flashsale_latency.yml` | 环境无 `promtool`，也无 `prometheus` 镜像 | recording rules 仅经文本/结构静态断言，语法正确性未由 Prometheus 解析器证实 |
| `nginx -t`（`make test-nginx` 动态项） | 未构建 `my-shop-nginx:local` 镜像 | Nginx 配置仅静态断言，未做真实加载校验 |
| `docker build` / `make docker-build` | 非本任务 AC 要求；本轮不引入无关镜像产物 | `Dockerfile` 仅静态核实（`make test-container` 覆盖断言） |
| 压测 / 容量规划 / P95-P99 实测 | Task Out of Scope 明确排除 | 无吞吐与延迟实测数据 |
| 生产环境部署验证 | 无生产环境访问权，且 Deliverer 禁止部署 | 生产实际开关与版本未知 |

## Remaining Risks

- 报告登记的风险 R1–R11（如技术失败被误判为业务终态、孤儿 Redis 标记、请求表无租约、审计表无限增长等）按 Task 约定**只记录不修复**；本次验收不改变这些结论。
- MySQL 库存兜底（`sold >= total_stock` 预检 + `WHERE sold < total_stock` + `RowsAffected`）缺少可独立证伪的测试覆盖，已登记为报告 §8.2 第 14 项并转 T02；`TestFlashSaleConcurrentNoOversell` 只能发现 Redis 闸门被破坏，不能发现 MySQL 两道兜底被移除。
- 工作流记账观察（不影响本任务交付结论）：`loop-ledger.md` 的 `EV-0003`（`OWNER_DECISION:baseline-audit-snapshot-acceptance` S1 round=2）仍为 `outcome: OPEN`，缺少对应 `CLOSE` 事件；`state.yaml` 为权威状态（`blocked.active=false`、`owner.status=ACCEPTED`），`delivery-start` 与后续 Gate 均正常通过，该记账由 OwnerGate 归属的 loop 收尾。
- 报告的事实结论只对 `7ba5a31…` 成立；`develop` 前进后需重新核验行号与行为。

## Result

PASS
