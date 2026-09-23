# Core Logic Review

> 本阶段为工程初始化骨架，无业务逻辑。以下为 Owner 应重点理解的两处核心行为。

## CL-001：启动依赖连通性门禁

### Location

`internal/boot/boot.go:83-113`（`waitForDependencies` + `checkDependencies`）

### What It Does

服务启动时先对 MySQL、Redis 各做一次连通性检查；任一失败则按 1 秒间隔重试，直到成功或超过 `STARTUP_DEPENDENCY_TIMEOUT`（默认 30 秒）超时。成功后才开始监听端口；超时或取消则返回错误，进程以非零状态退出。

### Business Invariant

服务只有在 MySQL 与 Redis 均可达后才会监听端口；任一依赖在超时窗口内不可达时，服务必须明确失败退出，不得静默带病启动。

### Why Owner Should Understand It

它决定了“依赖不可用时的系统行为”（对应 AC-006 / AC-007）。健康检查接口是纯 liveness（恒返回 `ok`），不含 readiness 探测；因此“依赖可用性”的守卫完全由这段启动门禁承担。若此门禁被弱化（例如失败仅告警后继续启动），服务会在依赖缺失的情况下运行，而健康接口仍返回 200，从而掩盖真实故障。

### Relevant Tests

- 无自动化测试直接覆盖失败路径（当前仅手动验证，见 `findings.md` CLEAN-001）。
- 相关：`TestLivenessHTTP`（`internal/controller/health/health_test.go`）、`TestLiveness`（`internal/logic/health/health_test.go`）。

### Suggested Mutation

将 `waitForDependencies` 中 `if lastErr == nil` 改为恒真（跳过检查直接返回 nil），或将 `checkDependencies` 中的 `PingMaster` / `PING` 调用移除。

### Expected Result

服务在 MySQL/Redis 不可用时仍会启动并监听端口。当前无自动化测试能捕获这一变化——这本身即印证了 CLEAN-001 的缺口，Owner 可据此确认需要补充失败路径测试，或在交付验收中显式覆盖 AC-007。

---

## CL-002：配置环境变量覆盖优先级

### Location

`internal/boot/boot.go:115-149`（`cfgString` / `cfgInt` / `cfgBool`，基于 `g.Cfg().GetEffective`）

### What It Does

以统一的优先级解析关键配置：环境变量 > 配置文件（`manifest/config/config.yaml`）> 代码内置默认值。数据库、Redis、监听地址、启动超时等均走该路径。

### Business Invariant

监听地址、数据库/Redis 连接信息（主机、端口、账号、密码）、启动超时等关键配置必须能通过环境变量覆盖，代码与配置文件中不得存在不可覆盖的硬编码真实凭据。

### Why Owner Should Understand It

这是 AC-004 / AC-008 的落点。运行环境（CNB 云原生开发环境，DinD，每日重置）要求配置可注入、可重建。若优先级错乱（例如代码默认值反过来覆盖环境变量），则无法在空白环境中通过注入真实配置启动服务。

### Relevant Tests

- `TestCfgStringEnvOverride`、`TestCfgStringDefaultFallback`
- `TestApplyDatabaseConfigEnvOverride`
- `TestApplyRedisConfigEnvOverride`
- `TestDependencyTimeoutEnvOverride`
（均位于 `internal/boot/boot_test.go`）

### Suggested Mutation

将 `cfgString`（及其它 `cfg*`）中的 `g.Cfg().GetEffective` 改为 `g.Cfg().Get`（只读配置文件、忽略环境变量），或注释掉环境变量读取路径。

### Expected Result

上述 4 个 env override 测试必须失败。若仍全部通过，说明测试未能真正约束“环境变量覆盖”这一业务语义。
