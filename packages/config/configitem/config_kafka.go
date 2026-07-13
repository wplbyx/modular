package configitem

import (
	"time"

	"github.com/wplbyx/modular/packages/config"
)

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// Kafka 消息队列配置
type Kafka struct {
	Brokers  []string      `mapstructure:"Brokers" validate:"required,min=1"` // Kafka broker地址列表
	Producer KafkaProducer `mapstructure:"Producer"`                          // 生产者配置
	Consumer KafkaConsumer `mapstructure:"Consumer"`                          // 消费者配置
}

// KafkaProducer 生产者
type KafkaProducer struct {
	Topic           string        `mapstructure:"Topic"`                                                  // 默认发送主题
	RequiredAck     string        `mapstructure:"RequiredAck"`                                            // 确认级别
	Compression     string        `mapstructure:"Compression" validate:"oneof=none gzip snappy lz4 zstd"` // 压缩算法
	BatchSize       int           `mapstructure:"BatchSize"`                                              // 批量发送大小
	BatchTimeout    time.Duration `mapstructure:"BatchTimeout"`                                           // 批量发送超时
	ReadTimeout     time.Duration `mapstructure:"ReadTimeout"`                                            // 连接读取超时
	WriteTimeout    time.Duration `mapstructure:"WriteTimeout"`                                           // 连接写入超时
	MaxMessageBytes int           `mapstructure:"MaxMessageBytes"`                                        // 单条消息最大字节数
	Balancer        string        `mapstructure:"Balancer" validate:"oneof=hash round_robin least_bytes"` // 分区策略
}

// KafkaConsumer 消费者
type KafkaConsumer struct {
	Topic          string        `mapstructure:"Topic" validate:"required"` // 默认消费主题
	GroupID        string        `mapstructure:"GroupID"`                   // 消费者组ID
	MinBytes       int           `mapstructure:"MinBytes"`                  // 每次拉取的最小字节数
	MaxBytes       int           `mapstructure:"MaxBytes"`                  // 每次拉取的最大字节数
	ReadBackoffMin time.Duration `mapstructure:"ReadBackoffMin"`            // 拉取失败最小退避时间
	ReadBackoffMax time.Duration `mapstructure:"ReadBackoffMax"`            // 拉取失败最大退避时间
	CommitInterval time.Duration `mapstructure:"CommitInterval"`            // 自动提交偏移量间隔，0表示手动提交
	StartOffset    string        `mapstructure:"StartOffset"`               // 起始偏移量
	MaxRetries     int           `mapstructure:"MaxRetries"`                // 消费处理失败重试次数
	DLQTopic       string        `mapstructure:"DLQTopic"`                  // 死信队列主题
}

// Flags 返回 Kafka 配置的命令行元数据。
func (Kafka) Flags(prefix string) []config.FlagSpec {
	return []config.FlagSpec{
		{Name: prefix + ".brokers", Default: []string(nil), Usage: "Kafka broker地址列表"},
		{Name: prefix + ".producer.topic", Default: "", Usage: "Kafka默认发送主题"},
		{Name: prefix + ".producer.requiredack", Default: "all", Usage: "Kafka生产者确认级别"},
		{Name: prefix + ".producer.compression", Default: "none", Usage: "Kafka生产者压缩算法"},
		{Name: prefix + ".producer.batchsize", Default: 100, Usage: "Kafka生产者批量发送大小"},
		{Name: prefix + ".producer.batchtimeout", Default: 10 * time.Millisecond, Usage: "Kafka生产者批量发送超时"},
		{Name: prefix + ".producer.readtimeout", Default: 10 * time.Second, Usage: "Kafka生产者连接读取超时"},
		{Name: prefix + ".producer.writetimeout", Default: 10 * time.Second, Usage: "Kafka生产者连接写入超时"},
		{Name: prefix + ".producer.maxmessagebytes", Default: 0, Usage: "Kafka单条消息最大字节数"},
		{Name: prefix + ".producer.balancer", Default: "hash", Usage: "Kafka分区策略"},

		{Name: prefix + ".consumer.topic", Default: "", Usage: "Kafka默认消费主题"},
		{Name: prefix + ".consumer.groupid", Default: "", Usage: "Kafka消费者组ID"},
		{Name: prefix + ".consumer.minbytes", Default: 1, Usage: "Kafka每次拉取的最小字节数"},
		{Name: prefix + ".consumer.maxbytes", Default: 10000000, Usage: "Kafka每次拉取的最大字节数"},
		{Name: prefix + ".consumer.readbackoffmin", Default: 100 * time.Millisecond, Usage: "Kafka拉取失败最小退避时间"},
		{Name: prefix + ".consumer.readbackoffmax", Default: time.Second, Usage: "Kafka拉取失败最大退避时间"},
		{Name: prefix + ".consumer.commitinterval", Default: time.Second, Usage: "Kafka自动提交偏移量间隔"},
		{Name: prefix + ".consumer.startoffset", Default: "newest", Usage: "Kafka起始偏移量"},
		{Name: prefix + ".consumer.maxretries", Default: 3, Usage: "Kafka消费失败重试次数"},
		{Name: prefix + ".consumer.dlqtopic", Default: "", Usage: "Kafka死信队列主题"},
	}
}
