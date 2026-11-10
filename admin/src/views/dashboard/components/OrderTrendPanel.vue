<script setup lang="ts">
import { computed, onMounted } from "vue";
import { getDashboardOrderTrend } from "@/api/modules/dashboard";
import { useAsyncSection } from "../composables/useAsyncSection";
import { buildOrderTrendOption } from "../utils/chart-options";
import ChartPanel from "./ChartPanel.vue";

defineOptions({ name: "DashboardOrderTrendPanel" });

const { data, loading, error, loaded, load } = useAsyncSection(
  getDashboardOrderTrend
);

const empty = computed(
  () => loaded.value && !error.value && (data.value?.days?.length ?? 0) === 0
);

const option = computed(() =>
  data.value?.days?.length ? buildOrderTrendOption(data.value.days) : null
);

onMounted(load);

defineExpose({ reload: load });
</script>

<template>
  <ChartPanel
    title="订单趋势（最近 7 天）"
    :loading="loading"
    :error="error"
    :empty="empty"
    :option="option"
    empty-text="最近 7 天暂无订单"
    @retry="load"
  />
</template>
