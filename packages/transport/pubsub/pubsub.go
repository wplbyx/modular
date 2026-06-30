package pubsub

import (
	"context"
)

// Message represents a pub/sub message
type Message struct {
	Topic   string
	Payload []byte
	Headers map[string]string
	Key     string // For partitioning (Kafka)
}

// MessageHandler is the callback function for processing messages
type MessageHandler func(ctx context.Context, msg Message) error

// Publisher is the interface for publishing messages
type Publisher interface {
	// Publish publishes a message to a topic
	Publish(ctx context.Context, topic string, payload []byte, opts ...PublishOption) error

	// Close closes the publisher
	Close() error
}

// Subscriber is the interface for subscribing to topics
type Subscriber interface {
	// Subscribe subscribes to a topic with a handler
	Subscribe(ctx context.Context, topic string, handler MessageHandler, opts ...SubscribeOption) error

	// Unsubscribe unsubscribes from a topic
	Unsubscribe(ctx context.Context, topic string) error

	// Close closes the subscriber
	Close() error
}

// Client combines Publisher and Subscriber interfaces
type Client interface {
	Publisher
	Subscriber

	// Connect establishes connection to the broker
	Connect(ctx context.Context) error

	// Disconnect disconnects from the broker
	Disconnect(ctx context.Context) error

	// IsConnected returns whether the client is connected
	IsConnected() bool
}

// PublishOptions contains options for publishing
type PublishOptions struct {
	QoS      byte
	Retained bool
	Key      string // For partitioning
	Headers  map[string]string
}

// PublishOption is a function that configures publish options
type PublishOption func(*PublishOptions)

// WithQoS sets the QoS level
func WithQoS(qos byte) PublishOption {
	return func(o *PublishOptions) {
		o.QoS = qos
	}
}

// WithRetained sets the retained flag
func WithRetained(retained bool) PublishOption {
	return func(o *PublishOptions) {
		o.Retained = retained
	}
}

// WithKey sets the message key for partitioning
func WithKey(key string) PublishOption {
	return func(o *PublishOptions) {
		o.Key = key
	}
}

// WithHeaders sets message headers
func WithHeaders(headers map[string]string) PublishOption {
	return func(o *PublishOptions) {
		o.Headers = headers
	}
}

// SubscribeOptions contains options for subscribing
type SubscribeOptions struct {
	QoS       byte
	QueueName string // For shared subscriptions
}

// SubscribeOption is a function that configures subscribe options
type SubscribeOption func(*SubscribeOptions)

// WithSubscribeQoS sets the QoS level for subscription
func WithSubscribeQoS(qos byte) SubscribeOption {
	return func(o *SubscribeOptions) {
		o.QoS = qos
	}
}

// WithQueueName sets the queue name for shared subscriptions
func WithQueueName(name string) SubscribeOption {
	return func(o *SubscribeOptions) {
		o.QueueName = name
	}
}
