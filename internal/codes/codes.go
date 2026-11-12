// Package codes 集中定义项目级业务错误码、HTTP 状态与用户安全 message。
// 客户端依靠 code 判断错误类型，不依赖 message 文本。
package codes

import (
	"net/http"

	"github.com/gogf/gf/v2/errors/gcode"
	"github.com/gogf/gf/v2/errors/gerror"
)

// Code 是项目统一的业务错误码（int）。
type Code = int

const (
	CodeOK Code = 0 // 成功

	// 通用/系统域 1000-1999，与 HTTP 语义对齐，全项目复用。
	CodeInternalError      Code = 1000 // 内部错误 → 500
	CodeInvalidArgument    Code = 1001 // 参数校验失败 → 400
	CodeUnauthorized       Code = 1002 // 未授权 → 401
	CodeForbidden          Code = 1003 // 禁止访问 → 403
	CodeNotFound           Code = 1004 // 未找到 → 404
	CodeServiceUnavailable Code = 1005 // 服务不可用 → 503

	// 认证/用户域（IAM）2000-2999。
	CodeUsernameExists     Code = 2001 // 用户名已存在 → 409
	CodeInvalidCredentials Code = 2002 // 用户名或密码错误 → 401
	CodeSessionNotFound    Code = 2011 // 会话不存在或不属于当前用户 → 404

	// refresh token（IAM 段扩展，仅前台用户域）。
	CodeRefreshTokenInvalid Code = 2012 // refresh token 无效/未知/篡改/已撤销（不泄露存在性）→ 401
	CodeRefreshTokenExpired Code = 2013 // refresh token 已过期 → 401
	CodeRefreshTokenReuse   Code = 2014 // refresh token 被重放（已轮换 token 再次提交）→ 401
	CodeUserNotFound        Code = 2015 // 用户状态管理目标用户不存在 → 404
	CodePhoneExists         Code = 2016 // 手机号已被使用（users.uk_phone 唯一约束冲突）→ 409

	// 后台管理员身份与 RBAC（IAM 段扩展）。
	CodeAdminNotFound          Code = 2003 // 管理员不存在 → 404
	CodeAdminUsernameExists    Code = 2004 // 管理员用户名已存在 → 409
	CodeSuperAdminProtected    Code = 2005 // 超级管理员受保护 → 403
	CodeSelfOperationForbidden Code = 2006 // 不能操作自身 → 403
	CodeRoleNotFound           Code = 2007 // 角色不存在 → 404
	CodeRoleNameExists         Code = 2008 // 角色名已存在 → 409
	CodePermissionNotFound     Code = 2009 // 权限不存在 → 404
	CodePermissionCodeExists   Code = 2010 // 权限 code 已存在 → 409

	// 商品分类域（Category）3000-3999。
	CodeCategoryNotFound      Code = 3001 // 分类不存在 → 404
	CodeCategoryNameExists    Code = 3002 // 同级重名 → 409
	CodeCategoryHasChildren   Code = 3003 // 有子分类不能删除 → 409
	CodeCategoryInvalidParent Code = 3004 // 父分类非法 → 400
	CodeCategoryHasProducts   Code = 3005 // 分类下存在商品不能删除 → 409

	// 商品域（Product）4000-4999。
	CodeProductNotFound                Code = 4001 // 商品不存在 → 404
	CodeProductInvalidPrice            Code = 4002 // 价格非法（负数/非整数/超上限）→ 400
	CodeProductInvalidCategory         Code = 4003 // 分类不存在或非法 → 400
	CodeProductCategoryNotLeaf         Code = 4004 // 分类非叶子分类 → 400
	CodeProductInvalidStatusTransition Code = 4005 // 状态迁移非法/并发冲突 → 409
	CodeProductInvalidStatus           Code = 4006 // 未知 status 值 → 400
	CodeProductCategoryDisabled        Code = 4007 // 分类已禁用 → 400

	// SKU 域（Sku）5000-5999。
	CodeSkuNotFound      Code = 5001 // SKU 不存在 → 404
	CodeSkuInvalidPrice  Code = 5002 // 价格非法（负数/非整数/超上限）→ 400
	CodeSkuInvalidStatus Code = 5003 // 未知 status 值 → 400
	CodeSkuNameExists    Code = 5004 // 同商品下 name 已存在 → 409
	CodeSkuHasInventory  Code = 5005 // SKU 存在库存记录，不能删除 → 409

	// 商品规格与 SKU 组合（B1-04-02，仍属 SKU 域）。
	CodeSpecNotFound     Code = 5010 // 规格组/规格值不存在 → 404
	CodeSpecNameExists   Code = 5011 // 同商品下规格组名已存在 → 409
	CodeSpecValueExists  Code = 5012 // 同规格组下规格值已存在 → 409
	CodeSpecValueInUse   Code = 5013 // 规格值已被 SKU 引用，不能删除 → 409
	CodeSkuSpecMissing   Code = 5015 // 商品未配置规格，无法生成组合 → 400
	CodeSkuSpecDuplicate Code = 5016 // 相同规格组合已存在 → 409
	CodeSkuCodeExists    Code = 5017 // SKU 编码已存在（唯一约束兜底）→ 409

	// 库存域（Inventory）6000-6999。
	CodeInventoryInsufficient    Code = 6001 // 库存不足 → 409
	CodeInventoryInvalidQuantity Code = 6002 // 库存数量非正整数 → 400

	// 收货地址域（Address）7000-7999。
	CodeAddressNotFound        Code = 7001 // 地址不存在或非本人（统一不泄露）→ 404
	CodeAddressDefaultConflict Code = 7002 // 并发设置默认地址唯一冲突 → 409

	// 购物车域（Cart）8000-8999。
	CodeCartItemNotFound    Code = 8001 // 条目不存在或不属于当前用户 → 404
	CodeCartInvalidQuantity Code = 8002 // 数量非正整数或超上限 → 400
	CodeCartSkuUnavailable  Code = 8003 // 商品下架或 SKU 禁用，不可加购 → 409

	// 订单域（Order）9000-9999。
	CodeOrderNotFound                Code = 9001 // 订单不存在或不属于当前用户 → 404（防枚举）
	CodeOrderInvalidStatusTransition Code = 9002 // 非法状态迁移 → 409
	CodeOrderSkuUnavailable          Code = 9003 // 下单时 SKU 禁用或商品下架 → 409
	CodeOrderCartEmpty               Code = 9004 // 从购物车下单无勾选项 → 400
	CodeOrderInvalidQuantity         Code = 9005 // 直接购买数量非法 → 400
	CodeOrderIdempotencyConflict     Code = 9006 // 同幂等键不同请求内容 → 409
	CodeOrderPriceMismatch           Code = 9007 // 客户端提交单价与服务端权威定价不一致 → 409

	// 商品评价域（Review）10000-10999。
	CodeReviewNotFound      Code = 10001 // 评价不存在/非本人/不可见（防枚举）→ 404
	CodeReviewNotEligible   Code = 10002 // 未购买该商品或订单未达可评价状态 → 409
	CodeReviewAlreadyExists Code = 10003 // 同一订单项已评价 → 409
	CodeReviewInvalidInput  Code = 10004 // 星级越界/内容为空或超长 → 400

	// 商品收藏域（Favorite）11000-11999。
	CodeFavoriteProductUnavailable Code = 11001 // 商品不在售/不可收藏 → 409

	// 商品点赞域（Like）13000-13999。
	CodeLikeProductUnavailable Code = 13001 // 商品不在售/不可点赞 → 409

	// 秒杀域（FlashSale）12000-12999。
	CodeFlashSaleActivityNotFound     Code = 12001 // 秒杀活动不存在或已下架 → 404
	CodeFlashSaleNotInTimeWindow      Code = 12002 // 活动未开始或已结束 → 409
	CodeFlashSaleStockInsufficient    Code = 12003 // 秒杀库存不足 → 409
	CodeFlashSaleAlreadyPurchased     Code = 12004 // 一人一单，已购买 → 409
	CodeFlashSaleIdempotencyConflict  Code = 12005 // 同幂等键不同请求内容 → 409
	CodeFlashSaleSkuUnavailable       Code = 12006 // 下单时 SKU 禁用或商品下架 → 409
	CodeFlashSaleInvalidArgument      Code = 12007 // 活动参数非法（秒杀价/库存/时间/状态）→ 400
	CodeFlashSaleRequestNotRepairable Code = 12008 // 秒杀请求当前状态不可修复（仅 dead 可修复）→ 409
	CodeFlashSaleRateLimited          Code = 12009 // 用户级/活动级限流拒绝（无副作用）→ 429
	CodeFlashSaleQueueFull            Code = 12010 // 排队容量软上限拒绝（无副作用）→ 429

	// 轮播图域（Banner）14000-14999。
	CodeBannerNotFound     Code = 14001 // 轮播图不存在 → 404
	CodeBannerInvalidInput Code = 14002 // 轮播图参数非法（标题/图片/跳转/状态）→ 400

	// 推荐位域（Recommendation）15000-15999。
	CodeRecommendPositionNotFound   Code = 15001 // 推荐位不存在 → 404
	CodeRecommendPositionCodeExists Code = 15002 // 推荐位 code 已存在 → 409
	CodeRecommendInvalidInput       Code = 15003 // 参数非法（code/name/sort/status/product_id）→ 400
	CodeRecommendItemNotFound       Code = 15004 // 推荐商品关系不存在 → 404
	CodeRecommendItemDuplicate      Code = 15005 // 同一推荐位重复添加同一商品 → 409
	CodeRecommendItemSortMismatch   Code = 15006 // 排序商品列表未覆盖全部已加入商品（缺漏）→ 409

	// 文章域（Article）16000-16999。
	CodeArticleNotFound     Code = 16001 // 文章不存在或非本人（防枚举）；点赞/收藏目标不存在亦复用 → 404
	CodeArticleInvalidInput Code = 16002 // 标题/内容为空或超长 → 400

	// 文件上传/对象存储域（Upload）17000-17999。
	CodeUploadInvalidInput  Code = 17001 // 扩展名/MIME 不在白名单 → 400
	CodeUploadConfigInvalid Code = 17002 // 七牛云配置缺失/非法（安全语义失败）→ 500
	CodeUploadTokenFailed   Code = 17003 // 签发上传凭证失败 → 500

	// 退款域（Refund）18000-18999。
	CodeRefundNotFound                Code = 18001 // 退款申请不存在或非本人/不可见（防枚举）→ 404
	CodeRefundInvalidStatusTransition Code = 18002 // 非法退款状态迁移 → 409
	CodeRefundOrderNotRefundable      Code = 18003 // 订单非已支付未发货，不可退款 → 409
	CodeRefundAlreadyExists           Code = 18004 // 同一订单已存在有效退款申请 → 409
	CodeRefundShipConflict            Code = 18005 // 退款与发货互斥冲突 → 409

	// 优惠券域（Coupon）19000-19999。
	CodeCouponTemplateNotFound Code = 19001 // 优惠券模板不存在 → 404
	CodeCouponTemplateInvalid  Code = 19002 // 模板参数非法（门槛/减免/时间/总量）→ 400
	CodeCouponNotFound         Code = 19003 // 优惠券不存在或不属于当前用户（防枚举）→ 404
	CodeCouponAlreadyClaimed   Code = 19004 // 已领取该模板 → 409
	CodeCouponClaimUnavailable Code = 19005 // 未开始/已结束/已停止，不可领取 → 409
	CodeCouponClaimSoldOut     Code = 19006 // 发放总量已满 → 409
	CodeCouponUnavailable      Code = 19007 // 下单用券：已使用/被占用/不在使用期/未达门槛 → 409

	// 一级分销域（Distribution）20000-20999（域序 20，B1-04-05）。
	CodeDistributionSelfReferral        Code = 20001 // 绑定自己为推荐人 → 400
	CodeDistributionReferrerUnavailable Code = 20002 // 推荐人不存在或已禁用 → 404
	CodeDistributionAlreadyBound        Code = 20003 // 已绑定其他推荐人，不可改绑 → 409
	CodeDistributionCycle               Code = 20004 // 绑定会形成推荐关系环 → 409
	CodeDistributionCommissionNotFound  Code = 20005 // 佣金记录不存在 → 404
	CodeDistributionCommissionStatus    Code = 20006 // 佣金状态迁移非法（已冲回不可结算）→ 409

	// 运费与统一计价域（Freight / Pricing）21000-21999（域序 21）。
	// B1-04 集成：B1-04-03 原编号 20001-20012 与已合入的一级分销域（域序 20）冲突，
	// 按 Registry「域序 = max(已记录域序)+1」规则整体迁移到域序 21，语义与 HTTP 状态不变。
	CodeFreightTemplateNotFound   Code = 21001 // 运费模板不存在 → 404
	CodeFreightTemplateNameExists Code = 21002 // 模板名已存在 → 409
	CodeFreightTemplateInvalid    Code = 21003 // 模板参数非法（名称/阈值/状态）→ 400
	CodeFreightTemplateDisabled   Code = 21004 // 模板已停用，不可用于计价 → 409
	CodeFreightRuleNotFound       Code = 21005 // 区域运费规则不存在 → 404
	CodeFreightRuleDuplicate      Code = 21006 // 同一模板下区域规则重复配置 → 409
	CodeFreightRuleInvalid        Code = 21007 // 规则参数非法（首件/续件数量为 0、金额越界）→ 400
	CodeFreightRegionNotFound     Code = 21008 // 地区不存在 → 404
	CodeFreightRegionRuleMissing  Code = 21009 // 模板无匹配当前地区的规则，拒绝静默免费 → 409
	CodePricingInvalidAmount      Code = 21010 // 计价金额非法（负数/超上限）→ 400
	CodePricingDiscountExceeds    Code = 21011 // 优惠累计超过可优惠金额 → 400
	CodePricingAmountOverflow     Code = 21012 // 计价金额溢出 int64 安全区间 → 400

	// 钱包域（Wallet）22000-22999（域序 22，B1-04-04）。
	// B1-04 集成：原编号 21001-21010 与运费/计价域（域序 21）冲突，整体迁移到域序 22；
	// 约定 22001-22099 为钱包业务码，22000 保留不用。
	CodeWalletAccountUnavailable       Code = 22001 // 钱包账户不存在或初始化失败 → 500
	CodeWalletInsufficientBalance      Code = 22002 // 余额不足（扣款条件更新 0 行）→ 409
	CodeWalletInvalidRechargeAmount    Code = 22003 // 充值金额非法（非正/超上限）→ 400
	CodeWalletRechargeOrderNotFound    Code = 22004 // 充值订单不存在或不属于当前用户（防枚举）→ 404
	CodeWalletRechargeStatusNotAllowed Code = 22005 // 充值订单状态不允许该操作 → 409
	CodeWalletDuplicatePayment         Code = 22006 // 重复支付（订单已扣款）→ 409
	CodeWalletRefundCreditFailed       Code = 22007 // 退款入账失败 → 409
	CodeWalletIdempotencyConflict      Code = 22008 // 资金流水幂等键冲突（同键不同业务金额）→ 409
	CodeWalletMockRechargeDisabled     Code = 22009 // Mock 充值未启用（安全开关关闭）→ 403
	CodeWalletAmountOverflow           Code = 22010 // 金额溢出（余额超出 INT UNSIGNED 可表示范围）→ 409

	// 砍价域（Bargain）23000-23999（域序 23）。
	// B1-04 八分支集成：原编号 21001-21009 与运费/计价域（域序 21）冲突，按域序 23 重新分配。
	CodeBargainActivityNotFound        Code = 23001 // 砍价活动不存在 → 404
	CodeBargainActivityInvalid         Code = 23002 // 活动参数非法（价格/时间/商品/SKU/助力参数）→ 400
	CodeBargainActivityUnavailable     Code = 23003 // 活动不可参与（停用/未开始/已结束）→ 409
	CodeBargainInstanceNotFound        Code = 23004 // 砍价实例不存在或不属于当前用户（防枚举）→ 404
	CodeBargainInstanceExists          Code = 23005 // 同一活动已存在进行中的实例 → 409
	CodeBargainSelfHelpForbidden       Code = 23006 // 不能给自己的砍价助力 → 409
	CodeBargainHelpUnavailable         Code = 23007 // 实例不可助力（已成功/已失败/已过期/次数用尽/已到底价）→ 409
	CodeBargainHelpDuplicated          Code = 23008 // 已助力过该实例 → 409
	CodeBargainInvalidStatusTransition Code = 23009 // 砍价实例状态迁移非法（终态不可重复推进）→ 409

	// 会员等级与成长值域（Member）24000-24999（域序 24，B1-04-08）。
	// B1-04 八分支集成：原编号 20001-20005 与一级分销域（域序 20）冲突，整体迁移到域序 24。
	CodeMemberLevelNotFound      Code = 24001 // 会员等级配置不存在 → 404
	CodeMemberLevelInvalid       Code = 24002 // 等级配置非法（名称/门槛/折扣率/状态/单调性）→ 400
	CodeMemberLevelInUse         Code = 24003 // 等级被用户会员记录引用，不可删除 → 409
	CodeMemberLevelDuplicate     Code = 24004 // 等级序号或成长值门槛已被占用 → 409
	CodeMemberGrowthInsufficient Code = 24005 // 成长值扣减超过当前余额（防御性拒绝）→ 409

	// 拼团域（Group Buy）25000-25999（域序 25，B1-04-06）。
	// B1-04 八分支集成：原编号 21001-21009 与运费/计价域（域序 21）冲突，按域序 25 重新分配。
	CodeGroupBuyNotFound         Code = 25001 // 拼团活动不存在 → 404
	CodeGroupBuyInvalidArgument  Code = 25002 // 活动/参团参数非法（价格、人数、有效期、时间窗）→ 400
	CodeGroupBuyActivityInactive Code = 25003 // 活动未开始/已结束/已停用，不可开团 → 409
	CodeGroupBuyPriceInvalid     Code = 25004 // 拼团价非法（<=0 或高于 SKU 现价）→ 400
	CodeGroupBuyTeamNotFound     Code = 25005 // 团不存在或不可见 → 404
	CodeGroupBuyTeamNotJoinable  Code = 25006 // 团已满/已成团/已失败/已过期，不可参与 → 409
	CodeGroupBuyAlreadyJoined    Code = 25007 // 该用户已在本团中（uk_team_user 兜底）→ 409
	CodeGroupBuySkuUnavailable   Code = 25008 // 拼团商品不可购买（SKU 禁用/商品下架）→ 409
	CodeGroupBuyTeamNotClosable  Code = 25009 // 团不在拼团中，不可关闭 → 409
)

// codeInfo 绑定错误码 → HTTP 状态与用户安全 message。
type codeInfo struct {
	HTTPStatus int
	Message    string
}

var codeTable = map[Code]codeInfo{
	CodeOK:                     {http.StatusOK, "OK"},
	CodeInternalError:          {http.StatusInternalServerError, "内部错误"},
	CodeInvalidArgument:        {http.StatusBadRequest, "参数错误"},
	CodeUnauthorized:           {http.StatusUnauthorized, "未授权"},
	CodeForbidden:              {http.StatusForbidden, "禁止访问"},
	CodeNotFound:               {http.StatusNotFound, "未找到"},
	CodeServiceUnavailable:     {http.StatusServiceUnavailable, "服务不可用"},
	CodeUsernameExists:         {http.StatusConflict, "用户名已存在"},
	CodeInvalidCredentials:     {http.StatusUnauthorized, "用户名或密码错误"},
	CodeSessionNotFound:        {http.StatusNotFound, "会话不存在"},
	CodeRefreshTokenInvalid:    {http.StatusUnauthorized, "refresh token 无效"},
	CodeRefreshTokenExpired:    {http.StatusUnauthorized, "refresh token 已过期"},
	CodeRefreshTokenReuse:      {http.StatusUnauthorized, "refresh token 已失效，请重新登录"},
	CodeUserNotFound:           {http.StatusNotFound, "用户不存在"},
	CodePhoneExists:            {http.StatusConflict, "手机号已被使用"},
	CodeAdminNotFound:          {http.StatusNotFound, "管理员不存在"},
	CodeAdminUsernameExists:    {http.StatusConflict, "管理员用户名已存在"},
	CodeSuperAdminProtected:    {http.StatusForbidden, "超级管理员受保护"},
	CodeSelfOperationForbidden: {http.StatusForbidden, "不能操作自身"},
	CodeRoleNotFound:           {http.StatusNotFound, "角色不存在"},
	CodeRoleNameExists:         {http.StatusConflict, "角色名已存在"},
	CodePermissionNotFound:     {http.StatusNotFound, "权限不存在"},
	CodePermissionCodeExists:   {http.StatusConflict, "权限 code 已存在"},
	CodeCategoryNotFound:       {http.StatusNotFound, "分类不存在"},
	CodeCategoryNameExists:     {http.StatusConflict, "同级重名"},
	CodeCategoryHasChildren:    {http.StatusConflict, "有子分类不能删除"},
	CodeCategoryInvalidParent:  {http.StatusBadRequest, "父分类非法"},
	CodeCategoryHasProducts:    {http.StatusConflict, "分类下存在商品，不能删除"},

	CodeProductNotFound:                {http.StatusNotFound, "商品不存在"},
	CodeProductInvalidPrice:            {http.StatusBadRequest, "价格非法"},
	CodeProductInvalidCategory:         {http.StatusBadRequest, "分类非法"},
	CodeProductCategoryNotLeaf:         {http.StatusBadRequest, "分类不是叶子分类"},
	CodeProductInvalidStatusTransition: {http.StatusConflict, "商品状态迁移非法"},
	CodeProductInvalidStatus:           {http.StatusBadRequest, "商品状态非法"},
	CodeProductCategoryDisabled:        {http.StatusBadRequest, "分类已禁用"},

	CodeSkuNotFound:      {http.StatusNotFound, "SKU 不存在"},
	CodeSkuInvalidPrice:  {http.StatusBadRequest, "价格非法"},
	CodeSkuInvalidStatus: {http.StatusBadRequest, "SKU 状态非法"},
	CodeSkuNameExists:    {http.StatusConflict, "SKU 名称已存在"},
	CodeSkuHasInventory:  {http.StatusConflict, "SKU 存在库存记录，不能删除"},

	CodeSpecNotFound:     {http.StatusNotFound, "规格不存在"},
	CodeSpecNameExists:   {http.StatusConflict, "规格组名已存在"},
	CodeSpecValueExists:  {http.StatusConflict, "规格值已存在"},
	CodeSpecValueInUse:   {http.StatusConflict, "规格值已被 SKU 使用，不能删除"},
	CodeSkuSpecMissing:   {http.StatusBadRequest, "商品未配置规格"},
	CodeSkuSpecDuplicate: {http.StatusConflict, "规格组合已存在"},
	CodeSkuCodeExists:    {http.StatusConflict, "SKU 编码已存在"},

	CodeInventoryInsufficient:    {http.StatusConflict, "库存不足"},
	CodeInventoryInvalidQuantity: {http.StatusBadRequest, "库存数量非法"},

	CodeAddressNotFound:        {http.StatusNotFound, "地址不存在"},
	CodeAddressDefaultConflict: {http.StatusConflict, "默认地址冲突"},

	CodeCartItemNotFound:    {http.StatusNotFound, "购物车条目不存在"},
	CodeCartInvalidQuantity: {http.StatusBadRequest, "数量非法"},
	CodeCartSkuUnavailable:  {http.StatusConflict, "商品不可加购"},

	CodeOrderNotFound:                {http.StatusNotFound, "订单不存在"},
	CodeOrderInvalidStatusTransition: {http.StatusConflict, "订单状态迁移非法"},
	CodeOrderSkuUnavailable:          {http.StatusConflict, "商品不可购买"},
	CodeOrderCartEmpty:               {http.StatusBadRequest, "购物车无勾选商品"},
	CodeOrderInvalidQuantity:         {http.StatusBadRequest, "购买数量非法"},
	CodeOrderIdempotencyConflict:     {http.StatusConflict, "幂等键冲突"},
	CodeOrderPriceMismatch:           {http.StatusConflict, "商品价格与当前售价不一致"},

	CodeReviewNotFound:      {http.StatusNotFound, "评价不存在"},
	CodeReviewNotEligible:   {http.StatusConflict, "暂不可评价"},
	CodeReviewAlreadyExists: {http.StatusConflict, "该订单项已评价"},
	CodeReviewInvalidInput:  {http.StatusBadRequest, "评价内容非法"},

	CodeFavoriteProductUnavailable:    {http.StatusConflict, "商品不可收藏"},
	CodeLikeProductUnavailable:        {http.StatusConflict, "商品不可点赞"},
	CodeFlashSaleActivityNotFound:     {http.StatusNotFound, "秒杀活动不存在"},
	CodeFlashSaleNotInTimeWindow:      {http.StatusConflict, "秒杀活动不在进行中"},
	CodeFlashSaleStockInsufficient:    {http.StatusConflict, "秒杀库存不足"},
	CodeFlashSaleAlreadyPurchased:     {http.StatusConflict, "已参与该秒杀"},
	CodeFlashSaleIdempotencyConflict:  {http.StatusConflict, "幂等键冲突"},
	CodeFlashSaleSkuUnavailable:       {http.StatusConflict, "秒杀商品不可购买"},
	CodeFlashSaleInvalidArgument:      {http.StatusBadRequest, "秒杀活动参数非法"},
	CodeFlashSaleRequestNotRepairable: {http.StatusConflict, "秒杀请求不可修复"},
	CodeFlashSaleRateLimited:          {http.StatusTooManyRequests, "请求过于频繁，请稍后重试"},
	CodeFlashSaleQueueFull:            {http.StatusTooManyRequests, "秒杀排队已满，请稍后重试"},

	CodeBannerNotFound:     {http.StatusNotFound, "轮播图不存在"},
	CodeBannerInvalidInput: {http.StatusBadRequest, "轮播图参数非法"},

	CodeRecommendPositionNotFound:   {http.StatusNotFound, "推荐位不存在"},
	CodeRecommendPositionCodeExists: {http.StatusConflict, "推荐位标识已存在"},
	CodeRecommendInvalidInput:       {http.StatusBadRequest, "推荐位参数非法"},
	CodeRecommendItemNotFound:       {http.StatusNotFound, "推荐商品关系不存在"},
	CodeRecommendItemDuplicate:      {http.StatusConflict, "该商品已加入此推荐位"},
	CodeRecommendItemSortMismatch:   {http.StatusConflict, "排序商品列表未覆盖全部已加入商品"},

	CodeArticleNotFound:     {http.StatusNotFound, "文章不存在"},
	CodeArticleInvalidInput: {http.StatusBadRequest, "文章内容非法"},

	CodeUploadInvalidInput:  {http.StatusBadRequest, "文件类型或参数非法"},
	CodeUploadConfigInvalid: {http.StatusInternalServerError, "上传服务配置错误"},
	CodeUploadTokenFailed:   {http.StatusInternalServerError, "签发上传凭证失败"},

	CodeRefundNotFound:                {http.StatusNotFound, "退款申请不存在"},
	CodeRefundInvalidStatusTransition: {http.StatusConflict, "退款状态迁移非法"},
	CodeRefundOrderNotRefundable:      {http.StatusConflict, "订单不可退款"},
	CodeRefundAlreadyExists:           {http.StatusConflict, "退款申请已存在"},
	CodeRefundShipConflict:            {http.StatusConflict, "退款与发货冲突"},

	CodeCouponTemplateNotFound: {http.StatusNotFound, "优惠券模板不存在"},
	CodeCouponTemplateInvalid:  {http.StatusBadRequest, "优惠券参数非法"},
	CodeCouponNotFound:         {http.StatusNotFound, "优惠券不存在"},
	CodeCouponAlreadyClaimed:   {http.StatusConflict, "已领取该优惠券"},
	CodeCouponClaimUnavailable: {http.StatusConflict, "优惠券暂不可领取"},
	CodeCouponClaimSoldOut:     {http.StatusConflict, "优惠券已领完"},
	CodeCouponUnavailable:      {http.StatusConflict, "优惠券不可用"},

	CodeDistributionSelfReferral:        {http.StatusBadRequest, "不能绑定自己为推荐人"},
	CodeDistributionReferrerUnavailable: {http.StatusNotFound, "推荐人不存在或不可用"},
	CodeDistributionAlreadyBound:        {http.StatusConflict, "已绑定推荐人，不可变更"},
	CodeDistributionCycle:               {http.StatusConflict, "推荐关系成环"},
	CodeDistributionCommissionNotFound:  {http.StatusNotFound, "佣金记录不存在"},
	CodeDistributionCommissionStatus:    {http.StatusConflict, "佣金状态迁移非法"},

	CodeFreightTemplateNotFound:   {http.StatusNotFound, "运费模板不存在"},
	CodeFreightTemplateNameExists: {http.StatusConflict, "运费模板名称已存在"},
	CodeFreightTemplateInvalid:    {http.StatusBadRequest, "运费模板参数非法"},
	CodeFreightTemplateDisabled:   {http.StatusConflict, "运费模板已停用"},
	CodeFreightRuleNotFound:       {http.StatusNotFound, "运费区域规则不存在"},
	CodeFreightRuleDuplicate:      {http.StatusConflict, "该地区规则已存在"},
	CodeFreightRuleInvalid:        {http.StatusBadRequest, "运费规则参数非法"},
	CodeFreightRegionNotFound:     {http.StatusNotFound, "地区不存在"},
	CodeFreightRegionRuleMissing:  {http.StatusConflict, "该地区未配置运费规则"},
	CodePricingInvalidAmount:      {http.StatusBadRequest, "计价金额非法"},
	CodePricingDiscountExceeds:    {http.StatusBadRequest, "优惠金额超过可优惠金额"},
	CodePricingAmountOverflow:     {http.StatusBadRequest, "计价金额溢出"},

	CodeWalletAccountUnavailable:       {http.StatusInternalServerError, "钱包账户不可用"},
	CodeWalletInsufficientBalance:      {http.StatusConflict, "余额不足"},
	CodeWalletInvalidRechargeAmount:    {http.StatusBadRequest, "充值金额非法"},
	CodeWalletRechargeOrderNotFound:    {http.StatusNotFound, "充值订单不存在"},
	CodeWalletRechargeStatusNotAllowed: {http.StatusConflict, "充值订单状态不允许该操作"},
	CodeWalletDuplicatePayment:         {http.StatusConflict, "订单已支付"},
	CodeWalletRefundCreditFailed:       {http.StatusConflict, "退款入账失败"},
	CodeWalletIdempotencyConflict:      {http.StatusConflict, "资金流水幂等冲突"},
	CodeWalletMockRechargeDisabled:     {http.StatusForbidden, "Mock 充值未启用"},
	CodeWalletAmountOverflow:           {http.StatusConflict, "金额超出可表示范围"},
	CodeBargainActivityNotFound:        {http.StatusNotFound, "砍价活动不存在"},
	CodeBargainActivityInvalid:         {http.StatusBadRequest, "砍价活动参数非法"},
	CodeBargainActivityUnavailable:     {http.StatusConflict, "砍价活动暂不可参与"},
	CodeBargainInstanceNotFound:        {http.StatusNotFound, "砍价记录不存在"},
	CodeBargainInstanceExists:          {http.StatusConflict, "已有进行中的砍价"},
	CodeBargainSelfHelpForbidden:       {http.StatusConflict, "不能给自己砍价"},
	CodeBargainHelpUnavailable:         {http.StatusConflict, "该砍价不可助力"},
	CodeBargainHelpDuplicated:          {http.StatusConflict, "已助力过该砍价"},
	CodeBargainInvalidStatusTransition: {http.StatusConflict, "砍价状态迁移非法"},

	CodeMemberLevelNotFound:      {http.StatusNotFound, "会员等级不存在"},
	CodeMemberLevelInvalid:       {http.StatusBadRequest, "会员等级配置非法"},
	CodeMemberLevelInUse:         {http.StatusConflict, "会员等级已被用户使用，不能删除"},
	CodeMemberLevelDuplicate:     {http.StatusConflict, "会员等级序号或成长值门槛已存在"},
	CodeMemberGrowthInsufficient: {http.StatusConflict, "成长值不足"},

	CodeGroupBuyNotFound:         {http.StatusNotFound, "拼团活动不存在"},
	CodeGroupBuyInvalidArgument:  {http.StatusBadRequest, "拼团参数非法"},
	CodeGroupBuyActivityInactive: {http.StatusConflict, "拼团活动当前不可参与"},
	CodeGroupBuyPriceInvalid:     {http.StatusBadRequest, "拼团价格非法"},
	CodeGroupBuyTeamNotFound:     {http.StatusNotFound, "拼团不存在"},
	CodeGroupBuyTeamNotJoinable:  {http.StatusConflict, "该团已满或已结束，不可参与"},
	CodeGroupBuyAlreadyJoined:    {http.StatusConflict, "您已参与该团"},
	CodeGroupBuySkuUnavailable:   {http.StatusConflict, "拼团商品不可购买"},
	CodeGroupBuyTeamNotClosable:  {http.StatusConflict, "拼团状态不允许关闭"},
}

// HTTPStatus 返回业务错误码对应的 HTTP 状态码；未知码按 500 处理。
func HTTPStatus(code Code) int {
	if info, ok := codeTable[code]; ok {
		return info.HTTPStatus
	}
	return http.StatusInternalServerError
}

// Message 返回业务错误码对应的用户安全 message。
func Message(code Code) string {
	if info, ok := codeTable[code]; ok {
		return info.Message
	}
	return "内部错误"
}

// New 创建携带业务错误码的 error，对外 message 使用码表内安全文案。
func New(code Code) error {
	return gerror.NewCode(gcode.New(code, Message(code), nil))
}

// Wrap 用业务错误码包裹底层错误，保留原始错误链用于日志定位。
// 对外响应仅使用码表安全 message，不泄漏底层技术细节。
func Wrap(code Code, err error) error {
	return gerror.WrapCode(gcode.New(code, Message(code), nil), err)
}

// FromError 从 error 链中提取业务错误码；未识别时返回内部错误码。
//
// 特殊处理：GoFrame 参数校验（gvalid）失败时返回框架码 51
// （gcode.CodeValidationFailed），它不是项目的业务错误码，若原样透传会被
// HTTPStatus 当作未知码映射为 500。这里在统一错误层归一为业务码 1001
// （参数校验失败 → 400），不逐个 Controller 打补丁。
//
// 仅归一「参数校验」这一确定的框架语义，其余未知错误码仍按原值返回并由
// HTTPStatus 兜底为 500，避免把真正的内部错误误报为客户端参数错误。
func FromError(err error) Code {
	if err == nil {
		return CodeOK
	}
	c := gerror.Code(err)
	if c == gcode.CodeNil {
		return CodeInternalError
	}
	if c.Code() == gcode.CodeValidationFailed.Code() {
		return CodeInvalidArgument
	}
	return Code(c.Code())
}
