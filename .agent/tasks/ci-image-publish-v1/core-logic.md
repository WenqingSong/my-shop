# Owner 核心逻辑验证

本任务是 CI 流水线配置变更（无 Go 业务代码），核心不变量集中在 `.cnb.yml` 的镜像发布编排。以下两张卡说明「同一 digest 的双标签」与「失败不更新 latest」两个关键机制，及其与测试/验证的因果关系。

## CL-001：双标签同源（短 SHA 与 latest 指向同一 digest）

- Owner 需要理解：`latest` 不是独立构建出来的第二个镜像，而是从「已构建的短 SHA 镜像」直接 `docker tag` 派生。若实现错误地分别 `docker build` 两次（或标签指向不同产物），`latest` 与短 SHA 就会指向不同 digest，回滚锚点与「最新版」不一致，`latest` 语义被破坏。
- 生产代码：`.cnb.yml:73-74`（`docker-build` stage）——`docker build -t "${image}:${CNB_COMMIT_SHORT}" .` 后 `docker tag "${image}:${CNB_COMMIT_SHORT}" "${image}:latest"`。
- 关键测试：本任务无自动化单测（`scripts/test-ci.sh` 已按 Scope 回退）；该不变量由 Deliverer 运行时验证——push 后用 `docker manifest inspect ${image}:${CNB_COMMIT_SHORT}` 与 `:latest` 比对 digest（Contract 已追加此验证要求）。
- 基线验证：静态审查确认 `.cnb.yml` 中 `latest` 仅经 `docker tag`（同一 `${image}:${CNB_COMMIT_SHORT}`）派生，无第二处 `docker build`。
- 可选 Mutation：把 `docker tag ... :latest` 改成第二次 `docker build -t "${image}:latest" .`（独立构建）。
- 预期失败：Deliverer 的 `docker manifest inspect` digest 比对会发现 `<sha>` 与 `latest` digest 不一致；静态审查也会识别出「latest 未由同一镜像 tag 派生」。
- 恢复确认：恢复为单次 build + `docker tag` 派生 latest，再次静态审查确认仅一处 `docker build`。

## CL-002：失败不更新 latest（测试/构建失败不发布任何标签）

- Owner 需要理解：发布顺序与阶段顺序共同保证「latest 只指向一次全流程成功的构建」。若把 `latest` 的 `docker push` 放在短 SHA 之前，或把构建/推送拆成「任一步失败也继续」的独立 job，则测试/构建失败时仍可能更新 `latest`，导致 `latest` 指向未通过测试的镜像。
- 生产代码：`.cnb.yml:59-83`（main-ci 阶段顺序）——`test` → `build` → `docker-build` → `docker-push`；`docker-push` 内 `.cnb.yml:82-83` 先 `docker push ${image}:${CNB_COMMIT_SHORT}` 再 `docker push ${image}:latest`。
- 关键测试：无自动化单测；不变量由顺序 stage「任一失败即中断」（Contract VERIFIED 的平台语义）+ 推送顺序共同保证，运行时由 Deliverer 构造失败提交核验 `latest` digest 未变。
- 基线验证：静态审查确认 `docker-push` 内短 SHA 推送行位于 `latest` 推送行之前，且 `docker-build`/`docker-push` 位于 `sync-to-github-mirror` 之前、`test`/`build` 之后。
- 可选 Mutation：把 `docker push ...:latest` 移到 `docker push ...:${CNB_COMMIT_SHORT}` 之前（或把两行 push 拆到两个独立 stage）。
- 预期失败：Deliverer 构造「短 SHA push 成功后 latest push 失败」或「构建失败」场景时，会观察到 `latest` 未指向本次失败构建（仍指向上一次成功 digest）；顺序 stage 语义下任一失败即整体 job 失败、后续不执行。
- 恢复确认：恢复为「先短 SHA、后 latest」的单一 docker-push stage 顺序，再次静态审查确认顺序。

## 备注

- 凭据安全（INV-003）：`.cnb.yml` 全程使用 CNB 内置变量（`${CNB_DOCKER_REGISTRY}`/`${CNB_REPO_SLUG_LOWERCASE}`/`${CNB_COMMIT_SHORT}`）与内置触发者凭据，无 `docker login`、无明文密码，扫描确认无硬编码凭据。
- 上述两个不变量的运行时证明依赖真实 CNB 环境，由 Deliverer 里程碑验证；Cleaner 已完成配置层的静态一致性审查。
