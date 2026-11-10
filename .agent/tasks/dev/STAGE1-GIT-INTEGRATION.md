# 阶段一 Git 集成：从分支森林到 develop / main

- 任务：SurgeCart 阶段一最终集成（Git 集成 + 集成文档整理，不含新功能）
- 仓库：`cnb.cool/framework-station/my-shop`
- 执行日期：2026-11-10
- 正式报告：`docs/reports/stage1-main-integration/STAGE1_MAIN_INTEGRATION_REPORT.md`
- 原始证据：`docs/reports/stage1-main-integration/raw/`

本文写给"下一次要做分支集成的人"：讲清分支职责、祖先关系怎么核验、
为什么必须 `--no-ff`、冲突该怎么判断，以及这次实际发生了什么。

---

## 1. develop 与 main 的职责

| 分支 | 职责 | 本项目的实际用法 |
|---|---|---|
| `main` | **可交付基线**。只接收已验收成果的集成提交，任何时刻都应能部署 | 每个阶段收尾时从 develop 合并一次，形成一条"阶段边界"的集成提交 |
| `develop` | **集成分支**。各任务分支先汇到这里，允许比 main 领先若干提交 | 阶段一的 5 个任务全部经 develop 汇聚后再进 main |
| `feat/*`、`fix/*` | **任务分支**。一条任务一条分支，完成后保留不删 | `feat/k8s-t5-04-...`、`fix/flashsale-kafka-b4.1` 等，共 17 条进入阶段一集成 |

三条本项目一直遵守的纪律：

1. **任务分支只前进、不改写**：不 rebase、不 force push、不删分支；
2. **集成只能"加"**：main / develop 上只做 merge 提交，绝不 reset；
3. **交付报告两段式**：先提交实现与文档，再提交"回填 SHA"的元数据提交。

---

## 2. Git 分支集成顺序

```text
任务分支（feat/fix）
   │  每条任务完成后 push，不删
   ▼
develop（集成分支）
   │  git merge --no-ff <task-branch>
   ▼
main（可交付基线）
   │  git merge --no-ff develop
   ▼
origin/main
```

顺序不能反：**先 develop 后 main**。原因很实际——
如果先动 main，一旦发现某个任务成果不完整，修 develop 就要再造一次 hotfix 分支；
先 develop 可以有多个集成提交，最后 main 只吃一个干净的合并。

本项目阶段一共有 17 条任务分支，但**没有做 17 次合并**：
T5-04 分支本身是以 `82243c0`（当时 main/develop 的同一个提交）为祖先的线性历史，
它已经把任务一/二/三/四（T4-02 起）/任务五全部包含进来。
所以正确做法是**只合并这一个"已经包含全部后代"的分支**，而不是重复合并它的祖先。

---

## 3. 祖先关系核验方法（集成前必做的 7 项只读检查）

```bash
git fetch origin --prune                     # 1. 先同步远端引用
git rev-parse --abbrev-ref HEAD              # 2. 当前分支
git status --porcelain                       # 3. 工作区（必须为空）
git ls-remote origin refs/heads/main refs/heads/develop   # 4. 远端权威 SHA
git for-each-ref --format='%(refname:short) %(upstream:track)' refs/heads  # 5. 本地是否有未推送提交

# 6. 关键判断：目标基线是不是待集成分支的祖先？
git merge-base --is-ancestor <baseline> <topic>; echo $?    # 0 = 是祖先（可安全快进合并）

# 7. 双向差异：谁有谁没有的提交
git rev-list --count <topic>..<baseline>     # baseline 独有 → 必须为 0，否则有分叉
git rev-list --count <baseline>..<topic>     # topic 独有 → 待集成的提交数
git merges-base <baseline> <topic>           # 共同祖先（应为 baseline 本身）
```

**读法**：

| 结果 | 含义 | 动作 |
|---|---|---|
| `baseline..topic` = 0 且 `is-ancestor` = 0 | topic 已包含 baseline，且 main 无独立提交 | 直接合并 topic（本次情况） |
| `topic..baseline` > 0 | baseline 上有 topic 没有的提交（真分叉） | **停下来看**：这些提交是否属于本阶段成果 |
| 某任务分支 `is-ancestor` ≠ 0 | 该分支成果没进 topic | 要么单独合并，要么确认它已被取代 |

本次实测（阶段一）：

```text
git merge-base 82243c0 03c1e7b            → 82243c0        （共同祖先就是基线本身）
git rev-list --count 03c1e7b..82243c0     → 0              （develop 无独立提交）
git rev-list --count 82243c0..03c1e7b     → 16             （待集成 16 个提交）
```

**这一步的价值**：它把"要不要合并 17 条分支"这种主观判断，变成了一次数提交。
17 条任务分支里 15 条的 tip 都是 `03c1e7b` 的祖先（`is-ancestor` = 0），
只有 2 条不是 —— 这才需要人工判断（见 §5）。

---

## 4. no-ff 合并的作用

`git merge --no-ff` 强制生成一个**合并提交**，即使可以快进。

为什么本项目非要它：

1. **保留"何时集成"这件事本身**。快进会把任务提交"平铺"进 develop，
   历史里看不出"阶段一是在哪一刻整体进来的"，出问题无法按集成批次回滚；
2. **每个集成提交 = 一个可回滚单元**。阶段一进 develop 是 `4e29dd1`、T4-01 文档是 `cc0108a`、
   进 main 是另一个提交 —— 任何一步有问题都能单独 revert 那一个 merge；
3. **报告可以引用确定的对象**。交付报告写"来源分支 @ SHA → 合并提交 SHA"，
   没有合并提交就只能写一串提交区间，审计时无法一一对应；
4. **冲突解决结果有归属**。冲突在合并提交里被解决，解决方式随合并提交一起被记录。

代价是多一个提交、图变"肥"，对这样一个阶段制项目完全值得。

> 注：`--no-ff` 与"保留历史"不冲突。它不重写任何既有提交，只是新增一个父提交指向两条线。

---

## 5. 冲突处理原则

本次两次合并**零冲突**，但仍按下面的优先级处理原则执行：

1. **能预判就预判**：合并前用 `git diff --name-status <base>...<topic>` 看文件集合，
   用 `git merge-tree <base> <ours> <theirs>` 做一次"不落地"的冲突模拟；
2. **文档冲突**（同一份报告被两条线各自修改）→ 可以解，但必须**两边信息都不丢**，
   通常做法是保留两份来源描述并显式标注"由 X 分支与 Y 分支合并"；
3. **代码 / 配置 / K8s 清单冲突** → **不猜**。这类冲突的语义（谁的安全约束更严、谁的默认值对）
   不是靠 diff 能判断的，必须停下来报告 Owner，给出双方差异与影响面；
4. **绝不**用 `-X ours` / `-X theirs` 一把梭，也绝不 `reset --hard` 后重来；
5. 冲突解决后**必须跑门禁**，因为冲突解决本身就是一次手写代码。

本次遇到的一个"边界判断"（不是冲突，是范围判断）：

| 分支 | 情况 | 决定 |
|---|---|---|
| `feat/k8s-t4-prometheus-monitoring` @ `63bb3be` | 不在 T5-04 中，仅新增 2 个 T4-01 文档 | **合并**（纯文档、`merge-tree` 预检无冲突、属阶段一任务四成果） |
| `feat/k8s-t2-06-1-5-rbac-resource-name-preflight` @ `1d7b474` | 不在 T5-04 中，含 `test/deployk8s`、`test/manifest` 代码改动 | **不合并**：同任务的 `-preflight-impl`(`3085522`)、`-final`(`9f66495`) 已在主线，`-final` 时间更晚且是最终版；合并它会把这些测试文件回退到中间态 |

判断依据不是"看起来像旧分支"，而是：**同一任务编号下 `-final` 已集成、且 `-final` 晚于它**。
若这次误合并了 `-preflight`，`go test ./test/deployk8s/...` 很可能直接红。

---

## 6. 本次实际集成过程

### 6.1 开工基线（只读核验结果）

| 项 | SHA |
|---|---|
| 远端 `main` | `82243c0fbce614e03c4ff57dcc829b5f68fb08f2` |
| 远端 `develop` | `82243c0fbce614e03c4ff57dcc829b5f68fb08f2`（与 main 同源） |
| 本地 `main` | `be930608db4173331f60a6094b7a9a6e8e8ea658`（**落后 102 个提交**，仅可快进） |
| 本地 `develop` | 不存在（本次新建） |
| 阶段一最终分支 | `feat/k8s-t5-04-kafka-backlog-failure-recovery` @ `03c1e7b2b8a651fe219d6c21b2ba3964e4716d24` |
| 工作区 | 干净（`git status --porcelain` 空） |
| 其他本地未推送提交 | 无（所有本地分支 `upstream:track` 为空） |

### 6.2 阶段一：develop

```bash
git switch -c develop --track origin/develop      # 82243c0
git merge --no-ff feat/k8s-t5-04-kafka-backlog-failure-recovery \
    -m "merge(test): 集成阶段一任务四/任务五成果（Prometheus/Grafana/k6/可靠性实验）到 develop"
# → 4e29dd1（parents: 82243c0, 03c1e7b），16 个提交进入，零冲突

git merge --no-ff origin/feat/k8s-t4-prometheus-monitoring \
    -m "merge(docs): 集成 T4-01 监控审计与 Git 收尾报告到 develop"
# → cc0108a（parents: 4e29dd1, 63bb3be），2 个文档，零冲突
```

### 6.3 阶段二：main

```bash
git switch main
git merge --ff-only origin/main                   # be93060 → 82243c0（本地落后，安全快进）
git merge --no-ff develop -m "merge(release): 阶段一成果进入 main"
```

### 6.4 推送

```bash
git push origin develop
git push origin main
git ls-remote origin refs/heads/develop refs/heads/main   # 必须与本地一致
```

---

## 7. 验证方法（集成后怎么证明"没丢东西"）

**光看 `git log` 不够，要分三层验证：**

### 7.1 提交层：成果都在历史里

```bash
# 对每条阶段一任务分支，验证其 tip 是 develop 的祖先
for b in $(git for-each-ref --format='%(refname:short)' refs/remotes/origin | grep -E 'k8s-t[2345]|flashsale-kafka'); do
  git merge-base --is-ancestor "$b" origin/develop \
    && echo "IN   $b" || echo "MISS $b"
done
```

判定标准：应为全 `IN`。出现 `MISS` 就必须解释（本次 2 条：1 条已合并、1 条被取代）。

### 7.2 文件层：成果文件真的在工作区

按任务维度逐个确认，不靠"我记得"：

| 任务 | 验证路径 |
|---|---|
| 任务一 Kafka | `internal/mq/kafka/*.go`（10 个）、`internal/logic/flashsale/*.go`（24 个）、`internal/worker/flashsale_runtime.go` |
| 任务二 K8s 适配 | `manifest/kubernetes/{base,overlays,infrastructure,migration,shared,examples}` |
| 任务三 本地部署 | `manifest/kubernetes/infrastructure/verify`、`scripts/` 下集群脚本 |
| 任务四 Prometheus/Grafana | `manifest/kubernetes/monitoring/{prometheus,grafana}/`、`T4-01_*.md`、`T4-02_*.md`、`docs/reports/t4-0*` |
| 任务五 k6/可靠性 | `test/k6/`（183 个文件）、`docs/reports/t5-0*`、`scripts/kafka-reliability/` |
| 技术学习文档 | `.agent/tasks/dev/*.md` |

### 7.3 门禁层：代码还能编译、测试还能过

```bash
gofmt -l api internal main.go                 # 期望无输出
go build ./...                                # Linux 容器（Windows 有 syscall.Flock 平台限制）
go vet ./...
bash scripts/test-unit.sh                     # 官方单元测试门禁
kubectl kustomize manifest/kubernetes/overlays/production          # 四个 Kustomize 入口
go test ./test/manifest/...                   # 清单结构化断言
```

**只报真实退出码**。没跑的写 NOT_VERIFIED，不写 PASS。

---

## 8. 这次踩到的两个坑

1. **`git log --format='%H %P'` 在 PowerShell 里被当成 cmd 解析**
   （报 `'Select-Object' 不是内部或外部命令`），命令根本没执行。
   教训：**验证命令要短、要能单独重放**，别把格式化字符串和复杂管道塞进一行；
   执行后立刻 `git log --oneline -3` 复核状态，别假设"应该成功了"。
2. **`docker run ... bash -c '...; ...'` 经 `Start-Process -ArgumentList` 传参会被拆散**
   （PowerShell 用空格拼接参数、不加引号），结果 `bash -c export` 只打印了环境变量。
   教训：**把脚本写成 `.sh` 文件再执行**，不要在 `-ArgumentList` 里塞带分号的命令行。

---

## 9. 面试时可以怎么讲

- "阶段一集成我做了两件事：先证明 T5-04 分支是超集（`merge-base --is-ancestor` + 双向 `rev-list --count`），
  再证明 develop 没有分叉（`topic..develop` 必须为 0）。这样 17 条分支只需要 1 次主合并。"
- "`--no-ff` 不是洁癖：它让每次集成成为一个可 revert 的单元，报告也能把
  '来源分支 SHA → 合并提交 SHA' 一一对应。"
- "遇到不在超集里的分支，判断依据是祖先关系和任务编号的最终版，而不是分支名看着像旧的：
  同任务的 `-final` 已集成且更晚，那条 preflight 分支就该留在原地不合并。"
- "验证分三层：提交层（ancestor）、文件层（按任务列路径）、门禁层（真实退出码）。"
