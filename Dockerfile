# =============================================================================
# my-shop 后端多阶段构建
#
# 阶段一（builder）：Go 1.23 + CGO_ENABLED=0 静态编译（依赖全为纯 Go）。
# 阶段二（runtime）：alpine + tzdata（Asia/Shanghai 时区）+ ca-certificates（七牛等出站 HTTPS）。
#
# 凭据安全：镜像内仅打包「已清空敏感默认值」的 manifest/config/config.yaml，
# .env、真实 AK/SK、JWT secret、超管密码、七牛密钥等一律经运行时环境变量注入，
# 不进构建上下文（.dockerignore 排除 .env/bin/tmp/storage）与镜像层。
# =============================================================================

# ---------------------------------------------------------------------------
# 构建阶段
# ---------------------------------------------------------------------------
FROM golang:1.23-alpine AS builder

WORKDIR /src

# 先拷贝依赖清单，利用 Docker 层缓存；模块为纯 Go，可离线/联网均可行。
COPY go.mod go.sum ./
RUN go mod download

# 拷贝源码并静态编译（migration 经 //go:embed 内嵌，无需额外拷贝 SQL）。
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/my-shop .

# ---------------------------------------------------------------------------
# 运行阶段
# ---------------------------------------------------------------------------
FROM alpine:3.20

# tzdata 提供 time.LoadLocation("Asia/Shanghai") 所需的时区数据；
# ca-certificates 提供七牛云对象存储 HTTPS 调用所需的根证书。
RUN apk add --no-cache tzdata ca-certificates

# 创建专用非 root 运行用户 app:app（固定 uid=10001/gid=10001，便于未来 K8s SecurityContext 对接）。
# 不使用 alpine nobody：不同基础镜像 UID/GID 行为差异，固定身份可预测。
RUN addgroup -g 10001 app && adduser -D -u 10001 -G app app

WORKDIR /app

COPY --from=builder /out/my-shop /app/my-shop
# 镜像默认配置路径（GoFrame 默认搜索 manifest/config/config.yaml，相对 /app）。
COPY manifest/config/config.yaml /app/manifest/config/config.yaml
COPY scripts/docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh

# 本地文件存储根目录挂载点（app_storage named volume）。
# 提前 chown 给运行用户：named volume 首次挂载会继承镜像目录 owner，保证非 root 可写 /app/storage。
RUN mkdir -p /app/storage && chown -R app:app /app

# 非 root 运行：主进程有效 UID 非 0（容器运行时加固，K8s runAsNonRoot 可直接对接）。
USER 10001:10001

EXPOSE 8000

# entrypoint：migrate up（幂等 + GET_LOCK 串行）→ exec serve（复用 serve 自身 fail-fast）。
ENTRYPOINT ["/app/docker-entrypoint.sh"]
