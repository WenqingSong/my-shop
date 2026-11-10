# T5-03 业务核验原始记录（MySQL / Redis / Kafka）

采集时间：2026-11-10 08:29–08:35 UTC
执行方式：`kubectl exec` 进入 MySQL Pod，使用容器内环境变量中的凭据（凭据未出现在任何命令或文件中）。
所有语句均为**只读查询**，未修改任何数据。

---

## 0. 会话时区与时间基准

```sql
SELECT NOW(), UTC_TIMESTAMP(), @@session.time_zone, DATABASE();
```

```text
2026-11-10 07:26:42  2026-11-10 07:26:42  SYSTEM  my_shop
```

容器时区为 UTC；而应用侧把会话时区设为 `dashboard.timezone=Asia/Shanghai`，
两者对 DATETIME 的解释相差 8 小时（见报告 §9.1）。

---

## 1. 活动时间窗核验（按应用会话时区 +08:00）

```sql
SELECT a.id, a.start_time, a.end_time,
       DATE_ADD(UTC_TIMESTAMP(), INTERVAL 8 HOUR) AS now_sh,
       UNIX_TIMESTAMP(a.end_time) - 28800 AS end_epoch_app,
       UNIX_TIMESTAMP(UTC_TIMESTAMP()) AS now_epoch,
       (a.start_time <= DATE_ADD(UTC_TIMESTAMP(), INTERVAL 8 HOUR)
        AND DATE_ADD(UTC_TIMESTAMP(), INTERVAL 8 HOUR) < a.end_time) AS in_window,
       b.total_stock, b.sold
FROM flash_sale_activities a JOIN flash_sale_activity_skus b ON b.activity_id = a.id
WHERE a.id IN (13,14,15) ORDER BY a.id;
```

```text
13  2026-11-10 16:24:02  2026-11-10 18:25:02  2026-11-10 16:25:35  1794306302  1794299135  1  20  0
14  2026-11-10 16:24:06  2026-11-10 18:25:06  2026-11-10 16:25:35  1794306306  1794299135  1  5   0
15  2026-11-10 16:24:10  2026-11-10 18:25:10  2026-11-10 16:25:35  1794306310  1794299135  1  5   0
```

---

## 2. 实验前基线（创建活动前，用于证明未污染历史数据）

```sql
SELECT (SELECT COUNT(*) FROM flash_sale_activities) AS acts,
       (SELECT COUNT(*) FROM flash_sale_activity_skus) AS bindings,
       (SELECT COUNT(*) FROM flash_sale_orders) AS orders,
       (SELECT COUNT(*) FROM flash_sale_order_requests) AS reqs,
       (SELECT COALESCE(MAX(id),0) FROM flash_sale_activities) AS max_act,
       (SELECT COALESCE(MAX(id),0) FROM flash_sale_orders) AS max_order;
```

```text
6  6  2  2  6  2
```

（6 个历史活动、2 笔 T3-02 历史订单；本次实验只新增数据，未修改/删除上述记录。）

---

## 3. 库存与订单（实验后）

```sql
SELECT activity_id, total_stock, sold, total_stock - sold AS remaining
FROM flash_sale_activity_skus WHERE activity_id IN (13,14,15) ORDER BY activity_id;
```

```text
13  20  20  0
14  5   1   4
15  5   1   4
```

```sql
SELECT activity_id, COUNT(*) AS orders, COUNT(DISTINCT user_id) AS users
FROM flash_sale_orders WHERE activity_id IN (13,14,15) GROUP BY activity_id ORDER BY activity_id;
```

```text
13  20  20
14  1   1
15  1   1
```

---

## 4. 一人一单违例检查

```sql
SELECT COUNT(*) AS dup_one_per_user FROM (
  SELECT activity_id, sku_id, user_id FROM flash_sale_orders
  WHERE activity_id IN (13,14,15)
  GROUP BY activity_id, sku_id, user_id HAVING COUNT(*) > 1
) t;
```

```text
0
```

---

## 5. 请求幂等违例检查

```sql
SELECT COUNT(*) AS dup_idem FROM (
  SELECT user_id, idempotency_key FROM flash_sale_order_requests
  WHERE activity_id IN (13,14,15)
  GROUP BY user_id, idempotency_key HAVING COUNT(*) > 1
) t;
```

```text
0
```

```sql
SELECT activity_id, COUNT(*) AS reqs, COUNT(DISTINCT idempotency_key) AS distinct_keys,
       SUM(status=1) AS success, SUM(status<>1) AS not_success
FROM flash_sale_order_requests
WHERE activity_id IN (13,14,15) GROUP BY activity_id ORDER BY activity_id;
```

```text
13  20  20  20  0
14  10  10  1   9
15  1   1   1   0
```

状态码定义：`0=queued 1=success 2=failed 3=dead`。

### 5.1 失败请求的业务码

```sql
SELECT activity_id, status, COALESCE(last_error_code,-1) AS err, COUNT(*)
FROM flash_sale_order_requests
WHERE activity_id IN (13,14,15) GROUP BY activity_id, status, last_error_code
ORDER BY activity_id, status;
```

```text
13  1  -1     20     -- 全部成功
14  1  -1     1      -- 成功 1 条
14  2  12004  9      -- 一人一单（already_purchased）9 条
15  1  -1     1      -- 幂等后只留 1 条请求
```

---

## 6. Outbox 发布状态（Kafka 模式）

```sql
SELECT activity_id, COUNT(*) AS unpublished FROM flash_sale_order_requests
WHERE activity_id IN (13,14,15) AND published_at IS NULL GROUP BY activity_id;
```

```text
(空结果集)
```

---

## 7. Redis 侧账实一致

```text
GET flashsale:stock:13:11  -> 0     (DB remaining 0)
GET flashsale:stock:14:12  -> 4     (DB remaining 4)
GET flashsale:stock:15:13  -> 4     (DB remaining 4)
EXISTS flashsale:soldout:13:11 -> 0
SCAN flashsale:activity:*  -> activity:12/13/14/15（应用自身预热，非人工写入）
```

---

## 8. Kafka 消费组

```text
bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
  --describe --group surgecart-flashsale-order-worker-v1

TOPIC surgecart.flashsale.order.requested.v1  PARTITION 0
CURRENT-OFFSET 34  LOG-END-OFFSET 34  LAG 0
```

消息数 34 = 本次 31（A20 + B10 + C1）+ 活动 7 探针 1 + T3-02 历史 2。
