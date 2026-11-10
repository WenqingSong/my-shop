// 测试数据准备工具（T5-01）。
//
// 状态：IMPLEMENTED / NOT_VERIFIED
//   本任务没有可用的管理员凭据（不读取 .env / Secret），因此没有真实执行过本工具；
//   执行方式与前置条件见 README §测试数据。产物形状与 API 契约均按源码核对。
//
// 为什么用 k6 脚本而不是 SQL：
//   1. 全部走真实业务 API（后台商品/SKU/秒杀活动接口），数据一定满足业务不变量
//      （分类必须为启用叶子、SKU 必须属主于商品、活动时间窗必须 start<end 等）；
//   2. 不直接写库，不绕过校验，也不会因为 schema 变化而静默造出脏数据；
//   3. 与压测脚本共用同一套配置与 HTTP 封装，减少「准备脚本与压测脚本行为不一致」。
//
// 数据隔离原则（本工具的核心约束）：
//   - 所有对象名都带 DATA_PREFIX + run 短标识，天然区分不同测试运行；
//   - 不修改、不复用已有正常业务商品/活动（除非显式传 CATEGORY_ID）；
//   - 不删除任何数据：清理是独立决策，需人工确认范围（见 README）。
//
// 产出：results/<run_id>.data.json
//   { run_id, category_id, product_id, sku_id, activity_id, flash_price, total_stock, ... }
//   后续场景通过环境变量传入 ACTIVITY_ID / SKU_ID / PRODUCT_ID。

import { check } from 'k6';
import { loadConfig, printConfig, envInt, envStr, shortRunId } from '../config/default.js';
import { newClient, expectEnvelopeOk } from '../lib/http.js';
import * as biz from '../lib/business.js';
import { loginAdmin } from '../lib/auth.js';
import { makeHandleSummary } from '../lib/report.js';

const cfg = loadConfig({ scenario: 'prepare-data' });
printConfig(cfg);

// 秒杀库存：独立测试活动的初始库存，决定 T5-03 的「超卖验证」样本规模。
const totalStock = envInt('FLASH_TOTAL_STOCK', 100);
// 普通库存：仅在需要同时验证普通下单路径时设置（0 = 不初始化）。
const inventoryQty = envInt('INVENTORY_QTY', 0);
// 商品价格（整数分）：SKU 价 199.00，秒杀价 99.00。
const skuPrice = envInt('SKU_PRICE', 19900);
const flashPrice = envInt('FLASH_PRICE', 9900);

const tag = shortRunId(cfg.runId);

export const options = {
  // 数据准备是「一次性」动作：单 VU、单迭代。setup 做写入，default 做只读复核。
  vus: 1,
  iterations: 1,
  tags: { scenario: 'prepare-data' },
  thresholds: {
    checks: ['rate==1'],
    k6_request_errors: ['count==0'],
    k6_envelope_errors: ['count==0'],
  },
};

// mustOk 统一处理后台写接口的失败：业务码非 0 一律中止，避免在半成品数据上继续建对象。
function mustOk(result, what) {
  expectEnvelopeOk(result);
  if (result.code !== 0) {
    throw new Error(`[prepare-data] ${what} 失败：code=${result.code} message=${result.message}`);
  }
  return result.data;
}

// findOrCreateLeafCategory 找一个「启用且无子分类」的分类。
// 为什么必须是叶子分类：商品创建要求 category_id 指向存在且启用的叶子分类
// （internal/codes/codes.go CodeProductCategoryNotLeaf / CodeProductCategoryDisabled）。
// 为什么不总是新建分类：分类是全局可见的业务数据，能用现成的就不新增。
function findOrCreateLeafCategory(admin) {
  const explicit = envInt('CATEGORY_ID', 0);
  if (explicit > 0) {
    console.log('[prepare-data] 使用显式 CATEGORY_ID=' + explicit);
    return explicit;
  }

  const res = admin.get('/categories', { name: 'GET /categories' });
  mustOk(res, '查询分类树');
  const items = (res.data && res.data.items) || [];
  for (let i = 0; i < items.length; i++) {
    const node = items[i];
    if (node.status === 1 && (!node.children || node.children.length === 0)) {
      console.log('[prepare-data] 复用已有叶子分类 id=' + node.id + ' name=' + node.name);
      return node.id;
    }
  }

  const created = mustOk(
    admin.post('/categories', { parent_id: 0, name: cfg.dataPrefix + '-cat-' + tag, sort: 0, status: 1 }, {
      name: 'POST /categories',
    }),
    '创建测试分类'
  );
  console.log('[prepare-data] 新建叶子分类 id=' + created.id);
  return created.id;
}

export function setup() {
  const admin = newClient(cfg);
  const adminAuth = loginAdmin(admin);
  admin.setToken(adminAuth.token);

  const categoryId = findOrCreateLeafCategory(admin);

  // 1) 商品：服务端强制 status=draft，创建后必须显式上架才对前台可见。
  const product = mustOk(
    biz.createProduct(admin, {
      name: cfg.dataPrefix + '-product-' + tag,
      brand: 'k6-test',
      category_id: categoryId,
      price: skuPrice,
      main_image: '',
      detail: 'k6 T5-01 独立测试商品（非正常业务数据）',
      images: [],
    }),
    '创建测试商品'
  );
  mustOk(biz.onShelfProduct(admin, product.id), '上架测试商品');

  // 2) SKU：必须属于该商品，且 enabled 才能参与秒杀与普通下单。
  const sku = mustOk(
    biz.createSku(admin, {
      product_id: product.id,
      name: cfg.dataPrefix + '-sku-' + tag,
      price: skuPrice,
      status: 'enabled',
    }),
    '创建测试 SKU'
  );

  // 3) 普通库存（可选）：只影响普通下单链路，不影响秒杀库存口径。
  if (inventoryQty > 0) {
    mustOk(biz.increaseInventory(admin, sku.id, inventoryQty), '初始化普通库存');
  }

  // 4) 秒杀活动：时间窗覆盖「现在」，因此立即可下单；
  //    start/end 用 RFC3339 UTC，避免容器时区与宿主机时区不一致导致窗口偏移。
  const activity = mustOk(
    biz.createFlashSale(admin, {
      name: cfg.dataPrefix + '-flash-' + tag,
      start_time: biz.isoSeconds(-60),
      end_time: biz.isoSeconds(3600),
      skus: [{ sku_id: sku.id, flash_price: flashPrice, total_stock: totalStock }],
    }),
    '创建测试秒杀活动'
  );

  const prepared = {
    run_id: cfg.runId,
    test_env: cfg.testEnv,
    base_url: cfg.baseUrl,
    data_prefix: cfg.dataPrefix,
    category_id: categoryId,
    product_id: product.id,
    sku_id: sku.id,
    activity_id: activity.id,
    activity_start_time: activity.start_time,
    activity_end_time: activity.end_time,
    flash_price: flashPrice,
    total_stock: totalStock,
    inventory_qty: inventoryQty,
    prepared_at: new Date().toISOString(),
  };

  // 单行机器可读标记：由 scripts/run.ps1 解析后落盘为 data/<run_id>.data.json。
  // 为什么不写在 handleSummary 里：handleSummary 只拿得到 k6 指标，
  // 拿不到 setup 的返回值；用显式标记行比依赖模块级变量跨阶段传递更可靠。
  console.log('K6_DATA_MARKER ' + JSON.stringify(prepared));
  console.log(
    '[prepare-data] 完成 activity_id=' +
      activity.id +
      ' sku_id=' +
      sku.id +
      ' product_id=' +
      product.id +
      ' total_stock=' +
      totalStock
  );

  return {
    admin_token: adminAuth.token,
    prepared: prepared,
  };
}

// default 只做只读复核：确认「准备出来的数据真的能被前台看到」。
// 写入成功但上架失败/缓存未同步这类问题，只能靠读接口暴露。
export default function (data) {
  const prepared = data.prepared;
  const client = newClient(cfg);

  const detail = biz.getProduct(client, prepared.product_id);
  expectEnvelopeOk(detail);
  check(detail, {
    '准备数据 | 商品前台可见且已上架': (r) =>
      r.code === 0 && r.data !== null && r.data.status === 'on_shelf',
    '准备数据 | 商品包含 enabled SKU': (r) =>
      r.code === 0 &&
      r.data !== null &&
      Array.isArray(r.data.skus) &&
      r.data.skus.some(function (s) {
        return s.id === prepared.sku_id && s.status === 'enabled';
      }),
  });

  // 库存与秒杀统计走后台只读接口，确认服务端视角的数据一致。
  const admin = newClient(cfg).setToken(data.admin_token);
  if (inventoryQty > 0) {
    const inv = biz.getInventory(admin, prepared.sku_id);
    expectEnvelopeOk(inv);
    check(inv, {
      '准备数据 | 普通库存已初始化': (r) => r.code === 0 && r.data !== null && r.data.quantity === inventoryQty,
    });
  }

  const stats = biz.getDashboardFlashSales(admin);
  expectEnvelopeOk(stats);
  check(stats, {
    '准备数据 | 秒杀活动总数 >= 1': (r) => r.code === 0 && r.data !== null && r.data.activity_total >= 1,
  });
}

// 结果输出：只写标准 summary。准备好的 ID 由 setup() 的 K6_DATA_MARKER 行
// 交给 runner 落盘成 data/<run_id>.data.json（见 scripts/run.ps1）。
export const handleSummary = makeHandleSummary(cfg, { profile: 'prepare-data' });
