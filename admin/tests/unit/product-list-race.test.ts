import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ProductDTO, ProductListResult } from "@/api/types/product";
import { listProducts } from "@/api/modules/product";
import { useProductList } from "@/views/product/composables/useProductList";

vi.mock("@/api/modules/product", () => ({
  listProducts: vi.fn(),
  getProduct: vi.fn().mockResolvedValue({ skus: [] }),
  scanProducts: vi.fn()
}));

function pending<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

function result(id: number): ProductListResult {
  const product: ProductDTO = {
    id,
    name: `商品 ${id}`,
    brand: "",
    category_id: 1,
    price: 100,
    main_image: "",
    detail: "",
    status: "draft",
    view_count: 0,
    images: [],
    created_at: null,
    updated_at: null
  };
  return { items: [product], total: 1, page: 1, size: 10 };
}

describe("商品筛选的并发响应", () => {
  beforeEach(() => vi.mocked(listProducts).mockReset());

  it("较早查询晚返回时，仍显示最后一次查询的商品", async () => {
    const oldRequest = pending<ProductListResult>();
    const newRequest = pending<ProductListResult>();
    vi.mocked(listProducts)
      .mockReturnValueOnce(oldRequest.promise)
      .mockReturnValueOnce(newRequest.promise);
    const list = useProductList();
    const first = list.load();
    list.filters.keyword = "新查询";
    const second = list.load();
    newRequest.resolve(result(2));
    await second;
    oldRequest.resolve(result(1));
    await first;
    expect(list.rows.value.map(row => row.id)).toEqual([2]);
    expect(list.total.value).toBe(1);
    expect(list.error.value).toBeNull();
  });

  it("过期查询失败时，不清空新查询的商品", async () => {
    const oldRequest = pending<ProductListResult>();
    vi.mocked(listProducts)
      .mockReturnValueOnce(oldRequest.promise)
      .mockResolvedValueOnce(result(2));
    const list = useProductList();
    const first = list.load();
    await list.load();
    oldRequest.reject(new Error("旧请求失败"));
    await first;
    expect(list.rows.value.map(row => row.id)).toEqual([2]);
    expect(list.error.value).toBeNull();
    expect(list.loading.value).toBe(false);
  });
});
