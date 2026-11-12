<script setup lang="ts">
import { computed, onMounted } from "vue";
import { getDashboardTopProducts } from "@/api/modules/dashboard";
import { useAsyncSection } from "../composables/useAsyncSection";
import { formatThousands } from "@/utils/number";

defineOptions({ name: "DashboardTopProductsPanel" });

/** 后端默认 10，上限 50 */
const TOP_LIMIT = 10;

const { data, loading, error, loaded, load } = useAsyncSection(() =>
  getDashboardTopProducts(TOP_LIMIT)
);

const rows = computed(() => data.value?.items ?? []);

const empty = computed(
  () => loaded.value && !error.value && rows.value.length === 0
);

onMounted(load);

defineExpose({ reload: load });
</script>

<template>
  <el-card shadow="never" class="panel">
    <template #header>
      <div class="panel-header">
        <span class="panel-title">热销商品 TOP {{ TOP_LIMIT }}</span>
        <div class="panel-actions">
          <span class="panel-sub">按有效订单销量统计</span>
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

      <el-empty v-else-if="empty" description="暂无销量数据" :image-size="80" />

      <el-table v-else :data="rows" size="default" border stripe>
        <el-table-column prop="rank" label="排名" width="80" align="center" />
        <el-table-column prop="product_id" label="商品 ID" width="140" />
        <el-table-column prop="product_name" label="商品名称" min-width="220" />
        <el-table-column label="销量" width="120" align="right">
          <template #default="{ row }">
            {{ formatThousands(row.sales) }}
          </template>
        </el-table-column>
      </el-table>
    </div>
  </el-card>
</template>
