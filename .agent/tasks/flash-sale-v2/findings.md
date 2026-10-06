# Cleaner Findings

## Review Target

- 任务基线：`06bc661`（分支 `feat/flash-sale-v2` 起点）
- 实现证据提交 C1（`review.target`）：`d1f878a233355ad988bf93a06038af81ef2482f9`
  （`feat(flash-sale-v2): 引入 Redis + Lua 秒杀热路径与对账（含集成测试）`，7 文件）
- Contract：`APPROVED` @ `06cf46f`（`contract.md` + `docs/design/flash-sale.md`）
- 当前 HEAD：`00e45fa`（`block：秒杀V2执行中断-cleaner`）
- 审查方式：`git diff 426f34b..d1f878a`（纯实现，无无关改动）；工作区 clean

## Result

BLOCKED

## 阻塞事实（非实现缺陷）

`review.target`（C1 = `d1f878a`）之后的 tail 包含 `task.md`：

- `task.md` 仅在 commit `00e45fa`（`block：秒杀V2执行中断-cleaner`）中被加入，晚于 C1；
  `d1f878a` 与基线 `06bc661` 均不含 `task.md`（`git log --follow` 仅命中 `00e45fa`）。
- `task.md` 不在 review-neutral 白名单（`internal/workflow/paths.go` 仅允许
  findings/core-logic/owner-decision/delivery/state.yaml），属实质（substantial）文件。
- `workflow-check gate cleaner-start` 已 FAIL：
  `Review Tail (INV-4): .agent/tasks/flash-sale-v2/task.md 在 review.target 之后发生变化`。
- 依「CLEAN 只对 target 有效、target 之后出现非 Review-neutral 实质变化即客观失效」，
  本任务当前无法给出有效的 CLEAN。

根因：TaskBuilder 未在任务建立阶段提交 `task.md`，其在一段被中断的 Cleaner session 中
被「block」commit 顺带提交，落到了实现之后。修复需改写历史（把 `task.md` 移到 C1 之前）
或 Owner 决策；均超出 Cleaner authority（禁止 rewrite 已发布历史、禁止修改 `task.md`）。

## Acceptance Criteria（实质审查：代码 + 运行证据）

| ID | Result | Evidence |
|---|---|---|
| AC-001 预热与一致性 | PASS | `syncActivityCache` 在 Create/Update/下架后同步维护；`TestFlashSaleV2PreheatAndInvalidate` |
| AC-002 Lua 原子校验/去重/预扣 | PASS | `redis.go` Lua 脚本 + `TestFlashSaleV2RedisPreDeductNoOversell` + V1 `TestFlashSaleOnePerUser`/`TestFlashSaleIdempotency` |
| AC-003 售罄快速失败 | PASS | `TestFlashSaleV2SoldOutFastFail` |
| AC-004 防缓存穿透 | PASS | 闸门纯 Redis（Lua，不触 MySQL）；`TestFlashSaleOrderTimeWindow`/`TestFlashSaleOrderFailures` |
| AC-005 业务不变量保持 | PASS | `TestFlashSaleConcurrentNoOversell`（-race）+ V1 全部用例 |
| AC-006 对账收敛 | PASS | `TestFlashSaleV2Reconcile` |
| AC-007 降级容错 | PASS | `TestFlashSaleV2Degrade` |
| AC-008 长期设计 | PASS | `docs/design/flash-sale.md` 与 Contract/实现一致（一处 P3 措辞漂移见下） |

## Verification

| Check | Result | Evidence |
|---|---|---|
| 构建 | PASS | `go build ./...` |
| 静态检查 | PASS | `go vet ./...` |
| 全量测试 | PASS | `go test -p 1 ./...`（MySQL/Redis 容器就绪） |
| 秒杀测试（-race） | PASS | `go test -race -run TestFlashSale ./internal/cmd/`（14 用例全过） |
| Registry 一致性 | PASS | `scripts/check-registry.sh`（无重复/漂移） |
| 三边一致性 | PASS | Registry↔Contract↔实现：复用错误码域 12000-12999（12001~12007）、无新 migration |

## Findings

实质审查无 P0/P1/P2。两处 P3 观察（非阻塞，供 Owner 参考，待 git 历史修复后复审确认）：

- P3：`docs/design/flash-sale.md` §6.2 与 `contract.md`「Lua 边界」将检查顺序写作
  「一人一单 → 幂等」，但实现为「幂等 → 一人一单」。实现与 Contract「重复/重试」语义及
  V1（幂等重试返回既有订单、一人一单返回 12004）一致，属文档措辞漂移，非行为错误。
- P3：`compensatePreDeduct` 仅 `INCR remaining`、不清除 `soldout` 标记；在「预扣最后一单
  → 并发请求置售罄 → 该单 MySQL 失败补偿」的特定序列下，可能出现最长一个对账周期（60s）
  的伪售罄（可用性受损）。自愈、不破坏不变量、不超卖。

阻塞原因为 git 历史异常（`task.md` 落入 review tail），非 Coder 可在当前 Scope 修复的
缺陷，故不建 `CLEAN-xxx` Finding。
