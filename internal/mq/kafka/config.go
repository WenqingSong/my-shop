// Package kafka 提供 SurgeCart 的 Kafka 基础设施适配：
//
//   - config.go：从 GoFrame 配置中心读取并校验 Kafka 配置；
//   - producer.go：基于 franz-go 的同步确认 Producer；
//   - consumer.go：基于 franz-go 的 Consumer Group 基础客户端（手动提交位移、禁止自动提交）。
//
// 本包只负责「可靠地收发消息」，不包含任何业务语义：不建单、不扣库存、不写业务表，
// 也不实现消费循环、重试调度与死信处理。秒杀请求事件契约位于
// internal/logic/flashsale/message.go，业务消费编排属于后续批次。
package kafka

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/gogf/gf/v2/container/gvar"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
	"github.com/gogf/gf/v2/os/genv"
)

// GoFrame 配置项路径。环境变量按 GoFrame 规则由「大写 + 点替换为下划线」映射，
// 例如 flash_sale.kafka.brokers 对应 FLASH_SALE_KAFKA_BROKERS。
// 覆盖行为见 lookup：环境变量优先于配置文件，并由 config_test.go 用真实配置对象验证。
const (
	configKeyEnabled       = "flash_sale.kafka.enabled"
	configKeyBrokers       = "flash_sale.kafka.brokers"
	configKeyTopic         = "flash_sale.kafka.topic"
	configKeyDLQTopic      = "flash_sale.kafka.dlq_topic"
	configKeyConsumerGroup = "flash_sale.kafka.consumer_group"
)

const (
	// maxTopicNameLength 是 Kafka 对 Topic 名的长度上限（Kafka 自身校验规则）。
	maxTopicNameLength = 249
	// maxConsumerGroupLength 是本适配器接受的消费组名长度上限，避免误把整段配置粘进组名。
	maxConsumerGroupLength = 255
)

// Config 是校验后的 Kafka 配置，也是 Producer/Consumer 构造函数的输入。
//
// 该结构应由 Parse（配置文件原始值）或 Load（GoFrame 配置中心）构造，
// 以保证 Enabled=false 时不要求 Broker/Topic 完整、Enabled=true 时关键项齐全；
// 构造函数会再次调用 Validate，防御手工拼装的配置绕过校验。
type Config struct {
	// Enabled 是 Kafka 总开关。false 时业务继续走既有 MySQL 出队表路径，
	// B1 不实现任何模式切换逻辑，仅保留开关语义。
	Enabled bool
	// Brokers 是 Broker 地址列表（host:port），已去除首尾空白并按配置顺序保留。
	Brokers []string
	// Topic 是秒杀请求事件主题。
	Topic string
	// DLQTopic 是死信主题；必须与 Topic 不同，避免失败消息回灌正常链路。
	DLQTopic string
	// ConsumerGroup 是消费组名；同组实例分摊分区，跨组互相独立。
	ConsumerGroup string
}

// RawConfig 是配置文件的原始形态：Brokers 为逗号分隔字符串，与 config.yaml 一一对应。
// 单独暴露该结构是为了让校验逻辑可脱离 GoFrame 配置中心做纯单元测试。
type RawConfig struct {
	Enabled       bool
	Brokers       string
	Topic         string
	DLQTopic      string
	ConsumerGroup string
}

// Parse 将配置原始值规范化并校验为 Config。
//
// 规范化规则：Brokers 按逗号拆分、逐项去空格；Topic/DLQTopic/ConsumerGroup 去首尾空格。
// 校验规则：
//   - Enabled=false：允许 Broker/Topic/消费组全部为空（关闭 Kafka 不应阻塞启动），
//     但只要填写了值就必须合法，避免「以为关闭了、实际配错了」；
//   - Enabled=true：Broker 至少一个、Topic/DLQTopic/ConsumerGroup 必填，
//     Broker 不得为空地址/重复/缺少端口，DLQTopic 不得与 Topic 相同。
func Parse(raw RawConfig) (Config, error) {
	brokers, err := parseBrokers(raw.Brokers)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		Enabled:       raw.Enabled,
		Brokers:       brokers,
		Topic:         strings.TrimSpace(raw.Topic),
		DLQTopic:      strings.TrimSpace(raw.DLQTopic),
		ConsumerGroup: strings.TrimSpace(raw.ConsumerGroup),
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Load 从 GoFrame 默认配置中心读取 flash_sale.kafka 节点并解析。
func Load(ctx context.Context) (Config, error) {
	return LoadFrom(ctx, g.Cfg())
}

// LoadFrom 从指定 GoFrame 配置对象读取 flash_sale.kafka 节点并解析。
// 抽成显式入参是为了在测试中注入隔离的配置对象，避免修改进程级全局配置。
func LoadFrom(ctx context.Context, cfg *gcfg.Config) (Config, error) {
	if cfg == nil {
		return Config{}, fmt.Errorf("读取 Kafka 配置: 配置中心未初始化")
	}
	enabled, err := readBool(ctx, cfg, configKeyEnabled)
	if err != nil {
		return Config{}, err
	}
	brokers, err := readString(ctx, cfg, configKeyBrokers)
	if err != nil {
		return Config{}, err
	}
	topic, err := readString(ctx, cfg, configKeyTopic)
	if err != nil {
		return Config{}, err
	}
	dlqTopic, err := readString(ctx, cfg, configKeyDLQTopic)
	if err != nil {
		return Config{}, err
	}
	group, err := readString(ctx, cfg, configKeyConsumerGroup)
	if err != nil {
		return Config{}, err
	}
	return Parse(RawConfig{
		Enabled:       enabled,
		Brokers:       brokers,
		Topic:         topic,
		DLQTopic:      dlqTopic,
		ConsumerGroup: group,
	})
}

// Validate 校验配置完整性。Parse 已调用本方法；构造函数再次调用以防御手工构造的 Config，
// 使「配置不合法」在创建客户端之前就明确失败，而不是等到第一次收发消息才暴露。
func (c Config) Validate() error {
	if err := validateBrokers(c.Brokers); err != nil {
		return err
	}
	if !c.Enabled {
		// 关闭状态下只校验已填写的值：未启用的 Kafka 不应要求主题和消费组齐全。
		if c.Topic != "" {
			if err := validateTopicName(configKeyTopic, c.Topic); err != nil {
				return err
			}
		}
		if c.DLQTopic != "" {
			if err := validateTopicName(configKeyDLQTopic, c.DLQTopic); err != nil {
				return err
			}
			if c.Topic != "" && c.DLQTopic == c.Topic {
				return fmt.Errorf("%s 不能与 %s 相同（%q），否则失败消息会回灌正常链路", configKeyDLQTopic, configKeyTopic, c.Topic)
			}
		}
		if c.ConsumerGroup != "" {
			return validateConsumerGroup(c.ConsumerGroup)
		}
		return nil
	}

	if len(c.Brokers) == 0 {
		return fmt.Errorf("%s: 启用 Kafka 时至少需要配置一个 Broker", configKeyBrokers)
	}
	if c.Topic == "" {
		return fmt.Errorf("%s: 启用 Kafka 时不能为空", configKeyTopic)
	}
	if err := validateTopicName(configKeyTopic, c.Topic); err != nil {
		return err
	}
	if c.DLQTopic == "" {
		return fmt.Errorf("%s: 启用 Kafka 时不能为空", configKeyDLQTopic)
	}
	if err := validateTopicName(configKeyDLQTopic, c.DLQTopic); err != nil {
		return err
	}
	if c.DLQTopic == c.Topic {
		return fmt.Errorf("%s 不能与 %s 相同（%q），否则失败消息会回灌正常链路", configKeyDLQTopic, configKeyTopic, c.Topic)
	}
	if c.ConsumerGroup == "" {
		return fmt.Errorf("%s: 启用 Kafka 时不能为空", configKeyConsumerGroup)
	}
	return validateConsumerGroup(c.ConsumerGroup)
}

// parseBrokers 按逗号拆分 Broker 字符串并逐项去空格。
// 整个字符串为空表示「未配置 Broker」，返回 nil；存在空项（如 "a:1,,b:2" 或 "a:1,"）
// 则视为配置错误，避免把空地址静默丢弃后以「少一个 Broker」的状态运行。
func parseBrokers(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	brokers := make([]string, 0, len(parts))
	for i, part := range parts {
		broker := strings.TrimSpace(part)
		if broker == "" {
			return nil, fmt.Errorf("%s: 第 %d 个 Broker 为空地址（逗号分隔的列表不能包含空项）", configKeyBrokers, i+1)
		}
		brokers = append(brokers, broker)
	}
	if err := validateBrokers(brokers); err != nil {
		return nil, err
	}
	return brokers, nil
}

// validateBrokers 校验 Broker 列表：非空、host:port 合法、端口范围正确、无重复。
// 重复判断忽略大小写（DNS 主机名大小写不敏感），并在错误中点明重复项与位置。
func validateBrokers(brokers []string) error {
	seen := make(map[string]int, len(brokers))
	for i, broker := range brokers {
		if broker != strings.TrimSpace(broker) {
			return fmt.Errorf("%s: 第 %d 个 Broker %q 含首尾空白", configKeyBrokers, i+1, broker)
		}
		if broker == "" {
			return fmt.Errorf("%s: 第 %d 个 Broker 为空地址", configKeyBrokers, i+1)
		}
		if err := validateBrokerAddr(broker); err != nil {
			return fmt.Errorf("%s: 第 %d 个 Broker %q 无效: %w", configKeyBrokers, i+1, broker, err)
		}
		key := strings.ToLower(broker)
		if first, ok := seen[key]; ok {
			return fmt.Errorf("%s: 第 %d 个 Broker %q 与第 %d 个重复", configKeyBrokers, i+1, broker, first+1)
		}
		seen[key] = i
	}
	return nil
}

// validateBrokerAddr 校验单个 Broker 是否为合法的 host:port。
// 使用 net.SplitHostPort 同时覆盖 IPv4、IPv6（[::1]:9092）与主机名，
// 并拒绝带 scheme 的值（如 http://host:9092），因为 Kafka 客户端只接受纯地址。
func validateBrokerAddr(broker string) error {
	host, portText, err := net.SplitHostPort(broker)
	if err != nil {
		return fmt.Errorf("应为 host:port 形式: %w", err)
	}
	if host == "" {
		return fmt.Errorf("缺少主机名")
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return fmt.Errorf("端口 %q 不是整数", portText)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("端口 %d 超出 1-65535 范围", port)
	}
	return nil
}

// validateTopicName 按 Kafka 的 Topic 命名规则校验：仅允许字母、数字、点、下划线、连字符，
// 长度不超过 249，且不能是 "." 或 ".."（Kafka 保留名，会造成路径歧义）。
// Topic 名不会自动创建，错误信息带上配置项路径便于直接定位。
func validateTopicName(key, topic string) error {
	if len(topic) > maxTopicNameLength {
		return fmt.Errorf("%s: Topic %q 长度 %d 超过上限 %d", key, topic, len(topic), maxTopicNameLength)
	}
	if topic == "." || topic == ".." {
		return fmt.Errorf("%s: Topic %q 是 Kafka 保留名", key, topic)
	}
	for _, r := range topic {
		isAlphaNum := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !isAlphaNum && r != '.' && r != '_' && r != '-' {
			return fmt.Errorf("%s: Topic %q 含非法字符 %q（仅允许字母、数字、点、下划线、连字符）", key, topic, r)
		}
	}
	return nil
}

// validateConsumerGroup 校验消费组名：非空、长度受限、不含空白与控制字符。
// Kafka 对 group.id 的字符限制比 Topic 宽松，这里只拒绝会破坏请求/日志语义的字符。
func validateConsumerGroup(group string) error {
	if group == "" {
		return fmt.Errorf("%s: 消费组不能为空", configKeyConsumerGroup)
	}
	if len(group) > maxConsumerGroupLength {
		return fmt.Errorf("%s: 消费组 %q 长度 %d 超过上限 %d", configKeyConsumerGroup, group, len(group), maxConsumerGroupLength)
	}
	for _, r := range group {
		if r <= ' ' || r == 0x7f {
			return fmt.Errorf("%s: 消费组 %q 含空白或控制字符", configKeyConsumerGroup, group)
		}
	}
	return nil
}

// lookup 读取单个配置项，优先级：环境变量 > 配置文件（缺失时返回 nil，由调用方使用默认值）。
//
// 为什么不能直接用 g.Cfg().GetEffective：
// GetEffective 在查询配置文件前会做键归一化，把 "_" 替换为 "."（GoFrame internal/utils.FormatCmdKey），
// 于是 "flash_sale.kafka.enabled" 会被读成 "flash.sale.kafka.enabled"，永远命中不了
// 本项目既有的 flash_sale 命名空间——表现为“环境变量和代码默认值都生效，唯独配置文件不生效”。
// 这里保留 GetEffective 的环境变量优先级（FLASH_SALE_KAFKA_*），文件读取改用不做键归一化的
// cfg.Get，从而让 YAML 与 env 两条路径都能真正生效。项目未使用 gf 命令行参数覆盖配置，故不额外处理。
func lookup(ctx context.Context, cfg *gcfg.Config, key string) (*gvar.Var, error) {
	if v := genv.Get(envKey(key)); v != nil {
		return v, nil
	}
	return cfg.Get(ctx, key)
}

// envKey 把配置键映射为环境变量名（与 GoFrame FormatEnvKey 规则一致）：
// 大写 + "." 替换为 "_"，例如 flash_sale.kafka.brokers -> FLASH_SALE_KAFKA_BROKERS。
func envKey(key string) string {
	return strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

// readString 读取字符串配置，缺失时返回空串。
func readString(ctx context.Context, cfg *gcfg.Config, key string) (string, error) {
	v, err := lookup(ctx, cfg, key)
	if err != nil {
		return "", fmt.Errorf("读取配置项 %s: %w", key, err)
	}
	if v == nil {
		return "", nil
	}
	return v.String(), nil
}

// readBool 严格读取布尔配置。
//
// GoFrame 的 gvar.Bool() 会把无法识别的字符串静默转换为 false，
// 这会让 enabled: "ture" 之类的拼写错误表现为「Kafka 永久关闭」而无人察觉。
// 因此这里先取字符串再用 strconv.ParseBool 严格解析，非法值直接报错并指出配置项。
func readBool(ctx context.Context, cfg *gcfg.Config, key string) (bool, error) {
	v, err := lookup(ctx, cfg, key)
	if err != nil {
		return false, fmt.Errorf("读取配置项 %s: %w", key, err)
	}
	if v == nil {
		return false, nil
	}
	text := strings.TrimSpace(v.String())
	if text == "" {
		return false, nil
	}
	enabled, err := strconv.ParseBool(text)
	if err != nil {
		return false, fmt.Errorf("配置项 %s 需要布尔值（true/false），实际为 %q", key, text)
	}
	return enabled, nil
}

// disabledError 返回「Kafka 未启用却尝试创建客户端」的统一错误。
// 构造函数共用该错误，保证调用方与日志能区分「配置关闭」与「配置非法」。
func disabledError(kind string) error {
	return fmt.Errorf("创建 Kafka %s: Kafka 未启用（%s=false）", kind, configKeyEnabled)
}
