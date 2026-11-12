<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { ElMessageBox } from "element-plus";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { deductInventory, increaseInventory } from "@/api/modules/inventory";
import type { SkuDTO } from "@/api/types/sku";
import {
  buildAdjustmentRequest,
  INVENTORY_ADJUST_MAX,
  INVENTORY_CHANGE_TYPE_OPTIONS,
  validateAdjustQuantity
} from "@/api/adapters/inventory-adapter";

/**
 * 库存调整对话框。
 *
 * 严格遵循后端语义：只有"增加 / 扣减"两种增量操作，
 * 不存在"把库存设置为 N"的覆盖式写入。
 *
 * 关于调整原因：后端 `IncreaseReq/DeductReq` 只有 quantity，
 * 因此原因**不会**提交，也不会出现在库存流水里。UI 必须明确告知用户，
 * 不能静默丢弃，更不能谎称已记录。
 */
const props = defineProps<{
  modelValue: boolean;
  sku: SkuDTO | null;
  productName: string;
  /** 打开时已知的当前库存；null 表示仍在加载 */
  currentQuantity: number | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [visible: boolean];
  saved: [quantity: number];
}>();

const canIncrease = computed(() => hasPerms(PERMISSION.INVENTORY_INCREASE));
const canDeduct = computed(() => hasPerms(PERMISSION.INVENTORY_DEDUCT));

const typeOptions = computed(() =>
  INVENTORY_CHANGE_TYPE_OPTIONS.filter(option =>
    option.value === "increase" ? canIncrease.value : canDeduct.value
  )
);

const form = reactive<{
  type: "increase" | "deduct";
  quantity: number | null;
  reason: string;
}>({
  type: "increase",
  quantity: null,
  reason: ""
});

const quantityError = ref("");
const { submitting, run } = useSubmitGuard();

const visible = computed({
  get: () => props.modelValue,
  set: value => emit("update:modelValue", value)
});

const currentText = computed(() =>
  props.currentQuantity === null ? "加载中" : String(props.currentQuantity)
);

const quantityHint = computed(() => {
  if (form.quantity === null) return "";
  if (form.type === "increase") {
    return `调整后的库存以操作结果为准（当前 ${currentText.value}）`;
  }
  return `扣减数量不能超过实际库存。`;
});

watch(
  () => props.modelValue,
  isOpen => {
    if (!isOpen) return;
    form.type = typeOptions.value[0]?.value ?? "increase";
    form.quantity = null;
    form.reason = "";
    quantityError.value = "";
  }
);

async function submit() {
  if (!props.sku) return;

  quantityError.value = validateAdjustQuantity(form.quantity) ?? "";
  if (quantityError.value) return;

  const quantity = Number(form.quantity);
  if (!Number.isInteger(quantity) || quantity <= 0) return;

  const actionLabel = form.type === "increase" ? "增加" : "扣减";
  const { request, provisional } = buildAdjustmentRequest({
    skuId: props.sku.id,
    type: form.type,
    quantity,
    reason: form.reason
  });

  const confirmText =
    `确认对「${props.productName} / ${props.sku.name}」${actionLabel} ${quantity} 件库存？` +
    `当前库存：${currentText.value}，最终结果以操作完成后的库存为准。` +
    (provisional.reason
      ? ` 调整原因（仅本次确认，不保存）：${provisional.reason}`
      : "");

  await run(async () => {
    try {
      await ElMessageBox.confirm(confirmText, "确认库存调整", {
        type: "warning",
        confirmButtonText: `确认${actionLabel}`,
        cancelButtonText: "取消"
      });
    } catch {
      return;
    }

    try {
      const result =
        request.type === "increase"
          ? await increaseInventory(request.skuId, request.quantity)
          : await deductInventory(request.skuId, request.quantity);

      message(`${actionLabel}成功，当前库存 ${result.quantity}`, {
        type: "success"
      });
      emit("saved", result.quantity);
      visible.value = false;
    } catch (cause) {
      message(resolveErrorMessage(cause, "库存调整失败"), { type: "error" });
    }
  });
}
</script>

<template>
  <el-dialog v-model="visible" title="库存调整" width="520px" destroy-on-close>
    <el-descriptions
      v-if="sku"
      :column="1"
      border
      size="small"
      class="sku-info"
    >
      <el-descriptions-item label="商品">
        {{ productName || "—" }}
      </el-descriptions-item>
      <el-descriptions-item label="SKU">
        {{ sku.name }}（ID {{ sku.id }}）
      </el-descriptions-item>
      <el-descriptions-item label="当前库存">
        {{ currentText }}
      </el-descriptions-item>
    </el-descriptions>

    <el-alert
      v-if="typeOptions.length === 0"
      type="warning"
      :closable="false"
      show-icon
      title="当前管理员没有库存调整权限"
    />

    <el-form v-else label-width="96px" class="adjust-form">
      <el-form-item label="调整类型" required>
        <el-radio-group v-model="form.type">
          <el-radio-button
            v-for="option in typeOptions"
            :key="option.value"
            :value="option.value"
          >
            {{ option.label }}
          </el-radio-button>
        </el-radio-group>
      </el-form-item>

      <el-form-item label="调整数量" required :error="quantityError">
        <el-input-number
          v-model="form.quantity"
          :min="1"
          :max="INVENTORY_ADJUST_MAX"
          :step="1"
          step-strictly
          controls-position="right"
          placeholder="正整数"
        />
        <div class="field-hint">{{ quantityHint }}</div>
      </el-form-item>

      <el-form-item label="调整原因">
        <el-input
          v-model="form.reason"
          type="textarea"
          :rows="2"
          maxlength="255"
          show-word-limit
          placeholder="选填，仅用于本次操作确认"
        />
        <div class="field-hint field-hint--warning">
          此原因仅用于本次确认，不会保存到库存流水中。
        </div>
      </el-form-item>
    </el-form>

    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button
        type="primary"
        :loading="submitting"
        :disabled="typeOptions.length === 0"
        @click="submit"
        ><template #loading><BrandLoadingIcon /></template>
        确认调整
      </el-button>
    </template>
  </el-dialog>
</template>

<style lang="scss" scoped>
.sku-info {
  margin-bottom: 14px;
}

.adjust-form {
  margin-top: 4px;
}

.field-hint {
  margin-top: 4px;
  font-size: 12px;
  line-height: 1.4;
  color: var(--el-text-color-secondary);
}

.field-hint--warning {
  color: var(--el-color-warning);
}
</style>
