# syntax=docker/dockerfile:1.7

# =============================================================================
# SurgeCart 后端镜像（T2-04 Docker 镜像与容器安全加固）
#
# 目标：
#   1. 构建阶段与运行阶段严格隔离：生产层只含静态二进制、配置、入口脚本与
#      必要运行时数据（tzdata / CA），不含 Go 工具链、源码、测试与文档；
#   2. 基础镜像按 patch 版本固定，不使用 latest 或浮动 minor 标签；
#      实际解析到的 digest 记录在 docs/development/container-security.md；
#   3. 运行阶段非 root（固定 UID/GID 10001），只读根文件系统可运行：
#      仅 /app/storage（持久化卷）与 /tmp（tmpfs）可写；
#   4. 入口保持 `exec /app/my-shop serve`，Go 进程直接作为 PID 1 接收 SIGTERM，
#      不引入 shell 中间层或额外 init 进程（单进程容器，无子进程需要回收）。
#
# 构建缓存：go mod download 与 go build 使用 BuildKit cache mount，
# 不使用 `latest` 镜像，也不把模块缓存写入最终镜像层。
# =============================================================================

ARG GO_IMAGE=golang:1.27.3-alpine3.24
ARG ALPINE_IMAGE=alpine:3.24.4

# ---------------------------------------------------------------------------
# 构建阶段
# ---------------------------------------------------------------------------
FROM ${GO_IMAGE} AS builder

# 固定构建环境：
#   - CGO_ENABLED=0：静态二进制，运行层不需要 glibc / libgcc；
#   - GOOS=linux：与发布平台一致；
#   - GOTOOLCHAIN=local：禁止构建期自动下载其它 Go 工具链（可复现、可控）；
#   - GOFLAGS=-mod=readonly：构建不得改写 go.mod / go.sum。
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOTOOLCHAIN=local \
    GOFLAGS=-mod=readonly \
    GO111MODULE=on

WORKDIR /src

# 依赖清单先入层：源码变更不会使 go mod download 层失效。
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

# 生产二进制：仅主包，不注入任何测试构建标签。
# -trimpath 去除宿主机路径；-buildvcs=false 避免构建目录缺少 .git 时产生非确定性；
# -ldflags="-s -w" 去除符号表与 DWARF（镜像内仍可通过独立源码调试）。
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/my-shop .

# 测试专用构建：仅 `--target lifecycle-verify` 时才会被求值，默认构建不会执行。
# 复用同一份源码、同一组编译参数，仅额外启用 serve_lifecycle 构建标签
# （测试专用阻塞路由 + 七牛 UC endpoint 覆盖），用于真实容器生命周期验证；
# 生产目标 runtime 不会包含该二进制。
FROM builder AS verify-builder
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -tags serve_lifecycle -trimpath -buildvcs=false -ldflags="-s -w" -o /out/my-shop ./test/serveproc

# ---------------------------------------------------------------------------
# 运行阶段基座（runtime 与 lifecycle-verify 共用，保证验证对象与生产镜像同构）
# ---------------------------------------------------------------------------
FROM ${ALPINE_IMAGE} AS runtime-base

# 先升级基础镜像自带的 patch 级软件包（如 zlib/musl），再安装运行时依赖：
# 基础镜像按 patch tag 固定，但仓库内已有同一 minor 分支的安全修订时及时吸收。
# tzdata：time.LoadLocation("Asia/Shanghai") 与业务时区边界依赖；
# ca-certificates：七牛对象存储等出站 HTTPS 的根证书。
# 同时创建固定 UID/GID 的非 root 运行用户；运行时不修改任何文件权限。
RUN apk upgrade --no-cache \
    && apk add --no-cache tzdata ca-certificates \
    && addgroup -g 10001 -S app \
    && adduser -D -H -u 10001 -G app -S -s /sbin/nologin app \
    && mkdir -p /app/manifest/config /app/storage \
    && chown app:app /app/storage \
    && chmod 0755 /app /app/manifest /app/manifest/config /app/storage

LABEL org.opencontainers.image.title="surgecart-my-shop" \
      org.opencontainers.image.description="SurgeCart backend (static CGO-free Go binary, non-root, read-only rootfs ready)"

WORKDIR /app

# 镜像内配置只含非敏感默认值（敏感字段为空，运行时由环境变量注入）。
# 注意：不使用 COPY --chmod 设置目录权限（Docker 会把 --chmod 同步到新建的父目录，
# 造成目录失去 x 位、非 root 无法遍历）；目录权限由上面的 RUN 显式固定。
COPY --chown=root:root manifest/config/config.yaml /app/manifest/config/config.yaml
COPY --chown=root:root scripts/docker-entrypoint.sh /app/docker-entrypoint.sh
# 二进制与配置不可被运行用户修改（非只读根文件系统时也避免持久化篡改）。
RUN chmod 0444 /app/manifest/config/config.yaml \
    && chmod 0555 /app/docker-entrypoint.sh

# 非 root 运行：固定数值身份，可直接对接 K8s runAsUser / runAsGroup / runAsNonRoot。
USER 10001:10001

EXPOSE 8000

# 镜像级健康检查（docker run 场景）：仅探测 /health 存活语义，不访问 MySQL/Redis/Kafka，
# 与 T2-02 冻结语义一致。Kubernetes 会忽略 Dockerfile HEALTHCHECK，改用 livenessProbe /
# readinessProbe；Compose 显式声明同值 healthcheck，二者由 scripts/test-container.sh 断言一致。
# 使用 alpine 自带 busybox wget，不额外引入 curl/wget 软件包。
HEALTHCHECK --interval=10s --timeout=5s --start-period=60s --retries=5 \
    CMD wget -q -O /dev/null http://127.0.0.1:8000/health || exit 1

# entrypoint：migrate up（幂等 + GET_LOCK 串行）→ exec serve（Go 进程成为 PID 1）。
ENTRYPOINT ["/app/docker-entrypoint.sh"]

# ---------------------------------------------------------------------------
# 验证镜像（测试专用；非默认目标）
#
# 仅该目标引用命名构建上下文 t204-certs（scripts/test-container-runtime.sh 生成的自签
# CA），用于信任容器内的七牛 Mock。默认目标 runtime 不引用该上下文，生产构建不依赖它。
# ---------------------------------------------------------------------------
FROM runtime-base AS lifecycle-verify
COPY --from=verify-builder --chown=root:root --chmod=0555 /out/my-shop /app/my-shop
# 只读根文件系统下无法在运行期创建目录；测试 CA 在构建期放入 /app/test-certs
# （仅验证镜像，生产目标 runtime 不含该目录）。
COPY --from=t204-certs server.crt /app/test-certs/ca.crt

# ---------------------------------------------------------------------------
# 生产运行阶段（默认目标，必须是最后一个 stage）
# ---------------------------------------------------------------------------
FROM runtime-base AS runtime
COPY --from=builder --chown=root:root --chmod=0555 /out/my-shop /app/my-shop
