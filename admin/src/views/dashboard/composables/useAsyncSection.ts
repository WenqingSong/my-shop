import { ref, type Ref } from "vue";
import { BusinessError } from "@/api/types/envelope";

/** 分区错误状态 */
export interface SectionError {
  /** 展示文案 */
  message: string;
  /** 是否为权限不足（HTTP 403） */
  forbidden: boolean;
}

/**
 * 工作台分区异步状态管理。
 *
 * 设计目的：让每个统计区域独立拥有 `loading / error / data`，
 * 一个接口失败不会影响其它区域（局部失败隔离）。
 *
 * - `load()` 内部吞掉异常，只写入 `error`，因此永远不会产生未处理的 rejection；
 * - 403 单独标记为 `forbidden`，页面展示"权限不足"而不是"加载失败"，
 *   且**不触发**登录态清理（与 401 区分）。
 */
export function useAsyncSection<T>(loader: () => Promise<T>) {
  const data = ref(null) as Ref<T | null>;
  const loading = ref(false);
  const error = ref<SectionError | null>(null);
  /** 至少成功加载过一次（用于区分"空数据"与"尚未加载"） */
  const loaded = ref(false);

  function toSectionError(cause: unknown): SectionError {
    if (cause instanceof BusinessError) {
      return {
        message: cause.serverMessage || "加载失败，请稍后重试",
        forbidden: false
      };
    }
    const status = (cause as { response?: { status?: number } })?.response
      ?.status;
    if (status === 403) {
      return { message: "权限不足，无法查看该数据", forbidden: true };
    }
    if (status === 401) {
      return { message: "登录已过期，请重新登录", forbidden: false };
    }
    if (status) {
      return { message: `加载失败（HTTP ${status}）`, forbidden: false };
    }
    return { message: "网络异常，请稍后重试", forbidden: false };
  }

  async function load(): Promise<void> {
    loading.value = true;
    error.value = null;
    try {
      data.value = await loader();
      loaded.value = true;
    } catch (cause) {
      error.value = toSectionError(cause);
    } finally {
      loading.value = false;
    }
  }

  return { data, loading, error, loaded, load };
}
