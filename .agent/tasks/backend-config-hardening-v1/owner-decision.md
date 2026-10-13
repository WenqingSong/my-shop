# Owner Decision

## Review Target

`cee1e6aed2a7527998d662cd69347892ec9fa4c1`

## Core Logic

- CL-001：MySQL 密码缺失（config 空且无 `DATABASE_DEFAULT_PASS`）时，`applyDatabaseConfig` / `buildDSN` / `OpenConnections` 均 fail-fast、非零退出、指认缺失字段，绝不回退内置默认密码 `root`。
- CL-002：所有数据库连接层（应用、migration、legacy-migrate、docker-compose）字符集统一为 `utf8mb4`，四字节 Unicode 字符可正确写入并读回一致；连接字符集为 `utf8`（utf8mb3）时该读写失败。

## Owner Decision

ACCEPTED

## Decision Evidence

Owner 明确表达「ACCEPT」，接受 Cleaner 已 CLEAN 的 snapshot `cee1e6aed2a7527998d662cd69347892ec9fa4c1` 对应的核心机制（CL-001 无凭据默认回退、CL-002 连接字符集 utf8mb4）。

适用范围：本次决定绑定该 CLEAN snapshot；若该 snapshot 之后出现实质变化（生产代码、测试或约束变更），CLEAN 失效，需重新走 Cleaner 复审。
