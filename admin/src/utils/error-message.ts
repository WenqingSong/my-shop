import { BusinessError } from "@/api/types/envelope";
import type { PureHttpError } from "@/utils/http/types";

/**
 * 把请求异常归一化为可展示的中文文案。
 *
 * 优先级：
 * 1. 后端业务错误（HTTP 可能仍是 200）→ 直接使用后端 message；
 * 2. HTTP 401 / 403 → 登录失效 / 权限不足；
 * 3. 其它 HTTP 状态 → 通用文案；
 * 4. 本地抛出的 Error（如上传失败）→ 其 message；
 * 5. 兜底文案。
 *
 * 仅用于 UI 展示，不做任何重试或状态回滚决策。
 */
export function resolveErrorMessage(
  cause: unknown,
  fallback = "操作失败，请稍后重试"
): string {
  if (cause instanceof BusinessError) {
    return cause.serverMessage || fallback;
  }

  const httpError = cause as PureHttpError | undefined;
  const status = httpError?.response?.status;

  // 认证 / 授权失败优先使用统一文案，避免把后端实现细节暴露给用户
  if (status === 401) return "登录已过期，请重新登录";
  if (status === 403) return "没有权限执行该操作";

  // 其余非 2xx 后端错误：语义在 envelope 里（如「库存不足」「有子分类不能删除」）
  if (httpError?.businessMessage) return httpError.businessMessage;

  if (status === 404) return "目标资源不存在";
  if (status === 409) return "数据状态冲突，请刷新后重试";
  if (status) return `请求失败（HTTP ${status}）`;

  if (cause instanceof Error && cause.message) return cause.message;

  return fallback;
}

/** 判断异常是否为"权限不足"（HTTP 403） */
export function isForbiddenError(cause: unknown): boolean {
  return (cause as PureHttpError | undefined)?.response?.status === 403;
}
