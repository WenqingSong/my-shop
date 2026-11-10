#!/usr/bin/env bash
# 阶段一 Git 集成回归门禁（在 golang:1.26 Linux 容器内执行）
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
echo "== scripts/test-unit.sh（官方单元测试门禁）=="
bash scripts/test-unit.sh
echo "UNIT_EXIT=$?"
echo "== 关键包 go test（Kafka/worker/flashsale）=="
go test ./internal/mq/... ./internal/worker/... ./internal/logic/flashsale/... 2>&1 | tail -10
echo "== done =="
