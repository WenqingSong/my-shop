// 负载档位与 k6 options 构造（T5-01 预留，T5-02 使用）。
//
// 重要边界：本文件里的数值是「框架默认值」，不是任何容量结论。
// T5-01 没有执行任何负载测试，也没有测出系统能支撑多少 QPS；
// 这些档位只是给出可被环境变量覆盖的结构，T5-02 必须结合本机 CPU/内存实测再定稿。
//
// k6 stage 语义（顺序执行，target 是 VU 数而不是 RPS）：
//   { duration: '60s', target: 20 }  → 在 60s 内把 VU 从上一阶段值线性拉到 20（ramp-up）
//   { duration: '180s', target: 20 } → 保持 20 VU（持续负载）
//   { duration: '30s', target: 0 }   → 线性降到 0（ramp-down）
// 因此「目标 QPS」要通过 VU 数 × 单 VU 吞吐间接达成，这也是 T5-02 需要同时看
// 服务端 RPS 指标的原因：客户端 VU 数不等于服务端实际吞吐。

import { envInt } from '../config/default.js';

// 三档负载的默认形状。总时长刻意保持较短，避免在本地单机 kind 上跑出
// 与机器资源无关的结论。
export const PROFILES = {
  low: [
    { duration: '30s', target: 5 },
    { duration: '120s', target: 5 },
    { duration: '15s', target: 0 },
  ],
  medium: [
    { duration: '60s', target: 20 },
    { duration: '180s', target: 20 },
    { duration: '30s', target: 0 },
  ],
  high: [
    { duration: '90s', target: 50 },
    { duration: '240s', target: 50 },
    { duration: '30s', target: 0 },
  ],
};

// buildStages 返回按档位缩放的 stage 列表。
// LOAD_PROFILE 选择档位；LOAD_VUS 覆盖该档位的峰值 VU（保持 ramp 时长不变），
// 用于 T5-02 在同一台机器上做细粒度扫描。
export function buildStages(profile) {
  const name = (profile || 'low').toLowerCase();
  const base = PROFILES[name];
  if (!base) {
    throw new Error(
      `[k6-stages] 未知负载档位 LOAD_PROFILE=${profile}，可选值：${Object.keys(PROFILES).join(' / ')}`
    );
  }
  const peak = envInt('LOAD_VUS', 0);
  if (peak <= 0) {
    return base;
  }
  // T5-02 修正（实测发现的缺陷）：k6 的 stage target 是「该阶段结束时的 VU 数」，
  // k6 会在整个 duration 内线性过渡。原实现把 ramp 阶段设成 peak/4，于是
  // 「持续负载」阶段变成从 peak/4 爬到 peak 的 120s 慢爬坡——全程没有稳态窗口，
  // Prometheus 只能看到一条单调上升的曲线，无法做「同一负载下的稳态对比」。
  // 修正后：ramp 阶段直接到达 peak，持续阶段 target 与当前值相同 → 真正的保持。
  return base.map(function (stage, index) {
    if (index === 0 || index === 1) {
      return { duration: stage.duration, target: peak };
    }
    return stage;
  });
}

// baseThresholds 给出框架级最小阈值：只拦住「明显坏掉」的情况，
// 不预设业务性能目标。T5-02 依据实测基线再收紧 p95/p99。
export function baseThresholds() {
  return {
    // 框架层检查必须全过：否则说明脚本/契约/环境本身有问题。
    checks: ['rate==1'],
    // 5xx 与网络失败计入 k6_request_errors；这里要求为 0，
    // 业务拒绝（4xx + 业务码）不计入该指标。
    k6_request_errors: ['count==0'],
    // 响应包结构异常必须为 0：出现即说明契约漂移，结果不可信。
    k6_envelope_errors: ['count==0'],
  };
}

// mergeThresholds 合并默认与场景自定义阈值（后者覆盖同名键）。
export function mergeThresholds(custom) {
  const merged = baseThresholds();
  const overrides = custom || {};
  Object.keys(overrides).forEach(function (key) {
    merged[key] = overrides[key];
  });
  return merged;
}
