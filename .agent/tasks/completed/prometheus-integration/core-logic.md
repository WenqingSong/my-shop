# Core Logic 验证卡

本任务为测试环境部署与采集接入，不改变秒杀指标定义与鉴权语义。以下两张卡覆盖本任务真正引入的、Owner 值得掌握的两条机制：抓取配置/网络打通（决定 target 是否 UP）与端到端对账（决定采集结果是否可信）。

## CL-001：Prometheus 抓取配置与网络打通（决定 target UP / 指标能否进入 TSDB）

- Owner 需要理解：真实抓取由 `job_name` / target / `scrape_interval` 与网络打通决定——容器内 Prometheus 经 `host.docker.internal:host-gateway` 访问宿主机 `:8000`，任一配错或网络不通，target 会变成 `down`、指标无法进入 TSDB。注意 `metrics_path` 有默认值 `/metrics`，删除该行不会改变真实抓取路径（仍抓 `/metrics`）；本任务将其显式写死并用静态校验强制，属防御式显式声明，用于防依赖默认值造成语义漂移/配置不可读，而非「删除即抓取失败」。
- 生产代码：`prometheus/prometheus.yml`（`scrape_configs`，job=`my-shop`、`metrics_path=/metrics`、target=`host.docker.internal:8000`、`scrape_interval=15s`）；`docker-compose.yml`（prometheus 服务 `extra_hosts: host.docker.internal:host-gateway` + 只读挂载 `./prometheus/prometheus.yml`）
- 关键测试：`scripts/test-prometheus.sh`（静态校验 job_name / metrics_path / target / scrape_interval / host-gateway / 只读挂载 / healthcheck，并跑 `docker compose config -q`）；集成入口 `scripts/test.sh` 在 `go test` 后调用它
- 基线验证：`make test` → 期望 `Prometheus 接入静态校验通过`、`测试全部通过`（exit 0）；运行时 `curl http://127.0.0.1:9090/api/v1/targets` → `health:"up"`、`lastError:""`
- 可选 Mutation（区分静态配置校验与真实网络抓取两层）：
  - 把 target 改成错误端口（如 `host.docker.internal:9999`）→ 静态校验 `grep 'host.docker.internal:8000'` 失败，且真实抓取 target down；
  - 删除 `metrics_path: /metrics` 行 → 仅静态校验 `grep '^    metrics_path: /metrics$'` 失败，真实抓取仍按默认 `/metrics` 成功、target 仍 UP
- 预期失败：`scripts/test-prometheus.sh` 中 `grep -q 'host.docker.internal:8000'` / `grep -q '^    metrics_path: /metrics$'` 匹配失败，脚本 `fail` 退出（exit 1）；其中删除 `metrics_path` 只触发静态校验失败，不触发真实抓取失败
- 恢复确认：还原配置后重跑 `make test` 或 `bash scripts/test-prometheus.sh`，恢复通过

## CL-002：指标端到端对账（histogram count/sum/bucket 与 /metrics 直读一致）

- Owner 需要理解：`flashsale_request_duration_seconds` 是进程内 Histogram，经 `GET /metrics` 以 Prometheus 文本格式暴露，Prometheus 按周期抓取落库。AC-004 的核心不变量是「指标稳定且完成新一轮 scrape 后，同标签（`interface`/`result`）、同采样时点的 Prometheus 查询值 == 服务端 `/metrics` 直读值」，二者不一致说明抓取链路或指标语义漂移，采集结果不可信。对账不是任意时刻实时相等：服务端 `/metrics` 是进程内即时快照，Prometheus 存在一个 `scrape_interval` 的滞后，须等待抓取完成后比对。
- 生产代码：`internal/metrics/metrics.go`（`ObserveOrder` 每次下单请求递增 `requestsTotal` 与 `requestDuration`，按 `interface/result` 标签）；`prometheus/prometheus.yml`（抓取该端点）；路由 `internal/cmd/cmd.go` `registerMetricsRoute`（仅 `flash_sale.metrics.enabled=true` 时挂载，本任务未改）
- 关键测试：`internal/metrics/metrics_test.go` `TestObserveOrderRecordsCountAndDuration`（一次 ObserveOrder 使 Counter 与 Histogram 各 +1、耗时样本非零）；运行时对账为集成验证
- 基线验证：`go test ./internal/metrics/` 通过；运行时发真实请求后 `curl http://127.0.0.1:8000/metrics | grep flashsale_request_duration_seconds_count` 与 `curl 'http://127.0.0.1:9090/api/v1/query?query=flashsale_request_duration_seconds_count'` 数值一致（本次实测 count=3、sum=0.001011639 逐值一致）
- 可选 Mutation：把 `internal/metrics/metrics.go` `ObserveOrder` 中的 `requestDuration.WithLabelValues(...).Observe(d.Seconds())` 删掉（或把 `.Inc()` 删掉）
- 预期失败：`TestObserveOrderRecordsCountAndDuration` 断言 Counter/Histogram 前后差 1 失败；或运行时 Prometheus `count` 不再随请求增长
- 恢复确认：还原观测调用后重跑 `go test ./internal/metrics/`，恢复通过
