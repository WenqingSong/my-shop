# Cleaner Findings

## Review

- Review Target: commit `bf606f2`（feat: 初始化电商项目工程骨架）
- Task: 电商项目工程初始化（project-init）
- Result: CLEAN

## Findings

### CLEAN-001：依赖不可用时的失败路径缺少自动化测试

- Severity: P3
- Status: OPEN
- File: internal/boot/boot.go
- Location: `checkDependencies`（105-113 行）与 `waitForDependencies`（83-102 行）
- Acceptance Criteria: AC-007
- Trigger: MySQL 或 Redis 连接失败 / 不可用
- Actual Behavior: 失败路径行为正确（重试、超时后返回明确错误、进程非零退出），但仅由 Cleaner 手动验证，仓库内无自动化测试覆盖。
- Expected Behavior: 有自动化测试约束“依赖不可用 → 明确报错并退出”这一关键失败路径。
- Impact: AC-007 是显式验收项，但其回归保护缺失；后续若有人弱化门禁（如失败仅告警继续启动），现有测试不会捕获。
- Evidence:
  - 手动验证：`STARTUP_DEPENDENCY_TIMEOUT=2 DATABASE_DEFAULT_PORT=39999 ./my-shop` → exit code 1，日志含 `dependency check failed after 2s: mysql connectivity check failed ... connection refused`。
  - `internal/boot/boot_test.go` 仅覆盖配置解析与 env 覆盖，无 `checkDependencies`/`waitForDependencies` 失败用例。
- Required Fix Boundary: 在 boot 包内补充依赖连通性失败路径测试（可借助真实不可达地址或对依赖检查逻辑做可注入抽象），使 AC-007 有自动化回归保护。非阻塞。

## Remaining P3 / Risks

- CLEAN-001（P3，测试覆盖缺口，不阻塞）。
- MySQL 配置 `charset: "utf8"` 为 MySQL 8.0 中已弃用的别名（建议后续建表时改为 `utf8mb4`）；本阶段无表结构，不构成问题。
- Redis 容器未配置 `requirepass`，与 `REDIS_DEFAULT_PASS` 的文档化覆盖能力不对称；默认空密码自洽，仅在需要密码鉴权的环境下需同时在编排侧补配置，属环境配置事项，非代码缺陷。
