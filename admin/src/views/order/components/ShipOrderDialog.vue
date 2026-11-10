<script setup lang="ts">
import { computed } from "vue";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { shipOrder } from "@/api/modules/order";
import type { OrderListRowView } from "@/api/adapters/order-adapter";

/**
 * 发货确认弹窗。
 *
 * 后端 `ShipReq` 只有路径参数 `id`，**没有**物流公司、运单号、备注字段，
 * 也没有物流轨迹查询接口。因此这里只做信息确认，不虚构物流表单。
 *
 * 发货成功条件由后端判定：订单必须为 paid，且不存在有效退款申请（否则 18005）。
 */
const props = defineProps<{
  modelValue: boolean;
  order: OrderListRowView | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [visible: boolean];
  shipped: [];
}>();

const { submitting, run } = useSubmitGuard();

const visible = computed({
  get: () => props.modelValue,
  set: value => emit("update:modelValue", value)
});

async function submit() {
  const target = props.order;
  if (!target) return;

  await run(async () => {
    try {
      await shipOrder(target.id);
      message("发货成功，订单状态已更新为已发货", { type: "success" });
      emit("shipped");
      visible.value = false;
    } catch (cause) {
      // 保留弹窗，便于用户看到后端真实错误（9002 / 18005）
      message(resolveErrorMessage(cause, "发货失败"), { type: "error" });
    }
  });
}
</script>

<template>
  <el-dialog v-model="visible" title="确认发货" width="480px" destroy-on-close>
    <el-descriptions v-if="order" :column="1" border size="small">
      <el-descriptions-item label="订单编号">
        {{ order.orderNo }}
      </el-descriptions-item>
      <el-descriptions-item label="用户">
        用户 #{{ order.userId }}
      </el-descriptions-item>
      <el-descriptions-item label="订单金额">
        {{ order.totalText }}
      </el-descriptions-item>
      <el-descriptions-item label="当前状态">
        <el-tag :type="order.statusTagType" size="small">
          {{ order.statusLabel }}
        </el-tag>
      </el-descriptions-item>
    </el-descriptions>

    <el-alert
      class="ship-alert"
      type="info"
      :closable="false"
      show-icon
      title="发货仅推进订单状态"
      description="后端发货接口不接收物流公司 / 运单号，本系统也没有物流轨迹查询能力；确认后订单由「已支付」变为「已发货」。"
    />

    <el-alert
      class="ship-alert"
      type="warning"
      :closable="false"
      show-icon
      title="退款互斥"
      description="若该订单存在申请中 / 待退款 / 退款失败的退款单，后端会拒绝发货并返回「退款与发货冲突」。"
    />

    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="submit">
        确认发货
      </el-button>
    </template>
  </el-dialog>
</template>

<style lang="scss" scoped>
.ship-alert {
  margin-top: 12px;
}
</style>
