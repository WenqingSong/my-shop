<script setup lang="ts">
import { computed, onMounted } from "vue";
import { getDashboardOverview } from "@/api/modules/dashboard";
import { useAsyncSection } from "../composables/useAsyncSection";
import { formatThousands } from "@/utils/number";
import { formatCentsAsCny } from "@/utils/money";

defineOptions({ name: "DashboardMetricCards" });

const { data, loading, error, loaded, load } =
  useAsyncSection(getDashboardOverview);

/**
 * 六项核心指标。
 *
 * 关于金额：后端字段 `today_order_amount` 单位是**分**，口径是"今日已支付订单金额"，
 * 不是退款后的实收金额，因此标题严格使用「今日已支付订单金额」。
 */
const cards = computed(() => {
  const overview = data.value;
  return [
    {
      key: "user_total",
      label: "用户总数",
      value: overview ? formatThousands(overview.user_total) : "-"
    },
    {
      key: "today_new_users",
      label: "今日新增用户",
      value: overview ? formatThousands(overview.today_new_users) : "-"
    },
    {
      key: "product_total",
      label: "商品总数",
      value: overview ? formatThousands(overview.product_total) : "-"
    },
    {
      key: "order_total",
      label: "订单总数",
      value: overview ? formatThousands(overview.order_total) : "-"
    },
    {
      key: "today_new_orders",
      label: "今日新增订单",
      value: overview ? formatThousands(overview.today_new_orders) : "-"
    },
    {
      key: "today_order_amount",
      label: "今日已支付订单金额",
      value: overview ? formatCentsAsCny(overview.today_order_amount) : "-",
      hint: "单位：元（后端以分返回，已换算）"
    }
  ];
});

const isEmpty = computed(() => loaded.value && !error.value && !data.value);

onMounted(load);

defineExpose({ reload: load });
</script>

<template>
  <el-card shadow="never" class="metric-card">
    <template #header>
      <div class="panel-header">
        <span class="panel-title">核心业务指标</span>
        <div class="panel-actions">
          <span class="panel-sub">今日数据按业务时区切分</span>
          <el-button link :loading="loading" @click="load">刷新</el-button>
        </div>
      </div>
    </template>

    <div v-loading="loading" class="panel-body">
      <el-alert
        v-if="error"
        :title="error.message"
        :type="error.forbidden ? 'warning' : 'error'"
        :closable="false"
        show-icon
      >
        <template #default>
          <el-button link type="primary" @click="load">重试</el-button>
        </template>
      </el-alert>

      <el-empty v-else-if="isEmpty" description="暂无统计数据" />

      <div v-else class="metric-grid">
        <div v-for="card in cards" :key="card.key" class="metric-item">
          <div class="metric-label">
            {{ card.label }}
            <el-tooltip v-if="card.hint" :content="card.hint" placement="top">
              <span class="metric-hint">?</span>
            </el-tooltip>
          </div>
          <div class="metric-value">{{ card.value }}</div>
        </div>
      </div>
    </div>
  </el-card>
</template>

<style lang="scss" scoped>
.metric-grid {
  display: grid;
  grid-template-columns: repeat(6, minmax(0, 1fr));
  gap: 12px;
}

.metric-item {
  padding: 14px 16px;
  background: var(--el-fill-color-lighter);
  border-radius: 6px;
}

.metric-label {
  display: flex;
  gap: 4px;
  align-items: center;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.metric-hint {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 14px;
  height: 14px;
  font-size: 10px;
  color: var(--el-text-color-placeholder);
  cursor: help;
  border: 1px solid var(--el-border-color);
  border-radius: 50%;
}

.metric-value {
  margin-top: 8px;
  font-size: 22px;
  font-weight: 600;
  line-height: 1.2;
  color: var(--el-text-color-primary);
  word-break: break-all;
}

@media (width <= 1440px) {
  .metric-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

@media (width <= 768px) {
  .metric-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
</style>
