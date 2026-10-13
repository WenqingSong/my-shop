#!/usr/bin/env bash
# 构建后端二进制：统一 go build 参数与输出目录，保证与 Dockerfile builder 阶段行为一致。
#
# 单一事实源（与 Dockerfile 对齐）：
#   CGO_ENABLED=0     依赖全为纯 Go，静态编译，无 glibc/musl 依赖差异
#   -trimpath         去除本地绝对路径，产物可复现、不泄漏构建机路径
#   -ldflags="-s -w"  剥离符号表与 DWARF，与镜像内产物一致
#   输出目录 bin/，产物名 my-shop
#
# GOOS / GOARCH 可经环境变量覆盖，缺省为宿主机原生目标，保证产物可直接被
# migrate / config-check 等运维命令运行。Dockerfile 因目标为 Linux 容器而显式
# GOOS=linux；如需与镜像完全同平台的部署产物，执行 `GOOS=linux make build`。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

CGO_ENABLED="${CGO_ENABLED:-0}"
LDFLAGS="${LDFLAGS:--s -w}"
BUILD_DIR="${BUILD_DIR:-bin}"
BINARY="${BINARY:-my-shop}"

log_info() { printf '\033[32m[INFO]\033[0m %s\n' "$*"; }

cd "${ROOT_DIR}"
mkdir -p "${BUILD_DIR}"

# CGO_ENABLED 显式导出；GOOS/GOARCH 仅在显式设置时才导出，空值交给 go 使用原生目标。
export CGO_ENABLED
[[ -z "${GOOS:-}" ]]   || export GOOS
[[ -z "${GOARCH:-}" ]] || export GOARCH

log_info "go build -trimpath -ldflags=\"${LDFLAGS}\" -o ${BUILD_DIR}/${BINARY} .（CGO_ENABLED=${CGO_ENABLED}${GOOS:+ GOOS=${GOOS}}${GOARCH:+ GOARCH=${GOARCH}}）"

go build -trimpath -ldflags="${LDFLAGS}" -o "${BUILD_DIR}/${BINARY}" .

log_info "build complete: ${BUILD_DIR}/${BINARY}"
