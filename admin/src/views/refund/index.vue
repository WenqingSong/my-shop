<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { findOrderIdByOrderNo } from "@/api/modules/order";
import { listRefunds } from "@/api/modules/refund";
import type { AdminRefundListQuery, RefundStatus } from "@/api/types/refund";
import {
  REFUND_STATUS_OPTIONS,
  toRefundView,
  type RefundView
} from "@/api/adapters/refund-adapter";

/**
 * 退款列表（售后管理）。
 *
 * 后端筛选只支持 `order_id` / `user_id` / `status` 与分页：
 * - **没有** order_no 参数：按订单编号搜索时先调用订单列表精确取 id；
 * - **没有**时间范围参数：不提供申请时间筛选（不做前端假筛选）。
 *
 * 退款与订单是两套状态枚举，这里只展示退款状态；订单状态在详情/订单页查看。
 */
defineOptions({ name: "RefundList" });

const route = useRoute();
const router = useRouter();

const canViewOrder = computed(() => hasPerms(PERMISSION.ORDER_VIEW));

const rows = ref<RefundView[]>([]);
const total = ref(0);
const page = ref(1);
const size = ref(10);
const loading = ref(false);
/** 订单编号解析中的独立状态，避免与列表 loading 互相覆盖 */
const resolving = ref(false);
const error = ref("");
/** 请求令牌：筛选快速切换时只接受最后一次响应 */
let loadToken = 0;

const filters = reactive<{
  orderNo: string;
  userId: number | undefined;
  status: RefundStatus | "";
}>({
  orderNo: "",
  userId: undefined,
  status: ""
});

/** 当前生效的订单 id 过滤（来自订单页跳转或订单编号解析） */
const activeOrderId = ref<number | undefined>(undefined);
/** 通过订单编号搜索时的展示上下文 */
const resolvedOrderNo = ref("");

const statusOptions = [
  { value: "", label: "全部状态" },
  ...REFUND_STATUS_OPTIONS
];

async function load() {
  const token = ++loadToken;
  loading.value = true;
  error.value = "";

  const query: AdminRefundListQuery = {
    page: page.value,
    size: size.value
  };
  if (activeOrderId.value) query.order_id = activeOrderId.value;
  if (filters.userId) query.user_id = filters.userId;
  if (filters.status) query.status = filters.status;

  try {
    const result = await listRefunds(query);
    if (token !== loadToken) return;
    rows.value = (result.items ?? []).map(toRefundView);
    total.value = result.total;
  } catch (cause) {
    if (token !== loadToken) return;
    rows.value = [];
    total.value = 0;
    error.value = resolveErrorMessage(cause, "退款列表加载失败");
  } finally {
    if (token === loadToken) loading.value = false;
  }
}

/** 查询：若填写订单编号，先用订单列表接口解析出 order_id */
async function search() {
  const orderNo = filters.orderNo.trim();

  if (!orderNo) {
    resolvedOrderNo.value = "";
    page.value = 1;
    void load();
    return;
  }

  resolving.value = true;
  error.value = "";

  try {
    const orderId = await findOrderIdByOrderNo(orderNo);
    if (!orderId) {
      activeOrderId.value = undefined;
      rows.value = [];
      total.value = 0;
      resolvedOrderNo.value = "";
      error.value = `未找到订单「${orderNo}」，请确认订单编号是否正确`;
      return;
    }
    activeOrderId.value = orderId;
    resolvedOrderNo.value = orderNo;
    page.value = 1;
    await load();
  } catch (cause) {
    rows.value = [];
    total.value = 0;
    error.value = `按订单编号定位失败：${resolveErrorMessage(
      cause,
      "需要订单列表查询权限"
    )}`;
  } finally {
    resolving.value = false;
  }
}

function reset() {
  filters.orderNo = "";
  filters.userId = undefined;
  filters.status = "";
  activeOrderId.value = undefined;
  resolvedOrderNo.value = "";
  page.value = 1;
  if (route.query.order_id) {
    void router.replace({ path: "/refunds" });
  }
  void load();
}

function clearOrderFilter() {
  activeOrderId.value = undefined;
  resolvedOrderNo.value = "";
  filters.orderNo = "";
  page.value = 1;
  if (route.query.order_id) {
    void router.replace({ path: "/refunds" });
  }
  void load();
}

function handlePageChange(next: number) {
  page.value = next;
  void load();
}

function handleSizeChange(next: number) {
  size.value = next;
  page.value = 1;
  void load();
}

function openDetail(id: number) {
  void router.push(`/refunds/${id}`);
}

function openOrder(id: number) {
  void router.push(`/orders/${id}`);
}

onMounted(() => {
  const raw = Number(route.query.order_id);
  if (Number.isFinite(raw) && raw > 0) activeOrderId.value = raw;
  void load();
});
</script>

<template>
  <div class="refund-list-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">退款管理</h2>
        <p class="page-desc">
          审核与执行分阶段：审核通过仅进入「待退款」，执行成功才完成退款
        </p>
      </div>
      <el-button @click="load">刷新</el-button>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="筛选范围说明"
      description="后端退款列表只支持 订单 ID / 用户 ID / 状态 筛选与分页，没有申请时间参数，因此页面不提供时间筛选；按订单编号搜索会先通过订单列表接口解析订单 ID。"
    />

    <el-card shadow="never">
      <el-form :inline="true" class="filter-form" @submit.prevent>
        <el-form-item label="订单编号">
          <el-input
            v-model="filters.orderNo"
            placeholder="精确匹配"
            clearable
            class="filter-order-no"
            @keyup.enter="search"
          />
        </el-form-item>
        <el-form-item label="用户 ID">
          <el-input-number
            v-model="filters.userId"
            :min="1"
            :step="1"
            step-strictly
            controls-position="right"
            placeholder="精确匹配"
            class="filter-user"
          />
        </el-form-item>
        <el-form-item label="退款状态">
          <el-select v-model="filters.status" class="filter-status">
            <el-option
              v-for="option in statusOptions"
              :key="option.value"
              :label="option.label"
              :value="option.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item>
          <el-button
            type="primary"
            :loading="loading || resolving"
            @click="search"
          >
            查询
          </el-button>
          <el-button @click="reset">重置</el-button>
        </el-form-item>
      </el-form>

      <el-alert
        v-if="activeOrderId"
        class="block-alert"
        type="success"
        :closable="false"
        show-icon
        :title="`当前仅显示订单相关退款（订单 ID ${activeOrderId}${
          resolvedOrderNo ? ` / 编号 ${resolvedOrderNo}` : ''
        }）`"
      >
        <template #default>
          <el-button link type="primary" @click="clearOrderFilter">
            清除订单过滤
          </el-button>
        </template>
      </el-alert>

      <el-alert
        v-if="error"
        class="block-alert"
        type="error"
        :closable="false"
        show-icon
        :title="error"
      >
        <template #default>
          <el-button link type="primary" @click="load">重试</el-button>
        </template>
      </el-alert>

      <el-table v-loading="loading" :data="rows" border stripe>
        <el-table-column label="退款单号" width="110">
          <template #default="{ row }">
            <el-button link type="primary" @click="openDetail(row.id)">
              #{{ row.id }}
            </el-button>
          </template>
        </el-table-column>
        <el-table-column label="关联订单" width="130">
          <template #default="{ row }">
            <el-button
              v-if="canViewOrder"
              link
              type="primary"
              @click="openOrder(row.orderId)"
            >
              订单 #{{ row.orderId }}
            </el-button>
            <span v-else>订单 #{{ row.orderId }}</span>
          </template>
        </el-table-column>
        <el-table-column label="用户" width="110">
          <template #default="{ row }">用户 #{{ row.userId }}</template>
        </el-table-column>
        <el-table-column label="退款金额" width="130" align="right">
          <template #default="{ row }">
            <span class="amount">{{ row.amountText }}</span>
          </template>
        </el-table-column>
        <el-table-column label="退款原因" min-width="200" show-overflow-tooltip>
          <template #default="{ row }">{{ row.reasonText }}</template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <el-tag :type="row.statusTagType" size="small">
              {{ row.statusLabel }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="申请时间" min-width="170">
          <template #default="{ row }">{{ row.createdText }}</template>
        </el-table-column>
        <el-table-column label="操作" width="100" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openDetail(row.id)">
              详情
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <div class="pagination-bar">
        <el-pagination
          :current-page="page"
          :page-size="size"
          :total="total"
          :page-sizes="[10, 20, 50]"
          layout="total, sizes, prev, pager, next, jumper"
          background
          @current-change="handlePageChange"
          @size-change="handleSizeChange"
        />
      </div>
    </el-card>
  </div>
</template>

<style lang="scss" scoped>
.refund-list-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
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

.filter-order-no {
  width: 190px;
}

.filter-user {
  width: 140px;
}

.filter-status {
  width: 140px;
}

.block-alert {
  margin-bottom: 12px;
}

.amount {
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.pagination-bar {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
</style>
