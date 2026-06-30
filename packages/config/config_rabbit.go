package config

import (
	"time"
)

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

type RabbitMQ struct {
	Conn     RabbitConn     `mapstructure:"Conn"`     // 连接配置
	Producer RabbitProducer `mapstructure:"Producer"` // 生产者/发布者配置
	Consumer RabbitConsumer `mapstructure:"Consumer"` // 消费者配置
}

// RabbitConn RabbitMQ 连接配置
type RabbitConn struct {
	URLs           []string      `mapstructure:"URLs" validate:"required,min=1"` // AMQP URL列表, e.g., ["amqp://user:pass@host:port/vhost"]
	Username       string        `mapstructure:"Username"`                       // 用户名
	Password       string        `mapstructure:"Password"`                       // 密码
	VHost          string        `mapstructure:"VHost"`                          // 虚拟主机
	DialTimeout    time.Duration `mapstructure:"DialTimeout"`                    // 连接超时
	ReadTimeout    time.Duration `mapstructure:"ReadTimeout"`                    // 读取超时
	WriteTimeout   time.Duration `mapstructure:"WriteTimeout"`                   // 写入超时
	Heartbeat      time.Duration `mapstructure:"Heartbeat"`                      // 心跳间隔
	Locale         string        `mapstructure:"Locale"`                         // 客户端区域设置
	EnableTLS      bool          `mapstructure:"EnableTLS"`                      // 是否启用TLS
	ClientCertFile string        `mapstructure:"ClientCertFile"`                 // 客户端证书文件
	ClientKeyFile  string        `mapstructure:"ClientKeyFile"`                  // 客户端私钥文件
	CACertFile     string        `mapstructure:"CACertFile"`                     // CA证书文件
}

// RabbitProducer RabbitMQ 发布者配置
type RabbitProducer struct {
	Exchange       string        `mapstructure:"Exchange"`                                                  // 目标交换机名称
	ExchangeType   string        `mapstructure:"ExchangeType" validate:"oneof=direct topic fanout headers"` // 交换机类型
	Mandatory      bool          `mapstructure:"Mandatory"`                                                 // 无法路由时是否返回消息给发布者
	Immediate      bool          `mapstructure:"Immediate"`                                                 // 消息是否能立即路由到队列
	DeliveryMode   uint8         `mapstructure:"DeliveryMode" validate:"oneof=1 2"`                         // 1=非持久化, 2=持久化
	ContentType    string        `mapstructure:"ContentType"`                                               // 消息内容类型
	Confirm        bool          `mapstructure:"Confirm"`                                                   // 是否启用发布者确认
	PublishTimeout time.Duration `mapstructure:"PublishTimeout"`                                            // 等待确认的超时时间
}

// RabbitConsumer RabbitMQ 消费者配置
type RabbitConsumer struct {
	Queue         string        `mapstructure:"Queue"`         // 消费的队列名称
	Binding       RabbitBinding `mapstructure:"Binding"`       // 队列到交换机的绑定
	ConsumerTag   string        `mapstructure:"ConsumerTag"`   // 消费者标签
	AutoAck       bool          `mapstructure:"AutoAck"`       // 是否自动确认
	Exclusive     bool          `mapstructure:"Exclusive"`     // 是否排他性消费者
	NoLocal       bool          `mapstructure:"NoLocal"`       // 是否不接收本连接发布的消息
	NoWait        bool          `mapstructure:"NoWait"`        // 是否等待队列声明
	PrefetchCount int           `mapstructure:"PrefetchCount"` // 预取消息数量
	Requeue       bool          `mapstructure:"Requeue"`       // 消息被拒绝时是否重新入队
}

// RabbitBinding 队列绑定配置
type RabbitBinding struct {
	Exchange   string `mapstructure:"Exchange"`   // 要绑定的交换机
	RoutingKey string `mapstructure:"RoutingKey"` // 路由键
}
