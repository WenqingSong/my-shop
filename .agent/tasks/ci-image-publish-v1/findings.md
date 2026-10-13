# Cleaner Findings

## Review Target

- Base commit：`359b8cceeebefe7b490f783c1ad5a118afd6e31e`（`develop` 头部，任务基线；TaskBuilder 记录起始 working tree clean）。
- Review Target（C1，Coder 实现证据提交）：`9eb842f48d41b0ef78c051d1b8b41e05046877fe`
- Coder metadata commit（C2）：`97d5ab89819d3dcb9ad29a793cb98f4a49c007bc`（仅改 `state.yaml`：`review.status=PENDING`、`review.target=9eb842f`）。
- Contract：APPROVED，`contract.target=6bff54d`（含 6bff54d 追加的双标签 digest 一致性验证要求）。
- Design Artifact：`docs/design/deployment.md`（Design Impact=UPDATE；commit `4034020`，Analyst 在 Contract APPROVED 后落盘）。
- 当前分支 `feat/ci-image-publish-v1`：local HEAD == `origin/feat/ci-image-publish-v1` == C2；working tree clean。
- 本次相关变更：
  - C1（Coder）：`.cnb.yml`、`Makefile`、`scripts/test-ci.sh`（新增）、`scripts/test.sh`。
  - 前置（Analyst/TaskBuilder，非 Coder 所有）：`docs/design/deployment.md`、`contract.md`、`task.md`、`state.yaml`。
- 全局资源：无（`resources.reservations={}`；任务 Out of Scope 明确不新增 migration/错误码域，Registry 三边检查不适用）。
- 任务前已有修改：无。

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `git diff` 显示 `$`（vscode）、`main.pull_request.pr-check`、`main.push.main-ci` 的 start-dependencies/test/build/sync-to-github-mirror 均保留未删改；`scripts/test-ci.sh` 静态断言通过。 |
| AC-002 | PASS | pr-check 仍执行 `make test`（`.cnb.yml:42`）；`make test`=scripts/test.sh 的 `set -euo pipefail` + `go test` 非零退出（Contract 已 VERIFIED）→ 失败信号正确。注：「禁止合并」由仓库保护规则（required check）平台侧启用，属 Owner 动作，本任务不保证，该子项 NOT_VERIFIED。 |
| AC-003 | PASS | main-ci 阶段顺序 test → build → docker-build 静态确认（`.cnb.yml:59-67`）；顺序 stage「任一失败即中断」语义由 Contract VERIFIED。实际「test 失败 build 不执行」需真实 CNB 运行确认。 |
| AC-004 | NOT_VERIFIED | `.cnb.yml` 已含 docker-push stage，但「镜像实际出现在 CNB 制品库」需真实 CNB push，Cleaner 无 CNB 环境，交 Deliverer 集成验证。 |
| AC-005 | NOT_VERIFIED | docker-build 已对同一镜像打 `<sha>` 与 `latest` 双标签，但「制品库实际出现两标签」需真实运行。 |
| AC-006 | NOT_VERIFIED | 双标签由 `docker tag` 同一镜像派生（digest 必相同），但「实际 digest 一致」需 `docker manifest inspect`/registry API（真实环境）。 |
| AC-007 | NOT_VERIFIED | 顺序 stage + 先推 `<sha>` 后推 `latest` 已编码「失败不更新 latest」，但「实际 latest 指向旧 digest」需构造失败提交的真实 CI。 |
| AC-008 | PASS | `.cnb.yml` 无明文密码/令牌；镜像名用 `${CNB_DOCKER_REGISTRY}/${CNB_REPO_SLUG_LOWERCASE}`、标签用 `${CNB_COMMIT_SHORT}` 内置变量，无 `docker login`；`grep` 与 test-ci.sh 均确认。 |
| AC-009 | NOT_VERIFIED | 需真实 CNB 制品库 + 可运行环境 `docker pull/run` + `/health`（task 明确「由 Deliverer 集成验证」）。 |
| AC-010 | PASS | `docs/design/deployment.md` §11 记录镜像仓库地址（docker.cnb.cool/framework-station/my-shop）、版本标识（短 SHA 8 位 + latest 语义）与回滚方法（用 SHA 标签回滚）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| 完整 Diff 审查（base..C1） | PASS | 12 文件（C1 的 4 文件 + 前置 8 文件）；无业务 Go 代码、无 Dockerfile/compose/entrypoint 改动。 |
| `bash -n scripts/test.sh` / `bash -n scripts/test-ci.sh` | PASS | 语法 OK。 |
| `bash scripts/test-ci.sh` | PASS | exit=0，静态断言通过。 |
| 凭据扫描（`docker login` / `password:` / `token:`） | PASS | 仅命中注释（第 78 行「无需手动 docker login」），无明文密码/令牌。 |
| 全局资源三边检查 | N/A | `resources.reservations={}`，任务不新增 migration/错误码域。 |
| 四者一致（Task↔Contract↔Design↔Impl） | PASS | 镜像地址/标签语义/阶段顺序/单平台 linux/amd64 四者一致。 |
| 运行时 AC（AC-004~007、009） | NOT_VERIFIED | 无 CNB 环境，交 Deliverer。 |
| `go test` / 全量 `make test` | 未执行 | C1 未改任何 Go 文件（仅 `.cnb.yml`/`Makefile`/两个 shell），业务测试不受影响；`make test` 需 MySQL/Redis 环境。 |

## Findings

### CLEAN-001：Scope/Contract 越界——改动 Makefile、scripts/test.sh 并新增 scripts/test-ci.sh

- Severity：P2
- Status：OPEN
- Location：`Makefile`、`scripts/test.sh`、`scripts/test-ci.sh`（C1 `9eb842f`）
- AC / Invariant：Scope / Contract「Allowed / Forbidden Changes」；Task Out of Scope
- Trigger：Coder 为实现镜像发布，额外新增 CNB 配置静态断言脚本并接入 `make test`。
- Actual：Contract 只允许改 `.cnb.yml` 的 `main.push` 与 `docs/design/deployment.md`；明确禁止「改 `scripts/*` 既有构建产物语义」。C1 实际改了 `Makefile`（新增 test-ci target）、`scripts/test.sh`（新增调用 test-ci.sh，改变 `make test` 语义）、新增 `scripts/test-ci.sh`。
- Expected：实现严格落在 Contract 允许范围；如需本地 CI 配置静态断言，应先走 Contract Revision 经 Owner 批准，或交由 Deliverer 做运行时集成验证（task 已将 AC-009 明确交 Deliverer）。
- Impact：`make test` 语义被改变（新增 test-ci.sh 校验，作用于 PR 检查与 main push 两处）；范围未获 Owner 授权，违反已 APPROVED Contract 边界。
- Evidence：`git show --stat 9eb842f` → `.cnb.yml`、`Makefile`、`scripts/test-ci.sh`、`scripts/test.sh`；Contract「Allowed / Forbidden Changes」原文。
- Required Fix Boundary：二选一——(a) 回退 `Makefile`/`scripts/test.sh`/`scripts/test-ci.sh`，仅保留 `.cnb.yml` 与 `docs/design/deployment.md` 变更；(b) 经 Contract Revision 取得 Owner 对新增测试基础设施的批准后再保留。不得以「测试通过」或「代码合理」为由擅自扩大范围。

### CLEAN-002：test-ci.sh 断言未限定到 main-ci，无法识别「main-ci 删除/重排 test」的错误实现

- Severity：P3
- Status：OPEN
- Location：`scripts/test-ci.sh:43,48,78-92`（若 CLEAN-001 选择保留该脚本）
- AC / Invariant：AC-003（main push 自动测试 + 构建顺序）
- Trigger：删除 main-ci 的 `test` stage（或把 test 移到 build 之后），pr-check 的 `test` 仍在。
- Actual：`grep -q 'script: make test'`（第 48 行）与 `order_ok test build`（第 82 行）用全局首个匹配（`grep -q` / `head -n 1`），会命中 pr-check 的 `test`（`.cnb.yml:41`）而非 main-ci 的 `test`（`.cnb.yml:59`）；删除或重排 main-ci 的 `test` 仍会通过断言（假阴性）。
- Expected：断言应限定到 `main.push` 的 `main-ci` 内（如按 job 分段后在其范围内取 stage 名顺序），使「main-ci test 缺失/顺序错」能被捕获。
- Impact：弱化 AC-003 回归保护；当前 `.cnb.yml` 实现本身正确，仅测试可信度不足。
- Evidence：`order_ok` 用 `grep -nE "^[[:space:]]*- name: ${a}$" | head -n 1`；`.cnb.yml` 存在两处 `- name: test`（pr-check 与 main-ci）。
- Required Fix Boundary：将顺序断言限定到 main-ci 作用域；不规定具体实现方式。

## 结论说明

- 阻塞 CLEAN 的仅 CLEAN-001（P2）。CLEAN-002 为 P3，Owner 可决定。
- AC-004/005/006/007/009 为运行时行为，需真实 CNB 环境，交 Deliverer 里程碑验证（非 Cleaner 可追认）。
- 四者一致性（Task ↔ APPROVED Contract ↔ Design ↔ Implementation）成立，无漂移；全局资源无涉。
- 运行时不确定性（CNB 制品库仓库是否需预创建、docker-build→docker-push 跨 stage 镜像是否持久、runner 默认架构）属 Contract「Open Risks」，由 Deliverer 在真实环境核验，不构成本次 Cleaner Finding。
