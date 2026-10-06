# Owner Decision

## Review Target

`df272012781fa29120f5a1d5aec09cbde041d02b`（`feat(product): 商品浏览量计数 V1（view_count 原子自增 + 详情/列表展示）`）

## Core Logic

- CL-001：单条条件 UPDATE 原子自增——`UPDATE products SET view_count = view_count + 1, updated_at = updated_at WHERE id = ? AND status = ?` 合并「可见性校验 + 计数 + 404 判定」为一次原子写；InnoDB 行锁保证并发不丢，`RowsAffected=0` 保证不存在/非上架返回 404 且无计数写入。
- CL-002：`updated_at = updated_at` 保护——规避 `ON UPDATE CURRENT_TIMESTAMP` 把「浏览」误刷成「最近更新」，保持列表按 `updated_at` 排序的既有语义。

## Owner Decision

ACCEPTED

## Decision Evidence

Owner 于 2026-10-06 明确回复 `ACCEPT`，接受 Cleaner 已 CLEAN 的本具体 snapshot `df272012781fa29120f5a1d5aec09cbde041d02b`。

适用范围：product-view-count-v1 全部 Goal / Acceptance Criteria（AC-001 ~ AC-007）。接受对象为 `review.target = df272012781fa29120f5a1d5aec09cbde041d02b` 对应的实现与测试，并非抽象接受任务；该 target 之后出现任何非 review-neutral 实质变化，本次接受即客观失效。
