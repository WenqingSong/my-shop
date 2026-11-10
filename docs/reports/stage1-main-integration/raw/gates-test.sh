#!/usr/bin/env bash
# 部署体系测试（test/deployk8s 使用 syscall.Flock，只能在 Linux 运行）
set -u
export PATH=/usr/local/go/bin:$PATH
cd /src
echo "== go test ./test/deployk8s/... ./test/manifest/... =="
go test ./test/deployk8s/... ./test/manifest/...
echo "TEST_EXIT=$?"
