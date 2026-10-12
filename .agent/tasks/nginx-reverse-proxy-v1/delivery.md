# Delivery Verification

## Milestone and Target
- Milestone：nginx-reverse-proxy-v1
- Delivery Target（review.target）：`f1c129b882a1fddf7edcc41a2af8a4656d8ed9e4`
- Feature Head（参与集成验证的 feature snapshot）：`f4733e2a15af0b95e9e24bdef6f321cb787fc352`
- Develop Base（验证时 origin/develop）：`657cd977ecae3f7b0e1baf17434a1aa819a7decd`
- Cleaner Review Target：`f1c129b882a1fddf7edcc41a2af8a4656d8ed9e4`
- Target Match：YES（`f1c129b..f4733e2` 仅 review-neutral 的 findings/core-logic/owner-decision/state 变更，生产代码自 review.target 起未变；develop_base 相对任务基线仅新增 `657cd97`（Registry-only 的 migration version 预留），不触及本任务改动文件，与 feature 无冲突）

## Environment
- OS：Linux（CNB DinD 开发环境）
- 语言运行时：宿主机 Go 1.24.1（`go build/vet/test` 使用）；镜像 builder 锁定 `golang:1.23-alpine`
- Docker：29.6.2；Docker Compose v5.3.1
- MySQL：`mysql:8.0`（容器，schema 版本 20261001000022）
- Redis：`redis:7-alpine`（容器）
- Nginx：`nginx:1.27.3-alpine`（镜像锁定，`nginx/Dockerfile` COPY 内置配置）
- 配置来源：仓库 `.env`（七牛 AK/SK/bucket/domain 与 `ADMIN_SUPER_PASSWORD` 已填入，Secret 值不记录）
- 测试数据与隔离：MySQL/Redis 为本地 Compose 开发实例（named volume）；测试用户 `dvtest01`、压测用户 `fslt*`、商品/分类/SKU/秒杀活动（活动 id=1 用于经 Nginx 压测、id=2 用于直连基线）均为本验收临时创建

## Verification

| Check | Result | Evidence |
|---|---|---|
| go build ./... | PASS | exit 0 |
| go vet ./... | PASS | exit 0 |
| go test -p 1 ./...（DATABASE_DEFAULT_PASS=root，MySQL/Redis 就绪） | PASS | exit 0，全包通过，含 `internal/middleware`（RequestID 测试） |
| docker compose config -q | PASS | exit 0 |
| nginx -t（`docker run --rm my-shop-nginx:local nginx -t`） | PASS | "syntax is ok / test is successful" |
| bash scripts/test-nginx.sh（静态断言） | PASS | exit 0 |
| 全栈启动 `docker compose up -d --build` | PASS | 五服务（nginx/app/mysql/redis/prometheus）全部 healthy |
| AC-001 唯一入口与直连隔离 | PASS | app 无宿主机端口映射（仅 `8000/tcp`）；`127.0.0.1:8000` 不可达（curl exit 7）；经 `127.0.0.1:8080/health` 返回 200 |
| AC-002 关键 API 经 Nginx 与直连逐字段一致 | PASS | IAM 登录成功/失败（200/401 + code 2002）、refresh（401 + code 2012）、普通订单（direct 下单）、后台管理（admin 登录 + 分类/商品/SKU/秒杀活动/库存创建）均逐字段一致 |
| AC-003 客户端 IP 防伪造 | PASS | 伪造 `X-Forwarded-For: 1.2.3.4`/`X-Real-IP: 5.6.7.8` 经 Nginx 登录，会话 Redis `ip`=172.19.0.1（Nginx `$remote_addr`），伪造值未生效 |
| AC-004 非幂等零自动重试 | PASS | 静态 `proxy_next_upstream off`；秒杀压测 `dup_idem=0`（无代理引入重复写入） |
| AC-005 /metrics 隔离与 /health 策略 | PASS | 公共入口 `/metrics`→404；`/health` 经 `location /` 透传 |
| AC-006 跨端 request_id 日志关联 | PASS | 伪造 `X-Request-ID` 被覆盖为 `$request_id`；Nginx 访问日志与 Go 访问日志 `request_id` 一致（`0c743be4453b465edfd6c7a837cecb7c`），含 `upstream_response_time`，无 Authorization/Cookie |
| AC-007 上游不可达错误语义 | PASS | stop app 后经 Nginx 返回 502/504（通用页，无内部细节）；Go 业务错误原样透传 |
| AC-008 请求体超限 413 | PASS | 2MB 请求体返回 413，nginx 层拦截 |
| AC-009 冷启动/重启/重建/IP 变化恢复 | PASS | stop→502→start→200（~4s）；`--force-recreate` 重建恢复；app IP 172.19.0.4→172.19.0.7 变化后 Nginx 无需重启自动恢复（DNS 再解析） |
| AC-010 秒杀压测业务正确性 + 性能对比 | PASS | 经 Nginx：200 请求，`correctness=PASS`（sold=100≤100、订单数=100==sold、dup_one=0、dup_idem=0）；直连基线：同样 `correctness=PASS`。性能见下 |
| AC-011 deployment.md 一致 | PASS | §7 纳入 Nginx 反向代理模型；未宣称 K8s/Ingress 已部署 |
| AC-012 compose config / nginx -t / go build/vet/test | PASS | 全部重跑通过 |

## Acceptance Evidence（关键 AC/INV → 本次结果）

- INV-001（客户端 IP 防伪造）：伪造 `X-Forwarded-For: 1.2.3.4`/`X-Real-IP: 5.6.7.8` 经 Nginx 登录，IAM 会话 `ip`=172.19.0.1（Nginx 真实对端），伪造值不生效。PASS。
- INV-002（非幂等零重试）：`proxy_next_upstream off` 静态断言 + 秒杀压测 `dup_idem=0`。PASS。
- INV-003（app 不对外暴露）：app 无宿主机端口映射，`127.0.0.1:8000` 不可达，仅经 Nginx 8080 可达。PASS。
- INV-004（413 不转发）：2MB 返回 413，nginx 层拦截。PASS。
- INV-005（秒杀业务正确性经 Nginx 前后一致）：经 Nginx 与直连基线均 `correctness=PASS`（sold≤total_stock、订单数==sold、dup_one=0、dup_idem=0）。PASS。

## 秒杀压测性能数据（SCENARIO=low，200 用户 × 1 请求，库存 100）

| 指标 | 经 Nginx（活动 1） | 直连 app:8000（活动 2） |
|---|---|---|
| 总请求 / 有效请求 | 200 / 200 | 200 / 200 |
| 成功受理 / 拒绝 | 100 / 100 | 100 / 100 |
| HTTP 429 / 503 | 0 / 0 | 0 / 0 |
| 吞吐 | 7.2 req/s | 7.8 req/s |
| p50 / p95 / p99 | 0.005s / 0.090s / 0.122s | 0.003s / 0.011s / 0.013s |
| correctness | PASS | PASS |

说明：本任务无性能验收指标（Contract 未定 SLO）。经 Nginx 首轮压测存在冷启动与消费者积压峰值（100）的资源竞争，p95/p99 高于已预热的直连基线；两者业务正确性均 PASS。Nginx 纯反向代理本地额外延迟理论 <1ms，本次差异主要来自压测时序（冷启动/积压），未做性能劣化结论。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 上游连接失败时 POST 的「代理层零重试」直接计数验证 | 依赖 `proxy_next_upstream off` 确定性语义 + 静态断言 + 秒杀 `dup_idem=0` 间接验证，未构造「上游单次请求内 fail 后观察 nginx 是否重发」的针对性抓包 | 低（`proxy_next_upstream off` 为 nginx 确定性语义，且幂等核对无重复） |
| 熔断（Redis 闸门）触发路径 | 低并发档未触发熔断（HTTP 503=0）；熔断为业务层既有机制，本任务不改其逻辑 | 低（AC-010 仅需业务正确性一致，限流/排队/熔断行为不变由业务测试覆盖） |

## Remaining Risks

- CNB 环境在 Nginx 之前是否还有额外代理层：本次实测 `$remote_addr`=172.19.0.1（宿主机桥接网关），客户端为本机 curl，未观察到额外代理层；若未来部署前有 LB/网关，需 `real_ip` 模块还原真实客户端 IP（Contract D2 已预留）。
- `prometheus` 宿主端口映射（`0.0.0.0:9090`）Owner 未要求变更，与「监控隔离」目标存在潜在不一致（非本任务阻塞项）。
- 单实例部署（无多实例负载均衡），上游 DNS 再解析 `valid=10s` 意味着 IP 变化后最长 ~10s 窗口内可能短暂 502（已实测恢复）。

## Result

PASS

所有关键交付检查均有本次运行证据并通过，无已知阻塞问题。以上结论对应 feature snapshot `f4733e2a15af0b95e9e24bdef6f321cb787fc352`（review.target `f1c129b882a1fddf7edcc41a2af8a4656d8ed9e4`）+ develop_base `657cd977ecae3f7b0e1baf17434a1aa819a7decd`。PASS 不等于 Owner 最终接受，最终集成由 Owner 决定。
