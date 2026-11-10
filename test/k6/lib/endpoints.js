// SurgeCart HTTP 端点清单（单一事实源）。
//
// 为什么单独建文件：路径同时被「场景脚本」和「request tag 的 name」使用。
// k6 的 name tag 会覆盖 URL 出现在指标里，因此带 ID 的动态路径必须映射回
// 路由模板（如 /products/:id），否则每个商品 ID 都会生成一条 k6 指标序列，
// 在 T5-02 正式压测时直接把结果文件撑爆，也让阈值无法按路由聚合。
//
// 所有路径均以 internal/cmd/routes_frontend.go、routes_admin.go 与
// api/*/v1/*.go 的 g.Meta path 为准（T5-01 已逐条核对源码）。

export const PATHS = {
  // 健康与可观测性（无认证，无副作用）。
  health: '/health',
  ready: '/ready',
  metrics: '/metrics',

  // IAM（公开）。
  register: '/register',
  login: '/login',
  refresh: '/refresh',
  me: '/me',

  // 商品（公开只读，前台仅返回上架数据）。
  products: '/products',
  categories: '/categories',

  // 后台认证与写接口（AdminAuth + RequirePermission）。
  adminLogin: '/admin/login',
  adminProducts: '/admin/products',
  adminSkus: '/admin/skus',
  adminFlashSales: '/admin/flash-sales',
  adminInventories: '/admin/inventories',
  adminDashboardFlashSales: '/admin/dashboard/flash-sales',

  // 秒杀下单与结果查询（前台登录用户）。
  flashSaleOrders: '/flash-sales/:id/orders',
  flashSaleOrderResult: '/flash-sales/:id/orders/result',

  // 普通订单查询（前台登录用户）。
  orders: '/orders',
};

// 动态路径构造器：返回值用于真实请求，routeTemplate 用于 k6 name tag。
export function productDetail(productId) {
  return {
    path: `/products/${productId}`,
    routeTemplate: '/products/:id',
  };
}

export function flashSaleOrders(activityId) {
  return {
    path: `/flash-sales/${activityId}/orders`,
    routeTemplate: PATHS.flashSaleOrders,
  };
}

export function flashSaleOrderResult(activityId, idempotencyKey) {
  return {
    path: `/flash-sales/${activityId}/orders/result?idempotency_key=${encodeURIComponent(idempotencyKey)}`,
    routeTemplate: PATHS.flashSaleOrderResult,
  };
}

export function adminProduct(productId) {
  return {
    path: `/admin/products/${productId}`,
    routeTemplate: '/admin/products/:id',
  };
}

export function adminProductOnShelf(productId) {
  return {
    path: `/admin/products/${productId}/on-shelf`,
    routeTemplate: '/admin/products/:id/on-shelf',
  };
}

export function adminInventory(skuId) {
  return {
    path: `/admin/inventories/${skuId}`,
    routeTemplate: '/admin/inventories/:sku_id',
  };
}

export function adminInventoryIncrease(skuId) {
  return {
    path: `/admin/inventories/${skuId}/increase`,
    routeTemplate: '/admin/inventories/:sku_id/increase',
  };
}

export function orderDetail(orderId) {
  return {
    path: `/orders/${orderId}`,
    routeTemplate: '/orders/:id',
  };
}
