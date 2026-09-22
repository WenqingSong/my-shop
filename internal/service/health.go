package service

import (
	"context"

	v1 "cnb.cool/go-cloud-devops/my-shop/api/health/v1"
)

// IHealth defines the health check service.
type IHealth interface {
	// Liveness returns the service liveness status.
	Liveness(ctx context.Context) (*v1.LivenessRes, error)
}

var (
	localHealth IHealth
)

// Health returns the health check service implementation.
func Health() IHealth {
	if localHealth == nil {
		panic("implement not found for interface IHealth, forgot register?")
	}
	return localHealth
}

// RegisterHealth registers the health check service implementation.
func RegisterHealth(i IHealth) {
	localHealth = i
}
