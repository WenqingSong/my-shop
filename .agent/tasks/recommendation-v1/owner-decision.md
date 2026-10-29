# Owner Decision

## Review Target
777212983e7ce43974cba9b0b65ada9d2dc88cc1

## Core Logic
- CL-001：前台 `GET /recommendations/:code` 仅返回「启用推荐位中的可售商品」（`status=1` + `products.status=on_shelf`），杜绝下架/draft 商品泄漏到公开接口；推荐关系不因下架物理删除。
- CL-002：同一推荐位同一商品至多一条关系，DB 唯一约束 `uk_position_product` 兜底并发重复，应用层将 MySQL 1062 翻译为稳定错误码 15005。
- CL-003：调整排序必须全量覆盖已加入商品（无缺漏），缺漏 15006、多余 15004、重复/空 15003，完整列表才在事务内原子写入全部 `sort`。

## Owner Decision
ACCEPTED

## Decision Evidence
Owner 于 2026-10-07 明确回复「ACCEPT」，接受 Cleaner 已 CLEAN 的具体 snapshot（`review.target=777212983e7ce43974cba9b0b65ada9d2dc88cc1`）的上述三项核心机制，即接受该具体实现作为本任务 recommendation-v1 的交付审查对象，而非抽象接受任务本身。

适用范围：本任务 recommendation-v1 全部实现（`review.target` 对应代码）；不改变既有模块公开接口与错误语义。
