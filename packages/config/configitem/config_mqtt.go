package configitem

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

// Flags 返回 MQTT 配置的命令行元数据。
func (MQTT) Flags(prefix string) []FlagSpec {
	return []FlagSpec{
		{Name: flagName(prefix, "BrokerURL"), Default: "", Usage: "MQTT broker地址"},
		{Name: flagName(prefix, "ClientID"), Default: "", Usage: "MQTT客户端标识符"},
		{Name: flagName(prefix, "Username"), Default: "", Usage: "MQTT用户名"},
		{Name: flagName(prefix, "Password"), Default: "", Usage: "MQTT密码"},
		{Name: flagName(prefix, "Client.ConnectTimeout"), Default: 30 * time.Second, Usage: "MQTT连接超时"},
		{Name: flagName(prefix, "Client.WriteTimeout"), Default: 30 * time.Second, Usage: "MQTT写入超时"},
		{Name: flagName(prefix, "Client.KeepAlive"), Default: 30 * time.Second, Usage: "MQTT保活间隔"},
		{Name: flagName(prefix, "Client.PingTimeout"), Default: 10 * time.Second, Usage: "MQTT ping超时"},
		{Name: flagName(prefix, "Client.MaxReconnectDelay"), Default: 10 * time.Minute, Usage: "MQTT最大重连延迟"},
		{Name: flagName(prefix, "Client.AutoReconnect"), Default: true, Usage: "MQTT是否自动重连"},
		{Name: flagName(prefix, "Client.CleanSession"), Default: true, Usage: "MQTT是否清除会话"},
		{Name: flagName(prefix, "Client.OrderMatters"), Default: true, Usage: "MQTT是否保证消息顺序"},
		{Name: flagName(prefix, "Producer.DefaultQos"), Default: uint8(0), Usage: "MQTT默认QoS级别"},
		{Name: flagName(prefix, "Producer.DefaultRetained"), Default: false, Usage: "MQTT默认保留消息标志"},
		{Name: flagName(prefix, "Producer.WillTopic"), Default: "", Usage: "MQTT遗嘱主题"},
		{Name: flagName(prefix, "Producer.WillPayload"), Default: "", Usage: "MQTT遗嘱消息"},
		{Name: flagName(prefix, "Producer.WillQos"), Default: uint8(0), Usage: "MQTT遗嘱QoS级别"},
		{Name: flagName(prefix, "Producer.WillRetained"), Default: false, Usage: "MQTT遗嘱保留标志"},
		{Name: flagName(prefix, "Consumer.Topic"), Default: "", Usage: "MQTT默认订阅主题"},
		{Name: flagName(prefix, "Consumer.Qos"), Default: uint8(0), Usage: "MQTT订阅QoS级别"},
		{Name: flagName(prefix, "Consumer.AutoReconnect"), Default: true, Usage: "MQTT是否自动重新订阅"},
		{Name: flagName(prefix, "Consumer.ReconnectDelay"), Default: time.Second, Usage: "MQTT重订阅延迟"},
		{Name: flagName(prefix, "Consumer.MaxRetries"), Default: 3, Usage: "MQTT消息处理失败重试次数"},
		{Name: flagName(prefix, "Consumer.ProcessTimeout"), Default: 30 * time.Second, Usage: "MQTT消息处理超时"},
		{Name: flagName(prefix, "Consumer.DLQTopic"), Default: "", Usage: "MQTT死信队列主题"},
	}
}
