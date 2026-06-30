package config

import (
	"time"
)

//go:generate gomodifytags -file $GOFILE -add-tags mapstructure -remove-tags json,yaml,default -transform pascalcase -all -w --override --sort --quiet

// MQTT 消息队列配置
type MQTT struct {
	BrokerURL string       `mapstructure:"BrokerURL" validate:"required,url"` // MQTT broker地址，格式: tcp://host:port 或 tls://host:port
	ClientID  string       `mapstructure:"ClientID"`                          // 客户端标识符，为空时自动生成
	Username  string       `mapstructure:"Username"`                          // 用户名
	Password  string       `mapstructure:"Password"`                          // 密码
	Client    MQTTClient   `mapstructure:"Client"`                            // 客户端配置
	Producer  MQTTProducer `mapstructure:"Producer"`                          // 生产者配置
	Consumer  MQTTConsumer `mapstructure:"Consumer"`                          // 消费者配置
}

// MQTTClient 客户端连接配置
type MQTTClient struct {
	ConnectTimeout    time.Duration `mapstructure:"ConnectTimeout"`    // 连接超时
	WriteTimeout      time.Duration `mapstructure:"WriteTimeout"`      // 写入超时
	KeepAlive         time.Duration `mapstructure:"KeepAlive"`         // 保活间隔
	PingTimeout       time.Duration `mapstructure:"PingTimeout"`       // ping超时
	MaxReconnectDelay time.Duration `mapstructure:"MaxReconnectDelay"` // 最大重连延迟
	AutoReconnect     bool          `mapstructure:"AutoReconnect"`     // 自动重连
	CleanSession      bool          `mapstructure:"CleanSession"`      // 清除会话
	OrderMatters      bool          `mapstructure:"OrderMatters"`      // 保证消息顺序
}

// MQTTProducer 生产者配置
type MQTTProducer struct {
	DefaultQos      byte   `mapstructure:"DefaultQos"`      // 默认QoS级别 (0, 1, 2)
	DefaultRetained bool   `mapstructure:"DefaultRetained"` // 默认保留消息标志
	WillTopic       string `mapstructure:"WillTopic"`       // 遗嘱主题
	WillPayload     string `mapstructure:"WillPayload"`     // 遗嘱消息
	WillQos         byte   `mapstructure:"WillQos"`         // 遗嘱QoS级别
	WillRetained    bool   `mapstructure:"WillRetained"`    // 遗嘱保留标志
}

// MQTTConsumer 消费者配置
type MQTTConsumer struct {
	Topic          string        `mapstructure:"Topic"`          // 默认订阅主题
	Qos            byte          `mapstructure:"Qos"`            // 订阅QoS级别
	AutoReconnect  bool          `mapstructure:"AutoReconnect"`  // 自动重新订阅
	ReconnectDelay time.Duration `mapstructure:"ReconnectDelay"` // 重订阅延迟
	MaxRetries     int           `mapstructure:"MaxRetries"`     // 消息处理失败重试次数
	ProcessTimeout time.Duration `mapstructure:"ProcessTimeout"` // 消息处理超时
	DLQTopic       string        `mapstructure:"DLQTopic"`       // 死信队列主题
}

// SetFlags implements Configurer.
func (m *MQTT) SetFlags() {
	// MQTT配置不设置命令行标志
}

// Validate implements Configurer.
func (m *MQTT) Validate() error {
	// 使用validator标签进行验证
	return nil
}
