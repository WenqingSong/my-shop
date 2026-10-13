# Cleaner Findings

## Review Target

- Base commit：`359b8cceeebefe7b490f783c1ad5a118afd6e31e`（`develop` 头部，任务基线；TaskBuilder 记录起始 working tree clean）。
- Review Target（C1，Coder 实现证据提交）：`6f1e2a660334ab46ea2c5bf10ba89fd698679b13`（修复后重新发起的 Review Request 的 C1）。
- Coder metadata commit（C2）：`87b5272b36e3558fc926e349e25bf75aec57aebb`（仅改 `state.yaml`：`review.status=PENDING`、`review.target=6f1e2a6`）。
- Contract：APPROVED，`contract.target=6bff54d`（含 6bff54d 追加的双标签 digest 一致性验证要求）。
- Design Artifact：`docs/design/deployment.md`（Design Impact=UPDATE；commit `4034020`，Analyst 在 Contract APPROVED 后落盘）。
- 当前分支 `feat/ci-image-publish-v1`：local HEAD == `origin/feat/ci-image-publish-v1` == C2；working tree clean。
- 本次相关变更（base..C1）：
  - 生产实现：`.cnb.yml`（main.push 的 main-ci 新增 docker-build / docker-push 两个 stage）。
  - Design：`docs/design/deployment.md`（新增 §11 镜像发布、版本标识与回滚）。
  - Workflow artifacts（非审查生产对象）：`.agent/tasks/ci-image-publish-v1/*`。
- 全局资源：无（`resources.reservations={}`；任务 Out of Scope 明确不新增 migration/错误码域，Registry 三边检查不适用）。
- 任务前已有修改：无。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `git diff` 显示 `$`（vscode）、`main.pull_request.pr-check`、`main.push.main-ci` 的 start-dependencies/test/build/sync-to-github-mirror 均保留未删改；仅新增 docker-build/docker-push。 |
| AC-002 | PASS | pr-check 仍执行 `make test`（`.cnb.yml:42`）；`make test`=scripts/test.sh 的 `set -euo pipefail` + `go test` 非零退出（Contract 已 VERIFIED）→ 失败信号正确。注：「禁止合并」由仓库保护规则（required check）平台侧启用，属 Owner 动作，本任务不保证，该子项 NOT_VERIFIED。 |
| AC-003 | PASS | main-ci 阶段顺序 test → build → docker-build 静态确认（`.cnb.yml:59-67`）；顺序 stage「任一失败即中断」语义由 Contract VERIFIED。实际「test 失败 build 不执行」需真实 CNB 运行确认（运行时，Deliverer）。 |
| AC-004 | NOT_VERIFIED | `.cnb.yml` 已含 docker-push stage（`docker push ${image}:${CNB_COMMIT_SHORT}` + `${image}:latest`，CNB 内置凭据），但「镜像实际出现在 CNB 制品库」需真实 CNB push，运行时验证交 Deliverer。 |
| AC-005 | NOT_VERIFIED | docker-build 已对同一镜像打 `<sha>` 与 `latest` 双标签，但「制品库实际出现两标签」需真实运行（Deliverer）。 |
| AC-006 | NOT_VERIFIED | 双标签由 `docker tag` 同一镜像派生（digest 必相同），但「实际 digest 一致」需 `docker manifest inspect`/registry API 在真实环境确认（Deliverer）。 |
| AC-007 | NOT_VERIFIED | 顺序 stage + 先推 `<sha>` 后推 `latest` 已编码「失败不更新 latest」，但「实际 latest 指向旧 digest」需构造失败提交的真实 CI（Deliverer）。 |
| AC-008 | PASS | `.cnb.yml` 无明文密码/令牌；镜像名用 `${CNB_DOCKER_REGISTRY}/${CNB_REPO_SLUG_LOWERCASE}`、标签用 `${CNB_COMMIT_SHORT}` 内置变量，无 `docker login`；`grep` 扫描仅命中注释。 |
| AC-009 | NOT_VERIFIED | 纯运行时行为：需真实 CNB 制品库 + 可运行环境 `docker pull/run` + `/health`（task 明确「由 Deliverer 集成验证」）。 |
| AC-010 | PASS | `docs/design/deployment.md` §11 记录镜像仓库地址（docker.cnb.cool/framework-station/my-shop）、版本标识（短 SHA 8 位 + latest 语义）与回滚方法（用 SHA 标签回滚）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| 完整 Diff 审查（base..C1） | PASS | 生产变更仅 `.cnb.yml` + `docs/design/deployment.md`；无业务 Go 代码、无 Dockerfile/compose/entrypoint 改动。 |
| 越界回退核验（CLEAN-001） | PASS | `git diff base..C1 -- Makefile scripts/test.sh scripts/test-ci.sh` 为空；`git ls-files scripts/test-ci.sh` 无（已删除）；三文件与基线完全一致。 |
| `.cnb.yml` 与 Contract 一致性 | PASS | docker-build（build sha + tag latest）→ docker-push（先 sha 后 latest）；镜像名/标签/单平台 linux/amd64 与 Contract 一致。 |
| 凭据扫描（`docker login` / `password:` / `token:`） | PASS | 仅命中注释（第 78 行「无需手动 docker login」），无明文密码/令牌。 |
| 全局资源三边检查 | N/A | `resources.reservations={}`，任务不新增 migration/错误码域。 |
| 四者一致（Task↔Contract↔Design↔Impl） | PASS | 镜像地址/标签语义/阶段顺序/单平台 linux/amd64 四者一致。 |
| 运行时 AC（AC-004~007、009） | NOT_VERIFIED | 无 CNB 环境，运行时里程碑验证交 Deliverer（非 Cleaner 可追认）。 |

## Findings

首次审查的两条 Finding 均已处理：

### CLEAN-001：Scope/Contract 越界——改动 Makefile、scripts/test.sh 并新增 scripts/test-ci.sh（P2）— CLOSED

- 修复：Coder 选择回退方案——`6f1e2a6` 删除 `scripts/test-ci.sh`、回退 `Makefile` 与 `scripts/test.sh` 到基线。
- 复审验证：`git diff 359b8cce..6f1e2a6 -- Makefile scripts/test.sh scripts/test-ci.sh` 输出为空；`git ls-files scripts/test-ci.sh` 无结果（已删除）；`Makefile`/`scripts/test.sh` 与基线 `359b8cce` 完全一致。累计 diff 生产变更仅 `.cnb.yml` 与 `docs/design/deployment.md`，符合 Contract「Allowed / Forbidden Changes」边界。

### CLEAN-002：test-ci.sh 断言未限定到 main-ci（P3）— CLOSED

- 说明：随 `scripts/test-ci.sh` 一并删除（CLEAN-001 回退方案），问题不复存在。CI 配置正确性改由 Cleaner 静态审查 + Deliverer 运行时里程碑验证承担，与 Contract「Verification Requirements」一致。

## 结论说明

- 实现（`.cnb.yml` 镜像构建/发布配置 + `docs/design/deployment.md` 设计）已满足 Goal 与全部实现层 AC，且在 Scope 内，无开放 P0/P1/P2。
- AC-004/005/006/007/009 为运行时行为，需真实 CNB 环境，属 Deliverer 里程碑验证（task 明确 AC-009「由 Deliverer 集成验证」，其余运行时 AC 的验证方式亦为「在 CNB 上…」）；Cleaner 阶段如实标记 NOT_VERIFIED，不替 Deliverer 追认。
- 四者一致性（Task ↔ APPROVED Contract ↔ Design ↔ Implementation）成立，无漂移；全局资源无涉。
- 运行时不确定性（CNB 制品库仓库是否需预创建、docker-build→docker-push 跨 stage 镜像是否持久、runner 默认架构）属 Contract「Open Risks」，由 Deliverer 在真实环境核验。
