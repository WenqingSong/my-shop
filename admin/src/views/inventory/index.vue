<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { useRouter } from "vue-router";
import { hasPerms } from "@/utils/auth";
import { resolveErrorMessage } from "@/utils/error-message";
import { formatCentsAsCny } from "@/utils/money";
import { PERMISSION } from "@/utils/permission";
import { getProduct, listProducts } from "@/api/modules/product";
import { getInventory } from "@/api/modules/inventory";
import type { SkuDTO } from "@/api/types/sku";
import {
  productStatusTagType,
  toProductView,
  type ProductView
} from "@/api/adapters/product-adapter";
import { formatSkuStatus } from "@/api/adapters/sku-adapter";
import InventoryAdjustDialog from "./components/InventoryAdjustDialog.vue";

/**
 * 库存查询。
 *
 * 后端没有库存列表接口，也没有按商品/名称搜索库存的能力。
 * 这里的"库存列表"是真实接口组合：
 * 商品列表/商品详情（含 SKU）→ 逐 SKU 查询库存。
 *
 * 因此：
 * - 不支持"全站库存一次性排序/导出"（后端没有该能力）；
 * - 锁定库存 / 可售库存后端未提供，页面不展示、不推导；
 * - 库存调整只能走 increase / deduct 增量接口。
 */
defineOptions({ name: "InventoryList" });

const router = useRouter();

const canIncrease = computed(() => hasPerms(PERMISSION.INVENTORY_INCREASE));
const canDeduct = computed(() => hasPerms(PERMISSION.INVENTORY_DEDUCT));
const canAdjust = computed(() => canIncrease.value || canDeduct.value);

const keyword = ref("");
const productIdInput = ref<number | undefined>(undefined);
const skuKeyword = ref("");

const products = ref<ProductView[]>([]);
const productTotal = ref(0);
const productPage = ref(1);
const productSize = ref(10);
const productsLoading = ref(false);
const productsError = ref("");

const selectedProductId = ref<number | null>(null);
const selectedProductName = ref("");
const skus = ref<SkuDTO[]>([]);
const skusLoading = ref(false);
const skusError = ref("");

/** SKU 库存：`null` 加载中，`-1` 未知 */
const inventoryMap = reactive<Record<number, number | null>>({});

const adjustVisible = ref(false);
const adjustSku = ref<SkuDTO | null>(null);
const adjustCurrent = ref<number | null>(null);

/** 商品切换的竞态令牌：只接受最后一次选择的结果 */
let selectToken = 0;

const filteredSkus = computed(() => {
  const search = skuKeyword.value.trim().toLowerCase();
  if (!search) return skus.value;
  return skus.value.filter(sku => sku.name.toLowerCase().includes(search));
});

async function loadInventories(skuIds: number[]) {
  for (const id of skuIds) inventoryMap[id] = null;

  const queue = [...skuIds];
  const workers = Array.from(
    { length: Math.min(4, queue.length) },
    async () => {
      for (;;) {
        const next = queue.shift();
        if (!next) return;
        try {
          const result = await getInventory(next);
          inventoryMap[next] = result.quantity;
        } catch {
          inventoryMap[next] = -1;
        }
      }
    }
  );

  await Promise.all(workers);
}

async function selectProduct(id: number) {
  const token = ++selectToken;
  selectedProductId.value = id;
  skusLoading.value = true;
  skusError.value = "";
  skus.value = [];

  try {
    const detail = await getProduct(id);
    if (token !== selectToken) return;
    selectedProductName.value = detail.name;
    skus.value = detail.skus ?? [];
    await loadInventories(skus.value.map(sku => sku.id));
  } catch (cause) {
    if (token !== selectToken) return;
    skusError.value = resolveErrorMessage(cause, "SKU 库存加载失败");
  } finally {
    if (token === selectToken) skusLoading.value = false;
  }
}

function clearSelection() {
  selectToken += 1;
  selectedProductId.value = null;
  selectedProductName.value = "";
  skus.value = [];
  skusError.value = "";
  skusLoading.value = false;
}

async function loadProducts() {
  productsLoading.value = true;
  productsError.value = "";

  const exactId = productIdInput.value;
  if (typeof exactId === "number" && exactId > 0) {
    try {
      const detail = await getProduct(exactId);
      products.value = [toProductView(detail)];
      productTotal.value = 1;
      await selectProduct(detail.id);
    } catch (cause) {
      products.value = [];
      productTotal.value = 0;
      productsError.value = resolveErrorMessage(cause, "商品不存在或加载失败");
      clearSelection();
    } finally {
      productsLoading.value = false;
    }
    return;
  }

  try {
    const result = await listProducts({
      page: productPage.value,
      size: productSize.value,
      keyword: keyword.value.trim() || undefined,
      sort: "id",
      order: "desc"
    });
    products.value = (result.items ?? []).map(toProductView);
    productTotal.value = result.total;

    if (products.value.length > 0) {
      await selectProduct(products.value[0].id);
    } else {
      clearSelection();
    }
  } catch (cause) {
    products.value = [];
    productTotal.value = 0;
    productsError.value = resolveErrorMessage(cause, "商品列表加载失败");
    clearSelection();
  } finally {
    productsLoading.value = false;
  }
}

function searchProducts() {
  productPage.value = 1;
  void loadProducts();
}

function resetFilters() {
  keyword.value = "";
  productIdInput.value = undefined;
  skuKeyword.value = "";
  productPage.value = 1;
  void loadProducts();
}

function handleProductPageChange(next: number) {
  productPage.value = next;
  void loadProducts();
}

function handleProductSizeChange(next: number) {
  productSize.value = next;
  productPage.value = 1;
  void loadProducts();
}

function handleRowClick(row: ProductView) {
  if (row.id === selectedProductId.value) return;
  void selectProduct(row.id);
}

function inventoryText(skuId: number) {
  const value = inventoryMap[skuId];
  if (value === null || value === undefined) return "…";
  if (value < 0) return "—";
  return String(value);
}

function openAdjust(sku: SkuDTO) {
  adjustSku.value = sku;
  adjustCurrent.value = inventoryMap[sku.id] ?? null;
  adjustVisible.value = true;
}

function handleAdjusted(quantity: number) {
  if (adjustSku.value) inventoryMap[adjustSku.value.id] = quantity;
}

function goTransactions(sku: SkuDTO) {
  void router.push({
    path: "/inventory/transactions",
    query: {
      sku_id: String(sku.id),
      ...(selectedProductId.value
        ? { product_id: String(selectedProductId.value) }
        : {})
    }
  });
}

function skuCountOf(product: ProductView) {
  // 商品列表不返回 SKU 数量时，仅当前选中的商品有真实明细
  if (product.id === selectedProductId.value) return skus.value.length;
  return null;
}

onMounted(loadProducts);
</script>

<template>
  <div class="inventory-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">库存查询</h2>
        <p class="page-desc">按商品查看各规格库存，并执行增加或扣减。</p>
      </div>
      <el-button @click="loadProducts">刷新</el-button>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="关于库存口径"
      description="库存数量为当前记录值，不区分锁定库存与可售库存。调整时请输入增加或扣减的数量，不能直接设置总量。"
    />

    <el-card shadow="never">
      <el-form :inline="true" @submit.prevent>
        <el-form-item label="商品名称">
          <el-input
            v-model="keyword"
            placeholder="名称 / 品牌"
            clearable
            class="filter-keyword"
            @keyup.enter="searchProducts"
          />
        </el-form-item>
        <el-form-item label="商品 ID">
          <el-input-number
            v-model="productIdInput"
            :min="1"
            :step="1"
            step-strictly
            controls-position="right"
            placeholder="精确查询"
          />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" @click="searchProducts">查询</el-button>
          <el-button @click="resetFilters">重置</el-button>
        </el-form-item>
      </el-form>

      <el-alert
        v-if="productsError"
        class="block-alert"
        type="error"
        :closable="false"
        show-icon
        :title="productsError"
      />

      <el-table
        v-loading="productsLoading"
        :data="products"
        row-key="id"
        highlight-current-row
        border
        @row-click="handleRowClick"
      >
        <el-table-column label="商品" min-width="240">
          <template #default="{ row }">
            <div class="product-cell">
              <el-image
                v-if="row.mainImage"
                :src="row.mainImage"
                fit="cover"
                class="product-thumb"
              />
              <div>
                <div class="product-name">{{ row.name }}</div>
                <div class="product-meta">ID：{{ row.id }}</div>
              </div>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="价格" width="130">
          <template #default="{ row }">{{ row.priceText }}</template>
        </el-table-column>
        <el-table-column label="SKU 数量" width="110" align="center">
          <template #default="{ row }">
            {{ skuCountOf(row) ?? "点击查看" }}
          </template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <el-tag :type="productStatusTagType(row.status)" size="small">
              {{ row.statusLabel }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="130" align="center">
          <template #default="{ row }">
            <el-button link type="primary" @click.stop="selectProduct(row.id)">
              查看库存
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <div class="pagination-bar">
        <el-pagination
          :current-page="productPage"
          :page-size="productSize"
          :total="productTotal"
          :page-sizes="[10, 20, 50]"
          layout="total, sizes, prev, pager, next"
          background
          @current-change="handleProductPageChange"
          @size-change="handleProductSizeChange"
        />
      </div>
    </el-card>

    <el-card shadow="never">
      <template #header>
        <div class="card-header">
          <span>
            SKU 库存
            <span v-if="selectedProductId" class="card-sub">
              · {{ selectedProductName }}（ID {{ selectedProductId }}）
            </span>
          </span>
          <el-input
            v-model="skuKeyword"
            placeholder="按 SKU 名称过滤"
            clearable
            class="filter-sku"
          />
        </div>
      </template>

      <el-alert
        v-if="skusError"
        class="block-alert"
        type="error"
        :closable="false"
        show-icon
        :title="skusError"
      />

      <el-empty
        v-if="!selectedProductId"
        description="请选择上方商品查看 SKU 库存"
      />

      <el-table v-else v-loading="skusLoading" :data="filteredSkus" border>
        <el-table-column prop="id" label="SKU ID" width="100" />
        <el-table-column prop="name" label="SKU 名称" min-width="200" />
        <el-table-column label="价格" width="130">
          <template #default="{ row }">
            {{ formatCentsAsCny(row.price) }}
          </template>
        </el-table-column>
        <el-table-column label="SKU 状态" width="110">
          <template #default="{ row }">
            <el-tag
              :type="row.status === 'enabled' ? 'success' : 'info'"
              size="small"
            >
              {{ formatSkuStatus(row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="当前库存" width="120" align="center">
          <template #default="{ row }">
            <span class="inventory-value">{{ inventoryText(row.id) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="210" align="center">
          <template #default="{ row }">
            <el-button
              v-if="canAdjust"
              link
              type="primary"
              @click="openAdjust(row)"
            >
              调整库存
            </el-button>
            <el-button link type="primary" @click="goTransactions(row)">
              库存流水
            </el-button>
          </template>
        </el-table-column>
      </el-table>

      <p v-if="selectedProductId" class="table-hint">
        当前库存为逐 SKU 实时查询结果；进入「库存流水」可查看变更历史。
      </p>
    </el-card>

    <InventoryAdjustDialog
      v-model="adjustVisible"
      :sku="adjustSku"
      :product-name="selectedProductName"
      :current-quantity="adjustCurrent"
      @saved="handleAdjusted"
    />
  </div>
</template>

<style lang="scss" scoped>
.inventory-page {
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

.filter-keyword {
  width: 200px;
}

.filter-sku {
  width: 220px;
}

.block-alert {
  margin-bottom: 12px;
}

.product-cell {
  display: flex;
  gap: 10px;
  align-items: center;
}

.product-thumb {
  width: 40px;
  height: 40px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 4px;
}

.product-name {
  font-size: 13px;
  font-weight: 500;
  color: var(--el-text-color-primary);
}

.product-meta {
  margin-top: 2px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.card-header {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  justify-content: space-between;
  font-weight: 600;
}

.card-sub {
  font-size: 12px;
  font-weight: 400;
  color: var(--el-text-color-secondary);
}

.inventory-value {
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.pagination-bar {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}

.table-hint {
  margin: 10px 0 0;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
</style>
