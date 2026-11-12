<script setup lang="ts">
import { computed, onMounted } from "vue";
import { getDashboardFlashSales } from "@/api/modules/dashboard";
import { useAsyncSection } from "../composables/useAsyncSection";
import { formatThousands } from "@/utils/number";

defineOptions({ name: "DashboardFlashSalePanel" });

const { data, loading, error, loaded, load } = useAsyncSection(
  getDashboardFlashSales
);

const isEmpty = computed(() => loaded.value && !error.value && !data.value);

/** 秒杀活动与请求处理情况：字段与后端 `FlashSalesRes` 一一对应，不含派生指标 */
const groups = computed(() => {
  const stats = data.value;
  if (!stats) return [];
  return [
    {
      title: "秒杀活动",
      items: [
        { label: "活动总数", value: formatThousands(stats.activity_total) },
        {
          label: "进行中",
          value: formatThousands(stats.activity_status.enabled)
        },
        {
          label: "已停用",
          value: formatThousands(stats.activity_status.disabled)
        }
      ]
    },
    {
      title: "请求分布",
      items: [
        {
          label: "排队中",
          value: formatThousands(stats.request_status.queued)
        },
        { label: "成功", value: formatThousands(stats.request_status.success) },
        { label: "失败", value: formatThousands(stats.request_status.failed) },
        { label: "死信", value: formatThousands(stats.request_status.dead) }
      ]
    },
    {
      title: "实时状态",
      items: [
        { label: "当前排队", value: formatThousands(stats.current_queued) },
        { label: "秒杀成功订单", value: formatThousands(stats.success_orders) }
      ]
    }
  ];
});

onMounted(load);

defineExpose({ reload: load });
</script>

<template>
  <el-card shadow="never" class="panel">
    <template #header>
      <div class="panel-header">
        <span class="panel-title">秒杀运营统计</span>
        <div class="panel-actions">
          <span class="panel-sub">活动状态与请求处理分布</span>
          <el-button link :loading="loading" @click="load"
            ><template #loading><BrandLoadingIcon /></template>刷新</el-button
          >
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

      <el-empty
        v-else-if="isEmpty"
        description="暂无秒杀数据"
        :image-size="80"
      />

      <div v-else class="flash-groups">
        <div v-for="group in groups" :key="group.title" class="flash-group">
          <div class="group-title">{{ group.title }}</div>
          <div class="group-items">
            <div
              v-for="item in group.items"
              :key="item.label"
              class="group-item"
            >
              <span class="item-label">{{ item.label }}</span>
              <span class="item-value">{{ item.value }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </el-card>
</template>

<style lang="scss" scoped>
.flash-groups {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}

.flash-group {
  padding: 14px 16px;
  background: var(--el-fill-color-lighter);
  border-radius: 6px;
}

.group-title {
  margin-bottom: 10px;
  font-size: 13px;
  font-weight: 600;
  color: var(--el-text-color-regular);
}

.group-items {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.group-item {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
}

.item-label {
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.item-value {
  font-size: 18px;
  font-weight: 600;
  color: var(--el-text-color-primary);
}

@media (width <= 1200px) {
  .flash-groups {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (width <= 768px) {
  .flash-groups {
    grid-template-columns: 1fr;
  }
}
</style>
