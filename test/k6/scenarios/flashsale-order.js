// 秒杀下单场景（T5-03 正确性验证 / T5-04 故障恢复入口）。
//
// 状态：T5-03 已执行（实验 A/B/C），T5-04 未执行。
//
// 为什么单独一层封装而不是在 T5-03 里现写：
//   T5-03（正确性）与 T5-04（故障恢复）对秒杀请求的要求高度重合：多用户并发、
//   独立账号、唯一幂等键、请求时刻记录、错误分类。把这层沉到 lib/business.js +
//   本场景的 options 里，后续两个任务只改参数，不再复制粘贴请求逻辑。
//
// 必须区分的事实（本场景的输出就是这四类）：
//   1. 请求已受理           → outcome=accepted（queued，尚未成交）
//   2. 幂等重放导致的既有状态 → outcome=idempotent_replay_*
//   3. 业务拒绝（库存/一人一单/限流…）→ outcome=stock_insufficient 等固定枚举
//   4. 系统侧不确定失败     → outcome=transport_error/internal_error/service_unavailable
//   只有经结果查询确认为 success 且带订单，才算「业务成功」。
//
// 关键环境变量：
//   ACTIVITY_ID / SKU_ID   已准备好的秒杀活动与绑定 SKU（由 tools/prepare-data.js 产出）
//   FLASH_USERS            参与的测试用户数（默认 10；每个用户独立注册登录）
//   K6_TEST_PASSWORD       测试用户密码（必需，不落仓库）
//   FLASH_IDEMPOTENCY_REPLAY=true  同一用户每轮复用同一幂等键（测幂等/重复提交）
//   FLASH_VERIFY=true      提交后轮询结果查询（会改变请求形状，仅正确性场景使用）
//
// T5-03 新增的三个正确性开关（默认值保持 T5-02 及以前的行为不变）：
//   FLASH_EXECUTOR=stages|once
//     stages：按 lib/stages.js 持续压（T5-02 负载用）。
//     once  ：per-vu-iterations，每个 VU 固定跑 FLASH_ITERATIONS 次后退出。
//             这是「每个用户只发一次请求」的正确性实验形态：请求数 = VU 数 × 迭代数，
//             完全确定，不受时长影响。
//   FLASH_SHARED_USER=true
//     所有 VU 复用同一个测试账号（users[0]）。用于「一人一单」与「请求幂等」两类实验。
//   FLASH_FIXED_KEY=<key>
//     所有请求使用同一个字面量幂等键。配合 FLASH_SHARED_USER 即可构造
//     「同一用户 + 同一逻辑请求」的并发重放。
//   FLASH_TRACE=true
//     每次请求输出一行结构化日志（VU/用户/幂等键/业务码/分类），用于逐条回溯证据。

import { sleep } from 'k6';
// k6/execution 的默认导出才是 execution 对象；写成命名导入 { execution } 会得到
// undefined，运行期报 "Cannot read property 'vu' of undefined"（本任务实测踩到）。
import exec from 'k6/execution';
import { check } from 'k6';
import { Trend } from 'k6/metrics';
import { loadConfig, printConfig, envInt, envBool, envStr } from '../config/default.js';
import { newClient, expectHttpOk } from '../lib/http.js';
import * as biz from '../lib/business.js';
import { provisionUsers } from '../lib/auth.js';
import { buildStages, mergeThresholds } from '../lib/stages.js';
import { makeHandleSummary } from '../lib/report.js';

const cfg = loadConfig({ scenario: 'flashsale-order' });
printConfig(cfg);

// 秒杀参数在这里只读取、默认 0；真正的「必须提供」校验放在 setup()。
// 原因：k6 inspect 会执行 init 上下文，若 init 阶段 throw，静态检查就无法进行。
const activityId = envInt('ACTIVITY_ID', 0);
const skuId = envInt('SKU_ID', 0);

const users = envInt('FLASH_USERS', 10);
const replayKey = envBool('FLASH_IDEMPOTENCY_REPLAY', false);
const verify = envBool('FLASH_VERIFY', false);
const verifyAttempts = envInt('FLASH_POLL_ATTEMPTS', 5);
const verifyIntervalMs = envInt('FLASH_POLL_INTERVAL_MS', 500);
const profile = envStr('LOAD_PROFILE', 'low').toLowerCase();

// T5-03 正确性实验开关。
const executor = envStr('FLASH_EXECUTOR', 'stages').toLowerCase();
if (executor !== 'stages' && executor !== 'once') {
  throw new Error('[k6-flashsale] FLASH_EXECUTOR 只支持 stages / once，收到: ' + executor);
}
const iterationsPerVU = envInt('FLASH_ITERATIONS', 1);
// FLASH_VUS：once 形态下的并发数。默认等于用户数（每个用户一个 VU）；
//   「一人一单」「请求幂等」实验只有一个账号，但需要多个并发 VU，
//   因此必须把「账号数」与「并发数」解耦。
const vus = envInt('FLASH_VUS', users);
const sharedUser = envBool('FLASH_SHARED_USER', false);
const fixedKey = envStr('FLASH_FIXED_KEY', '');
const trace = envBool('FLASH_TRACE', false);
const maxDuration = envStr('FLASH_MAX_DURATION', '3m');

if (fixedKey !== '' && !sharedUser) {
  // 固定幂等键 + 多用户会命中「幂等键冲突(12005)」而不是幂等重放，属于用错场景。
  // 这里直接失败，避免跑出一份看似成功但语义错误的实验。
  throw new Error('[k6-flashsale] FLASH_FIXED_KEY 必须与 FLASH_SHARED_USER=true 一起使用（同一用户同一请求）');
}

// 端到端落单耗时：从提交到观察到最终状态（仅 FLASH_VERIFY=true 时有效）。
const finalStateLatency = new Trend('k6_flash_order_final_state_ms', true);

// 视为「系统侧未给出确定性业务结论」的下单结果：这些才是失败，
// 库存不足/一人一单/限流都是系统按规则给出的正确回答。
const UNEXPLAINED_OUTCOMES = [
  'internal_error',
  'service_unavailable',
  'biz_error',
  'accepted_unknown_status',
];

// 两种执行形态：
//  - stages：负载形态（T5-02 风格，VU 数随时长变化）；
//  - once  ：正确性形态，VU 数 == 并发用户数，每个 VU 只跑固定次数后退出。
//    per-vu-iterations 的总请求数是确定的（vus × iterations），
//    这对「库存 20 / 40 个用户」这类需要精确对照的业务实验是必要条件：
//    用 stages 会因为时长内反复迭代而发出远多于预期的请求。
export const options = executor === 'once'
  ? {
      scenarios: {
        once: {
          executor: 'per-vu-iterations',
          vus: vus,
          iterations: iterationsPerVU,
          maxDuration: maxDuration,
        },
      },
      tags: { scenario: 'flashsale-order' },
      thresholds: mergeThresholds(),
    }
  : {
      stages: buildStages(profile),
      tags: { scenario: 'flashsale-order' },
      thresholds: mergeThresholds(),
    };

// setup 只执行一次：顺序注册并登录 FLASH_USERS 个测试用户，供各 VU 轮流使用。
// 注意：bcrypt 成本较高（每用户 register+login 约 120~300ms），用户数不宜无限放大；
// T5-03 若需要大量用户，应固定 RUN_ID 与 K6_TEST_PASSWORD 复用已有账号。
export function setup() {
  if (activityId <= 0 || skuId <= 0) {
    throw new Error(
      '[k6-flashsale] 缺少 ACTIVITY_ID / SKU_ID。先执行 tools/prepare-data.js 准备独立测试活动，' +
        '再用 -e ACTIVITY_ID=<id> -e SKU_ID=<id> 传入（禁止直接复用正常业务活动做高并发压测）。'
    );
  }
  const setupClient = newClient(cfg);
  const provisioned = provisionUsers(setupClient, cfg, users);
  // 把本次实验的形态打印出来：正确性实验必须能一眼确认
  //「多少用户、每人几次、是否共用账号、是否固定幂等键」。
  console.log(
    '[k6-flashsale] executor=' + executor +
      ' users=' + users +
      ' vus=' + (executor === 'once' ? vus : '(stages)') +
      ' iterations_per_vu=' + (executor === 'once' ? iterationsPerVU : 'n/a') +
      ' shared_user=' + sharedUser +
      ' fixed_key=' + (fixedKey === '' ? '(none)' : fixedKey) +
      ' verify=' + verify +
      ' activity=' + activityId + ' sku=' + skuId
  );
  return { users: provisioned };
}

export default function (data) {
  // 用户选择：默认每个 VU 一个独立用户（正确性实验 A：40 用户各抢一次）；
  // FLASH_SHARED_USER=true 时所有 VU 用同一个账号（实验 B 一人一单 / 实验 C 请求幂等）。
  const user = sharedUser
    ? data.users[0]
    : data.users[(exec.vu.idInTest - 1) % data.users.length];
  const client = newClient(cfg).setToken(user.token);

  // 幂等键：默认每次迭代唯一（模拟不同用户的独立请求）；
  // 开启 FLASH_IDEMPOTENCY_REPLAY 后每个 VU 固定一个 key（模拟重复提交同一请求）；
  // FLASH_FIXED_KEY 给出字面量时，所有请求共用同一个键（跨 VU 的真实并发重放）。
  let idempotencyKey;
  if (fixedKey !== '') {
    idempotencyKey = fixedKey;
  } else if (replayKey) {
    idempotencyKey = cfg.runId + '-replay-' + exec.vu.idInTest;
  } else {
    idempotencyKey = cfg.runId + '-k6-' + exec.vu.idInTest + '-' + exec.scenario.iterationInTest;
  }

  const submittedAt = Date.now();
  const res = biz.createFlashOrder(client, activityId, skuId, idempotencyKey);
  const outcome = biz.classifyCreateOrder(res);
  biz.recordOutcome('create_order', outcome);

  if (trace) {
    // 逐条证据：VU 号、用户、幂等键、HTTP 状态、业务码、分类结果。
    // 只在正确性实验开启（请求量确定且小），不做批量输出。
    console.log(
      'FLASH_TRACE vu=' + exec.vu.idInTest +
        ' user=' + user.username +
        ' key=' + idempotencyKey +
        ' http=' + res.status +
        ' code=' + res.code +
        ' outcome=' + outcome +
        ' data_status=' + (res.data && res.data.status ? res.data.status : '-')
    );
  }

  // 只对「系统侧不确定失败」做断言：业务拒绝是正确行为，不该被判失败。
  check(res, {
    '秒杀下单 | 无传输层失败': (r) => r.status !== 0,
    '秒杀下单 | 响应包合法': (r) => r.envelopeValid,
    '秒杀下单 | 无未解释的系统错误': () => UNEXPLAINED_OUTCOMES.indexOf(outcome) < 0,
  });

  if (!verify || outcome !== 'accepted') {
    sleep(0.1);
    return;
  }

  // 结果查询只用于正确性场景：每次提交追加最多 verifyAttempts 次查询，
  // 会显著改变请求形状（读多写少），因此负载场景必须保持 FLASH_VERIFY=false。
  let state = biz.FLASH_STATUS.QUEUED;
  let lastResult = null;
  for (let attempt = 0; attempt < verifyAttempts; attempt++) {
    sleep(verifyIntervalMs / 1000);
    lastResult = biz.getFlashOrderResult(client, activityId, idempotencyKey);
    expectHttpOk(lastResult);
    const classified = biz.classifyOrderResult(lastResult);
    biz.recordOutcome('query_result', classified.state);
    state = classified.state;
    if (state !== biz.FLASH_STATUS.QUEUED) {
      break;
    }
  }

  if (state === biz.FLASH_STATUS.SUCCESS) {
    check(lastResult, {
      '秒杀结果 | success 必须带订单': (r) => r.data !== null && r.data.order !== null && r.data.order.id > 0,
    });
  }

  // 记录端到端耗时（受理 → 观察到最终状态）与是否在轮询次数内收敛。
  // T5-03 用这条 Trend 判断「queued 停留时间」是否符合消费链路预期。
  const settled = state !== biz.FLASH_STATUS.QUEUED;
  if (settled) {
    finalStateLatency.add(Date.now() - submittedAt);
  }
  check(state, { '秒杀结果 | 轮询次数内到达最终状态': (s) => s !== biz.FLASH_STATUS.QUEUED });
}

export const handleSummary = makeHandleSummary(cfg, {
  profile: profile,
  activity_id: activityId,
  sku_id: skuId,
  flash_users: users,
  idempotency_replay: replayKey,
  verify_result: verify,
  executor: executor,
  iterations_per_vu: executor === 'once' ? iterationsPerVU : null,
  shared_user: sharedUser,
  fixed_key: fixedKey === '' ? null : fixedKey,
});
