<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { ElMessageBox } from "element-plus";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { closeRefund, executeRefund, reviewRefund } from "@/api/modules/refund";
import {
  REFUND_ACTION_META,
  REFUND_REASON_MAX_LENGTH,
  validateRefundReason,
  type RefundActionMode,
  type RefundView
} from "@/api/adapters/refund-adapter";

/**
 * 退款操作弹窗（审核通过/拒绝、执行成功/失败、关闭）。
 *
 * 所有状态推进都调用后端业务接口；弹窗只负责收集必填原因与二次确认。
 * 审核通过 ≠ 资金到账：文案严格区分「待退款」与「退款成功」。
 */
const props = defineProps<{
  modelValue: boolean;
  mode: RefundActionMode;
  refund: RefundView | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [visible: boolean];
  done: [];
}>();

const reason = ref("");
const reasonError = ref("");
const { submitting, run } = useSubmitGuard();

const visible = computed({
  get: () => props.modelValue,
  set: value => emit("update:modelValue", value)
});

const meta = computed(() => REFUND_ACTION_META[props.mode]);

const summary = computed(() => {
  if (!props.refund) return "";
  return `退款单 #${props.refund.id}（订单 #${props.refund.orderId}，金额 ${props.refund.amountText}）`;
});

watch(
  () => props.modelValue,
  isOpen => {
    if (!isOpen) return;
    reason.value = "";
    reasonError.value = "";
  }
);

/** 成功提示语严格对应后端状态语义 */
function successText(mode: RefundActionMode): string {
  switch (mode) {
    case "review_approve":
      return "审核通过，退款单已进入「待退款」；资金尚未到账";
    case "review_reject":
      return "已拒绝退款申请";
    case "execute_success":
      return "已确认退款成功；订单已更新为已退款，库存由后端恢复";
    case "execute_failure":
      return "已标记退款失败，可重试退款或关闭";
    case "close":
      return "退款已关闭，订单保持已支付";
    default:
      return "操作成功";
  }
}

async function submit() {
  const refund = props.refund;
  if (!refund) return;

  const invalid = validateRefundReason(reason.value);
  reasonError.value = invalid ?? "";
  if (invalid) return;

  const trimmed = reason.value.trim();
  const mode = props.mode;

  await run(async () => {
    try {
      await ElMessageBox.confirm(
        `${meta.value.warning} ${summary.value}`,
        "确认操作",
        {
          type: "warning",
          confirmButtonText: "确认",
          cancelButtonText: "取消"
        }
      );
    } catch {
      return;
    }

    try {
      if (mode === "review_approve" || mode === "review_reject") {
        await reviewRefund(refund.id, {
          result: mode === "review_approve" ? "approve" : "reject",
          reason: trimmed
        });
      } else if (mode === "execute_success" || mode === "execute_failure") {
        await executeRefund(refund.id, {
          result: mode === "execute_success" ? "success" : "failure",
          reason: trimmed
        });
      } else {
        await closeRefund(refund.id, trimmed);
      }

      message(successText(mode), { type: "success" });
      emit("done");
      visible.value = false;
    } catch (cause) {
      message(resolveErrorMessage(cause, "退款操作失败"), { type: "error" });
    }
  });
}
</script>

<template>
  <el-dialog
    v-model="visible"
    :title="meta.title"
    width="520px"
    destroy-on-close
  >
    <el-alert
      type="info"
      :closable="false"
      show-icon
      :title="meta.description"
    />

    <div v-if="refund" class="refund-summary">
      <div>退款单 #{{ refund.id }}</div>
      <div>关联订单 #{{ refund.orderId }}</div>
      <div>退款金额 {{ refund.amountText }}</div>
      <div>当前状态 {{ refund.statusLabel }}</div>
    </div>

    <el-form label-width="90px" class="action-form">
      <el-form-item label="原因" required :error="reasonError">
        <el-input
          v-model="reason"
          type="textarea"
          :rows="3"
          :maxlength="REFUND_REASON_MAX_LENGTH"
          show-word-limit
          :placeholder="
            mode === 'review_approve' || mode === 'review_reject'
              ? '填写审核意见（必填）'
              : '填写执行 / 关闭原因（必填）'
          "
        />
        <div class="field-hint">
          原因会写入后端退款审计记录，提交前请确认内容。
        </div>
      </el-form-item>
    </el-form>

    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button
        :type="meta.danger ? 'danger' : 'primary'"
        :loading="submitting"
        @click="submit"
        ><template #loading><BrandLoadingIcon /></template>
        {{ meta.submitLabel }}
      </el-button>
    </template>
  </el-dialog>
</template>

<style lang="scss" scoped>
.refund-summary {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 6px 12px;
  padding: 10px 12px;
  margin-top: 12px;
  font-size: 13px;
  color: var(--el-text-color-regular);
  background: var(--el-fill-color-lighter);
  border-radius: 6px;
}

.action-form {
  margin-top: 14px;
}

.field-hint {
  margin-top: 4px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
</style>
