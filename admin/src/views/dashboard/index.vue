<script setup lang="ts">
import { ref } from "vue";
import MetricCards from "./components/MetricCards.vue";
import OrderTrendPanel from "./components/OrderTrendPanel.vue";
import OrderStatusPanel from "./components/OrderStatusPanel.vue";
import TopProductsPanel from "./components/TopProductsPanel.vue";
import FlashSalePanel from "./components/FlashSalePanel.vue";

defineOptions({
  name: "Dashboard"
});

/**
 * 运营工作台。
 *
 * 结构：核心指标 → 订单趋势 + 状态分布 → 热销商品 + 秒杀统计。
 *
 * 加载策略：
 * - 每个分区自己持有 `loading / error / data`（见 `useAsyncSection`），
 *   单个接口失败只影响该分区，其余分区照常展示。
 * - 页面级"刷新全部"使用 `Promise.allSettled`，即使某个分区抛错也不会中断其它分区。
 * - 不使用任何假数据填充失败分区；失败就是失败，可重试。
 */
const metricRef = ref<InstanceType<typeof MetricCards>>();
const trendRef = ref<InstanceType<typeof OrderTrendPanel>>();
const statusRef = ref<InstanceType<typeof OrderStatusPanel>>();
const topRef = ref<InstanceType<typeof TopProductsPanel>>();
const flashRef = ref<InstanceType<typeof FlashSalePanel>>();

const refreshing = ref(false);
const refreshedAt = ref("");

async function refreshAll() {
  if (refreshing.value) return;
  refreshing.value = true;
  try {
    await Promise.allSettled([
      metricRef.value?.reload(),
      trendRef.value?.reload(),
      statusRef.value?.reload(),
      topRef.value?.reload(),
      flashRef.value?.reload()
    ]);
  } finally {
    refreshedAt.value = new Date().toLocaleTimeString("zh-CN", {
      hour: "2-digit",
      minute: "2-digit"
    });
    refreshing.value = false;
  }
}
</script>

<template>
  <div class="dashboard">
    <div class="dashboard-header">
      <div>
        <p class="dashboard-eyebrow">OVERVIEW / 运营概览</p>
        <h2 class="dashboard-title">每一笔业务，清晰在握。</h2>
        <p class="dashboard-desc">
          关注今日表现，掌握商品、订单与秒杀活动的运营状态。
        </p>
      </div>
      <div class="dashboard-refresh">
        <span v-if="refreshedAt">最近刷新 {{ refreshedAt }}</span
        ><el-button :loading="refreshing" @click="refreshAll"
          ><template #loading><BrandLoadingIcon /></template>刷新数据
          ↻</el-button
        >
      </div>
    </div>

    <MetricCards ref="metricRef" />

    <div class="dashboard-row">
      <OrderTrendPanel ref="trendRef" />
      <OrderStatusPanel ref="statusRef" />
    </div>

    <div class="dashboard-row">
      <TopProductsPanel ref="topRef" />
      <FlashSalePanel ref="flashRef" />
    </div>
  </div>
</template>

<style lang="scss" scoped>
.dashboard-eyebrow {
  margin-bottom: 12px;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.12em;
  color: var(--el-text-color-secondary);
}
.dashboard-refresh {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 12px;
  font-size: 11px;
  color: var(--el-text-color-secondary);
}
.dashboard {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.dashboard-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 4px 2px;
}

.dashboard-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.dashboard-desc {
  margin: 6px 0 0;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.dashboard-row {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}

@media (width <= 1280px) {
  .dashboard-row {
    grid-template-columns: 1fr;
  }
}
</style>
