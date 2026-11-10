<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { useRouter } from "vue-router";
import { useListReturn } from "@/composables/useListReturn";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { getOrder, listOrders } from "@/api/modules/order";
import type { OrderStatus } from "@/api/types/order";
import {
  buildItemsSummary,
  canShip,
  ORDER_STATUS_OPTIONS,
  sumItemQuantity,
  toOrderListQuery,
  toOrderListRowView,
  type OrderListRowView
} from "@/api/adapters/order-adapter";
import ShipOrderDialog from "./components/ShipOrderDialog.vue";

/**
 * 订单列表。
 *
 * 后端列表接口是**轻量**的（只有 id/order_no/user_id/status/total_amount/created_at），
 * 不返回商品摘要与件数。为了展示"商品摘要/数量"，页面按需并发调用详情接口补全，
 * 失败时显示 `—`，不影响列表本身。
 *
 * 列表只覆盖普通订单（orders 表）；秒杀订单为独立表，不在此接口范围内。
 */
defineOptions({ name: "OrderList" });

const router = useRouter();
const pageRoot = ref<HTMLElement>();

const canShipOrder = computed(() => hasPerms(PERMISSION.ORDER_SHIP));

const rows = ref<OrderListRowView[]>([]);
const total = ref(0);
const page = ref(1);
const size = ref(10);
const loading = ref(false);
const error = ref("");
/** 请求令牌：筛选快速切换时只接受最后一次响应 */
let loadToken = 0;

const filters = reactive<{
  orderNo: string;
  userId: number | undefined;
  status: OrderStatus | "";
  range: [Date, Date] | undefined;
}>({
  orderNo: "",
  userId: undefined,
  status: "",
  range: undefined
});

/** 详情补全：商品摘要 + 件数；-1 表示未知 */
const enrichment = reactive<
  Record<number, { summary: string; count: number; failed: boolean }>
>({});

const statusOptions = [
  { value: "", label: "全部状态" },
  ...ORDER_STATUS_OPTIONS
];

async function enrichRows(items: OrderListRowView[], token: number) {
  const queue = [...items];

  const workers = Array.from(
    { length: Math.min(4, queue.length) },
    async () => {
      for (;;) {
        const next = queue.shift();
        if (!next || token !== loadToken) return;
        enrichment[next.id] = { summary: "…", count: -1, failed: false };
        try {
          const detail = await getOrder(next.id);
          if (token !== loadToken) return;
          enrichment[next.id] = {
            summary: buildItemsSummary(detail.items),
            count: sumItemQuantity(detail.items),
            failed: false
          };
        } catch {
          if (token !== loadToken) return;
          enrichment[next.id] = { summary: "—", count: -1, failed: true };
        }
      }
    }
  );

  await Promise.all(workers);
}

async function load() {
  const token = ++loadToken;
  loading.value = true;
  error.value = "";

  try {
    const result = await listOrders(
      toOrderListQuery({
        page: page.value,
        size: size.value,
        orderNo: filters.orderNo,
        userId: filters.userId,
        status: filters.status,
        range: filters.range
      })
    );

    if (token !== loadToken) return;

    rows.value = (result.items ?? []).map(toOrderListRowView);
    total.value = result.total;
    void enrichRows(rows.value, token);
  } catch (cause) {
    if (token !== loadToken) return;
    rows.value = [];
    total.value = 0;
    error.value = resolveErrorMessage(cause, "订单列表加载失败");
  } finally {
    if (token === loadToken) loading.value = false;
  }
}

function search() {
  page.value = 1;
  void load();
}

function reset() {
  filters.orderNo = "";
  filters.userId = undefined;
  filters.status = "";
  filters.range = undefined;
  page.value = 1;
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
  void router.push(`/orders/${id}`);
}

function summaryOf(id: number) {
  return enrichment[id]?.summary ?? "…";
}

function countOf(id: number) {
  const count = enrichment[id]?.count;
  if (count === undefined || count < 0) return "—";
  return String(count);
}

/** 发货弹窗 */
const shipVisible = ref(false);
const shipTarget = ref<OrderListRowView | null>(null);

function openShip(row: OrderListRowView) {
  shipTarget.value = row;
  shipVisible.value = true;
}

function handleShipped() {
  message("发货成功", { type: "success" });
  void load();
}

onMounted(load);
useListReturn(pageRoot, load);
</script>

<template>
  <div ref="pageRoot" class="order-list-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">订单列表</h2>
        <p class="page-desc">查询普通订单，查看交易状态并处理发货。</p>
      </div>
      <el-button @click="load">刷新</el-button>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="查询范围说明"
      description="本页展示普通订单，不包含秒杀订单。用户查询需填写准确的用户 ID。"
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
        <el-form-item label="订单状态">
          <el-select v-model="filters.status" class="filter-status">
            <el-option
              v-for="option in statusOptions"
              :key="option.value"
              :label="option.label"
              :value="option.value"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="下单时间">
          <el-date-picker
            v-model="filters.range"
            type="datetimerange"
            range-separator="至"
            start-placeholder="开始时间"
            end-placeholder="结束时间"
            class="filter-range"
          />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="loading" @click="search"
            ><template #loading><BrandLoadingIcon /></template>查询</el-button
          >
          <el-button @click="reset">重置</el-button>
        </el-form-item>
      </el-form>

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
        <el-table-column label="订单编号" min-width="200">
          <template #default="{ row }">
            <el-button link type="primary" @click="openDetail(row.id)">
              {{ row.orderNo }}
            </el-button>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <el-tag :type="row.statusTagType" size="small">
              {{ row.statusLabel }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="用户" width="110">
          <template #default="{ row }">用户 #{{ row.userId }}</template>
        </el-table-column>
        <el-table-column label="商品摘要" min-width="180">
          <template #default="{ row }">
            {{ summaryOf(row.id) }}
          </template>
        </el-table-column>
        <el-table-column label="件数" width="80" align="center">
          <template #default="{ row }">
            {{ countOf(row.id) }}
          </template>
        </el-table-column>
        <el-table-column label="订单金额" width="130" align="right">
          <template #default="{ row }">
            <span class="amount">{{ row.totalText }}</span>
          </template>
        </el-table-column>
        <el-table-column label="下单时间" min-width="170">
          <template #default="{ row }">{{ row.createdText }}</template>
        </el-table-column>
        <el-table-column label="操作" width="130" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openDetail(row.id)">
              详情
            </el-button>
            <el-button
              v-if="canShipOrder && canShip(row.status)"
              link
              type="success"
              @click="openShip(row)"
            >
              发货
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

    <ShipOrderDialog
      v-model="shipVisible"
      :order="shipTarget"
      @shipped="handleShipped"
    />
  </div>
</template>

<style lang="scss" scoped>
.order-list-page {
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

.filter-range {
  width: 340px;
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
