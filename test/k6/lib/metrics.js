// Prometheus 文本格式解析与指标族断言（T5-01）。
//
// 为什么要自己解析 /metrics：
//  - Smoke 测试除了「HTTP 200」，还必须回答「关键指标是否真的被导出」。只看状态码
//    无法发现「指标开关被关掉」或「指标被改名」这类静默退化；
//  - k6 不能在 VU 阶段随意调用外部库，也不适合引入 Prometheus 客户端，因此用
//    最小解析器读取 # TYPE/# HELP 行与样本行即可。
//
// 解析范围：只覆盖 Prometheus 文本格式中本项目实际会用到的部分（单行样本、
// 花括号标签、无 exemplar），不实现完整规范，避免测试基础设施比被测代码更复杂。

// parseMetricsText 解析为 name -> { type, help, samples: [{labels, value}] }
export function parseMetricsText(text) {
  const families = {};
  const lines = String(text).split('\n');
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].trim();
    if (line === '') {
      continue;
    }
    if (line.indexOf('# TYPE ') === 0) {
      const parts = line.split(/\s+/);
      const name = parts[2];
      families[name] = families[name] || { type: '', help: '', samples: [] };
      families[name].type = parts[3] || '';
      continue;
    }
    if (line.indexOf('# HELP ') === 0) {
      const rest = line.slice('# HELP '.length);
      const spaceAt = rest.indexOf(' ');
      const name = spaceAt < 0 ? rest : rest.slice(0, spaceAt);
      families[name] = families[name] || { type: '', help: '', samples: [] };
      families[name].help = spaceAt < 0 ? '' : rest.slice(spaceAt + 1);
      continue;
    }
    if (line.charAt(0) === '#') {
      continue;
    }
    const sample = parseSampleLine(line);
    if (sample === null) {
      continue;
    }
    families[sample.name] = families[sample.name] || { type: '', help: '', samples: [] };
    families[sample.name].samples.push({ labels: sample.labels, value: sample.value });
  }
  return families;
}

// parseSampleLine 解析一行样本：metric{a="b",c="d"} 12.5
function parseSampleLine(line) {
  const braceAt = line.indexOf('{');
  if (braceAt < 0) {
    const parts = line.split(/\s+/);
    if (parts.length < 2) {
      return null;
    }
    return { name: parts[0], labels: {}, value: Number(parts[1]) };
  }
  const name = line.slice(0, braceAt);
  const closeAt = line.lastIndexOf('}');
  if (closeAt < braceAt) {
    return null;
  }
  const labels = parseLabelSet(line.slice(braceAt + 1, closeAt));
  const rest = line.slice(closeAt + 1).trim().split(/\s+/);
  if (rest.length < 1 || rest[0] === '') {
    return null;
  }
  return { name: name, labels: labels, value: Number(rest[0]) };
}

// parseLabelSet 解析 a="b",c="d"：只处理双引号包裹的取值（本项目指标标签都是枚举）。
function parseLabelSet(raw) {
  const labels = {};
  let i = 0;
  while (i < raw.length) {
    const eqAt = raw.indexOf('=', i);
    if (eqAt < 0) {
      break;
    }
    const key = raw.slice(i, eqAt).trim();
    let cursor = eqAt + 1;
    while (cursor < raw.length && raw.charAt(cursor) === ' ') {
      cursor++;
    }
    if (raw.charAt(cursor) !== '"') {
      break;
    }
    cursor++;
    let value = '';
    while (cursor < raw.length) {
      const ch = raw.charAt(cursor);
      if (ch === '\\') {
        value += raw.charAt(cursor + 1);
        cursor += 2;
        continue;
      }
      if (ch === '"') {
        cursor++;
        break;
      }
      value += ch;
      cursor++;
    }
    labels[key] = value;
    const commaAt = raw.indexOf(',', cursor);
    if (commaAt < 0) {
      break;
    }
    i = commaAt + 1;
  }
  return labels;
}

// hasFamily 判断指标族是否存在（TYPE/HELP 行或样本行任一出现即算存在）。
export function hasFamily(families, name) {
  return Object.prototype.hasOwnProperty.call(families, name);
}

// findSample 按标签过滤取第一条样本，用于确认「指标不只是存在，而且标签符合契约」。
// 例：findSample(families, 'surgecart_sampler_up', { sampler: 'kafka_lag' })
export function findSample(families, name, expectedLabels) {
  const family = families[name];
  if (!family) {
    return null;
  }
  for (let i = 0; i < family.samples.length; i++) {
    const sample = family.samples[i];
    let matched = true;
    const keys = Object.keys(expectedLabels || {});
    for (let k = 0; k < keys.length; k++) {
      if (sample.labels[keys[k]] !== expectedLabels[keys[k]]) {
        matched = false;
        break;
      }
    }
    if (matched) {
      return sample;
    }
  }
  return null;
}

// REQUIRED_FAMILIES 与 dispatch_mode 无关，任何开启指标的应用都必须导出。
// 依据：internal/metrics/application.go 与 Prometheus client_golang 默认 Collector。
export const REQUIRED_FAMILIES = [
  'go_goroutines',
  'go_memstats_heap_alloc_bytes',
  'process_cpu_seconds_total',
  'surgecart_http_requests_total',
  'surgecart_http_request_duration_seconds',
  'surgecart_http_requests_in_flight',
  'surgecart_sampler_up',
];

// KAFKA_MODE_FAMILIES 只在 dispatch_mode=kafka 时必然存在：
// Kafka Lag 采样器与 Outbox 采样器只在 Kafka 链路启动后才运行。
export const KAFKA_MODE_FAMILIES = [
  'surgecart_kafka_consumer_lag',
  'flashsale_outbox_backlog_pending',
  'surgecart_outbox_oldest_age_seconds',
];

// checkFamilies 返回缺失清单；由场景决定缺失即失败还是仅告警，避免解析器越权判断。
export function checkFamilies(families, expected) {
  const missing = [];
  for (let i = 0; i < expected.length; i++) {
    if (!hasFamily(families, expected[i])) {
      missing.push(expected[i]);
    }
  }
  return missing;
}
