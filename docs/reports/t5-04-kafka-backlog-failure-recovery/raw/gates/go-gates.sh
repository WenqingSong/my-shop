#!/usr/bin/env bash
# T5-04 自动化门禁（在 golang:1.26 Linux 容器内执行；Windows 侧无法运行 bash 门禁脚本）
set -u
export PATH=/usr/local/go/bin:$PATH
cd /src
echo "== go version =="
go version
echo "== go build ./... =="
go build ./...
echo "BUILD_EXIT=$?"
echo "== go vet ./... =="
go vet ./...
echo "VET_EXIT=$?"
echo "== 官方单元测试门禁（scripts/test-unit.sh 等价命令）=="
go test \
  ./internal/auth/... \
  ./internal/config/... \
  ./internal/metrics/... \
  ./internal/logic/health/... \
  ./internal/logic/dashboard/... \
  ./internal/controller/health/... \
  ./internal/mq/... \
  ./internal/termination/... \
  ./internal/worker/... 2>&1 | tail -20
echo "UNIT_WHOLE_EXIT=$?"
go test -run 'TestValidateUsername|TestValidatePassword' ./internal/logic/iam/... 2>&1 | tail -5
echo "UNIT_IAM_EXIT=$?"
go test -run 'TestCircuitBreakerStateMachine|TestOrderOutcomeMapping|TestRequestedEvent|TestOutboxPublishBackoff|TestOutboxClaimToken|TestOutboxOptionsValidation|TestOutboxMessageContract|TestConsumerParseRequestedEvent|TestConsumerInvalidMessageGoesToDLQAndCommits|TestConsumerDLQFailureKeepsOffset|TestConsumerCommitFailureStopsProgress|TestConsumerIdentityMismatchGoesToDLQ|TestConsumerRetryBudgetExhaustedGoesToDLQ|TestConsumerDeadLetterFailureKeepsOffset|TestConsumerRetryThenSuccess|TestConsumerPersistenceFailureNeverDropsMessage|TestConsumerBackoffBounded|TestConsumerOptionsValidation|TestConsumerContextCancelReleasesRebalance' ./internal/logic/flashsale/... 2>&1 | tail -8
echo "UNIT_FLASHSALE_EXIT=$?"
go test -run 'TestYuanToCents|TestMapCategoryStatus|TestMapProductStatus|TestSkuStatusForProduct|TestSplitSliderImages|TestMapSkusInventoryDedup|TestOpenConnectionsRejectsEmptyPass|TestBuildDSNCharsetUTF8MB4' ./internal/logic/legacymigrate/... 2>&1 | tail -5
echo "UNIT_LEGACY_EXIT=$?"
echo "== done =="
