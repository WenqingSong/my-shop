# Owner Decision

## Review Target

`496dcd060832b17d2f27bf0232e22199e1b32e75`

## Core Logic

- CL-001（作者身份不可伪造 + 越权隔离）：文章作者唯一来源是登录上下文 `Principal.UserID`，请求体不含任何身份字段；非作者改/删他人文章一律返回 404（16001，防枚举）且零写入。
- CL-002（删除事务清理 + 幂等唯一约束）：删除文章在单事务内先清 `article_likes`→`article_favorites`→再条件删本体，任一步失败整体回滚；点赞/收藏靠 `uk_user_article` 唯一约束兜底并发，重复幂等。

## Owner Decision

ACCEPTED

## Decision Evidence

Owner 在 OwnerGate 呈报上述两个核心机制后，明确回复 `ACCEPT`，接受 Cleaner 已 CLEAN 的该 snapshot（`review.target = 496dcd060832b17d2f27bf0232e22199e1b32e75`）。

决策范围：适用于本任务（article-cms-v1）全部实现与测试；不改变 Task 的 Goal/Scope/AC，也不改变已 APPROVED 的 Contract。剩余非阻塞风险（删除与并发点赞的极小 TOCTOU 竞态、硬删不可恢复无审计）已在 Contract Open Risks 记录并属 V1 可接受，不阻塞交付。
