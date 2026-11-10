<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import {
  useECharts,
  type ElementRef,
  type UtilsEChartsOption
} from "@pureadmin/utils";
import type { EChartsCoreOption } from "echarts/core";
import type { SectionError } from "../composables/useAsyncSection";

defineOptions({ name: "DashboardChartPanel" });

const props = defineProps<{
  title: string;
  loading: boolean;
  error: SectionError | null;
  /** 是否为空数据（已加载但无数据） */
  empty: boolean;
  /** 图表配置；为 null 时不渲染 */
  option: EChartsCoreOption | null;
  emptyText?: string;
}>();

const emit = defineEmits<{ (event: "retry"): void }>();

const chartRef = ref<HTMLDivElement>();
const { setOptions, resize, clear } = useECharts(
  chartRef as unknown as ElementRef<HTMLDivElement>
);

const canRender = computed(
  () => !props.loading && !props.error && !props.empty && !!props.option
);

function render() {
  if (!props.option || !canRender.value) return;
  // 先清空再渲染：切换数据源时整体替换，避免残留旧系列
  clear();
  setOptions(props.option as unknown as UtilsEChartsOption);
  resize();
}

watch(
  () => props.option,
  () => render(),
  { flush: "post" }
);
watch(
  () => props.loading,
  value => {
    if (!value) render();
  }
);
onMounted(() => render());
</script>

<template>
  <el-card shadow="never" class="panel">
    <template #header>
      <div class="panel-header">
        <span class="panel-title">{{ title }}</span>
        <div class="panel-actions">
          <slot name="extra" />
          <el-button link :loading="loading" @click="emit('retry')">
            刷新
          </el-button>
        </div>
      </div>
    </template>

    <div v-loading="loading" class="panel-body chart-body">
      <el-alert
        v-if="error"
        :title="error.message"
        :type="error.forbidden ? 'warning' : 'error'"
        :closable="false"
        show-icon
      >
        <template #default>
          <el-button link type="primary" @click="emit('retry')">重试</el-button>
        </template>
      </el-alert>

      <el-empty
        v-else-if="empty"
        :description="emptyText || '暂无数据'"
        :image-size="80"
      />

      <div v-show="canRender" ref="chartRef" class="chart" />
    </div>
  </el-card>
</template>

<style lang="scss" scoped>
.chart-body {
  min-height: 280px;
}

.chart {
  width: 100%;
  height: 280px;
}
</style>
