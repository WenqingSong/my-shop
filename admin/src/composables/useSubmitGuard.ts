import { readonly, ref } from "vue";

/** `run` 的返回值：`executed = false` 表示本次调用被并发锁拦截 */
export type SubmitGuardResult<T> =
  | { executed: true; value: T }
  | { executed: false; value: undefined };

/**
 * 提交防抖锁。
 *
 * 只解决"用户在请求进行中重复点击"的问题：
 * - 第一次调用立即执行；
 * - 请求进行中的重复调用直接返回 `executed: false`，**不会**再次发请求；
 * - 任务结束（成功或失败）后自动解锁。
 *
 * 不负责业务校验、不吞异常：任务的异常原样抛给调用方。
 */
export function useSubmitGuard() {
  const submitting = ref(false);

  async function run<T>(task: () => Promise<T>): Promise<SubmitGuardResult<T>> {
    if (submitting.value) {
      return { executed: false, value: undefined };
    }

    submitting.value = true;
    try {
      const value = await task();
      return { executed: true, value };
    } finally {
      submitting.value = false;
    }
  }

  return { submitting: readonly(submitting), run };
}
