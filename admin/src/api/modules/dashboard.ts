/**
 * 运营工作台（Dashboard）API 适配层。
 *
 * 依据：`api/dashboard/v1/dashboard.go`（后端已实现的 v1 契约）。
 * 五个接口均挂在 `/admin/dashboard/**`，需要后端权限 `dashboard:view`。
 *
 * URI 约定：路径不带 `/api`，由 Axios `baseURL=/api` + Vite Proxy 统一处理。
 *
 * 类型状态：PROVISIONAL。
 * `contracts/openapi.yaml` 尚未交付，DTO 依据后端 Go 结构体手写。
 * 契约冻结后应替换为 `pnpm api:types` 生成类型，函数签名保持不变。
 *
 * 失败策略：全部使用 `silent: true`。
 * 工作台各分区独立渲染"加载失败/权限不足/重试"，不让单个接口失败触发全局弹窗。
 */
import { http } from "@/utils/http";
import { getRecentDaysRFC3339 } from "@/utils/datetime";

/** 【PROVISIONAL】运营概览 */
export interface DashboardOverview {
  /** 用户总数 */
  user_total: number;
  /** 今日新增用户数 */
  today_new_users: number;
  /** 商品总数 */
  product_total: number;
  /** 订单总数（全部状态） */
  order_total: number;
  /** 今日新增订单数（全部状态） */
  today_new_orders: number;
  /** 今日订单金额，单位：分（已支付口径，非退款后实收） */
  today_order_amount: number;
}

/** 【PROVISIONAL】单日订单数 */
export interface DashboardTrendItem {
  /** 日期 `YYYY-MM-DD` */
  date: string;
  /** 当日订单数（无数据为 0） */
  order_count: number;
}

/** 【PROVISIONAL】订单趋势（后端固定最近 7 天） */
export interface DashboardOrderTrend {
  days: DashboardTrendItem[];
}

/** 【PROVISIONAL】单个状态的分布计数 */
export interface DashboardStatusItem {
  /** 订单状态整数码（10/20/30/40/50/60/70） */
  status: number;
  count: number;
}

/** 【PROVISIONAL】订单状态分布 */
export interface DashboardOrderStatus {
  total: number;
  status_distribution: DashboardStatusItem[];
}

/** 【PROVISIONAL】销量排行条目 */
export interface DashboardTopProduct {
  rank: number;
  product_id: number;
  product_name: string;
  /** 销量（有效订单明细数量之和） */
  sales: number;
}

/** 【PROVISIONAL】销量排行 */
export interface DashboardTopProducts {
  items: DashboardTopProduct[];
}

/** 【PROVISIONAL】秒杀活动状态分布 */
export interface DashboardFlashSaleActivityStatus {
  enabled: number;
  disabled: number;
}

/** 【PROVISIONAL】秒杀请求状态分布 */
export interface DashboardFlashSaleRequestStatus {
  queued: number;
  success: number;
  failed: number;
  dead: number;
}

/** 【PROVISIONAL】秒杀运营统计 */
export interface DashboardFlashSales {
  activity_total: number;
  activity_status: DashboardFlashSaleActivityStatus;
  request_status: DashboardFlashSaleRequestStatus;
  /** 当前 queued 请求数量 */
  current_queued: number;
  /** 秒杀成功订单数量 */
  success_orders: number;
}

/** 订单状态分布查询参数（RFC3339，必须同传） */
export interface DashboardStatusQuery {
  start_time: string;
  end_time: string;
}

/** 运营概览 */
export function getDashboardOverview() {
  return http.get<DashboardOverview, undefined>(
    "/admin/dashboard/overview",
    undefined,
    { silent: true }
  );
}

/** 最近 7 天订单趋势（后端固定窗口，无查询参数） */
export function getDashboardOrderTrend() {
  return http.get<DashboardOrderTrend, undefined>(
    "/admin/dashboard/orders/trend",
    undefined,
    { silent: true }
  );
}

/**
 * 订单状态分布。
 *
 * 不传参数时后端统计全部订单；工作台默认传最近 7 天窗口。
 */
export function getDashboardOrderStatus(query?: DashboardStatusQuery) {
  return http.get<DashboardOrderStatus, undefined>(
    "/admin/dashboard/orders/status",
    query ? { params: query } : undefined,
    { silent: true }
  );
}

/** 热销商品 TOP 榜（后端默认 10，上限 50） */
export function getDashboardTopProducts(limit = 10) {
  return http.get<DashboardTopProducts, undefined>(
    "/admin/dashboard/products/top",
    { params: { limit } },
    { silent: true }
  );
}

/** 秒杀运营统计 */
export function getDashboardFlashSales() {
  return http.get<DashboardFlashSales, undefined>(
    "/admin/dashboard/flash-sales",
    undefined,
    { silent: true }
  );
}

/**
 * 工作台默认查询窗口：最近 7 天。
 *
 * 单独导出，便于页面与测试复用同一口径。
 */
export function getDefaultDashboardStatusQuery() {
  return getRecentDaysRFC3339(7);
}
