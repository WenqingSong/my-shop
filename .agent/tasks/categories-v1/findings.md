# Cleaner Findings

## Review Target

- 分支：`feat/category`
- 基线 Commit：`8e4ff4df6536383a4720630c7b62e03bd0e5689f`（`git log` 确认 HEAD 即此 commit）
- 工作区状态（`git status` + `git diff`）：
  - 已跟踪修改：`internal/boot/boot.go`、`internal/cmd/cmd.go`、`internal/codes/codes.go`、`internal/logic/logic.go`
  - 新增未跟踪：`api/categories/`、`internal/controller/categories/`、`internal/logic/categories/`、`internal/service/category.go`、`.agent/`
  - `agents.md` 的修改（新增第 11 条「遗留设计与未完成能力」）为任务开始前已有，属规范文档、与本任务 Scope 相关，不计入本任务产出（Review Baseline 已声明）。
- 环境：MySQL 8.0（容器 `my-shop-mysql`，127.0.0.1:3306，healthy）、Redis 7（healthy）。

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `SHOW CREATE TABLE categories` 确认字段齐全（id/parent_id/name/sort/status/created_at/updated_at）且含 `UNIQUE KEY uk_parent_name (parent_id, name)`；DDL 为 `CREATE TABLE IF NOT EXISTS`，幂等可重复执行。 |
| AC-002 | PASS | 集成测试 `TestCategoriesEndToEnd`（list 仅启用项、sort 升序、父子嵌套）+ 单元测试 `TestBuildTreeSortingAndVisibility`（禁用父隐藏子树、sort/id 排序）。 |
| AC-003 | PASS | 集成测试覆盖详情完整字段、created_at/updated_at 非空；不存在 id 返回 404/3001 且 `data:null`。 |
| AC-004 | PASS | 集成测试覆盖无 token 创建 401/1002、带 token 创建成功并返回新 id、查库确认。 |
| AC-005 | PASS | 集成测试覆盖重复创建 409/3002 且无重复行；`TestConcurrentCreateSameName` 并发 10 个仅 1 成功，依赖 DB 复合唯一键兜底。 |
| AC-006 | PASS | 集成测试覆盖无 token 更新 401、改名/改排序/改状态/改父及查库确认；「更新不存在 id → 404」代码路径正确（`findOne→nil→3001`）但无显式测试（见 P3 建议）。 |
| AC-007 | PASS | 集成测试覆盖 parent_id 指向自身/后代返回 400/3004 且数据不变；`isDescendant` 含自身判断。 |
| AC-008 | FAIL | 创建/改父导致「自身」超 3 级被正确拒绝（测试覆盖），但「改父移动含子节点的子树」时未校验后代层级，可造出第 4 级分类，见 CLEAN-001。 |
| AC-009 | PASS | 集成测试覆盖无 token 删除 401、有子禁删 409/3003 且数据不变、叶子删除成功且详情 404。「删除不存在 id → 404」代码路径正确（`findOne→nil→3001`）但无显式测试（见 P3 建议）。 |
| AC-010 | PASS | 集成测试覆盖 status=0 后记录仍在（dbCount=1）、树不再展示该分类、详情仍 200；单元测试覆盖禁用父隐藏其启用子。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 退出码 0 |
| `go vet ./...` | PASS | 退出码 0 |
| `go test -count=1 -race ./internal/controller/categories/... ./internal/logic/categories/...` | PASS | 两个包均 ok，无 race |
| `go test ./...` | PASS（有波动） | 首次运行 `internal/controller/iam` 报 `:8000 address already in use`；单独运行该包通过、二次全量通过。根因是 health/iam 既有测试用 `g.Server(guid.S())` 未 `SetAddr(":0")`（基线代码、非本任务引入），分类测试已正确使用 `:0`。 |
| 临时验证 CLEAN-001 | FAIL（复现） | 构造 A(顶)/B(顶)/C(B子)/D(顶)/E(D子)，`validateParent(parentOf, E, B)` 返回 nil，即允许把带子树 B 移到 E 下，令 C 成为第 4 级。验证用临时测试已删除，不影响工作区。 |

## Findings

### CLEAN-001：改父移动含子节点的子树时未校验后代层级，可产生第 4 级分类

- Severity：P1
- Status：OPEN
- Location：`internal/logic/categories/categories.go` `validateParent`（L323-338）及 `levelOf`（L341-362）
- AC / Invariant：AC-008「创建或改父后会导致分类层级超过 3 级时返回 400/3004，且不产生第 4 级分类」；层级上限不变量 maxLevel=3。
- Trigger：把「含子节点的分类」改父到一个较深的父分类，使被移动节点自身层级仍 ≤3，但其后代层级超过 3。例如：A(顶)、B(顶)、C(B 的子，层级2)、D(顶)、E(D 的子，层级2)；`PUT /categories/B {parent_id:E}`。
- Actual：`validateParent` 只校验 `levelOf(parentID)+1 ≤ 3`（被移动节点自身的新层级），未校验被移动节点子树的最大深度。上述场景 B 新层级=3 通过校验，C 随 B 下移后变为第 4 级，写入成功。
- Expected：改父时必须保证整个被移动子树最深后代 ≤ 3 级；否则返回 400/3004 且数据不变。
- Impact：违反「最多 3 级」核心业务不变量，可产生第 4 级分类脏数据；且 `buildTree.visible` 的 `depth > maxLevel` 是离一层判断（depth 0 起、`>3` 才隐藏），第 4 级（depth=3）仍会展示，无法兜底。
- Evidence：
  - 代码：`validateParent` 仅 `levelOf(parentID)+1`，无子树深度计算。
  - 实测复现（临时单测，已删除）：`validateParent({1:0,2:0,3:2,4:0,5:4}, 5, 2)` 返回 `nil`。
  - 测试缺口：`TestValidateParent` 仅覆盖「叶子改父」（`reparent 4 under 1`），`TestCategoriesEndToEnd` 的 AC-008 仅覆盖「把顶级节点改到三级父」与「三级下创建子」，均未覆盖「移动含子节点的子树」。
- Required Fix Boundary：改父校验必须纳入被移动子树的深度（新父层级 + 子树最大相对深度 ≤ 3），保证任何改父都不会产生第 4 级分类；并新增能区分修复前后的回归测试（移动含子节点的子树导致后代超 3 级必须 400/3004 且父子关系不变）。不规定具体算法（如预计算每节点子树高度或递归下探）与额外抽象。

### CLEAN-002：缺少「更新/删除不存在 id → 404」的显式回归测试

- Severity：P3
- Status：OPEN
- Location：`internal/controller/categories/categories_test.go`（`TestCategoriesEndToEnd`）
- AC / Invariant：AC-006「更新 id 不存在 → 404/3001」、AC-009「删除不存在 id → 404/3001」。
- Trigger：`PUT /categories/:id` 或 `DELETE /categories/:id` 使用不存在的 id。
- Actual：生产代码路径正确（`findOne` 返回 nil → `CodeCategoryNotFound`），但测试未显式覆盖这两条「不存在 id」路径。
- Expected：补充两个断言，锁定 404/3001 行为。
- Impact：仅测试覆盖缺口，无行为缺陷；由 Owner 决定是否补充。
- Evidence：`TestCategoriesEndToEnd` 中更新仅覆盖改名/改排序/改状态/改父与 401，删除仅覆盖 401/有子禁删/叶子删除；`missing` 详情 404 只针对 GET。
- Required Fix Boundary：为 PUT/DELETE 不存在 id 各加一条 404/3001 断言，不涉及生产代码。
