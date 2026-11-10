// k6 结果汇总输出（T5-01）。
//
// 为什么不用 jslib.k6.io 的 k6-summary：
//  - 它需要联网 import，本地/离线环境会直接失败，而结果输出是「必须成功」的环节；
//  - 本框架只需要少量关键指标 + 元数据，自绘更可控，也避免外部依赖的版本漂移。
//
// 输出两类文件（路径由 K6_SUMMARY_DIR 决定，容器内默认 /results）：
//   <run_id>.summary.json —— 结构化结果，后续报告/图表直接引用
//   <run_id>.summary.txt  —— 人读摘要，便于 CI 日志与终端回看
// 原始 JSON Lines（--out json）体积大且默认关闭，只按需导出，见 README §结果管理。

// 需要原样保留的关键指标（其余同名子指标按前缀/后缀规则保留）。
const KEY_METRICS = [
  'checks',
  'http_reqs',
  'http_req_failed',
  'http_req_duration',
  'http_req_waiting',
  'http_req_blocked',
  'http_req_connecting',
  'iteration_duration',
  'iterations',
  'vus_max',
  'data_received',
  'data_sent',
];

// shouldKeep 判定某条指标是否进入 summary：
//  - 框架自定义指标（k6_ 前缀）全保留；
//  - 关键指标本身的聚合值与带 tag 的子指标（如 http_req_duration{name:...}）保留；
//  - 其余（大量 http_req_tls_handshaking 之类的固定为 0 的指标）丢弃，控制文件体积。
function shouldKeep(name) {
  if (name.indexOf('k6_') === 0) {
    return true;
  }
  for (let i = 0; i < KEY_METRICS.length; i++) {
    if (name === KEY_METRICS[i] || name.indexOf(KEY_METRICS[i] + '{') === 0) {
      return true;
    }
  }
  return false;
}

// collectThresholds 汇总阈值判定结果，供报告直接引用「哪些阈值未通过」。
function collectThresholds(metrics) {
  const passed = [];
  const failed = [];
  Object.keys(metrics).forEach(function (name) {
    const item = metrics[name];
    if (!item || !item.thresholds) {
      return;
    }
    Object.keys(item.thresholds).forEach(function (expr) {
      const entry = name + ' ' + expr;
      if (item.thresholds[expr] && item.thresholds[expr].ok) {
        passed.push(entry);
      } else {
        failed.push(entry);
      }
    });
  });
  return { passed: passed, failed: failed };
}

// buildSummary 把 k6 的 data 转成「元数据 + 指标 + 阈值」三段式结构。
export function buildSummary(data, cfg, extra) {
  const metrics = {};
  Object.keys(data.metrics).forEach(function (name) {
    if (!shouldKeep(name)) {
      return;
    }
    const item = data.metrics[name];
    metrics[name] = { type: item.type, values: item.values };
    if (item.thresholds) {
      metrics[name].thresholds = item.thresholds;
    }
  });

  const thresholds = collectThresholds(data.metrics);

  return {
    meta: Object.assign(
      {
        run_id: cfg.runId,
        scenario: cfg.scenario,
        test_env: cfg.testEnv,
        base_url: cfg.baseUrl,
        dispatch_mode: cfg.dispatchMode,
        data_prefix: cfg.dataPrefix,
        // 只记录「summary 导出时刻」与 k6 自报的测试时长。
        // 为什么不记录测试开始时刻：handleSummary 可能运行在独立的 JS 运行时，
        // 模块级时间戳会被重新初始化，写出来的开始时间会比真实开始时间晚，
        // 用它去对齐 Prometheus 窗口会漏掉前半段。权威时间窗以 runner 写出的
        // <run_id>.meta.json（started_at_utc / finished_at_utc）为准。
        summary_exported_at: new Date().toISOString(),
        test_run_duration_ms: data.state ? data.state.testRunDurationMs : null,
      },
      extra || {}
    ),
    thresholds: { passed: thresholds.passed, failed: thresholds.failed },
    metrics: metrics,
  };
}

// padRight 用空格右侧补齐（不用 padEnd：Goja 对 ES2017 覆盖率不保证，保持保守写法）。
function padRight(value, width) {
  let out = String(value);
  while (out.length < width) {
    out += ' ';
  }
  return out;
}

// fmt 把指标值格式化：计数保留整数，比率/耗时保留 4 位小数。
function fmt(value) {
  if (typeof value !== 'number' || !isFinite(value)) {
    return String(value);
  }
  if (Math.abs(value) >= 1000 && Math.floor(value) === value) {
    return String(value);
  }
  return value.toFixed(4);
}

// buildTextSummary 生成人读摘要：只列关键行，避免在 CI 日志里刷屏。
export function buildTextSummary(summary) {
  const lines = [];
  const meta = summary.meta;
  lines.push('=== SurgeCart k6 summary ===');
  lines.push('run_id      : ' + meta.run_id);
  lines.push('scenario    : ' + meta.scenario);
  lines.push('test_env    : ' + meta.test_env);
  lines.push('base_url    : ' + meta.base_url);
  lines.push('exported_at : ' + meta.summary_exported_at);
  lines.push('duration_ms : ' + meta.test_run_duration_ms);
  lines.push('');
  lines.push('--- key metrics ---');

  const priority = [
    'checks',
    'http_reqs',
    'http_req_failed',
    'http_req_duration',
    'iterations',
    'vus_max',
    'k6_request_errors',
    'k6_envelope_errors',
    'k6_business_rejected',
    'k6_biz_outcomes',
  ];
  priority.forEach(function (name) {
    const metric = summary.metrics[name];
    if (!metric) {
      return;
    }
    const values = metric.values || {};
    const parts = [];
    Object.keys(values).forEach(function (key) {
      parts.push(key + '=' + fmt(values[key]));
    });
    lines.push(padRight(name, 24) + ': ' + parts.join(' '));
  });

  lines.push('');
  lines.push('--- thresholds ---');
  if (summary.thresholds.failed.length === 0 && summary.thresholds.passed.length === 0) {
    lines.push('(no thresholds declared)');
  }
  summary.thresholds.passed.forEach(function (item) {
    lines.push('PASS ' + item);
  });
  summary.thresholds.failed.forEach(function (item) {
    lines.push('FAIL ' + item);
  });
  lines.push('');
  return lines.join('\n');
}

// makeHandleSummary 生成场景脚本的 handleSummary 回调。
// extra 用于场景补充自己的元数据（如负载档位、目标 VU）。
export function makeHandleSummary(cfg, extra) {
  return function (data) {
    const summary = buildSummary(data, cfg, extra);
    const base = cfg.summaryDir + '/' + cfg.runId;
    const out = {};
    out[base + '.summary.json'] = JSON.stringify(summary, null, 2);
    out[base + '.summary.txt'] = buildTextSummary(summary);
    return out;
  };
}
