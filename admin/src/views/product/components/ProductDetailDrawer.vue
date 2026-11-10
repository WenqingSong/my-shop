<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { ElMessageBox } from "element-plus";
import {
  getProduct,
  offShelfProduct,
  onShelfProduct
} from "@/api/modules/product";
import { getInventory } from "@/api/modules/inventory";
import type { ProductDetailDTO } from "@/api/types/product";
import { formatCentsAsCny } from "@/utils/money";
import { formatServerDateTime } from "@/utils/datetime";
import { resolveErrorMessage } from "@/utils/error-message";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { PERMISSION } from "@/utils/permission";
import { formatSkuStatus } from "@/api/adapters/sku-adapter";
import {
  formatProductStatus,
  productStatusTagType
} from "@/api/adapters/product-adapter";
import { useSubmitGuard } from "@/composables/useSubmitGuard";

/**
 * 商品详情抽屉。
 *
 * 后端没有"商品 + SKU + 库存"的聚合接口，这里用真实接口组合：
 * 详情（含 SKU）→ 逐 SKU 查询库存。任何一个库存查询失败只显示"—"。
 */
const props = defineProps<{
  modelValue: boolean;
  productId: number | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [visible: boolean];
  edit: [id: number];
  changed: [];
}>();

const detail = ref<ProductDetailDTO | null>(null);
const loading = ref(false);
const error = ref("");
/** SKU 库存：`null` 加载中，`-1` 未知 */
const inventories = reactive<Record<number, number | null>>({});

const { submitting, run } = useSubmitGuard();

const canEdit = computed(() => hasPerms(PERMISSION.PRODUCT_UPDATE));
const canOnShelf = computed(() => hasPerms(PERMISSION.PRODUCT_ON_SHELF));
const canOffShelf = computed(() => hasPerms(PERMISSION.PRODUCT_OFF_SHELF));

const canToggleShelf = computed(() => {
  if (!detail.value) return false;
  return detail.value.status === "on_shelf"
    ? canOffShelf.value
    : canOnShelf.value;
});

const shelfLabel = computed(() =>
  detail.value?.status === "on_shelf" ? "下架" : "上架"
);

const previewImages = computed(() => {
  const images = detail.value?.images ?? [];
  const main = detail.value?.main_image;
  return [main, ...images].filter(Boolean) as string[];
});

const visible = computed({
  get: () => props.modelValue,
  set: value => emit("update:modelValue", value)
});

async function loadInventories(skuIds: number[]) {
  for (const id of skuIds) inventories[id] = null;

  const queue = [...skuIds];
  const workers = Array.from(
    { length: Math.min(4, queue.length) },
    async () => {
      for (;;) {
        const next = queue.shift();
        if (!next) return;
        try {
          const inventory = await getInventory(next);
          inventories[next] = inventory.quantity;
        } catch {
          inventories[next] = -1;
        }
      }
    }
  );

  await Promise.all(workers);
}

async function load() {
  if (!props.productId) return;
  loading.value = true;
  error.value = "";

  try {
    const result = await getProduct(props.productId);
    detail.value = result;
    void loadInventories((result.skus ?? []).map(sku => sku.id));
  } catch (cause) {
    detail.value = null;
    error.value = resolveErrorMessage(cause, "商品详情加载失败");
  } finally {
    loading.value = false;
  }
}

watch(
  () => [props.modelValue, props.productId] as const,
  ([isOpen, id]) => {
    if (isOpen && id) void load();
  },
  { immediate: true }
);

async function toggleShelf() {
  const current = detail.value;
  if (!current) return;

  try {
    await ElMessageBox.confirm(
      `确认${shelfLabel.value}商品「${current.name}」？`,
      `${shelfLabel.value}商品`,
      { type: "warning" }
    );
  } catch {
    return;
  }

  await run(async () => {
    try {
      const updated =
        current.status === "on_shelf"
          ? await offShelfProduct(current.id)
          : await onShelfProduct(current.id);
      current.status = updated.status;
      message(`${shelfLabel.value}成功`, { type: "success" });
      emit("changed");
    } catch (cause) {
      message(resolveErrorMessage(cause, `${shelfLabel.value}失败`), {
        type: "error"
      });
    }
  });
}

function handleEdit() {
  if (detail.value) emit("edit", detail.value.id);
}

function inventoryText(skuId: number) {
  const value = inventories[skuId];
  if (value === null || value === undefined) return "…";
  if (value < 0) return "—";
  return String(value);
}
</script>

<template>
  <el-drawer v-model="visible" title="商品详情" size="62%" destroy-on-close>
    <el-skeleton v-if="loading" :rows="8" animated />

    <el-alert
      v-else-if="error"
      type="error"
      :closable="false"
      show-icon
      :title="error"
    >
      <template #default>
        <el-button link type="primary" @click="load">重试</el-button>
      </template>
    </el-alert>

    <template v-else-if="detail">
      <div class="detail-header">
        <div class="detail-title">
          <el-image
            v-if="detail.main_image"
            :src="detail.main_image"
            fit="cover"
            class="detail-cover"
            :preview-src-list="previewImages"
            preview-teleported
          />
          <div class="detail-title__text">
            <h3>{{ detail.name }}</h3>
            <div class="detail-title__meta">
              <el-tag :type="productStatusTagType(detail.status)" size="small">
                {{ formatProductStatus(detail.status) }}
              </el-tag>
              <span>商品 ID：{{ detail.id }}</span>
              <span v-if="detail.brand">品牌：{{ detail.brand }}</span>
            </div>
          </div>
        </div>

        <div class="detail-actions">
          <el-button v-if="canEdit" @click="handleEdit">编辑商品</el-button>
          <el-button
            v-if="canToggleShelf"
            :loading="submitting"
            @click="toggleShelf"
          >
            {{ shelfLabel }}
          </el-button>
        </div>
      </div>

      <el-descriptions :column="3" border class="detail-descriptions">
        <el-descriptions-item label="价格">
          {{ formatCentsAsCny(detail.price) }}
        </el-descriptions-item>
        <el-descriptions-item label="分类 ID">
          {{ detail.category_id }}
        </el-descriptions-item>
        <el-descriptions-item label="浏览量">
          {{ detail.view_count }}
        </el-descriptions-item>
        <el-descriptions-item label="创建时间">
          {{ formatServerDateTime(detail.created_at) }}
        </el-descriptions-item>
        <el-descriptions-item label="更新时间">
          {{ formatServerDateTime(detail.updated_at) }}
        </el-descriptions-item>
        <el-descriptions-item label="SKU 数量">
          {{ detail.skus?.length ?? 0 }}
        </el-descriptions-item>
      </el-descriptions>

      <div v-if="previewImages.length > 1" class="detail-gallery">
        <el-image
          v-for="url in previewImages"
          :key="url"
          :src="url"
          fit="cover"
          class="detail-gallery__item"
          :preview-src-list="previewImages"
          preview-teleported
        />
      </div>

      <h4 class="detail-section-title">SKU 列表</h4>
      <el-table :data="detail.skus ?? []" border size="small">
        <el-table-column prop="id" label="SKU ID" width="100" />
        <el-table-column prop="name" label="SKU 名称" min-width="180" />
        <el-table-column label="价格" width="130">
          <template #default="{ row }">
            {{ formatCentsAsCny(row.price) }}
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag
              :type="row.status === 'enabled' ? 'success' : 'info'"
              size="small"
            >
              {{ formatSkuStatus(row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="当前库存" width="110">
          <template #default="{ row }">
            {{ inventoryText(row.id) }}
          </template>
        </el-table-column>
        <el-table-column label="更新时间" min-width="170">
          <template #default="{ row }">
            {{ formatServerDateTime(row.updated_at) }}
          </template>
        </el-table-column>
      </el-table>

      <p class="detail-hint">
        库存为逐 SKU 实时查询结果；后端不提供锁定库存 /
        可售库存字段，页面不做推导。
      </p>

      <h4 class="detail-section-title">商品详情</h4>
      <div class="detail-content">
        {{ detail.detail || "（暂无详情）" }}
      </div>
    </template>
  </el-drawer>
</template>

<style lang="scss" scoped>
.detail-header {
  display: flex;
  gap: 16px;
  align-items: flex-start;
  justify-content: space-between;
  margin-bottom: 16px;
}

.detail-title {
  display: flex;
  gap: 12px;
  align-items: flex-start;
}

.detail-cover {
  width: 96px;
  height: 96px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 6px;
}

.detail-title__text h3 {
  margin: 0 0 8px;
  font-size: 16px;
  font-weight: 600;
}

.detail-title__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  align-items: center;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.detail-actions {
  display: flex;
  gap: 8px;
  flex-shrink: 0;
}

.detail-descriptions {
  margin-bottom: 16px;
}

.detail-gallery {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 16px;
}

.detail-gallery__item {
  width: 84px;
  height: 84px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 4px;
}

.detail-section-title {
  margin: 8px 0 10px;
  font-size: 14px;
  font-weight: 600;
}

.detail-hint {
  margin: 8px 0 16px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.detail-content {
  padding: 12px;
  font-size: 13px;
  line-height: 1.7;
  color: var(--el-text-color-regular);
  white-space: pre-wrap;
  background: var(--el-fill-color-lighter);
  border-radius: 6px;
}
</style>
