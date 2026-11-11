# B1-02-02 DTO → OpenAPI Schema 覆盖清单

| 项目 | 内容 |
|---|---|
| 任务 | B1-02-02 Go DTO Schema 自动生成 |
| 契约基线 | `82243c0fbce614e03c4ff57dcc829b5f68fb08f2` |
| 生成器 | `tools/openapi-gen`（Go AST，确定性输出） |
| 字段事实源 | `api/*/v1/*.go` 的 `json` / `dc` / `v` / `in` / `d` 标签 |
| 生成命令 | `go run ./tools/openapi-gen -repo .` |
| 输出 | `openapi/components/schemas.yaml`、`openapi/components/error-codes.yaml`、`openapi/surgecart.openapi.yaml` |

## 1. 覆盖统计

| 指标 | 数值 |
|---|---|
| DTO 结构体总数 | 391 |
| Request 结构体 | 170 |
| Response 结构体 | 169 |
| 业务实体结构体 | 52 |
| 已登记 API | 170 |
| OperationId 唯一性 | 通过（生成期强制校验） |
| 无法确认的字段 | 0（遇到未登记类型直接失败，不猜测） |

## 2. 命名规则

- `<Domain><Name>Req`  → `<Prefix><Name>Request`（如 `order.CreateReq` → `OrderCreateRequest`）
- `<Domain><Name>Res`  → `<Prefix><Name>Response`（如 `order.ListRes` → `OrderListResponse`）
- 业务实体使用显式登记表（`entitySchemaNames`），禁止使用 Go 包路径名作为 Schema 名
- 未登记的类型会让生成器直接失败并提示补登记，避免静默猜测
- Schema 名冲突由生成期检测并失败

## 3. 类型映射规则

| Go 类型 | OpenAPI 表达 | 说明 |
|---|---|---|
| `string` | `type: string` | |
| `bool` | `type: boolean` | |
| `int`/`int64`/... | `type: integer, format: int64` + `x-go-type` | 统一 int64，避免 32/64 平台差异 |
| `float64` | `type: number, format: double` | 仅 `avg_rating` 使用 |
| `json.Number` | `oneOf: [integer(int64), string(数字字符串)]` | GoFrame 绑定接受两种输入 |
| `*gtime.Time`（响应） | `type: [string, "null"]` + pattern + `x-format: gtime-local` | 实际格式 `YYYY-MM-DD HH:MM:SS`，无时区偏移 |
| `*gtime.Time`（请求） | `type: [string, "null"]` + `x-format: gtime-lenient` | 服务端宽松解析 |
| `time.Time` | `type: [string, "null"], format: date-time` | 当前 DTO 未使用 |
| `*T` | `oneOf: [$ref, {type: null}]` 或 `type: [x, "null"]` | 可空语义 |
| `[]T` | `type: array, items: <T>` | 元素指针同样可空 |
| 匿名嵌入 `T` | `allOf: [$ref T]` | 字段提升 |

`required` 规则：请求 DTO 仅 `v:"required"` 字段必填；响应 DTO 的非 `omitempty` 字段一律视为必出现（Go 会输出 null，键仍存在）。

## 4. 全量 Schema 清单

| Schema | Go 类型 | 类型 | 字段数 | 金额字段 | 枚举字段 | 时间字段 | 可空字段 |
|---|---|---|---|---|---|---|---|
| `Address` | `address.Address` | Entity | 11 | - | - | `created_at`<br>`updated_at` | `region_id`<br>`created_at`<br>`updated_at` |
| `AddressCreateRequest` | `address.CreateReq` | Request | 8 | - | - | - | `region_id`<br>`is_default` |
| `AddressCreateResponse` | `address.CreateRes` | Response | 1 | - | - | - | - |
| `AddressListRequest` | `address.ListReq` | Request | 0 | - | - | - | - |
| `AddressListResponse` | `address.ListRes` | Response | 1 | - | - | - | - |
| `AddressDetailRequest` | `address.DetailReq` | Request | 1 | - | - | - | - |
| `AddressDetailResponse` | `address.DetailRes` | Response | 1 | - | - | - | - |
| `AddressUpdateRequest` | `address.UpdateReq` | Request | 9 | - | - | - | `recipient_name`<br>`phone`<br>`province`<br>`city`<br>`district`<br>`region_id`<br>`detail`<br>`is_default` |
| `AddressUpdateResponse` | `address.UpdateRes` | Response | 1 | - | - | - | - |
| `AddressDeleteRequest` | `address.DeleteReq` | Request | 1 | - | - | - | - |
| `AddressDeleteResponse` | `address.DeleteRes` | Response | 0 | - | - | - | - |
| `AdminLoginRequest` | `admin.LoginReq` | Request | 2 | - | - | - | - |
| `AdminLoginResponse` | `admin.LoginRes` | Response | 3 | - | - | - | - |
| `AdminMeRequest` | `admin.MeReq` | Request | 0 | - | - | - | - |
| `AdminMeResponse` | `admin.MeRes` | Response | 4 | - | - | - | - |
| `AdminLogoutRequest` | `admin.LogoutReq` | Request | 0 | - | - | - | - |
| `AdminLogoutResponse` | `admin.LogoutRes` | Response | 0 | - | - | - | - |
| `AdminCreateAdminRequest` | `admin.CreateAdminReq` | Request | 2 | - | - | - | - |
| `AdminCreateAdminResponse` | `admin.CreateAdminRes` | Response | 2 | - | - | - | - |
| `AdminUpdateAdminStatusRequest` | `admin.UpdateAdminStatusReq` | Request | 2 | - | `status` | - | - |
| `AdminUpdateAdminStatusResponse` | `admin.UpdateAdminStatusRes` | Response | 0 | - | - | - | - |
| `AdminDeleteAdminRequest` | `admin.DeleteAdminReq` | Request | 1 | - | - | - | - |
| `AdminDeleteAdminResponse` | `admin.DeleteAdminRes` | Response | 0 | - | - | - | - |
| `AdminAssignAdminRoleRequest` | `admin.AssignAdminRoleReq` | Request | 2 | - | - | - | - |
| `AdminAssignAdminRoleResponse` | `admin.AssignAdminRoleRes` | Response | 0 | - | - | - | - |
| `AdminRemoveAdminRoleRequest` | `admin.RemoveAdminRoleReq` | Request | 2 | - | - | - | - |
| `AdminRemoveAdminRoleResponse` | `admin.RemoveAdminRoleRes` | Response | 0 | - | - | - | - |
| `AdminRole` | `admin.Role` | Entity | 3 | - | - | - | - |
| `AdminCreateRoleRequest` | `admin.CreateRoleReq` | Request | 2 | - | - | - | - |
| `AdminCreateRoleResponse` | `admin.CreateRoleRes` | Response | 1 | - | - | - | - |
| `AdminListRoleRequest` | `admin.ListRoleReq` | Request | 0 | - | - | - | - |
| `AdminListRoleResponse` | `admin.ListRoleRes` | Response | 1 | - | - | - | - |
| `AdminUpdateRoleRequest` | `admin.UpdateRoleReq` | Request | 3 | - | - | - | `name`<br>`description` |
| `AdminUpdateRoleResponse` | `admin.UpdateRoleRes` | Response | 1 | - | - | - | - |
| `AdminDeleteRoleRequest` | `admin.DeleteRoleReq` | Request | 1 | - | - | - | - |
| `AdminDeleteRoleResponse` | `admin.DeleteRoleRes` | Response | 0 | - | - | - | - |
| `AdminAssignRolePermissionRequest` | `admin.AssignRolePermissionReq` | Request | 2 | - | - | - | - |
| `AdminAssignRolePermissionResponse` | `admin.AssignRolePermissionRes` | Response | 0 | - | - | - | - |
| `AdminRemoveRolePermissionRequest` | `admin.RemoveRolePermissionReq` | Request | 2 | - | - | - | - |
| `AdminRemoveRolePermissionResponse` | `admin.RemoveRolePermissionRes` | Response | 0 | - | - | - | - |
| `Permission` | `admin.Permission` | Entity | 4 | - | - | - | - |
| `AdminCreatePermissionRequest` | `admin.CreatePermissionReq` | Request | 3 | - | - | - | - |
| `AdminCreatePermissionResponse` | `admin.CreatePermissionRes` | Response | 1 | - | - | - | - |
| `AdminListPermissionRequest` | `admin.ListPermissionReq` | Request | 0 | - | - | - | - |
| `AdminListPermissionResponse` | `admin.ListPermissionRes` | Response | 1 | - | - | - | - |
| `AdminUpdatePermissionRequest` | `admin.UpdatePermissionReq` | Request | 4 | - | - | - | `code`<br>`name`<br>`description` |
| `AdminUpdatePermissionResponse` | `admin.UpdatePermissionRes` | Response | 1 | - | - | - | - |
| `AdminDeletePermissionRequest` | `admin.DeletePermissionReq` | Request | 1 | - | - | - | - |
| `AdminDeletePermissionResponse` | `admin.DeletePermissionRes` | Response | 0 | - | - | - | - |
| `Article` | `article.Article` | Entity | 7 | - | - | `created_at`<br>`updated_at` | `created_at`<br>`updated_at` |
| `ArticleItem` | `article.ArticleItem` | Entity | 6 | - | - | `created_at`<br>`updated_at` | `created_at`<br>`updated_at` |
| `ArticleListRequest` | `article.ListReq` | Request | 2 | - | - | - | - |
| `ArticleListResponse` | `article.ListRes` | Response | 4 | - | - | - | - |
| `ArticleDetailRequest` | `article.DetailReq` | Request | 1 | - | - | - | - |
| `ArticleDetailResponse` | `article.DetailRes` | Response | 1 | - | - | - | - |
| `ArticleCreateRequest` | `article.CreateReq` | Request | 2 | - | - | - | - |
| `ArticleCreateResponse` | `article.CreateRes` | Response | 1 | - | - | - | - |
| `ArticleUpdateRequest` | `article.UpdateReq` | Request | 3 | - | - | - | - |
| `ArticleUpdateResponse` | `article.UpdateRes` | Response | 1 | - | - | - | - |
| `ArticleDeleteRequest` | `article.DeleteReq` | Request | 1 | - | - | - | - |
| `ArticleDeleteResponse` | `article.DeleteRes` | Response | 0 | - | - | - | - |
| `ArticleMyListRequest` | `article.MyListReq` | Request | 2 | - | - | - | - |
| `ArticleMyListResponse` | `article.MyListRes` | Response | 4 | - | - | - | - |
| `ArticleLikeCountRequest` | `article.LikeCountReq` | Request | 1 | - | - | - | - |
| `ArticleLikeCountResponse` | `article.LikeCountRes` | Response | 1 | - | - | - | - |
| `ArticleLikeRequest` | `article.LikeReq` | Request | 1 | - | - | - | - |
| `ArticleLikeResponse` | `article.LikeRes` | Response | 1 | - | - | - | - |
| `ArticleUnlikeRequest` | `article.UnlikeReq` | Request | 1 | - | - | - | - |
| `ArticleUnlikeResponse` | `article.UnlikeRes` | Response | 0 | - | - | - | - |
| `ArticleLikeCheckRequest` | `article.LikeCheckReq` | Request | 1 | - | - | - | - |
| `ArticleLikeCheckResponse` | `article.LikeCheckRes` | Response | 1 | - | - | - | - |
| `ArticleFavoriteRequest` | `article.FavoriteReq` | Request | 1 | - | - | - | - |
| `ArticleFavoriteResponse` | `article.FavoriteRes` | Response | 1 | - | - | - | - |
| `ArticleUnfavoriteRequest` | `article.UnfavoriteReq` | Request | 1 | - | - | - | - |
| `ArticleUnfavoriteResponse` | `article.UnfavoriteRes` | Response | 0 | - | - | - | - |
| `ArticleFavoriteCheckRequest` | `article.FavoriteCheckReq` | Request | 1 | - | - | - | - |
| `ArticleFavoriteCheckResponse` | `article.FavoriteCheckRes` | Response | 1 | - | - | - | - |
| `ArticleMyFavoritesRequest` | `article.MyFavoritesReq` | Request | 2 | - | - | - | - |
| `ArticleMyFavoritesResponse` | `article.MyFavoritesRes` | Response | 4 | - | - | - | - |
| `BannerItem` | `banner.BannerItem` | Entity | 5 | - | - | - | - |
| `Banner` | `banner.Banner` | Entity | 8 | - | `status` | `created_at`<br>`updated_at` | `created_at`<br>`updated_at` |
| `BannerListRequest` | `banner.ListReq` | Request | 0 | - | - | - | - |
| `BannerListResponse` | `banner.ListRes` | Response | 1 | - | - | - | - |
| `BannerAdminListRequest` | `banner.AdminListReq` | Request | 0 | - | - | - | - |
| `BannerAdminListResponse` | `banner.AdminListRes` | Response | 1 | - | - | - | - |
| `BannerAdminDetailRequest` | `banner.AdminDetailReq` | Request | 1 | - | - | - | - |
| `BannerAdminDetailResponse` | `banner.AdminDetailRes` | Response | 1 | - | - | - | - |
| `BannerCreateRequest` | `banner.CreateReq` | Request | 5 | - | `status` | - | `status` |
| `BannerCreateResponse` | `banner.CreateRes` | Response | 1 | - | - | - | - |
| `BannerUpdateRequest` | `banner.UpdateReq` | Request | 6 | - | `status` | - | `title`<br>`image_url`<br>`link_url`<br>`sort`<br>`status` |
| `BannerUpdateResponse` | `banner.UpdateRes` | Response | 1 | - | - | - | - |
| `BannerDeleteRequest` | `banner.DeleteReq` | Request | 1 | - | - | - | - |
| `BannerDeleteResponse` | `banner.DeleteRes` | Response | 0 | - | - | - | - |
| `CartItem` | `cart.CartItem` | Entity | 17 | `price_snapshot` | `unavailable_reason` | `created_at`<br>`updated_at` | `product_id`<br>`current_price`<br>`stock`<br>`created_at`<br>`updated_at` |
| `CartListRequest` | `cart.ListReq` | Request | 0 | - | - | - | - |
| `CartListResponse` | `cart.ListRes` | Response | 1 | - | - | - | - |
| `CartAddRequest` | `cart.AddReq` | Request | 2 | - | - | - | `quantity` |
| `CartAddResponse` | `cart.AddRes` | Response | 1 | - | - | - | - |
| `CartUpdateQuantityRequest` | `cart.UpdateQuantityReq` | Request | 2 | - | - | - | - |
| `CartUpdateQuantityResponse` | `cart.UpdateQuantityRes` | Response | 1 | - | - | - | - |
| `CartUpdateSelectedRequest` | `cart.UpdateSelectedReq` | Request | 2 | - | - | - | - |
| `CartUpdateSelectedResponse` | `cart.UpdateSelectedRes` | Response | 1 | - | - | - | - |
| `CartDeleteRequest` | `cart.DeleteReq` | Request | 1 | - | - | - | - |
| `CartDeleteResponse` | `cart.DeleteRes` | Response | 0 | - | - | - | - |
| `Category` | `categories.Category` | Entity | 8 | - | `status` | `created_at`<br>`updated_at` | `created_at`<br>`updated_at` |
| `CategoryListRequest` | `categories.ListReq` | Request | 0 | - | - | - | - |
| `CategoryListResponse` | `categories.ListRes` | Response | 1 | - | - | - | - |
| `CategoryDetailRequest` | `categories.DetailReq` | Request | 1 | - | - | - | - |
| `CategoryDetailResponse` | `categories.DetailRes` | Response | 1 | - | - | - | - |
| `CategoryCreateRequest` | `categories.CreateReq` | Request | 4 | - | `status` | - | `status` |
| `CategoryCreateResponse` | `categories.CreateRes` | Response | 1 | - | - | - | - |
| `CategoryUpdateRequest` | `categories.UpdateReq` | Request | 5 | - | `status` | - | `parent_id`<br>`name`<br>`sort`<br>`status` |
| `CategoryUpdateResponse` | `categories.UpdateRes` | Response | 1 | - | - | - | - |
| `CategoryDeleteRequest` | `categories.DeleteReq` | Request | 1 | - | - | - | - |
| `CategoryDeleteResponse` | `categories.DeleteRes` | Response | 0 | - | - | - | - |
| `CouponTemplate` | `coupon.Template` | Entity | 13 | `threshold_amount`<br>`discount_amount` | `status` | `claim_start_time`<br>`claim_end_time`<br>`use_start_time`<br>`use_end_time`<br>`created_at`<br>`updated_at` | `claim_start_time`<br>`claim_end_time`<br>`use_start_time`<br>`use_end_time`<br>`created_at`<br>`updated_at` |
| `UserCoupon` | `coupon.UserCoupon` | Entity | 12 | `threshold_amount`<br>`discount_amount` | `status` | `received_at`<br>`locked_at`<br>`used_at`<br>`use_start_time`<br>`use_end_time` | `order_id`<br>`received_at`<br>`locked_at`<br>`used_at`<br>`use_start_time`<br>`use_end_time` |
| `CouponAdminCreateRequest` | `coupon.AdminCreateReq` | Request | 8 | `threshold_amount`<br>`discount_amount` | - | `claim_start_time`<br>`claim_end_time`<br>`use_start_time`<br>`use_end_time` | `claim_start_time`<br>`claim_end_time`<br>`use_start_time`<br>`use_end_time` |
| `CouponAdminCreateResponse` | `coupon.AdminCreateRes` | Response | 1 | - | - | - | - |
| `CouponAdminListRequest` | `coupon.AdminListReq` | Request | 0 | - | - | - | - |
| `CouponAdminListResponse` | `coupon.AdminListRes` | Response | 1 | - | - | - | - |
| `CouponAdminStopRequest` | `coupon.AdminStopReq` | Request | 1 | - | - | - | - |
| `CouponAdminStopResponse` | `coupon.AdminStopRes` | Response | 1 | - | - | - | - |
| `CouponClaimRequest` | `coupon.ClaimReq` | Request | 1 | - | - | - | - |
| `CouponClaimResponse` | `coupon.ClaimRes` | Response | 1 | - | - | - | - |
| `CouponListMineRequest` | `coupon.ListMineReq` | Request | 0 | - | - | - | - |
| `CouponListMineResponse` | `coupon.ListMineRes` | Response | 1 | - | - | - | - |
| `DashboardOverviewRequest` | `dashboard.OverviewReq` | Request | 0 | - | - | - | - |
| `DashboardOverviewResponse` | `dashboard.OverviewRes` | Response | 6 | `today_order_amount` | - | - | - |
| `DashboardTrendRequest` | `dashboard.TrendReq` | Request | 0 | - | - | - | - |
| `DashboardTrendItem` | `dashboard.TrendItem` | Entity | 2 | - | - | - | - |
| `DashboardTrendResponse` | `dashboard.TrendRes` | Response | 1 | - | - | - | - |
| `DashboardStatusRequest` | `dashboard.StatusReq` | Request | 2 | - | - | - | - |
| `DashboardStatusItem` | `dashboard.StatusItem` | Entity | 2 | - | `status` | - | - |
| `DashboardStatusResponse` | `dashboard.StatusRes` | Response | 2 | - | - | - | - |
| `DashboardTopRequest` | `dashboard.TopReq` | Request | 1 | - | - | - | - |
| `DashboardTopItem` | `dashboard.TopItem` | Entity | 4 | - | - | - | - |
| `DashboardTopResponse` | `dashboard.TopRes` | Response | 1 | - | - | - | - |
| `DashboardFlashSalesRequest` | `dashboard.FlashSalesReq` | Request | 0 | - | - | - | - |
| `DashboardActivityStatus` | `dashboard.ActivityStatus` | Entity | 2 | - | - | - | - |
| `DashboardRequestStatus` | `dashboard.RequestStatus` | Entity | 4 | - | - | - | - |
| `DashboardFlashSalesResponse` | `dashboard.FlashSalesRes` | Response | 5 | - | - | - | `activity_status`<br>`request_status` |
| `DistributionReferral` | `distribution.Referral` | Entity | 3 | - | - | `created_at` | `created_at` |
| `DistributionCommission` | `distribution.Commission` | Entity | 15 | `base_amount`<br>`commission_amount`<br>`reversed_amount` | `status` | `settled_at`<br>`reversed_at`<br>`created_at`<br>`updated_at` | `settled_at`<br>`reversed_at`<br>`refund_id`<br>`created_at`<br>`updated_at` |
| `DistributionCommissionSummary` | `distribution.CommissionSummary` | Entity | 6 | `pending_amount`<br>`settled_amount`<br>`reversed_amount` | - | - | - |
| `DistributionSubordinate` | `distribution.Subordinate` | Entity | 3 | - | - | `bound_at` | `bound_at` |
| `DistributionDistributor` | `distribution.Distributor` | Entity | 6 | `commission_amount`<br>`reversed_amount` | - | `first_bound_at` | `first_bound_at` |
| `DistributionCommissionStatsItem` | `distribution.CommissionStatsItem` | Entity | 4 | `amount`<br>`reversed_amount` | `status` | - | - |
| `DistributionBindReferralRequest` | `distribution.BindReferralReq` | Request | 1 | - | - | - | - |
| `DistributionBindReferralResponse` | `distribution.BindReferralRes` | Response | 1 | - | - | - | - |
| `DistributionMyInfoRequest` | `distribution.MyInfoReq` | Request | 0 | - | - | - | - |
| `DistributionMyInfoResponse` | `distribution.MyInfoRes` | Response | 4 | - | - | - | `referrer` |
| `DistributionListSubordinatesRequest` | `distribution.ListSubordinatesReq` | Request | 2 | - | - | - | `page`<br>`size` |
| `DistributionListSubordinatesResponse` | `distribution.ListSubordinatesRes` | Response | 4 | - | - | - | - |
| `DistributionListCommissionsRequest` | `distribution.ListCommissionsReq` | Request | 3 | - | - | - | `page`<br>`size` |
| `DistributionListCommissionsResponse` | `distribution.ListCommissionsRes` | Response | 4 | - | - | - | - |
| `DistributionAdminListReferralsRequest` | `distribution.AdminListReferralsReq` | Request | 4 | - | - | - | `page`<br>`size`<br>`user_id`<br>`referrer_user_id` |
| `DistributionAdminListReferralsResponse` | `distribution.AdminListReferralsRes` | Response | 4 | - | - | - | - |
| `DistributionAdminListDistributorsRequest` | `distribution.AdminListDistributorsReq` | Request | 2 | - | - | - | `page`<br>`size` |
| `DistributionAdminListDistributorsResponse` | `distribution.AdminListDistributorsRes` | Response | 4 | - | - | - | - |
| `DistributionAdminListCommissionsRequest` | `distribution.AdminListCommissionsReq` | Request | 6 | - | - | - | `page`<br>`size`<br>`order_id`<br>`buyer_user_id`<br>`beneficiary_user_id` |
| `DistributionAdminListCommissionsResponse` | `distribution.AdminListCommissionsRes` | Response | 4 | - | - | - | - |
| `DistributionAdminCommissionStatsRequest` | `distribution.AdminCommissionStatsReq` | Request | 0 | - | - | - | - |
| `DistributionAdminCommissionStatsResponse` | `distribution.AdminCommissionStatsRes` | Response | 3 | `total_amount` | - | - | - |
| `FavoriteItem` | `favorite.FavoriteItem` | Entity | 8 | - | `unavailable_reason` | `created_at` | `product_price`<br>`created_at` |
| `FavoriteAddRequest` | `favorite.AddReq` | Request | 1 | - | - | - | - |
| `FavoriteAddResponse` | `favorite.AddRes` | Response | 1 | - | - | - | - |
| `FavoriteRemoveRequest` | `favorite.RemoveReq` | Request | 1 | - | - | - | - |
| `FavoriteRemoveResponse` | `favorite.RemoveRes` | Response | 0 | - | - | - | - |
| `FavoriteListRequest` | `favorite.ListReq` | Request | 2 | - | - | - | - |
| `FavoriteListResponse` | `favorite.ListRes` | Response | 4 | - | - | - | - |
| `FavoriteCheckRequest` | `favorite.CheckReq` | Request | 1 | - | - | - | - |
| `FavoriteCheckResponse` | `favorite.CheckRes` | Response | 1 | - | - | - | - |
| `FlashSaleActivitySku` | `flashsale.ActivitySku` | Entity | 5 | `flash_price` | - | - | - |
| `FlashSaleActivity` | `flashsale.Activity` | Entity | 8 | - | `status` | `start_time`<br>`end_time`<br>`created_at`<br>`updated_at` | `start_time`<br>`end_time`<br>`created_at`<br>`updated_at` |
| `FlashSaleOrder` | `flashsale.FlashOrder` | Entity | 14 | `flash_price` | - | `created_at`<br>`updated_at` | `created_at`<br>`updated_at` |
| `FlashSaleActivitySkuInput` | `flashsale.ActivitySkuInput` | Entity | 3 | - | - | - | - |
| `FlashSaleCreateRequest` | `flashsale.CreateReq` | Request | 4 | - | - | `start_time`<br>`end_time` | `start_time`<br>`end_time` |
| `FlashSaleCreateResponse` | `flashsale.CreateRes` | Response | 1 | - | - | - | - |
| `FlashSaleUpdateRequest` | `flashsale.UpdateReq` | Request | 6 | - | - | `start_time`<br>`end_time` | `name`<br>`status`<br>`start_time`<br>`end_time` |
| `FlashSaleUpdateResponse` | `flashsale.UpdateRes` | Response | 1 | - | - | - | - |
| `FlashSaleCreateOrderRequest` | `flashsale.CreateOrderReq` | Request | 3 | - | - | - | - |
| `FlashSaleCreateOrderResponse` | `flashsale.CreateOrderRes` | Response | 4 | - | `status` | - | - |
| `FlashSaleGetOrderResultRequest` | `flashsale.GetOrderResultReq` | Request | 2 | - | - | - | - |
| `FlashSaleGetOrderResultResponse` | `flashsale.GetOrderResultRes` | Response | 3 | - | `status` | - | `order` |
| `FlashSaleRepairRequestRequest` | `flashsale.RepairRequestReq` | Request | 3 | - | `target_status` | - | - |
| `FlashSaleRepairRequestResponse` | `flashsale.RepairRequestRes` | Response | 2 | - | `status` | - | - |
| `FlashSaleListRequestAuditsRequest` | `flashsale.ListRequestAuditsReq` | Request | 1 | - | - | - | - |
| `FlashSaleRequestAudit` | `flashsale.RequestAudit` | Entity | 9 | - | `before_status`<br>`after_status` | `created_at` | `created_at` |
| `FlashSaleListRequestAuditsResponse` | `flashsale.ListRequestAuditsRes` | Response | 1 | - | - | - | - |
| `GroupBuyActivity` | `groupbuy.Activity` | Entity | 17 | `group_price`<br>`sku_price` | - | `start_time`<br>`end_time`<br>`created_at`<br>`updated_at` | `start_time`<br>`end_time`<br>`created_at`<br>`updated_at` |
| `GroupBuyTeam` | `groupbuy.Team` | Entity | 11 | `group_price` | - | `expire_at`<br>`closed_at`<br>`created_at`<br>`updated_at` | `expire_at`<br>`closed_at`<br>`created_at`<br>`updated_at` |
| `GroupBuyParticipant` | `groupbuy.Participant` | Entity | 6 | - | - | `created_at` | `created_at` |
| `GroupBuyTeamDetail` | `groupbuy.TeamDetail` | Entity | 10 | - | - | - | `my_participant`<br>`my_order_id` |
| `GroupBuyMyTeamItem` | `groupbuy.MyTeamItem` | Entity | 7 | - | - | `joined_at` | `order_id`<br>`joined_at` |
| `GroupBuyListActivitiesRequest` | `groupbuy.ListActivitiesReq` | Request | 2 | - | - | - | `page`<br>`size` |
| `GroupBuyListActivitiesResponse` | `groupbuy.ListActivitiesRes` | Response | 4 | - | - | - | - |
| `GroupBuyActivityDetailRequest` | `groupbuy.ActivityDetailReq` | Request | 1 | - | - | - | - |
| `GroupBuyActivityDetailResponse` | `groupbuy.ActivityDetailRes` | Response | 1 | - | - | - | - |
| `GroupBuyListTeamsRequest` | `groupbuy.ListTeamsReq` | Request | 3 | - | - | - | `page`<br>`size` |
| `GroupBuyListTeamsResponse` | `groupbuy.ListTeamsRes` | Response | 4 | - | - | - | - |
| `GroupBuyCreateTeamRequest` | `groupbuy.CreateTeamReq` | Request | 3 | - | - | - | - |
| `GroupBuyCreateTeamResponse` | `groupbuy.CreateTeamRes` | Response | 1 | - | - | - | - |
| `GroupBuyJoinTeamRequest` | `groupbuy.JoinTeamReq` | Request | 3 | - | - | - | - |
| `GroupBuyJoinTeamResponse` | `groupbuy.JoinTeamRes` | Response | 1 | - | - | - | - |
| `GroupBuyTeamDetailRequest` | `groupbuy.TeamDetailReq` | Request | 1 | - | - | - | - |
| `GroupBuyTeamDetailResponse` | `groupbuy.TeamDetailRes` | Response | 1 | - | - | - | - |
| `GroupBuyMyListRequest` | `groupbuy.MyListReq` | Request | 3 | - | - | - | `page`<br>`size` |
| `GroupBuyMyListResponse` | `groupbuy.MyListRes` | Response | 4 | - | - | - | - |
| `GroupBuyAdminCreateRequest` | `groupbuy.AdminCreateReq` | Request | 8 | `group_price` | - | `start_time`<br>`end_time` | `start_time`<br>`end_time` |
| `GroupBuyAdminCreateResponse` | `groupbuy.AdminCreateRes` | Response | 1 | - | - | - | - |
| `GroupBuyAdminUpdateRequest` | `groupbuy.AdminUpdateReq` | Request | 7 | `group_price` | - | `start_time`<br>`end_time` | `start_time`<br>`end_time` |
| `GroupBuyAdminUpdateResponse` | `groupbuy.AdminUpdateRes` | Response | 1 | - | - | - | - |
| `GroupBuyAdminStatusRequest` | `groupbuy.AdminStatusReq` | Request | 2 | - | - | - | - |
| `GroupBuyAdminStatusResponse` | `groupbuy.AdminStatusRes` | Response | 1 | - | - | - | - |
| `GroupBuyAdminListRequest` | `groupbuy.AdminListReq` | Request | 5 | - | - | - | `page`<br>`size`<br>`product_id`<br>`sku_id` |
| `GroupBuyAdminListResponse` | `groupbuy.AdminListRes` | Response | 4 | - | - | - | - |
| `GroupBuyAdminDetailRequest` | `groupbuy.AdminDetailReq` | Request | 1 | - | - | - | - |
| `GroupBuyAdminDetailResponse` | `groupbuy.AdminDetailRes` | Response | 1 | - | - | - | - |
| `GroupBuyAdminListTeamsRequest` | `groupbuy.AdminListTeamsReq` | Request | 5 | - | - | - | `page`<br>`size`<br>`activity_id`<br>`leader_user_id` |
| `GroupBuyAdminListTeamsResponse` | `groupbuy.AdminListTeamsRes` | Response | 4 | - | - | - | - |
| `GroupBuyAdminTeamDetailRequest` | `groupbuy.AdminTeamDetailReq` | Request | 1 | - | - | - | - |
| `GroupBuyAdminTeamDetailResponse` | `groupbuy.AdminTeamDetailRes` | Response | 1 | - | - | - | - |
| `GroupBuyAdminCloseTeamRequest` | `groupbuy.AdminCloseTeamReq` | Request | 1 | - | - | - | - |
| `GroupBuyAdminCloseTeamResponse` | `groupbuy.AdminCloseTeamRes` | Response | 1 | - | - | - | - |
| `HealthLivenessRequest` | `health.LivenessReq` | Request | 0 | - | - | - | - |
| `HealthLivenessResponse` | `health.LivenessRes` | Response | 2 | - | - | - | - |
| `HealthReadinessRequest` | `health.ReadinessReq` | Request | 0 | - | - | - | - |
| `HealthReadinessResponse` | `health.ReadinessRes` | Response | 1 | - | - | - | - |
| `UserRegisterRequest` | `iam.RegisterReq` | Request | 2 | - | - | - | - |
| `UserRegisterResponse` | `iam.RegisterRes` | Response | 2 | - | - | - | - |
| `UserLoginRequest` | `iam.LoginReq` | Request | 2 | - | - | - | - |
| `UserLoginResponse` | `iam.LoginRes` | Response | 4 | - | - | - | - |
| `UserRefreshRequest` | `iam.RefreshReq` | Request | 1 | - | - | - | - |
| `UserRefreshResponse` | `iam.RefreshRes` | Response | 4 | - | - | - | - |
| `UserMeRequest` | `iam.MeReq` | Request | 0 | - | - | - | - |
| `UserMeResponse` | `iam.MeRes` | Response | 2 | - | - | - | - |
| `UserLogoutRequest` | `iam.LogoutReq` | Request | 0 | - | - | - | - |
| `UserLogoutResponse` | `iam.LogoutRes` | Response | 0 | - | - | - | - |
| `UserSession` | `iam.Session` | Entity | 5 | - | - | - | - |
| `UserListSessionsRequest` | `iam.ListSessionsReq` | Request | 0 | - | - | - | - |
| `UserListSessionsResponse` | `iam.ListSessionsRes` | Response | 1 | - | - | - | - |
| `UserRevokeSessionRequest` | `iam.RevokeSessionReq` | Request | 1 | - | - | - | - |
| `UserRevokeSessionResponse` | `iam.RevokeSessionRes` | Response | 0 | - | - | - | - |
| `UserRevokeOthersRequest` | `iam.RevokeOthersReq` | Request | 0 | - | - | - | - |
| `UserRevokeOthersResponse` | `iam.RevokeOthersRes` | Response | 0 | - | - | - | - |
| `UserRevokeAllRequest` | `iam.RevokeAllReq` | Request | 0 | - | - | - | - |
| `UserRevokeAllResponse` | `iam.RevokeAllRes` | Response | 0 | - | - | - | - |
| `UserGetUserStatusRequest` | `iam.GetUserStatusReq` | Request | 1 | - | - | - | - |
| `UserGetUserStatusResponse` | `iam.GetUserStatusRes` | Response | 2 | - | `status` | - | - |
| `UserUpdateUserStatusRequest` | `iam.UpdateUserStatusReq` | Request | 3 | - | `status` | - | - |
| `UserUpdateUserStatusResponse` | `iam.UpdateUserStatusRes` | Response | 0 | - | - | - | - |
| `Inventory` | `inventory.Inventory` | Entity | 4 | - | - | `created_at`<br>`updated_at` | `created_at`<br>`updated_at` |
| `InventoryLog` | `inventory.Log` | Entity | 9 | - | `change_type` | `created_at` | `operator_admin_id`<br>`created_at` |
| `InventoryGetRequest` | `inventory.GetReq` | Request | 1 | - | - | - | - |
| `InventoryGetResponse` | `inventory.GetRes` | Response | 1 | - | - | - | - |
| `InventoryIncreaseRequest` | `inventory.IncreaseReq` | Request | 2 | - | - | - | - |
| `InventoryIncreaseResponse` | `inventory.IncreaseRes` | Response | 1 | - | - | - | - |
| `InventoryDeductRequest` | `inventory.DeductReq` | Request | 2 | - | - | - | - |
| `InventoryDeductResponse` | `inventory.DeductRes` | Response | 1 | - | - | - | - |
| `InventoryListLogsRequest` | `inventory.ListLogsReq` | Request | 3 | - | - | - | - |
| `InventoryListLogsResponse` | `inventory.ListLogsRes` | Response | 4 | - | - | - | - |
| `LikeLikeRequest` | `like.LikeReq` | Request | 1 | - | - | - | - |
| `LikeLikeResponse` | `like.LikeRes` | Response | 1 | - | - | - | - |
| `LikeCancelRequest` | `like.CancelReq` | Request | 1 | - | - | - | - |
| `LikeCancelResponse` | `like.CancelRes` | Response | 0 | - | - | - | - |
| `LikeCheckRequest` | `like.CheckReq` | Request | 1 | - | - | - | - |
| `LikeCheckResponse` | `like.CheckRes` | Response | 1 | - | - | - | - |
| `LikeCountRequest` | `like.CountReq` | Request | 1 | - | - | - | - |
| `LikeCountResponse` | `like.CountRes` | Response | 1 | - | - | - | - |
| `Order` | `order.Order` | Entity | 27 | `total_amount`<br>`original_amount`<br>`discount_amount` | `status`<br>`cancel_reason` | `expire_at`<br>`paid_at`<br>`shipped_at`<br>`received_at`<br>`completed_at`<br>`cancelled_at`<br>`refunded_at`<br>`created_at`<br>`updated_at` | `user_coupon_id`<br>`address_id`<br>`expire_at`<br>`cancel_reason`<br>`paid_at`<br>`shipped_at`<br>`received_at`<br>`completed_at`<br>`cancelled_at`<br>`refunded_at`<br>`created_at`<br>`updated_at` |
| `OrderItem` | `order.OrderItem` | Entity | 8 | `price` | - | - | - |
| `OrderCreateRequest` | `order.CreateReq` | Request | 6 | - | `source` | - | - |
| `OrderCreateResponse` | `order.CreateRes` | Response | 1 | - | - | - | - |
| `OrderListRequest` | `order.ListReq` | Request | 0 | - | - | - | - |
| `OrderListResponse` | `order.ListRes` | Response | 1 | - | - | - | - |
| `OrderDetailRequest` | `order.DetailReq` | Request | 1 | - | - | - | - |
| `OrderDetailResponse` | `order.DetailRes` | Response | 1 | - | - | - | - |
| `OrderPayRequest` | `order.PayReq` | Request | 1 | - | - | - | - |
| `OrderPayResponse` | `order.PayRes` | Response | 1 | - | - | - | - |
| `OrderCancelRequest` | `order.CancelReq` | Request | 1 | - | - | - | - |
| `OrderCancelResponse` | `order.CancelRes` | Response | 1 | - | - | - | - |
| `OrderReceiveRequest` | `order.ReceiveReq` | Request | 1 | - | - | - | - |
| `OrderReceiveResponse` | `order.ReceiveRes` | Response | 1 | - | - | - | - |
| `OrderShipRequest` | `order.ShipReq` | Request | 1 | - | - | - | - |
| `OrderShipResponse` | `order.ShipRes` | Response | 1 | - | - | - | - |
| `OrderAdminListRequest` | `order.AdminListReq` | Request | 9 | - | `status` | - | `page`<br>`size`<br>`user_id` |
| `OrderAdminListResponse` | `order.AdminListRes` | Response | 4 | - | - | - | - |
| `AdminOrderListItem` | `order.AdminOrderListItem` | Entity | 6 | `total_amount` | `status` | `created_at` | `created_at` |
| `OrderAdminOrderStatsRequest` | `order.AdminOrderStatsReq` | Request | 0 | - | - | - | - |
| `OrderStatsResponse` | `order.AdminOrderStatsRes` | Response | 2 | - | - | - | - |
| `OrderStatsItem` | `order.AdminOrderStatsItem` | Entity | 2 | - | `status` | - | - |
| `OrderAdminOrderDetailRequest` | `order.AdminOrderDetailReq` | Request | 1 | - | - | - | - |
| `OrderAdminDetailResponse` | `order.AdminOrderDetailRes` | Response | 1 | - | - | - | - |
| `Product` | `product.Product` | Entity | 12 | `price` | `status` | `created_at`<br>`updated_at` | `created_at`<br>`updated_at` |
| `ProductListRequest` | `product.ListReq` | Request | 6 | - | - | - | `category_id` |
| `ProductListResponse` | `product.ListRes` | Response | 4 | - | - | - | - |
| `ProductDetailRequest` | `product.DetailReq` | Request | 1 | - | - | - | - |
| `ProductDetailResponse` | `product.DetailRes` | Response | 2 | - | - | - | - |
| `ProductAdminListRequest` | `product.AdminListReq` | Request | 6 | - | - | - | `category_id` |
| `ProductAdminListResponse` | `product.AdminListRes` | Response | 4 | - | - | - | - |
| `ProductAdminDetailRequest` | `product.AdminDetailReq` | Request | 1 | - | - | - | - |
| `ProductAdminDetailResponse` | `product.AdminDetailRes` | Response | 2 | - | - | - | - |
| `ProductCreateRequest` | `product.CreateReq` | Request | 7 | - | - | - | - |
| `ProductCreateResponse` | `product.CreateRes` | Response | 1 | - | - | - | - |
| `ProductUpdateRequest` | `product.UpdateReq` | Request | 9 | - | - | - | `name`<br>`brand`<br>`category_id`<br>`price`<br>`main_image`<br>`detail`<br>`status`<br>`images` |
| `ProductUpdateResponse` | `product.UpdateRes` | Response | 1 | - | - | - | - |
| `ProductOnShelfRequest` | `product.OnShelfReq` | Request | 1 | - | - | - | - |
| `ProductOnShelfResponse` | `product.OnShelfRes` | Response | 1 | - | - | - | - |
| `ProductOffShelfRequest` | `product.OffShelfReq` | Request | 1 | - | - | - | - |
| `ProductOffShelfResponse` | `product.OffShelfRes` | Response | 1 | - | - | - | - |
| `UserProfile` | `profile.Profile` | Entity | 6 | - | - | - | `phone`<br>`birthday` |
| `ProfileGetRequest` | `profile.GetReq` | Request | 0 | - | - | - | - |
| `ProfileGetResponse` | `profile.GetRes` | Response | 1 | - | - | - | - |
| `ProfileUpdateRequest` | `profile.UpdateReq` | Request | 4 | - | - | - | `nickname`<br>`avatar_url`<br>`phone`<br>`birthday` |
| `ProfileUpdateResponse` | `profile.UpdateRes` | Response | 1 | - | - | - | - |
| `RecommendItemSnapshot` | `recommendation.ItemSnapshot` | Entity | 5 | `price` | - | - | - |
| `RecommendItem` | `recommendation.Item` | Entity | 6 | - | - | `created_at`<br>`updated_at` | `created_at`<br>`updated_at` |
| `RecommendPosition` | `recommendation.Position` | Entity | 6 | - | `status` | `created_at`<br>`updated_at` | `created_at`<br>`updated_at` |
| `RecommendFrontendRequest` | `recommendation.FrontendReq` | Request | 1 | - | - | - | - |
| `RecommendFrontendResponse` | `recommendation.FrontendRes` | Response | 3 | - | - | - | - |
| `RecommendAdminListRequest` | `recommendation.AdminListReq` | Request | 0 | - | - | - | - |
| `RecommendAdminListResponse` | `recommendation.AdminListRes` | Response | 1 | - | - | - | - |
| `RecommendAdminDetailRequest` | `recommendation.AdminDetailReq` | Request | 1 | - | - | - | - |
| `RecommendAdminDetailResponse` | `recommendation.AdminDetailRes` | Response | 2 | - | - | - | - |
| `RecommendCreateRequest` | `recommendation.CreateReq` | Request | 3 | - | - | - | `status` |
| `RecommendCreateResponse` | `recommendation.CreateRes` | Response | 1 | - | - | - | - |
| `RecommendUpdateRequest` | `recommendation.UpdateReq` | Request | 3 | - | - | - | `name`<br>`status` |
| `RecommendUpdateResponse` | `recommendation.UpdateRes` | Response | 1 | - | - | - | - |
| `RecommendDeleteRequest` | `recommendation.DeleteReq` | Request | 1 | - | - | - | - |
| `RecommendDeleteResponse` | `recommendation.DeleteRes` | Response | 0 | - | - | - | - |
| `RecommendAddItemRequest` | `recommendation.AddItemReq` | Request | 3 | - | - | - | `sort` |
| `RecommendAddItemResponse` | `recommendation.AddItemRes` | Response | 1 | - | - | - | - |
| `RecommendRemoveItemRequest` | `recommendation.RemoveItemReq` | Request | 2 | - | - | - | - |
| `RecommendRemoveItemResponse` | `recommendation.RemoveItemRes` | Response | 0 | - | - | - | - |
| `RecommendUpdateSortRequest` | `recommendation.UpdateSortReq` | Request | 2 | - | - | - | - |
| `RecommendUpdateSortResponse` | `recommendation.UpdateSortRes` | Response | 1 | - | - | - | - |
| `Refund` | `refund.Refund` | Entity | 14 | `amount` | `status` | `reviewed_at`<br>`executed_at`<br>`created_at`<br>`updated_at` | `reviewed_by`<br>`reviewed_at`<br>`executed_by`<br>`executed_at`<br>`created_at`<br>`updated_at` |
| `RefundApplyRequest` | `refund.ApplyReq` | Request | 2 | - | - | - | - |
| `RefundApplyResponse` | `refund.ApplyRes` | Response | 1 | - | - | - | - |
| `RefundRevokeRequest` | `refund.RevokeReq` | Request | 1 | - | - | - | - |
| `RefundRevokeResponse` | `refund.RevokeRes` | Response | 1 | - | - | - | - |
| `RefundDetailRequest` | `refund.DetailReq` | Request | 1 | - | - | - | - |
| `RefundDetailResponse` | `refund.DetailRes` | Response | 1 | - | - | - | - |
| `RefundListRequest` | `refund.ListReq` | Request | 1 | - | - | - | - |
| `RefundListResponse` | `refund.ListRes` | Response | 1 | - | - | - | - |
| `RefundAdminListRequest` | `refund.AdminListReq` | Request | 5 | - | `status` | - | `page`<br>`size`<br>`order_id`<br>`user_id` |
| `RefundAdminListResponse` | `refund.AdminListRes` | Response | 4 | - | - | - | - |
| `RefundAdminDetailRequest` | `refund.AdminDetailReq` | Request | 1 | - | - | - | - |
| `RefundAdminDetailResponse` | `refund.AdminDetailRes` | Response | 1 | - | - | - | - |
| `RefundListAuditsRequest` | `refund.ListAuditsReq` | Request | 1 | - | - | - | - |
| `RefundListAuditsResponse` | `refund.ListAuditsRes` | Response | 1 | - | - | - | - |
| `RefundAudit` | `refund.RefundAudit` | Entity | 10 | - | `action`<br>`operator_type`<br>`before_status`<br>`after_status` | `created_at` | `created_at` |
| `RefundReviewRequest` | `refund.ReviewReq` | Request | 3 | - | `result` | - | - |
| `RefundReviewResponse` | `refund.ReviewRes` | Response | 1 | - | - | - | - |
| `RefundExecuteRequest` | `refund.ExecuteReq` | Request | 3 | - | `result` | - | - |
| `RefundExecuteResponse` | `refund.ExecuteRes` | Response | 1 | - | - | - | - |
| `RefundCloseRequest` | `refund.CloseReq` | Request | 2 | - | - | - | - |
| `RefundCloseResponse` | `refund.CloseRes` | Response | 1 | - | - | - | - |
| `Region` | `region.Region` | Entity | 4 | - | - | - | - |
| `RegionListRequest` | `region.ListReq` | Request | 1 | - | - | - | - |
| `RegionListResponse` | `region.ListRes` | Response | 1 | - | - | - | - |
| `RegionTreeNode` | `region.TreeNode` | Entity | 2 | - | - | - | - |
| `RegionTreeRequest` | `region.TreeReq` | Request | 0 | - | - | - | - |
| `RegionTreeResponse` | `region.TreeRes` | Response | 2 | - | - | - | - |
| `RegionDetailRequest` | `region.DetailReq` | Request | 1 | - | - | - | - |
| `RegionDetailResponse` | `region.DetailRes` | Response | 4 | - | - | - | `province`<br>`city`<br>`district` |
| `ReviewItem` | `review.ReviewItem` | Entity | 7 | - | - | `created_at` | `created_at` |
| `Review` | `review.Review` | Entity | 9 | - | `status` | `created_at` | `created_at` |
| `ReviewListRequest` | `review.ListReq` | Request | 3 | - | - | - | - |
| `ReviewListResponse` | `review.ListRes` | Response | 6 | - | - | - | - |
| `ReviewCreateRequest` | `review.CreateReq` | Request | 3 | - | - | - | - |
| `ReviewCreateResponse` | `review.CreateRes` | Response | 1 | - | - | - | - |
| `ReviewMyListRequest` | `review.MyListReq` | Request | 2 | - | - | - | - |
| `ReviewMyListResponse` | `review.MyListRes` | Response | 4 | - | - | - | - |
| `ReviewUpdateRequest` | `review.UpdateReq` | Request | 3 | - | - | - | - |
| `ReviewUpdateResponse` | `review.UpdateRes` | Response | 1 | - | - | - | - |
| `ReviewDeleteRequest` | `review.DeleteReq` | Request | 1 | - | - | - | - |
| `ReviewDeleteResponse` | `review.DeleteRes` | Response | 1 | - | - | - | - |
| `ReviewTakeDownRequest` | `review.TakeDownReq` | Request | 1 | - | - | - | - |
| `ReviewTakeDownResponse` | `review.TakeDownRes` | Response | 1 | - | - | - | - |
| `Sku` | `sku.Sku` | Entity | 7 | `price` | `status` | `created_at`<br>`updated_at` | `created_at`<br>`updated_at` |
| `SkuCreateRequest` | `sku.CreateReq` | Request | 4 | - | `status` | - | `status` |
| `SkuCreateResponse` | `sku.CreateRes` | Response | 1 | - | - | - | - |
| `SkuUpdateRequest` | `sku.UpdateReq` | Request | 4 | - | `status` | - | `name`<br>`price`<br>`status` |
| `SkuUpdateResponse` | `sku.UpdateRes` | Response | 1 | - | - | - | - |
| `SkuDeleteRequest` | `sku.DeleteReq` | Request | 1 | - | - | - | - |
| `SkuDeleteResponse` | `sku.DeleteRes` | Response | 0 | - | - | - | - |
| `UploadFrontendTokenRequest` | `upload.FrontendTokenReq` | Request | 2 | - | - | - | - |
| `UploadAdminTokenRequest` | `upload.AdminTokenReq` | Request | 2 | - | - | - | - |
| `UploadTokenResponse` | `upload.TokenRes` | Response | 6 | - | - | - | - |

## 5. 金额字段（整数分，`x-unit: cent`）

| Schema | 字段 | Go 类型 |
|---|---|---|
| `CartItem` | `price_snapshot` | int64 |
| `CouponTemplate` | `threshold_amount` | int64 |
| `CouponTemplate` | `discount_amount` | int64 |
| `UserCoupon` | `threshold_amount` | int64 |
| `UserCoupon` | `discount_amount` | int64 |
| `CouponAdminCreateRequest` | `threshold_amount` | int64 |
| `CouponAdminCreateRequest` | `discount_amount` | int64 |
| `DashboardOverviewResponse` | `today_order_amount` | int64 |
| `DistributionCommission` | `base_amount` | int64 |
| `DistributionCommission` | `commission_amount` | int64 |
| `DistributionCommission` | `reversed_amount` | int64 |
| `DistributionCommissionSummary` | `pending_amount` | int64 |
| `DistributionCommissionSummary` | `settled_amount` | int64 |
| `DistributionCommissionSummary` | `reversed_amount` | int64 |
| `DistributionDistributor` | `commission_amount` | int64 |
| `DistributionDistributor` | `reversed_amount` | int64 |
| `DistributionCommissionStatsItem` | `amount` | int64 |
| `DistributionCommissionStatsItem` | `reversed_amount` | int64 |
| `DistributionAdminCommissionStatsResponse` | `total_amount` | int64 |
| `FlashSaleActivitySku` | `flash_price` | int64 |
| `FlashSaleOrder` | `flash_price` | int64 |
| `GroupBuyActivity` | `group_price` | int64 |
| `GroupBuyActivity` | `sku_price` | int64 |
| `GroupBuyTeam` | `group_price` | int64 |
| `GroupBuyAdminCreateRequest` | `group_price` | int64 |
| `GroupBuyAdminUpdateRequest` | `group_price` | int64 |
| `Order` | `total_amount` | int64 |
| `Order` | `original_amount` | int64 |
| `Order` | `discount_amount` | int64 |
| `OrderItem` | `price` | int64 |
| `AdminOrderListItem` | `total_amount` | int64 |
| `Product` | `price` | int64 |
| `RecommendItemSnapshot` | `price` | int64 |
| `Refund` | `amount` | int64 |
| `Sku` | `price` | int64 |

## 6. 枚举字段

| Schema | 字段 | Go 类型 |
|---|---|---|
| `AdminUpdateAdminStatusRequest` | `status` | int |
| `Banner` | `status` | int |
| `BannerCreateRequest` | `status` | *int |
| `BannerUpdateRequest` | `status` | *int |
| `CartItem` | `unavailable_reason` | string |
| `Category` | `status` | int |
| `CategoryCreateRequest` | `status` | *int |
| `CategoryUpdateRequest` | `status` | *int |
| `CouponTemplate` | `status` | int |
| `UserCoupon` | `status` | string |
| `DashboardStatusItem` | `status` | int |
| `DistributionCommission` | `status` | string |
| `DistributionCommissionStatsItem` | `status` | string |
| `FavoriteItem` | `unavailable_reason` | string |
| `FlashSaleActivity` | `status` | string |
| `FlashSaleCreateOrderResponse` | `status` | string |
| `FlashSaleGetOrderResultResponse` | `status` | string |
| `FlashSaleRepairRequestRequest` | `target_status` | string |
| `FlashSaleRepairRequestResponse` | `status` | string |
| `FlashSaleRequestAudit` | `before_status` | int |
| `FlashSaleRequestAudit` | `after_status` | int |
| `UserGetUserStatusResponse` | `status` | int |
| `UserUpdateUserStatusRequest` | `status` | int |
| `InventoryLog` | `change_type` | string |
| `Order` | `status` | string |
| `Order` | `cancel_reason` | *int |
| `OrderCreateRequest` | `source` | string |
| `OrderAdminListRequest` | `status` | string |
| `AdminOrderListItem` | `status` | string |
| `OrderStatsItem` | `status` | string |
| `Product` | `status` | string |
| `RecommendPosition` | `status` | int |
| `Refund` | `status` | string |
| `RefundAdminListRequest` | `status` | string |
| `RefundAudit` | `action` | string |
| `RefundAudit` | `operator_type` | int |
| `RefundAudit` | `before_status` | int |
| `RefundAudit` | `after_status` | int |
| `RefundReviewRequest` | `result` | string |
| `RefundExecuteRequest` | `result` | string |
| `Review` | `status` | string |
| `Sku` | `status` | string |
| `SkuCreateRequest` | `status` | *string |
| `SkuUpdateRequest` | `status` | *string |

string 枚举直接声明 `enum`；整数枚举同时声明 `x-enum-labels` 给出数值与业务含义的对照。

## 7. 时间字段

| Schema | 字段 | Go 类型 | 形态 |
|---|---|---|---|
| `Address` | `created_at` | *gtime | response |
| `Address` | `updated_at` | *gtime | response |
| `Article` | `created_at` | *gtime | response |
| `Article` | `updated_at` | *gtime | response |
| `ArticleItem` | `created_at` | *gtime | response |
| `ArticleItem` | `updated_at` | *gtime | response |
| `Banner` | `created_at` | *gtime | response |
| `Banner` | `updated_at` | *gtime | response |
| `CartItem` | `created_at` | *gtime | response |
| `CartItem` | `updated_at` | *gtime | response |
| `Category` | `created_at` | *gtime | response |
| `Category` | `updated_at` | *gtime | response |
| `CouponTemplate` | `claim_start_time` | *gtime | response |
| `CouponTemplate` | `claim_end_time` | *gtime | response |
| `CouponTemplate` | `use_start_time` | *gtime | response |
| `CouponTemplate` | `use_end_time` | *gtime | response |
| `CouponTemplate` | `created_at` | *gtime | response |
| `CouponTemplate` | `updated_at` | *gtime | response |
| `UserCoupon` | `received_at` | *gtime | response |
| `UserCoupon` | `locked_at` | *gtime | response |
| `UserCoupon` | `used_at` | *gtime | response |
| `UserCoupon` | `use_start_time` | *gtime | response |
| `UserCoupon` | `use_end_time` | *gtime | response |
| `CouponAdminCreateRequest` | `claim_start_time` | *gtime | request |
| `CouponAdminCreateRequest` | `claim_end_time` | *gtime | request |
| `CouponAdminCreateRequest` | `use_start_time` | *gtime | request |
| `CouponAdminCreateRequest` | `use_end_time` | *gtime | request |
| `DistributionReferral` | `created_at` | *gtime | response |
| `DistributionCommission` | `settled_at` | *gtime | response |
| `DistributionCommission` | `reversed_at` | *gtime | response |
| `DistributionCommission` | `created_at` | *gtime | response |
| `DistributionCommission` | `updated_at` | *gtime | response |
| `DistributionSubordinate` | `bound_at` | *gtime | response |
| `DistributionDistributor` | `first_bound_at` | *gtime | response |
| `FavoriteItem` | `created_at` | *gtime | response |
| `FlashSaleActivity` | `start_time` | *gtime | response |
| `FlashSaleActivity` | `end_time` | *gtime | response |
| `FlashSaleActivity` | `created_at` | *gtime | response |
| `FlashSaleActivity` | `updated_at` | *gtime | response |
| `FlashSaleOrder` | `created_at` | *gtime | response |
| `FlashSaleOrder` | `updated_at` | *gtime | response |
| `FlashSaleCreateRequest` | `start_time` | *gtime | request |
| `FlashSaleCreateRequest` | `end_time` | *gtime | request |
| `FlashSaleUpdateRequest` | `start_time` | *gtime | request |
| `FlashSaleUpdateRequest` | `end_time` | *gtime | request |
| `FlashSaleRequestAudit` | `created_at` | *gtime | response |
| `GroupBuyActivity` | `start_time` | *gtime | response |
| `GroupBuyActivity` | `end_time` | *gtime | response |
| `GroupBuyActivity` | `created_at` | *gtime | response |
| `GroupBuyActivity` | `updated_at` | *gtime | response |
| `GroupBuyTeam` | `expire_at` | *gtime | response |
| `GroupBuyTeam` | `closed_at` | *gtime | response |
| `GroupBuyTeam` | `created_at` | *gtime | response |
| `GroupBuyTeam` | `updated_at` | *gtime | response |
| `GroupBuyParticipant` | `created_at` | *gtime | response |
| `GroupBuyMyTeamItem` | `joined_at` | *gtime | response |
| `GroupBuyAdminCreateRequest` | `start_time` | *gtime | request |
| `GroupBuyAdminCreateRequest` | `end_time` | *gtime | request |
| `GroupBuyAdminUpdateRequest` | `start_time` | *gtime | request |
| `GroupBuyAdminUpdateRequest` | `end_time` | *gtime | request |
| `Inventory` | `created_at` | *gtime | response |
| `Inventory` | `updated_at` | *gtime | response |
| `InventoryLog` | `created_at` | *gtime | response |
| `Order` | `expire_at` | *gtime | response |
| `Order` | `paid_at` | *gtime | response |
| `Order` | `shipped_at` | *gtime | response |
| `Order` | `received_at` | *gtime | response |
| `Order` | `completed_at` | *gtime | response |
| `Order` | `cancelled_at` | *gtime | response |
| `Order` | `refunded_at` | *gtime | response |
| `Order` | `created_at` | *gtime | response |
| `Order` | `updated_at` | *gtime | response |
| `AdminOrderListItem` | `created_at` | *gtime | response |
| `Product` | `created_at` | *gtime | response |
| `Product` | `updated_at` | *gtime | response |
| `RecommendItem` | `created_at` | *gtime | response |
| `RecommendItem` | `updated_at` | *gtime | response |
| `RecommendPosition` | `created_at` | *gtime | response |
| `RecommendPosition` | `updated_at` | *gtime | response |
| `Refund` | `reviewed_at` | *gtime | response |
| `Refund` | `executed_at` | *gtime | response |
| `Refund` | `created_at` | *gtime | response |
| `Refund` | `updated_at` | *gtime | response |
| `RefundAudit` | `created_at` | *gtime | response |
| `ReviewItem` | `created_at` | *gtime | response |
| `Review` | `created_at` | *gtime | response |
| `Sku` | `created_at` | *gtime | response |
| `Sku` | `updated_at` | *gtime | response |

## 8. 可空字段

| Schema | 字段 |
|---|---|
| `Address` | `region_id` |
| `Address` | `created_at` |
| `Address` | `updated_at` |
| `AddressCreateRequest` | `region_id` |
| `AddressCreateRequest` | `is_default` |
| `AddressUpdateRequest` | `recipient_name` |
| `AddressUpdateRequest` | `phone` |
| `AddressUpdateRequest` | `province` |
| `AddressUpdateRequest` | `city` |
| `AddressUpdateRequest` | `district` |
| `AddressUpdateRequest` | `region_id` |
| `AddressUpdateRequest` | `detail` |
| `AddressUpdateRequest` | `is_default` |
| `AdminUpdateRoleRequest` | `name` |
| `AdminUpdateRoleRequest` | `description` |
| `AdminUpdatePermissionRequest` | `code` |
| `AdminUpdatePermissionRequest` | `name` |
| `AdminUpdatePermissionRequest` | `description` |
| `Article` | `created_at` |
| `Article` | `updated_at` |
| `ArticleItem` | `created_at` |
| `ArticleItem` | `updated_at` |
| `Banner` | `created_at` |
| `Banner` | `updated_at` |
| `BannerCreateRequest` | `status` |
| `BannerUpdateRequest` | `title` |
| `BannerUpdateRequest` | `image_url` |
| `BannerUpdateRequest` | `link_url` |
| `BannerUpdateRequest` | `sort` |
| `BannerUpdateRequest` | `status` |
| `CartItem` | `product_id` |
| `CartItem` | `current_price` |
| `CartItem` | `stock` |
| `CartItem` | `created_at` |
| `CartItem` | `updated_at` |
| `CartAddRequest` | `quantity` |
| `Category` | `created_at` |
| `Category` | `updated_at` |
| `CategoryCreateRequest` | `status` |
| `CategoryUpdateRequest` | `parent_id` |
| `CategoryUpdateRequest` | `name` |
| `CategoryUpdateRequest` | `sort` |
| `CategoryUpdateRequest` | `status` |
| `CouponTemplate` | `claim_start_time` |
| `CouponTemplate` | `claim_end_time` |
| `CouponTemplate` | `use_start_time` |
| `CouponTemplate` | `use_end_time` |
| `CouponTemplate` | `created_at` |
| `CouponTemplate` | `updated_at` |
| `UserCoupon` | `order_id` |
| `UserCoupon` | `received_at` |
| `UserCoupon` | `locked_at` |
| `UserCoupon` | `used_at` |
| `UserCoupon` | `use_start_time` |
| `UserCoupon` | `use_end_time` |
| `CouponAdminCreateRequest` | `claim_start_time` |
| `CouponAdminCreateRequest` | `claim_end_time` |
| `CouponAdminCreateRequest` | `use_start_time` |
| `CouponAdminCreateRequest` | `use_end_time` |
| `DashboardFlashSalesResponse` | `activity_status` |
| `DashboardFlashSalesResponse` | `request_status` |
| `DistributionReferral` | `created_at` |
| `DistributionCommission` | `settled_at` |
| `DistributionCommission` | `reversed_at` |
| `DistributionCommission` | `refund_id` |
| `DistributionCommission` | `created_at` |
| `DistributionCommission` | `updated_at` |
| `DistributionSubordinate` | `bound_at` |
| `DistributionDistributor` | `first_bound_at` |
| `DistributionMyInfoResponse` | `referrer` |
| `DistributionListSubordinatesRequest` | `page` |
| `DistributionListSubordinatesRequest` | `size` |
| `DistributionListCommissionsRequest` | `page` |
| `DistributionListCommissionsRequest` | `size` |
| `DistributionAdminListReferralsRequest` | `page` |
| `DistributionAdminListReferralsRequest` | `size` |
| `DistributionAdminListReferralsRequest` | `user_id` |
| `DistributionAdminListReferralsRequest` | `referrer_user_id` |
| `DistributionAdminListDistributorsRequest` | `page` |
| `DistributionAdminListDistributorsRequest` | `size` |
| `DistributionAdminListCommissionsRequest` | `page` |
| `DistributionAdminListCommissionsRequest` | `size` |
| `DistributionAdminListCommissionsRequest` | `order_id` |
| `DistributionAdminListCommissionsRequest` | `buyer_user_id` |
| `DistributionAdminListCommissionsRequest` | `beneficiary_user_id` |
| `FavoriteItem` | `product_price` |
| `FavoriteItem` | `created_at` |
| `FlashSaleActivity` | `start_time` |
| `FlashSaleActivity` | `end_time` |
| `FlashSaleActivity` | `created_at` |
| `FlashSaleActivity` | `updated_at` |
| `FlashSaleOrder` | `created_at` |
| `FlashSaleOrder` | `updated_at` |
| `FlashSaleCreateRequest` | `start_time` |
| `FlashSaleCreateRequest` | `end_time` |
| `FlashSaleUpdateRequest` | `name` |
| `FlashSaleUpdateRequest` | `status` |
| `FlashSaleUpdateRequest` | `start_time` |
| `FlashSaleUpdateRequest` | `end_time` |
| `FlashSaleGetOrderResultResponse` | `order` |
| `FlashSaleRequestAudit` | `created_at` |
| `GroupBuyActivity` | `start_time` |
| `GroupBuyActivity` | `end_time` |
| `GroupBuyActivity` | `created_at` |
| `GroupBuyActivity` | `updated_at` |
| `GroupBuyTeam` | `expire_at` |
| `GroupBuyTeam` | `closed_at` |
| `GroupBuyTeam` | `created_at` |
| `GroupBuyTeam` | `updated_at` |
| `GroupBuyParticipant` | `created_at` |
| `GroupBuyTeamDetail` | `my_participant` |
| `GroupBuyTeamDetail` | `my_order_id` |
| `GroupBuyMyTeamItem` | `order_id` |
| `GroupBuyMyTeamItem` | `joined_at` |
| `GroupBuyListActivitiesRequest` | `page` |
| `GroupBuyListActivitiesRequest` | `size` |
| `GroupBuyListTeamsRequest` | `page` |
| `GroupBuyListTeamsRequest` | `size` |
| `GroupBuyMyListRequest` | `page` |
| `GroupBuyMyListRequest` | `size` |
| `GroupBuyAdminCreateRequest` | `start_time` |
| `GroupBuyAdminCreateRequest` | `end_time` |
| `GroupBuyAdminUpdateRequest` | `start_time` |
| `GroupBuyAdminUpdateRequest` | `end_time` |
| `GroupBuyAdminListRequest` | `page` |
| `GroupBuyAdminListRequest` | `size` |
| `GroupBuyAdminListRequest` | `product_id` |
| `GroupBuyAdminListRequest` | `sku_id` |
| `GroupBuyAdminListTeamsRequest` | `page` |
| `GroupBuyAdminListTeamsRequest` | `size` |
| `GroupBuyAdminListTeamsRequest` | `activity_id` |
| `GroupBuyAdminListTeamsRequest` | `leader_user_id` |
| `Inventory` | `created_at` |
| `Inventory` | `updated_at` |
| `InventoryLog` | `operator_admin_id` |
| `InventoryLog` | `created_at` |
| `Order` | `user_coupon_id` |
| `Order` | `address_id` |
| `Order` | `expire_at` |
| `Order` | `cancel_reason` |
| `Order` | `paid_at` |
| `Order` | `shipped_at` |
| `Order` | `received_at` |
| `Order` | `completed_at` |
| `Order` | `cancelled_at` |
| `Order` | `refunded_at` |
| `Order` | `created_at` |
| `Order` | `updated_at` |
| `OrderAdminListRequest` | `page` |
| `OrderAdminListRequest` | `size` |
| `OrderAdminListRequest` | `user_id` |
| `AdminOrderListItem` | `created_at` |
| `Product` | `created_at` |
| `Product` | `updated_at` |
| `ProductListRequest` | `category_id` |
| `ProductAdminListRequest` | `category_id` |
| `ProductUpdateRequest` | `name` |
| `ProductUpdateRequest` | `brand` |
| `ProductUpdateRequest` | `category_id` |
| `ProductUpdateRequest` | `price` |
| `ProductUpdateRequest` | `main_image` |
| `ProductUpdateRequest` | `detail` |
| `ProductUpdateRequest` | `status` |
| `ProductUpdateRequest` | `images` |
| `UserProfile` | `phone` |
| `UserProfile` | `birthday` |
| `ProfileUpdateRequest` | `nickname` |
| `ProfileUpdateRequest` | `avatar_url` |
| `ProfileUpdateRequest` | `phone` |
| `ProfileUpdateRequest` | `birthday` |
| `RecommendItem` | `created_at` |
| `RecommendItem` | `updated_at` |
| `RecommendPosition` | `created_at` |
| `RecommendPosition` | `updated_at` |
| `RecommendCreateRequest` | `status` |
| `RecommendUpdateRequest` | `name` |
| `RecommendUpdateRequest` | `status` |
| `RecommendAddItemRequest` | `sort` |
| `Refund` | `reviewed_by` |
| `Refund` | `reviewed_at` |
| `Refund` | `executed_by` |
| `Refund` | `executed_at` |
| `Refund` | `created_at` |
| `Refund` | `updated_at` |
| `RefundAdminListRequest` | `page` |
| `RefundAdminListRequest` | `size` |
| `RefundAdminListRequest` | `order_id` |
| `RefundAdminListRequest` | `user_id` |
| `RefundAudit` | `created_at` |
| `RegionDetailResponse` | `province` |
| `RegionDetailResponse` | `city` |
| `RegionDetailResponse` | `district` |
| `ReviewItem` | `created_at` |
| `Review` | `created_at` |
| `Sku` | `created_at` |
| `Sku` | `updated_at` |
| `SkuCreateRequest` | `status` |
| `SkuUpdateRequest` | `name` |
| `SkuUpdateRequest` | `price` |
| `SkuUpdateRequest` | `status` |

## 9. 已知限制与不猜测声明

1. `openapi/surgecart.openapi.yaml` 内联全部 paths：OpenAPI 3.1 的 Path Item Object 不支持 `$ref`，逐 path 引用会产出非法契约。`openapi/paths/*.yaml` 是同源生成的**分域视图**，由 `contract-check.sh` 校验与主文件一致。
2. 请求 DTO 中的 `json.Number` 同时接受整数与数字字符串，这是 GoFrame 绑定的实际行为，契约用 `oneOf` 表达。
3. `dc` 描述中的业务约束（如长度、取值范围）未全部映射为 Schema 约束，因为 DTO 未使用 `v:"min/max/length"`，这些约束由 service 层显式校验；契约只在 `description` 中保留原文。
4. 生成器无法从 DTO 判断成功响应的 `data` 是 `null` 还是 `{}`；该事实由 `tools/openapi-gen/opmeta.go` 的 `NullData` 显式登记，依据是 controller 的实际 `return nil, nil`。未登记项一律按实际结构体输出。
5. 错误码不落到单接口粒度：每个 operation 的 `x-error-codes` 是「该业务域在 `internal/logic` 中实际引用的错误码集合」+ 通用码，并以 `scope: domain` 明确声明，不声称是逐路径穷举。
