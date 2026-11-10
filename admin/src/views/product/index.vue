<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useRouter } from "vue-router";
import { ElMessageBox } from "element-plus";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { formatServerDateTime } from "@/utils/datetime";
import { PERMISSION } from "@/utils/permission";
import { listCategories } from "@/api/modules/category";
import { offShelfProduct, onShelfProduct } from "@/api/modules/product";
import type { CategoryDTO } from "@/api/types/category";
import type { ProductStatus } from "@/api/types/product";
import {
  PRODUCT_STATUS_OPTIONS,
  productStatusTagType
} from "@/api/adapters/product-adapter";
import { toCategoryNameMap } from "@/api/adapters/category-adapter";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { useProductList } from "./composables/useProductList";
import ProductDetailDrawer from "./components/ProductDetailDrawer.vue";

defineOptions({ name: "ProductList" });

const router = useRouter();

const canCreate = computed(() => hasPerms(PERMISSION.PRODUCT_CREATE));
const canEdit = computed(() => hasPerms(PERMISSION.PRODUCT_UPDATE));
const canOnShelf = computed(() => hasPerms(PERMISSION.PRODUCT_ON_SHELF));
const canOffShelf = computed(() => hasPerms(PERMISSION.PRODUCT_OFF_SHELF));

const {
  rows,
  total,
  page,
  size,
  loading,
  enriching,
  error,
  truncated,
  filters,
  hasStatusFilter,
  skuCounts,
  load,
  search,
  reset,
  handlePageChange,
  handleSizeChange
} = useProductList();

const { run: runShelf } = useSubmitGuard();

const categoryTree = ref<CategoryDTO[]>([]);
const categoryNames = ref<Map<number, string>>(new Map());
const detailVisible = ref(false);
const detailId = ref<number | null>(null);

const statusOptions = [
  { value: "", label: "全部状态" },
  ...PRODUCT_STATUS_OPTIONS
];

function categoryNameOf(categoryId: number) {
  return categoryNames.value.get(categoryId) ?? `分类 #${categoryId}`;
}

function skuCountLabel(productId: number) {
  const value = skuCounts[productId];
  if (value === null || value === undefined) return enriching.value ? "…" : "—";
  if (value < 0) return "—";
  return String(value);
}

async function loadCategories() {
  try {
    const result = await listCategories();
    categoryTree.value = result.items ?? [];
    categoryNames.value = toCategoryNameMap(categoryTree.value);
  } catch {
    // 分类加载失败不影响商品列表，分类列退化为显示分类 ID
    categoryTree.value = [];
    categoryNames.value = new Map();
  }
}

async function toggleShelf(row: {
  id: number;
  name: string;
  status: ProductStatus;
}) {
  const action = row.status === "on_shelf" ? "下架" : "上架";

  try {
    await ElMessageBox.confirm(
      `确认${action}商品「${row.name}」？`,
      `${action}商品`,
      {
        type: "warning"
      }
    );
  } catch {
    return;
  }

  await runShelf(async () => {
    try {
      if (row.status === "on_shelf") {
        await offShelfProduct(row.id);
      } else {
        await onShelfProduct(row.id);
      }
      message(`${action}成功`, { type: "success" });
      await load();
    } catch (cause) {
      message(resolveErrorMessage(cause, `${action}失败`), { type: "error" });
    }
  });
}

function openDetail(id: number) {
  detailId.value = id;
  detailVisible.value = true;
}

function goEdit(id: number) {
  void router.push(`/products/${id}/edit`);
}

function goCreate() {
  void router.push("/products/create");
}

onMounted(async () => {
  await Promise.all([loadCategories(), load()]);
});
</script>

<template>
  <div class="product-list">
    <div class="page-header">
      <div>
        <h2 class="page-title">商品列表</h2>
        <p class="page-desc">
          商品基本信息与 SKU 分离维护；上下架通过后端状态迁移接口执行
        </p>
      </div>
      <el-button v-if="canCreate" type="primary" @click="goCreate">
        新建商品
      </el-button>
    </div>

    <el-card shadow="never">
      <el-form :inline="true" class="filter-form" @submit.prevent>
        <el-form-item label="商品名称">
          <el-input
            v-model="filters.keyword"
            placeholder="名称 / 品牌"
            clearable
            class="filter-keyword"
            @keyup.enter="search"
          />
        </el-form-item>

        <el-form-item label="商品分类">
          <el-tree-select
            v-model="filters.categoryId"
            :data="categoryTree"
            :props="{ label: 'name', children: 'children' }"
            node-key="id"
            check-strictly
            clearable
            :render-after-expand="false"
            placeholder="全部分类"
            class="filter-category"
          />
        </el-form-item>

        <el-form-item label="商品状态">
          <el-select v-model="filters.status" class="filter-status">
            <el-option
              v-for="option in statusOptions"
              :key="option.value"
              :label="option.label"
              :value="option.value"
            />
          </el-select>
        </el-form-item>

        <el-form-item>
          <el-button type="primary" @click="search">查询</el-button>
          <el-button @click="reset">重置</el-button>
        </el-form-item>
      </el-form>

      <el-alert
        v-if="hasStatusFilter"
        class="filter-notice"
        type="info"
        :closable="false"
        show-icon
        title="状态筛选由前端跨页过滤实现"
        description="后端 /admin/products 列表接口没有 status 参数，页面会分页拉取真实数据后在前端过滤。"
      />
      <el-alert
        v-if="truncated"
        class="filter-notice"
        type="warning"
        :closable="false"
        show-icon
        title="状态筛选结果可能不完整"
        description="已达到跨页拉取上限（5 页 × 100 条），请缩小关键词或分类范围。"
      />

      <el-alert
        v-if="error"
        class="filter-notice"
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
        <el-table-column label="商品" min-width="260">
          <template #default="{ row }">
            <div class="product-cell">
              <el-image
                v-if="row.mainImage"
                :src="row.mainImage"
                fit="cover"
                class="product-thumb"
                :preview-src-list="[row.mainImage]"
                preview-teleported
              />
              <div v-else class="product-thumb product-thumb--empty">无图</div>
              <div class="product-info">
                <div class="product-name">{{ row.name }}</div>
                <div class="product-meta">
                  <span>ID：{{ row.id }}</span>
                  <span v-if="row.brand">品牌：{{ row.brand }}</span>
                </div>
              </div>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="分类" min-width="130">
          <template #default="{ row }">
            {{ categoryNameOf(row.categoryId) }}
          </template>
        </el-table-column>

        <el-table-column label="价格" width="130">
          <template #default="{ row }">
            {{ row.priceText }}
          </template>
        </el-table-column>

        <el-table-column label="SKU 数量" width="110" align="center">
          <template #default="{ row }">
            <el-tooltip
              content="列表接口未返回 SKU 数量，页面通过商品详情接口逐条补全"
              placement="top"
            >
              <span>{{ skuCountLabel(row.id) }}</span>
            </el-tooltip>
          </template>
        </el-table-column>

        <el-table-column label="库存" width="90" align="center">
          <template #default>
            <el-tooltip
              content="后端无库存列表接口，请在商品详情中查看每个 SKU 的实时库存"
              placement="top"
            >
              <span>—</span>
            </el-tooltip>
          </template>
        </el-table-column>

        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <el-tag :type="productStatusTagType(row.status)" size="small">
              {{ row.statusLabel }}
            </el-tag>
          </template>
        </el-table-column>

        <el-table-column label="更新时间" min-width="170">
          <template #default="{ row }">
            {{ formatServerDateTime(row.updatedAt) }}
          </template>
        </el-table-column>

        <el-table-column label="操作" width="220" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openDetail(row.id)">
              详情
            </el-button>
            <el-button
              v-if="canEdit"
              link
              type="primary"
              @click="goEdit(row.id)"
            >
              编辑
            </el-button>
            <el-button
              v-if="row.status === 'on_shelf' && canOffShelf"
              link
              type="warning"
              @click="toggleShelf(row)"
            >
              下架
            </el-button>
            <el-button
              v-else-if="row.status !== 'on_shelf' && canOnShelf"
              link
              type="success"
              @click="toggleShelf(row)"
            >
              上架
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

    <ProductDetailDrawer
      v-model="detailVisible"
      :product-id="detailId"
      @edit="goEdit"
      @changed="load"
    />
  </div>
</template>

<style lang="scss" scoped>
.product-list {
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

.filter-form {
  margin-bottom: 4px;
}

.filter-keyword {
  width: 200px;
}

.filter-category {
  width: 200px;
}

.filter-status {
  width: 140px;
}

.filter-notice {
  margin-bottom: 12px;
}

.product-cell {
  display: flex;
  gap: 10px;
  align-items: center;
}

.product-thumb {
  flex-shrink: 0;
  width: 48px;
  height: 48px;
  overflow: hidden;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 4px;
}

.product-thumb--empty {
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  color: var(--el-text-color-placeholder);
  background: var(--el-fill-color-lighter);
}

.product-info {
  min-width: 0;
}

.product-name {
  overflow: hidden;
  font-size: 13px;
  font-weight: 500;
  color: var(--el-text-color-primary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.product-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 2px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.pagination-bar {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}
</style>
