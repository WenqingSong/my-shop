# Workflow V4 事件日志协议（Event Log Protocol）

本文件定义 Workflow V4 事件日志的固定格式、写入规则与边界。事件日志只用于诊断：为 Agent Handoff、
BLOCKED 诊断与异常恢复提供持久记录，不承载任何审批权威，不参与 Gate 的 PASS/FAIL 判定。

## 1. 目标与定位

- 目标：记录 `START` / `HANDOFF_PREPARED` / `ACK` / `BLOCKED` / `COMPLETED` 等生命周期事件。
- 定位：纯诊断 metadata，与 `state.yaml` 同级；不是审批证据。
- 审批权威仍以 State + Git Evidence 为准（Owner、Contract、Cleaner 的审批证据不受事件日志影响）。

## 2. 文件位置

- Task 事件文件：`.agent/tasks/<task-id>/events.md`
- Program 事件文件：`.agent/programs/<program-id>/events.md`

`state.yaml` 中的 `event_log.path` 仅作为定位信息使用，不修改 Schema，不把日志条目当作审批依据。

## 3. 文件格式

事件文件使用 UTF-8 Markdown。文件开头允许存在一行文件头：

```text
# Workflow Events
```

每个事件占单独一行，以 `- `（短横线 + 一个空格）开始，之后是一个完整 JSON 对象（单行，紧凑编码）。
JSON 对象字段固定为以下 7 个，字段名与顺序不得增改：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `timestamp` | string | UTC RFC3339Nano 时间（偏移必须为 0） |
| `role` | string | 产生事件的角色，非空 |
| `event` | string | 事件类型（见下表），枚举固定 |
| `scope_kind` | string | `task` 或 `program` |
| `scope_id` | string | 对应 Task/Program ID |
| `sha` | string | 关联的完整 Commit SHA；无关联时为空串 |
| `reason` | string | 附加说明；`BLOCKED` 时必填 |

示例（仅定义格式，不代表实际发生过该事件）：

```text
# Workflow Events

- {"timestamp":"2026-10-16T05:00:00Z","role":"Coder","event":"BLOCKED","scope_kind":"task","scope_id":"example-task","sha":"","reason":"缺少批准的 Contract"}
```

## 4. 固定事件类型

- `START`
- `HANDOFF_PREPARED`
- `ACK`
- `BLOCKED`
- `COMPLETED`

不新增任何隐藏的自动调度字段，不把 `NEXT_ROLE` 等字段混入事件协议。

## 5. 写入规则

写入由 `internal/workflow/event_writer.go` 的 `EventWriter` 与 `cmd/workflow-event` CLI 提供。

校验（任一条不满足即拒绝写入）：

1. `scope_kind` 必须是 `task` 或 `program`。
2. `scope_id` 必须是非空、单一安全路径段（`^[A-Za-z0-9][A-Za-z0-9._-]*$`），禁止路径穿越
   （拒绝 `..`、`.`、`/`、`\`、NUL 等）。
3. `event` 必须是第 4 节的固定枚举之一。
4. `role` 非空。
5. `timestamp` 必须是 UTC RFC3339Nano。
6. `sha` 为空串，或 40/64 位十六进制完整 Commit SHA。
7. `event == BLOCKED` 时 `reason` 必须非空。
8. `reason` 中的特殊字符（引号、换行、`<`、`>`、`&` 等）通过 JSON 编码处理，保证事件仍占单行。

写入行为：

- 以追加方式（`O_APPEND`）写入，绝不覆盖既有记录。
- 仅写入 `.agent/tasks/<id>/events.md` 或 `.agent/programs/<id>/events.md`，
  不允许用户传入任意文件路径覆盖源码。
- 写入路径以仓库根为可信边界，逐级 `openat` + `O_NOFOLLOW` + `O_DIRECTORY` 打开/创建，
  最终文件以 `O_NOFOLLOW` 打开；`.agent`、`tasks`/`programs`、`<scope-id>` 目录或
  `events.md` 文件任意一级为符号链接（无论指向仓库内还是仓库外）都 fail-closed，
  不追踪链接继续写入。
- 单进程内用互斥锁串行化追加；跨进程用 `flock(LOCK_EX)` 排它锁串行化，避免多事件交错。
  跨进程原子性以文件锁为准，不依赖无依据的原子性声明。
- I/O 错误（含 `Sync` 落盘错误）正确返回，不伪造成功记录。

## 6. 审批权威边界

事件日志只负责诊断。Gate 不得根据存在 `ACK` 或 `COMPLETED` 事件就推断审批有效；
Owner、Contract、Cleaner 的审批证据仍以 State + Git Evidence 为准。

## 7. Neutral Tail 作用域

- 仅当前 task 自己的事件文件 `.agent/tasks/<task-id>/events.md` 被视为 review-neutral 与
  delivery-neutral metadata（由 `paths.go` 的 `taskScoped` 精确路径匹配限定）。
- 其他 task 的事件文件、子目录下的同名文件、`.agent/programs/*/events.md` 一律 substantive
  （Program 事件路径不进入 task-scoped 白名单，default-deny）。
- 不得通过目录级或 basename-only 白名单放行任意路径；不得将业务代码、测试代码、`contract.md`、
  `docs/design/*`、Registry 授权文件加入 neutral。

## 8. CLI 接口

```text
workflow-event append \
    --scope-kind <task|program> \
    --scope-id <id> \
    --role <role> \
    --event <START|HANDOFF_PREPARED|ACK|BLOCKED|COMPLETED> \
    [--sha <sha>] [--reason <text>] [--timestamp <RFC3339Nano>] [--root DIR]
```

exit code：`0` 成功；`1` 事件校验失败；`2` 用法或 I/O 错误。

## 9. 读取端（Task 04）

事件读取与恢复诊断由 Task 04 独立实现。写入端只保证格式稳定与追加可靠，不要求读取端等待、
不依赖读取端完成。双方共享本文件的字段名与事件类型约定。
