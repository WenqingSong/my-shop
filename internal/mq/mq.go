// Package mq 定义与具体消息中间件实现解耦的通用消息契约：消息载体、发布端、消费端，
// 以及处理器返回的处置结果（ACK / Retry / DeadLetter）。
//
// 本包只冻结接口与语义边界，不包含 Kafka、数据库或秒杀业务实现：
//   - 不依赖 internal/logic/flashsale，避免业务语义反向污染通用层，也便于未来替换 MQ；
//   - 不提供消费循环、重试调度、死信落库、数据库事务编排，这些属于后续消费适配层的职责；
//   - 具体适配器（如 internal/mq/kafka）只负责把本包契约映射到驱动 API，不改变语义。
//
// 设计约束：
//   - 所有外部 I/O 以 context.Context 贯穿，由调用方决定超时与取消；
//   - Key/Value 使用 []byte，通用层不绑定 JSON/Protobuf 等序列化方式；
//   - 发布结果按条返回，部分失败不得聚合成「整批成功」；
//   - 消费端不自动提交位移，提交时机由上层在业务副作用持久化后显式决定。
package mq

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrClosed 表示消息客户端已关闭。适配器在关闭后收到发布/拉取/提交调用时应包装该错误，
// 使调用方可用 errors.Is 区分「客户端生命周期结束」与「单次 I/O 失败」。
var ErrClosed = errors.New("mq: 客户端已关闭")

// Header 是消息头键值对。Kafka 等实现原样透传，不解释 Value 的编码；
// 约定同一个消息内的 Header Key 唯一（由发布方保证），消费方按 Key 查找。
type Header struct {
	Key   string
	Value []byte
}

// Message 是一条待发布或已消费的消息。
//
// Topic/Partition/Offset/Timestamp 是传输层元数据：
//   - 发布时 Topic 可留空（由 Publisher 使用其默认 Topic），Partition/Offset 由 Broker 分配；
//   - 消费时由适配器填充，Commit 依赖 Topic/Partition/Offset 定位位移。
//
// 通用层不限制 Value 的序列化格式，也不携带价格、库存等业务字段；
// 业务消息契约由各自的业务包定义（例如秒杀 RequestedEvent v1）。
type Message struct {
	// Topic 是消息所属主题。发布时留空表示使用 Publisher 的默认 Topic。
	Topic string
	// Key 是分区键：相同 Key 的消息在同一分区内保持顺序，也是业务侧常用的聚合标识。
	Key []byte
	// Value 是消息体（通常是 JSON 字节）。
	Value []byte
	// Headers 是附加消息头，用于事件类型、追踪信息等非业务载荷元数据。
	Headers []Header
	// Partition 是消息所在分区；发布前无意义，消费时由 Broker 提供。
	Partition int32
	// Offset 是消息在分区内的位移；发布前无意义，消费时由 Broker 提供，手动提交位移时使用。
	Offset int64
	// Timestamp 是消息时间戳。发布时为零值表示由驱动在发送时填充（当前时间）。
	Timestamp time.Time
}

// HeaderValue 返回指定 Key 的第一个消息头值；不存在时返回 nil, false。
func (m Message) HeaderValue(key string) ([]byte, bool) {
	for _, h := range m.Headers {
		if h.Key == key {
			return h.Value, true
		}
	}
	return nil, false
}

// PublishResult 描述单条消息的发布结果。
//
// Err 非 nil 表示该条消息未被 Broker 确认（网络错误、超时、重试耗尽、单条过大等）。
// 即使同批次其他消息成功，该条也绝不能被调用方视为已投递。
type PublishResult struct {
	// Message 是发布时实际使用的消息副本（Topic 已按默认值补全）。
	Message Message
	// Partition 是 Broker 分配的分区；失败时为 0。
	Partition int32
	// Offset 是 Broker 分配的位移；失败时为 0。
	Offset int64
	// Err 是该条消息的发布错误；nil 表示已获得 Broker 确认（acks=all 语义由适配器保证）。
	Err error
}

// Publisher 是消息发布端契约。
//
// Publish 是同步确认语义：返回时本批次每条消息都已有结果（确认或失败），
// 调用方无需再等待回调。适配器应保证 results 长度与入参一致（除非返回批次级错误）。
//
// 发布失败语义：
//   - 返回 error 且 results 为 nil：批次未进入投递流程（客户端已关闭、context 已取消等），
//     整批结果未知，调用方不得把任何一条消息标记为已发布；
//   - 返回 error 且 results 非 nil：至少一条消息失败，results 中保留逐条错误；
//   - 返回 nil：全部消息均已获得 Broker 确认。
//
// at-least-once 的业务语义（例如 Outbox 标记已发布）不在此接口内实现，
// 由调用方结合自身持久化事实决定重发策略。
type Publisher interface {
	// Publish 同步发布一批消息，按入参顺序返回逐条结果。
	// 单条消息失败不应中断同批次其他消息的投递（除非 context 结束）。
	Publish(ctx context.Context, messages ...Message) ([]PublishResult, error)
	// Close 停止接收新消息、尽力刷新在途消息并释放资源；可重复调用。
	Close(ctx context.Context) error
}

// Consumer 是消息消费端的基础契约。
//
// 本接口只描述「拉取」与「手动提交位移」能力，不包含消费循环：
// 何时 ACK、如何退避重试、失败如何进入死信、数据库事务与位移提交的先后关系，
// 必须由上层消费适配器在业务语义明确后实现（见 Handler/Disposition 注释）。
//
// 实现约束：
//   - Poll 阻塞等待消息，context 取消或客户端关闭时必须及时返回；
//   - Commit 只提交调用方传入消息的位移，适配器不得自动提交；
//   - 允许重复投递（at-least-once），业务侧必须自行幂等。
type Consumer interface {
	// Poll 拉取至多 maxMessages 条消息。maxMessages<=0 时使用实现默认值。
	// 返回空切片且 error 为 nil 表示本轮没有可交付消息（实现可选择阻塞等待）。
	Poll(ctx context.Context, maxMessages int) ([]Message, error)
	// Commit 手动提交消息位移。调用方只能在消息对应的业务副作用持久化后调用；
	// 只传入已成功处理的消息，未传入的消息在进程重启后会被重新投递。
	Commit(ctx context.Context, messages ...Message) error
	// Close 停止拉取、尽力提交/离开消费组并释放资源；可重复调用。
	Close(ctx context.Context) error
}

// Disposition 是 Handler 对单条消息的处置结论，界定 ACK / Retry / DeadLetter 的语义边界。
//
// 本包只定义语义，不实现执行动作；消费循环（后续批次）负责把结论转换为：
// 位移提交、退避重投、写入死信主题等具体操作。
type Disposition int

const (
	// DispositionAck：业务处理成功且副作用已持久化，允许提交该消息位移。
	// 若进程在提交前崩溃，消息会被重复投递，业务必须幂等。
	DispositionAck Disposition = iota + 1

	// DispositionRetry：可重试失败（如数据库短暂不可用），本批次不得提交该消息位移，
	// 由上层在有限次数、有界退避后重新处理；超过重试预算必须升级为 DeadLetter，
	// 不允许无限重试或无限阻塞分区。
	DispositionRetry

	// DispositionDeadLetter：不可重试失败或重试预算耗尽，消息应写入死信主题并留存失败原因；
	// 死信写入成功后才允许提交原消息位移，避免「已提交但未留痕」。死信本身不重放业务写入。
	DispositionDeadLetter
)

// String 返回便于日志与指标展示的稳定小写名称，避免日志里出现裸数字。
func (d Disposition) String() string {
	switch d {
	case DispositionAck:
		return "ack"
	case DispositionRetry:
		return "retry"
	case DispositionDeadLetter:
		return "dead_letter"
	default:
		return fmt.Sprintf("unknown(%d)", int(d))
	}
}

// Handler 处理单条消息并给出处置结论。
//
// error 仅用于记录失败原因（日志/死信载荷），不改变 Disposition 的执行语义：
// 返回 DispositionAck 时 error 必须为 nil；返回 Retry/DeadLetter 时应携带具体错误。
// 消费循环不得根据 error 自行猜测处置方式，避免把「业务终态失败」误判为「可重试」。
type Handler func(ctx context.Context, msg Message) (Disposition, error)
