# Owner Decision

## Review Target

`0414bff4116952b5a7bce1f6c4a53934d7f95a85`

## Core Logic

- CL-001：上传凭证签发核心——key 唯一可控（`upload/{日期}/{hex32}.{ext}`）+ token `scope=bucket:key` 固化文件边界（`mimeLimit`/`fsizeLimit`），防止越界覆盖与任意类型/大小上传。
- CL-002：启动 fail-fast + 最小权限只读可用性检查——`serve` 启动对 AK/SK/bucket/domain 缺失/非法，或指定 bucket 的 `GetBucketInfo`（非 `Buckets()`）真实检查失败即非零退出；`17002` 仅运行期防御性守卫。

## Owner Decision

ACCEPTED

## Decision Evidence

Owner 明确 ACCEPT，接受 Cleaner 已 CLEAN 的 Review Target `0414bff4116952b5a7bce1f6c4a53934d7f95a85`，认可 CL-001、CL-002 的核心机制与验证证据。

Owner 同时对 Deliverer 的真实环境验收提出明确要求（交付约束，随 Handoff 传递给 Deliverer）：

1. `make init` → `make up` 正常启动；
2. 真实七牛 Bucket 连通性检查通过；
3. `make test-storage` 完成真实图片上传、访问与删除；
4. 确认测试通过且无 Secret 泄漏。

未经实际验证的 AC 不得标记 PASS（缺真实凭据时如实 `NOT_VERIFIED`）。
