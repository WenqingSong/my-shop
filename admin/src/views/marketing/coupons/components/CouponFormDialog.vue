<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { createCoupon } from "@/api/modules/coupon";
import {
  buildCreateCouponPayload,
  emptyCouponForm,
  validateCouponForm,
  type CouponForm,
  type CouponFormErrors
} from "@/api/adapters/coupon-adapter";

/**
 * 新建优惠券模板弹窗。
 *
 * 后端只有创建接口，没有编辑；因此弹窗只用于新建。
 * 领取/使用时间均必填（后端 19002），提交时格式化为 `YYYY-MM-DD HH:mm:ss`。
 */
const props = defineProps<{ modelValue: boolean }>();

const emit = defineEmits<{
  "update:modelValue": [visible: boolean];
  saved: [];
}>();

const form = reactive<CouponForm>(emptyCouponForm());
const errors = ref<CouponFormErrors>({});
const { submitting, run } = useSubmitGuard();

const visible = computed({
  get: () => props.modelValue,
  set: value => emit("update:modelValue", value)
});

watch(
  () => props.modelValue,
  isOpen => {
    if (!isOpen) return;
    Object.assign(form, emptyCouponForm());
    errors.value = {};
  }
);

async function submit() {
  errors.value = validateCouponForm(form);
  if (Object.values(errors.value).some(Boolean)) {
    message("请检查表单内容", { type: "warning" });
    return;
  }

  await run(async () => {
    try {
      await createCoupon(buildCreateCouponPayload(form));
      message("优惠券模板已创建", { type: "success" });
      emit("saved");
      visible.value = false;
    } catch (cause) {
      message(resolveErrorMessage(cause, "创建优惠券失败"), { type: "error" });
    }
  });
}
</script>

<template>
  <el-dialog
    v-model="visible"
    title="新建优惠券"
    width="620px"
    destroy-on-close
  >
    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="后端仅支持满减券"
      description="支持「满 X 减 Y」固定金额优惠；暂不支持折扣券、指定商品、每人限领和领取后计时。"
    />

    <el-form label-width="110px" class="coupon-form">
      <el-form-item label="优惠券名称" required :error="errors.name">
        <el-input
          v-model="form.name"
          maxlength="64"
          show-word-limit
          placeholder="例如：新人满减券"
        />
      </el-form-item>

      <el-form-item label="使用门槛" required :error="errors.threshold">
        <el-input-number
          v-model="form.thresholdYuan"
          :min="0"
          :max="999999.99"
          :precision="2"
          :step="10"
          controls-position="right"
        />
        <span class="field-hint">单位：元；0 表示无门槛</span>
      </el-form-item>

      <el-form-item label="减免金额" required :error="errors.discount">
        <el-input-number
          v-model="form.discountYuan"
          :min="0.01"
          :max="999999.99"
          :precision="2"
          :step="1"
          controls-position="right"
        />
        <span class="field-hint">单位：元；提交时转换为整数分</span>
      </el-form-item>

      <el-form-item label="发放总量" required :error="errors.quantity">
        <el-input-number
          v-model="form.totalQuantity"
          :min="1"
          :step="1"
          step-strictly
          controls-position="right"
        />
      </el-form-item>

      <el-form-item label="领取时间" required :error="errors.claimRange">
        <el-date-picker
          v-model="form.claimRange"
          type="datetimerange"
          range-separator="至"
          start-placeholder="开始时间"
          end-placeholder="结束时间"
        />
      </el-form-item>

      <el-form-item label="使用时间" required :error="errors.useRange">
        <el-date-picker
          v-model="form.useRange"
          type="datetimerange"
          range-separator="至"
          start-placeholder="开始时间"
          end-placeholder="结束时间"
        />
      </el-form-item>
    </el-form>

    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="submit"
        ><template #loading><BrandLoadingIcon /></template>
        创建
      </el-button>
    </template>
  </el-dialog>
</template>

<style lang="scss" scoped>
.coupon-form {
  margin-top: 14px;
}

.field-hint {
  margin-left: 8px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
</style>
