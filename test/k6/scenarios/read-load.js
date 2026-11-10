// 只读负载场景（T5-02 实验入口）。
//
// 状态：IMPLEMENTED / VERIFIED（T5-02 已执行 1 / 5 / 10 VU 三档，见
//   docs/reports/t5-02-load-performance-analysis/）
//   - 阈值只覆盖「框架层不能坏」的部分，不预设任何性能目标；
//   - 具体负载参数由 LOAD_PROFILE + LOAD_VUS 控制，T5-02 定稿为 1 / 5 / 10 VU。
//
// 注意（T5-02 实测发现）：/products/:id 会在服务端执行一次 view_count 自增
// （internal/logic/product/product.go Detail），因此它不是纯 SELECT 接口。
// 本场景把它当作「读为主的业务接口」使用，其写放大必须在报告中披露。
//
// 为什么先做只读负载：
//   1. 只读链路（商品列表/详情）不写库、不占库存、不产生订单，可以安全地反复
//      建立基线，是理解「应用自身 HTTP 处理能力」的干净起点；
//   2. 秒杀写链路受 Redis 闸门、Kafka、Outbox 等多组件影响，必须先有只读基线
//      才能判断瓶颈是在应用层还是下游组件（T5-02 的核心分析目标）。
//
// 采集口径：
//   - 客户端侧：k6 的 http_req_duration（P50/P95/P99 由 summary 输出）；
//   - 服务端侧：surgecart_http_requests_total / _duration_seconds（Prometheus，
//     按 route 标签区分 /products 与 /products/:id）；
//   - 两者必须同时看：客户端 P95 包含网络与 port-forward 开销，服务端直方图才是
//     应用自身耗时。

import { sleep } from 'k6';
import { Trend } from 'k6/metrics';
import { loadConfig, printConfig, envStr, envInt } from '../config/default.js';
import { newClient, expectHttpOk, expectEnvelopeOk } from '../lib/http.js';
import * as biz from '../lib/business.js';
import { buildStages, mergeThresholds } from '../lib/stages.js';
import { makeHandleSummary } from '../lib/report.js';

const cfg = loadConfig({ scenario: 'read-load' });
printConfig(cfg);

const client = newClient(cfg);

// T5-02 新增：按接口拆分的客户端耗时 Trend。
// 为什么需要：k6 默认只在全局 http_req_duration 上做聚合，summary 里不会出现
// 按 route 拆分的分位数；而本实验要回答「列表页和详情页随负载如何变化」，
// 只有一个混合分位数是不够的（两个接口的耗时量级差异会被平均掉）。
// 用自定义 Trend 而不是给 http_req_duration 加 name tag 阈值：Trend 更直观，
// 且不会为了「让指标出现」而引入一个可能误报的阈值。
const listDuration = new Trend('k6_products_list_ms', true);
const detailDuration = new Trend('k6_products_detail_ms', true);

// LOAD_PROFILE: low / medium / high（见 lib/stages.js；峰值可用 LOAD_VUS 覆盖）
const profile = envStr('LOAD_PROFILE', 'low').toLowerCase();

// PRODUCT_ID 可选：提供则额外压 /products/:id（走单行查询 + SKU 关联），
// 不提供则只压列表页。ID 由 tools/prepare-data.js 产出后通过环境变量传入。
const productId = envInt('PRODUCT_ID', 0);
// T5-02 实测教训：环境变量按 shell 会话生效，漏传 PRODUCT_ID 时脚本会「静默降级」
// 成只压列表页，且阈值依然全绿（每次迭代请求数从 2 变 1）。这里显式告警，
// 让「实际压了哪些接口」在日志第一屏就能被看见，避免拿不可比的两次结果做对比。
if (productId <= 0) {
  console.warn('[read-load] PRODUCT_ID 未设置：本次只压 GET /products，不会请求商品详情');
} else {
  console.log('[read-load] 目标接口：GET /products + GET /products/' + productId);
}

// 每次迭代后的思考时间，避免 VU 以纯忙等方式刷服务端（真实用户有间隔）。
// 用它而不是「无间隔循环」，是为了让「VU 数 → RPS」的换算更接近真实流量形状。
const thinkTimeMs = envInt('THINK_TIME_MS', 200);

export const options = {
  stages: buildStages(profile),
  tags: { scenario: 'read-load' },
  // T5-02 新增：默认 summary 只给 med/p(90)/p(95)，本任务要求 P50/P90/P95/P99，
  // 因此显式声明需要的分位统计（同时影响控制台摘要与 summary.json）。
  summaryTrendStats: ['avg', 'min', 'med', 'p(50)', 'p(90)', 'p(95)', 'p(99)', 'max'],
  // 这里只保留框架级阈值。P95/P99 的性能目标必须由 T5-02 依据实测基线确定，
  // 提前写死一个数字会让「未验证的猜测」看起来像验收标准。
  thresholds: mergeThresholds(),
};

export default function () {
  // 列表页：分页参数固定，避免不同参数造成缓存/索引命中差异，影响可比性。
  const list = biz.listProducts(client, 'page=1&size=20');
  expectHttpOk(list);
  expectEnvelopeOk(list);
  listDuration.add(list.res.timings.duration);

  if (productId > 0) {
    const detail = biz.getProduct(client, productId);
    expectHttpOk(detail);
    expectEnvelopeOk(detail);
    detailDuration.add(detail.res.timings.duration);
  }

  sleep(thinkTimeMs / 1000);
}

export const handleSummary = makeHandleSummary(cfg, {
  profile: profile,
  product_id: productId,
  think_time_ms: thinkTimeMs,
});
