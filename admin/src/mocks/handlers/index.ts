import type { HttpHandler } from "msw";
import { adminHandlers } from "./admin";
import { dashboardHandlers } from "./dashboard";
import { productHandlers } from "./product";
import { categoryHandlers } from "./category";
import { inventoryHandlers } from "./inventory";
import { uploadHandlers } from "./upload";
import { orderHandlers } from "./order";
import { refundHandlers } from "./refund";
import { couponHandlers } from "./coupon";
import { flashSaleHandlers } from "./flash-sale";
import { bannerHandlers } from "./banner";
import { recommendationHandlers } from "./recommendation";
import { articleHandlers } from "./article";
import { accessHandlers } from "./access";

/**
 * Mock 处理器集合。
 *
 * 约定：
 * - 覆盖 A1（管理员认证、运营工作台）与 A2（商品、分类、SKU、库存、上传）
 *   的接口，字段结构与后端 DTO 保持一致。
 * - handler 中的数值是演示夹具，不是真实业务数据；
 *   单元测试可以 `server.use(...)` 覆盖成空数据、403、业务错误等场景。
 * - 请求仍由真实 `src/utils/http` 的 Axios 实例发出，Mock 只在网络层拦截。
 * - A2 的内存状态可通过 `resetMockDb()` 重置（见 `./mock-db`）。
 */
export const handlers: HttpHandler[] = [
  ...adminHandlers,
  ...dashboardHandlers,
  ...productHandlers,
  ...categoryHandlers,
  ...inventoryHandlers,
  ...uploadHandlers,
  ...orderHandlers,
  ...refundHandlers,
  ...couponHandlers,
  ...flashSaleHandlers,
  ...bannerHandlers,
  ...recommendationHandlers,
  ...articleHandlers,
  ...accessHandlers
];
