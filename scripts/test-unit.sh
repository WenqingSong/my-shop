#!/usr/bin/env bash
# 单元测试入口（make test-unit）：仅运行不依赖 MySQL/Redis 的纯内存测试。
#
# 环境隔离：LIB_SKIP_ENV=1 跳过 .env 加载（同 test.sh），不注入 DATABASE_DEFAULT_PASS、
# 不启动/依赖任何容器，测试在干净环境变量下运行，结果与本地 .env 真实凭据无关。
#
# 拆分依据：仓库大部分测试为集成测试（启动完整 app + 真实 MySQL/Redis），仅有少数纯内存测试，
# 且部分包同时包含纯内存与集成测试。本脚本用「整包 go test + -run 精确匹配」枚举纯内存测试：
#   - 整包纯内存：auth / config / metrics / logic/health / logic/dashboard / controller/health / mq / termination / worker；
#     config 包（T2-01 统一配置层）用临时 YAML + 独立 gcfg 实例 + t.Setenv 验证优先级、
#     类型解析与严格模式，不依赖任何外部服务。
#   - 混合包纯内存：iam（用户名/密码校验）、flashsale（熔断器状态机 / 结果映射 / Kafka 消息契约）、
#     legacymigrate（金额/状态映射 / DSN 构建拒绝）、migrations（DSN 构建 / charset）、
#     middleware（RequestID 中间件）。
# 新增纯内存测试时应在此登记；集成测试一律由 test-integration.sh 覆盖。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

cd "${ROOT_DIR}"

log_info "go test：整包纯内存测试（无 MySQL/Redis）..."
go test \
  ./internal/auth/... \
  ./internal/config/... \
  ./internal/metrics/... \
  ./internal/logic/health/... \
  ./internal/logic/dashboard/... \
  ./internal/controller/health/... \
  ./internal/mq/... \
  ./internal/termination/... \
  ./internal/worker/...

log_info "go test：混合包中的纯内存测试（-run 精确匹配）..."
go test -run 'TestValidateUsername|TestValidatePassword' ./internal/logic/iam/...
# B3 Consumer 的纯内存用例：消息解析/死信分类、位移提交与 AllowRebalance 生命周期、
# 重试预算与退避。需要 MySQL/Redis 的消费事务用例见 test-integration.sh / test-race.sh。
go test -run 'TestCircuitBreakerStateMachine|TestOrderOutcomeMapping|TestRequestedEvent|TestOutboxPublishBackoff|TestOutboxClaimToken|TestOutboxOptionsValidation|TestOutboxMessageContract|TestConsumerParseRequestedEvent|TestConsumerInvalidMessageGoesToDLQAndCommits|TestConsumerDLQFailureKeepsOffset|TestConsumerCommitFailureStopsProgress|TestConsumerIdentityMismatchGoesToDLQ|TestConsumerRetryBudgetExhaustedGoesToDLQ|TestConsumerDeadLetterFailureKeepsOffset|TestConsumerRetryThenSuccess|TestConsumerPersistenceFailureNeverDropsMessage|TestConsumerBackoffBounded|TestConsumerOptionsValidation|TestConsumerContextCancelReleasesRebalance' ./internal/logic/flashsale/...
go test -run 'TestYuanToCents|TestMapCategoryStatus|TestMapProductStatus|TestSkuStatusForProduct|TestSplitSliderImages|TestMapSkusInventoryDedup|TestOpenConnectionsRejectsEmptyPass|TestBuildDSNCharsetUTF8MB4' ./internal/logic/legacymigrate/...
go test -run 'TestBuildDSNRejectsEmptyPass|TestBuildDSNCharsetUTF8MB4' ./internal/migrations/...
go test -run 'TestRequestID' ./internal/middleware/...
# T2-03 / T2-03.1：B4.1 WithoutCancel 语义、drain 预算与强制关闭路径（纯内存，不连接 DB/Redis）。
# T2-05：migrate 子命令的前置配置校验顺序（严格模式/密码/本地模式，端口不可达即可判定顺序）。
go test -run 'TestRuntimeContextDetachesSignalCancellation|TestForceCloseWindow|TestTrackedListener|TestStopHTTPServer|TestMigrate' ./internal/cmd/...
# 注：扫描器 ctx 取消与 B4 秒杀装配/互斥/恢复用例已随 ./internal/worker/... 整包执行，
# 不再指向 ./internal/cmd（该包没有同名用例，旧命令是空跑）。

log_info "单元测试全部通过"
