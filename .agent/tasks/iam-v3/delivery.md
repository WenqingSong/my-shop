# Delivery Verification

## Milestone and Target
- Milestone：IAM V3（多设备会话管理与主动撤销）里程碑交付验收
- Delivery Target：Commit `029141327d4d4246697062dba0f8fcdcc9bb1d64`（分支 `feat/iam-v3`，`feat(iam): 新增多设备会话管理与主动撤销`）
- Cleaner Review Target：`3e61dc3..0291413`（审查对象 Commit `0291413`）
- Target Match：YES（HEAD 与 Cleaner 审查对象一致；生产代码/测试/配置/迁移无未提交改动，仅 `findings.md`、`core-logic.md` 两个过程文档未提交，不影响交付物）

## Environment
- OS：Linux（linux/amd64）
- Go：1.24.1（`go.mod` 声明 `go 1.23.0`，见 Remaining Risks）
- MySQL：8.0（Docker `mysql:8.0`，healthy）
- Redis：7-alpine（Docker `redis:7-alpine`，healthy）
- Docker：29.6.2 / Compose v5.3.1
- 应用：本地进程监听 `:8000`，经 `bash scripts/up.sh` 构建 + 迁移 + 启动，`/health` 返回 `code=0`
- 配置来源：`manifest/config/config.yaml` 开发默认值 + 环境变量（不记录任何 Secret）
- 测试数据：注册 `dv<ts>a` / `dv<ts>b` 两个用户及若干会话；Redis 会话/索引 TTL 3600 自愈；MySQL 残留两条测试用户行（dev 库）

## Verification
| Check | Result | Evidence |
|---|---|---|
| gofmt -l（改动文件） | PASS | 输出为空 |
| go build ./... | PASS | 退出码 0 |
| go vet ./... | PASS | 退出码 0 |
| go test -p 1 ./... | PASS | 全部包 `ok` |
| go test -race ./internal/controller/iam/... | PASS | `ok ... 50.68s` |
| 应用构建 + 迁移 + 启动 | PASS | `up.sh` 完成，`/health` `{"code":0,"message":"OK",...}` |
| 核心主链路 Smoke（AC-001~007、AC-009） | PASS | 独立脚本 30/30 断言通过 |
| AC-008 fail-closed | PASS | `TestRedisWrongTypeFailClosed` + `TestListSessionsRedisErrorReturns500` 通过 |
| 最终数据（Redis / DB） | PASS | 见 Acceptance Evidence |
| Design 一致性（AC-010，最小确认） | PASS | `docs/design/iam.md` 含新索引 / 撤销 / 2011 / 失败语义章节 |

## Acceptance Evidence
- AC-001：用户 A 两次登录产生不同 sid（`afe4c315…` vs `7a766570…`），各自 session 均存在未撤销。
- AC-002：`GET /sessions` 返回 A 全部 2 条、`current` 唯一且 = 当前 token 的 sid、含 `login_at`/`user_agent`/`ip`；B 列表仅 1 条且不含 A 会话。
- AC-003：`DELETE /sessions/{sid2}` 200 后，该 token 访问 `/me` = 401（code 1002），其他会话（A 设备1 / B）仍 200。
- AC-004：`revoke-others` 后设备3 token = 401，当前设备1 仍 200。
- AC-005：`revoke-all` 后设备1 / 设备4 均 401，B 不受影响。
- AC-006：A 撤销 B 的 sid 与非法 sid 均 404 / 2011，B 无变化仍 200。
- AC-007：对已撤销会话重复撤销幂等 200。
- AC-009：`logout` 后当前会话 `/me` = 401，其他会话仍 200（只撤销当前）。
- Redis 数据：用户 37 的 5 个会话均 `revoked=1` 且 key/TTL 保留（TTL≈3583s）；用户 38 会话 `revoked=0`；`login_at`/`user_agent`/`ip` 正确落库；索引 `iam:user:{id}:sessions` 及 TTL 正确。
- 索引过滤（INV-006）：索引仍含已撤销的 sid5，但 `GET /sessions` 正确只返回有效会话（sid6 + 新 sid7）、排除 sid5 —— 证实 Hash 为权威、索引为枚举优化、过滤无遗漏。
- DB 核对：`users` 表 id 37 = `dv…a`、id 38 = `dv…b`。

## Not Executed
| Check | Reason | Risk |
|---|---|---|
| 停 Redis 容器的「完全宕机」Smoke | 采用 AC-008 允许的「注入错误」确定性方式（WRONGTYPE 单测）覆盖同一 fail-closed 代码路径；停容器会扰动共享 dev 环境 | 低：单测已覆盖鉴权 401 / 列表 500 不 fail-open |

## Remaining Risks
- CLEAN-001（P3，OPEN）：单会话撤销（revoke-one / logout）不从索引 ZREM，已撤销成员滞留至 TTL。本次独立复核已确认（索引仍含已撤销 sid5），但列表以 Hash 过滤、无正确性/安全影响，TTL 自愈。是否修复由 Owner 决定。
- CLEAN-002（P3，OPEN）：列表/撤销数据操作失败路径未显式 `glog` 记录底层错误。仅可观测性影响，fail-closed 已实测通过（返回 500 不 fail-open）。是否修复由 Owner 决定。
- Go 版本差异：`go.mod` 声明 `go 1.23.0`，验收环境 Go 1.24.1；构建/测试均通过，未观察到功能差异。

## Result
PASS
