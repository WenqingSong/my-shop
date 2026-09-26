# 项目工程规范（Engineering Baseline）

本文档定义项目长期适用的工程基线。所有 Agent 和开发者修改本项目代码时均应遵守。

本文档只规定**代码应如何编写和验证**，不定义当前任务的业务目标，也不安排 Agent 的交接。当前需求、业务规则、数据模型、接口行为和验收标准应记录在 `.agent/tasks/<task-slug>/task.md`、必要时的 `contract.md`，或项目已有且已确认的 `design.md` 中。角色职责和协作流程见 `docs/agent/AgentCollaborationSpecification.md` 与 `docs/agent/Five-AgentResponsibilityBoundary.md`。

## **规则优先级**

执行要求按下列顺序理解：

1. Owner 当前明确指令；
2. 当前 `task.md` 的 Goal、Scope、Out of Scope 和 Acceptance Criteria；
3. Owner 已确认的 `contract.md` 及适用的已确认设计；
4. 本文档中的项目工程基线；
5. 全局协同规范、职责边界与各角色 Prompt 中适用的流程要求；
6. 默认编码惯例。

若当前指令改变既有任务目标，应先按协同规范更新任务定义；若 Task、Contract 和真实系统约束发生明显冲突，不得自行猜测或暗中选择一边。无论优先级如何，都不得泄漏密码、Token、Secret 或私钥，不得绕过明确要求的认证授权，也不得吞掉关键错误制造成功。

文中的 **MUST** 表示适用时必须遵守；**SHOULD** 表示应根据当前任务的真实收益和成本判断，不要求机械执行。

---

## **1. 项目架构（Architecture）**

项目采用当前 GoFrame v2 工程结构。典型 HTTP 请求链路：

```
HTTP Request
    ↓
Router / Middleware
    ↓
API Req / Res
    ↓
Controller
    ↓
Service
    ↓
Logic
    ↓
DAO
    ↓
MySQL / Redis / MQ
```

各层职责：

- **API**：定义请求、响应和对外接口契约；
- **Controller**：完成 HTTP 适配、参数接收、调用业务能力并返回结果；
- **Service**：向调用方暴露业务能力接口；
- **Logic**：承载主要业务规则和业务流程；
- **DAO**：负责数据访问；
- **Middleware**：处理认证、日志、恢复等 HTTP 横切逻辑。

MUST：

- Controller 不直接操作数据库；
- DAO 不承载业务规则；
- Logic 不处理 HTTP Header、Cookie、Response 等协议细节；
- 遵循当前仓库真实结构；某个功能无需某层时，不为了图示而机械增加层次。

若项目已有不同但合理的代码组织，先按当前 Task 和真实代码确定修改位置；重要结构调整属于设计决定，不通过局部实现顺手完成。

---

## **2. 代码质量（Code Quality）**

### **2.1 命名**

命名优先表达业务语义。避免把缺少上下文的 `tmp`、`data`、`info`、`obj`、`foo`、`bar` 用作长期变量名。

优先使用能说明含义的名称，例如 `categoryCount`、`userProfile`、`orderStatus`、`accessToken`。允许明确、短小的 Go 惯用名称，例如 `id`、`ctx`、`err`、`req`、`res`。不要为了少打几个字符牺牲可读性。

### **2.2 简单优先**

一般取舍顺序：

1. 正确性与安全性；
2. 可读性；
3. 可维护性；
4. 满足当前需求的性能。

不要为了未来可能存在的需求提前设计复杂抽象，不要为简单功能引入无明确收益的设计模式、基础设施或依赖。存在性能目标时应满足任务定义的真实指标；不能以“简单”为由忽略已经确认的性能约束。

### **2.3 重复与抽象**

出现重复逻辑时，判断它是否确实是公共能力、是否会导致后续修改困难，以及抽象后是否更容易理解。少量重复代码未必值得抽象。不要将不同业务规则强行合并，只因它们当前代码相似。

修改范围应服务于当前任务；重构必要的相邻代码可以做，但不得借机进行无关的大规模整理。

---

## **3. 错误处理（Error Handling）**

MUST：

- 不静默忽略有业务或运行意义的错误；
- 跨重要边界传播错误时，增加有价值的操作上下文，并保留原始错误链；
- 区分内部技术错误、领域错误和对外 API 错误；
- 不把数据库、Redis、文件路径、堆栈等内部细节直接暴露给客户端；
- 客户端需要稳定区分的错误，应使用稳定错误码，不依赖错误 message 文本；
- 原则上在系统边界记录一次最终失败，避免多层对同一个错误重复打印。

推荐：

```
return fmt.Errorf("create category: %w", err)
```

避免：

```
return errors.New(err.Error()) // 丢失原始错误链
```

除非明确知道错误无需处理，不要写：

```
result, _ := function()
```

### **3.1 错误分层**

适用时将技术原因转换成调用者能理解的业务原因：

```
MySQL duplicate key
    ↓
ErrCategoryNameExists
    ↓
CATEGORY_NAME_EXISTS
```

转换时保留内部可定位的错误信息，同时确保对外响应不泄漏实现细节。不要把底层技术错误直接当作公开业务错误码。

### **3.2 API 错误码**

错误码应稳定、可机器判断、与数据库或框架实现解耦，并表达调用者真正关心的失败原因。根据当前接口的错误约定选择适用代码；不要为尚未存在的业务一次性建立庞大码表。

通用例子：

```
INVALID_ARGUMENT
UNAUTHORIZED
FORBIDDEN
NOT_FOUND
INTERNAL_ERROR
SERVICE_UNAVAILABLE
```

业务例子：

```
CATEGORY_NAME_EXISTS
CATEGORY_HAS_CHILDREN
PRODUCT_OFFLINE
ORDER_ALREADY_PAID
```

不适合作为公开业务错误码的例子：

```
MYSQL_DUPLICATE_ENTRY_1062
REDIS_TIMEOUT_001
GORM_RECORD_NOT_FOUND
CATEGORY_ERROR
CREATE_CATEGORY_FAILED
```

HTTP Status 与业务错误码承担不同职责。例如 `HTTP 409 Conflict` 可以配合 `CATEGORY_NAME_EXISTS`，但 `409` 本身不是业务错误码。新增接口应遵循项目现有的响应格式；改变公共错误格式需要当前 Task 明确覆盖。

---

## **4. Context 使用规范**

HTTP 请求链路应合理传递请求的 `context.Context`：

```
HTTP → Controller → Logic → DAO → Database / Redis / MQ
```

MUST：

- 不无理由使用 `context.Background()` 替代已有请求 Context；
- 让超时、取消等生命周期信息沿调用链传播；
- 不让依附请求的任务在请求结束后无控制地继续运行；
- 调用数据库、Redis、外部服务和 MQ 时，根据实际 API 传入可取消的 Context。

真正独立于请求生命周期的后台任务可以建立自己的 Context，并明确其取消、超时和退出机制。不要把请求 Context 直接用于必须在响应后可靠完成的独立工作。

---

## **5. 日志规范（Logging）**

优先使用结构化日志，让日志能够回答：什么操作失败、哪个对象受影响、失败原因是什么、属于哪个请求或后台任务。适用时包含：

```
operation
request_id
user_id
object_id
error
```

MUST：

- 不记录密码、完整 Token、Secret、私钥或不必要的敏感用户数据；
- 不在 DAO、Logic、Controller 对同一个错误逐层重复记录；
- 日志字段不得依赖无法稳定解析的自由文本来表达重要状态。

通常由底层增加上下文并返回错误，由系统边界统一记录最终失败。系统边界可包括 HTTP Handler / Middleware、MQ Consumer、Job / Worker、CLI 入口。确需在中途记录重试、补偿或状态变化时，应确保记录的是独立事件，而非重复打印同一失败。

---

## **6. 注释规范（Comments）**

注释优先解释 **原因、约束、取舍和不明显的行为**，不要逐句翻译代码。

低价值：

```
// 查询分类
GetCategory()
```

有价值：

```
// Keep the stock deduction as a conditional update.
// A query-then-update sequence can oversell under concurrency.
```

SHOULD 在以下场景考虑注释：非显而易见的业务约束、数据一致性要求、并发不变量、性能原因、workaround，以及容易被后续维护者误改的设计取舍。

TODO 必须说明原因或移除条件。不要只写 `TODO fix`。例如：

```
// TODO: remove this fallback after the legacy data migration is complete.
```

---

## **7. 配置管理（Configuration）**

MUST：

- 不在生产代码中硬编码环境地址、数据库或 Redis 密码、Token、API Key、Secret；
- 不提交生产环境敏感配置；
- 不依赖未说明的开发者本地绝对路径或专属配置才能构建和启动。

环境差异可以通过配置文件、环境变量、Secret 管理系统或配置中心管理；代码负责读取、校验和使用配置。仓库可以保留安全默认值、`.env.example` 和不含真实凭据的示例配置。

新增配置时，按当前 Task 要求说明名称、用途、必填条件和合理默认值；不能将连接失败等关键配置错误静默解释成可用状态。

---

## **8. 数据库规范（Database）**

按任务的真实风险判断需要哪些约束和验证，不机械为简单 CRUD 增加复杂机制。

### **8.1 数据完整性**

能由数据库可靠保证的重要约束，不应只依赖应用层先查询再判断。例如适用的 `NOT NULL`、`UNIQUE`、引用约束和数据类型限制。重要业务不变量需要确定在并发执行时是否仍成立。

### **8.2 写操作**

重要写操作必须考虑事务边界、并发冲突、`RowsAffected`、唯一约束、rollback 和错误传播。

关键 `UPDATE` / `DELETE` 不能仅因 SQL 没有报错就断言业务成功。多步操作若要求原子性，应明确失败时已做修改如何撤销；跨数据库、缓存或 MQ 的行为还应按当前 Task / Contract 定义一致性语义。

### **8.3 查询**

根据真实查询模式考虑索引、分页、N+1 和不必要的重复查询。不要机械地给所有字段加索引，也不要因为未来的假设提前引入复杂优化。

---

## **9. API 与输入设计（API Design）**

API 是对调用者的稳定契约。MUST：

- 不暴露内部数据库错误和框架异常字符串；
- 不要求客户端解析 message 文本判断错误类型；
- 对所有外部输入做与业务风险相称的校验；
- 不在未获授权的 Task 中改变已有公开接口或错误语义。

外部输入不限于 HTTP，还包括 MQ 消息、第三方回调、CLI 和 Job 参数。至少按实际场景考虑必填、类型、长度、范围、枚举值，以及 ID 或引用对象是否存在。

Controller 做协议适配；业务校验与权限规则应放在能覆盖所有调用入口的合适层次，不能仅靠前端或某个 HTTP 参数校验来维护跨入口不变量。

---

## **10. 安全规范（Security）**

所有外部输入默认不可信。MUST：

- 做必要输入校验；
- 使用参数化查询或 ORM 的安全能力，避免注入；
- 密码使用安全的密码哈希算法存储，不得明文或使用普通快速哈希替代；
- 不记录或泄漏密码、完整 Token、Secret、私钥；
- 不把内部异常直接暴露给客户端；
- 不通过临时判断、测试开关或绕过 Middleware 来跳过明确要求的认证授权；
- 在服务端核查身份和权限，不信任客户端自行提交的角色或用户身份。

新增认证、授权、Token、Session 或权限相关功能时，先明确安全边界、失效与撤销语义，再考虑性能优化。具体权限模型和业务规则属于 Task / Contract，不在本文档预先定义。

---

## **11. 测试规范（Testing）**

测试优先验证**可观察的业务行为**，而不是只验证某个内部函数被调用。

新功能至少考虑正常路径、关键错误路径和重要边界条件。Bug 修复优先增加回归测试；在可行时，该测试应能区分修复前后的行为。

涉及 MySQL、Redis、MQ 等真实依赖时，根据 Task / Contract 的验证要求执行必要的集成测试，并说明测试环境。涉及共享可变状态或并发业务不变量时，考虑 `go test -race ./...` 或相关包的 Race Test；Race Test 不能代替业务层面的并发正确性检查。

禁止通过下列方式制造假 `PASS`：

- Skip 或删除失败测试；
- 放宽关键断言；
- 捕获、忽略关键错误；
- 过度 Mock，导致本次真正需要验证的核心行为没有执行；
- 仅验证 HTTP 200，却不核对重要数据或状态结果。

若因环境限制未运行某项必要检查，报告 `NOT_VERIFIED` 和原因；不要把未运行写成通过。Cleaner 和 Deliverer 的独立验证职责以协同规范为准。

---

## **12. 依赖管理（Dependencies）**

新增依赖前应判断：标准库或现有依赖能否解决问题；依赖是否适合当前项目；它是否确实降低实现复杂度；是否显著增加构建、运行、维护或安全成本。

不要为了简单功能引入大型依赖。新增依赖若影响部署或运行环境，应在当前任务的验证要求中体现。

---

## **13. 完成前检查（Delivery）**

完成代码修改前，执行适用的格式化与验证。Go 项目的常用检查包括：

```
gofmt
go test ./...
go vet ./...
go build ./...
```

根据 Task / Contract 增加集成、Race、API 或运行验证。命令不能执行时如实报告原因、影响与尚未验证的行为。

检查当前 Git 状态、相对任务基线的已跟踪变更、暂存区和新增文件，确认：

- 无无关修改、调试代码和临时日志；
- 无硬编码凭据或遗漏的新文件；
- 未意外覆盖或删除 Owner 与其他 Agent 已有代码；
- 未留下大段已废弃但仍注释在生产文件中的实现；
- 本次修改与实际准备交给 Cleaner 的审查对象一致。

具体如何确定审查基线、交接审查版本及处理已有修改，见全局协同规范。

---

## **14. 本文件的维护边界**

Agent 开始实现前应阅读 `AGENTS.md`、当前 `task.md`，以及存在时已确认的 `contract.md` / `design.md`。三类文件各司其职：

```
AGENTS.md                 → 长期工程约束
task.md / contract.md      → 本次做什么及哪些设计约束已确认
角色 Prompt / 协同规范     → 本阶段如何执行、何时交接
```

不要把临时业务规则或 Agent 工作流重复写入 `AGENTS.md`。只有一条规则对多数任务长期适用、违反后代价明显且较稳定，才考虑加入本文件；否则应放入当前 Task、模块设计或 Contract。

本文档不负责替 Owner 决定功能范围，也不将某次 Agent 的偏好提升为全项目强制规范。

---

## **15. 设计取舍**

多个方案都可行时，根据当前需求、正确性、安全性、实现复杂度、可维护性、可测试性、可靠性、性能和运维成本做取舍。

原则：

> Design enough, then code.
> 

先设计到足以安全开始；复杂方案需走协同规范中的 Owner 确认关口。实现过程中通过代码、测试和反馈修正细节，但不得自行改变已确定的目标、重要设计或验收标准。