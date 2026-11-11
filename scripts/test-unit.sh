#!/usr/bin/env bash
# 单元测试入口（make test-unit）：仅运行不依赖 MySQL/Redis/Kafka/七牛/Kubernetes 的纯内存测试。
#
# 环境隔离：LIB_SKIP_ENV=1 跳过 .env 加载（同 test.sh），不注入 DATABASE_DEFAULT_PASS、
# 不启动/依赖任何容器，测试在干净环境变量下运行，结果与本地 .env 真实凭据无关。
#
# 拆分依据：仓库大部分测试为集成测试（启动完整 app + 真实 MySQL/Redis），仅有部分纯内存测试，
# 且部分包同时包含纯内存与集成测试。本脚本用「整包 go test + -run 精确匹配」枚举纯内存测试：
#   - 整包纯内存：auth / config / logx / metrics / logic/health / logic/dashboard /
#     controller/health / mq / termination / worker / logic/pricing / logic/categories /
#     logic/upload / logic/address / logic/profile；
#     config 包（T2-01 统一配置层）用临时 YAML + 独立 gcfg 实例 + t.Setenv 验证优先级、
#     类型解析与严格模式，不依赖任何外部服务。
#   - 混合包纯内存：iam（用户名/密码校验）、flashsale（熔断器状态机 / 结果映射 / Kafka 消息契约 /
#     消费循环生命周期）、legacymigrate（金额/状态映射 / 旧地址拆分 / SKU 指纹 / DSN 构建拒绝）、
#     migrations（DSN 构建 / charset / 迁移文件成对性）、middleware（RequestID 中间件）、
#     sku（规格指纹 / SKU 编码 / 笛卡尔积）、regiondata（地区数据文件完整性与校验器）、
#     cmd（路由契约 / 迁移前置校验顺序 / HTTP 关闭语义）、boot（配置覆盖与 DST 判定）、
#     bargain（砍价金额与入参校验）、wallet（金额校验与分页/文案映射）、freight（运费计算与规则匹配）。
#
# 执行约定：
#   - 每个包使用 -count=1，禁用测试结果缓存，保证每次都是真实执行。
#   - 测试失败立即以非零退出码终止（set -euo pipefail），不吞掉任何 go test 退出状态。
#   - 新增纯内存测试时必须在此登记；依赖 MySQL/Redis/Kafka 等外部服务的用例一律由
#     test-integration.sh / test-race.sh / test-kafka-e2e.sh / test-migration.sh 覆盖。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LIB_SKIP_ENV=1
source "${SCRIPT_DIR}/lib.sh"

cd "${ROOT_DIR}"

UNIT_GROUPS=0

# run_unit <范围说明> <go test 参数...>
# 打印执行范围并执行 go test；失败时打印明确的失败范围并以非零状态返回。
run_unit() {
  local scope="$1"
  shift
  printf '\033[32m[INFO]\033[0m go test %s\n' "${scope}"
  if ! go test -count=1 "$@"; then
    printf '\033[31m[FAIL]\033[0m 单元测试失败：%s\n' "${scope}" >&2
    return 1
  fi
  UNIT_GROUPS=$((UNIT_GROUPS + 1))
}

# run_unit_match <范围说明> <go test 参数...>
# 用于 -run 过滤的混合包：除退出码外，还要求至少出现一条顶层 "--- PASS"。
# 防止 -run 表达式失效/用例改名后 go test 以「no tests to run」返回 0，制造空跑 PASS。
run_unit_match() {
  local scope="$1"
  shift
  printf '\033[32m[INFO]\033[0m go test %s\n' "${scope}"
  local output
  if ! output="$(go test -count=1 -v "$@" 2>&1)"; then
    printf '%s\n' "${output}"
    printf '\033[31m[FAIL]\033[0m 单元测试失败：%s\n' "${scope}" >&2
    return 1
  fi
  printf '%s\n' "${output}"
  if ! printf '%s\n' "${output}" | grep -q '^--- PASS: '; then
    printf '\033[31m[FAIL]\033[0m -run 未匹配到任何用例（空跑）：%s\n' "${scope}" >&2
    return 1
  fi
  UNIT_GROUPS=$((UNIT_GROUPS + 1))
}

log_info "单元测试范围：纯内存包 + 混合包中的纯内存用例（不依赖 MySQL/Redis/Kafka/七牛/K8s）"

log_info "整包纯内存测试"
run_unit "./internal/auth/...（JWT / 刷新令牌 / 会话键）" ./internal/auth/...
run_unit "./internal/config/...（统一配置层：优先级 / 类型解析 / 严格模式）" ./internal/config/...
run_unit "./internal/logx/...（结构化日志字段渲染）" ./internal/logx/...
run_unit "./internal/metrics/...（Prometheus 指标口径）" ./internal/metrics/...
run_unit "./internal/logic/health/...（探针与就绪缓存）" ./internal/logic/health/...
run_unit "./internal/logic/dashboard/...（后台看板参数解析 / DST 判定）" ./internal/logic/dashboard/...
run_unit "./internal/controller/health/...（健康检查 HTTP 状态）" ./internal/controller/health/...
run_unit "./internal/mq/...（Kafka 配置 / 生命周期 / 生产结果）" ./internal/mq/...
run_unit "./internal/termination/...（终止预算契约）" ./internal/termination/...
run_unit "./internal/worker/...（秒杀运行时装配与生命周期）" ./internal/worker/...
run_unit "./internal/logic/pricing/...（统一计价：优惠边界 / 溢出 / 守恒）" ./internal/logic/pricing/...
run_unit "./internal/logic/categories/...（分类校验 / 层级 / 树构建）" ./internal/logic/categories/...
run_unit "./internal/logic/upload/...（上传参数与凭据校验）" ./internal/logic/upload/...
run_unit "./internal/logic/address/...（地址字段校验）" ./internal/logic/address/...
run_unit "./internal/logic/profile/...（生日解析 / URL 校验）" ./internal/logic/profile/...

log_info "混合包中的纯内存测试（-run 精确匹配）"
run_unit_match "./internal/logic/iam/...（用户名 / 密码校验）" \
  -run 'TestValidateUsername|TestValidatePassword' ./internal/logic/iam/...

# B3 Consumer 的纯内存用例：消息解析/死信分类、位移提交与 AllowRebalance 生命周期、
# 重试预算与退避、Poll 错误路径。需要 MySQL/Redis 的消费事务用例见 test-integration.sh / test-race.sh。
FLASH_SALE_UNIT_TESTS='TestCircuitBreakerStateMachine|TestOrderOutcomeMapping|TestRequestedEvent'
FLASH_SALE_UNIT_TESTS+='|TestKafkaDocFreezesIdempotencyAndPartitionContract'
FLASH_SALE_UNIT_TESTS+='|TestOutboxPublishBackoff|TestOutboxClaimToken|TestOutboxOptionsValidation'
FLASH_SALE_UNIT_TESTS+='|TestOutboxMessageContract|TestOutboxMessageKeyStrategy'
FLASH_SALE_UNIT_TESTS+='|TestConsumerParseRequestedEvent|TestConsumerInvalidMessageGoesToDLQAndCommits'
FLASH_SALE_UNIT_TESTS+='|TestConsumerDLQFailureKeepsOffset|TestConsumerCommitFailureStopsProgress'
FLASH_SALE_UNIT_TESTS+='|TestConsumerIdentityMismatchGoesToDLQ|TestConsumerRetryBudgetExhaustedGoesToDLQ'
FLASH_SALE_UNIT_TESTS+='|TestConsumerDeadLetterFailureKeepsOffset|TestConsumerRetryThenSuccess'
FLASH_SALE_UNIT_TESTS+='|TestConsumerPersistenceFailureNeverDropsMessage|TestConsumerBackoffBounded'
FLASH_SALE_UNIT_TESTS+='|TestConsumerOptionsValidation|TestConsumerContextCancelReleasesRebalance'
FLASH_SALE_UNIT_TESTS+='|TestConsumerPollErrorReleasesRebalance'
run_unit_match "./internal/logic/flashsale/...（熔断器 / 结果映射 / Kafka 契约 / 消费循环生命周期）" \
  -run "${FLASH_SALE_UNIT_TESTS}" ./internal/logic/flashsale/...

# B1-03/04 遗留数据迁移的纯内存用例：金额与状态映射、旧地址拆分、SKU 指纹、DSN 构建拒绝。
run_unit_match "./internal/logic/legacymigrate/...（映射 / 拆分 / 指纹 / DSN 校验）" \
  -run 'TestYuanToCents|TestSpecSignature|TestSplitLegacyAddress|TestMapLegacyPayType|TestMapCategoryStatus|TestMapProductStatus|TestSkuStatusForProduct|TestSplitSliderImages|TestMapSkusInventoryDedup|TestOpenConnectionsRejectsEmptyPass|TestBuildDSNCharsetUTF8MB4' \
  ./internal/logic/legacymigrate/...

# 迁移包的纯内存用例：DSN 构建拒绝空密码 / charset，迁移文件 up/down 成对性。
# 真实 MySQL 的迁移语义（加锁 / 并发 / 中途终止）由 test-migration.sh 覆盖。
run_unit_match "./internal/migrations/...（DSN 校验 / 迁移文件成对性）" \
  -run 'TestBuildDSNRejectsEmptyPass|TestBuildDSNCharsetUTF8MB4|TestMarketingMigrationFilesArePaired|TestB103MigrationFilesArePaired|TestSkuCodePricingMigrationFilesArePaired|TestTradeFundMigrationFilesArePaired' \
  ./internal/migrations/...

run_unit_match "./internal/middleware/...（RequestID 中间件）" \
  -run 'TestRequestID' ./internal/middleware/...

# B1-04-02：规格组合指纹 / SKU 编码派生 / 笛卡尔积（纯内存，不连接 DB）。
run_unit "./internal/logic/sku/...（规格指纹 / SKU 编码 / 组合生成）" ./internal/logic/sku/...

# B1-04-FIX-02：地区数据文件完整性、来源元信息与结构校验器（纯内存，不连接 DB）。
# 真实 MySQL 的幂等初始化用例（TestEnsureIntegration*）由 test-integration.sh 覆盖。
run_unit_match "./internal/regiondata/...（地区数据完整性与校验器）" \
  -run 'TestDatasetIntegrity|TestMetadataSource|TestValidateRejectsBrokenData' ./internal/regiondata/...

# T2-03 / T2-03.1：B4.1 WithoutCancel 语义、drain 预算与强制关闭路径（纯内存，不连接 DB/Redis）。
# T2-05：migrate 子命令的前置配置校验顺序（严格模式/密码/本地模式，端口不可达即可判定顺序）。
# B1-02：150 条常驻路由锁定 + 路由↔OpenAPI 双向比对 + 契约生成器（纯内存，不连接 DB/Redis）。
# 注：扫描器 ctx 取消与 B4 秒杀装配/互斥/恢复用例已随 ./internal/worker/... 整包执行。
run_unit_match "./internal/cmd/...（HTTP 关闭语义 / 迁移前置校验 / 路由契约）" \
  -run 'TestRuntimeContextDetachesSignalCancellation|TestForceCloseWindow|TestTrackedListener|TestStopHTTPServer|TestMigrate|TestRoute|TestContractPlannedNotRouted' \
  ./internal/cmd/...

# T2-01 / T2-03：配置环境变量覆盖、空密码拒绝、依赖超时、时区 DST 判定（纯内存）。
# boot 包其余用例会连接真实 MySQL 初始化 schema/seed，由 test-integration.sh 覆盖。
run_unit_match "./internal/boot/...（配置覆盖 / 空密码拒绝 / DST 判定）" \
  -run 'TestConfigStringEnvOverride|TestApplyDatabaseConfigEnvOverride|TestApplyDatabaseConfigRejectsEmptyPass|TestDependencyTimeoutEnvOverride|TestDatabaseTimeZoneExtra|TestHasDST|TestApplyServerConfigGracefulTimeoutEnvOverride|TestApplyRedisConfigEnvOverride' \
  ./internal/boot/...

# B1-04-07：砍价金额计算与入参校验（纯内存）；活动/助力事务用例需 MySQL，见 test-integration.sh。
run_unit_match "./internal/logic/bargain/...（砍价金额与入参校验）" \
  -run 'TestCalcCutAmount|TestHelpsRequired|TestNormalizeActivityInput|TestStatusToString|TestParsePagination' \
  ./internal/logic/bargain/...

# B1-04-04：钱包金额校验、订单号确定性、幂等键与分页/文案映射（纯内存）；
# 资金事务用例需 MySQL，见 wallet_integration_test.go / test-integration.sh。
run_unit_match "./internal/logic/wallet/...（金额校验 / 幂等键 / 分页与文案）" \
  -run 'TestValidateRechargeAmount|TestRechargeOrderNoDeterministic|TestIdempotencyKeys|TestNormalizePaging|TestReplayFundResult|TestDirectionAndStatusText|TestValidTxType|TestMaxWalletBalanceMatchesUnsignedInt' \
  ./internal/logic/wallet/...

# B1-04-03：按件计费、区域规则回退、包邮阈值与溢出（纯内存）；模板/区域规则落库用例需 MySQL。
run_unit_match "./internal/logic/freight/...（运费计算 / 区域规则匹配 / 包邮阈值）" \
  -run 'TestFeeByPieces|TestMatchRule|TestComputeShippingFee|TestGroupItems|TestEffectiveThreshold' \
  ./internal/logic/freight/...

# B1-02：OpenAPI 契约生成器（纯内存，不启动服务）。
run_unit "./tools/openapi-gen/...（OpenAPI 契约生成器）" ./tools/openapi-gen/...

log_info "单元测试全部通过（共 ${UNIT_GROUPS} 组执行单元，均已实际运行，未使用缓存）"
