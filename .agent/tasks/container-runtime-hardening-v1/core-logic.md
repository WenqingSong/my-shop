# Owner Core Logic 验证卡

> 本文件由 Cleaner 在 CLEAN 后整理，承载「Owner 应理解并亲自验证」的核心机制、因果说明与验证证据。Owner 决策状态由 OwnerGate 持久化，不在此记录。

## CL-001：`/ready` readiness 判定（fail-closed + 探测超时 + 结果缓存）

- Owner 需要理解：`/ready` 是未来 K8s readinessProbe 的摘流依据——DB 或 Redis 任一不可达必须返回 503（fail-closed，把实例从流量中摘除），而不是假装就绪；且高频探针不能每次都真打库/打缓存（用超时 + 进程内缓存 TTL 抑制探测负载）。若 fail-closed 被改成 fail-open，DB/Redis 故障时实例仍被调度流量，导致请求打到坏实例上。
- 生产代码：`internal/logic/health/health.go`（`Readiness`、`probeReady`、`probeDependencies`、`readinessCacheTTL`/`readinessProbeTimeout`）；错误码 `codes.CodeServiceUnavailable(1005→503)` 经 `internal/middleware/response.go` 统一写回。
- 关键测试：
  - `TestReadinessNotReadyReturns503`（`internal/logic/health/health_test.go`）——依赖不可达 → 错误码 1005。
  - `TestReadinessCacheAvoidsReprobe`（同文件）——缓存窗口内重复调用只探测一次。
  - `TestProbeReadyTimeout`（同文件）——探测阻塞时在超时内返回 false。
- 基线验证：`cd /root/projects/my-shop && go test -count=1 ./internal/logic/health/...` → 全部 PASS。
- 可选 Mutation：把 `Readiness` 里 `if s.readyValid && time.Since(s.readyAt) < ttl { ... }` 这段缓存短路删除（每次调用都重新探测）。
- 预期失败：`TestReadinessCacheAvoidsReprobe` 会因探测次数 `calls.Load() != 1` 而失败（证明缓存确实抑制了重复探测）。
- 恢复确认：撤销 Mutation 后重跑 `go test -count=1 ./internal/logic/health/...` → 恢复 PASS。

## CL-002：优雅关闭与后台扫描器/消费停机一致性（SIGTERM/SIGINT）

- Owner 需要理解：停机时 `signal.NotifyContext(SIGINT, SIGTERM)` 作为根 ctx 取消三个后台扫描器（订单超时取消、秒杀对账、秒杀消费），使它们停止领取新任务；在途的单事务由数据库原子性保证「整体回滚或完成」，消费被打断时请求保持 `queued`、重启后重投，不产生半成品或重复下单。若扫描器不接 ctx（忽略取消），停机时 goroutine 永不退出，进程无法干净收尾。
- 生产代码：`internal/cmd/cmd.go`（`serve` 的 `signalCtx`/`wg.Wait`、`startOrderCancelScanner`/`startFlashSaleReconcileScanner`/`startFlashSaleConsumeScanner` 的 `select { case <-ctx.Done(): return }`）；`internal/logic/flashsale/consume.go`（`consumeOne` 单事务 + `consumeBeforeCommitHook`）。
- 关键测试：
  - `TestScannersStopOnContextCancel`（`internal/cmd/scanner_test.go`）——取消后 `wg.Wait` 在限定时间内返回。
  - `TestConsumeInterruptedByCtxCancel`（`internal/logic/flashsale/consume_test.go`）——事务进行中取消 → 回滚、请求仍 `queued`、无订单不扣库存、重投恰一单。
- 基线验证：`cd /root/projects/my-shop && export DATABASE_DEFAULT_PASS=root && go test -count=1 -run 'TestScannersStopOnContextCancel' ./internal/cmd/... && go test -count=1 -run 'TestConsumeInterruptedByCtxCancel' ./internal/logic/flashsale/...` → 全部 PASS。
- 可选 Mutation：把 `startOrderCancelScanner` 等三处循环里的 `case <-ctx.Done(): return` 分支删除（扫描器忽略 ctx 取消）。
- 预期失败：`TestScannersStopOnContextCancel` 会因 `wg.Wait` 无法返回而在 5s 后超时失败（证明停机收尾依赖 ctx 取消）。
- 恢复确认：撤销 Mutation 后重跑 `TestScannersStopOnContextCancel` → 恢复 PASS。
