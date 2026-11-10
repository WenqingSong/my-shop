// 只读负载场景（T5-02 入口，T5-01 未执行）。
//
// 状态：IMPLEMENTED / NOT_VERIFIED
//   - 本任务只做静态检查（k6 inspect），没有执行任何负载档；
//   - 因此这里不声明任何容量结论，阈值只覆盖「框架层不能坏」的部分。
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
import { loadConfig, printConfig, envStr, envInt } from '../config/default.js';
import { newClient, expectHttpOk, expectEnvelopeOk } from '../lib/http.js';
import * as biz from '../lib/business.js';
import { buildStages, mergeThresholds } from '../lib/stages.js';
import { makeHandleSummary } from '../lib/report.js';

const cfg = loadConfig({ scenario: 'read-load' });
printConfig(cfg);

const client = newClient(cfg);

// LOAD_PROFILE: low / medium / high（见 lib/stages.js；峰值可用 LOAD_VUS 覆盖）
const profile = envStr('LOAD_PROFILE', 'low').toLowerCase();

// PRODUCT_ID 可选：提供则额外压 /products/:id（走单行查询 + SKU 关联），
// 不提供则只压列表页。ID 由 tools/prepare-data.js 产出后通过环境变量传入。
const productId = envInt('PRODUCT_ID', 0);

// 每次迭代后的思考时间，避免 VU 以纯忙等方式刷服务端（真实用户有间隔）。
// 用它而不是「无间隔循环」，是为了让「VU 数 → RPS」的换算更接近真实流量形状。
const thinkTimeMs = envInt('THINK_TIME_MS', 200);

export const options = {
  stages: buildStages(profile),
  tags: { scenario: 'read-load' },
  // 这里只保留框架级阈值。P95/P99 的性能目标必须由 T5-02 依据实测基线确定，
  // 提前写死一个数字会让「未验证的猜测」看起来像验收标准。
  thresholds: mergeThresholds(),
};

export default function () {
  // 列表页：分页参数固定，避免不同参数造成缓存/索引命中差异，影响可比性。
  const list = biz.listProducts(client, 'page=1&size=20');
  expectHttpOk(list);
  expectEnvelopeOk(list);

  if (productId > 0) {
    const detail = biz.getProduct(client, productId);
    expectHttpOk(detail);
    expectEnvelopeOk(detail);
  }

  sleep(thinkTimeMs / 1000);
}

export const handleSummary = makeHandleSummary(cfg, {
  profile: profile,
  product_id: productId,
  think_time_ms: thinkTimeMs,
});
