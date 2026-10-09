# 测试环境 Prometheus 镜像：将抓取配置与 recording rules 内置进镜像。
#
# 为什么不用 docker-compose 的 bind mount？
#   本仓库运行在 rootless / 嵌套容器环境下，bind mount 单个文件会退化为目录挂载
#   （文件在容器内变成目录），导致 Prometheus 启动时 runc 报 "not a directory"。
#   改为构建期 COPY 进镜像，彻底绕开运行时 bind mount 限制。
FROM prom/prometheus:v2.53.0
COPY prometheus.yml /etc/prometheus/prometheus.yml
COPY rules/ /etc/prometheus/rules/
