// Smoke 测试（T5-01）：验证「测试框架 + 被测环境」这条链路本身是通的。
//
// 定位（必须明确，否则会被误读成性能结论）：
//  - 这不是性能测试：1 VU、3 次迭代、总请求量个位数，不产生任何 QPS 结论；
//  - 不产生业务副作用：只调用 /health、/ready、/metrics，不下单、不改库存、
//    不创建活动、不注册用户；
//  - 目的是尽早暴露「环境没起来 / 端口转发没开 / 指标开关被关掉 / 指标被改名」
//    这类会让后续 T5-02~T5-04 全部白跑的问题。
//
// 覆盖的三层事实：
//  1. HTTP 层：状态码 2xx、响应可解析；
//  2. 契约层：响应包 {code:0,message,data} 符合 middleware.Response 的约定；
//  3. 指标层：/metrics 里必须存在的指标族确实被导出（按 dispatch_mode 分级）。

import { check } from 'k6';
import { loadConfig, printConfig } from '../config/default.js';
import { newClient, expectHttpOk, expectEnvelopeOk, expectDataField } from '../lib/http.js';
import * as biz from '../lib/business.js';
import {
  parseMetricsText,
  checkFamilies,
  REQUIRED_FAMILIES,
  KAFKA_MODE_FAMILIES,
  findSample,
} from '../lib/metrics.js';
import { makeHandleSummary } from '../lib/report.js';

// 配置在 init 阶段解析：配置错误会让 k6 在启动前就失败，不会跑出「假通过」。
const cfg = loadConfig({ scenario: 'smoke' });
printConfig(cfg);

const client = newClient(cfg);

export const options = {
  // 单 VU + 少量迭代：这是一次「连通性 + 契约 + 指标」巡检，不是压测。
  vus: 1,
  iterations: 3,
  // 全局 tag：所有 k6 指标都带 scenario=smoke，便于在同一结果库里区分批次。
  tags: { scenario: 'smoke' },
  thresholds: {
    // 任何一条 check 失败即判定 smoke 失败：smoke 的意义就是全绿或明确报错。
    checks: ['rate==1'],
    k6_request_errors: ['count==0'],
    k6_envelope_errors: ['count==0'],
  },
};

// checkMetricsFamilies 依据 dispatch_mode 分级断言指标族。
// 为什么分级：Kafka Lag 与 Outbox 采样只在 dispatch_mode=kafka 时启动，
// 在 db_scanner 模式下这些序列本来就不存在，硬性要求会把正确行为判成失败。
function checkMetricsFamilies(body) {
  const families = parseMetricsText(body);
  const missingRequired = checkFamilies(families, REQUIRED_FAMILIES);

  check(missingRequired, {
    'GET /metrics | 必需指标族齐全': (missing) => missing.length === 0,
  });
  if (missingRequired.length > 0) {
    console.error('[smoke] /metrics 缺少必需指标族: ' + missingRequired.join(', '));
  }

  if (cfg.dispatchMode === 'kafka') {
    const missingKafka = checkFamilies(families, KAFKA_MODE_FAMILIES);
    check(missingKafka, {
      'GET /metrics | kafka 模式指标族齐全': (missing) => missing.length === 0,
    });
    if (missingKafka.length > 0) {
      console.error('[smoke] dispatch_mode=kafka 但缺少指标族: ' + missingKafka.join(', '));
    }
    // 采样器健康：1=最近一次采样成功。0 表示采集链路有病，指标值不可信。
    ['kafka_lag', 'outbox'].forEach(function (sampler) {
      const sample = findSample(families, 'surgecart_sampler_up', { sampler: sampler });
      check(sample, {
        [`GET /metrics | sampler_up{${sampler}} = 1`]: (s) => s !== null && s.value === 1,
      });
    });
  } else if (cfg.dispatchMode === 'db_scanner') {
    // db_scanner 模式：Kafka/Outbox 序列本就不存在，只记录事实，不判失败。
    const present = KAFKA_MODE_FAMILIES.filter(function (name) {
      return checkFamilies(families, [name]).length === 0;
    });
    console.log(
      '[smoke] dispatch_mode=db_scanner，Kafka/Outbox 指标族按设计不导出；实际存在: ' +
        (present.length === 0 ? '(none)' : present.join(', '))
    );
  } else {
    // 未声明 dispatch_mode：只输出观测结果，避免用猜测的期望值判定失败。
    const present = KAFKA_MODE_FAMILIES.filter(function (name) {
      return checkFamilies(families, [name]).length === 0;
    });
    console.log(
      '[smoke] EXPECT_DISPATCH_MODE=unknown，跳过 kafka 指标族强制断言；实际存在: ' +
        (present.length === 0 ? '(none)' : present.join(', '))
    );
  }

  return families;
}

export default function () {
  // 1) 存活探针：进程活着。
  const health = biz.getHealth(client);
  expectHttpOk(health);
  expectEnvelopeOk(health);
  expectDataField(health, 'status');
  check(health, {
    'GET /health | data.status=ok': (r) => r.data !== null && r.data.status === 'ok',
    'GET /health | data.time 非空': (r) => r.data !== null && typeof r.data.time === 'string' && r.data.time.length > 0,
  });

  // 2) 就绪探针：MySQL 与 Redis 可达（服务端会缓存就绪结果，非每次都打依赖）。
  const ready = biz.getReady(client);
  expectHttpOk(ready);
  expectEnvelopeOk(ready);
  check(ready, {
    'GET /ready | data.status=ready': (r) => r.data !== null && r.data.status === 'ready',
  });

  // 3) 指标端点：必须是 Prometheus 文本格式，且关键指标族存在。
  const metrics = biz.getMetricsText(client);
  expectHttpOk(metrics);
  check(metrics, {
    'GET /metrics | 是 Prometheus 文本': (r) =>
      typeof r.body === 'string' && r.body.indexOf('# HELP') >= 0 && r.body.indexOf('# TYPE') >= 0,
    'GET /metrics | 包含 http 请求计数样本': (r) =>
      typeof r.body === 'string' && r.body.indexOf('surgecart_http_requests_total') >= 0,
  });
  if (metrics.httpOk && typeof metrics.body === 'string') {
    checkMetricsFamilies(metrics.body);
  }
}

// 结果落盘：results/<run_id>.summary.{json,txt}
export const handleSummary = makeHandleSummary(cfg, {
  profile: 'smoke',
  smoke_iterations: 3,
  note: 'smoke 只验证框架与环境连通性，不构成任何性能结论',
});
