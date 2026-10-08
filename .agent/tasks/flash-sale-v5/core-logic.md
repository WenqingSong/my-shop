# Core Logic 验证卡

以下核心机制决定秒杀 V5 容量保护的正确性，供 OwnerGate 提炼并向 Owner 解释。核心不变量：被容量保护拒绝的请求**无副作用**（不预扣库存、不写请求），熔断期间**快速失败且不破坏五个业务不变量**。

## CL-001：容量保护的无副作用拒绝顺序（限流 + 排队软上限）

- Owner 需要理解：用户级/活动级限流（12009/429）与排队软上限（12010/429）必须在 Redis 闸门 `runGate` **之前**执行。一旦顺序错位（先过闸门再限流），被拒绝的请求就会已预扣库存/写一人一单标记，产生「拒绝却有副作用」的脏数据，破坏 INV-018/INV-019。
- 生产代码：`internal/logic/flashsale/flashsale.go` `CreateOrder`（L279-287 依次 `rateLimitUser` → `rateLimitActivity` → `checkQueueCapacity`，随后 L290 熔断、L295 `runGate`）；`internal/logic/flashsale/ratelimit.go`（固定窗口 Lua `INCR`+`EXPIRE`，Redis 故障 fail-open）；`internal/logic/flashsale/queue_capacity.go`（入队前 `GET` 近似计数、超限 12010）。
- 关键测试：`internal/logic/flashsale/capacity_test.go` `TestRateLimitUserRejects` / `TestRateLimitActivityRejects` / `TestQueueCapacityRejects`；`internal/cmd/flashsale_v5_test.go` `TestFlashSaleV5RateLimitHTTP`。
- 基线验证：`go test -run 'TestRateLimit|TestQueueCapacity' -v ./internal/logic/flashsale/...` → 全部 PASS；测试断言超阈值返回 12009/12010 且 `remaining` 不变、`flash_sale_order_requests` 无新增。
- 可选 Mutation：把 `flashsale.go` 中三处限流/排队检查移动到 `runGate` 调用**之后**。
- 预期失败：`TestRateLimitUserRejects` 在第 2 次请求断言 `remaining == "9"` 时会得到实际 `"8"`（闸门已预扣），从而 `t.Fatalf` 失败——证明「拒绝无副作用」这一判断被真实保护。
- 恢复确认：撤销 Mutation 后重跑 `go test -run 'TestRateLimit|TestQueueCapacity' ./internal/logic/flashsale/...` 恢复 PASS。

## CL-002：Redis 闸门熔断的快速失败（fail-closed，不破坏不变量）

- Owner 需要理解：Redis 闸门连续失败达阈值 → 熔断 Open → 直接 503/1005 **不进入闸门、不预扣、不建单、不排队**；超时后经半开探测（真实请求）验证 Redis 恢复自动关闭。这是 V5 在 Redis outage 期间的容量保护：把故障从「每次请求都打到 Redis 失败」缩短为「进程内快速失败」，且绝不把流量打回 MySQL。
- 生产代码：`internal/logic/flashsale/circuit_breaker.go`（进程内状态机 Closed→Open→Half-Open，`sync.Mutex` 保护，连续失败阈值 + `open_timeout` + 半开探测数上限）；`flashsale.go` `CreateOrder` L290-301（`breaker.allow` 短路 + `runGate` 出错 `recordFailure`、成功 `recordSuccess`）。
- 关键测试：`internal/logic/flashsale/capacity_test.go` `TestCircuitBreakerOpensAndRecovers`。
- 基线验证：`go test -run TestCircuitBreakerOpensAndRecovers -v ./internal/logic/flashsale/...` → PASS；测试注入真实闸门故障（活动 key 改 string 触发 Lua WRONGTYPE），验证 Open 期间快速失败、无预扣、无 request，超时后半开探测成功自动关闭。
- 可选 Mutation：删除 `flashsale.go` L290 的 `if !s.breaker.allow(ctx)` 短路判断。
- 预期失败：`TestCircuitBreakerOpensAndRecovers` 在「Open 未超时」步骤断言 `remaining == "10"` 时会因闸门被实际调用而预扣得到 `"9"`，从而 `t.Fatalf` 失败——证明「熔断 Open 不产生业务副作用」这一判断被保护。
- 恢复确认：撤销 Mutation 后重跑 `go test -run TestCircuitBreakerOpensAndRecovers ./internal/logic/flashsale/...` 恢复 PASS。
