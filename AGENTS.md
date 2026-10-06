# AGENTS

本仓库使用 **Workflow V2**。Agent 进入仓库后，先按职责定位规则，再阅读下方长期工程规范。

## Workflow 入口

执行 Workflow Task 前，根据职责读取：

1. 协作规范：`.agent/specs/AgentCollaborationSpecification.md`
2. 职责边界：`.agent/specs/Five-AgentResponsibilityBoundary.md`
3. 本角色 Prompt：`.agent/roles/<Role>.md`
4. 机器配置：`.agent/workflow.yaml`
5. 当前任务事实：`.agent/tasks/<task-id>/` 下的 `task.md` / `state.yaml` / `contract.md` 等

架构 Design（WHY / architecture）：

`docs/design/agent-workflow.md`

首次跨项目 Adoption：

`.agent/specs/WorkflowAdoption.md` + `.agent/templates/PROJECT_ADAPTATION.template.md`

`.agent/` namespace 索引见 `.agent/README.md`。

Workflow Engine（只读 Gate Validator `workflow-check`）是独立 Go Module，位于 `.agent/engine/`（`module workflow-v2-engine`），不依赖本仓库业务 Go Module、不通过 `go.work`/`replace` 关联：

```text
# 构建本地 binary（进入 .agent/bin/，被 Git ignore）
go -C .agent/engine build -o ../bin/workflow-check ./cmd/workflow-check

# 运行（正式 Gate 机器入口优先 direct binary，保留 0/1/2 exit code）
.agent/bin/workflow-check gate <gate> <task>
```

Engine 测试与业务测试分开：Engine 用 `go -C .agent/engine test ./...`；业务模块仍在 repo 根用 `go test ./...`（不含 Engine）。

注意：不要从历史 `.agent/tasks/*` 推断当前 Workflow 规则。当前 Workflow 权威来源是：

- `docs/design/agent-workflow.md`（Architecture）
- `.agent/specs/*`（Execution Specifications）
- `.agent/roles/*`（Role Instructions）
- `.agent/workflow.yaml`（Machine Configuration）

---

# 项目工程规范

本文档保留长期适用于多数任务、违反后代价明显的工程规则。当前业务目标和验收标准写入 `task.md`；关键设计写入经 Owner 确认的 `contract.md`；项目级长期设计事实（架构、数据模型、状态机、模块边界、不变量、公开协议等）沉淀在 `docs/design/*`，其职责、同步时机与 `CLEAN` 门槛由 `.agent/specs/*` 与 `.agent/roles/*` 定义；协作流程见 `.agent/specs/`。

规则优先级：Owner 当前指令 → 当前 Task → 已确认 Contract/设计 → 本文件 → 角色流程。其中「已确认设计」包括任务级设计约束（`contract.md`）与项目级长期设计（`docs/design/*`），后者的落点与维护规则见 `.agent/specs/*`。发现冲突时报告证据，不自行改写目标。

## 1. 基本取舍

优先级通常是：正确与安全、可读、可维护、满足当前需求的性能。

- 设计到足以安全开始，然后通过代码、测试和反馈继续修正。
- 不为未来假设提前引入复杂抽象、依赖或基础设施。
- 不为追求形式上的“整洁”机械增加层次、接口或设计模式。
- 少量重复代码不一定需要抽象；不同业务规则不能只因代码相似就强行合并。
- 必要的相邻重构可以做，无关的大规模整理不属于当前任务。

## 2. 代码结构

遵循仓库当前真实结构。GoFrame HTTP 功能通常按下列职责组织：

- API：请求、响应和公开契约；
- Controller：HTTP 适配；
- Service：对内暴露业务能力；
- Logic：业务规则与流程；
- DAO：数据访问；
- Middleware：认证、响应、日志等横切行为。

Controller 不直接操作数据库；DAO 不承载业务规则；Logic 不处理 Header、Cookie 等 HTTP 细节。简单功能无需某层时，不为了目录完整机械创建。

命名应表达业务含义。允许 `id`、`ctx`、`err`、`req` 等明确的 Go 惯用短名，避免把 `tmp`、`data`、`info` 等缺少语义的名称长期保留。

## 3. 中文、注释与日志

- Agent 输出、项目说明和面向 Owner 的文档默认使用中文。
- 代码标识符、API 字段、错误码、配置键和协议名称保持稳定英文，不使用拼音命名。
- 注释默认使用中文，重点解释原因、约束、取舍和不明显行为，不逐句翻译代码。
- 导出标识符或外部工具对注释格式有要求时，兼顾其规范。
- 日志的人类可读信息默认使用中文；结构化字段名保持稳定英文，例如 `operation`、`request_id`、`user_id`、`error`。
- 客户端展示信息默认使用中文；调用方判断错误必须依赖稳定错误码，而不是解析 message。

TODO 必须说明原因或移除条件，禁止只写 `TODO fix`。

## 4. 错误处理

- 不静默忽略有意义的错误。
- 跨重要边界传播错误时增加操作上下文，并保留原始错误链。
- 区分底层技术错误、领域错误和公开 API 错误。
- 不向客户端暴露数据库、Redis、文件路径、堆栈或敏感信息。
- 只在合适的系统边界记录一次最终失败，避免逐层重复打印。
- 客户端需要区分的错误使用稳定错误码，不一次性设计尚无需求的庞大码表。

技术错误应转换为调用方能理解的业务语义，例如：

```text
MySQL duplicate key → ErrUsernameExists → USERNAME_EXISTS
```

HTTP Status 与业务错误码职责不同，新增接口沿用项目现有响应格式。

## 5. Context、配置和敏感信息

HTTP 请求的 `context.Context` 应沿 Controller、Logic、DAO 和外部依赖传播。不要无理由用 `context.Background()` 替代请求 Context，也不要让依附请求的任务失控地继续运行。

- 不在生产代码中硬编码数据库密码、Token、API Key、Secret 或生产地址。
- 不提交生产凭据，不把敏感值写入日志、错误响应或测试快照。
- 环境差异使用配置文件、环境变量或 Secret 管理方式处理。
- 新配置应说明用途、必填条件和安全默认值；关键配置错误不能被静默忽略。

## 6. 数据库与一致性

- 能由数据库可靠保证的重要约束，应使用适当的 `NOT NULL`、`UNIQUE`、引用约束或条件写入，而不只依赖“先查再写”。
- 重要写操作考虑事务边界、并发冲突、`RowsAffected`、rollback 和错误传播。
- SQL 没有报错不等于业务修改成功；关键更新和删除应核对实际结果。
- 多步操作若要求原子性，应明确失败时如何处理已完成的修改。
- 跨数据库、缓存或 MQ 的一致性语义由当前 Task/Contract 确定，不能自行假设。
- 根据真实查询考虑索引、分页、N+1 和重复查询，不机械优化所有字段。

## 7. API 与安全

所有外部输入默认不可信，包括 HTTP、MQ、回调、CLI 和 Job 参数。

- 按风险校验必填、类型、长度、范围和枚举值。
- 使用参数化查询或 ORM 的安全能力，避免注入。
- 服务端验证身份和权限，不信任客户端提交的用户身份或角色。
- 密码必须使用适合密码存储的哈希算法，不得明文存储或使用普通快速哈希替代。
- 不通过临时开关、测试代码或绕过 Middleware 规避认证授权。
- 未经当前 Task 授权，不改变已有公开接口、错误语义或数据契约。

认证、Token、Session 和权限功能应先明确安全边界、过期和撤销语义，再考虑性能优化。

## 8. 测试

测试优先验证可观察的业务结果，而不是只验证内部函数被调用。

新功能按真实风险覆盖：正常路径、关键拒绝路径、重要边界；Bug 修复增加能捕捉原错误的回归测试。关键业务不变量应至少有一个测试能够区分正确与错误实现。

需要 MySQL、Redis、MQ 等真实依赖才能证明的行为，应按 Task/Contract 做集成验证。涉及共享状态或并发时按风险运行 Race Test，但 Race Test 不能代替库存、唯一性等业务并发验证。

禁止通过删除或 Skip 失败测试、放宽关键断言、忽略错误或 Mock 掉核心行为制造 `PASS`。环境不具备时记录 `NOT_VERIFIED` 及其影响。

## 9. 依赖与完成检查

新增依赖前判断标准库或现有依赖是否足够，以及新依赖对构建、维护、安全和部署的成本。简单功能不引入不必要的大型依赖。

完成修改前，按任务风险执行适用检查，例如：

```text
gofmt
go test ./...
go vet ./...
go build ./...
```

同时检查任务基线后的完整变更，包括新增文件，确认：

- 没有无关修改、调试代码、临时日志和硬编码凭据；
- 没有遗漏必要迁移、配置或测试；
- 没有覆盖 Owner 或其他 Agent 的已有工作；
- 交给 Cleaner 的版本与自验版本一致；
- 未执行的检查和已知限制已如实交接。

## 10. 维护本文件

只有一条规则对多数任务长期适用、违反代价明显且较稳定时，才加入 `AGENTS.md`。临时业务规则进入 Task，关键设计进入 Contract，角色执行方式进入角色 Prompt。

不要把同一规则复制到多个文件；优先引用唯一来源。定期根据真实任务记录删除没有防止过实际问题的冗余规则。

## 11. 遗留设计与未完成能力

项目当前处于初步实现阶段，部分能力是刻意留白的简化实现。以下缺口在当前是已知且被接受的，但**任何后续任务一旦牵涉到它们，必须先显式识别并完成对应设计，不得假设这些能力已经存在或已被处理**：

- **权限/RBAC**：当前仅有登录认证（`Principal.UserID`），没有管理员或角色体系。商品分类等写接口暂只要求登录、不区分角色。
- **商品关联**：当前没有商品表，商品分类的删除等操作暂不校验商品关联。

处理牵涉上述能力（管理员、商品或其他已声明的遗留缺口）的任务时，必须在 Task/Contract 中显式明确相关设计与实现，禁止在未考虑这些遗留能力的情况下继续实现或审查。

## 12. 全局资源预留

需要占用项目级全局唯一资源（至少含错误码域/编号与 migration version）的任务，必须在进入 Coder 前，从 `develop` 上的共享事实源 `.agent/registry/*` 取得独占分配；Coder 只能使用已预留资源，禁止自行推断编号。分类、生命周期、分配与一致性检查规则见 `.agent/specs/AgentCollaborationSpecification.md`（唯一来源），不在本文件复制全文。