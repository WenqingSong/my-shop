// 认证与测试用户准备（T5-01）。
//
// 真实契约（以 api/iam/v1/iam.go 与 internal/logic/iam/iam.go 为准）：
//   POST /register {username,password} → data:{id,username}
//     username 必须是 ^[a-zA-Z0-9]{3,24}$；password 长度 8~24。
//     用户名已存在时返回业务码 2001（HTTP 409），对「可重复执行」是正常结果。
//   POST /login    {username,password} → data:{access_token,refresh_token,token_type,expires_in}
//     access_token 有效期 3600s，登录用户接口用 Authorization: Bearer <token>。
//
// 安全约束：测试用户密码不写死在脚本里，必须由 K6_TEST_PASSWORD 传入；
// 管理员凭据同样只从环境变量读取，且在日志中一律脱敏。

import { PATHS } from './endpoints.js';
import { requireEnv, shortRunId } from '../config/default.js';
import { expectEnvelopeOk } from './http.js';

// 业务码：用户名已存在（internal/codes/codes.go CodeUsernameExists）。
const CODE_USERNAME_EXISTS = 2001;

// testUserName 生成 run 内唯一的测试用户名。
// 形状：<prefix><runHash>u<index>，例如 k6t501a1b2c3u7。
// 约束：只含字母数字、总长 <= 24（IAM 用户名校验），且同一 run_id 稳定可复现，
// 因此「同一 run_id 重跑」会命中 2001 而不是创建出重复账号。
export function testUserName(cfg, index) {
  const name = cfg.dataPrefix + shortRunId(cfg.runId) + 'u' + index;
  if (!/^[a-zA-Z0-9]{3,24}$/.test(name)) {
    throw new Error(`[k6-auth] 生成的测试用户名 ${name} 不符合 IAM 规则 ^[a-zA-Z0-9]{3,24}$`);
  }
  return name;
}

// testUserPassword 读取测试用户密码；缺失或长度非法直接失败，不猜默认值。
export function testUserPassword() {
  const password = requireEnv('K6_TEST_PASSWORD', '测试用户登录密码（8~24 位，仅用于本地测试账号）');
  if (password.length < 8 || password.length > 24) {
    throw new Error('[k6-auth] K6_TEST_PASSWORD 长度必须为 8~24（IAM 校验规则）');
  }
  return password;
}

// ensureUser 注册并登录一个测试用户。
// register 返回 2001 时表示该 run_id 的账号已存在（重复执行），继续登录即可，
// 因此本函数是幂等的：重复执行不会因「用户名已存在」失败。
export function ensureUser(client, cfg, username, password) {
  const registerRes = client.post(
    PATHS.register,
    { username: username, password: password },
    { name: 'POST /register', tags: { phase: 'setup' } }
  );
  if (registerRes.code !== 0 && registerRes.code !== CODE_USERNAME_EXISTS) {
    throw new Error(
      `[k6-auth] 注册测试用户 ${username} 失败：code=${registerRes.code} message=${registerRes.message}`
    );
  }

  const loginRes = client.post(
    PATHS.login,
    { username: username, password: password },
    { name: 'POST /login', tags: { phase: 'setup' } }
  );
  expectEnvelopeOk(loginRes);
  if (loginRes.code !== 0 || !loginRes.data || !loginRes.data.access_token) {
    throw new Error(
      `[k6-auth] 登录测试用户 ${username} 失败：code=${loginRes.code} message=${loginRes.message}。` +
        `若该用户是上一次 run 用不同密码创建，请改用新的 RUN_ID 或保持 K6_TEST_PASSWORD 一致。`
    );
  }
  return {
    username: username,
    token: loginRes.data.access_token,
    expiresIn: loginRes.data.expires_in,
  };
}

// loginAdmin 用环境变量中的超级管理员登录后台，返回 access token。
// 仅用于测试数据准备（商品/SKU/秒杀活动），不参与负载压测路径。
export function loginAdmin(client) {
  const username = requireEnv('ADMIN_USERNAME', '后台管理员用户名（测试数据准备用）');
  const password = requireEnv('ADMIN_PASSWORD', '后台管理员密码（测试数据准备用）');
  const res = client.post(
    PATHS.adminLogin,
    { username: username, password: password },
    { name: 'POST /admin/login', tags: { phase: 'setup' } }
  );
  expectEnvelopeOk(res);
  if (res.code !== 0 || !res.data || !res.data.access_token) {
    throw new Error(`[k6-admin] 管理员登录失败：code=${res.code} message=${res.message}`);
  }
  return { username: username, token: res.data.access_token };
}

// provisionUsers 顺序注册并登录 count 个测试用户，供 setup() 使用。
// 注意成本：register 与 login 各自包含一次 bcrypt，单次约 60~150ms（取决于 CPU），
// 因此 1000 个用户需要数分钟。T5-03 若需要大量用户，应优先复用已存在的账号
// （固定 RUN_ID + 固定 K6_TEST_PASSWORD 即可命中 2001 分支），而不是每次重建。
export function provisionUsers(client, cfg, count) {
  const password = testUserPassword();
  const users = [];
  for (let i = 1; i <= count; i++) {
    const username = testUserName(cfg, i);
    const user = ensureUser(client, cfg, username, password);
    users.push(user);
  }
  return users;
}
