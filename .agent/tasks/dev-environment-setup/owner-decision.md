# Owner Decision

## Review Target

`fb5bbee68ec31893aa7ddcd0e8bb7400c6a2b222`（短 `fb5bbee`）

## Core Logic

- CL-001：`.env` 空值不覆盖 `config.yaml` 非空默认（INV-001 / AC-003）。生产代码 `scripts/lib.sh` `_load_env_file`（L54 空值跳过），加载优先级稳定为「已导出环境变量 > `.env` 非空值 > config.yaml 默认值」；回归测试 `scripts/test-lib.sh`（`LIB_TEST_EMPTY` 空值不导出断言）。
- CL-002：超级管理员创建条件预检（INV-002 / AC-008）。生产代码 `internal/boot/admin_check.go` `CheckSuperAdminCondition` 复用 `internal/boot/seed.go` `superAdminExists`（1146 视为「不存在」、其它 DB 错误 fail-closed）：已存在超管幂等通过、缺失 `ADMIN_SUPER_PASSWORD` 时 fail-fast；接线 `scripts/up.sh` / `internal/cmd/cmd.go` `admin check`。测试 `internal/boot/admin_check_test.go`（已存在/缺密码失败/有密码通过/表不存在四态）。

## Owner Decision

ACCEPTED

## Decision Evidence

- 2026-10-09 Owner 明确 ACCEPT，接受 Review Target `fb5bbee68ec31893aa7ddcd0e8bb7400c6a2b222`，认可 CL-001、CL-002 的实现及验证证据。
- 三个已记录的非阻塞风险按当前 Contract 接受，不阻塞交付：
  1. INV-003（错误不泄漏 Secret）目前靠 Cleaner 实测验证，未沉淀为独立持久化回归测试；
  2. `_load_env_file` 空值跳过只覆盖 `.env` 文件，shell 直接 `export AUTH_JWT_SECRET=` 仍会覆盖默认（超出 AC-003 范围）；
  3. 超管判定依赖 `DATABASE_DEFAULT_*` 与 docker-compose 根凭据一致，不一致时 fail-closed（不泄漏凭据，需自行对齐）。
- 交付阶段重点实际验证（Owner 明确要求，供 Deliverer）：
  1. `make init` 创建和维护 `.env`；
  2. 填写必要配置后 `make up` 正常启动；
  3. 首次初始化与已有超级管理员场景；
  4. `make test` 正常通过；
  5. 不泄漏任何真实 Secret。
  且不得将尚未执行的真实环境验收标记为 PASS。
