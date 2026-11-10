<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { resolveErrorMessage } from "@/utils/error-message";
import { formatServerDateTime } from "@/utils/datetime";
import { listProducts, getProduct } from "@/api/modules/product";
import { listInventoryLogs } from "@/api/modules/inventory";
import type { SkuDTO } from "@/api/types/sku";
import type { ProductDTO } from "@/api/types/product";
import {
  toInventoryLogView,
  type InventoryLogView
} from "@/api/adapters/inventory-adapter";

/**
 * 库存流水（只读）。
 *
 * 后端只有 `GET /admin/inventories/:sku_id/logs`，因此必须先确定 SKU。
 * 页面支持两种进入方式：
 * 1. 从库存页带 `sku_id` / `product_id` 跳转；
 * 2. 在页面内按商品名称搜索 → 选择商品 → 选择 SKU。
 *
 * 流水只查询，不提供任何修改/删除入口（后端也没有对应接口）。
 */
defineOptions({ name: "InventoryTransactions" });

const route = useRoute();
const router = useRouter();

const productKeyword = ref("");
const productOptions = ref<ProductDTO[]>([]);
const productLoading = ref(false);
const selectedProductId = ref<number | null>(null);
const productName = ref("");
const skuOptions = ref<SkuDTO[]>([]);
const selectedSkuId = ref<number | null>(null);
const selectedSkuName = ref("");

const logs = ref<InventoryLogView[]>([]);
const total = ref(0);
const page = ref(1);
const size = ref(10);
const loading = ref(false);
const error = ref("");

const hasSku = computed(() => selectedSkuId.value !== null);

const skuLabel = computed(() => {
  if (!selectedSkuId.value) return "";
  const name = selectedSkuName.value || `SKU #${selectedSkuId.value}`;
  return `${name}（ID ${selectedSkuId.value}）`;
});

function resetLogs() {
  logs.value = [];
  total.value = 0;
}

async function loadLogs() {
  if (!selectedSkuId.value) {
    resetLogs();
    return;
  }

  loading.value = true;
  error.value = "";
  try {
    const result = await listInventoryLogs(selectedSkuId.value, {
      page: page.value,
      size: size.value
    });
    logs.value = (result.items ?? []).map(toInventoryLogView);
    total.value = result.total;
  } catch (cause) {
    logs.value = [];
    total.value = 0;
    error.value = resolveErrorMessage(cause, "库存流水加载失败");
  } finally {
    loading.value = false;
  }
}

function syncQuery() {
  void router.replace({
    path: "/inventory/transactions",
    query: {
      ...(selectedSkuId.value ? { sku_id: String(selectedSkuId.value) } : {}),
      ...(selectedProductId.value
        ? { product_id: String(selectedProductId.value) }
        : {})
    }
  });
}

async function searchProducts() {
  productLoading.value = true;
  try {
    const result = await listProducts({
      page: 1,
      size: 20,
      keyword: productKeyword.value.trim() || undefined,
      sort: "id",
      order: "desc"
    });
    productOptions.value = result.items ?? [];
  } catch (cause) {
    productOptions.value = [];
    error.value = resolveErrorMessage(cause, "商品搜索失败");
  } finally {
    productLoading.value = false;
  }
}

async function loadProductSkus(productId: number) {
  try {
    const detail = await getProduct(productId);
    selectedProductId.value = detail.id;
    productName.value = detail.name;
    skuOptions.value = detail.skus ?? [];
  } catch (cause) {
    skuOptions.value = [];
    error.value = resolveErrorMessage(cause, "商品 SKU 加载失败");
  }
}

async function handleProductChange(productId: number) {
  productName.value = "";
  selectedSkuId.value = null;
  selectedSkuName.value = "";
  resetLogs();

  await loadProductSkus(productId);

  if (skuOptions.value.length > 0) {
    selectedSkuId.value = skuOptions.value[0].id;
    selectedSkuName.value = skuOptions.value[0].name;
    page.value = 1;
    syncQuery();
    await loadLogs();
  }
}

async function handleSkuChange(skuId: number) {
  selectedSkuId.value = skuId;
  selectedSkuName.value =
    skuOptions.value.find(sku => sku.id === skuId)?.name ?? "";
  page.value = 1;
  syncQuery();
  await loadLogs();
}

function handlePageChange(next: number) {
  page.value = next;
  void loadLogs();
}

function handleSizeChange(next: number) {
  size.value = next;
  page.value = 1;
  void loadLogs();
}

/** 从 URL 恢复上下文（库存页跳转 / 刷新） */
async function restoreFromQuery() {
  const querySkuId = Number(route.query.sku_id);
  const queryProductId = Number(route.query.product_id);

  if (queryProductId > 0) {
    await loadProductSkus(queryProductId);
  }

  if (querySkuId > 0) {
    selectedSkuId.value = querySkuId;
    selectedSkuName.value =
      skuOptions.value.find(sku => sku.id === querySkuId)?.name ?? "";
    await loadLogs();
    return;
  }

  if (queryProductId > 0 && skuOptions.value.length > 0) {
    selectedSkuId.value = skuOptions.value[0].id;
    selectedSkuName.value = skuOptions.value[0].name;
    syncQuery();
    await loadLogs();
  }
}

onMounted(async () => {
  await Promise.all([searchProducts(), restoreFromQuery()]);
});
</script>

<template>
  <div class="transactions-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">库存流水</h2>
        <p class="page-desc">
          库存变更历史只读展示；流水由后端在增加 / 扣减库存时自动写入
        </p>
      </div>
      <div class="page-actions">
        <el-button @click="loadLogs">刷新</el-button>
        <el-button @click="router.push('/inventory')">返回库存查询</el-button>
      </div>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="关于调整原因"
      description="后端增减库存的请求体只有 quantity，没有 reason，因此流水「原因」恒为空；页面不做伪造填充。"
    />

    <el-card shadow="never">
      <el-form :inline="true" @submit.prevent>
        <el-form-item label="商品搜索">
          <el-input
            v-model="productKeyword"
            placeholder="商品名称 / 品牌"
            clearable
            class="filter-keyword"
            @keyup.enter="searchProducts"
          />
        </el-form-item>
        <el-form-item>
          <el-button :loading="productLoading" @click="searchProducts">
            搜索商品
          </el-button>
        </el-form-item>
        <el-form-item label="商品">
          <el-select
            :model-value="selectedProductId"
            placeholder="选择商品"
            filterable
            class="filter-product"
            @update:model-value="handleProductChange"
          >
            <el-option
              v-for="product in productOptions"
              :key="product.id"
              :label="`${product.name}（ID ${product.id}）`"
              :value="product.id"
            />
          </el-select>
        </el-form-item>
        <el-form-item label="SKU">
          <el-select
            :model-value="selectedSkuId"
            placeholder="选择 SKU"
            filterable
            :disabled="skuOptions.length === 0"
            class="filter-sku"
            @update:model-value="handleSkuChange"
          >
            <el-option
              v-for="sku in skuOptions"
              :key="sku.id"
              :label="`${sku.name}（ID ${sku.id}）`"
              :value="sku.id"
            />
          </el-select>
        </el-form-item>
      </el-form>

      <div v-if="hasSku" class="current-sku">
        当前 SKU：<strong>{{ skuLabel }}</strong>
        <span v-if="productName" class="current-sku__product">
          · 商品：{{ productName }}
        </span>
      </div>

      <el-alert
        v-if="error"
        class="block-alert"
        type="error"
        :closable="false"
        show-icon
        :title="error"
      >
        <template #default>
          <el-button link type="primary" @click="loadLogs">重试</el-button>
        </template>
      </el-alert>

      <el-empty v-if="!hasSku" description="请先选择商品与 SKU" />

      <el-table v-else v-loading="loading" :data="logs" border stripe>
        <el-table-column prop="id" label="流水 ID" width="100" />
        <el-table-column prop="skuId" label="SKU ID" width="100" />
        <el-table-column label="操作类型" width="110">
          <template #default="{ row }">
            <el-tag
              :type="row.changeType === 'increase' ? 'success' : 'warning'"
              size="small"
            >
              {{ row.changeTypeLabel }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="变更量" width="100" align="right">
          <template #default="{ row }">
            <span
              :class="
                row.changeType === 'increase'
                  ? 'delta-increase'
                  : 'delta-deduct'
              "
            >
              {{ row.deltaText }}
            </span>
          </template>
        </el-table-column>
        <el-table-column
          prop="beforeQty"
          label="变更前"
          width="100"
          align="right"
        />
        <el-table-column
          prop="afterQty"
          label="变更后"
          width="100"
          align="right"
        />
        <el-table-column label="原因" min-width="140">
          <template #default="{ row }">{{ row.reasonText }}</template>
        </el-table-column>
        <el-table-column label="操作人" min-width="130">
          <template #default="{ row }">{{ row.operatorText }}</template>
        </el-table-column>
        <el-table-column label="创建时间" min-width="170">
          <template #default="{ row }">
            {{ formatServerDateTime(row.createdAt) }}
          </template>
        </el-table-column>
      </el-table>

      <div v-if="hasSku" class="pagination-bar">
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
.transactions-page {
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

.page-actions {
  display: flex;
  gap: 8px;
}

.filter-keyword {
  width: 190px;
}

.filter-product {
  width: 220px;
}

.filter-sku {
  width: 200px;
}

.current-sku {
  padding: 8px 0;
  font-size: 13px;
  color: var(--el-text-color-regular);
}

.current-sku__product {
  color: var(--el-text-color-secondary);
}

.block-alert {
  margin-bottom: 12px;
}

.delta-increase {
  font-weight: 600;
  color: var(--el-color-success);
}

.delta-deduct {
  font-weight: 600;
  color: var(--el-color-warning);
}

.pagination-bar {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
</style>
