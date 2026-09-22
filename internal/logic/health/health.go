package health

import (
	"context"
	"time"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/health/v1"
	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

type sHealth struct{}

func init() {
	service.RegisterHealth(New())
}

// New creates and returns the health check service implementation.
func New() *sHealth {
	return &sHealth{}
}

// Liveness returns the service liveness status.
func (s *sHealth) Liveness(ctx context.Context) (*v1.LivenessRes, error) {
	return &v1.LivenessRes{
		Status: "ok",
		Time:   time.Now().Format("2006-01-02 15:04:05"),
	}, nil
}
