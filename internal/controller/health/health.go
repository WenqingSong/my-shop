package health

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/health/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

// ControllerV1 implements the health check v1 API.
type ControllerV1 struct{}

// NewV1 creates and returns the health check v1 controller.
func NewV1() *ControllerV1 {
	return &ControllerV1{}
}

// Liveness handles the liveness probe request.
func (c *ControllerV1) Liveness(ctx context.Context, req *v1.LivenessReq) (res *v1.LivenessRes, err error) {
	return service.Health().Liveness(ctx)
}
