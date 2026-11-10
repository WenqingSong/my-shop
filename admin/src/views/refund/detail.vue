<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { hasPerms } from "@/utils/auth";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { getRefund, listRefundAudits } from "@/api/modules/refund";
import type { RefundDTO } from "@/api/types/refund";
import { getOrder } from "@/api/modules/order";
import {
  getRefundActions,
  toRefundAuditView,
  toRefundView,
  type RefundActionMode,
  type RefundAuditView,
  type RefundView
} from "@/api/adapters/refund-adapter";
import { toOrderView, type OrderView } from "@/api/adapters/order-adapter";
import RefundActionDialog from "./components/RefundActionDialog.vue";

/**
 * 退款详情（售后详情）。
 *
 * 数据来源全部为真实接口：
 * - `GET /admin/refunds/:id` 退款主体
 * - `GET /admin/refunds/:id/audits` 处理记录（append-only）
 * - `GET /admin/orders/:id` 关联订单与商品快照（需要 order:view，失败则降级展示）
 *
 * 页面不提供"直接改状态"的入口；审核 / 执行 / 关闭分别调用对应业务接口。
 */
defineOptions({ name: "RefundDetailPage" });

const route = useRoute();
const router = useRouter();

const refundId = computed(() => Number(route.params.id));

const refund = ref<RefundView | null>(null);
const audits = ref<RefundAuditView[]>([]);
const order = ref<OrderView | null>(null);
const auditsError = ref("");
const orderError = ref("");
const loading = ref(false);
const error = ref("");
/** 路由 id 变化时只接受最后一次请求 */
let loadToken = 0;

const canReview = computed(() => hasPerms(PERMISSION.REFUND_REVIEW));
const canExecute = computed(() => hasPerms(PERMISSION.REFUND_EXECUTE));

const actions = computed(() => getRefundActions(refund.value?.status ?? ""));

const actionVisible = ref(false);
const actionMode = ref<RefundActionMode>("review_approve");

const showReview = computed(() => actions.value.review && canReview.value);
const showExecuteSuccess = computed(
  () => actions.value.executeSuccess && canExecute.value
);
const showExecuteFailure = computed(
  () => actions.value.executeFailure && canExecute.value
);
const showClose = computed(() => actions.value.close && canExecute.value);

async function load() {
  if (!Number.isFinite(refundId.value) || refundId.value <= 0) {
    error.value = "退款单 id 非法";
    return;
  }

  const token = ++loadToken;
  loading.value = true;
  error.value = "";
  auditsError.value = "";
  orderError.value = "";

  let detail: RefundDTO;
  try {
    detail = await getRefund(refundId.value);
    if (token !== loadToken) return;
    refund.value = toRefundView(detail);
  } catch (cause) {
    if (token !== loadToken) return;
    refund.value = null;
    error.value = resolveErrorMessage(cause, "退款详情加载失败");
    loading.value = false;
    return;
  }

  const [auditsResult, orderResult] = await Promise.allSettled([
    listRefundAudits(refundId.value),
    getOrder(detail.order_id)
  ]);

  if (token !== loadToken) return;

  if (auditsResult.status === "fulfilled") {
    audits.value = (auditsResult.value.items ?? []).map(toRefundAuditView);
  } else {
    audits.value = [];
    auditsError.value = resolveErrorMessage(
      auditsResult.reason,
      "退款审计记录加载失败"
    );
  }

  if (orderResult.status === "fulfilled") {
    order.value = toOrderView(orderResult.value);
  } else {
    order.value = null;
    orderError.value = resolveErrorMessage(
      orderResult.reason,
      "关联订单加载失败（可能需要 order:view 权限）"
    );
  }

  loading.value = false;
}

function openAction(mode: RefundActionMode) {
  actionMode.value = mode;
  actionVisible.value = true;
}

function handleDone() {
  void load();
}

function goBack() {
  void router.push("/refunds");
}

function goOrder() {
  if (refund.value) void router.push(`/orders/${refund.value.orderId}`);
}

watch(refundId, load);
onMounted(load);
</script>

<template>
  <div class="refund-detail-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">退款详情</h2>
        <p class="page-desc">
          审核与执行分阶段；处理记录来自后端 append-only 审计表
        </p>
      </div>
      <div class="page-actions">
        <el-button @click="goBack">返回退款列表</el-button>
        <el-button v-if="refund" @click="goOrder">查看订单</el-button>

        <el-button
          v-if="showReview"
          type="success"
          @click="openAction('review_approve')"
        >
          审核通过
        </el-button>
        <el-button
          v-if="showReview"
          type="danger"
          plain
          @click="openAction('review_reject')"
        >
          拒绝
        </el-button>
        <el-button
          v-if="showExecuteSuccess"
          type="success"
          @click="openAction('execute_success')"
        >
          确认退款成功
        </el-button>
        <el-button
          v-if="showExecuteFailure"
          type="warning"
          @click="openAction('execute_failure')"
        >
          标记退款失败
        </el-button>
        <el-button
          v-if="showClose"
          type="info"
          plain
          @click="openAction('close')"
        >
          关闭退款
        </el-button>
      </div>
    </div>

    <el-alert
      type="warning"
      :closable="false"
      show-icon
      title="退款语义说明"
      description="审核通过后仍为「待退款」，不代表资金到账。本系统使用人工确认方式记录退款结果，未对接真实支付渠道。确认退款成功后，订单将标记为已退款并恢复库存。"
    />

    <PageLoading v-if="loading" />

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

    <template v-else-if="refund">
      <el-card shadow="never" class="summary-card">
        <div class="summary">
          <div>
            <div class="summary__label">退款单号</div>
            <div class="summary__value">#{{ refund.id }}</div>
            <div class="summary__meta">
              <el-tag :type="refund.statusTagType" size="small">
                {{ refund.statusLabel }}
              </el-tag>
              <span>订单 #{{ refund.orderId }}</span>
              <span>用户 #{{ refund.userId }}</span>
            </div>
          </div>
          <div class="summary__amount">
            <div class="summary__label">退款金额</div>
            <div class="summary__amount-value">{{ refund.amountText }}</div>
          </div>
        </div>
      </el-card>

      <el-card shadow="never">
        <template #header><span class="card-title">退款信息</span></template>
        <el-descriptions :column="3" border>
          <el-descriptions-item label="退款单 ID">
            {{ refund.id }}
          </el-descriptions-item>
          <el-descriptions-item label="退款状态">
            {{ refund.statusLabel }}
          </el-descriptions-item>
          <el-descriptions-item label="申请用户">
            用户 #{{ refund.userId }}
          </el-descriptions-item>
          <el-descriptions-item label="申请时间">
            {{ refund.createdText }}
          </el-descriptions-item>
          <el-descriptions-item label="更新时间">
            {{ refund.updatedText }}
          </el-descriptions-item>
          <el-descriptions-item label="关联订单">
            <el-button link type="primary" @click="goOrder">
              订单 #{{ refund.orderId }}
            </el-button>
          </el-descriptions-item>
        </el-descriptions>

        <div class="reason-block">
          <div class="reason-block__label">申请原因</div>
          <div class="reason-block__text">{{ refund.reasonText }}</div>
        </div>
      </el-card>

      <el-card shadow="never">
        <template #header
          ><span class="card-title">审核与执行信息</span></template
        >
        <el-descriptions :column="2" border>
          <el-descriptions-item label="审核原因">
            {{ refund.reviewReasonText }}
          </el-descriptions-item>
          <el-descriptions-item label="审核人 / 时间">
            {{ refund.reviewedByText }} / {{ refund.reviewedAtText }}
          </el-descriptions-item>
          <el-descriptions-item label="执行原因">
            {{ refund.executeReasonText }}
          </el-descriptions-item>
          <el-descriptions-item label="执行人 / 时间">
            {{ refund.executedByText }} / {{ refund.executedAtText }}
          </el-descriptions-item>
        </el-descriptions>
      </el-card>

      <el-card shadow="never">
        <template #header
          ><span class="card-title">关联订单与商品快照</span></template
        >

        <el-alert
          v-if="orderError"
          type="warning"
          :closable="false"
          show-icon
          :title="orderError"
        />

        <template v-else-if="order">
          <el-descriptions :column="3" border class="order-descriptions">
            <el-descriptions-item label="订单编号">
              {{ order.orderNo }}
            </el-descriptions-item>
            <el-descriptions-item label="订单状态">
              <el-tag :type="order.statusTagType" size="small">
                {{ order.statusLabel }}
              </el-tag>
            </el-descriptions-item>
            <el-descriptions-item label="订单实付">
              {{ order.totalText }}
            </el-descriptions-item>
          </el-descriptions>

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
            <el-table-column label="单价" width="120" align="right">
              <template #default="{ row }">{{ row.priceText }}</template>
            </el-table-column>
            <el-table-column label="数量" width="80" align="center">
              <template #default="{ row }">{{ row.quantity }}</template>
            </el-table-column>
            <el-table-column label="小计" width="120" align="right">
              <template #default="{ row }">{{ row.subtotalText }}</template>
            </el-table-column>
          </el-table>
          <p class="field-note">商品信息为订单创建时快照，不读取当前商品表。</p>
        </template>

        <p v-else class="field-note">—</p>
      </el-card>

      <el-card shadow="never">
        <template #header>
          <span class="card-title">售后处理记录</span>
        </template>

        <el-alert
          v-if="auditsError"
          type="error"
          :closable="false"
          show-icon
          :title="auditsError"
        />

        <el-empty v-else-if="audits.length === 0" description="暂无处理记录" />

        <el-timeline v-else>
          <el-timeline-item
            v-for="audit in audits"
            :key="audit.id"
            :type="audit.actionTagType"
            :timestamp="audit.createdText"
            placement="top"
          >
            <div class="audit-item">
              <div class="audit-item__header">
                <el-tag :type="audit.actionTagType" size="small">
                  {{ audit.actionLabel }}
                </el-tag>
                <span class="audit-item__operator">
                  {{ audit.operatorText }}
                </span>
              </div>
              <div class="audit-item__status">
                {{ audit.beforeStatusText }} → {{ audit.afterStatusText }}
              </div>
              <div class="audit-item__reason">原因：{{ audit.reasonText }}</div>
            </div>
          </el-timeline-item>
        </el-timeline>
      </el-card>

      <RefundActionDialog
        v-model="actionVisible"
        :mode="actionMode"
        :refund="refund"
        @done="handleDone"
      />
    </template>
  </div>
</template>

<style lang="scss" scoped>
.refund-detail-page {
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
  flex-wrap: wrap;
  gap: 8px;
  justify-content: flex-end;
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

.reason-block {
  padding: 10px 12px;
  margin-top: 12px;
  font-size: 13px;
  line-height: 1.6;
  color: var(--el-text-color-regular);
  white-space: pre-wrap;
  background: var(--el-fill-color-lighter);
  border-radius: 6px;
}

.reason-block__label {
  margin-bottom: 4px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.reason-block__text {
  word-break: break-word;
}

.order-descriptions {
  margin-bottom: 12px;
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

.audit-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.audit-item__header {
  display: flex;
  gap: 8px;
  align-items: center;
}

.audit-item__operator {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.audit-item__status {
  font-size: 13px;
  color: var(--el-text-color-regular);
}

.audit-item__reason {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  word-break: break-word;
}
</style>
