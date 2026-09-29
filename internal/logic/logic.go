// Package logic aggregates all business logic implementations.
// Importing this package (as a blank import) triggers the init functions of
// each logic sub-package, which register their services.
package logic

import (
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/admin"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/categories"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/health"
	_ "cnb.cool/go-cloud-devops/my-shop/internal/logic/iam"
)
