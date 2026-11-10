<script setup lang="ts">
import { ref } from "vue";
import { message } from "@/utils/message";
import { resolveErrorMessage } from "@/utils/error-message";
import { parseYuanToCents } from "@/utils/money";
import { listProducts, getProduct } from "@/api/modules/product";
import type { ProductDTO } from "@/api/types/product";
import type { SkuDTO } from "@/api/types/sku";
import {
  createBindingForm,
  FLASH_SALE_PRICE_MAX_CENTS,
  type FlashSaleBindingForm
} from "@/api/adapters/flash-sale-adapter";

/**
 * SKU 绑定选择器。
 *
 * 复用 A2 商品查询：商品列表（关键词）→ 商品详情（SKU 列表）→ 秒杀价/库存。
 * 只构造绑定草稿，不修改任何库存。
 */
const props = withDefaults(defineProps<{ disabled?: boolean }>(), {
  disabled: false
});

const emit = defineEmits<{ add: [binding: FlashSaleBindingForm] }>();

const keyword = ref("");
const products = ref<ProductDTO[]>([]);
const productLoading = ref(false);
const productId = ref<number | null>(null);
const skus = ref<SkuDTO[]>([]);
const skuLoading = ref(false);
const skuId = ref<number | null>(null);
const priceYuan = ref<number | null>(null);
const totalStock = ref<number | null>(null);

async function searchProducts() {
  productLoading.value = true;
  try {
    const result = await listProducts({
      page: 1,
      size: 20,
      keyword: keyword.value.trim() || undefined,
      sort: "id",
      order: "desc"
    });
    products.value = result.items ?? [];
  } catch (cause) {
    products.value = [];
    message(resolveErrorMessage(cause, "商品搜索失败"), { type: "error" });
  } finally {
    productLoading.value = false;
  }
}

async function handleProductChange(id: number) {
  productId.value = id;
  skuId.value = null;
  skus.value = [];
  skuLoading.value = true;
  try {
    const detail = await getProduct(id);
    skus.value = detail.skus ?? [];
  } catch (cause) {
    skus.value = [];
    message(resolveErrorMessage(cause, "SKU 加载失败"), { type: "error" });
  } finally {
    skuLoading.value = false;
  }
}

function addBinding() {
  if (props.disabled) return;

  if (!productId.value || !skuId.value) {
    message("请先选择商品与 SKU", { type: "warning" });
    return;
  }

  const price = parseYuanToCents(priceYuan.value, FLASH_SALE_PRICE_MAX_CENTS);
  if (price === null || price <= 0) {
    message("秒杀价需大于 0 且最多两位小数", { type: "warning" });
    return;
  }

  if (
    !totalStock.value ||
    !Number.isInteger(totalStock.value) ||
    totalStock.value <= 0
  ) {
    message("秒杀库存必须是正整数", { type: "warning" });
    return;
  }

  const product = products.value.find(item => item.id === productId.value);
  const sku = skus.value.find(item => item.id === skuId.value);
  if (!sku) {
    message("请选择有效的 SKU", { type: "warning" });
    return;
  }

  const binding = createBindingForm();
  binding.productId = productId.value;
  binding.productName = product?.name ?? `商品 #${productId.value}`;
  binding.skuId = sku.id;
  binding.skuName = sku.name;
  binding.flashPriceYuan = priceYuan.value;
  binding.totalStock = totalStock.value;

  emit("add", binding);

  // 保留商品选择便于连续添加同一商品的多个 SKU
  skuId.value = null;
  priceYuan.value = null;
  totalStock.value = null;
}
</script>

<template>
  <div class="sku-picker">
    <div class="sku-picker__row">
      <el-input
        v-model="keyword"
        placeholder="商品名称 / 品牌"
        clearable
        class="sku-picker__keyword"
        :disabled="disabled"
        @keyup.enter="searchProducts"
      />
      <el-button
        :loading="productLoading"
        :disabled="disabled"
        @click="searchProducts"
        ><template #loading><BrandLoadingIcon /></template>
        搜索商品
      </el-button>
      <el-select
        :model-value="productId"
        placeholder="选择商品"
        filterable
        class="sku-picker__product"
        :disabled="disabled"
        @update:model-value="handleProductChange"
      >
        <el-option
          v-for="product in products"
          :key="product.id"
          :label="`${product.name}（ID ${product.id}）`"
          :value="product.id"
        />
      </el-select>
    </div>

    <div class="sku-picker__row">
      <el-select
        v-model="skuId"
        placeholder="选择 SKU"
        filterable
        :loading="skuLoading"
        :disabled="disabled || skus.length === 0"
        class="sku-picker__sku"
      >
        <el-option
          v-for="sku in skus"
          :key="sku.id"
          :label="`${sku.name}（ID ${sku.id}）`"
          :value="sku.id"
        />
      </el-select>

      <el-input-number
        v-model="priceYuan"
        :min="0.01"
        :max="999999.99"
        :precision="2"
        :step="1"
        controls-position="right"
        placeholder="秒杀价（元）"
        :disabled="disabled"
        class="sku-picker__price"
      />

      <el-input-number
        v-model="totalStock"
        :min="1"
        :step="1"
        step-strictly
        controls-position="right"
        placeholder="秒杀库存"
        :disabled="disabled"
        class="sku-picker__stock"
      />

      <el-button type="primary" plain :disabled="disabled" @click="addBinding">
        添加绑定
      </el-button>
    </div>
  </div>
</template>

<style lang="scss" scoped>
.sku-picker {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px;
  background: var(--el-fill-color-lighter);
  border-radius: 6px;
}

.sku-picker__row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
}

.sku-picker__keyword {
  width: 190px;
}

.sku-picker__product {
  width: 220px;
}

.sku-picker__sku {
  width: 200px;
}

.sku-picker__price {
  width: 160px;
}

.sku-picker__stock {
  width: 140px;
}
</style>
