# Owner 核心逻辑验证卡

本任务为纯 CI/脚本/测试工程，无权限、事务、库存、金额、幂等等业务不变量。值得 Owner 掌握的两处核心机制是「race 门禁能否真的抓到数据竞争」与「Registry 门禁能否真的抓到全局资源漂移」——它们决定了新门禁是否“真能拦下问题”而非“恒绿”。

## CL-001：race 门禁能捕获熔断器数据竞争

- Owner 需要理解：秒杀熔断器 `circuitBreaker` 是进程内共享内存状态机，靠 `sync.Mutex` 串行化 `consecutiveFails`/`state`/`halfOpenInFlight` 的读写；一旦锁被去掉，并发下单会数据竞争（计数/状态错乱，可能错误放行或错误熔断），`go test -race` 必须能报出 `DATA RACE` 并使门禁非零退出。
- 生产代码：`internal/logic/flashsale/circuit_breaker.go`（`circuitBreaker` 状态机及 `allow`/`recordFailure`/`recordSuccess`）
- 关键测试：`internal/logic/flashsale/circuit_breaker_test.go` 的 `TestCircuitBreakerStateMachineRace`（16 goroutine 并发读写）+ `TestCircuitBreakerStateMachineTransitions`（确定性状态迁移）
- 门禁入口：`scripts/test-race.sh`（`make test-race`）→ `go test -race -count=1 -run 'TestCircuitBreakerStateMachine' ./internal/logic/flashsale/...`
- 基线验证：`go test -race -count=1 -run TestCircuitBreakerStateMachine ./internal/logic/flashsale/...` → PASS（exit 0）
- 可选 Mutation：临时注释掉 `circuit_breaker.go` 三处 `cb.mu.Lock()` 与 `defer cb.mu.Unlock()`
- 预期失败：`go test -race ... -run TestCircuitBreakerStateMachineRace` → 多行 `WARNING: DATA RACE` → `--- FAIL` → exit 1
- 恢复确认：撤销 Mutation 后重跑同命令 → PASS

## CL-002：Registry 门禁能捕获全局资源漂移

- Owner 需要理解：错误码域与 migration version 是跨任务全局唯一资源，重复或漂移会导致运行时语义冲突/迁移失败；`check-registry.sh` 并入 `make test` 后，任一冲突/漂移会使门禁非零退出，从而在构建/发布前拦下问题。
- 生产代码：`scripts/check-registry.sh`（只读校验，读 `.agent/registry/{error-codes,migrations}.md` ↔ `internal/codes/codes.go` / `internal/migrations/sql/*`）
- 关键测试：无独立测试文件，门禁自身即断言（脚本非零退出）
- 门禁入口：`scripts/test.sh`（`make test`）在 `go test` 后调用 `check-registry.sh`
- 基线验证：`bash scripts/check-registry.sh` → exit 0（`校验通过`）
- 可选 Mutation：向 `.agent/registry/migrations.md` 追加一行重复 version（如 `20261001000001`），或向 `internal/codes/codes.go` 注入一个不在任何域内的错误码（如 `99999`）
- 预期失败：`bash scripts/check-registry.sh` → `[FAIL] migration version ... 重复` 或 `[FAIL] 错误码 ... 不在任何 ACTIVE/RESERVED 域内` → exit 1
- 恢复确认：撤销 Mutation 后重跑同命令 → exit 0
