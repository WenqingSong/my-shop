package codes_test

import (
	"errors"
	"testing"

	"github.com/gogf/gf/v2/errors/gcode"
	"github.com/gogf/gf/v2/errors/gerror"

	"cnb.cool/go-cloud-devops/my-shop/internal/codes"
)

// TestFromErrorMapsValidationFailedToInvalidArgument 锁定 D1 修复：
// GoFrame gvalid 的框架码 51 必须归一为业务码 1001（HTTP 400），
// 而真正的内部错误仍为 1000（HTTP 500）。
func TestFromErrorMapsValidationFailedToInvalidArgument(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"nil", nil, codes.CodeOK},
		{"gvalid-51", gerror.NewCode(gcode.CodeValidationFailed, "validation failed"), codes.CodeInvalidArgument},
		{"gvalid-51-wrapped", gerror.WrapCode(gcode.CodeValidationFailed, errors.New("field required")), codes.CodeInvalidArgument},
		{"business-code", codes.New(codes.CodeOrderIdempotencyConflict), codes.CodeOrderIdempotencyConflict},
		{"plain-error", gerror.New("boom"), codes.CodeInternalError},
		{"framework-nil", gerror.NewCode(gcode.CodeNil, "nil code"), codes.CodeInternalError},
	}
	for _, tc := range cases {
		if got := codes.FromError(tc.err); got != tc.want {
			t.Errorf("%s: FromError = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestHTTPStatusKeepsUnknownAs500 防止把未知错误误映射为 400/1001。
func TestHTTPStatusKeepsUnknownAs500(t *testing.T) {
	if got := codes.HTTPStatus(codes.CodeInvalidArgument); got != 400 {
		t.Errorf("HTTPStatus(1001) = %d, want 400", got)
	}
	if got := codes.HTTPStatus(codes.CodeInternalError); got != 500 {
		t.Errorf("HTTPStatus(1000) = %d, want 500", got)
	}
	if got := codes.HTTPStatus(codes.Code(999999)); got != 500 {
		t.Errorf("HTTPStatus(unknown) = %d, want 500", got)
	}
}
