# 历史数据说明（Legacy Data）

本文说明仓库历史电商数据（`shopping.sql`）的来源、用途、迁移范围与安全边界。原始 SQL 属于敏感数据，禁止进入 Git、Docker 镜像与 CI 产物。

## 1. 来源

- 文件：`storage/private/legacy/shopping.sql`（已从仓库根归档至此，`.gitignore` 阻止跟踪）。
- 内容：CRMEB 导出的历史生产库全量备份，含 83 张表（含用户、订单、优惠券等 Out of Scope 数据）。
- 迁移只读取其中 5 张旧表：`store_category`、`store_product`、`store_product_attr`、`store_product_attr_value`、`store_product_attr_result`（后者因数据污染弃用）。

## 2. 用途

作为历史数据迁移工具（`my-shop legacy-migrate`）的**源数据**，只导入隔离的 staging 库（`my_shop_staging`），经映射适配后导入 SurgeCart 目标库（`my_shop`）。迁移范围仅限：

- 分类（`store_category` → `categories`）
- 商品 + 图片（`store_product` → `products` + `product_images`）
- SKU（`store_product_attr_value` → `skus`）
- 库存（商品级/SKU 级 → `inventories`）

迁移映射规则与不变量见 `docs/design/legacy-data-migration.md`。

## 3. 迁移范围

| 旧表 | 目标表 | 说明 |
| --- | --- | --- |
| `store_category` | `categories` | 含软删除/上下架状态映射、父级层级 |
| `store_product` | `products` + `product_images` | 金额元→整数分、主图/轮播图拆分 |
| `store_product_attr_value` | `skus` | SKU 权威来源，规格组合 → SKU 名 |
| `store_product`（商品级 stock）+ `store_product_attr_value`（SKU 级 stock） | `inventories` | 单规格用商品级、多规格用 SKU 级，绝不叠加 |

**Out of Scope**：历史用户/密码、订单/订单明细、优惠券、支付/物流/资金/积分业务均不迁移。

## 4. 安全注意

- 原始 `shopping.sql` 含真实账号、地址等敏感信息，**绝不提交仓库、不进 Docker 镜像、不进 CI 产物**。
- 归档路径 `storage/private/legacy/` 已由 `.gitignore` 排除；`storage/` 已由 `.dockerignore` 排除。
- 原始 SQL 头部含 `SET FOREIGN_KEY_CHECKS = 0`，**只允许在隔离 staging 库生效**；目标库连接绝不执行该语句。
- 迁移结果写入目标库 `my_shop`，不与生产库、历史生产库建立任何连接。
- 提交仓库的测试数据均为脱敏/合成数据；日志、报告、测试样本不出现真实私钥、第三方凭据或个人身份信息。

## 5. 归档与校验

归档操作需保证 SHA-256 前后一致：

```bash
# 移动前后分别记录
sha256sum shopping.sql
mv shopping.sql storage/private/legacy/shopping.sql
sha256sum storage/private/legacy/shopping.sql

# 确认不被 Git 跟踪
git check-ignore -v storage/private/legacy/shopping.sql
git log --all --oneline -- shopping.sql storage/private/legacy/shopping.sql  # 应无跟踪记录
```

## 6. 导入与迁移

```bash
# 1) 归档并导入 staging 库（隔离 schema，仅导入历史 SQL）
scripts/import-legacy.sh

# 2) Dry Run：只校验 + 输出将执行变更，不写目标库
go run . legacy-migrate dry-run

# 3) 真实导入（幂等，可重复执行）
go run . legacy-migrate import
```

迁移前需先确保目标库 schema 就绪（`my-shop migrate up`）。
