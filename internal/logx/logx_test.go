package logx

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

// TestRenderFields 验证结构化日志序列化契约：event、message 与调用方字段正确输出，
// 空上下文不注入 request_id，且输出为合法 JSON。
func TestRenderFields(t *testing.T) {
	got, err := render(context.Background(), "order_created", "下单成功", Fields{
		"user_id":  int64(42),
		"order_id": int64(7),
	})
	if err != nil {
		t.Fatalf("render 返回错误: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("render 输出非合法 JSON: %v", err)
	}
	if m["event"] != "order_created" {
		t.Errorf("event = %v, 期望 order_created", m["event"])
	}
	if m["message"] != "下单成功" {
		t.Errorf("message = %v, 期望 下单成功", m["message"])
	}
	if m["user_id"] != float64(42) {
		t.Errorf("user_id = %v, 期望 42", m["user_id"])
	}
	if m["order_id"] != float64(7) {
		t.Errorf("order_id = %v, 期望 7", m["order_id"])
	}
	if _, ok := m["request_id"]; ok {
		t.Errorf("空上下文不应注入 request_id 字段")
	}
}

// TestRenderEmptyMessage 验证空 message 时省略该字段，避免无意义空串噪声。
func TestRenderEmptyMessage(t *testing.T) {
	got, err := render(context.Background(), "order_paid", "", Fields{"order_id": int64(1)})
	if err != nil {
		t.Fatalf("render 返回错误: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatalf("render 输出非合法 JSON: %v", err)
	}
	if _, ok := m["message"]; ok {
		t.Errorf("空 message 不应输出 message 字段")
	}
}

// TestEmitInvokesLogger 验证 emit 把渲染后的单行 JSON 原样交给 logger；
// 字段无法序列化时仅降级为不记录，不得 panic，也不能把错误暴露给业务调用方。
func TestEmitInvokesLogger(t *testing.T) {
	var lines []string
	logger := func(_ context.Context, v ...any) {
		if len(v) != 1 {
			t.Fatalf("logger 收到 %d 个参数，期望 1", len(v))
		}
		lines = append(lines, fmt.Sprint(v[0]))
	}

	emit(logger, context.Background(), "order_created", "下单成功", Fields{"order_id": int64(7)})
	if len(lines) != 1 {
		t.Fatalf("emit 调用 logger %d 次，期望 1", len(lines))
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatalf("emit 输出非合法 JSON: %v", err)
	}
	if m["event"] != "order_created" {
		t.Errorf("event = %v, 期望 order_created", m["event"])
	}
	if m["order_id"] != float64(7) {
		t.Errorf("order_id = %v, 期望 7", m["order_id"])
	}

	// chan 不可 JSON 序列化：必须静默降级，不调用 logger、不 panic。
	emit(logger, context.Background(), "bad_event", "", Fields{"ch": make(chan int)})
	if len(lines) != 1 {
		t.Fatalf("序列化失败时不应调用 logger，实际调用 %d 次", len(lines))
	}
}
