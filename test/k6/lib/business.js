// SurgeCart 业务 API 封装与业务结果分类（T5-01）。
//
// 本文件的核心价值不是「再写一遍 HTTP 调用」，而是把「HTTP 成功」与
// 「业务成功」拆开，并把秒杀异步状态机的真实语义固化下来：
//
//   POST /flash-sales/:id/orders
//     → 受理（queued，HTTP 200 + code 0），不等于秒杀成功；
//     → 幂等命中时可能直接返回既有的 success/failed/dead 状态；
//     → 库存不足 / 一人一单 / 幂等冲突 / 限流 / 排队满 都是「业务拒绝」，
//       通常伴随 HTTP 409 / 429，按业务码判定而不是按状态码猜测。
//
// 所有业务码与状态枚举均以 internal/codes/codes.go 与
// api/flashsale/v1/flash_sale.go 为唯一事实源，禁止自行发明新值。

import { Counter } from 'k6/metrics';
import { PATHS } from './endpoints.js';
import * as ep from './endpoints.js';

// 通用业务码。
export const CODE = {
  OK: 0,
  INTERNAL_ERROR: 1000,
  INVALID_ARGUMENT: 1001,
  UNAUTHORIZED: 1002,
  FORBIDDEN: 1003,
  NOT_FOUND: 1004,
  SERVICE_UNAVAILABLE: 1005,
  USERNAME_EXISTS: 2001,
  INVALID_CREDENTIALS: 2002,
  SKU_NOT_FOUND: 5001,
  FLASH_ACTIVITY_NOT_FOUND: 12001,
  FLASH_NOT_IN_TIME_WINDOW: 12002,
  FLASH_STOCK_INSUFFICIENT: 12003,
  FLASH_ALREADY_PURCHASED: 12004,
  FLASH_IDEMPOTENCY_CONFLICT: 12005,
  FLASH_SKU_UNAVAILABLE: 12006,
  FLASH_INVALID_ARGUMENT: 12007,
  FLASH_RATE_LIMITED: 12009,
  FLASH_QUEUE_FULL: 12010,
};

// 秒杀异步请求状态（DB TINYINT 0/1/2/3 的 API 字符串形态）。
export const FLASH_STATUS = {
  QUEUED: 'queued',
  SUCCESS: 'success',
  FAILED: 'failed',
  DEAD: 'dead',
};

// 业务结果分类计数：operation 区分接口，outcome 是下面的固定枚举。
// 标签只使用固定枚举，杜绝把订单号/用户 ID 变成指标维度。
export const bizOutcomes = new Counter('k6_biz_outcomes');

export function recordOutcome(operation, outcome) {
  bizOutcomes.add(1, { operation: operation, outcome: outcome });
}

// ---------------------------------------------------------------------------
// 健康与可观测性
// ---------------------------------------------------------------------------

export function getHealth(client) {
  return client.get(PATHS.health, { name: 'GET /health' });
}

export function getReady(client) {
  return client.get(PATHS.ready, { name: 'GET /ready' });
}

// getMetricsText 读取 Prometheus 文本格式。
// textual:true 跳过 {code,message,data} 解析：/metrics 由 registerMetricsRoute
// 直接写裸文本，不经过 middleware.Response。
export function getMetricsText(client) {
  return client.get(PATHS.metrics, {
    name: 'GET /metrics',
    textual: true,
    headers: { Accept: 'text/plain' },
    timeoutMs: Math.max(client.cfg.timeoutMs, 10000),
  });
}

// ---------------------------------------------------------------------------
// 商品与 SKU（前台只读）
// ---------------------------------------------------------------------------

export function listProducts(client, query) {
  const suffix = query ? '?' + query : '';
  return client.get(PATHS.products + suffix, { name: 'GET /products' });
}

export function getProduct(client, productId) {
  const target = ep.productDetail(productId);
  return client.get(target, { name: 'GET /products/:id' });
}

export function listCategories(client) {
  return client.get(PATHS.categories, { name: 'GET /categories' });
}

// ---------------------------------------------------------------------------
// 后台写接口（仅测试数据准备使用，不进负载路径）
// ---------------------------------------------------------------------------

// createProduct 后台创建商品：status 恒为 draft（服务端强制），需再显式上架。
export function createProduct(client, payload) {
  return client.post(PATHS.adminProducts, payload, { name: 'POST /admin/products' });
}

export function onShelfProduct(client, productId) {
  return client.post(ep.adminProductOnShelf(productId), undefined, {
    name: 'POST /admin/products/:id/on-shelf',
  });
}

// createSku 后台创建 SKU：product_id 在请求体，价格单位为整数分。
export function createSku(client, payload) {
  return client.post(PATHS.adminSkus, payload, { name: 'POST /admin/skus' });
}

// createFlashSale 后台创建秒杀活动：start_time 必须早于 end_time，skus 非空。
export function createFlashSale(client, payload) {
  return client.post(PATHS.adminFlashSales, payload, { name: 'POST /admin/flash-sales' });
}

export function getInventory(client, skuId) {
  return client.get(ep.adminInventory(skuId), { name: 'GET /admin/inventories/:sku_id' });
}

export function increaseInventory(client, skuId, quantity) {
  return client.post(ep.adminInventoryIncrease(skuId), { quantity: quantity }, {
    name: 'POST /admin/inventories/:sku_id/increase',
  });
}

export function getDashboardFlashSales(client) {
  return client.get(PATHS.adminDashboardFlashSales, { name: 'GET /admin/dashboard/flash-sales' });
}

// ---------------------------------------------------------------------------
// 秒杀下单与结果查询
// ---------------------------------------------------------------------------

// createFlashOrder 提交秒杀下单。idempotency_key 由调用方提供：
// 同一个 key + 同一 (activity,sku) 视为同一请求（幂等重放），
// 同一个 key + 不同内容返回 12005。
export function createFlashOrder(client, activityId, skuId, idempotencyKey) {
  const target = ep.flashSaleOrders(activityId);
  return client.post(
    target,
    { sku_id: skuId, idempotency_key: idempotencyKey },
    { name: 'POST /flash-sales/:id/orders', phase: 'load' }
  );
}

export function getFlashOrderResult(client, activityId, idempotencyKey) {
  const target = ep.flashSaleOrderResult(activityId, idempotencyKey);
  return client.get(target, { name: 'GET /flash-sales/:id/orders/result', phase: 'verify' });
}

export function getOrderDetail(client, orderId) {
  return client.get(ep.orderDetail(orderId), { name: 'GET /orders/:id', phase: 'verify' });
}

// classifyCreateOrder 把下单响应映射为固定枚举的业务结果。
// 这是「区分 HTTP 请求成功与真正业务成功」的落点：HTTP 200 + code 0 只说明
// 请求被受理（queued），真正成交要等异步消费落单后由结果查询确认。
export function classifyCreateOrder(result) {
  if (result.status === 0) {
    return 'transport_error';
  }
  if (result.code === CODE.OK) {
    const status = result.data && result.data.status ? result.data.status : '';
    if (status === FLASH_STATUS.QUEUED) {
      return 'accepted';
    }
    if (status === FLASH_STATUS.SUCCESS) {
      return 'idempotent_replay_success';
    }
    if (status === FLASH_STATUS.FAILED) {
      return 'idempotent_replay_failed';
    }
    if (status === FLASH_STATUS.DEAD) {
      return 'idempotent_replay_dead';
    }
    return 'accepted_unknown_status';
  }
  switch (result.code) {
    case CODE.FLASH_STOCK_INSUFFICIENT:
      return 'stock_insufficient';
    case CODE.FLASH_ALREADY_PURCHASED:
      return 'already_purchased';
    case CODE.FLASH_IDEMPOTENCY_CONFLICT:
      return 'idempotency_conflict';
    case CODE.FLASH_SKU_UNAVAILABLE:
      return 'sku_unavailable';
    case CODE.FLASH_NOT_IN_TIME_WINDOW:
      return 'not_in_time_window';
    case CODE.FLASH_ACTIVITY_NOT_FOUND:
      return 'activity_not_found';
    case CODE.FLASH_RATE_LIMITED:
      return 'rate_limited';
    case CODE.FLASH_QUEUE_FULL:
      return 'queue_full';
    case CODE.INVALID_ARGUMENT:
      return 'invalid_argument';
    case CODE.UNAUTHORIZED:
      return 'unauthorized';
    case CODE.SERVICE_UNAVAILABLE:
      return 'service_unavailable';
    case CODE.INTERNAL_ERROR:
      return 'internal_error';
    default:
      return 'biz_error';
  }
}

// 视为「系统侧未能给出确定性业务结论」的结果集合：T5-02 用来单独统计
// 不被业务规则解释的失败，避免把它们混进「秒杀失败」掩盖真实故障。
export const AMBIGUOUS_OUTCOMES = [
  'transport_error',
  'internal_error',
  'service_unavailable',
  'accepted_unknown_status',
  'biz_error',
];

export function isAmbiguousOutcome(outcome) {
  return AMBIGUOUS_OUTCOMES.indexOf(outcome) >= 0;
}

// classifyOrderResult 把结果查询响应映射为最终业务状态。
// success 必须带 order（服务端保证 success 与成功订单同事务写入）；
// failed/dead 携带 fail_code（最近失败码，观测用但不是权威状态）。
export function classifyOrderResult(result) {
  if (result.status === 0) {
    return { state: 'transport_error', order: null, failCode: 0 };
  }
  if (result.code !== CODE.OK || !result.data) {
    return { state: 'query_error', order: null, failCode: 0 };
  }
  const status = result.data.status || '';
  if (status === FLASH_STATUS.SUCCESS) {
    return { state: FLASH_STATUS.SUCCESS, order: result.data.order || null, failCode: 0 };
  }
  if (status === FLASH_STATUS.FAILED) {
    return { state: FLASH_STATUS.FAILED, order: null, failCode: result.data.fail_code || 0 };
  }
  if (status === FLASH_STATUS.DEAD) {
    return { state: FLASH_STATUS.DEAD, order: null, failCode: result.data.fail_code || 0 };
  }
  return { state: FLASH_STATUS.QUEUED, order: null, failCode: 0 };
}

// isoSeconds 返回 RFC3339 秒级 UTC 时间串（如 2026-11-10T03:41:26Z）。
// 为什么用 RFC3339：GoFrame gtime 可直接解析，且带 Z 时区语义明确，
// 不会因为宿主机/容器时区不同导致活动时间窗偏移。
export function isoSeconds(offsetSeconds) {
  const base = Date.now() + (offsetSeconds || 0) * 1000;
  return new Date(base).toISOString().replace(/\.\d{3}Z$/, 'Z');
}
