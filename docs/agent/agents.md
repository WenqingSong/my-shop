# 项目工程规范（Engineering Baseline）

本文档定义项目级工程基线。

所有 Agent、开发者在修改本项目代码时，默认必须遵守本文档。

本文档只描述长期稳定的工程约束，不承载具体业务需求。

具体业务规则、模块边界、数据模型、API Contract、Acceptance Criteria 等，应放在对应的：

- `task.md`
- `design.md`
- `contract.md`

## **规则优先级**

发生冲突时，按以下顺序处理：

1. Owner 当前明确要求；
2. 当前 Task / Contract / 已确认 Design；
3. 本文档中的项目工程规范；
4. 默认编码惯例。

任何情况下不得突破安全红线，例如：

- 泄漏密码、Token、Secret、私钥；
- 绕过明确认证或授权要求；
- 静默吞掉关键错误并继续执行。

---

# 1. 项目架构（Architecture）

项目遵循当前 GoFrame v2 工程结构。

典型请求链：

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

职责边界：

- API：定义请求、响应与接口契约；
- Controller：处理 HTTP 适配、参数接收、调用业务能力；
- Service：暴露业务能力接口；
- Logic：承载主要业务规则与业务流程；
- DAO：负责数据访问；
- Middleware：处理认证、日志、恢复等 HTTP 横切逻辑。

MUST：

- Controller 不直接操作数据库；
- DAO 不承载业务规则；
- Logic 不处理 HTTP Header、Cookie、Response 等协议细节；
- 不机械套层，优先遵循当前项目真实结构。

---

# 2. 代码质量（Code Quality）

## **2.1 命名**

命名必须优先表达业务语义。

避免无明确含义的名称：

```
tmp
data
info
obj
foo
bar
```

优先使用：

```
categoryCount
userProfile
orderStatus
accessToken
```

允许使用行业内明确、短小且无歧义的惯用名称，例如：

```
id
ctx
err
req
res
```

不要为了缩短字符牺牲可读性。

## **2.2 简单优先**

优先级：

1. 正确性；
2. 可读性；
3. 可维护性；
4. 性能优化。

禁止：

- 为未来可能存在的需求提前设计复杂抽象；
- 在没有明确收益时引入设计模式；
- 使用复杂技巧替代清晰代码；
- 为“看起来高级”而增加基础设施或依赖。

## **2.3 重复与抽象**

出现重复逻辑时，应判断：

- 是否确实属于公共能力；
- 是否会导致未来修改困难；
- 抽象后是否更容易理解。

不要为了消除少量重复代码制造过度抽象。

---

# 3. 错误处理（Error Handling）

MUST：

- 不得静默忽略有业务或运行意义的错误；
- 跨重要边界传播错误时增加有价值的 operation context；
- 必须保留原始 error chain；
- 内部错误、Domain Error、API Error 保持职责分离；
- API 不得直接暴露数据库、Redis、文件路径、堆栈等内部实现错误；
- 客户端依赖稳定 error code，不依赖 message；
- 同一个错误原则上只在系统边界记录一次，避免跨层重复日志。

推荐：

```
return fmt.Errorf("create category: %w", err)
```

禁止：

```
return errors.New(err.Error())
```

除非明确知道无需处理，否则不要：

```
result, _ := function()
```

## **3.1 错误分层**

推荐模型：

```
Infrastructure Error
    ↓
Domain Error
    ↓
API Error
```

例如：

```
MySQL duplicate key
    ↓
ErrCategoryNameExists
    ↓
CATEGORY_NAME_EXISTS
```

技术错误不得直接作为对外业务错误码。

## **3.2 API 错误码**

错误码应：

- 稳定；
- 可机器判断；
- 与具体技术实现解耦；
- 表达调用者真正关心的业务原因。

推荐：

```
INVALID_ARGUMENT
UNAUTHORIZED
FORBIDDEN
NOT_FOUND
INTERNAL_ERROR
SERVICE_UNAVAILABLE

CATEGORY_NAME_EXISTS
CATEGORY_HAS_CHILDREN
PRODUCT_OFFLINE
ORDER_ALREADY_PAID
```

禁止：

```
MYSQL_DUPLICATE_ENTRY_1062
REDIS_TIMEOUT_001
GORM_RECORD_NOT_FOUND
CATEGORY_ERROR
CREATE_CATEGORY_FAILED
```

HTTP Status 与业务错误码职责不同。

例如：

```
HTTP 409 Conflict
code = CATEGORY_NAME_EXISTS
```

不要把：

```
409
```

直接当成业务错误码。

---

# 4. Context 使用规范

HTTP 请求链路应合理传递同一个：

```
context.Context
```

典型链路：

```
HTTP
↓
Controller
↓
Logic
↓
DAO
↓
Database / Redis / MQ
```

MUST：

- 不无理由使用 `context.Background()` 替代已有请求 Context；
- timeout、cancel 等生命周期信息应能够沿调用链传播；
- 请求结束后不应让依附该请求的任务无控制地继续运行。

只有真正独立于请求生命周期的后台任务，才应创建新的 Context。

---

# 5. 日志规范（Logging）

优先使用结构化日志。

日志应尽可能回答：

- 什么操作失败；
- 哪个对象失败；
- 为什么失败；
- 属于哪个请求或任务。

适用时包含：

```
operation
request_id
user_id
object_id
error
```

MUST：

- 不记录密码；
- 不记录完整 Token；
- 不记录 Secret；
- 不记录私钥；
- 不记录不必要的敏感用户数据；
- 不在 Repository / Logic / Controller 对同一个错误重复打印。

原则：

```
底层：
增加上下文并返回错误

系统边界：
统一记录最终失败
```

系统边界包括：

- HTTP Handler / Middleware；
- MQ Consumer；
- Job / Worker；
- CLI 入口。

---

# 6. 注释规范（Comments）

注释优先解释：

```
Why
Constraint
Trade-off
Non-obvious Behavior
```

不要把代码直接翻译成中文或英文。

低价值：

```
// 查询分类
GetCategory()
```

更有价值：

```
// Keep the stock deduction as a conditional update.
// A query-then-update sequence can oversell under concurrency.
```

SHOULD 在以下情况考虑注释：

- 非显而易见的业务约束；
- 数据一致性要求；
- 并发不变量；
- 性能原因；
- workaround；
- 容易被后续维护者误改的设计取舍。

TODO 必须说明原因或删除条件。

禁止：

```
// TODO fix
```

推荐：

```
// TODO: remove this fallback after the legacy data migration is complete.
```

---

# 7. 配置管理（Configuration）

MUST：

- 不在生产代码中硬编码环境地址；
- 不硬编码数据库密码；
- 不硬编码 Redis 密码；
- 不硬编码 Token、API Key、Secret；
- 不提交生产环境敏感配置。

环境差异应通过以下方式管理：

- 配置文件；
- 环境变量；
- Secret 管理系统；
- 配置中心。

代码只负责读取与使用配置。

仓库中可以保留：

- 安全默认值；
- `.env.example`；
- 示例配置。

---

# 8. 数据库规范（Database）

MUST / SHOULD 根据任务实际风险执行。

## **8.1 数据完整性**

能够由数据库可靠保证的重要约束，不应只依赖应用层判断。

例如：

- NOT NULL；
- UNIQUE；
- 合理的外键或引用约束；
- 必要的数据类型限制。

## **8.2 写操作**

重要写操作必须考虑：

- 是否需要事务；
- 是否存在并发冲突；
- 是否需要检查 `RowsAffected`；
- rollback 行为；
- 错误传播。

关键 UPDATE / DELETE 不应只因为 SQL 没报错就认为业务成功。

## **8.3 查询**

根据真实查询模式考虑：

- 索引；
- 分页；
- N+1；
- 不必要的重复查询。

不要机械地给所有字段加索引，也不要为了未来假设提前做复杂优化。

---

# 9. API 设计规范（API Design）

API 是对调用者的稳定契约。

MUST：

- 不暴露内部数据库错误；
- 不暴露框架异常字符串；
- 不要求客户端解析 message 判断错误类型；
- 所有外部输入都应进行必要校验。

输入来源包括：

- HTTP；
- MQ；
- 第三方回调；
- CLI；
- Job 参数。

至少考虑：

- 必填；
- 类型；
- 长度；
- 合法范围；
- 枚举值；
- ID / 引用对象是否存在。

---

# 10. 安全规范（Security）

所有外部输入默认不可信。

MUST：

- 做必要输入校验；
- 使用参数化查询或 ORM 安全能力，避免注入；
- 密码必须使用安全密码哈希算法存储；
- 不记录密码、Token、Secret；
- 不把内部异常直接暴露给客户端；
- 认证与授权逻辑不得通过业务代码临时绕过。

新增认证、授权、Token、Session、权限相关功能时，应优先保证正确性和可撤销边界，再考虑性能优化。

---

# 11. 测试规范（Testing）

测试优先验证：

```
业务行为
```

而不是：

```
某个内部函数是否被调用
```

新功能至少考虑：

- 正常路径；
- 关键错误路径；
- 重要边界条件。

Bug 修复：

- 优先增加 Regression Test；
- 测试应能够证明旧错误行为被修复。

涉及数据库、Redis、MQ 等真实依赖时，根据 Task / Design 要求执行必要的集成测试。

涉及并发时，应考虑：

```
go test -race ./...
```

不要通过以下方式制造假 PASS：

- Skip 掉失败测试；
- 删除失败测试；
- 放宽断言；
- Catch / Ignore 关键错误；
- Mock 掉本次真正需要验证的核心行为。

---

# 12. 依赖管理（Dependencies）

新增依赖前必须考虑：

- 标准库或现有依赖是否已经能够解决；
- 依赖是否持续维护；
- 是否真正降低复杂度；
- 是否会显著增加构建、运行或安全成本。

不要为了简单功能引入大型依赖。

---

# 13. 提交前检查（Delivery）

完成任务前至少执行适用的检查：

```
gofmt
go test ./...
go vet ./...
go build ./...
```

并检查：

```
git status
git diff
```

确认：

- 没有无关修改；
- 没有调试代码；
- 没有临时日志；
- 没有硬编码凭据；
- 没有遗漏新文件；
- 没有意外删除用户代码；
- 没有大段被注释掉但仍留在生产代码中的废弃实现。

涉及真实基础设施时，按 Task / Design 中定义的 Verification 额外验证。

---

# 14. Agent 使用规则

Agent 开始实现前必须读取：

```
AGENTS.md
+
当前 Task / Design / Contract
```

职责区分：

```
AGENTS.md
→ 项目代码应该怎么写

Task / Design / Contract
→ 当前功能要实现什么

Coder Agent Prompt
→ Agent 以什么流程工作
```

不要把当前业务规则写入 `AGENTS.md`。

不要把 Agent 工作流规则重复写入 `AGENTS.md`。

如果当前任务出现新的长期工程规则：

先判断它是否满足：

- 多数 Task 都适用；
- 违反后代价明显；
- 长期较稳定。

只有满足这些条件，才考虑加入本文档。

否则应放入：

- 当前 Task；
- 模块 Design；
- Contract；
- Cleaner Checklist；
- 独立工程文档。

---

# 15. 设计原则

当多个方案都可行时，优先根据以下因素做取舍：

- 正确性；
- 业务需求；
- 当前阶段；
- 实现复杂度；
- 可维护性；
- 可测试性；
- 可靠性；
- 性能；
- 安全性。

不要为了追求“最佳实践”而脱离当前真实需求。

原则：

```
Design enough, then code.
```

先设计到足以安全开始，再通过代码、测试和反馈继续修正。