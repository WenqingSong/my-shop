# Delivery — SurgeCart 秒杀链路工程基线审计（T01）

## 1. 交付范围

- 分支: `task/surgecart-baseline-audit`
- 审计产物提交（交付 commit）: `73a48f6755e7ed041643c1bde96e3e09ec6b1f8f`
- 记录补记提交: 见 `git log -2 --format=%H`（本节所在提交；交付 commit 已包含全部审计产物）
- 审查对象: `95c13910c7226b4e32487ed046702caf75d74fc6`（审计基线；源码与 `473d6f5253e3abcb98ece9d79a5a8059563a3e8d` 等价）
- 审查结论: **NOT_REVIEWED**
- 独立性披露: 本任务由单个 Agent Session 完成，报告作者即 LEAD；**审查为作者自审，独立性未满足**。按 `AgentCollaborationSpecification.md` §4.3 如实标注，不声称已通过独立审查。Owner 可指派新 Session 或外部审查者按上述 SHA 复核。
- 变更范围: 仅 `.agent/tasks/surgecart-baseline-audit/` 与 `docs/reports/surgecart-baseline-audit/`。无业务代码、无 Schema、无 Migration、无测试断言、无构建/CI 配置改动。

## 2. 验收标准核对（逐条）

| # | 验收标准（摘要，原文见 `task.md`） | 结论 | 证据 |
| --- | --- | --- | --- |
| AC-1 | 报告首页登记仓库、分支、完整 HEAD SHA、工作区状态，并与 `git rev-parse HEAD`/`git status --short` 一致 | PASS | `call-chain-map.md` §0；`git rev-parse HEAD` = `95c13910c7226b4e32487ed046702caf75d74fc6`，`git status --short` 交付前仅本任务改动 |
| AC-2 | D1 从 HTTP 入口到订单结果完整链路，覆盖十类环节，可 grep 到函数与调用点 | PASS | `call-chain-map.md` §1（15 跳）、§2；抽查：路由 `routes_frontend.go:106`、Lua `redis.go:65/207`、领取 `consume.go:84` 均可 grep 命中 |
| AC-3 | D2 列出关键表/字段/状态/事务/唯一约束/索引/条件更新/Migration，并说明守护的不变量 | PASS | `data-layer-map.md` §1-§5、§8；migration 15:26-27、11:56-58 实测命中 |
| AC-4 | D2 回答「请求表承担哪些职责」，说明领取/并发/失败/恢复，并逐条列出与 `docs/design/` 的差异 | PASS | `data-layer-map.md` §6（8 项职责判定）、§7（7 条专项检查）、§10（一致项 + 3 条差异，2 条登记为 F7） |
| AC-5 | D3 以表格记录命令、退出码、PASS/FAIL/NOT_EXECUTED，覆盖 build/vet/test-unit/test-prometheus/test-container/集成 | PASS | `test-baseline.md` §1（19 行执行表）、§2-§3；全部命令实跑，未执行项明确标注 |
| AC-6 | D4 四类分区，事实有锚点，推断/未知/风险不混淆 | PASS | `facts-and-risks.md` A（21 条，均带 path:line/命令/SQL）、B（8 条）、C（9 条）、D（F1-F12） |
| AC-7 | 变更范围仅报告目录与任务目录，无 `internal/`、`api/`、`test/`、`go.mod`、Migration、Dockerfile、`.cnb.yml`、`prometheus/`、`.agent/specs/` 改动 | PASS | `git diff --stat 473d6f5..HEAD`（提交后复核，见 §3） |
| AC-8 | D5 给出可按路径/行号/SQL/命令/SHA 复核的证据清单与待查问题，不下 T02/T03 结论 | PASS | `review-input-t02-t03.md` §1-§5；明确声明 T02/T03 范围未提供、按两组线索组织，未替 Owner 定义任务 |
| AC-9 | D1~D5 五文件齐备且非空，路径登记在 `delivery.md` | PASS | `wc -l`：`call-chain-map 228`、`data-layer-map 200`、`test-baseline 212`、`facts-and-risks 311`、`review-input-t02-t03 187`；另附 `engineering-inventory.md` 98 行 |

## 3. 验证命令与结果

### 3.1 Git / 范围

```bash
$ git rev-parse HEAD
95c13910c7226b4e32487ed046702caf75d74fc6
$ git branch --show-current
task/surgecart-baseline-audit
$ git status --short        # 交付前
（仅 .agent/tasks/surgecart-baseline-audit/ 与 docs/reports/surgecart-baseline-audit/ 的新增/修改）
$ git diff --stat 473d6f5253e3abcb98ece9d79a5a8059563a3e8d..HEAD
（提交后复核：仅上述两个目录）
$ git diff --check
（无输出，退出码 0）
```

### 3.2 构建与质量门禁（均退出码 0）

```bash
$ go build ./...
$ go vet ./...
$ gofmt -l .                     # 输出 0 行
$ bash scripts/test-unit.sh      # 11 个包 ok
$ bash scripts/test-race.sh      # ok internal/logic/flashsale 1.039s
$ bash scripts/test-integration.sh   # go test -p 1 ./... 全部 ok（test/e2e 91.922s）
$ bash scripts/test-container.sh     # 静态断言通过
$ bash scripts/test-prometheus.sh    # 通过（promtool 缺失，跳过 rules 语法校验）
$ bash scripts/test-lib.sh           # 全部通过
$ bash scripts/check-registry.sh     # 未发现 Migration 重复/错误码漂移
$ bash scripts/test-nginx.sh         # 静态校验通过（未构建镜像，跳过 nginx -t）
```

### 3.3 秒杀专项测试

```bash
$ DATABASE_DEFAULT_PASS=root go test -count=1 -v -p 1 ./internal/logic/flashsale/
# 22 个顶层用例 PASS，ok 3.898s

$ DATABASE_DEFAULT_PASS=root go test -count=1 -v -p 1 -run 'TestFlashSale|TestConcurrent' ./test/e2e/
# 20 个用例 PASS，ok 11.110s
```

### 3.4 运行期只读核对

```bash
$ mysql -h127.0.0.1 -P3306 -uroot -proot my_shop -e "SELECT version,dirty FROM schema_migrations; SHOW TABLES LIKE 'flash_sale%';"
20261001000023  0
（5 张秒杀表全部存在）
```

### 3.5 Review Baseline SHA 存在性

```bash
$ git cat-file -t 95c13910c7226b4e32487ed046702caf75d74fc6
commit
```

## 4. 未验证项

| # | 未验证项 | 原因 | 影响 |
| --- | --- | --- | --- |
| 1 | 独立审查（Cleaner 结论） | 单 Agent Session，报告作者即 LEAD | 审查独立性未满足；不声称 CLEAN |
| 2 | 多实例并发消费 / 真实 SKIP LOCKED 竞争 | 测试均为单进程驱动 | F3 的吞吐结论未量化；并发正确性仅有单进程证据 |
| 3 | Redis/MySQL 真实宕机、网络分区、SIGKILL | 仅 `WRONGTYPE` + 进程内 hook 模拟 | 崩溃恢复的测试证据弱于真实故障 |
| 4 | 生产规模执行计划与出队延迟 | 测试库仅 1 行数据 | 索引选择（D2 §9）不能代表生产 |
| 5 | 压测 / 容量结论 | 本任务未运行 `scripts/flashsale-loadtest/` | 不给出 QPS/p95/p99 |
| 6 | Docker 镜像构建、完整 compose 栈、Nginx、Prometheus 实际抓取 | 只启动 mysql/redis | Docker/CNB/Prometheus 多为「已配置但未验证运行」 |
| 7 | CNB 流水线实际状态 | 本地无 CNB runner | CI 是否绿未知 |

## 5. 已知限制

- 审计只对 `95c1391`（源码等价 `473d6f5`）成立；任何业务代码变更后结论需重新锚定。
- 测试使用 `make deps-start` 新建的本地 MySQL/Redis 容器；E2E 会清空相关业务表并 `FlushDB`，不可在有数据的库上执行。
- MySQL 客户端为 MariaDB 15.1，但连接目标为 MySQL 8.0.46 容器；只执行了只读查询与 EXPLAIN。
- `docs/design/flash-sale.md` 与 README 只作「宣称」对照，未作为事实来源。
- 旧交付 `docs/reports/surgecart-baseline-audit-v1/` 未逐条复核，其结论未被引用。
- 任务 slug / 分支名与 Owner 指令中的 `-v1` 后缀不一致，裁决与理由见 `decisions.md` D4；该差异待 Owner 验收时确认（Q1）。

## 6. 回滚方式

本任务不修改业务代码与 Schema，回滚仅为记录级操作：

```bash
# 撤销交付记录（保留历史）
git revert <C1> <C2>            # 或在评审后删除分支（需 Owner 授权）
# 清理本地测试依赖（可选，不影响仓库）
MYSQL_ROOT_PASSWORD=root AUTH_JWT_SECRET=ci-placeholder-jwt-secret-not-for-prod docker compose down
# 如需彻底清理本次测试库数据（谨慎，仅限本机测试环境）
docker compose down -v
```

不涉及数据迁移回滚、不涉及线上变更。

## 7. 交付文件索引

| # | 文件 | 行数 | 内容 |
| --- | --- | --- | --- |
| D1 | `docs/reports/surgecart-baseline-audit/call-chain-map.md` | 228 | 15 跳链路、逐跳细节、必答问题、闸门语义、数据流图 |
| D2 | `docs/reports/surgecart-baseline-audit/data-layer-map.md` | 200 | 表/索引/约束、状态迁移、并发控制、事务、幂等、请求表职责、7 条专项检查、文档差异 |
| D3 | `docs/reports/surgecart-baseline-audit/test-baseline.md` | 212 | 19 行执行表、61 个测试文件盘点、逐用例结果、实测 SQL/事务、未覆盖场景 |
| D4 | `docs/reports/surgecart-baseline-audit/facts-and-risks.md` | 311 | A 事实 21 条、B 推断 8 条、C 未知 9 条、D 风险 F1-F12 |
| D5 | `docs/reports/surgecart-baseline-audit/review-input-t02-t03.md` | 187 | T02/T03 入口、调用关系、SQL、测试命令、风险与待查问题 |
| 附 | `docs/reports/surgecart-baseline-audit/engineering-inventory.md` | 98 | 测试/门禁/Docker/CNB/Prometheus 五态盘点 |

任务记录：`.agent/tasks/surgecart-baseline-audit/{task,plan,findings,handoff,delivery,decisions}.md`。

## 8. Git 交付状态

| 项目 | 状态 |
| --- | --- |
| 分支 | `task/surgecart-baseline-audit` |
| 交付 commit（审计产物） | `73a48f6755e7ed041643c1bde96e3e09ec6b1f8f` |
| 记录提交 | 本文件所在提交（`git log -2 --format=%H`） |
| 基线 | `95c13910c7226b4e32487ed046702caf75d74fc6`（`473d6f5253e3abcb98ece9d79a5a8059563a3e8d` 源码等价） |
| 远程分支 | `origin/task/surgecart-baseline-audit`，推送后用 `git ls-remote` 与本地比对（结果见 `handoff.md` 最后一条） |
| 工作区 | 交付时 clean |
| Tag | 未创建（本任务不发布版本） |
| 合并 | 未合并到 `main`/`develop`（无授权） |
