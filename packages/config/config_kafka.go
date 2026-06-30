package config

import (
	"time"
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
