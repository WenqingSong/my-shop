# Cleaner Findings

## Review

- Review Target: `make-lifecycle`（Makefile + scripts/ 统一生命周期入口）
- Task: `.agent/tasks/make-lifecycle/task.md`
- Result: CHANGES_REQUIRED

## Acceptance Criteria 逐项结论

| AC | 结论 | 证据 |
| --- | --- | --- |
| AC-001 bootstrap 全新环境一键进入可开发状态 | PASS | `make bootstrap` 成功：依赖容器 healthy、构建、启动、应用 healthy |
| AC-002 help 列出全部目标 | PASS | `make help` 列出 10 个目标及说明 |
| AC-003 up 幂等 | PASS | 连续两次 `make up` 均 exit 0，第二次提示「应用已在运行」，无重复容器/进程 |
| AC-004 down 保留数据卷 | PASS | `make down` 后 `workspace_mysql_data`/`workspace_redis_data` 仍存在 |
| AC-005 restart 先停后启恢复可用 | PASS | `make restart` 成功，最终三者 healthy |
| AC-006 status 展示三者状态 | PASS | 正确显示 App/mysql/redis 的 running/healthy |
| AC-007 logs 查看日志 | PASS | 展示应用日志与 mysql/redis 容器日志 |
| AC-008 health 三者健康与异常反馈 | PASS | 全健康 exit 0；App 缺失时 exit 1 并打印「存在不健康组件」 |
| AC-009 test 统一测试入口并传递成败 | PASS（有前提） | App 未运行时 PASS；App 运行时因端口冲突 FAIL（见 P3） |
| AC-010 clean 清理产物且不误删 | FAIL | clean 运行中会误删 pid 文件，留下无法 stop 的孤儿进程（CLEAN-001） |
| AC-011 依赖就绪不依赖固定 sleep | PASS | `wait_for_deps` 基于容器 healthcheck 轮询（`container_healthy`） |
| AC-012 配置与逻辑分离、Makefile 薄封装 | PASS | Makefile 仅转发，逻辑在 scripts/ |
| AC-013 敏感配置不硬编码 | PASS | 密码无硬编码，健康探测无需密码，环境变量注入 |

## Findings

### CLEAN-001：`make clean` 在应用运行时会删除 pid 文件，留下无法被生命周期停止的孤儿进程

- Severity: P2
- Status: OPEN
- File: `scripts/clean.sh`
- Location: `scripts/clean.sh:8`（`rm -rf "${BIN_DIR}" "${TMP_DIR}"`）
- Acceptance Criteria: AC-010（clean 应清理编译产物/临时产物且不破坏运行态）
- Trigger: 应用处于运行状态（`make up` / `make bootstrap` 之后）时执行 `make clean`。
- Actual Behavior:
  - `clean.sh` 直接 `rm -rf` 整个 `tmp/` 目录，删除了 `tmp/my-shop.pid` 与 `tmp/my-shop.log`，但应用进程仍存活并继续监听 `:8000`。
  - 实测：`make clean` 后 `make status` 输出 `App stopped`，但 `curl http://127.0.0.1:8000/health` 仍返回 `{"code":0,...}`，`ss -ltnp` 显示 `my-shop` 进程仍占用 `:8000`。
- Expected Behavior: clean 不应让应用进入「实际在跑但生命周期无法感知/无法停止」的不可控状态；应优先停止应用，或保留 pid 文件，或拒绝在应用运行时清理。
- Impact:
  - 之后 `make down` 无法停止该应用（pid 文件已丢失）。
  - 之后 `make up` / `make bootstrap` 会因 `:8000` 被占用而失败（`bind: address already in use`）。
- Evidence:
  - 复现命令：`make up` → `make clean` → `make status`（App stopped）→ `curl :8000/health`（仍返回 ok）。
- Required Fix Boundary:
  - 保证 AC-010 清理动作不破坏运行态：在删除 `tmp/` 前先 `stop_app`，或让 clean 在 `is_app_running` 为真时拒绝/提示，或至少保留 pid 文件使 `down` 仍能停止该进程。修复范围限定在 `scripts/clean.sh`（可复用 `scripts/lib.sh` 中的 `stop_app` / `is_app_running`）。

### CLEAN-002：`make test` 在应用运行时会因端口冲突失败（前置测试未做端口隔离）

- Severity: P3
- Status: OPEN
- File: `internal/controller/health/health_test.go`
- Location: `internal/controller/health/health_test.go:19`（`s := g.Server(guid.S())`，实际绑定全局默认 `:8000`）
- Acceptance Criteria: AC-009（test 作为统一测试入口）
- Trigger: 应用已运行（`make up` 后）时执行 `make test`。
- Actual Behavior: `go test ./...` 中 `internal/controller/health` 测试绑定 `:8000`，报 `listen tcp :8000: bind: address already in use`，`make test` exit 2。App 未运行时测试 PASS。
- Expected Behavior: 测试应使用随机端口（`s.SetPort(0)` 或等价方式）实现与运行态隔离，使 `make test` 结果不受应用是否运行影响。
- Impact: 开发者在 `make up` 后直接 `make test` 会误判测试失败。
- Evidence: `make up` 后 `make test` FAIL；`kill` 应用后 `make test` PASS。
- 说明: 该文件属于 `internal/` 前置测试，本次 Task 的 Out of Scope 明确「不修改现有 Go 测试」，故不阻塞本次 CLEAN；但作为统一 `make test` 入口的可靠性缺陷记录在案，建议由 Owner 决定是否另行排期修复。
