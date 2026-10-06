# 错误码 Namespace（Error Code Namespace）

本文面向项目接手者，说明项目级业务错误码的编码模型、域分配规则、已落地码兼容性、immutable/reuse 政策，以及与全局资源预留 Registry 的关系。事实来源为 `error-code-namespace-expansion` 的 APPROVED Contract 与 `internal/codes/codes.go` 实际实现。权威的域分配状态以 `.agent/registry/error-codes.md`（`develop` 上）为准。

## 1. 编码模型

- 业务错误码类型为 `int`（`internal/codes/codes.go` 的 `type Code = int`），单一 `const` 块定义全部业务码。
- `CodeOK = 0` 表示成功，不属于任何域。
- `codeTable map[Code]codeInfo{HTTPStatus, Message}` 提供 HTTP 状态与用户安全 message 映射；`HTTPStatus`/`Message`/`FromError` 均按 `map[Code]` 查表，对任意宽度整数无差别。
- 统一响应协议 `{code,message,data}`：成功 `code:0`；失败 `code:业务码` 且 `data:null`；HTTP 状态由 `HTTPStatus(code)` 派生。业务码与 HTTP 状态是两层语义，客户端依靠 code 判断错误类型，不解析 message 文本。

## 2. 域编码模型（Domain Encoding）

- 错误码按「域（Domain）」组织，每域为固定大小 1000 的连续整数区间，按 1000 对齐。
- 域序 `domain_seq = code / 1000`（整数除法）。
- 域区间 `[domain_seq × 1000, domain_seq × 1000 + 999]`。
- `domain_seq` 为从 1 开始的正整数；`CodeOK = 0` 无域序。

## 3. 域分配规则

- 下一空闲域：`domain_seq_next = max(已记录域序) + 1`，区间 `[domain_seq_next × 1000, domain_seq_next × 1000 + 999]`。
- 域序不受现有四位数宽度限制，可按正整数域序继续扩展：`9000-9999`（序 9）、`10000-10999`（序 10）、`11000-11999`（序 11）……不存在 `9999` 上限。
- 域内具体编号由 Analyst 在 Contract 中逐个列出；域本身独占，域内编号无跨任务冲突。
- `next` 由规则派生，不维护显式 next 指针，不依赖 Owner 手工算号。

## 4. 当前域分配表

| 域序 | 域区间 | 拥有方 | 状态 |
| --- | --- | --- | --- |
| 1 | 1000-1999 | 通用 | ACTIVE |
| 2 | 2000-2999 | IAM | ACTIVE |
| 3 | 3000-3999 | category | ACTIVE |
| 4 | 4000-4999 | product | ACTIVE |
| 5 | 5000-5999 | SKU | ACTIVE |
| 6 | 6000-6999 | inventory | ACTIVE |
| 7 | 7000-7999 | address | ACTIVE |
| 8 | 8000-8999 | cart | ACTIVE |

（本表为长期设计快照；分配状态与未来 RESERVED/ACTIVE/RELEASED 变化以 `.agent/registry/error-codes.md` 为唯一权威。）

## 5. 已落地错误码兼容性

- 已落地业务错误码（`1000-8999`）是稳定 API 契约，被 Backend 测试、Frontend、API Consumer、各模块 Design、Registry 依赖为稳定事实。
- 扩展规则不 renumber、不改变任何已落地错误码的取值、HTTP 状态映射、message 与业务含义；`7001`/`8001` 等取值在扩展前后保持原值。

## 6. immutable / reuse 政策

- 代码级：已落地（ACTIVE，进入 `internal/codes/codes.go`）的具体 code 值不可变、永不复用；不允许无独立迁移方案的 renumber；已落地错误码即使废弃也不得重新赋予其它语义。
- 域级：沿用 Global Resource Reservation 生命周期——仅纯 `RESERVED`、尚未进入 APPROVED Contract / Implementation / merge 的域允许 `RELEASE` 后重新分配；已实现/已合并的域不复用。

## 7. 与 Global Resource Registry 的关系

- `.agent/registry/error-codes.md`（`develop` 上）是域「分配状态」的权威事实源（谁 RESERVED / ACTIVE / RELEASED 了哪个域）。
- 本文档是域「编码模型与长期规则」的结论权威；`.agent/specs/*` 是工程治理流程权威。三者互补、不复制，无双事实源冲突。
- 新域 Reservation 规则：Task 需新增域时，Analyst 读 Registry 按 §3 派生 `domain_seq_next`，写入 Contract 全局资源清单与 Registry 的 RESERVED 条目（通过独立 Registry 变更进入 `develop`，Feature Branch 内私留不视为有效预留）；Coder 只使用已 APPROVED 的域，禁止自行推断编号（含 `max+1`）。

## 8. 各模块错误码

各业务模块 Design 继续在自身「错误码域」章节记录本模块具体错误码与归属声明，不在此重复，也不要求各模块重复全局 Namespace 规则：

- `iam.md`、`category.md`、`product.md`、`sku.md`、`inventory.md`、`address.md`、`cart.md`、`rbac.md`。
