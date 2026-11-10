<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { ElMessageBox } from "element-plus";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { listProducts, getProduct } from "@/api/modules/product";
import type { ProductDTO } from "@/api/types/product";
import {
  addPositionItem,
  deletePosition,
  getPosition,
  listPositions,
  removePositionItem,
  updatePosition,
  updatePositionItemSort
} from "@/api/modules/recommendation";
import {
  buildSortPayload,
  moveItem,
  toPositionView,
  toRecommendItemView,
  type PositionView,
  type RecommendItemView
} from "@/api/adapters/recommendation-adapter";
import PositionFormDialog from "./components/PositionFormDialog.vue";

/**
 * 商品推荐位管理。
 *
 * 数据模型为「推荐位 → 推荐商品关系」：
 * - 推荐位列表/详情来自真实接口；
 * - 关系里只有 product_id + sort，商品名/图片/价格复用 A2 商品详情补全；
 * - 排序接口是全量覆盖语义，上下移动后提交完整 product_ids。
 */
defineOptions({ name: "RecommendationManagement" });

const canCreate = computed(() => hasPerms(PERMISSION.RECOMMEND_CREATE));
const canUpdate = computed(() => hasPerms(PERMISSION.RECOMMEND_UPDATE));
const canDelete = computed(() => hasPerms(PERMISSION.RECOMMEND_DELETE));
const canManageItem = computed(() => hasPerms(PERMISSION.RECOMMEND_ITEM));

const positions = ref<PositionView[]>([]);
const loading = ref(false);
const error = ref("");

const selectedId = ref<number | null>(null);
const selectedName = ref("");
const items = ref<RecommendItemView[]>([]);
const itemsLoading = ref(false);
const itemsError = ref("");

const { run: runPosition } = useSubmitGuard();
const { run: runItem } = useSubmitGuard();

const dialogVisible = ref(false);
const dialogMode = ref<"create" | "edit">("create");
const editing = ref<PositionView | null>(null);

/** 添加商品选择器 */
const productKeyword = ref("");
const productOptions = ref<ProductDTO[]>([]);
const productLoading = ref(false);
const selectedProductId = ref<number | null>(null);

async function loadPositions() {
  loading.value = true;
  error.value = "";
  try {
    const result = await listPositions();
    positions.value = (result.items ?? []).map(toPositionView);
  } catch (cause) {
    positions.value = [];
    error.value = resolveErrorMessage(cause, "推荐位列表加载失败");
  } finally {
    loading.value = false;
  }
}

async function loadItems(positionId: number) {
  itemsLoading.value = true;
  itemsError.value = "";
  try {
    const detail = await getPosition(positionId);
    selectedId.value = detail.id;
    selectedName.value = detail.name;

    const products = new Map<number, ProductDTO>();
    const uniqueIds = [...new Set(detail.items.map(item => item.product_id))];
    const queue = [...uniqueIds];

    const workers = Array.from(
      { length: Math.min(4, queue.length) },
      async () => {
        for (;;) {
          const id = queue.shift();
          if (!id) return;
          try {
            products.set(id, await getProduct(id));
          } catch {
            // 商品不存在/已删除时只展示 product_id
          }
        }
      }
    );
    await Promise.all(workers);

    items.value = detail.items.map(item =>
      toRecommendItemView(item, products.get(item.product_id) ?? null)
    );
  } catch (cause) {
    items.value = [];
    itemsError.value = resolveErrorMessage(cause, "推荐商品加载失败");
  } finally {
    itemsLoading.value = false;
  }
}

function selectPosition(row: PositionView) {
  if (selectedId.value === row.id) return;
  void loadItems(row.id);
}

function openCreate() {
  dialogMode.value = "create";
  editing.value = null;
  dialogVisible.value = true;
}

function openEdit(row: PositionView) {
  dialogMode.value = "edit";
  editing.value = row;
  dialogVisible.value = true;
}

async function toggleStatus(row: PositionView) {
  const target = row.status === 1 ? 0 : 1;
  const action = target === 1 ? "启用" : "禁用";

  try {
    await ElMessageBox.confirm(
      `确认${action}推荐位「${row.name}」？${action === "禁用" ? "前台接口将不再返回该位商品。" : ""}`,
      `${action}推荐位`,
      { type: "warning" }
    );
  } catch {
    return;
  }

  await runPosition(async () => {
    try {
      await updatePosition(row.id, { status: target as 0 | 1 });
      message(`${action}成功`, { type: "success" });
      await loadPositions();
      if (selectedId.value === row.id) await loadItems(row.id);
    } catch (cause) {
      message(resolveErrorMessage(cause, `${action}失败`), { type: "error" });
    }
  });
}

async function handleDelete(row: PositionView) {
  try {
    await ElMessageBox.confirm(
      `确认删除推荐位「${row.name}」？后端会级联删除该位下的全部推荐商品关系。`,
      "删除推荐位",
      { type: "warning", confirmButtonText: "删除", cancelButtonText: "取消" }
    );
  } catch {
    return;
  }

  await runPosition(async () => {
    try {
      await deletePosition(row.id);
      message("推荐位已删除", { type: "success" });
      if (selectedId.value === row.id) {
        selectedId.value = null;
        selectedName.value = "";
        items.value = [];
      }
      await loadPositions();
    } catch (cause) {
      message(resolveErrorMessage(cause, "删除失败"), { type: "error" });
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
    message(resolveErrorMessage(cause, "商品搜索失败"), { type: "error" });
  } finally {
    productLoading.value = false;
  }
}

async function addProduct() {
  if (!selectedId.value) {
    message("请先选择推荐位", { type: "warning" });
    return;
  }
  if (!selectedProductId.value) {
    message("请选择要添加的商品", { type: "warning" });
    return;
  }

  const positionId = selectedId.value;
  await runItem(async () => {
    try {
      await addPositionItem(positionId, {
        product_id: selectedProductId.value!,
        sort: items.value.length
      });
      message("商品已加入推荐位", { type: "success" });
      selectedProductId.value = null;
      await loadItems(positionId);
    } catch (cause) {
      message(resolveErrorMessage(cause, "添加商品失败"), { type: "error" });
    }
  });
}

async function move(index: number, direction: -1 | 1) {
  if (!selectedId.value) return;

  const next = moveItem(items.value, index, direction);
  if (
    next.every((item, idx) => item.productId === items.value[idx].productId)
  ) {
    return;
  }

  const positionId = selectedId.value;
  await runItem(async () => {
    try {
      await updatePositionItemSort(positionId, buildSortPayload(next));
      message("排序已更新", { type: "success" });
      await loadItems(positionId);
    } catch (cause) {
      message(resolveErrorMessage(cause, "排序失败"), { type: "error" });
    }
  });
}

async function removeProduct(row: RecommendItemView) {
  if (!selectedId.value) return;

  try {
    await ElMessageBox.confirm(
      `确认从推荐位移除商品「${row.productName}」？`,
      "移除推荐商品",
      { type: "warning", confirmButtonText: "移除", cancelButtonText: "取消" }
    );
  } catch {
    return;
  }

  const positionId = selectedId.value;
  await runItem(async () => {
    try {
      await removePositionItem(positionId, row.productId);
      message("已移除", { type: "success" });
      await loadItems(positionId);
    } catch (cause) {
      message(resolveErrorMessage(cause, "移除失败"), { type: "error" });
    }
  });
}

function handleSaved() {
  void loadPositions();
  if (dialogMode.value === "edit" && editing.value) {
    void loadItems(editing.value.id);
  }
}

const canAddProduct = computed(
  () => canManageItem.value && selectedId.value !== null
);

onMounted(async () => {
  await Promise.all([loadPositions(), searchProducts()]);
  if (positions.value.length > 0) {
    await loadItems(positions.value[0].id);
  }
});
</script>

<template>
  <div class="recommend-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">商品推荐位</h2>
        <p class="page-desc">
          推荐位 → 推荐商品关系；商品信息复用商品管理接口补全
        </p>
      </div>
      <div class="page-actions">
        <el-button :loading="loading" @click="loadPositions">刷新</el-button>
        <el-button v-if="canCreate" type="primary" @click="openCreate">
          新增推荐位
        </el-button>
      </div>
    </div>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="数据模型说明"
      description="推荐位有 code（创建后不可变）；推荐商品关系只存 product_id 与 sort，商品名/图片/价格由商品详情接口补全。排序接口要求提交该位全部商品的有序列表。"
    />

    <el-card shadow="never">
      <el-alert
        v-if="error"
        class="block-alert"
        type="error"
        :closable="false"
        show-icon
        :title="error"
      >
        <template #default>
          <el-button link type="primary" @click="loadPositions">重试</el-button>
        </template>
      </el-alert>

      <el-table
        v-loading="loading"
        :data="positions"
        row-key="id"
        border
        highlight-current-row
        @row-click="selectPosition"
      >
        <el-table-column prop="id" label="ID" width="80" />
        <el-table-column prop="code" label="code" min-width="140" />
        <el-table-column prop="name" label="名称" min-width="160" />
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="row.statusTagType" size="small">
              {{ row.statusLabel }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="更新时间" min-width="170">
          <template #default="{ row }">{{ row.updatedText }}</template>
        </el-table-column>
        <el-table-column label="操作" width="260" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click.stop="selectPosition(row)">
              查看商品
            </el-button>
            <el-button
              v-if="canUpdate"
              link
              type="primary"
              @click.stop="openEdit(row)"
            >
              编辑
            </el-button>
            <el-button
              v-if="canUpdate"
              link
              :type="row.status === 1 ? 'warning' : 'success'"
              @click.stop="toggleStatus(row)"
            >
              {{ row.status === 1 ? "禁用" : "启用" }}
            </el-button>
            <el-button
              v-if="canDelete"
              link
              type="danger"
              @click.stop="handleDelete(row)"
            >
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-card shadow="never">
      <template #header>
        <div class="card-header">
          <span class="card-title">
            推荐商品
            <span v-if="selectedId" class="card-sub">
              · {{ selectedName }}（ID {{ selectedId }}）
            </span>
          </span>
        </div>
      </template>

      <el-empty v-if="!selectedId" description="请选择上方推荐位查看推荐商品" />

      <template v-else>
        <el-form :inline="true" class="add-form" @submit.prevent>
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
              搜索
            </el-button>
          </el-form-item>
          <el-form-item label="商品">
            <el-select
              v-model="selectedProductId"
              placeholder="选择商品"
              filterable
              class="filter-product"
              :disabled="!canAddProduct"
            >
              <el-option
                v-for="product in productOptions"
                :key="product.id"
                :label="`${product.name}（ID ${product.id}）`"
                :value="product.id"
              />
            </el-select>
          </el-form-item>
          <el-form-item>
            <el-button
              type="primary"
              plain
              :disabled="!canAddProduct"
              @click="addProduct"
            >
              加入推荐位
            </el-button>
          </el-form-item>
        </el-form>

        <el-alert
          v-if="itemsError"
          class="block-alert"
          type="error"
          :closable="false"
          show-icon
          :title="itemsError"
        />

        <el-table v-loading="itemsLoading" :data="items" border size="small">
          <el-table-column label="商品" min-width="260">
            <template #default="{ row }">
              <div class="item-cell">
                <el-image
                  v-if="row.productImage"
                  :src="row.productImage"
                  fit="cover"
                  class="item-image"
                  :preview-src-list="[row.productImage]"
                  preview-teleported
                />
                <div v-else class="item-image item-image--empty">无图</div>
                <div>
                  <div class="item-name">{{ row.productName }}</div>
                  <div class="item-meta">
                    商品 ID {{ row.productId }}
                    <el-tag :type="row.productStatusTagType" size="small">
                      {{ row.productStatusLabel }}
                    </el-tag>
                  </div>
                </div>
              </div>
            </template>
          </el-table-column>
          <el-table-column
            prop="productPriceText"
            label="价格"
            width="130"
            align="right"
          />
          <el-table-column prop="sort" label="排序" width="80" align="center" />
          <el-table-column
            v-if="canManageItem"
            label="操作"
            width="220"
            align="center"
          >
            <template #default="{ row, $index }">
              <el-button
                link
                type="primary"
                :disabled="$index === 0"
                @click="move($index, -1)"
              >
                上移
              </el-button>
              <el-button
                link
                type="primary"
                :disabled="$index === items.length - 1"
                @click="move($index, 1)"
              >
                下移
              </el-button>
              <el-button link type="danger" @click="removeProduct(row)">
                移除
              </el-button>
            </template>
          </el-table-column>
        </el-table>

        <p class="table-note">
          商品状态来自商品表实时查询；推荐关系本身没有状态字段。
        </p>
      </template>
    </el-card>

    <PositionFormDialog
      v-model="dialogVisible"
      :mode="dialogMode"
      :position="editing"
      @saved="handleSaved"
    />
  </div>
</template>

<style lang="scss" scoped>
.recommend-page {
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

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.card-title {
  font-weight: 600;
}

.card-sub {
  font-size: 12px;
  font-weight: 400;
  color: var(--el-text-color-secondary);
}

.add-form {
  margin-bottom: 4px;
}

.filter-keyword {
  width: 190px;
}

.filter-product {
  width: 220px;
}

.block-alert {
  margin-bottom: 12px;
}

.item-cell {
  display: flex;
  gap: 10px;
  align-items: center;
}

.item-image {
  width: 44px;
  height: 44px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 4px;
}

.item-image--empty {
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  color: var(--el-text-color-placeholder);
  background: var(--el-fill-color-lighter);
}

.item-name {
  font-size: 13px;
  font-weight: 500;
}

.item-meta {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-top: 2px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.table-note {
  margin: 10px 0 0;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
</style>
