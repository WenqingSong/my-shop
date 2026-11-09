package kafka

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogf/gf/v2/os/gcfg"
)

// kafkaEnvKeys 是 Kafka 配置可能使用到的环境变量。测试前统一清理，
// 避免开发者本机或 CI 环境变量污染「配置文件默认值」类断言。
var kafkaEnvKeys = []string{
	"FLASH_SALE_KAFKA_ENABLED",
	"FLASH_SALE_KAFKA_BROKERS",
	"FLASH_SALE_KAFKA_TOPIC",
	"FLASH_SALE_KAFKA_DLQ_TOPIC",
	"FLASH_SALE_KAFKA_CONSUMER_GROUP",
}

// clearKafkaEnv 取消当前进程中的所有 Kafka 环境变量，并在测试结束后恢复原值。
func clearKafkaEnv(t *testing.T) {
	t.Helper()
	for _, key := range kafkaEnvKeys {
		if value, ok := os.LookupEnv(key); ok {
			key, value := key, value
			t.Cleanup(func() { _ = os.Setenv(key, value) })
			if err := os.Unsetenv(key); err != nil {
				t.Fatalf("清理环境变量 %s: %v", key, err)
			}
		}
	}
}

// newTestConfig 用临时 YAML 文件构造隔离的 GoFrame 配置对象，
// 不触碰进程级 g.Cfg()，从而保证测试之间互不影响。
func newTestConfig(t *testing.T, content string) *gcfg.Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写入临时配置: %v", err)
	}
	adapter, err := gcfg.NewAdapterFile()
	if err != nil {
		t.Fatalf("创建配置适配器: %v", err)
	}
	if err := adapter.SetPath(dir); err != nil {
		t.Fatalf("设置配置目录: %v", err)
	}
	adapter.SetFileName("config.yaml")
	return gcfg.NewWithAdapter(adapter)
}

// 默认配置（仓库 config.yaml 的形态）：Kafka 关闭但主题/消费组已填好，应能正常解析。
func TestConfigParseDefaultDisabled(t *testing.T) {
	cfg, err := Parse(RawConfig{
		Enabled:       false,
		Brokers:       "127.0.0.1:29092",
		Topic:         "surgecart.flashsale.order.requested.v1",
		DLQTopic:      "surgecart.flashsale.order.requested.v1.dlq",
		ConsumerGroup: "surgecart-flashsale-order-worker-v1",
	})
	if err != nil {
		t.Fatalf("默认配置应可解析: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("默认配置 Enabled 应为 false")
	}
	if len(cfg.Brokers) != 1 || cfg.Brokers[0] != "127.0.0.1:29092" {
		t.Fatalf("Brokers 解析不符合预期: %#v", cfg.Brokers)
	}
	if cfg.Topic != "surgecart.flashsale.order.requested.v1" {
		t.Fatalf("Topic 不符合预期: %q", cfg.Topic)
	}
}

// Enabled=false 时允许 Broker/Topic/消费组全部留空：关闭 Kafka 不应阻塞启动。
func TestConfigParseDisabledAllowsEmpty(t *testing.T) {
	cfg, err := Parse(RawConfig{})
	if err != nil {
		t.Fatalf("关闭 Kafka 且未配置任何值时应通过: %v", err)
	}
	if cfg.Enabled || len(cfg.Brokers) != 0 || cfg.Topic != "" || cfg.DLQTopic != "" || cfg.ConsumerGroup != "" {
		t.Fatalf("空配置解析结果不符合预期: %#v", cfg)
	}
}

// Enabled=false 时「已填写的值」仍然要合法，避免配置拼写错误被开关掩盖。
func TestConfigParseDisabledStillValidatesFilledValues(t *testing.T) {
	t.Run("非法 Broker", func(t *testing.T) {
		_, err := Parse(RawConfig{Enabled: false, Brokers: "not-a-broker"})
		if err == nil || !strings.Contains(err.Error(), configKeyBrokers) {
			t.Fatalf("期望 Broker 配置错误，实际: %v", err)
		}
	})
	t.Run("死信主题与正常主题相同", func(t *testing.T) {
		_, err := Parse(RawConfig{
			Enabled:  false,
			Topic:    "a.b",
			DLQTopic: "a.b",
		})
		if err == nil || !strings.Contains(err.Error(), configKeyDLQTopic) {
			t.Fatalf("期望 DLQ Topic 冲突错误，实际: %v", err)
		}
	})
}

// 多 Broker 必须按逗号拆分并逐项去除空格，保留配置顺序。
func TestConfigParseMultiBrokers(t *testing.T) {
	cfg, err := Parse(RawConfig{
		Enabled:       true,
		Brokers:       "  kafka-1:9092 ,kafka-2:9092,  kafka-3:9092  ",
		Topic:         "surgecart.flashsale.order.requested.v1",
		DLQTopic:      "surgecart.flashsale.order.requested.v1.dlq",
		ConsumerGroup: "surgecart-flashsale-order-worker-v1",
	})
	if err != nil {
		t.Fatalf("多 Broker 解析失败: %v", err)
	}
	want := []string{"kafka-1:9092", "kafka-2:9092", "kafka-3:9092"}
	if len(cfg.Brokers) != len(want) {
		t.Fatalf("Broker 数量 = %d, want %d (%#v)", len(cfg.Brokers), len(want), cfg.Brokers)
	}
	for i := range want {
		if cfg.Brokers[i] != want[i] {
			t.Fatalf("Broker[%d] = %q, want %q", i, cfg.Brokers[i], want[i])
		}
	}
}

// 空 Broker 片段必须报错，不能被静默丢弃后以「少一个 Broker」运行。
func TestConfigParseRejectsEmptyBrokerSegment(t *testing.T) {
	cases := []string{
		"kafka-1:9092,,kafka-2:9092",
		"kafka-1:9092,",
		",kafka-1:9092",
	}
	for _, brokers := range cases {
		t.Run(brokers, func(t *testing.T) {
			_, err := Parse(RawConfig{Enabled: false, Brokers: brokers})
			if err == nil {
				t.Fatalf("Brokers=%q 应被拒绝", brokers)
			}
			if !strings.Contains(err.Error(), configKeyBrokers) || !strings.Contains(err.Error(), "空地址") {
				t.Fatalf("错误信息应指出 brokers 配置项与空地址，实际: %v", err)
			}
		})
	}
}

// 重复 Broker（忽略大小写）必须报错，错误信息点明重复位置。
func TestConfigParseRejectsDuplicateBrokers(t *testing.T) {
	_, err := Parse(RawConfig{Enabled: false, Brokers: "kafka:9092,KAFKA:9092"})
	if err == nil {
		t.Fatal("重复 Broker 应被拒绝")
	}
	if !strings.Contains(err.Error(), configKeyBrokers) || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("错误信息应指出重复 Broker，实际: %v", err)
	}
}

// 非法 Broker 地址逐项拒绝，错误信息包含配置项路径与具体值。
func TestConfigParseRejectsInvalidBrokers(t *testing.T) {
	cases := map[string]string{
		"缺少端口":  "kafka",
		"端口非数字": "kafka:abc",
		"端口为零":  "kafka:0",
		"端口越界":  "kafka:70000",
		"带协议头":  "http://kafka:9092",
		"多段冒号":  "kafka:9092:1",
	}
	for name, broker := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(RawConfig{Enabled: false, Brokers: broker})
			if err == nil {
				t.Fatalf("Broker=%q 应被拒绝", broker)
			}
			if !strings.Contains(err.Error(), configKeyBrokers) || !strings.Contains(err.Error(), broker) {
				t.Fatalf("错误信息应包含配置项与非法值 %q，实际: %v", broker, err)
			}
		})
	}
}

// Enabled=true 时 Broker/Topic/DLQTopic/ConsumerGroup 任一缺失都必须明确失败。
func TestConfigParseEnabledRequiresFields(t *testing.T) {
	base := RawConfig{
		Enabled:       true,
		Brokers:       "kafka:9092",
		Topic:         "surgecart.flashsale.order.requested.v1",
		DLQTopic:      "surgecart.flashsale.order.requested.v1.dlq",
		ConsumerGroup: "surgecart-flashsale-order-worker-v1",
	}

	if _, err := Parse(base); err != nil {
		t.Fatalf("完整配置应通过: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*RawConfig)
		key    string
	}{
		{name: "缺少 Broker", mutate: func(c *RawConfig) { c.Brokers = "  " }, key: configKeyBrokers},
		{name: "缺少 Topic", mutate: func(c *RawConfig) { c.Topic = " " }, key: configKeyTopic},
		{name: "缺少 DLQ Topic", mutate: func(c *RawConfig) { c.DLQTopic = "" }, key: configKeyDLQTopic},
		{name: "缺少消费组", mutate: func(c *RawConfig) { c.ConsumerGroup = "" }, key: configKeyConsumerGroup},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := base
			tc.mutate(&raw)
			_, err := Parse(raw)
			if err == nil {
				t.Fatalf("%s 应被拒绝", tc.name)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("错误信息应指出 %s，实际: %v", tc.key, err)
			}
		})
	}
}

// 死信主题与正常主题相同时必须拒绝，否则失败消息会回灌正常链路。
func TestConfigParseRejectsDLQEqualsTopic(t *testing.T) {
	_, err := Parse(RawConfig{
		Enabled:       true,
		Brokers:       "kafka:9092",
		Topic:         "surgecart.flashsale.order.requested.v1",
		DLQTopic:      "surgecart.flashsale.order.requested.v1",
		ConsumerGroup: "surgecart-flashsale-order-worker-v1",
	})
	if err == nil {
		t.Fatal("DLQ Topic 与 Topic 相同应被拒绝")
	}
	if !strings.Contains(err.Error(), configKeyDLQTopic) {
		t.Fatalf("错误信息应指出 DLQ Topic 配置项，实际: %v", err)
	}
}

// 非法 Topic 名称（非法字符、保留名、超长）必须拒绝。
func TestConfigParseRejectsInvalidTopic(t *testing.T) {
	cases := map[string]string{
		"含空格": "surgecart flashsale",
		"含斜杠": "surgecart/flashsale",
		"保留点": ".",
		"超长":  strings.Repeat("a", maxTopicNameLength+1),
	}
	for name, topic := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(RawConfig{Enabled: false, Topic: topic})
			if err == nil {
				t.Fatalf("Topic=%q 应被拒绝", topic)
			}
			if !strings.Contains(err.Error(), configKeyTopic) {
				t.Fatalf("错误信息应指出 topic 配置项，实际: %v", err)
			}
		})
	}
}

// LoadFrom 应从 GoFrame 配置文件读取并解析 flash_sale.kafka 节点。
func TestConfigLoadFromFile(t *testing.T) {
	clearKafkaEnv(t)
	cfg := newTestConfig(t, `
flash_sale:
  dispatch_mode: "db_scanner"
  kafka:
    enabled: true
    brokers: "kafka:9092, localhost:29092"
    topic: "surgecart.flashsale.order.requested.v1"
    dlq_topic: "surgecart.flashsale.order.requested.v1.dlq"
    consumer_group: "surgecart-flashsale-order-worker-v1"
`)
	got, err := LoadFrom(context.Background(), cfg)
	if err != nil {
		t.Fatalf("从配置文件加载失败: %v", err)
	}
	if !got.Enabled {
		t.Fatal("Enabled 应为 true")
	}
	if len(got.Brokers) != 2 || got.Brokers[0] != "kafka:9092" || got.Brokers[1] != "localhost:29092" {
		t.Fatalf("Brokers 不符合预期: %#v", got.Brokers)
	}
	if got.DLQTopic != "surgecart.flashsale.order.requested.v1.dlq" {
		t.Fatalf("DLQTopic 不符合预期: %q", got.DLQTopic)
	}
	if got.ConsumerGroup != "surgecart-flashsale-order-worker-v1" {
		t.Fatalf("ConsumerGroup 不符合预期: %q", got.ConsumerGroup)
	}
}

// 环境变量必须真实覆盖配置文件值（验证 FLASH_SALE_KAFKA_* 映射生效，
// 而不是仅凭「大写+下划线」的命名推断）。
func TestConfigEnvironmentOverridesFile(t *testing.T) {
	clearKafkaEnv(t)
	cfg := newTestConfig(t, `
flash_sale:
  kafka:
    enabled: true
    brokers: "file-broker:9092"
    topic: "surgecart.flashsale.order.requested.v1"
    dlq_topic: "surgecart.flashsale.order.requested.v1.dlq"
    consumer_group: "surgecart-flashsale-order-worker-v1"
`)
	t.Setenv("FLASH_SALE_KAFKA_ENABLED", "false")
	t.Setenv("FLASH_SALE_KAFKA_BROKERS", "env-broker:19092")

	got, err := LoadFrom(context.Background(), cfg)
	if err != nil {
		t.Fatalf("环境变量覆盖后应可解析: %v", err)
	}
	if got.Enabled {
		t.Fatal("FLASH_SALE_KAFKA_ENABLED=false 应覆盖配置文件中的 enabled=true")
	}
	if len(got.Brokers) != 1 || got.Brokers[0] != "env-broker:19092" {
		t.Fatalf("FLASH_SALE_KAFKA_BROKERS 未覆盖配置文件值: %#v", got.Brokers)
	}
}

// 非法布尔值必须报错而不是被 gvar.Bool 静默转换为 false。
func TestConfigLoadFromRejectsInvalidBoolean(t *testing.T) {
	clearKafkaEnv(t)
	cfg := newTestConfig(t, `
flash_sale:
  kafka:
    enabled: "ture"
`)
	_, err := LoadFrom(context.Background(), cfg)
	if err == nil {
		t.Fatal("非法布尔值应被拒绝")
	}
	if !strings.Contains(err.Error(), configKeyEnabled) {
		t.Fatalf("错误信息应指出 enabled 配置项，实际: %v", err)
	}
}

// 仓库默认配置必须保持「db_scanner + kafka.enabled=false」，
// 防止后续批次误改默认值导致现有秒杀链路行为变化。
func TestRepositoryConfigKeepsSafeDefaults(t *testing.T) {
	clearKafkaEnv(t)
	configDir, err := filepath.Abs(filepath.Join("..", "..", "..", "manifest", "config"))
	if err != nil {
		t.Fatalf("解析配置目录: %v", err)
	}
	adapter, err := gcfg.NewAdapterFile()
	if err != nil {
		t.Fatalf("创建配置适配器: %v", err)
	}
	if err := adapter.SetPath(configDir); err != nil {
		t.Fatalf("设置配置目录: %v", err)
	}
	adapter.SetFileName("config.yaml")
	cfg := gcfg.NewWithAdapter(adapter)

	ctx := context.Background()
	mode, err := cfg.Get(ctx, "flash_sale.dispatch_mode", "")
	if err != nil {
		t.Fatalf("读取 dispatch_mode: %v", err)
	}
	if mode.String() != "db_scanner" {
		t.Fatalf("默认 dispatch_mode 应为 db_scanner，实际 %q", mode.String())
	}

	got, err := LoadFrom(ctx, cfg)
	if err != nil {
		t.Fatalf("仓库默认 Kafka 配置应可解析: %v", err)
	}
	if got.Enabled {
		t.Fatal("仓库默认 flash_sale.kafka.enabled 必须为 false")
	}
	if got.Topic != "surgecart.flashsale.order.requested.v1" {
		t.Fatalf("默认 topic 不符合契约: %q", got.Topic)
	}
	if got.DLQTopic != "surgecart.flashsale.order.requested.v1.dlq" {
		t.Fatalf("默认 dlq_topic 不符合契约: %q", got.DLQTopic)
	}
	if got.ConsumerGroup != "surgecart-flashsale-order-worker-v1" {
		t.Fatalf("默认 consumer_group 不符合契约: %q", got.ConsumerGroup)
	}
}
