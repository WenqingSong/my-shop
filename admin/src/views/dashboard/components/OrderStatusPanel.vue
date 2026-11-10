<script setup lang="ts">
import { computed, onMounted } from "vue";
import {
  getDashboardOrderStatus,
  getDefaultDashboardStatusQuery
} from "@/api/modules/dashboard";
import { useAsyncSection } from "../composables/useAsyncSection";
import { buildOrderStatusOption } from "../utils/chart-options";
import { formatThousands } from "@/utils/number";
import ChartPanel from "./ChartPanel.vue";

defineOptions({ name: "DashboardOrderStatusPanel" });

/**
 * 默认查询最近 7 天。
 *
 * 后端要求 `start_time` / `end_time` 必须同传且为 RFC3339，
 * 时间窗口径见 `@/utils/datetime` 的 `getRecentDaysRange`（左闭右开）。
 */
const { data, loading, error, loaded, load } = useAsyncSection(() =>
  getDashboardOrderStatus(getDefaultDashboardStatusQuery())
);

const distribution = computed(() => data.value?.status_distribution ?? []);

const empty = computed(
  () =>
    loaded.value &&
    !error.value &&
    distribution.value.every(item => item.count === 0)
);

const option = computed(() =>
  distribution.value.some(item => item.count > 0)
    ? buildOrderStatusOption(distribution.value)
    : null
);

const total = computed(() => formatThousands(data.value?.total ?? 0));

onMounted(load);

defineExpose({ reload: load });
</script>

<template>
  <ChartPanel
    title="订单状态分布（最近 7 天）"
    :loading="loading"
    :error="error"
    :empty="empty"
    :option="option"
    empty-text="最近 7 天暂无订单"
    @retry="load"
  >
    <template #extra>
      <span v-if="data && !error" class="panel-sub">
        范围内总数 {{ total }}
      </span>
    </template>
  </ChartPanel>
</template>
