<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { getOrder } from "@/api/modules/order";
import {
  canShip,
  toOrderView,
  type OrderListRowView,
  type OrderView
} from "@/api/adapters/order-adapter";
import ShipOrderDialog from "./components/ShipOrderDialog.vue";

/**
 * 订单详情。
 *
 * 商品信息全部使用订单项快照（product_name / sku_name / product_main_image / price），
 * **不会**回查当前商品表，避免历史订单被商品改名/改图污染。
 *
 * 后端没有订单操作日志接口，页面不展示伪造的操作历史。
 */
defineOptions({ name: "OrderDetailPage" });

const route = useRoute();
const router = useRouter();

const orderId = computed(() => Number(route.params.id));
const order = ref<OrderView | null>(null);
const loading = ref(false);
const error = ref("");
/** 路由 id 变化时只接受最后一次请求 */
let loadToken = 0;

const canShipOrder = computed(() => hasPerms(PERMISSION.ORDER_SHIP));
const canViewRefunds = computed(() => hasPerms(PERMISSION.REFUND_LIST));

const canShipCurrent = computed(
  () => Boolean(order.value) && canShip(order.value!.status)
);

async function load() {
  if (!Number.isFinite(orderId.value) || orderId.value <= 0) {
    error.value = "订单 id 非法";
    return;
  }

  const token = ++loadToken;
  loading.value = true;
  error.value = "";

  try {
    const detail = await getOrder(orderId.value);
    if (token !== loadToken) return;
    order.value = toOrderView(detail);
  } catch (cause) {
    if (token !== loadToken) return;
    order.value = null;
    error.value = resolveErrorMessage(cause, "订单详情加载失败");
  } finally {
    if (token === loadToken) loading.value = false;
  }
}

function goBack() {
  void router.push("/orders");
}

function goRefunds() {
  if (!order.value) return;
  void router.push({
    path: "/refunds",
    query: { order_id: String(order.value.id) }
  });
}

const shipVisible = ref(false);

/** 发货弹窗只需要列表行级别的字段 */
const shipTarget = computed<OrderListRowView | null>(() => {
  const current = order.value;
  if (!current) return null;
  return {
    id: current.id,
    orderNo: current.orderNo,
    userId: current.userId,
    status: current.status,
    statusLabel: current.statusLabel,
    statusTagType: current.statusTagType,
    totalCents: current.totalCents,
    totalText: current.totalText,
    createdText: current.createdText
  };
});

function handleShipped() {
  message("发货成功", { type: "success" });
  void load();
}

watch(orderId, load);
onMounted(load);
</script>

<template>
  <div class="order-detail-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">订单详情</h2>
        <p class="page-desc">
          商品与金额均为下单时快照；状态变更只能通过后端业务接口
        </p>
      </div>
      <div class="page-actions">
        <el-button @click="goBack">返回列表</el-button>
        <el-button v-if="canViewRefunds" @click="goRefunds">
          查看退款记录
        </el-button>
        <el-button
          v-if="canShipOrder && canShipCurrent"
          type="primary"
          @click="shipVisible = true"
        >
          发货
        </el-button>
      </div>
    </div>

    <el-skeleton v-if="loading" :rows="8" animated />

    <el-alert
      v-else-if="error"
      type="error"
      :closable="false"
      show-icon
      :title="error"
    >
      <template #default>
        <el-button link type="primary" @click="load">重试</el-button>
      </template>
    </el-alert>

    <template v-else-if="order">
      <el-card shadow="never" class="summary-card">
        <div class="summary">
          <div>
            <div class="summary__label">订单编号</div>
            <div class="summary__value">{{ order.orderNo }}</div>
            <div class="summary__meta">
              <el-tag :type="order.statusTagType" size="small">
                {{ order.statusLabel }}
              </el-tag>
              <span>用户 #{{ order.userId }}</span>
              <span>共 {{ order.itemCount }} 件商品</span>
            </div>
          </div>
          <div class="summary__amount">
            <div class="summary__label">实付金额</div>
            <div class="summary__amount-value">{{ order.totalText }}</div>
          </div>
        </div>
      </el-card>

      <el-card shadow="never">
        <template #header><span class="card-title">基本信息</span></template>
        <el-descriptions :column="3" border>
          <el-descriptions-item label="订单 ID">{{
            order.id
          }}</el-descriptions-item>
          <el-descriptions-item label="订单状态">
            {{ order.statusLabel }}
          </el-descriptions-item>
          <el-descriptions-item label="用户 ID">
            {{ order.userId }}
          </el-descriptions-item>
          <el-descriptions-item label="下单时间">
            {{ order.createdText }}
          </el-descriptions-item>
          <el-descriptions-item label="支付截止">
            {{ order.expireText }}
          </el-descriptions-item>
          <el-descriptions-item label="取消原因">
            {{ order.cancelReasonText }}
          </el-descriptions-item>
        </el-descriptions>
      </el-card>

      <el-card shadow="never">
        <template #header><span class="card-title">金额信息</span></template>
        <el-descriptions :column="3" border>
          <el-descriptions-item label="商品金额">
            {{ order.originalText }}
          </el-descriptions-item>
          <el-descriptions-item label="优惠金额">
            {{ order.discountText }}
          </el-descriptions-item>
          <el-descriptions-item label="实付金额">
            <span class="amount-strong">{{ order.totalText }}</span>
          </el-descriptions-item>
        </el-descriptions>
        <p class="field-note">
          后端未提供运费字段；金额单位在接口中为整数分，页面仅做展示换算。
        </p>
      </el-card>

      <el-card shadow="never">
        <template #header><span class="card-title">收货与支付</span></template>
        <el-descriptions :column="2" border>
          <el-descriptions-item label="收货人">
            {{ order.recipientName }}
          </el-descriptions-item>
          <el-descriptions-item label="手机号">
            {{ order.phone }}
          </el-descriptions-item>
          <el-descriptions-item label="收货地址" :span="2">
            {{ order.addressText }}
          </el-descriptions-item>
          <el-descriptions-item label="支付时间">
            {{ order.paidText }}
          </el-descriptions-item>
          <el-descriptions-item label="支付方式"> — </el-descriptions-item>
        </el-descriptions>
        <p class="field-note">
          后端订单结构没有支付方式、支付流水号与物流公司/运单号字段，
          页面不做猜测填充；发货时间见下方时间线。
        </p>
      </el-card>

      <el-card shadow="never">
        <template #header><span class="card-title">商品快照</span></template>
        <el-table :data="order.items" border size="small">
          <el-table-column label="商品" min-width="260">
            <template #default="{ row }">
              <div class="item-cell">
                <el-image
                  v-if="row.image"
                  :src="row.image"
                  fit="cover"
                  class="item-image"
                  :preview-src-list="[row.image]"
                  preview-teleported
                />
                <div v-else class="item-image item-image--empty">无图</div>
                <div>
                  <div class="item-name">{{ row.productName }}</div>
                  <div class="item-sku">{{ row.skuName }}</div>
                </div>
              </div>
            </template>
          </el-table-column>
          <el-table-column label="单价" width="130" align="right">
            <template #default="{ row }">{{ row.priceText }}</template>
          </el-table-column>
          <el-table-column label="数量" width="90" align="center">
            <template #default="{ row }">{{ row.quantity }}</template>
          </el-table-column>
          <el-table-column label="小计" width="130" align="right">
            <template #default="{ row }">
              <span class="amount-strong">{{ row.subtotalText }}</span>
            </template>
          </el-table-column>
        </el-table>
        <p class="field-note">
          以上为订单创建时的商品名 / SKU / 主图 /
          成交价快照，不随商品后续修改而变化。
        </p>
      </el-card>

      <el-card shadow="never">
        <template #header><span class="card-title">时间线</span></template>
        <el-descriptions :column="3" border>
          <el-descriptions-item label="支付时间">
            {{ order.paidText }}
          </el-descriptions-item>
          <el-descriptions-item label="发货时间">
            {{ order.shippedText }}
          </el-descriptions-item>
          <el-descriptions-item label="收货时间">
            {{ order.receivedText }}
          </el-descriptions-item>
          <el-descriptions-item label="完成时间">
            {{ order.completedText }}
          </el-descriptions-item>
          <el-descriptions-item label="取消时间">
            {{ order.cancelledText }}
          </el-descriptions-item>
          <el-descriptions-item label="退款时间">
            {{ order.refundedText }}
          </el-descriptions-item>
        </el-descriptions>

        <el-alert
          class="history-alert"
          type="info"
          :closable="false"
          show-icon
          title="订单操作记录"
          description="后端未提供订单操作日志接口，因此不展示操作历史；退款审核与执行记录可在「退款管理」中查看。"
        />
      </el-card>

      <ShipOrderDialog
        v-model="shipVisible"
        :order="shipTarget"
        @shipped="handleShipped"
      />
    </template>
  </div>
</template>

<style lang="scss" scoped>
.order-detail-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.page-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.page-desc {
  margin: 6px 0 0;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.page-actions {
  display: flex;
  flex-shrink: 0;
  gap: 8px;
}

.summary-card {
  background: var(--el-fill-color-lighter);
}

.summary {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}

.summary__label {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.summary__value {
  margin-top: 4px;
  font-size: 16px;
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.summary__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: center;
  margin-top: 8px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.summary__amount {
  text-align: right;
}

.summary__amount-value {
  margin-top: 4px;
  font-size: 22px;
  font-weight: 700;
  color: var(--el-color-danger);
}

.card-title {
  font-weight: 600;
}

.amount-strong {
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.field-note {
  margin: 10px 0 0;
  font-size: 12px;
  line-height: 1.5;
  color: var(--el-text-color-secondary);
}

.item-cell {
  display: flex;
  gap: 10px;
  align-items: center;
}

.item-image {
  width: 48px;
  height: 48px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 4px;
}

.item-image--empty {
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  color: var(--el-text-color-placeholder);
  background: var(--el-fill-color-lighter);
}

.item-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--el-text-color-primary);
}

.item-sku {
  margin-top: 2px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.history-alert {
  margin-top: 12px;
}
</style>
