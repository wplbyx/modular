package config

import (
	"time"
)

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// RocketMQ 消息队列配置
type RocketMQ struct {
	NameServers []string       `mapstructure:"NameServers" validate:"required,min=1"` // NameServer地址列表
	Producer    RocketProducer `mapstructure:"Producer"`                              // 生产者配置
	Consumer    RocketConsumer `mapstructure:"Consumer"`                              // 消费者配置
}

// RocketProducer RocketMQ 生产者配置
type RocketProducer struct {
	GroupName    string        `mapstructure:"GroupName"`                                       // 生产者组名，事务消息必须设置
	Retry        int           `mapstructure:"Retry"`                                           // 发送失败重试次数
	Timeout      time.Duration `mapstructure:"Timeout"`                                         // 发送消息超时
	Compress     bool          `mapstructure:"Compress"`                                        // 是否压缩消息
	InstanceName string        `mapstructure:"InstanceName"`                                    // 实例名称
	LogLevel     string        `mapstructure:"LogLevel" validate:"oneof=error warn info debug"` // 客户端日志级别
}

// RocketConsumer RocketMQ 消费者配置
type RocketConsumer struct {
	GroupName                  string `mapstructure:"GroupName" validate:"required"`                         // 消费者组名
	Topic                      string `mapstructure:"Topic" validate:"required"`                             // 订阅的主题
	Expression                 string `mapstructure:"Expression"`                                            // 消息过滤表达式 (SQL)
	MaxReconsumeTimes          int    `mapstructure:"MaxReconsumeTimes"`                                     // 最大重试消费次数
	ConsumeMessageBatchMaxSize int    `mapstructure:"ConsumeMessageBatchMaxSize"`                            // 一批最大消费消息数
	ConsumeThreadMin           int    `mapstructure:"ConsumeThreadMin"`                                      // 最小消费线程数
	ConsumeThreadMax           int    `mapstructure:"ConsumeThreadMax"`                                      // 最大消费线程数
	MessageModel               string `mapstructure:"MessageModel" validate:"oneof=clustering broadcasting"` // 消费模式: 集群/广播
	Orderly                    bool   `mapstructure:"Orderly"`                                               // 是否顺序消费
	InstanceName               string `mapstructure:"InstanceName"`                                          // 实例名称
	LogLevel                   string `mapstructure:"LogLevel" validate:"oneof=error warn info debug"`       // 客户端日志级别
}
