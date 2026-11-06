// 秒杀请求事件契约（RequestedEvent v1）。
//
// 该消息表达的是「某个已受理的秒杀请求需要被异步落单」，不是「订单已创建」，
// 更不是「库存/价格快照」。消费方必须根据 request_id 重新读取 MySQL 权威数据
// （请求状态、活动与 SKU 绑定、库存与已售数量）后再决定是否落单。
//
// 严禁把以下数据作为可信落单依据放进消息：
//   - 秒杀价格（flash_price）；
//   - 当前库存（total_stock / remaining）；
//   - 已售数量（sold）；
//   - Redis remaining。
// 这些值在消息投递期间可能已经变化，只能由消费者从 MySQL 重新读取。
//
// 事件标识的稳定性：event_id 由 request_id 确定性推导（flashsale.requested:<request_id>），
// 因此同一请求无论重发多少次、由哪条 Outbox 记录发布，event_id 都保持不变，
// 消费方可据此做幂等去重，而不会因为重发产生「新事件」。

package flashsale

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	// RequestedEventType 是秒杀请求事件的类型名，写入消息的 event_type 字段。
	RequestedEventType = "flashsale.order.requested"

	// RequestedSchemaVersion 是当前消息契约版本。结构不兼容变更必须升版本，
	// 消费方遇到不认识的版本必须拒绝处理，而不是按 v1 猜测。
	RequestedSchemaVersion = 1

	// requestedEventIDPrefix 是 event_id 的固定前缀，与 request_id 拼接后全局稳定。
	requestedEventIDPrefix = "flashsale.requested:"

	// maxIdempotencyKeyLength 与 flash_sale_order_requests.idempotency_key
	// 的 VARCHAR(64) 保持一致，避免消息里出现数据库无法存储的键。
	maxIdempotencyKeyLength = 64

	// requestHashHexLength 是 sha256 十六进制摘要长度。
	requestHashHexLength = 64
)

// RequestedEvent 是「秒杀下单请求已受理」事件 v1 的 JSON 契约。
//
// 字段全部来自 flash_sale_order_requests 的受理事实，不含任何价格/库存快照；
// JSON 键名是跨服务契约的一部分，重命名等同于破坏性变更。
type RequestedEvent struct {
	// EventID 是稳定事件标识：flashsale.requested:<request_id>，重发不变。
	EventID string `json:"event_id"`
	// EventType 固定为 flashsale.order.requested。
	EventType string `json:"event_type"`
	// SchemaVersion 固定为 1。
	SchemaVersion int `json:"schema_version"`
	// RequestID 是 MySQL flash_sale_order_requests.id，消费方据此读取权威请求状态。
	RequestID int64 `json:"request_id"`
	// UserID 是下单用户 ID。
	UserID int64 `json:"user_id"`
	// ActivityID 是秒杀活动 ID。
	ActivityID int64 `json:"activity_id"`
	// SkuID 是商品 SKU ID。
	SkuID int64 `json:"sku_id"`
	// IdempotencyKey 是业务幂等键（与 request 表唯一约束一致）。
	IdempotencyKey string `json:"idempotency_key"`
	// RequestHash 是请求指纹（sha256hex），用于识别「同键不同内容」的冲突。
	RequestHash string `json:"request_hash"`
	// OccurredAt 是事件发生时间（请求受理时间），保留时区信息。
	OccurredAt time.Time `json:"occurred_at"`
}

// RequestedEventInput 是构造 RequestedEvent 的业务输入。
// event_id/event_type/schema_version 由契约冻结，不接受调用方自填，避免出现不一致事件。
type RequestedEventInput struct {
	RequestID      int64
	UserID         int64
	ActivityID     int64
	SkuID          int64
	IdempotencyKey string
	RequestHash    string
	OccurredAt     time.Time
}

// RequestedEventID 返回 request_id 对应的稳定事件标识。
// 使用确定性推导而非随机 UUID，保证「同一请求重新发布 event_id 不变」。
func RequestedEventID(requestID int64) string {
	return fmt.Sprintf("%s%d", requestedEventIDPrefix, requestID)
}

// NewRequestedEvent 按 v1 契约构造事件。
// 本函数只做字段填充，不做校验；调用方应在发布前调用 Encode（内部会校验），
// 或在需要提前失败时显式调用 Validate。
func NewRequestedEvent(in RequestedEventInput) *RequestedEvent {
	return &RequestedEvent{
		EventID:        RequestedEventID(in.RequestID),
		EventType:      RequestedEventType,
		SchemaVersion:  RequestedSchemaVersion,
		RequestID:      in.RequestID,
		UserID:         in.UserID,
		ActivityID:     in.ActivityID,
		SkuID:          in.SkuID,
		IdempotencyKey: in.IdempotencyKey,
		RequestHash:    in.RequestHash,
		OccurredAt:     in.OccurredAt,
	}
}

// Encode 校验后把事件序列化为 JSON。
// 校验失败时不输出残缺消息，避免消费方收到「字段缺失但语法合法」的事件。
func (e RequestedEvent) Encode() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, fmt.Errorf("编码秒杀请求事件: %w", err)
	}
	data, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("编码秒杀请求事件: %w", err)
	}
	return data, nil
}

// DecodeRequestedEvent 解析并校验 v1 事件。
//
// 解析使用标准库 json.Unmarshal：未知字段被忽略，为同版本内的前向兼容留出空间；
// 但已知字段的类型错误、缺失必填字段、错误的版本/事件类型一律拒绝。
// 调用方必须把非 nil error 视为「消息不可处理」，走死信而不是猜测字段含义。
func DecodeRequestedEvent(data []byte) (*RequestedEvent, error) {
	var event RequestedEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, fmt.Errorf("解析秒杀请求事件: %w", err)
	}
	if err := event.Validate(); err != nil {
		return nil, fmt.Errorf("校验秒杀请求事件: %w", err)
	}
	return &event, nil
}

// Validate 校验 v1 事件的完整性、版本、类型与字段取值。
// 校验通过意味着消息可以安全地交给消费逻辑，但不意味着请求一定能落单——
// 业务终态仍由消费方读取 MySQL 权威数据后决定。
func (e RequestedEvent) Validate() error {
	if e.EventType != RequestedEventType {
		return fmt.Errorf("event_type 必须为 %q，实际为 %q", RequestedEventType, e.EventType)
	}
	if e.SchemaVersion != RequestedSchemaVersion {
		return fmt.Errorf("不支持的 schema_version %d（仅支持 %d）", e.SchemaVersion, RequestedSchemaVersion)
	}
	if e.RequestID <= 0 {
		return fmt.Errorf("request_id 必须为正整数，实际为 %d", e.RequestID)
	}
	if e.UserID <= 0 {
		return fmt.Errorf("user_id 必须为正整数，实际为 %d", e.UserID)
	}
	if e.ActivityID <= 0 {
		return fmt.Errorf("activity_id 必须为正整数，实际为 %d", e.ActivityID)
	}
	if e.SkuID <= 0 {
		return fmt.Errorf("sku_id 必须为正整数，实际为 %d", e.SkuID)
	}
	if expected := RequestedEventID(e.RequestID); e.EventID != expected {
		return fmt.Errorf("event_id 必须由 request_id 稳定推导：期望 %q，实际 %q", expected, e.EventID)
	}
	if err := validateIdempotencyKey(e.IdempotencyKey); err != nil {
		return err
	}
	if err := validateRequestHash(e.RequestHash); err != nil {
		return err
	}
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("occurred_at 不能为零值")
	}
	return nil
}

// validateIdempotencyKey 校验幂等键：非空、无首尾空白、长度与数据库列一致。
// 拒绝首尾空白而不是静默 Trim：消息中的键必须与 MySQL 中存储的键逐字节一致，
// 否则消费方按消息键查不到请求，或误判为另一个幂等键。
func validateIdempotencyKey(key string) error {
	if key == "" {
		return fmt.Errorf("idempotency_key 不能为空")
	}
	if key != strings.TrimSpace(key) {
		return fmt.Errorf("idempotency_key 不能包含首尾空白")
	}
	if len(key) > maxIdempotencyKeyLength {
		return fmt.Errorf("idempotency_key 长度 %d 超过上限 %d", len(key), maxIdempotencyKeyLength)
	}
	return nil
}

// validateRequestHash 校验请求指纹必须是 64 位小写 sha256 十六进制串。
// 现有秒杀链路使用 sha256hex(activity_id|sku_id) 生成指纹，契约保持同一形态；
// 大写或非十六进制字符意味着上游计算方式不一致，应当拒绝而不是规范化。
func validateRequestHash(hash string) error {
	if len(hash) != requestHashHexLength {
		return fmt.Errorf("request_hash 必须是 %d 位 sha256 十六进制串，实际长度 %d", requestHashHexLength, len(hash))
	}
	if strings.ToLower(hash) != hash {
		return fmt.Errorf("request_hash 必须为小写十六进制串")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return fmt.Errorf("request_hash 不是合法的十六进制串: %w", err)
	}
	return nil
}
