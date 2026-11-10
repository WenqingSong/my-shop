// SurgeCart k6 统一配置入口（T5-01）。
//
// 设计约束（为什么这样写）：
//  1. 环境变量是唯一配置来源：k6 通过全局 __ENV 暴露进程环境，脚本不读 YAML、
//     不读业务配置文件，避免测试侧与 manifest/config 双份事实源漂移；
//  2. 敏感项没有默认值：管理员账号密码、测试用户密码缺失时立即抛错（fail-fast），
//     绝不把凭据写进仓库或日志（见 redact()）；
//  3. 默认值只服务本地 kind 环境：BASE_URL 默认 host.docker.internal:8000，因为
//     k6 跑在容器里、应用 Service 是 ClusterIP（需先 kubectl port-forward）；
//  4. 校验集中在 loadConfig：非法数字/非法 URL/非法 run_id 在初始化阶段就失败，
//     不把配置错误拖延成压测中途的假数据。

// 本地 kind 环境下 k6 容器访问宿主机 port-forward 的默认地址。
const DEFAULT_BASE_URL = 'http://host.docker.internal:8000';

// k6 容器的结果挂载点（由 test/k6/scripts/run.ps1 把宿主机 results/ 挂到这里）。
const DEFAULT_SUMMARY_DIR = '/results';

// 测试数据前缀默认值：所有测试用户名/名称都以此开头，便于事后按前缀清理与识别。
const DEFAULT_DATA_PREFIX = 'k6t501';

// run_id 允许字符集：会进入结果文件名与测试用户名，只能是文件名安全的字符串。
const RUN_ID_PATTERN = /^[A-Za-z0-9._-]{6,48}$/;

// dispatch_mode 只影响「哪些指标必然存在」，不影响脚本行为。
const DISPATCH_MODES = ['kafka', 'db_scanner', 'unknown'];

function rawEnv(name) {
  const value = __ENV[name];
  if (value === undefined || value === null) {
    return undefined;
  }
  const trimmed = String(value).trim();
  return trimmed === '' ? undefined : trimmed;
}

// envStr 读取字符串配置：空字符串视为未设置（避免 CI 里 `FOO=` 把默认值顶掉）。
export function envStr(name, fallback) {
  const value = rawEnv(name);
  return value === undefined ? fallback : value;
}

// envInt 读取正整数配置：显式给出的非法值直接失败，不做静默回退。
export function envInt(name, fallback) {
  const value = rawEnv(name);
  if (value === undefined) {
    return fallback;
  }
  const parsed = Number(value);
  if (!isFinite(parsed) || Math.floor(parsed) !== parsed || parsed <= 0) {
    throw new Error(`[k6-config] 环境变量 ${name}=${value} 非法：必须为正整数`);
  }
  return parsed;
}

// envBool 读取布尔配置：只接受 true/false/1/0，其余直接失败。
export function envBool(name, fallback) {
  const value = rawEnv(name);
  if (value === undefined) {
    return fallback;
  }
  const normalized = value.toLowerCase();
  if (normalized === 'true' || normalized === '1') {
    return true;
  }
  if (normalized === 'false' || normalized === '0') {
    return false;
  }
  throw new Error(`[k6-config] 环境变量 ${name}=${value} 非法：必须为 true/false/1/0`);
}

// requireEnv 读取必需配置；缺失时给出「变量名 + 用途 + 取值来源」三段式错误，
// 让操作者在执行输出的第一屏就知道该补哪个变量，而不是等断言的含糊失败。
export function requireEnv(name, purpose) {
  const value = rawEnv(name);
  if (value === undefined) {
    throw new Error(
      `[k6-config] 缺少必需环境变量 ${name}（用途：${purpose}）。` +
        `请通过 test/k6/scripts/run.ps1 -Set @{ ${name} = '<值>' } 或 -e ${name}=<值> 传入，` +
        `不得把该值写入仓库。`
    );
  }
  return value;
}

// assertHttpUrl 只做最小结构校验：k6 的 Goja 运行时没有保证提供 WHATWG URL，
// 因此不用 new URL()，而是用显式协议前缀 + 无空格判断。
export function assertHttpUrl(value, label) {
  if (!/^https?:\/\/\S+$/.test(value)) {
    throw new Error(`[k6-config] ${label}=${value} 非法：必须形如 http(s)://host[:port][/path]`);
  }
  return value.replace(/\/+$/, '');
}

// defaultRunId 生成默认可排序的运行标识（本地时间无关，使用 UTC 紧凑格式）。
// 形状：r20261110t034126 → 便于 results/ 目录按名称排序。
export function defaultRunId() {
  const iso = new Date().toISOString(); // 2026-11-10T03:41:26.123Z
  return 'r' + iso.slice(0, 19).replace(/[-:]/g, '').replace('T', 't');
}

// shortRunId 把 run_id 压缩成 6 位 alnum，用于拼接测试用户名。
// 为什么要压缩：IAM 用户名规则是 ^[a-zA-Z0-9]{3,24}$，run_id 带 '-'/'t' 且可能很长。
export function shortRunId(runId) {
  let hash = 0x811c9dc5;
  for (let i = 0; i < runId.length; i++) {
    hash ^= runId.charCodeAt(i);
    hash = (hash * 0x01000193) >>> 0;
  }
  const base36 = hash.toString(36);
  return (base36 + '000000').slice(-6);
}

// loadConfig 汇总本次运行的全部配置；overrides 由场景脚本传入（如固定的 scenario 名），
// 优先级：overrides > 环境变量 > 代码默认值。
export function loadConfig(overrides) {
  const runId = envStr('RUN_ID', defaultRunId());
  if (!RUN_ID_PATTERN.test(runId)) {
    throw new Error(
      `[k6-config] RUN_ID=${runId} 非法：只允许字母数字与 . _ -，长度 6~48（会进入结果文件名）`
    );
  }

  const dispatchMode = envStr('EXPECT_DISPATCH_MODE', 'unknown').toLowerCase();
  if (DISPATCH_MODES.indexOf(dispatchMode) < 0) {
    throw new Error(
      `[k6-config] EXPECT_DISPATCH_MODE=${dispatchMode} 非法：只允许 ${DISPATCH_MODES.join(' / ')}`
    );
  }

  const cfg = {
    // 被测服务入口：容器内视角的地址，不是宿主机浏览器视角。
    baseUrl: assertHttpUrl(envStr('SURGECART_BASE_URL', DEFAULT_BASE_URL), 'SURGECART_BASE_URL'),
    // 环境标识：进入结果元数据，用于区分 local-kind / staging 等。
    testEnv: envStr('TEST_ENV', 'local-kind'),
    // 运行标识：结果文件名、测试用户名、后续 Prometheus 查询窗口的关联键。
    runId: runId,
    // 场景名：由场景脚本 overrides 提供，进入 k6 tag 与结果元数据。
    scenario: envStr('SCENARIO', 'unknown'),
    // 单请求超时：必须小于 k6 的默认 60s，避免慢请求把整个迭代拖住。
    timeoutMs: envInt('HTTP_TIMEOUT_MS', 5000),
    // 测试数据前缀：独立数据范围，避免污染正常业务数据。
    dataPrefix: envStr('DATA_PREFIX', DEFAULT_DATA_PREFIX),
    // 结果输出目录（k6 容器内路径）。
    summaryDir: envStr('K6_SUMMARY_DIR', DEFAULT_SUMMARY_DIR),
    // Prometheus 基址：仅用于文档/后续脚本关联查询，k6 本身不直接查 Prometheus。
    prometheusUrl: envStr('PROMETHEUS_BASE_URL', ''),
    // 被测进程的 dispatch_mode：决定 /metrics 中哪些指标必然存在。
    dispatchMode: dispatchMode,
    // 是否导出 k6 原始 JSON Lines（体积大，默认关闭，由 --out json 显式开启）。
    rawJson: envBool('K6_RAW_JSON', false),
  };

  return Object.assign(cfg, overrides || {});
}

// redact 返回可安全打印的配置视图：任何 *_password 字段一律替换，防止凭据进日志。
export function redact(cfg) {
  const safe = {};
  Object.keys(cfg).forEach(function (key) {
    safe[key] = /password|secret|token/i.test(key) ? '***' : cfg[key];
  });
  return safe;
}

// printConfig 统一打印一次配置摘要（不含敏感值），便于结果与日志互相印证。
export function printConfig(cfg) {
  console.log(`[k6-config] ${JSON.stringify(redact(cfg))}`);
}
