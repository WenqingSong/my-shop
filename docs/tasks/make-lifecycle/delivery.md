# Delivery Verification

## Milestone

- 统一项目生命周期入口（Makefile + scripts）：`make-lifecycle`
- Task: `.agent/tasks/make-lifecycle/task.md`

## Environment

- OS: Linux (CNB DinD 临时开发环境)
- Go: go1.24.1 linux/amd64
- Docker: 29.6.2 / Compose v5.3.1
- MySQL: 8.0.46 (容器 `my-shop-mysql`, healthcheck: mysqladmin ping)
- Redis: 7.4.11 (容器 `my-shop-redis`, healthcheck: redis-cli ping)
- Other: 无

## Delivery Target

- Commit / Worktree: `chore/make-build` @ `8a152ba`（working tree clean）
  - `c2f93e4` feat: 添加统一生命周期管理脚本与 Makefile
  - `8a152ba` docs(agent): 更新 make-lifecycle 审查发现
- Task: `.agent/tasks/make-lifecycle/task.md`
- Contract: 无独立 contract.md

## Build

| Check | Result | Evidence |
|---|---|---|
| go build ./... | PASS | `go build ./...` exit 0，无编译错误、无缺失依赖 |
| 生成代码缺失 | PASS | 无生成代码依赖 |

## Tests

| Test | Result | Evidence |
|---|---|---|
| Unit (`go test ./...`) | PASS（App 未运行时） | `make test` exit 0，`internal/boot`、`internal/controller/health`、`internal/logic/health` 均 ok |
| Unit（App 运行时） | FAIL | `make test` exit 2：`net.Listen address ":8000" failed: bind: address already in use` |
| Race (`go test -race ./...`) | PASS | exit 0，全部包 ok（boot 1.019s / controller 1.124s / logic 1.017s） |
| go vet | PASS | `make test` 内嵌 `go vet ./...` 通过 |

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 bootstrap 全新环境一键可开发 | PASS | 清理 tmp/bin/容器后 `make bootstrap` exit 0，依赖 healthy、构建、启动、App healthy |
| AC-002 help 列出全部目标 | PASS | `make help` 列出 10 个目标及说明，exit 0 |
| AC-003 up 幂等 | PASS | 连续两次 `make up` 均 exit 0，第二次提示「应用已在运行（pid 29705）」，无重复容器/进程（pid 文件与 `pgrep` 一致） |
| AC-004 down 保留数据卷 | PASS | `make down` 后 `workspace_mysql_data`/`workspace_redis_data` 仍存在；容器被移除 |
| AC-005 restart 先停后启恢复可用 | PASS | `make restart` exit 0，pid 更新，最终三者 healthy |
| AC-006 status 展示三者状态 | PASS | 显示 App running/stopped、mysql/redis running(healthy) |
| AC-007 logs 查看日志 | PASS | 展示应用日志与 mysql/redis 容器日志 |
| AC-008 health 三者健康/异常反馈 | PASS | 全健康 exit 0；组件异常时 exit 1 并打印「存在不健康组件」 |
| AC-009 test 统一测试入口并传递成败 | FAIL（CLEAN-002, P3） | App 运行时 `make test` exit 2（端口冲突）；仅 App 未运行时 PASS |
| AC-010 clean 清理且不误删 | FAIL（CLEAN-001, P2） | App 运行时 `make clean` 删除 pid 文件，留下孤儿进程（见下） |
| AC-011 依赖就绪不依赖固定 sleep | PASS | `wait_for_deps` 基于 `container_healthy`（docker healthcheck）轮询 |
| AC-012 配置与逻辑分离 | PASS | Makefile 仅转发，逻辑全部下沉 scripts/ |
| AC-013 敏感配置不硬编码 | PASS | Makefile 与 scripts/ 无硬编码密码；健康探测无需密码，环境变量注入 |

## Data Verification

- MySQL / Redis 依赖连通与持久化：
  - 写入 Redis `deliverer_probe`、MySQL `my_shop.probe` 后 `make down` → `make up`，数据仍存在：
    - `redis-cli get deliverer_probe` → `alive-1790240110`
    - `SELECT * FROM my_shop.probe` → `1 | persist`
- 证明 `make down` 不删除持久数据卷（AC-004）。

## Runtime / Infrastructure Verification

- `make bootstrap`（全新环境）→ 依赖容器 healthy → 构建 → 启动 → `GET /health` 返回 `{"code":0,...,"status":"ok"}`。
- 全链路健康：`make health` 输出 App/mysql/redis 三者 healthy，exit 0。

## FAIL 详情（独立复现证据）

### FAIL-1（对应 CLEAN-001 / AC-010）：`make clean` 在应用运行时删除 pid 文件，产生孤儿进程

复现步骤与证据（独立执行）：

1. `make up` 后应用运行中：`tmp/my-shop.pid` = `19642`，`ss -ltnp` 显示 `my-shop` pid 19642 监听 `:8000`，`curl :8000/health` 正常。
2. 执行 `make clean`（exit 0）后：
   - `tmp/`、`bin/` 被整体删除（pid 文件丢失）。
   - `make status` → `App stopped`。
   - 但 `curl http://127.0.0.1:8000/health` 仍返回 `{"code":0,...}`，`ss -ltnp` 仍显示 pid 19642 占用 `:8000`。
3. 后续 `make down` 无法停止该应用（pid 文件已丢失），`:8000` 仍被占用。
4. 后续 `make up` 在孤儿仍占 `:8000` 时：重新构建并启动新进程（新进程 bind `:8000` 失败退出），却因孤儿仍在响应 `/health` 而误报「应用已健康 / up complete」，留下 pid 文件指向已退出进程、tmp 目录缺失、`make status` 显示 `App stopped` 但 `make health` 显示 `App healthy` 的自相矛盾状态。

影响：应用进入「实际在跑但生命周期无法感知/无法停止」的不可控状态，并可级联导致 `down`/`up`/`status`/`health` 行为互相矛盾。

### FAIL-2（对应 CLEAN-002 / AC-009）：`make test` 在应用运行时因端口冲突失败

证据（独立执行）：

```
make up 后执行 make test：
... [FATA] net.Listen address ":8000" failed: listen tcp :8000: bind: address already in use
FAIL  cnb.cool/go-cloud-devops/my-shop/internal/controller/health  0.007s
FAIL
make: *** [Makefile:40: test] Error 1  (exit 2)
```

根因：`internal/controller/health/health_test.go:19` 测试绑定全局默认 `:8000`，未与运行态隔离。

## Tests Not Executed

| Test | Reason | Risk |
|---|---|---|
| 生产部署 / 镜像打包 / 发布流水线 | Out of Scope（任务明确不覆盖） | 无 |
| MySQL/Redis 高可用/主从/集群 | Out of Scope | 无 |
| 压测 / 高并发 | 任务未包含压测指标 | 无 |

## Remaining Risks

- `make clean` 在应用运行时破坏运行态（FAIL-1），修复前会污染后续所有生命周期命令的可靠性。
- `make test` 结果依赖应用是否运行，可能误导开发者（FAIL-2）。
- 上述两项均为 Cleaner 已记录的 OPEN Finding（CLEAN-001 P2 / CLEAN-002 P3），尚未修复。

## Rollback / Recovery Notes

- 当前环境已由 Deliverer 恢复干净状态：应用进程已停止、依赖容器已 `make down`（数据卷保留）。
- 若遇孤儿进程，需手动 `kill <pid>`（pid 可由 `ss -ltnp | grep :8000` 获取）以恢复。

## Result

FAIL
