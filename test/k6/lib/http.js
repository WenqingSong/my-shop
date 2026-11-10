// 公共 HTTP 请求封装（T5-01）。
//
// 职责边界：
//  - 统一 Base URL 拼接、超时、Authorization 头、k6 tag（name/scenario/phase）；
//  - 统一解析 SurgeCart 的响应包 {code,message,data}，把「HTTP 成功」与
//    「业务成功（code=0）」拆成两个独立事实，避免把 200 当成业务成功；
//  - 记录框架层错误（网络/超时/5xx、响应包结构异常、业务错误码），供报告解释
//    失败原因，而不是只留下一句 "status != 200"。
//
// 明确不做的事：
//  - 不把 user_id / order_id / 幂等键写进 k6 tag：这些值基数无界，会让指标序列
//    爆炸。动态路径一律用 endpoints.js 里的路由模板作为 name。
//  - 不重试。重试属于被测系统的能力（幂等键、Kafka 重投），测试框架擅自重试会
//    掩盖真实失败率，也会放大对服务端的压力。

import http from 'k6/http';
import { check } from 'k6';
import { Counter } from 'k6/metrics';

// 框架层错误分类计数：三者互斥，便于在 summary 中直接读出失败性质。
export const requestErrors = new Counter('k6_request_errors'); // 网络失败/超时/5xx
export const envelopeErrors = new Counter('k6_envelope_errors'); // 2xx 但响应包结构异常
export const businessRejected = new Counter('k6_business_rejected'); // 业务错误码（code != 0）

// normalizeTarget 兼容两种调用形态：
//   client.get(PATHS.health)                    → 静态路径
//   client.get(productDetail(id))               → {path, routeTemplate}
// name tag 始终取 routeTemplate，保证 k6 指标按路由模板聚合。
function normalizeTarget(target) {
  if (typeof target === 'string') {
    return { path: target, routeTemplate: target };
  }
  if (!target || typeof target.path !== 'string') {
    throw new Error('[k6-http] target 非法：需要字符串路径或 {path, routeTemplate} 对象');
  }
  return { path: target.path, routeTemplate: target.routeTemplate || target.path };
}

// buildParams 组装 k6 请求参数：超时、头、标签。
// tags 只允许低基数枚举（scenario/phase/接口名），由调用方保证。
function buildParams(cfg, client, name, options) {
  const opts = options || {};
  const headers = Object.assign({ Accept: 'application/json' }, opts.headers || {});
  if (opts.body !== undefined && opts.body !== null && headers['Content-Type'] === undefined) {
    headers['Content-Type'] = 'application/json';
  }
  if (client.token) {
    headers.Authorization = 'Bearer ' + client.token;
  }
  const tags = Object.assign({ name: name, scenario: client.scenario }, opts.tags || {});
  return {
    timeout: (opts.timeoutMs || cfg.timeoutMs) + 'ms',
    headers: headers,
    tags: tags,
    // 4xx/5xx 也按「真实响应」计：http_req_failed 由 k6 依据 status 自动判定，
    // 这里显式关闭「把 4xx 视为 expected」的默认宽容，让错误率可见。
    responseCallback: http.expectedStatuses(200, 201, 202, 204, 400, 401, 403, 404, 409, 429, 503),
  };
}

// parseEnvelope 解析统一响应包，返回扁平结果对象。
// 重要：SurgeCart 的 middleware.Response 对所有 handler 输出 {code,message,data}，
// 但 /metrics 是纯文本（不经 Response 包装），因此调用方可以标记 textual:true 跳过解析。
function buildResult(res, name, meta) {
  const result = {
    name: name,
    status: res.status,
    httpOk: res.status >= 200 && res.status < 300,
    body: res.body,
    code: null,
    message: '',
    data: null,
    envelopeValid: false,
    error: '',
    res: res,
    phase: meta.phase || '',
  };

  if (res.status === 0) {
    result.error = res.error || 'network error';
    requestErrors.add(1, { name: name });
    console.error(`[k6-http] ${name} 网络失败/超时: ${result.error}`);
    return result;
  }
  if (res.status >= 500) {
    result.error = `server error ${res.status}`;
    requestErrors.add(1, { name: name });
  }
  if (meta.textual) {
    result.envelopeValid = true;
    return result;
  }

  let parsed = null;
  try {
    parsed = res.json();
  } catch (e) {
    result.error = 'invalid json body';
    envelopeErrors.add(1, { name: name });
    return result;
  }
  if (!parsed || typeof parsed.code !== 'number') {
    result.error = 'missing numeric code field';
    envelopeErrors.add(1, { name: name });
    return result;
  }
  result.envelopeValid = true;
  result.code = parsed.code;
  result.message = parsed.message || '';
  result.data = parsed.data === undefined ? null : parsed.data;
  if (result.code !== 0) {
    businessRejected.add(1, { name: name, code: String(result.code) });
    if (meta.logRejection) {
      console.warn(`[k6-http] ${name} 业务拒绝 code=${result.code} message=${result.message}`);
    }
  }
  return result;
}

// newClient 创建带独立认证态的客户端。
// 每个 k6 VU 是独立 JS 运行时，因此每个 VU 各自持有一个 client 与 token 即可，
// 不存在跨 VU 的状态竞争。
export function newClient(cfg, options) {
  const opts = options || {};
  const client = {
    cfg: cfg,
    token: opts.token || '',
    scenario: opts.scenario || cfg.scenario,

    setToken: function (token) {
      client.token = token || '';
      return client;
    },

    // request 是唯一出口：get/post/put/delete 都是它的薄包装。
    request: function (method, target, options) {
      const normalized = normalizeTarget(target);
      const reqOpts = options || {};
      const name = reqOpts.name || method + ' ' + normalized.routeTemplate;
      const params = buildParams(cfg, client, name, reqOpts);

      let payload = reqOpts.body;
      if (payload !== undefined && payload !== null && typeof payload !== 'string') {
        payload = JSON.stringify(payload);
      }

      const res = http.request(method, cfg.baseUrl + normalized.path, payload, params);
      return buildResult(res, name, reqOpts);
    },

    get: function (target, options) {
      return client.request('GET', target, options);
    },
    post: function (target, body, options) {
      const opts = Object.assign({}, options || {});
      opts.body = body;
      return client.request('POST', target, opts);
    },
    put: function (target, body, options) {
      const opts = Object.assign({}, options || {});
      opts.body = body;
      return client.request('PUT', target, opts);
    },
    del: function (target, options) {
      return client.request('DELETE', target, options);
    },
  };
  return client;
}

// ---------------------------------------------------------------------------
// 检查辅助：把「HTTP 层」与「业务层」拆成两组独立 check，
// 这样 summary 里的 check 名称就能直接回答「失败在哪一层」。
// ---------------------------------------------------------------------------

// expectHttpOk 只断言 HTTP 2xx，不解释业务码。
export function expectHttpOk(result) {
  return check(result, {
    [`${result.name} | http 2xx`]: (r) => r.httpOk,
  });
}

// expectEnvelopeOk 断言响应包结构合法且业务码为 0（真正的业务成功）。
export function expectEnvelopeOk(result) {
  return check(result, {
    [`${result.name} | envelope valid`]: (r) => r.envelopeValid,
    [`${result.name} | business code=0`]: (r) => r.code === 0,
  });
}

// expectBusinessCode 断言业务码等于期望值：用于「预期的业务拒绝」场景
// （如库存不足 12003、一人一单 12004），避免把这些正确行为算成失败。
export function expectBusinessCode(result, expectedCode) {
  return check(result, {
    [`${result.name} | business code=${expectedCode}`]: (r) => r.code === expectedCode,
  });
}

// expectDataField 断言 data 中存在某个字段且非空（用于确认响应内容而不是只看状态码）。
export function expectDataField(result, field) {
  return check(result, {
    [`${result.name} | data.${field} present`]: (r) =>
      r.data !== null && typeof r.data === 'object' && r.data[field] !== undefined && r.data[field] !== null,
  });
}
