# my-shop Nginx 反向代理镜像：配置与错误页经 COPY 内置进镜像。
#
# 为什么不用 docker-compose 的 bind mount？
#   与 prometheus/Dockerfile 同一约束：本仓库运行在 rootless / 嵌套容器环境下，
#   bind mount 单个文件会退化为目录挂载（文件在容器内变成目录），导致 Nginx 启动失败。
#   改为构建期 COPY 进镜像，彻底绕开运行时 bind mount 限制。
#
# 版本选择：server ... resolve（upstream 运行时 DNS 再解析）需 nginx >= 1.27.3，
# 锁定具体 tag 保证可重复构建（1.27.3 为支持该特性的主line版本）。
FROM nginx:1.27.3-alpine

COPY nginx.conf /etc/nginx/nginx.conf
COPY error/ /etc/nginx/error/
