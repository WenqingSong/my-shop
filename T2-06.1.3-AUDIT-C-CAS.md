# T2-06.1.3 审计报告 C — F-06：Fake Kubernetes CAS 与并发语义

## 0. 执行与取证方式

| 项 | 事实 |
|---|---|
| 独立审计子 Agent | `agent-c-cas`（独立上下文，只读源码，无 shell） |
| 主 Agent 复核 | 逐行阅读 `fakestate` / `fakekubectl` / `harness`；**把 fakekubectl 当黑盒直接驱动**（不经过 Go 测试框架、不修改仓库） |
| 交叉核对 | 与 `agent-a-rbac` 互校「权限模型与真实授权脱节」；`agent-c-cas` 纠正了本项目对「无 rv 的 PUT 真实语义」的过度断言，本报告已改为 NOT_VERIFIED |

## 1. Git 基线

分支 `feat/k8s-t2-06-1-3-rbac-renewal-budget`；HEAD `1919d4a238dac431b194222547c30530f012105f`（= 远端）。

## 2. Fake 数据模型

- 单一 JSON 状态文件 `state.json`（`fakestate.Save` = 临时文件 + rename，`state.go:243-253`），一把全局 `state.lock`（`257-270`，`syscall.Flock`）。
- `Lease`（52-61）：rv / uid / holder / acquireTime / renewTime / duration / transitions / annotations；**只有一个对象**，无 namespace 维度。
- `mutate()`（`main.go:81-100`）：加锁 → Load → fn(st) → Save。**fn 的返回值不影响是否 Save**，失败路径同样落盘。
- 命令分发（`163-197`）：`version / kustomize / auth / create / replace / apply / get / logs / describe / wait` —— **没有 `delete`**。
- `create`（320-351）：rv 硬编码 `"1"`，uid = `lease-uid-<acquisitions+1>`。
- `replace`（359-496）：①续租专用注入(380-419) ②通用超时/500/409/UID 冲突(420-450) ③UID 校验(451-457) ④rv 校验(458-462) ⑤整体覆盖写(464-472) ⑥可选「写成功后立刻被接管」(475-481) ⑦事件与升/续/放分类(484-492)。
- `get lease`（673-698）：**是写操作**（`LeaseGets++`、可触发 steal、持久化）。

## 3. C1：resourceVersion（含主 Agent 黑盒实测）

| 核验点 | 源码 | 实测 | 结论 |
|---|---|---|---|
| create 推进 rv | `337` 赋 `"1"` | create 后 rv=1 | ✅（新对象语义，非「推进」） |
| 成功 replace 推进 rv | `472` | rv 1→2 | ✅ |
| GET 返回当前 rv | `965-987` | ✅ | ✅（`LeaseStealAfterGets` 触发时返回的是 **steal 之后**的 rv，见 C-6） |
| replace 校验 rv | `458` 字符串相等 | 旧 rv=1 再提交 → 409 | ✅（比真实更严格：`"01"` ≠ `"1"`） |
| 409 不修改 Lease 对象 | 校验在写回前 | 409 后 rv=2 / holder=runner-a 不变 | ✅（但 `mutate` 仍 Save 计数器与事件） |
| 新持有者接管后旧 CAS 必失败 | 三处接管都推进 rv | ✅ | ✅ |
| 注入型 409 是否推进 rv | `399-443` 直接 return 1，不推进 | **409 后 rv 仍为 2；用同一 rv 立即重试 → `rc=0` 成功** | ❌ **C-1：注入的 409 与「rv 已变」自相矛盾** |
| 无 rv 的 PUT | `"" != rv` → 409 | 409 | 与真实语义的差异方向：fake 更严格（真实是否无条件更新 **NOT_VERIFIED**） |
| rv 回绕/非数字退化 | `498-504` | 未触发 | `"12abc"→"13"`、`""→"1"`；重建后 rv **回退到 1**（真实 etcd revision 永不回退） |

## 4. C2：UID

| 核验点 | 结论 | 证据 |
|---|---|---|
| 正常更新保持 UID | ✅ `replace` 从不写 `st.Lease.UID` | `464-472`；`takeover_test.go:35-37` |
| 删除重建 UID 变化 | ⚠ **不可由被测脚本触发**：fake 无 `delete`（**实测 `delete` → exit 1「未实现的命令」**）；只能靠改写 `state.json`（主 Agent 的黑盒实验即如此） | `169-196` |
| replace 校验 UID | ✅ 仅当清单 uid 非空；黑盒实测 uid 不匹配 → 409（UID precondition 文案） | `453-457`；`355-357` |
| UID 为空时跳过 | fake 跳过；真实行为 **NOT_VERIFIED** | `453` |
| 旧对象 UID 能否覆盖新对象 | fake 中不能（409），但该路径**无测试**：`LeaseReplaceUIDConflicts`/`LeaseRenewUIDConflicts` 在 `*_test.go` 中出现 0 次 | `438-443` |
| `takeover_test.go:104` 的 `requireNoEvent("lease.uid-conflict")` | **永真断言**（uid 不可能变化），不构成证据 | `takeover_test.go:104` |

补充：uid = `lease-uid-(acquisitions+1)`，而 takeover 也令 `acquisitions++` ⇒ 序号与「对象代数」脱钩（create→uid-1，接管后重建→uid-3，uid-2 被跳过）；唯一性成立，但不是对象身份。

## 5. C3：接管语义（4 套实现，2 套缺字段）

| 位置 | holder | rv | renewTime | transitions | 时机 | 判定 |
|---|---|---|---|---|---|---|
| `main.go:380-388`（LeaseStealBeforeRenewReplace） | ✅ | ✅ | ✅ now | ✅ +1 | CAS **之前** | 符合「一次成功写入」 |
| `main.go:475-481`（LeaseStealAfterState） | ✅ | ✅ | ❌ | ❌ | A 的成功写之后 | 半次写（顺序正确） |
| `main.go:700-704`（stealLease / LeaseStealAfterGets） | ✅ | ✅ | ❌ | ❌ | **在 GET 之内** | 半次写 + 顺序错误 |
| `harness_test.go:621-633`（测试侧） | ✅ | ✅ | ✅ | ✅ | 带外 | 与 fake 实现已漂移 |

主 Agent 黑盒实测（`LeaseStealAfterGets=1`）：`holder=runner-b`、`rv=2`、`transitions=0`、`renewTime` 保持旧值 —— 直接实证 `700-704` 的缺陷。

更严重的不是缺字段，而是**读改写顺序**：steal 由 GET 触发、渲染在 steal 之后，同一个 GET 返回的是接管后状态。读者永远拿不到「陈旧 rv + 自己仍是 holder」的组合——而这正是接管竞态唯一危险的形态。真正构造该窗口的只有 `380-388`。

`acquireTime` 三处都不刷新，而脚本 `lease_takeover` 会写 `now`（`deploy-k8s.sh:775-780`）⇒ fake 与脚本对同一字段语义不同。

## 6. C4：并发竞争

```text
t0  A get lease                     → rv=12, holder=A
t1  B replace(holder=B, rv=12)      → 成功，rv=13, holder=B
t2  A replace(rv=12)                → UID 通过 → rv 12≠13 → 409
t3  A 记「续租 CAS 冲突」，sleep 0.2，重试
t4  A get lease → holder=B ≠ A → lease_renew return 1
t5  A 写 lost + SIGTERM → exit 3
```

**A 不会误判续租成功**（`replace` 成功才 return 0）。

flock 的证明边界：
- **证明**：单请求内读-改-写原子；两进程 `create` 只有一个成功，`AlreadyExists` 是真实竞争产物。
- **未证明**：跨请求原子性 —— 但这与真实 apiserver 一致（每请求原子、请求间不原子），**是正确粒度，不是缺陷**。
- **真正缺口**：**没有任何测试让两个 Runner 同时进入接管竞争**；跨进程并发只出现在 `TestLeaseConcurrentRunnersOnlyOneDeploys`（`lease_test.go:75-121`），而它竞争的是 `create`（互斥由 flock 提供，不由被测代码提供）。
- **接管 CAS 失败分支零覆盖**：`deploy-k8s.sh:1158-1172` 从未被执行（`LeaseStealBeforeRenewReplace` 只对 `isRenew` 生效；能命中接管的 `LeaseReplaceConflicts` 无任何测试）。
- 唯一自然 409（主进程写 `lease-record.yaml` 与续租抢 rv，脚本 `:790` 注释）依赖调度，无确定性测试。

## 7. C5：测试有效性

| 核验点 | 结论 | 证据 |
|---|---|---|
| 真实执行生产脚本 | **是** | `harness_test.go:121-122`、`861-881` |
| 独立进程 | **是**（独立 bash 进程组 + 每次 kubectl 新进程） | `868` |
| 状态隔离 | **是**（每 fixture 独立 state/TMPDIR/HOME，PATH 剔除真 kubectl） | `160-186`、`259-277` |
| 共享状态 | **是（有意）**：同一 `state.json` 即「集群」；但下面的 C-5 是**无锁共享** |
| 只测辅助函数 | 基本否；`takeover_test.go:23-44` 例外（测的是 harness 副本） | C-13 |
| 断言强度 | 多处 `requireEvent/countEvents` 是「前缀存在」断言，事件由 fake 自造，属自证 | — |
| **测试侧写状态是否持锁** | **否**：`fixture.updateState`（`472-479`）与 `fixture.stealLease`（`621-633`）裸 Load→Save，而它们恰在脚本运行中调用 | C-5（High） |

**fake 结构上不可能发现**：真实 apiserver 是否把 rv 当 CAS 前置条件（fake 用自己的实现自证）；etcd 事务/多 apiserver/线性一致读/watch 旧值；rv 空值的无条件更新；rv 全局 revision 单调性；UID 前置条件在普通 PUT 上是否存在；真实 RBAC/Admission 拒绝；服务端默认值/裁剪/不可变字段；finalizer/宽限期/GC/TTL 时序；**跨节点时钟偏移**（fake 与脚本同钟）；真实超时语义（部分写、可能已提交）。

## 8. 与真实 Kubernetes 的差异表

分类：**假阴性** = fake 更宽松 → 生产缺陷漏检；**假阳性** = fake 更严格或语义不同 → 测试绿/红但结论不同；**无影响**。

| # | 差异 | 影响 | 证据 |
|---|---|---|---|
| D1 | rv 为 per-object 计数器，非全局 revision | 假阴性 | `337,498-504` |
| D2 | rv 空值：fake 必 409，真实可能无条件更新 | **假阳性（诊断误导）** | `458`；真实语义 NOT_VERIFIED |
| D3 | 重建后 rv 回退到 1 | 假阴性 | 实测 |
| D4 | uid 为计数器派生，非 UUID，与对象代数脱钩 | 假阴性（低） | `338` |
| D5 | 删除重建不可达 → 脚本两条 uid 校验永不走 | 假阴性 + 断言永真 | `169-196`；`deploy-k8s.sh:806-809,961-965` |
| D6 | 注入 409 不推进 rv | **假阳性（语义不同）** | `399-443` vs `458,472`；实测 |
| D7 | `LeaseExpireAfterRenewConflicts` 在失败请求上改状态 | 假阳性 | `402-411` |
| D8 | 按文件名注入故障 | 假阴性/假阳性 | `377,445` |
| D9 | 契约/预检/写路径三层皆不连接真实授权 | 假阳性 | `232-284,320-496`；`test/manifest/rbac_test.go:114` |
| D10 | GET 有副作用（可 steal、改 rv） | 假阳性 | `673-698` |
| D11 | 无 watch/缓存/线性一致读 | 假阴性 | 全局 |
| D12 | apply 无服务端校验；按文本 `kind:` 定阶段 | 假阴性 | `556-600` |
| D13 | deletionTimestamp/finalizer/GC/TTL 为静态 bool | 假阴性 | `state.go:74,110` |
| D14 | 先打印 `replaced` 再 Save（ack-before-commit） | 假阳性 | `483-493` vs `94-99` |
| D15 | 不支持 `-o jsonpath` | 假阴性/难诊断 | `939-945` |
| D16 | 时钟同源 | 假阴性（时钟偏移缺陷不可测） | `383,405` |
| D17 | leaseTransitions 全由客户端决定 | 无影响，但三处注入不一致 | `469,385,478` |
| D18 | 无 etcd 事务/多 apiserver | 假阴性（最核心不可证明项） | `257-270` |

## 9. 缺陷清单（C-1 … C-17）

| ID | 严重度 | 缺陷 | 证据 |
|---|---|---|---|
| **C-1** | **High** | 注入型 409 不推进 rv，用同一 rv 立即重试可成功（**主 Agent 黑盒复现**）→ CAS 冲突不可证伪 | `399-443` vs `458,472`；实测 |
| **C-2** | **High** | 接管 CAS 失败分支零覆盖；3 个 conflict 注入字段无测试使用 | `deploy-k8s.sh:1158-1172`；`main.go:432-443` |
| **C-3** | **High** | 接管 4 套实现、2 套缺 renewTime/transitions；`700-704` 在 GET 内做写并返回写后状态 | `380-388,475-481,700-704`；实测 |
| **C-4** | **High** | 无 `delete`（实测 exit 1）→ 删除重建/UID 变化不可由脚本触发；两条 uid 校验与 `takeover_test.go:104` 无意义 | `169-196` |
| **C-5** | **High** | `updateState`/`stealLease` 绕过 flock，与 `mutate` 形成丢更新（恰在最关键的「在途失锁」用例中） | `harness_test.go:472-479,621-633` vs `main.go:81-100` |
| **C-6** | Medium | `LeaseStealAfterGets` 在 GET 内写并返回写后状态，消除「读旧 rv 后被抢」窗口 | `692-696,700-704` |
| **C-7** | Medium | 故障注入按文件名判定 | `377,445` |
| **C-8** | Medium | 契约/预检/写路径三层皆不连接真实授权（写路径永不 403） | `232-284` vs `320-496` |
| **C-9** | Medium | rv 计数器化、create 恒 `"1"`、重建回退、非数字退化；`nextResourceVersion` 双份实现 | `337,498-504`；`harness_test.go:637-643` |
| **C-10** | Medium | 写成功消息在 Save 之前输出（ack-before-commit） | `483-493` vs `94-99` |
| **C-11** | Medium | `LeaseExpireAfterRenewConflicts` 在失败请求上改写 renewTime/rv | `402-411` |
| **C-12** | Medium | 接管注入排在其它失败注入之前并持久化 → 「接管」被归因到未到达 CAS 的请求 | `380-398` |
| **C-13** | Low | `TestStealLeaseAdvancesResourceVersion` 测的是 harness 副本，不是 fake，也不是脚本；副本已漂移 | `takeover_test.go:23-44` |
| **C-14** | Low | `apply` 无服务端校验；未知内容当 namespace 成功 | `556-600` |
| **C-15** | Low | 单对象模型，`replace` 不校验 name/namespace，任意名字都改写唯一那把锁 | `359-496` |
| **C-16** | Low | 服务端对象与测试计数器/故障注入混在同一 `state.json`，断言时不可区分 | `state.go:127-224` |
| **C-17** | Low | 不支持 `-o jsonpath`，静默降级为表格 | `939-945` |

## 10. 最终判定

| 项 | 判定 |
|---|---|
| C1 resourceVersion | **部分 PASS**：推进/校验/409 骨架成立（黑盒实测）；**注入型 409 不推进 rv（C-1）** |
| C2 UID | **PASS（前置条件校验）/ 删除重建不可达（C-4）** |
| C3 接管语义 | **FAIL**（C-3：4 套实现、2 套缺字段、GET 内写） |
| C4 并发竞争 | **PASS（替身层面）**；接管竞态零覆盖（C-2） |
| C5 测试有效性 | **FAIL（部分）**（C-5 无锁写入、C-8 权限脱节、C-13 自测副本） |
| 真实 API Server 语义 | **NOT_VERIFIED** |

**F-06：原始缺陷（`stealLease` 不推进 resourceVersion）已修复并回归验证 → PASS；但替身保真度未达标（C-1/C-3/C-4/C-5）→ 整体「部分通过」。**
`TestStaleResourceVersionCannotOverwriteTakeover` 是唯一有效的 CAS 回归证据；conflict 注入类用例只能证明调用序列。

## 11. NOT_VERIFIED

| 项 | 原因 |
|---|---|
| 真实 apiserver 对 **rv 为空** 的 PUT 是否无条件更新 | 无集群；`AllowUnconditionalUpdate` 未被实测（不得作为已验证事实） |
| 真实 apiserver 是否在普通 PUT 上校验 `metadata.uid`、uid 为空时的行为 | 无集群 |
| rv 为 etcd 全局 revision 的单调性/回绕/跨对象可比较性 | 无集群 |
| etcd 事务内 CAS、多 apiserver、quorum、线性一致读、watch 缓存陈旧 | 无集群 |
| 真实 409/UID 冲突文案与 kubectl 退出码分类 | 无集群（脚本靠文本匹配：`1135,1076`） |
| `leaseTransitions`/`acquireTime` 是否被服务端维护 | 无集群（fake 全由客户端决定） |
| finalizer/宽限期/GC/TTL 真实时序 | 无集群 |
| 真实 RBAC/SubjectAccessReview 对 write 动词的判定 | 无集群 |
| 跨节点时钟偏移下的过期判定 | fake 与脚本同钟 |
| Server-Side Apply 字段管理与冲突 | 脚本用 client-side apply，fake 不建模 |
