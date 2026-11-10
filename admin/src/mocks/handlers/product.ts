import { http } from "msw";
import { mockDb, type MockProduct, type MockSku } from "./mock-db";
import {
  fail,
  isAuthorized,
  ok,
  queryInt,
  toInt,
  unauthorized
} from "./shared";

/**
 * 商品 / SKU Mock。
 *
 * 行为对齐 `internal/logic/product`、`internal/logic/sku`：
 * - 创建商品强制 draft；
 * - 更新商品忽略合法 status、拒绝非法 status；
 * - 状态只能通过 on-shelf / off-shelf 迁移；
 * - 分类必须是存在、启用、无子分类的叶子；
 * - SKU 名称同商品内唯一，删除有库存/流水的 SKU 返回 5005。
 */
const PRICE_MAX = 99_999_999;

function findProduct(id: number): MockProduct | null {
  return mockDb.products.find(product => product.id === id) ?? null;
}

function skusOf(productId: number): MockSku[] {
  return mockDb.skus
    .filter(sku => sku.product_id === productId)
    .sort((left, right) => left.id - right.id);
}

function validateCategory(
  categoryId: number
): { code: number; message: string } | null {
  const category = mockDb.categories.find(item => item.id === categoryId);
  if (!category) return { code: 4003, message: "分类非法" };
  if (category.status !== 1) return { code: 4007, message: "分类已禁用" };
  const hasChildren = mockDb.categories.some(
    item => item.parent_id === categoryId
  );
  if (hasChildren) return { code: 4004, message: "分类不是叶子分类" };
  return null;
}

function validateName(name: string): boolean {
  const trimmed = name.trim();
  return trimmed.length > 0 && [...trimmed].length <= 128;
}

function validatePrice(value: unknown): number | null {
  const price = toInt(value);
  if (price === null || price < 0 || price > PRICE_MAX) return null;
  return price;
}

function validateSkuStatus(value: unknown): "enabled" | "disabled" | null {
  if (value === "enabled" || value === "disabled") return value;
  return null;
}

function productListResponse(products: MockProduct[]) {
  return products.map(product => ({
    ...product,
    images: [...product.images]
  }));
}

export const productHandlers = [
  /** GET /admin/products */
  http.get("*/api/admin/products", ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();

    const url = new URL(request.url);
    const page = queryInt(url.searchParams, "page", 1);
    const size = Math.min(queryInt(url.searchParams, "size", 20), 100);
    const keyword = (url.searchParams.get("keyword") ?? "")
      .trim()
      .toLowerCase();
    const categoryId = url.searchParams.get("category_id");
    const sort = url.searchParams.get("sort") ?? "id";
    const order = url.searchParams.get("order") ?? "desc";

    let items = [...mockDb.products];

    if (keyword) {
      items = items.filter(
        product =>
          product.name.toLowerCase().includes(keyword) ||
          product.brand.toLowerCase().includes(keyword)
      );
    }
    if (categoryId) {
      items = items.filter(
        product => product.category_id === Number(categoryId)
      );
    }

    const direction = order === "asc" ? 1 : -1;
    items.sort((left, right) => {
      if (sort === "price") return (left.price - right.price) * direction;
      if (sort === "created_at" || sort === "updated_at") {
        return left[sort].localeCompare(right[sort]) * direction;
      }
      return (left.id - right.id) * direction;
    });

    const total = items.length;
    const start = (page - 1) * size;

    return ok({
      items: productListResponse(items.slice(start, start + size)),
      total,
      page,
      size
    });
  }),

  /** GET /admin/products/:id */
  http.get("*/api/admin/products/:id", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();
    const product = findProduct(Number(params.id));
    if (!product) return fail(4001, "商品不存在", 404);

    return ok({
      ...product,
      images: [...product.images],
      skus: skusOf(product.id)
    });
  }),

  /** POST /admin/products */
  http.post("*/api/admin/products", async ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();
    const body = (await request.json()) as Record<string, unknown>;

    const name = String(body.name ?? "");
    if (!validateName(name)) return fail(1001, "参数错误");

    const brand = String(body.brand ?? "");
    if ([...brand].length > 64) return fail(1001, "参数错误");

    const categoryId = toInt(body.category_id) ?? 0;
    const categoryError = validateCategory(categoryId);
    if (categoryError) return fail(categoryError.code, categoryError.message);

    const price = validatePrice(body.price);
    if (price === null) return fail(4002, "价格非法");

    const mainImage = String(body.main_image ?? "");
    if (mainImage.length > 512) return fail(1001, "参数错误");

    const images = Array.isArray(body.images) ? body.images.map(String) : [];
    if (images.some(url => !url.trim())) return fail(1001, "参数错误");

    const product: MockProduct = {
      id: mockDb.nextProductId++,
      name: name.trim(),
      brand: brand.trim(),
      category_id: categoryId,
      price,
      main_image: mainImage,
      detail: String(body.detail ?? ""),
      // 后端固定创建为草稿
      status: "draft",
      view_count: 0,
      images,
      created_at: mockDb.now(),
      updated_at: mockDb.now()
    };

    mockDb.products.push(product);
    return ok({ ...product, images: [...product.images] });
  }),

  /** PUT /admin/products/:id */
  http.put("*/api/admin/products/:id", async ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();
    const product = findProduct(Number(params.id));
    if (!product) return fail(4001, "商品不存在", 404);

    const body = (await request.json()) as Record<string, unknown>;

    if (body.name !== undefined) {
      const name = String(body.name);
      if (!validateName(name)) return fail(1001, "参数错误");
      product.name = name.trim();
    }

    if (body.brand !== undefined) {
      const brand = String(body.brand);
      if ([...brand].length > 64) return fail(1001, "参数错误");
      product.brand = brand.trim();
    }

    if (body.category_id !== undefined) {
      const categoryId = toInt(body.category_id) ?? 0;
      const categoryError = validateCategory(categoryId);
      if (categoryError) return fail(categoryError.code, categoryError.message);
      product.category_id = categoryId;
    }

    if (body.price !== undefined) {
      const price = validatePrice(body.price);
      if (price === null) return fail(4002, "价格非法");
      product.price = price;
    }

    if (body.main_image !== undefined) {
      const mainImage = String(body.main_image);
      if (mainImage.length > 512) return fail(1001, "参数错误");
      product.main_image = mainImage;
    }

    if (body.detail !== undefined) product.detail = String(body.detail);

    if (body.images !== undefined) {
      const images = Array.isArray(body.images) ? body.images.map(String) : [];
      if (images.some(url => !url.trim())) return fail(1001, "参数错误");
      product.images = images;
    }

    if (body.status !== undefined) {
      // 合法值被忽略，非法值拒绝
      if (
        body.status !== "draft" &&
        body.status !== "on_shelf" &&
        body.status !== "off_shelf"
      ) {
        return fail(4006, "商品状态非法", 400);
      }
    }

    product.updated_at = mockDb.now();
    return ok({ ...product, images: [...product.images] });
  }),

  /** POST /admin/products/:id/on-shelf */
  http.post("*/api/admin/products/:id/on-shelf", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();
    const product = findProduct(Number(params.id));
    if (!product) return fail(4001, "商品不存在", 404);
    if (product.status !== "draft" && product.status !== "off_shelf") {
      return fail(4005, "商品状态迁移非法", 409);
    }

    const categoryError = validateCategory(product.category_id);
    if (categoryError) return fail(categoryError.code, categoryError.message);

    product.status = "on_shelf";
    product.updated_at = mockDb.now();
    return ok({ ...product, images: [...product.images] });
  }),

  /** POST /admin/products/:id/off-shelf */
  http.post("*/api/admin/products/:id/off-shelf", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();
    const product = findProduct(Number(params.id));
    if (!product) return fail(4001, "商品不存在", 404);
    if (product.status !== "on_shelf") {
      return fail(4005, "商品状态迁移非法", 409);
    }

    product.status = "off_shelf";
    product.updated_at = mockDb.now();
    return ok({ ...product, images: [...product.images] });
  }),

  /** POST /admin/skus */
  http.post("*/api/admin/skus", async ({ request }) => {
    if (!isAuthorized(request)) return unauthorized();
    const body = (await request.json()) as Record<string, unknown>;

    const productId = toInt(body.product_id) ?? 0;
    if (!findProduct(productId)) return fail(4001, "商品不存在", 404);

    const name = String(body.name ?? "");
    if (!validateName(name)) return fail(1001, "参数错误");

    const price = validatePrice(body.price);
    if (price === null) return fail(5002, "价格非法");

    const status =
      body.status === undefined ? "enabled" : validateSkuStatus(body.status);
    if (status === null) return fail(5003, "SKU 状态非法");

    const duplicated = mockDb.skus.some(
      sku => sku.product_id === productId && sku.name === name.trim()
    );
    if (duplicated) return fail(5004, "SKU 名称已存在", 409);

    const sku: MockSku = {
      id: mockDb.nextSkuId++,
      product_id: productId,
      name: name.trim(),
      price,
      status,
      created_at: mockDb.now(),
      updated_at: mockDb.now()
    };

    mockDb.skus.push(sku);
    return ok(sku);
  }),

  /** PUT /admin/skus/:id */
  http.put("*/api/admin/skus/:id", async ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();
    const sku = mockDb.skus.find(item => item.id === Number(params.id));
    if (!sku) return fail(5001, "SKU 不存在", 404);

    const body = (await request.json()) as Record<string, unknown>;

    if (body.name !== undefined) {
      const name = String(body.name);
      if (!validateName(name)) return fail(1001, "参数错误");
      const duplicated = mockDb.skus.some(
        item =>
          item.id !== sku.id &&
          item.product_id === sku.product_id &&
          item.name === name.trim()
      );
      if (duplicated) return fail(5004, "SKU 名称已存在", 409);
      sku.name = name.trim();
    }

    if (body.price !== undefined) {
      const price = validatePrice(body.price);
      if (price === null) return fail(5002, "价格非法");
      sku.price = price;
    }

    if (body.status !== undefined) {
      const status = validateSkuStatus(body.status);
      if (!status) return fail(5003, "SKU 状态非法");
      sku.status = status;
    }

    sku.updated_at = mockDb.now();
    return ok(sku);
  }),

  /** DELETE /admin/skus/:id */
  http.delete("*/api/admin/skus/:id", ({ request, params }) => {
    if (!isAuthorized(request)) return unauthorized();
    const id = Number(params.id);
    const sku = mockDb.skus.find(item => item.id === id);
    if (!sku) return fail(5001, "SKU 不存在", 404);

    // 与后端 FK RESTRICT 等价：有库存记录或有流水都不能删
    const hasInventory = mockDb.inventories.has(id);
    const hasLogs = mockDb.logs.some(log => log.sku_id === id);
    if (hasInventory || hasLogs) {
      return fail(5005, "SKU 存在库存记录，不能删除", 409);
    }

    mockDb.skus = mockDb.skus.filter(item => item.id !== id);
    return ok(null);
  })
];
