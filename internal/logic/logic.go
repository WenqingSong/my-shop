// Package logic aggregates all business logic implementations.
// Importing this package (as a blank import) triggers the init functions of
// each logic sub-package, which register their services.
package logic

import (
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/address"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/admin"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/article"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/banner"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/bargain"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/cart"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/categories"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/coupon"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/dashboard"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/distribution"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/favorite"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/flashsale"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/freight"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/health"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/iam"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/inventory"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/like"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/member"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/order"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/product"
	// B1-04-01 BEGIN：用户资料与地区字典
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/profile"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/region"
	// B1-04-01 END
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/recommendation"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/refund"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/review"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/sku"
	// B1-04-02 BEGIN：商品规格组/规格值与 SKU 组合
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/spec"
	// B1-04-02 END
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/upload"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/wallet"
)
