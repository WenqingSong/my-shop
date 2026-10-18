# 项目首次适配与安全更新

本规范只使用 `.agent/` 内的框架资料。Core 规定 Gate/证据/权限；Project Policy 决定业务、父分支、资源和真实验证。不得因适配修改 Engine 来绕过 Gate。

## 首次适配

入口是 `.agent/README.md` 的中文指令。Agent 在独立适配分支执行，保留当前已有修改，不直接写共享父分支：

1. 读取真实仓库路径、Git HEAD/分支/remote、已有暂存/未暂存/新增修改、所有 Go module/go.work、内部 `.agent/AGENTS.md`、存在时的宿主根 AGENTS.md 与业务构建/测试/部署约定。已有 `.agent/workflow.yaml` 或任务数据表示这是更新/补充适配，不整目录覆盖。若尚未复制，先识别来源 `.agent/` 中框架文件，拒绝把来源的运行数据/二进制复制进项目。
2. 确认实际 integration branch、远端是否存在、测试/构建命令与所需服务、全局资源 kinds/Registry 和权限。唯一明确的既有项目约定可采用并列证据；多个候选或影响审批/业务行为的选择请求 Owner。不预设 main/develop、错误码域、数据库或 mock 等同真实验证。
3. 使用 `.agent/templates/PROJECT_ADAPTATION.template.md` 临时记录事实/未知项；只物化已确认参数。基于 `.agent/templates/workflow.yaml.template` 建项目 `.agent/workflow.yaml`（schema_version: 1；git.integration_branch 真实分支；resources 可为 {}，非空 Registry 严格位于 .agent/registry/）。有原文件只合并已授权差异。资源值必须来自实际项目权威，不自行占用。
4. 显式读取 `.agent/AGENTS.md`，以内部 Spec/Role/模板完成适配。宿主根 AGENTS.md 是可选业务规则，不存在时不创建、不阻断；存在时完整读取并保存原字节，保留所有业务约定，不覆盖、不移动到 .agent。模板 `.agent/templates/AGENTS.template.md` 仅供 Owner 明确要求新增业务规则时参考，不是接入前置。若原文件含旧框架入口或冲突，报告具体位置、旧/新含义和最小改动，只有 Owner 授权该融合差异后才修改；无法安全解释时停止，不假装忽略根业务规则。
5. 在宿主根构建独立 Go module（Go 1.23+、Git）：

```bash
GOWORK=off go -C .agent/engine test ./...
GOWORK=off go -C .agent/engine vet ./...
GOWORK=off go -C .agent/engine build ./...
for cli in workflow-check workflow-git workflow-status workflow-event workflow-program workflow-deliver; do
  GOWORK=off go -C .agent/engine build -o "../bin/$cli" "./cmd/$cli" || exit
done
```

逐条检查真实退出码，任何一个失败均不算构建成功。每个新 Task/Review Worktree 的 bin 互不继承，角色会在实际工作区按需从本地源码构建，不依赖外部版本的二进制。bin 的内部 ignore 规则应使构建不弄脏项目。不用 go.work/replace 连接业务 module；受限导出环境 Git VCS stamping 不可用时可明确用 `-buildvcs=false` 并记录原因，不能隐瞒其他错误。
6. 先运行 `.agent/bin/workflow-check config` 让真实解析器验证配置结构（只读、不是 Gate 或批准）；再用真实确认父分支/Base 只读 `.agent/bin/workflow-git verify-base --parent <真实父分支> --base <完整SHA>`，确认资源 Registry 可读、配置能被 CLI 正确加载；没有真实 Task 不运行伪造批准的 Gate 来冒充 E2E。若需 Intake 到 taskbuilder-init 实验，在独立临时仓库+本地 remote 中做，fixture Owner 决定明确标记，不写入业务运行数据。
7. 记录下面的准确来源、实际验证和缺口。检查四流程入口/完整 Role 能读取、引用都在 .agent 内。只有构建、配置/基线/资源与操作入口检查通过可称 ADOPTION_READY（接入准备就绪），这不代表真实业务 E2E 或任何 Task 被批准。把本次明确拥有的框架/配置/融合规则作为独立提交供 Owner 按项目原流程审阅集成；Task 的批准基线必须已包含这些已提交文件。未集成时明确说明“接入分支准备完成，等待可用基线”，不能从不含框架的旧基线建 Task。之后首个业务 Task 按 README 正常开始。

## 已有项目更新

新版完整源码在项目外。先使用独立更新分支/worktree并保留原工作区已有修改，不自动 stash、清理 Worktree 或 reset。

三方输入：B=上次采用的准确旧上游 Commit 的 `.agent/`；L=业务项目当前 `.agent/`；N=新版准确 Commit 的 `.agent/`。使用 Git show/export 或可核验归档取得 B/N；不靠 VERSION 猜测具体 Commit。上游来源来自下节记录和真实 Git 核验。

- 框架文件的 B→L 是本地定制，B→N 是上游变化；按文件和语义比较。Role/Spec 的项目定制允许保留，并检查 Gate/权限边界冲突。宿主不运行维护者生成器来覆盖定制 Prompt。
- 项目拥有的 `.agent/workflow.yaml`、tasks/、programs/、intake/、registry/、全部 State/批准/Event Log、宿主 AGENTS.md、业务 docs/design/ 和原 upstream.yaml，不能作为上游替换目标。新上游不携带它们；缺模板不表示要删项目数据。bin/index 缓存可重建，不能当权威证据。
- 显式读取新版和项目原有内部 AGENTS，并三方比较其本地定制；新框架不能把根业务规则转存为通用框架规则。根 AGENTS.md 缺失时不创建，存在时原字节默认保留。旧 Workflow 路径/命令只在 Owner 授权具体融合差异后修正，不直接覆盖业务规则。
- 精确记录更新前受保护文件的 Git blob/字节哈希、路径、任务/Program引用及原 HEAD/已有修改。新增框架文件须与宿主同名定制/运行文件区分，禁止简单 rsync --delete 或 cp 覆盖整目录。
- 能安全解释的文档/路径变化直接适配；Contract/Owner、Gate/Schema、Review neutral、C0/C1、历史路径或本地 Engine 变化发生语义冲突时列出差异，等 Owner 决定，不能静默选择上游或项目一方。运行中 Task 不升级 Schema、不重写审批、不移动目录；必要兼容测试在副本执行。
- B 未知且无法取回时，仍可只读比较 L/N，但不能称已识别全部本地定制。收集已知来源；保留未知文件与差异，向 Owner 给出需保留/替换的具体内容，得到决定后才形成受控合并，不虚构“三方安全合并”。不能把当前 L 伪装成 B。
- 在隔离分支实施框架差异；保留定制记录，执行引擎 test/vet/build、六 CLI 构建、规范与 Role 路径检查、未授权改动的受保护文件前后字节比较（根 AGENTS 缺失仍缺失；存在仍原字节，明确批准的最小融合差异另审）和适用的历史 schema/证据回归。检查 README 四流程可启动。宿主未携带根 scripts/tools 时不得把它们列作使用前置；框架源仓库的 CI 可以额外验证生成一致性。
- 失败仅撤回本次明确拥有的框架修改：未合并的独立更新分支保留供检查，原分支继续使用。需要撤回已提交更新时由明确提交范围生成 revert，在干净隔离工作区检查；不 hard reset 宿主，不恢复整套旧 .agent 覆盖新任务数据。有后续并发修改时先对比，不覆盖。
- 成功后再写来源记录、说明各定制的保留/变化、兼容验证和仍未验证的真实业务行为，交 Owner 检查并自行决定何时合并。更新不自动批准任务或交付。

## 最小来源记录（项目拥有，进入宿主 Git）

路径 `.agent/upstream.yaml`；不是 Engine State，也不参与 Gate。首次适配 Agent 创建，更新 Agent 仅在完成受控更新后维护：

```yaml
schema_version: 1
repository: https://cnb.cool/framework-station/agent-workflow
commit: <核验过的完整上游Commit，未知时用null并记录原因>
version: <来源.agent/VERSION的实际文本>
subdirectory: .agent
local_changes: <保留的Role/Spec/Engine定制摘要或项目内记录路径>
```

VERSION 只标产品版本，不足以定位旧上游。commit 不使用宿主 HEAD，不凭生成文件头 hash 反推，记录的是来源上游。仅有 .agent 导出时先核对原 ZIP comment/发布说明中的完整 Commit 与对应上游；无 Git/来源凭据时如实 unknown，后续提供原归档/Commit再核验。此记录不能授权覆盖运行数据，也不是自动升级系统。

## V3 → V4.2 迁移

schema 2/3 只读不回写、不追溯补 Discovery；历史 Deliverer 见 `.agent/roles/legacy/DelivererAgent.md`。新 Task 用 schema 4 和正式 Handoff/ACK；已有 Program Registry 不覆盖、不重登。原平铺 Role 通过 `.agent/roles/README.md` 的流程映射继续查找，历史 Handoff 的机器 Role 枚举不变。不得移动历史证据路径来适配新 Role 布局。


### 支持范围与操作顺序

历史可解析不等于新 Role 可直接执行旧任务。先盘点旧分支/完整 SHA、Task schema、Contract、Review/Owner/Delivery 绑定、资源 Registry、活动 Program、项目 Role/Spec/可选根 AGENTS 定制；不改原数据。

| 路径 | 支持 | 实际操作 |
| --- | --- | --- |
| T0 只读 | 支持 | 使用 inspect 查看 schema 2/3，不调用 V4 专属 handoff、taskbuilder-init、design Gate，不写回 |
| T1 新任务承继 | 支持，推荐 | Owner 逐任务批准新范围/类型/父分支/Base，创建新 Discovery Intake 和 schema 4 Task、独立分支/Worktree；在新 task.md 引用旧路径及准确 Evidence SHA，重新履行 Contract/Architecture、Cleaner、Owner 和交付要求 |
| T2 原地升级 | 不提供 | 不直接修改 schema_version，不给旧任务补造 Intake、Handoff、批准、Revision 或 SHA |

有活动 V3 Task，可保留原 Commit 的完整旧 Workflow 环境在原有分支完成；或者由 Owner 决定 T1，新任务只承继明确需求/证据引用，不继承批准。无活动任务时也须先保留旧数据与定制，再完成三方适配并提交可用框架基线。已有 V4 schema 4 任务不改 Schema/审批/基线；保留其分支内原版本，采用新版前在副本核对适用 Gate 兼容性，差异需 Owner 决定。框架升级不是现有 Task 自动迁移。

旧上游未知时不得把 L 当 B；只读列出 L/N 差异，Owner 决定保留/替换范围后才继续。保护 workflow.yaml、资源与 Program Registry、所有 State、Contract、Review、Owner、Event Log、Git Evidence 和宿主业务资料；不重登、不移目录、不覆盖、不改历史 SHA，不把 schema 读取成功报告为任务迁移完成。

### 旧入口映射（只处理当前入口，不改历史 Evidence）

- TaskBuilderPrompt、Coder、Cleaner、OwnerGate：按实际 Task 类型选择 normal/complex/design 的同名完整 Role，不能一律映射 NORMAL；DESIGN 不走 Coder。
- Analyst 只在 complex；Architect/DocsSpecWriter 只在 design；Program Collector/Assembler/Cleaner/OwnerGate/Merger 在 program。
- 历史 Deliverer 在 legacy，仅作历史读取；新任务最终交付用 Merger，不能让 schema 4 Handoff 指向 Deliverer。
- 旧职责边界映射到 AgentResponsibilityBoundary；旧 Adoption 映射本文件；框架架构映射内部 Architecture；Event/Handoff/Gate 映射 protocols 目录。遇到旧入口上下文不足时询问 Owner，不猜类型。

### 七条红线与完成条件

1. 不伪造 Owner ACCEPT、Cleaner CLEAN 或资源 RESERVED。
2. 不回填旧 review.target、design.*_target、delivery.feature_head 或批准。
3. 不覆盖 Task/Program/Registry，不 reset/rebase/amend 已发布历史。
4. 不要求旧任务追溯补做从未发生的 Discovery。
5. 承继只追加新 Intake/Task/metadata，原证据和旧分支可追溯。
6. Owner 逐任务授权 T0/T1，框架更新不替代该决定。
7. 无法无损衔接、缺文件或批准冲突时明确阻断，不以放宽 Gate 获得成功。

T1 的新 Task 必须通过真实 taskbuilder-init 和对应工作流；批准仍由人类完成。检查旧数据字节/路径/SHA 未变、新引用可追溯、项目规则保留和适用工具结果。仅完成静态比较只能称静态准备完成，不报告真实 Agent/业务 E2E 通过。失败按本规范撤回本次拥有的框架差异，不恢复整套旧 .agent 覆盖后续运行数据。

新初始化会重新核对 Intake 原始 Owner 决策的 intake_id/task_id/task_type/parent_branch/base_commit 与 Handoff。旧版只有 decision 的批准保留历史读取；不能补造绑定，需 Owner 明确重新确认并创建新 Intake 后初始化新任务。已初始化的旧 Task 不因此重写审批或强制重跑 taskbuilder-init。

## V4.2.x → 公共入口版本（当前开发分支）

这是一项公开 Prompt 路径变化，不是 Task State 升级。两个公共完整入口在 `.agent/roles/DiscoveryAgent.md`、`.agent/roles/TaskBuilderAgent.md`，用户不再先选流程；Discovery 调查后推荐，Owner 批准后 TaskBuilder 加载专属 Workflow 初始化规范。原有 11 个机器 Role、六 CLI、20 Gate、Schema 与 Handoff/ACK 不变。

六个旧相对路径与新入口的逐项映射见 [Role 索引](../../roles/README.md)。新版不保留重复 Prompt 权威定义；历史 commit 中的旧路径仍从原 commit 读取，禁止改写历史 Evidence。活跃 Task 保留原分支内工具/Prompt，可在副本验证兼容性后再由 Owner 决定是否采用新版；不能仅因移动 Role 就重做批准或修改 task_type。

更新时特别比较六份项目旧 Prompt 的本地定制：B→L 差异与上游合并不同。共同规则适配到公共完整 Prompt，流程专有部分保留在项目 Workflow Spec；相互冲突的本地定制由 Owner 决定，不整目录删除。只改当前入口导航/自动化配置中的旧路径，宿主根 AGENTS.md 的融合仍需明确授权，历史 Task/Program/Registry/审批/SHA 保持原字节与原路径。旧上游未知时如实报告，不能推断没有定制。来源记录只在完成安全更新后更新。
