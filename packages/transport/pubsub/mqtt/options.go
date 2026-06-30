package mqtt

import (
	"crypto/tls"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// Option is a function that configures a MQTT client.
type Option func(*Options)

// Options contains configuration for MQTT client.
type Options struct {
	// Broker connection settings
	BrokerURL string
	ClientID  string
	Username  string
	Password  string

	// Connection settings
	ConnectTimeout    time.Duration
	WriteTimeout      time.Duration
	KeepAlive         time.Duration
	PingTimeout       time.Duration
	MaxReconnectDelay time.Duration
	AutoReconnect     bool

	// Message settings
	DefaultQos      byte
	DefaultRetained bool
	CleanSession    bool

	// TLS settings
	TLSConfig *tls.Config

	// Message handlers
	OnConnectHandler      paho.OnConnectHandler
	ConnectionLostHandler paho.ConnectionLostHandler
	DefaultMessageHandler paho.MessageHandler

	// Will message
	WillTopic    string
	WillPayload  string
	WillQos      byte
	WillRetained bool

	// Client options
	Store        paho.Store
	OrderMatters bool
}

// DefaultOptions returns the default MQTT options.
func DefaultOptions() *Options {
	return &Options{
		ConnectTimeout:    30 * time.Second,
		WriteTimeout:      5 * time.Second,
		KeepAlive:         30 * time.Second,
		PingTimeout:       10 * time.Second,
		MaxReconnectDelay: 60 * time.Second,
		AutoReconnect:     true,
		DefaultQos:        0,
		DefaultRetained:   false,
		CleanSession:      true,
		OrderMatters:      false,
	}
}

// WithBrokerURL sets the MQTT broker URL.
func WithBrokerURL(url string) Option {
	return func(o *Options) {
		o.BrokerURL = url
	}
}

// WithClientID sets the client identifier.
func WithClientID(id string) Option {
	return func(o *Options) {
		o.ClientID = id
	}
}

// WithCredentials sets username and password for authentication.
func WithCredentials(username, password string) Option {
	return func(o *Options) {
		o.Username = username
		o.Password = password
	}
}

// WithConnectTimeout sets the connection timeout.
func WithConnectTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		o.ConnectTimeout = timeout
	}
}

// WithWriteTimeout sets the write timeout for publishing messages.
func WithWriteTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		o.WriteTimeout = timeout
	}
}

// WithKeepAlive sets the keep-alive interval.
func WithKeepAlive(interval time.Duration) Option {
	return func(o *Options) {
		o.KeepAlive = interval
	}
}

// WithPingTimeout sets the ping timeout.
func WithPingTimeout(timeout time.Duration) Option {
	return func(o *Options) {
		o.PingTimeout = timeout
	}
}

// WithMaxReconnectDelay sets the maximum delay between reconnection attempts.
func WithMaxReconnectDelay(delay time.Duration) Option {
	return func(o *Options) {
		o.MaxReconnectDelay = delay
	}
}

// WithAutoReconnect enables or disables automatic reconnection.
func WithAutoReconnect(enable bool) Option {
	return func(o *Options) {
		o.AutoReconnect = enable
	}
}

// WithDefaultQos sets the default QoS level for published messages.
func WithDefaultQos(qos byte) Option {
	return func(o *Options) {
		o.DefaultQos = qos
	}
}

// WithDefaultRetained sets the default retained flag for published messages.
func WithDefaultRetained(retained bool) Option {
	return func(o *Options) {
		o.DefaultRetained = retained
	}
}

// WithCleanSession sets the clean session flag.
func WithCleanSession(clean bool) Option {
	return func(o *Options) {
		o.CleanSession = clean
	}
}

// WithTLSConfig sets the TLS configuration for secure connections.
func WithTLSConfig(config *tls.Config) Option {
	return func(o *Options) {
		o.TLSConfig = config
	}
}

// WithOnConnectHandler sets the handler called when the client connects.
func WithOnConnectHandler(handler paho.OnConnectHandler) Option {
	return func(o *Options) {
		o.OnConnectHandler = handler
	}
}

// WithConnectionLostHandler sets the handler called when the connection is lost.
func WithConnectionLostHandler(handler paho.ConnectionLostHandler) Option {
	return func(o *Options) {
		o.ConnectionLostHandler = handler
	}
}

// WithDefaultMessageHandler sets the default handler for unmatched incoming messages.
func WithDefaultMessageHandler(handler paho.MessageHandler) Option {
	return func(o *Options) {
		o.DefaultMessageHandler = handler
	}
}

// WithWill sets the Last Will and Testament message.
func WithWill(topic string, payload string, qos byte, retained bool) Option {
	return func(o *Options) {
		o.WillTopic = topic
		o.WillPayload = payload
		o.WillQos = qos
		o.WillRetained = retained
	}
}

// WithStore sets the MQTT message store.
func WithStore(store paho.Store) Option {
	return func(o *Options) {
		o.Store = store
	}
}

// WithOrderMatters sets whether message order must be preserved.
func WithOrderMatters(order bool) Option {
	return func(o *Options) {
		o.OrderMatters = order
	}
}
