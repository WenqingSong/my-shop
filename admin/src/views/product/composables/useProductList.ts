import { computed, reactive, ref } from "vue";
import { getProduct, listProducts, scanProducts } from "@/api/modules/product";
import type { ProductDTO, ProductStatus } from "@/api/types/product";
import {
  filterProductsByStatus,
  slicePage,
  toProductQuery,
  toProductView,
  type ProductView
} from "@/api/adapters/product-adapter";
import { resolveErrorMessage } from "@/utils/error-message";

/** SKU 数量并发请求上限 */
const SKU_ENRICH_CONCURRENCY = 4;

/**
 * 商品列表状态。
 *
 * 两个已知的后端限制在这里被显式处理，而不是假装支持：
 * 1. 列表接口**没有** status 参数：选择状态时改为"跨页拉取真实数据 + 前端过滤"，
 *    并暴露 `truncated` 告知结果可能不完整；
 * 2. 列表接口**不返回** SKU 数量：按需并发请求商品详情补全，
 *    失败时该格显示"—"，不影响整页。
 */
export function useProductList() {
  const rows = ref<ProductView[]>([]);
  const total = ref(0);
  const page = ref(1);
  const size = ref(10);
  const loading = ref(false);
  const enriching = ref(false);
  const error = ref<string | null>(null);
  /** 状态筛选跨页拉取是否达到页数上限 */
  const truncated = ref(false);

  /** SKU 数量：`null` 加载中，`-1` 未知 */
  const skuCounts = reactive<Record<number, number | null>>({});

  const filters = reactive<{
    keyword: string;
    categoryId: number | null;
    status: ProductStatus | "";
  }>({
    keyword: "",
    categoryId: null,
    status: ""
  });

  const hasStatusFilter = computed(() => filters.status !== "");

  async function enrichSkuCounts(items: ProductDTO[]) {
    if (items.length === 0) return;
    enriching.value = true;

    const queue = [...items];
    const workers = Array.from(
      { length: Math.min(SKU_ENRICH_CONCURRENCY, queue.length) },
      async () => {
        for (;;) {
          const next = queue.shift();
          if (!next) return;
          skuCounts[next.id] = null;
          try {
            const detail = await getProduct(next.id);
            skuCounts[next.id] = detail.skus?.length ?? 0;
          } catch {
            skuCounts[next.id] = -1;
          }
        }
      }
    );

    try {
      await Promise.all(workers);
    } finally {
      enriching.value = false;
    }
  }

  function applyRows(items: ProductDTO[]) {
    rows.value = items.map(toProductView);
  }

  async function load() {
    loading.value = true;
    error.value = null;
    truncated.value = false;

    try {
      if (hasStatusFilter.value) {
        const scan = await scanProducts({
          keyword: filters.keyword.trim() || undefined,
          category_id: filters.categoryId ?? undefined,
          sort: "id",
          order: "desc"
        });
        const filtered = filterProductsByStatus(scan.items, filters.status);
        truncated.value = scan.truncated;
        total.value = filtered.length;

        const pageItems = slicePage(filtered, page.value, size.value);
        applyRows(pageItems);
        void enrichSkuCounts(pageItems);
        return;
      }

      const result = await listProducts(
        toProductQuery({
          page: page.value,
          size: size.value,
          keyword: filters.keyword,
          categoryId: filters.categoryId,
          sort: "id",
          order: "desc"
        })
      );

      total.value = result.total;
      applyRows(result.items ?? []);
      void enrichSkuCounts(result.items ?? []);
    } catch (cause) {
      rows.value = [];
      total.value = 0;
      error.value = resolveErrorMessage(cause, "商品列表加载失败");
    } finally {
      loading.value = false;
    }
  }

  /** 点击查询：回到第一页 */
  function search() {
    page.value = 1;
    void load();
  }

  /** 重置筛选条件 */
  function reset() {
    filters.keyword = "";
    filters.categoryId = null;
    filters.status = "";
    page.value = 1;
    void load();
  }

  function handlePageChange(next: number) {
    page.value = next;
    void load();
  }

  function handleSizeChange(next: number) {
    size.value = next;
    page.value = 1;
    void load();
  }

  return {
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
  };
}
