# my-shop Nginx 入口镜像（V2：静态站点 + API 反向代理）
#
# 为什么用多阶段构建？
#   前端产物必须在镜像内生成，否则 `docker compose build nginx` 依赖宿主机先手工构建 dist，
#   干净检出（CI/新环境）无法复现部署。
#
# 为什么不用 docker-compose 的 bind mount？
#   与 prometheus/Dockerfile 同一约束：本仓库运行在 rootless / 嵌套容器环境下，
#   bind mount 单个文件会退化为目录挂载（文件在容器内变成目录），导致 Nginx 启动失败。
#   改为构建期 COPY 进镜像，彻底绕开运行时 bind mount 限制。
#
# 版本选择：server ... resolve（upstream 运行时 DNS 再解析）需 nginx >= 1.27.3，
# 锁定具体 tag 保证可重复构建（1.27.3 为支持该特性的主line版本）。

# ---------- React 用户商城构建 ----------
FROM node:22-alpine AS web

WORKDIR /src

COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY web/ ./
RUN npm run build

# ---------- Vue 管理后台构建 ----------
FROM node:22-alpine AS admin

WORKDIR /src

# 与 admin/pnpm-lock.yaml 的 lockfileVersion 9 对齐，固定 pnpm 主版本保证可重复安装。
RUN corepack enable && corepack prepare pnpm@9.15.9 --activate

COPY admin/package.json admin/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY admin/ ./
RUN pnpm build

# ---------- 运行镜像 ----------
FROM nginx:1.27.3-alpine

COPY nginx/nginx.conf /etc/nginx/nginx.conf
COPY nginx/error/ /etc/nginx/error/

# 路径契约：/ → web，/console/ → console（见 nginx.conf）。
COPY --from=web /src/dist /usr/share/nginx/html/web
COPY --from=admin /src/dist /usr/share/nginx/html/console
