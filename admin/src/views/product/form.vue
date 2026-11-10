<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { ElMessageBox } from "element-plus";
import { hasPerms } from "@/utils/auth";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { PERMISSION } from "@/utils/permission";
import { useSubmitGuard } from "@/composables/useSubmitGuard";
import { listCategories } from "@/api/modules/category";
import {
  createProduct,
  getProduct,
  offShelfProduct,
  onShelfProduct,
  updateProduct
} from "@/api/modules/product";
import {
  syncProductSkus,
  type SkuSyncFailure,
  type SkuSyncPermissions
} from "@/api/modules/sku";
import type { CategoryDTO } from "@/api/types/category";
import type { ProductStatus } from "@/api/types/product";
import type { SkuDTO } from "@/api/types/sku";
import {
  buildCreatePayload,
  buildUpdatePayload,
  emptyProductForm,
  formatProductStatus,
  hasFormErrors,
  productStatusTagType,
  toProductForm,
  validateProductForm,
  type ProductBasicForm,
  type ProductFormErrors
} from "@/api/adapters/product-adapter";
import {
  buildSkuEditorState,
  createDefaultSkuDraft,
  validateSkuDrafts,
  type SkuDraft,
  type SpecGroupDraft
} from "@/api/adapters/sku-adapter";
import SkuSpecEditor from "./components/SkuSpecEditor.vue";
import ProductImageUploader from "./components/ProductImageUploader.vue";

/**
 * 商品创建 / 编辑页。
 *
 * 保存顺序刻意拆成两步：
 * 1. 保存商品基本信息（真实 `POST/PUT /admin/products`）；
 * 2. 同步 SKU（真实 `POST/PUT/DELETE /admin/skus`）。
 *
 * 为什么不能合并：后端创建商品时没有 SKU 字段，SKU 必须拿到 product_id 后单独创建。
 * 若第 1 步成功、第 2 步部分失败，页面会保留并展示失败明细，不会谎报"全部成功"。
 */
defineOptions({ name: "ProductFormPage" });

const route = useRoute();
const router = useRouter();

const mode = computed<"create" | "edit">(() =>
  route.name === "ProductCreate" ? "create" : "edit"
);
const productId = computed(() => Number(route.params.id));

const canCreateProduct = computed(() => hasPerms(PERMISSION.PRODUCT_CREATE));
const canUpdateProduct = computed(() => hasPerms(PERMISSION.PRODUCT_UPDATE));
const canOnShelf = computed(() => hasPerms(PERMISSION.PRODUCT_ON_SHELF));
const canOffShelf = computed(() => hasPerms(PERMISSION.PRODUCT_OFF_SHELF));

const skuPermissions: SkuSyncPermissions = {
  create: hasPerms(PERMISSION.SKU_CREATE),
  update: hasPerms(PERMISSION.SKU_UPDATE),
  remove: hasPerms(PERMISSION.SKU_DELETE)
};

const canEditSku = computed(
  () => skuPermissions.create || skuPermissions.update || skuPermissions.remove
);

/** 当前身份是否允许进入本页（新建看 create，编辑看 update） */
const allowed = computed(() =>
  mode.value === "create" ? canCreateProduct.value : canUpdateProduct.value
);

const form = reactive<ProductBasicForm>(emptyProductForm());
const formErrors = ref<ProductFormErrors>({});
const groups = ref<SpecGroupDraft[]>([]);
const drafts = ref<SkuDraft[]>([]);
const existingSkus = ref<SkuDTO[]>([]);
const categoryTree = ref<CategoryDTO[]>([]);
const productStatus = ref<ProductStatus | null>(null);

const loading = ref(false);
const loadError = ref("");
const syncFailures = ref<SkuSyncFailure[]>([]);

const { submitting, run } = useSubmitGuard();

const pageTitle = computed(() =>
  mode.value === "create" ? "新建商品" : "编辑商品"
);

const canSubmit = computed(() =>
  mode.value === "create" ? canCreateProduct.value : canUpdateProduct.value
);

const priceCents = computed(() => {
  if (form.priceYuan === null) return 0;
  return Math.round(form.priceYuan * 100);
});

const canToggleShelf = computed(() => {
  if (mode.value !== "edit" || !productStatus.value) return false;
  return productStatus.value === "on_shelf"
    ? canOffShelf.value
    : canOnShelf.value;
});

const shelfActionLabel = computed(() =>
  productStatus.value === "on_shelf" ? "下架" : "上架"
);

/** 分类树：非叶子节点（后端只接受叶子分类）在 UI 上禁用 */
const categorySelectData = computed(() => {
  const decorate = (
    nodes: CategoryDTO[]
  ): Array<CategoryDTO & { disabled: boolean }> =>
    nodes.map(node => ({
      ...node,
      disabled: Boolean(node.children?.length),
      children: node.children ? decorate(node.children) : undefined
    }));
  return decorate(categoryTree.value);
});

const mainImageList = computed<string[]>({
  get: () => (form.mainImage ? [form.mainImage] : []),
  set: value => {
    form.mainImage = value[0] ?? "";
  }
});

/** el-input-number 不接收 null，这里做一次显式转换 */
const priceYuanModel = computed<number | undefined>({
  get: () => form.priceYuan ?? undefined,
  set: value => {
    form.priceYuan = value ?? null;
  }
});

const priceDisplay = computed(() =>
  form.priceYuan === null ? "—" : `¥${form.priceYuan.toFixed(2)}`
);

async function load() {
  loading.value = true;
  loadError.value = "";

  try {
    const categoryResult = await listCategories();
    categoryTree.value = categoryResult.items ?? [];

    if (mode.value === "create") {
      drafts.value = [createDefaultSkuDraft(priceCents.value)];
      return;
    }

    const detail = await getProduct(productId.value);
    Object.assign(form, toProductForm(detail));
    productStatus.value = detail.status;
    existingSkus.value = detail.skus ?? [];

    const state = buildSkuEditorState(existingSkus.value, detail.price);
    groups.value = state.groups;
    drafts.value = state.drafts;
  } catch (cause) {
    loadError.value = resolveErrorMessage(cause, "商品加载失败");
  } finally {
    loading.value = false;
  }
}

/** 上架 / 下架（真实状态迁移接口） */
async function toggleShelf() {
  if (!productStatus.value || !productId.value) return;

  try {
    await ElMessageBox.confirm(
      `确认${shelfActionLabel.value}商品「${form.name}」？该操作会调用后端状态迁移接口。`,
      `${shelfActionLabel.value}商品`,
      { type: "warning" }
    );
  } catch {
    return;
  }

  await run(async () => {
    try {
      const updated =
        productStatus.value === "on_shelf"
          ? await offShelfProduct(productId.value)
          : await onShelfProduct(productId.value);
      productStatus.value = updated.status;
      message(`${shelfActionLabel.value}成功`, { type: "success" });
    } catch (cause) {
      message(resolveErrorMessage(cause, `${shelfActionLabel.value}失败`), {
        type: "error"
      });
    }
  });
}

async function handleSave() {
  syncFailures.value = [];
  formErrors.value = validateProductForm(form);

  if (hasFormErrors(formErrors.value)) {
    message("请检查商品基本信息", { type: "warning" });
    return;
  }

  if (canEditSku.value) {
    const skuErrors = validateSkuDrafts(drafts.value);
    if (skuErrors.length > 0) {
      message(skuErrors[0], { type: "error" });
      return;
    }
  }

  const result = await run(async () => {
    let savedId = productId.value;

    try {
      if (mode.value === "create") {
        const created = await createProduct(buildCreatePayload(form));
        savedId = created.id;
      } else {
        await updateProduct(productId.value, buildUpdatePayload(form));
      }
    } catch (cause) {
      message(resolveErrorMessage(cause, "商品保存失败"), { type: "error" });
      return;
    }

    if (!canEditSku.value) {
      message("商品已保存（当前身份无 SKU 写权限，未同步 SKU）", {
        type: "warning"
      });
      await router.push("/products");
      return;
    }

    const sync = await syncProductSkus(
      savedId,
      drafts.value,
      existingSkus.value,
      skuPermissions
    );

    if (sync.failures.length > 0) {
      syncFailures.value = sync.failures;
      message(
        `商品已保存，但有 ${sync.failures.length} 个 SKU 操作失败，请查看页面提示`,
        { type: "error" }
      );

      if (mode.value === "create") {
        // 商品已创建：切成编辑模式，避免用户重复点击"保存"又建一个商品
        await router.replace(`/products/${savedId}/edit`);
      }
      await refreshExistingSkus(savedId);
      return;
    }

    message(
      `保存成功（新增 ${sync.created} / 更新 ${sync.updated} / 删除 ${sync.removed} 个 SKU）`,
      { type: "success" }
    );
    await router.push("/products");
  });

  if (result.executed === false) {
    message("正在保存，请勿重复提交", { type: "warning" });
  }
}

/**
 * 部分 SKU 失败后刷新"后端已有 SKU"快照。
 *
 * 只更新 existingSkus / status：**不重建草稿行**，
 * 这样用户已填写的失败行仍然可见，可直接重试；
 * 已成功的行则不会再被重复创建。
 */
async function refreshExistingSkus(id: number) {
  try {
    const detail = await getProduct(id);
    existingSkus.value = detail.skus ?? [];
    productStatus.value = detail.status;
  } catch {
    // 刷新失败时保留页面现状，用户可手动刷新
  }
}

function goBack() {
  router.push("/products");
}

onMounted(load);
</script>

<template>
  <div class="product-form-page">
    <div class="page-header">
      <div>
        <h2 class="page-title">{{ pageTitle }}</h2>
        <p class="page-desc">
          商品基本信息与 SKU 分开保存；库存数量请前往「库存管理」调整
        </p>
      </div>
      <div class="page-actions">
        <el-button @click="goBack">返回列表</el-button>
        <el-button
          v-if="mode === 'edit'"
          :disabled="!canToggleShelf || submitting"
          @click="toggleShelf"
        >
          {{ shelfActionLabel }}
        </el-button>
        <el-button
          v-if="canSubmit"
          type="primary"
          :loading="submitting"
          @click="handleSave"
        >
          保存
        </el-button>
      </div>
    </div>

    <el-alert
      v-if="!allowed"
      type="warning"
      :closable="false"
      show-icon
      title="当前管理员没有该操作权限"
      description="页面入口已按后端权限码隐藏，如需操作请联系超级管理员。"
    />

    <el-alert
      v-else-if="loadError"
      type="error"
      :closable="false"
      show-icon
      :title="loadError"
    >
      <template #default>
        <el-button link type="primary" @click="load">重试</el-button>
      </template>
    </el-alert>

    <el-skeleton v-if="loading" :rows="8" animated />

    <template v-else-if="allowed && !loadError">
      <el-alert
        v-if="syncFailures.length > 0"
        type="error"
        :closable="false"
        show-icon
        title="部分 SKU 同步失败"
      >
        <ul class="failure-list">
          <li
            v-for="failure in syncFailures"
            :key="`${failure.action}-${failure.name}`"
          >
            {{
              failure.action === "create"
                ? "创建"
                : failure.action === "update"
                  ? "更新"
                  : "删除"
            }}
            「{{ failure.name }}」失败：{{ failure.message }}
          </li>
        </ul>
      </el-alert>

      <el-card shadow="never">
        <template #header>
          <div class="card-header">
            <span>基本信息</span>
            <span v-if="mode === 'edit'" class="card-sub">
              状态：
              <el-tag
                :type="productStatusTagType(productStatus ?? 'draft')"
                size="small"
              >
                {{ formatProductStatus(productStatus ?? "draft") }}
              </el-tag>
              <span class="card-sub__hint"
                >（状态只能通过上架 / 下架接口变更）</span
              >
            </span>
          </div>
        </template>

        <el-form label-width="110px" label-position="right">
          <el-row :gutter="24">
            <el-col :span="12">
              <el-form-item label="商品名称" required :error="formErrors.name">
                <el-input
                  v-model="form.name"
                  maxlength="128"
                  show-word-limit
                  placeholder="请输入商品名称"
                />
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="品牌" :error="formErrors.brand">
                <el-input
                  v-model="form.brand"
                  maxlength="64"
                  placeholder="可选"
                />
              </el-form-item>
            </el-col>
          </el-row>

          <el-row :gutter="24">
            <el-col :span="12">
              <el-form-item
                label="商品分类"
                required
                :error="formErrors.categoryId"
              >
                <el-tree-select
                  v-model="form.categoryId"
                  :data="categorySelectData"
                  :props="{ label: 'name', children: 'children' }"
                  node-key="id"
                  check-strictly
                  :render-after-expand="false"
                  default-expand-all
                  placeholder="请选择叶子分类"
                  class="full-width"
                />
                <div class="field-hint">
                  后端只接受「启用中的叶子分类」；父级分类不可选。
                </div>
              </el-form-item>
            </el-col>
            <el-col :span="12">
              <el-form-item label="商品价格" required :error="formErrors.price">
                <el-input-number
                  v-model="priceYuanModel"
                  :min="0"
                  :max="999999.99"
                  :precision="2"
                  :step="1"
                  controls-position="right"
                  placeholder="单位：元"
                  class="full-width"
                />
                <div class="field-hint">
                  提交时转换为整数分；当前展示 {{ priceDisplay }}
                </div>
              </el-form-item>
            </el-col>
          </el-row>

          <el-form-item label="商品主图" :error="formErrors.mainImage">
            <ProductImageUploader
              v-model="mainImageList"
              :max="1"
              tip="建议 800×800，支持 jpg / png / gif / webp，单张不超过 10MB"
            />
          </el-form-item>

          <el-form-item label="商品轮播图" :error="formErrors.images">
            <ProductImageUploader
              v-model="form.images"
              :max="8"
              tip="最多 8 张，保存时按当前顺序全量提交"
            />
          </el-form-item>

          <el-form-item label="商品详情">
            <el-input
              v-model="form.detail"
              type="textarea"
              :rows="5"
              maxlength="5000"
              show-word-limit
              placeholder="商品描述 / 详情（纯文本，后端 detail 字段）"
            />
          </el-form-item>
        </el-form>
      </el-card>

      <el-card shadow="never">
        <template #header>
          <div class="card-header">
            <span>SKU 与商品规格</span>
            <span class="card-sub">
              后端 SKU 为平铺模型；规格组合由前端生成后写入 SKU 名称
            </span>
          </div>
        </template>

        <SkuSpecEditor
          v-model:groups="groups"
          v-model:drafts="drafts"
          :default-price-cents="priceCents"
          :existing-skus="existingSkus"
          :disabled="!canEditSku || submitting"
        />

        <el-alert
          v-if="!canEditSku"
          class="sku-permission-tip"
          type="warning"
          :closable="false"
          show-icon
          title="当前管理员没有 SKU 写权限，规格与 SKU 仅可查看"
        />
      </el-card>

      <div class="bottom-actions">
        <el-button @click="goBack">取消</el-button>
        <el-button
          v-if="canSubmit"
          type="primary"
          :loading="submitting"
          @click="handleSave"
        >
          保存
        </el-button>
      </div>
    </template>
  </div>
</template>

<style lang="scss" scoped>
.product-form-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
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
  flex-shrink: 0;
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

.card-sub__hint {
  margin-left: 6px;
}

.full-width {
  width: 100%;
}

.field-hint {
  margin-top: 4px;
  font-size: 12px;
  line-height: 1.4;
  color: var(--el-text-color-secondary);
}

.sku-permission-tip {
  margin-top: 12px;
}

.failure-list {
  padding-left: 18px;
  margin: 4px 0 0;
  font-size: 13px;
}

.bottom-actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  padding-bottom: 8px;
}
</style>
