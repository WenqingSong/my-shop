#!/usr/bin/env bash
# 两阶段初始化第一阶段（make init）：准备本地依赖容器与 .env。
# 本阶段不要求七牛凭据、不启动 Go 后端、不在终端交互输入 Secret。
# 幂等：不覆盖已有 .env、不破坏已运行的容器与已有数据，可重复执行。
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib.sh"

log_info "检查本地依赖（Docker / Docker Compose）..."
if ! command -v docker >/dev/null 2>&1; then
  log_error "未找到 docker，请先安装 Docker"
  exit 1
fi
# docker_compose 封装内部会校验 docker compose / docker-compose 可用性，不可用时自动报错退出。
docker_compose version >/dev/null 2>&1

log_info "启动依赖容器（MySQL、Redis）..."
docker_compose up -d mysql redis
wait_for_deps

ENV_FILE="${ROOT_DIR}/.env"
ENV_EXAMPLE="${ROOT_DIR}/.env.example"
if [[ -f "${ENV_FILE}" ]]; then
  log_info "检测到已有 ${ENV_FILE}，保留不覆盖（如需重置请手动删除后重跑）"
else
  if [[ -f "${ENV_EXAMPLE}" ]]; then
    cp "${ENV_EXAMPLE}" "${ENV_FILE}"
    log_info "已从 .env.example 生成 ${ENV_FILE}"
  else
    log_warn "未找到 .env.example，跳过生成 .env"
  fi
fi

# ---------------------------------------------------------------------------
# 缺项检测与必填提示（只提示、不改写 .env、不打印任何已填 Secret 值）
# ---------------------------------------------------------------------------

# 提取 .env / .env.example 中的键名（仅未注释的 `KEY=...` 行），每行一个。
_env_keys() {
  local file="$1" line key
  [[ -f "${file}" ]] || return 0
  while IFS= read -r line || [[ -n "${line}" ]]; do
    line="${line%%#*}"                             # 去掉行内注释
    line="${line#"${line%%[![:space:]]*}"}"        # 去掉前导空白
    line="${line%"${line##*[![:space:]]}"}"        # 去掉尾部空白
    [[ -z "${line}" ]] && continue
    [[ "${line}" == *=* ]] || continue
    key="${line%%=*}"
    key="${key%"${key##*[![:space:]]}"}"
    printf '%s\n' "${key}"
  done < "${file}"
}

# 读取 .env 中某键的值（键不存在或空值均输出空串）。
_env_value() {
  local file="$1" target="$2" line key val
  [[ -f "${file}" ]] || { printf ''; return 0; }
  while IFS= read -r line || [[ -n "${line}" ]]; do
    line="${line%%#*}"
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    [[ -z "${line}" ]] && continue
    [[ "${line}" == *=* ]] || continue
    key="${line%%=*}"
    key="${key%"${key##*[![:space:]]}"}"
    if [[ "${key}" == "${target}" ]]; then
      val="${line#*=}"
      printf '%s\n' "${val}"
      return 0
    fi
  done < "${file}"
  printf ''
}

# 必填配置清单（开发环境）：七牛 4 项 required + 超级管理员初始密码。
# MySQL/Redis/JWT secret/超管用户名等均有 config.yaml 开发默认值，不强制在 .env 填写。
REQUIRED_VARS=(QINIU_ACCESS_KEY QINIU_SECRET_KEY QINIU_BUCKET QINIU_DOMAIN ADMIN_SUPER_PASSWORD)

missing_keys=""
if [[ -f "${ENV_EXAMPLE}" && -f "${ENV_FILE}" ]]; then
  # 对比 .env.example 与既有 .env：找出旧 .env 缺少的配置键（新增配置项）。
  missing_keys="$(comm -23 <(_env_keys "${ENV_EXAMPLE}" | sort -u) <(_env_keys "${ENV_FILE}" | sort -u) || true)"
fi

empty_required=()
for v in "${REQUIRED_VARS[@]}"; do
  if [[ -z "$(_env_value "${ENV_FILE}" "${v}")" ]]; then
    empty_required+=("${v}")
  fi
done

cat <<EOF

============================================================
第一阶段完成：依赖容器已就绪，.env 已就绪（若首次创建）。
============================================================

EOF

if [[ -n "${missing_keys}" ]]; then
  log_warn "检测到旧 .env 缺少以下配置键（模板已更新，请按需补齐）："
  printf '%s\n' "${missing_keys}" | sed 's/^/  - /'
  echo
fi

if [[ "${#empty_required[@]}" -gt 0 ]]; then
  log_warn "以下必填配置在 .env 中当前为空，请填写后再执行 make up："
  for v in "${empty_required[@]}"; do
    case "${v}" in
      QINIU_ACCESS_KEY)    printf '  - QINIU_ACCESS_KEY    七牛 AccessKey（与 SecretKey 成对注入）\n' ;;
      QINIU_SECRET_KEY)    printf '  - QINIU_SECRET_KEY    七牛 SecretKey（机密）\n' ;;
      QINIU_BUCKET)        printf '  - QINIU_BUCKET        对象存储空间名\n' ;;
      QINIU_DOMAIN)        printf '  - QINIU_DOMAIN        对外访问域名，如 https://cdn.example.com\n' ;;
      ADMIN_SUPER_PASSWORD) printf '  - ADMIN_SUPER_PASSWORD 超级管理员初始密码（仅「数据库尚无超管、首次创建」时必填）\n' ;;
    esac
  done
  echo
fi

cat <<EOF
下一步：编辑 ${ENV_FILE} 填写必填配置后执行 make up 完成第二阶段启动（会校验七牛配置与超管创建条件）。

注意：AK/SK、JWT secret、超管密码等为机密，仅保存在本地 .env，绝不提交仓库、不进日志/响应。
EOF
