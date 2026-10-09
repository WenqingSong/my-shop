# Cleaner Findings

## Review Target

- 任务：`prometheus-integration`（测试环境部署并接入 Prometheus）
- 结论对应版本：`review.target` = C1 = `6af455c11bae122872cbbc459238dcc87079aba8`（Coder 实现 Evidence Commit）
- 任务基线 base：`99ace202a90de7699ab7ea3cd24dd7fa9742ef8c`（与 `task.md` Review Baseline 声明一致）
- 分支：`feat/prometheus-metrics-integration`；当前 HEAD = `be019a4c2d386f00cd35d97d28f41071eb30b5f7`（C2 metadata commit，只改 `state.yaml`）
- 工作区状态：审查开始时 `git status` clean；无任务前遗留未提交修改需区分
- 本任务相关变更（`git diff 99ace20..6af455c`，C1 内为 `git diff 5533b41..6af455c`）：
  - 修改：`Makefile`、`README.md`、`docker-compose.yml`、`scripts/health.sh`、`scripts/logs.sh`、`scripts/status.sh`、`scripts/test.sh`、`scripts/up.sh`
  - 新增：`prometheus/prometheus.yml`、`scripts/test-prometheus.sh`
  - 未触碰：任何 `.go` 生产代码 / `.sql` 迁移 / `internal/metrics` / `docs/design/*`
- 全局资源：`resources.reservations` 为空，本任务未申请 migration_version / error_code_domain，无三边一致性检查项

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | Prometheus（`prom/prometheus:v2.53.0`）启动后 HTTP API 正常：`/-/ready`=200、`/api/v1/targets`、`/api/v1/query`、`/api/v1/status/config` 均返回 `status:success`。注：因审查 Sandbox 为 DinD（见 Verification 环境限制），compose 的 bind mount 无法直接 `up`，采用命名卷承载同一份 `prometheus.yml`、按 compose 服务定义（image/command/extra_hosts/端口）启动等价容器验证。 |
| AC-002 | PASS | `prometheus.yml` 含 `job_name: my-shop`、`metrics_path: /metrics`、`scrape_interval: 15s`、target `host.docker.internal:8000`；`promtool check config` SUCCESS；运行时 `/api/v1/status/config` 回读的 scrape_configs 与之一致。 |
| AC-003 | PASS | `/api/v1/targets` 返回 `health:"up"`、`lastError:""`、`lastScrape` 持续前进、`lastScrapeDuration≈3ms`、无 dropped targets。 |
| AC-004 | PASS | 发送 3 次真实下单请求（`POST /flash-sales/1/orders`，均被 `秒杀活动不存在` 拒绝为 `result=gate_rejected`）后，等待 ≥1 个 scrape 周期，Prometheus 中 `flashsale_request_duration_seconds_count=3`、`_sum=0.001011639`、`_bucket{le="+Inf"}=3`，与服务端 `GET /metrics` 直读的 `count=3`、`sum=0.001011639` 完全对账一致。 |
| AC-005 | FAIL | 子项「metrics 仅开关开启时暴露」PASS（关闭时 `/metrics`=404、开启=200；无 Go 鉴权代码改动）；「启停/健康检查接入」PASS（`up/down/status/health/logs` 均正确纳入 prometheus，App/MySQL/Redis 健康不受影响）；但统一测试入口 `make test` 因 `scripts/test-prometheus.sh` 缺可执行权限而确定性失败（exit 126），构成回归，见 CLEAN-001。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `bash -n`（7 个改动脚本） | PASS | `up/health/logs/status/test/test-prometheus/lib` 语法均通过 |
| `docker compose -f docker-compose.yml config -q` | PASS | 退出码 0 |
| `bash scripts/test-prometheus.sh` | PASS | 静态校验（job/metrics_path/target/scrape_interval/挂载/healthcheck）通过 |
| `promtool check config prometheus.yml` | PASS | `SUCCESS: ... is valid prometheus config file syntax` |
| `go build -o bin/my-shop .` | PASS | 退出码 0 |
| `go test -p 1 ./...`（`make test` 内） | PASS | 全部包 ok（含 `internal/metrics`），在 test-prometheus.sh 失败前 |
| `make test`（统一入口） | FAIL | `scripts/test.sh:17: .../test-prometheus.sh: Permission denied`，exit 126 |
| 运行时：Prometheus `/-/ready` | PASS | 200 |
| 运行时：`/api/v1/targets` | PASS | `health=up`、无 scrape 错误 |
| 运行时：histogram 对账 | PASS | Prometheus count/sum/bucket 与 `/metrics` 直读逐值一致 |
| 运行时：`GET /metrics` 开关语义 | PASS | 关=404、开=200 |

环境限制说明（非代码缺陷，仅影响本 Sandbox 直接走 compose `up` 的路径）：
- 本审查 Sandbox 是容器化 workspace（overlay 文件系统 + DinD，`/.dockerenv` 存在），Docker daemon 与 workspace 文件系统不同源，`docker-compose.yml` 的单文件 bind mount `./prometheus/prometheus.yml:/etc/prometheus/prometheus.yml:ro` 在本 Sandbox 中会被 daemon 当作目录导致启动失败。
- 目标测试环境按 `task.md` 假设为「应用宿主机进程 + Docker 宿主机」，该 bind mount 为标准且正确的写法，不受此限制。
- 为完成 AC-001/003/004 的运行时核验，采用命名卷（named volume）承载同一份 `prometheus.yml`、以 `docker run --add-host host.docker.internal:host-gateway` 复制 compose 的网络打通，验证了配置、抓取、Targets UP 与 histogram 对账的真实行为；核验后已清理临时容器/卷与 `storage/` 运行时产物，工作区恢复 clean。

## Findings

### CLEAN-001：`scripts/test-prometheus.sh` 缺少可执行权限，导致 `make test` 失败

- Severity：P2
- Status：OPEN
- Location：`scripts/test-prometheus.sh`（C1 中 git 文件模式为 `100644`，其余脚本均为 `100755`）；触发点 `scripts/test.sh:17` 直接以 `"${SCRIPT_DIR}/test-prometheus.sh"` 调用
- AC / Invariant：AC-005「无回归」（统一测试入口 `make test` 应保持可用）；任务 Verification「如改动 shell 脚本执行 `bash -n` 校验」
- Trigger：执行 `make test`（或 `bash scripts/test.sh`）
- Actual：`test-prometheus.sh` 无 `+x` 位，`test.sh` 第 17 行直接执行该脚本时报 `Permission denied`，`make test` 以 exit 126 失败（`go vet` / `go test` 已通过，但整体命令失败）
- Expected：`scripts/test-prometheus.sh` 与其他脚本一致具备可执行权限（`100755`），`make test` 完整通过
- Impact：`make test`（统一测试入口，`test.sh` 已在本任务中加入 Prometheus 静态校验）确定性失败，构成对既有生命周期命令的回归；CI/本地自检会被打断
- Evidence：
  - `git ls-tree 6af455c scripts/`：`100644 blob ... scripts/test-prometheus.sh`（唯一 100644，其余脚本 100755）
  - 实测 `make test`：`scripts/test.sh: line 17: .../test-prometheus.sh: Permission denied`，`make: *** [Makefile:40: test] Error 126`
  - 反证：`bash scripts/test-prometheus.sh` 直接以 bash 解释执行可正常通过（说明脚本内容正确，仅缺执行位）
- Required Fix Boundary：使 `scripts/test-prometheus.sh` 具备可执行权限（`chmod +x`，git 模式 `100755`），使 `make test` 完整通过；不规定其他无关改动，不改脚本内容。
