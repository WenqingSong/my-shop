# Cleaner Findings

## Review Target

- Task：`.agent/tasks/flashsale-performance-baseline/task.md`
- 分支：`test/flashsale-performance-baseline`
- 任务基线（Base）：`8106d22b851e73f8cfe5f7770e0cfcfc5e29aad1`
- 初审对象（原任务 C1）：`e67cf2829ecdf6d9ff0febc66f10a406a5fa092f`（loadtest 基线字段扩充 + 摘要生成，已 CLEAN）
- 本次复审对象（新 Review Target）：`cb2a8fd62342aaab611fbd857ad715052b3d0ea9`（fix(prometheus)）
- 复审触发：Owner 指令「复审 cb2a8fd」；`cb2a8fd` 在 `e67cf28` 之后引入非 review-neutral 实质变化（`docker-compose.yml` 等），使先前 CLEAN 客观失效
- `cb2a8fd` 变更范围：
  - `docker-compose.yml`（M）：prometheus 服务由 `image` + bind mount 改为 `build`（镜像内置配置），移除只读挂载
  - `prometheus/Dockerfile`（A）：`FROM prom/prometheus:v2.53.0`，`COPY prometheus.yml` + `COPY rules/`
  - `scripts/test-prometheus.sh`（M）：断言由「只读挂载」改为「镜像内置」
  - `scripts/up.sh`（M）：`docker compose up` 增加 `--build`
  - `storage/banners/banner-{1,2,3}.png`（A）：16x16 占位图（与本 commit 无关，见 CLEAN-001）
- 全局资源：无（`migration_version` / `error_code_domain` 不涉及）
- Design Impact：NONE（Prometheus 部署修复，不涉及 `docs/design/*`）

## Result

CLEAN

## Findings

### CLEAN-001：`storage/banners/banner-{1,2,3}.png` 与 Prometheus 修复无关，误提交进本 commit

- Severity：P3
- Status：OPEN
- Location：`storage/banners/banner-1.png` / `storage/banners/banner-2.png` / `storage/banners/banner-3.png`
- AC / Invariant：N/A（本 commit 为 Prometheus 部署修复，不属 loadtest 任务 AC）
- Trigger：提交 `cb2a8fd` 时把 banner 占位图一并加入
- Actual：3 个 16x16 RGB 占位 PNG（各 87 字节）随 Prometheus 修复一起入库，当前分支全仓库无任何代码引用
- Expected：与 Prometheus 修复无关的文件不应进入本 commit
- Impact：仓库混入未引用二进制占位文件；这些资产属 `banner-v1` 特性（其 contract 约定占位图由启动 seed 写入 LocalStorage，而非静态入库），当前分支无消费方
- Evidence：`git show --stat cb2a8fd` 显示 3 个 `storage/banners/*.png`；`od -c` 确认 16x16 PNG；`grep -rI banner` 除 `.agent/tasks/banner-v1/contract.md` 与 registry 外无消费方
- Required Fix Boundary：移除这 3 个文件，或移至 `banner-v1` 特性分支；不得改动 Prometheus 修复本身

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `bash -n scripts/test-prometheus.sh scripts/up.sh` | PASS | 语法通过 |
| `bash scripts/test-prometheus.sh` | PASS | EXIT=0（静态断言通过；本机无 promtool，镜像语法校验分支跳过） |
| `docker build -t my-shop-prometheus:local-test ./prometheus` | PASS | EXIT=0，COPY prometheus.yml + rules/ 成功 |
| 镜像内文件路径 | PASS | `/etc/prometheus/prometheus.yml`(882B)、`/etc/prometheus/rules/flashsale_latency.yml`(1456B) 就位 |
| `promtool check config /etc/prometheus/prometheus.yml`（镜像内） | PASS | SUCCESS：1 rule file found，config 语法有效 |
| `promtool check rules /etc/prometheus/rules/flashsale_latency.yml`（镜像内） | PASS | SUCCESS：2 rules found |
| 路径三边一致 | PASS | `--config.file=/etc/prometheus/prometheus.yml`(compose) ↔ `COPY prometheus.yml → /etc/prometheus/prometheus.yml`(Dockerfile) ↔ `rule_files: /etc/prometheus/rules/flashsale_latency.yml`(prometheus.yml) 全部对齐 |

## 原任务 Acceptance Criteria（初审 e67cf28，已 CLEAN）

原 loadtest 基线 + 摘要任务在初审（`e67cf28`）已全部 AC PASS。本复审（`cb2a8fd`）只针对 Prometheus 修复，未改动 `scripts/flashsale-loadtest/*`，原 AC 结论不变。
