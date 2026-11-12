# API 时间字段语义（当前实现）

> 本文记录 SurgeCart HTTP API 响应时间字段的**真实行为**，用于替代"默认 UTC / 默认 RFC3339"之类的错误假设。
> 本文不改变任何现有接口格式；新 API 版本的要求见 §5。

## 1. 适用范围

所有响应 DTO 中类型为 `*gtime.Time` 的字段，在 OpenAPI 中表现为：

```yaml
type: [string, "null"]
x-go-type: gtime.Time
pattern: ^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$
x-format: gtime-local
```

代表性字段：订单 `created_at`/`paid_at`/`shipped_at`、钱包流水 `created_at`、佣金 `settled_at`/`reversed_at`、
成长值流水 `created_at`、秒杀活动时间、退款与优惠券时间等。

可通过以下命令枚举全部受影响字段：

```bash
grep -c 'x-format: gtime-local' openapi/surgecart.openapi.yaml
grep -B3 'x-format: gtime-local' openapi/surgecart.openapi.yaml
```

## 2. 实际序列化行为

- 格式：`YYYY-MM-DD HH:MM:SS`（例如 `2026-11-10 11:27:26`）。
- **不包含时区偏移**，也不是 RFC3339（没有 `T`、没有 `Z`/`+08:00`）。
- 空值：字段可为 `null`（对应 Go 的 `*gtime.Time`）。
- 该格式由 `gtime.Time` 的默认 JSON 序列化产生，属于既有 API 行为，当前不做修改。

## 3. 时区来源

| 环节 | 实际配置 |
|---|---|
| 业务时区 | `dashboard.timezone`，默认 `Asia/Shanghai`（`manifest/config/config.yaml`） |
| MySQL 会话 | 启动时按上述配置计算固定偏移并写入 DSN：`SET time_zone='+08:00'`；含 DST 的时区在启动时 fail-fast |
| 应用进程 | 容器/主机 TZ 应与业务时区一致（本地环境为 `+08:00`） |
| API 响应 | 只输出墙钟时间数字，不携带时区标识 |

因此：

- 响应中的时间数字按**部署配置的业务时区**解释，默认 `Asia/Shanghai`。
- 客户端**不得假定**该时间为 UTC，也不能从响应体推断服务端时区。
- 若部署修改 `dashboard.timezone`，历史数据的数字语义随配置变化，需运维侧保证配置稳定。

**未明确项**：API 契约没有字段承载时区偏移或时区名；这是当前实现的已知限制，不属于本次可修复范围。

## 4. 兼容性决定（B1-05-FINAL-FIX-02 / O2）

- **不修改**现有接口的时间序列化格式，避免破坏既有客户端解析。
- 不新增 `Z`/`+08:00`，不改变字段类型，不改变字段可空性。
- OpenAPI 继续以 `x-format: gtime-local` + `pattern` 如实描述，不伪装为 `date-time`。

## 5. 新 API 版本要求

后续新增 API 版本或新接口时：

1. 时间字段统一使用 RFC3339（含时区偏移），例如 `2026-11-10T11:27:26+08:00`。
2. OpenAPI 必须声明 `format: date-time`，不得继续使用 `gtime-local`。
3. 不得在同一 API 版本内混用两种时间格式。
4. 旧版本保持本文 §2 的格式，直至正式下线。

## 6. 验证方法

```bash
# 数据库会话时区与当前时间
mysql -N -e "SELECT @@session.time_zone, NOW();"

# 任意返回 gtime.Time 的接口，对比响应字段与数据库值
# 期望：响应数字 == 数据库墙钟时间，且响应中无 +08:00 / Z 后缀
```

## 7. 相关文档

- `docs/design/dashboard.md` §3：时区落地与 DST 限制
- `docs/design/deployment.md`：容器 TZ 与数据库配置
- `openapi/surgecart.openapi.yaml`：契约中逐字段的 `x-format` / `pattern`
