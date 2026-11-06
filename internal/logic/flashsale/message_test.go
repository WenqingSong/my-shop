package flashsale

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// requestedEventFixture 返回一个合法 v1 事件及其期望字段，供各用例复用。
func requestedEventFixture(t *testing.T) *RequestedEvent {
	t.Helper()
	occurredAt, err := time.Parse(time.RFC3339, "2026-11-06T10:00:00+08:00")
	if err != nil {
		t.Fatalf("构造固定时间: %v", err)
	}
	return NewRequestedEvent(RequestedEventInput{
		RequestID:      123456,
		UserID:         789,
		ActivityID:     42,
		SkuID:          1001,
		IdempotencyKey: "client-key",
		RequestHash:    strings.Repeat("a", 64),
		OccurredAt:     occurredAt,
	})
}

// 正常 Encode/Decode 往返：字段完整、时间与类型保持不变。
func TestRequestedEventEncodeDecode(t *testing.T) {
	event := requestedEventFixture(t)
	data, err := event.Encode()
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}
	decoded, err := DecodeRequestedEvent(data)
	if err != nil {
		t.Fatalf("Decode 失败: %v", err)
	}

	if decoded.EventID != "flashsale.requested:123456" {
		t.Fatalf("event_id = %q", decoded.EventID)
	}
	if decoded.EventType != RequestedEventType {
		t.Fatalf("event_type = %q", decoded.EventType)
	}
	if decoded.SchemaVersion != RequestedSchemaVersion {
		t.Fatalf("schema_version = %d", decoded.SchemaVersion)
	}
	if decoded.RequestID != 123456 || decoded.UserID != 789 || decoded.ActivityID != 42 || decoded.SkuID != 1001 {
		t.Fatalf("ID 字段往返不一致: %+v", decoded)
	}
	if decoded.IdempotencyKey != "client-key" {
		t.Fatalf("idempotency_key = %q", decoded.IdempotencyKey)
	}
	if decoded.RequestHash != strings.Repeat("a", 64) {
		t.Fatalf("request_hash = %q", decoded.RequestHash)
	}
	if !decoded.OccurredAt.Equal(event.OccurredAt) {
		t.Fatalf("occurred_at = %v, want %v", decoded.OccurredAt, event.OccurredAt)
	}
	// 逐字节再编码应稳定：同一消息重复序列化不产生差异。
	again, err := decoded.Encode()
	if err != nil {
		t.Fatalf("再次 Encode 失败: %v", err)
	}
	if string(again) != string(data) {
		t.Fatalf("重复 Encode 结果不稳定:\n first=%s\nsecond=%s", data, again)
	}
}

// JSON 键集合是跨服务契约的一部分：必须与 v1 定义完全一致，
// 尤其不得出现秒杀价格、库存、已售数量、Redis remaining 等字段。
func TestRequestedEventJSONFieldsAreFrozen(t *testing.T) {
	data, err := requestedEventFixture(t).Encode()
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("解析 JSON: %v", err)
	}

	want := []string{
		"event_id", "event_type", "schema_version", "request_id", "user_id",
		"activity_id", "sku_id", "idempotency_key", "request_hash", "occurred_at",
	}
	if len(raw) != len(want) {
		t.Fatalf("JSON 字段数量 = %d, want %d: %s", len(raw), len(want), data)
	}
	for _, key := range want {
		if _, ok := raw[key]; !ok {
			t.Fatalf("缺少契约字段 %q: %s", key, data)
		}
	}
	for _, forbidden := range []string{"flash_price", "total_stock", "remaining", "sold"} {
		if _, ok := raw[forbidden]; ok {
			t.Fatalf("消息中不得出现权威数据字段 %q: %s", forbidden, data)
		}
	}
}

// event_id 必须由 request_id 确定性推导：同一请求重复构造、重复编解码都不变化。
func TestRequestedEventIDStable(t *testing.T) {
	first := requestedEventFixture(t)
	// 其余字段变化（例如重试时重新读取时间/指纹）不应影响 event_id。
	second := NewRequestedEvent(RequestedEventInput{
		RequestID:      first.RequestID,
		UserID:         first.UserID,
		ActivityID:     first.ActivityID,
		SkuID:          first.SkuID,
		IdempotencyKey: first.IdempotencyKey,
		RequestHash:    first.RequestHash,
		OccurredAt:     first.OccurredAt.Add(time.Minute),
	})
	if first.EventID != second.EventID {
		t.Fatalf("同一 request_id 的 event_id 不稳定: %q vs %q", first.EventID, second.EventID)
	}

	data, err := second.Encode()
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}
	decoded, err := DecodeRequestedEvent(data)
	if err != nil {
		t.Fatalf("Decode 失败: %v", err)
	}
	if decoded.EventID != first.EventID {
		t.Fatalf("编解码后 event_id 发生变化: %q vs %q", decoded.EventID, first.EventID)
	}
	if RequestedEventID(123456) != "flashsale.requested:123456" {
		t.Fatalf("RequestedEventID 格式不符合契约: %q", RequestedEventID(123456))
	}
}

// 非法 JSON（语法错误、尾随内容、类型错误）必须拒绝。
func TestRequestedEventDecodeRejectsInvalidJSON(t *testing.T) {
	cases := map[string]string{
		"截断":   `{"event_id":`,
		"空内容":  ``,
		"尾随内容": `{"event_id":"flashsale.requested:1"} trailing`,
		"类型错误": `{"request_id":"not-a-number"}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeRequestedEvent([]byte(payload)); err == nil {
				t.Fatalf("非法 JSON 应被拒绝: %q", payload)
			}
		})
	}
}

// 字段/版本/类型校验：任何一项不满足都必须拒绝，并指出具体字段。
func TestRequestedEventValidateRejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*RequestedEvent)
		wantSub string
	}{
		{name: "不支持的版本", mutate: func(e *RequestedEvent) { e.SchemaVersion = 2 }, wantSub: "schema_version"},
		{name: "错误的事件类型", mutate: func(e *RequestedEvent) { e.EventType = "flashsale.order.created" }, wantSub: "event_type"},
		{name: "缺失 request_id", mutate: func(e *RequestedEvent) { e.RequestID = 0 }, wantSub: "request_id"},
		{name: "非法 request_id", mutate: func(e *RequestedEvent) { e.RequestID = -1 }, wantSub: "request_id"},
		{name: "非法 user_id", mutate: func(e *RequestedEvent) { e.UserID = 0 }, wantSub: "user_id"},
		{name: "非法 activity_id", mutate: func(e *RequestedEvent) { e.ActivityID = -5 }, wantSub: "activity_id"},
		{name: "非法 sku_id", mutate: func(e *RequestedEvent) { e.SkuID = 0 }, wantSub: "sku_id"},
		{name: "event_id 与 request_id 不一致", mutate: func(e *RequestedEvent) { e.EventID = "flashsale.requested:999" }, wantSub: "event_id"},
		{name: "缺失幂等键", mutate: func(e *RequestedEvent) { e.IdempotencyKey = "" }, wantSub: "idempotency_key"},
		{name: "幂等键含首尾空白", mutate: func(e *RequestedEvent) { e.IdempotencyKey = " key " }, wantSub: "idempotency_key"},
		{name: "幂等键超长", mutate: func(e *RequestedEvent) { e.IdempotencyKey = strings.Repeat("k", maxIdempotencyKeyLength+1) }, wantSub: "idempotency_key"},
		{name: "指纹长度错误", mutate: func(e *RequestedEvent) { e.RequestHash = "abc" }, wantSub: "request_hash"},
		{name: "指纹含大写", mutate: func(e *RequestedEvent) { e.RequestHash = strings.Repeat("A", 64) }, wantSub: "request_hash"},
		{name: "指纹非十六进制", mutate: func(e *RequestedEvent) { e.RequestHash = strings.Repeat("z", 64) }, wantSub: "request_hash"},
		{name: "零值发生时间", mutate: func(e *RequestedEvent) { e.OccurredAt = time.Time{} }, wantSub: "occurred_at"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event := *requestedEventFixture(t)
			tc.mutate(&event)
			err := event.Validate()
			if err == nil {
				t.Fatal("非法事件应被拒绝")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("错误信息应指出 %s，实际: %v", tc.wantSub, err)
			}
			if _, encodeErr := event.Encode(); encodeErr == nil {
				t.Fatal("非法事件不应被 Encode 输出")
			}
		})
	}
}

// 解码时允许同版本新增未知字段（前向兼容），但已知字段仍必须通过校验。
func TestRequestedEventDecodeIgnoresUnknownFields(t *testing.T) {
	event := requestedEventFixture(t)
	data, err := event.Encode()
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("解析 JSON: %v", err)
	}
	payload["trace_id"] = "trace-1"
	withExtra, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("重新编码: %v", err)
	}
	decoded, err := DecodeRequestedEvent(withExtra)
	if err != nil {
		t.Fatalf("未知字段应被忽略: %v", err)
	}
	if decoded.RequestID != event.RequestID {
		t.Fatalf("已知字段解析错误: %+v", decoded)
	}
}
