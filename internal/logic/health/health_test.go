package health

import (
	"context"
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/internal/service"
)

func TestLiveness(t *testing.T) {
	ctx := context.Background()
	res, err := service.Health().Liveness(ctx)
	if err != nil {
		t.Fatalf("Liveness returned error: %v", err)
	}
	if res == nil {
		t.Fatal("Liveness returned nil result")
	}
	if res.Status != "ok" {
		t.Fatalf("expected status %q, got %q", "ok", res.Status)
	}
	if res.Time == "" {
		t.Fatal("expected non-empty time")
	}
}
