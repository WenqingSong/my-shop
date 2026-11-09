# T2-06.1.3 审计报告 B — F-02 续租预算与 F-05 失锁窗口

## 0. 执行与取证方式

| 项 | 事实 |
|---|---|
| 独立审计子 Agent | `agent-b-lease`（独立上下文，只读源码，无 shell） |
| 主 Agent 复核 | 逐行阅读 `lease_renew` 及其全部调用点；`/tmp/audit-b` 注入 50s 预检延迟；参数空间枚举；`/tmp/trap-delay.sh` 实测 bash trap 延迟 |
| 交叉核对 | 与 `agent-c-cas` 核对 409 注入的语义；`agent-b-lease` 据主 Agent 实测修正了检测延迟数值（29s → 31.6s） |

## 1. Git 基线

分支 `feat/k8s-t2-06-1-3-rbac-renewal-budget`；HEAD `1919d4a238dac431b194222547c30530f012105f`（= 远端）；HEAD^ `6919139`。

## 2. 续租完整调用链（源码为准）

```text
lease_renewer_loop (893-914)
  └─ while :; sleep LOCK_RENEW_INTERVAL(10)              # 899（在上一轮 PUT 返回之后才开始）
       if stop 标记 → exit 0                              # 900-902
       lease_renew                                        # 903
         for attempt = 1..LOCK_RENEW_MAX_ATTEMPTS(3)      # 803
           lease_read  → kubectl get lease（≤3s）          # 731-745 / 804
           holder != self        → return 1（失锁）        # 805
           uid 变化              → return 1                # 806-809
           lease_expired         → log + return 1（不写）  # 810-815
           lease_record_from_lease（保留记录）             # 817
           kubectl replace -f lease-renew.yaml（≤3s）      # 823
             成功 → return 0；失败 → log + sleep 0.2       # 823-829
         return 1（attempts 用尽）
       失败且无 stop → : > lost; kill -TERM $$            # 908-911
```

关键事实：

- 一次 attempt = **2 个 kubectl 进程 / 2 个 HTTP 请求**（GET + PUT），各自受 `--request-timeout=3s` 约束。
- **GET 失败不重试**：`lease_read || return 1`（804），attempts 只覆盖 PUT。
- 退避 `sleep 0.2` 是**字面量**（829）；`LOCK_RENEW_BACKOFF_MS`（102）只参与 `:404` 的校验，**从未被使用**（已 grep 确认）。
- 退避在**每次失败后都执行，含最后一次**，真实退避总量 = 3×0.2 = **0.6s**，而校验用的是 `(A-1)×0.2 = 0.4s`。
- 续租链期间没有其他 kubectl 调用。

## 3. 独立数学推导（修正交付报告的数值）

符号：`I`=10，`A`=3，`G`=3，`R`=3，`β`=0.2，`B`=1，`M`=5，`D`=45，`δ_succ`=最后一次成功 PUT 的耗时（≤R），`δ_sched`=续租进程调度延迟，`ε`=每条链的进程启动开销。

```text
单链最坏   C = A(G+R) + A·β + ε = 18 + 0.6 + ε = 18.6s + ε
下一次链起点 = t0 + δ_succ + I            （sleep 在 PUT 返回后才开始，:899）
发现失锁   = t0 + δ_succ + I + C ≤ t0 + 3 + 10 + 18.6 = t0 + 31.6s + ε
服务端过期 = floor(renewTime) + D          （renewTime 为秒级截断，:576）
            ∈ [t0 + 44, t0 + 45]
余量       = 44 − 31.6 = 12.4s（不是文档的 16s，也不是上一版的 28.4/45 模型）
```

交付文档的「29s」「42s」「余量 16s」均低估/高估了这一项：

| 交付报告/文档 | 独立推导 | 差异来源 |
|---|---|---|
| 检测 ≤ 29s | **≤ 31.6s** | 漏了最后一次成功 PUT 的 `δ_succ ≤ R`（3s）与最后一次退避 0.2s |
| 可接管 ≥ 42s | **[44s, 45s]** | `renewTime` 由客户端生成并秒级截断，过期即 `floor(t0)+D` |
| 余量 16s / 15s | **12.4s** | 同上两项 |

参数守卫（`:409-416`）：`lock_budget = I + A·2T + B + M`；`if (( lock_budget >= D )) die 1`（严格小于）。

- 结构一致、边界语义正确（`D=34` 拒绝、`D=35` 通过，已实测）。
- 对「从新鲜 t0 起算的失败链」保守成立：31.6 < 34 < 45；**守卫自身的余量只有 2.4s**，最紧的合法组合（`D = I+6T+7`）余量 **3.4s**。

### 独立枚举（137,634 个合法组合，`D∈[1,180]`、`I∈[1,D]`、`T∈[1,60]`）

| 每链进程开销 ε | 出现 `C ≥ D` 的合法组合数 |
|---|---|
| 0 / 1 / 2 / **3.0s** | **0** |
| 3.4s | 2408（全部是 `D = I+6T+7` 的最紧组合） |
| 4.0s | 2408 |
| 6.0s | 7140 |
| 6.7s | 9464 |

（说明：上一版报告中的「6.0s 无越界」基于 28.4s 的乐观模型；按含 `δ_succ` 的精确模型，容差是 **3.0s**。）

## 4. t0 的定义错误（F-02 未闭环的关键，已实测证实）

源码事实：

- `LOCK_ACQUIRED_AT="$(date -u ...)"` 在**参数解析后、任何网络调用之前**（456）。
- `lease_create` 把它同时写成 `acquireTime` 与 `renewTime`（763-764）。
- 续租进程在获取锁之后启动，**先 sleep 10s**（899）才首次续租。
- create 之前的 API 调用：`kubectl version`（≤30s）、2×`kustomize`（本地无上界）、`get namespace`（≤30s）、可选 `apply namespace`（≤30s，锁之前的写）、7×`auth can-i`（各 ≤30s）⇒ `D` 最坏 ≈ 270~300s，典型 1~3s。

### 独立复现（`/tmp/audit-b`，真实执行生产脚本 + 真实 fake）

| 事件 | 时刻 |
|---|---|
| 脚本启动（`LOCK_ACQUIRED_AT`） | `12:02:32Z`（epoch 1794139352） |
| `kubectl version`（注入 50s）结束 | `12:03:22Z` |
| `create -f lease-create.yaml` | `12:03:23Z`（**启动后 51s**） |
| 写入的 `renewTime` / `acquireTime` | `2026-11-08T12:02:32Z` |
| 首次服务器复核 `require_lock_owned_server_side` | 判定「租约已过期」→ **exit 3** |

### 阈值（默认参数）

| create 前耗时 D | 后果 |
|---|---|
| < 16.4s | 首轮链（`D+10+18.6`）在过期前结束，安全 |
| 16.4 ~ 35s | create 时未过期，但首轮链跨过过期点；检测滞后于「可接管」 |
| 35 ~ 45s | 首次续租 GET 即判过期；`[45, D+10]` 内已可被合法接管 |
| ≥ 45s | **Lease 创建即过期**，任何 Runner 下一次轮询（每 2s）即可接管；本 Runner 在 `require_lock_owned_server_side`（1853→1008-1018）exit 3 |

正确的不变量应为 `D + I + A(G+R) + backoff + margin < D_lease`，而 `D` 不可由参数校验约束。

## 5. 过期后仍写 Lease 的路径（B-3，复活窗口）

| 路径 | 是否可写过期 Lease | 证据 |
|---|---|---|
| `lease_renew` | 否（每 attempt 先 GET，过期即 return 1） | 804-815 |
| `require_lock_owned_server_side` | 否（旧的「立即续租两次」已删除） | 1008-1018 |
| `migration_record_write` | **是**：5 次重试中只校验 holder（1505），**不校验过期**；1567 用 `now` 写 renewTime | 1500-1571 |
| `lease_release` | 是（只查 holder，不查过期） | 854-885 |

可达路径：续租进程判过期 → 写 lost（908）→ 主进程恰在 `wait_for_migration` 的 `require_lock_alive`（1913）之后进入 `record_migration_state`（1929）→ `lease_read` 见 holder 仍是自己 → PUT 成功 ⇒ **lost 之后仍发生一次 Lease 写，并把 `renewTime` 刷新为 now**（等于把过期锁续命一个租期）。随后主进程会在下一个检查点 exit 3，因此不构成并发写，但会让「lost ⇒ 不再写 Lease」这一断言失真，并最多使他人接管延迟一个租期。

## 6. 故障注入结果（真实退出码/时间线）

| # | 注入 | 证据 | 结果 |
|---|---|---|---|
| 1 | GET/PUT 各延迟 0.9s（超时 1s），3 attempt 全失败 | `TestRenewChainStaysWithinBudget`（已执行） | 实测链 **5.928s**（含 0.6s 退避）≤ 公式上界 7s；确有 3 次 GET + 3 次 REPLACE |
| 2 | 续租 PUT 连续 409 / 500 / 客户端超时 / GET 超时 | `TestRenewThreeCASConflicts…`、`TestRenewAPI500…`、`TestRenewReplaceTimeout…`、`TestRenewGetTimeout…` | 均 exit 3，`apply.application` 未发生（409 用例只证明调用序列，见 C-1/B-15） |
| 3 | 重试期间服务端租约过期 | `TestRenewAbortsWhenLeaseExpiresDuringRetries` | 过期后不再写 Lease（注：该注入在失败请求里改状态，非真实可达，仅作状态构造） |
| 4 | 续租 CAS 前被接管（rv 推进） | `TestStaleResourceVersionCannotOverwriteTakeover` | 旧 rv → 409，新 holder 不被覆盖，exit 3 |
| 5 | Application apply 在途失锁 | `TestLockLostDuringApplicationApplyReportsUnknown` | Deployment **确实被改**（`Applied==true`）、UNKNOWN、exit 3、不重试 |
| 6 | API 已接受但客户端超时 | `TestApplicationApplyAcceptedButClientTimesOut` | exit 1、UNKNOWN、`Applied==true` |
| 7 | SIGSTOP → 接管 → SIGCONT | `TestPausedRunnerResumesAfterLockLoss` | 恢复后完成在途 apply、UNKNOWN、exit 3 |
| 8 | 慢预检（主 Agent 新增） | `/tmp/audit-b` | 新建 Lease 即过期 → exit 3 |
| 9 | bash 前台 `sleep 3` 期间收到 TERM | `/tmp/trap-delay.sh` | TERM `...429138` → trap `...431839`，**延迟 2.7s** |

## 7. F-05：四类场景（分开，不合并）

记 `T` = 接管完成时刻，`t0` = 最近一次成功写入的 renewTime。

### 情况一：正常存活 Runner 被接管 → 发现失锁

- 接管前提 `T ≥ t0+44`；发现时刻 `≤ t0+31.6+δ_sched`。
- ⇒ 从 T 起算的延迟 = `max(0, δ_sched − 12.4)`；正常调度（`δ_sched ≤ 12.4s`）时为 **0s**（接管发生时旧 Runner 已写出 lost）。
- 接管发生在链中途（仅时钟偏移或外部强制）⇒ ≤ `R + β + G = 6.2s`。
- 时钟偏移 S > 12.4s 会直接吃掉余量。

### 情况二：lost → 主进程停止发起新变更

- **不再发起新请求：0s**（bash 只在前台命令结束后执行 trap，实测延迟 2.7s 即该机制；lost 先写、SIGTERM 后发，顺序正确）。
- **进程退出/在途命令结束**：≤ 当前前台命令剩余时长 —— `KUBECTL` ≤30s、`LEASE_KUBECTL` ≤3s、`wait_for_migration` 的 `sleep 1` ≤1s、rollout 的 `sleep 2` ≤2s、acquire 的 `sleep 2` ≤2s。
- 缺口：`require_lock_alive`（1913）到 `record_migration_state`（1929）之间 lost 可能被写入，此时仍会发起一次写（B-3）。

### 情况三：已发出的 kubectl 请求

- Application apply：**可以在接管后生效**；`observe_lock_after_request` 只报告不撤销；测试断言 `Applied==true`。
- Migration apply：**可以**，且 `begin_migration`（1882）之后没有 read-after-write/UNKNOWN 报告路径（B-13）。

### 情况四：SIGSTOP/SIGCONT

- 续租链：恢复后第一步 GET；已过期则不 PUT；未过期则 PUT 用旧 rv，被接管则 409；重试读到 holder≠self 即停。**不复活**，发现 ≤ G。
- 主进程：阻塞在 apply 中 → 请求继续（情况三）；返回后 observe → holder 变化则 UNKNOWN + exit 3。
- **但 `observe_lock_after_request` 不检查过期**（949-967）：「已过期但未被接管」会被判成功，随后 rollout 循环只查本地 lost（2537），要到 2651 才因过期停止（只读，最长 600s）。
- TOCTOU：暂停点若落在 2481 与 2486 之间，恢复后不再复核就发请求。

### 「重叠 0」核验

**必须拆成三个口径**：

| 口径 | 是否成立 | 说明 |
|---|---|---|
| Lease 对象所有权重叠 | **成立** | 陈旧 rv 的 PUT 必 409（`TestStaleResourceVersionCannotOverwriteTakeover`）；过期续租被放弃 |
| 旧 Runner 不再**发起新请求** | **成立（带前提）** | 前提 `δ_sched ≤ 12.4s`、无 >12.4s 时钟偏移、续租进程存活。文档用的是 29s/42s/16s，真实是 31.6s/44s/12.4s，结论不变但推导需改 |
| 旧 Runner 的**写仍在生效** | **不成立** | 无需暂停进程。反例（仅需 ~0.7s API 提交延迟）：<br>`t0+44.8` 复核通过（剩 0.2s）→ `t0+44.9` 发出 apply → `t0+45.0` 租约到期 → `t0+45.2` 新 Runner 接管 → `t0+45.5` 新 apply 发出 → `t0+45.6` 旧 apply 在服务端提交（后写覆盖）→ `t0+46.5` 新 Runner 验证看到旧镜像 exit 5，或验证发生在提交之前则打印成功而实际运行旧版本 |

⇒ 「重叠 0」只对「Lease 所有权 + 不再发起新请求」成立；「旧 Runner 仍在写 + 新 Runner 已接管 = 0」是**过度声明**，与 `AUD-05 = NOT_CLOSED` 矛盾。

## 8. 安全窗口分类

| 层 | 窗口 | 长度 | 触发 |
|---|---|---|---|
| Lease 层 | `migration_record_write` 过期后刷新 renewTime（B-3） | ≤6s，续命一个租期 | 续租判过期的同时主进程写记录，且未被接管 |
| Lease 层 | `lease_release` 过期后写入 holder=""（B-4） | ≤5×6s | lost 不存在 |
| 在途写 | 已发出的 apply 在接管后提交 | **无界** | API/网络延迟、代理重试 |
| 在途写 | 暂停期间发出的写 | = 暂停时长 | SIGSTOP / VM 冻结 |
| 恢复后 | 复核→apply 之间被暂停 | 微秒级窗口 | 恢复后无二次复核 |
| 恢复后 | 过期未被接管时主进程不停止（只读） | ≤600s | observe 不查过期 |
| 首轮 | create 前预检耗时吃掉租期 | 见 §4 阈值 | API 变慢 |

## 9. 缺陷清单（B-1 … B-16）

| 编号 | 严重度 | 缺陷 | 证据 |
|---|---|---|---|
| **B-1** | **High（F-02 未闭环）** | 预算 t0 = 脚本启动时刻；`D` 未计入且无上界；`D≥45` 时 Lease 创建即过期（**已实测**） | 456、763-765、899、2143-2248、1046-1064、409-416 |
| **B-2** | Medium | create 成功后无「未过期」断言、无立即续租；`acquire_release_lock` 只读一次 UID 就返回成功 | 1125-1133 |
| **B-3** | Medium | `migration_record_write` 不校验过期、每轮写 renewTime=now ⇒ lost 之后仍可写、可给过期锁续命一个租期 | 1500-1571（1567） |
| **B-4** | Medium-Low | `lease_release` 不校验过期（只查 holder） | 854-885 |
| **B-5** | Low | `LOCK_RENEW_BACKOFF_MS` 是死常量；`sleep 0.2` 硬编码且最后一次也 sleep ⇒ 真实退避 0.6s ≠ 校验用的 0.4s | 102、404、829 |
| **B-6** | Low | GET 失败不重试，一次 3s GET 抖动即终止发布 | 804 |
| **B-7** | Medium | 交付文档推导错误：29s（应 31.6s）、42s（应 44s）、余量 16s/15s（应 12.4s） | `deploy-lock.md:580-589`、`kubernetes.md:669-675` |
| **B-8** | Medium | 「重叠 0」过度声明（见 §7） | `deploy-lock.md:594-601`、`kubernetes.md:687`、`verification.md:107-108` |
| **B-9** | Medium-Low | 续租进程无监督：死亡/冻结时不写 lost，检测延迟无界 | 893-914、971-979 |
| **B-10** | Low | `observe_lock_after_request` 不查过期 ⇒「已过期未接管」被判成功，退出可迟至 600s | 948-967、2537、2651 |
| **B-11** | Low | apply 已返回（INFLIGHT=0）后信号到达时走 541-553 分支，不打印 UNKNOWN，尽管写已发出 | 539-553、2484-2487 |
| **B-12** | Low（部分 NOT_VERIFIED） | 预算按「每请求 3s」计；kubectl 进程可能发多个请求，进程启动开销未计入（容差仅 3.0s，见 §3） | 429、731-732、823-824 |
| **B-13** | Low | 迁移 apply 失锁后无 read-after-write/UNKNOWN 报告路径 | 1851-1905 vs 2489-2498 |
| **B-14** | Info | renewTime 不可解析时按「未过期」处理 ⇒ 畸形 Lease 永久卡锁 | 749-758 |
| **B-15** | Medium-Low（测试保真度） | 所有确定性 409 注入在 rv 校验前返回且不推进 rv（**主 Agent 黑盒复现：同一 rv 重试成功**）⇒ 只能证明调用序列，不能证明「重读后 rv 是新的」；`LeaseExpireAfterRenewConflicts` 在失败请求里改状态 | `fakekubectl/main.go:399-450,458,472`；`renew_test.go:255-286` |
| **B-16** | Info | `:924-926` 注释把「wait 的等待语义」写成「bash 会延迟处理信号」；stop 检查只在循环首（900-902）与失败分支（905-907），语义是「下一轮不再进入 lease_renew」，不是「不再发 kubectl」 | 900-907、924-933 |

## 10. 最终判定

| 项 | 判定 |
|---|---|
| F-02 续租循环公式与真实路径一致 | **PASS**（attempt = GET+PUT；守卫结构与边界语义正确） |
| F-02 覆盖全部执行路径（含 create → 首次续租） | **FAIL**（B-1，已实测） |
| F-05 四类场景区分 | **PASS** |
| F-05 「重叠 0」表述 | **FAIL（过度声明）**（B-8） |
| 是否仍存在安全窗口 | **是**：在途写无界；Lease 层 B-3/B-4；首轮 D 窗口；恢复后只读延迟 ≤600s |

**F-02：FAIL（部分修复）。F-05：FAIL（推导基本正确，结论句不成立）。**

## 11. NOT_VERIFIED

| 项 | 原因 |
|---|---|
| kubectl 单次调用是否发起 discovery、缓存是否预热 | 无集群 |
| `--request-timeout` 对「多请求进程」的墙钟上界 | 无集群；本报告只断言「按请求」语义 |
| 真实 API Server 下 apply 的提交延迟分布（能否 ≥0.7s） | 无集群；反例以该前提成立 |
| 真实 Lease 的 etcd CAS/409 行为 | 无集群 |
| `D` 在生产 CI 的分布 | 需真实 RBAC 预检延迟 |
| B-3 的复活窗口是否可稳定触发 | 仅代码路径推导，未构造确定性触发器 |
| 未捕获 SIGTERM 的非交互 bash 是否立即退出、子进程是否成孤儿 | 手册语义推导，未实测 |
