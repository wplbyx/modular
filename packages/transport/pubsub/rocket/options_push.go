package rocket

import (
	"time"
)

// ConsumerOptions configures a RocketMQ PushConsumer (the Push delivery model).
type ConsumerOptions struct {
	// Endpoint is the RocketMQ 5.x gRPC endpoint. Required.
	Endpoint string

	// Group is the consumer group name. Required.
	Group string

	// Topic is the topic subscribed at construction time. Required (the v5
	// PushConsumer needs at least one initial subscription). Additional topics
	// may be added at runtime via Consumer.Subscribe.
	Topic string

	// AccessKey / AccessSecret are optional credentials.
	AccessKey    string
	AccessSecret string

	// NameSpace is the tenant namespace (optional).
	NameSpace string

	// FilterExpression is the tag/SQL92 expression for the initial topic
	// subscription. Empty means subscribe to all ("*").
	FilterExpression string

	// FilterType selects the expression type: "tag" (default) or "sql".
	FilterType string

	// AwaitDuration is the long-polling await duration (max wait for receive).
	// <=0 keeps the SDK default.
	AwaitDuration time.Duration

	// MaxCache is WithPushMaxCacheMessageCount (in-memory message buffer).
	// <=0 keeps the SDK default of 1024.
	MaxCache int32

	// Threads is WithPushConsumptionThreadCount (concurrent consumption
	// goroutines). <=0 keeps the SDK default of 20.
	Threads int32
}

// ConsumerOption configures a ConsumerOptions.
type ConsumerOption func(*ConsumerOptions)

// DefaultConsumerOptions returns ConsumerOptions with sensible defaults.
func DefaultConsumerOptions() *ConsumerOptions {
	return &ConsumerOptions{}
}

// WithConsumerEndpoint sets the RocketMQ 5.x gRPC endpoint.
func WithConsumerEndpoint(endpoint string) ConsumerOption {
	return func(o *ConsumerOptions) {
		o.Endpoint = endpoint
	}
}

// WithConsumerGroup sets the consumer group name.
func WithConsumerGroup(group string) ConsumerOption {
	return func(o *ConsumerOptions) {
		o.Group = group
	}
}

// WithConsumerTopic sets the initial topic subscribed at construction time.
func WithConsumerTopic(topic string) ConsumerOption {
	return func(o *ConsumerOptions) {
		o.Topic = topic
	}
}

// WithConsumerCredentials sets the access key / secret for ACL-enabled brokers.
func WithConsumerCredentials(accessKey, accessSecret string) ConsumerOption {
	return func(o *ConsumerOptions) {
		o.AccessKey = accessKey
		o.AccessSecret = accessSecret
	}
}

// WithConsumerNameSpace sets the tenant namespace.
func WithConsumerNameSpace(nameSpace string) ConsumerOption {
	return func(o *ConsumerOptions) {
		o.NameSpace = nameSpace
	}
}

// WithConsumerFilter sets the tag/SQL92 filter expression for the initial
// subscription. filterType is "tag" (default) or "sql".
func WithConsumerFilter(expression, filterType string) ConsumerOption {
	return func(o *ConsumerOptions) {
		o.FilterExpression = expression
		o.FilterType = filterType
	}
}

// WithConsumerAwaitDuration sets the long-polling await duration.
func WithConsumerAwaitDuration(d time.Duration) ConsumerOption {
	return func(o *ConsumerOptions) {
		o.AwaitDuration = d
	}
}

// WithConsumerMaxCache sets the in-memory message buffer count.
func WithConsumerMaxCache(n int32) ConsumerOption {
	return func(o *ConsumerOptions) {
		o.MaxCache = n
	}
}

// WithConsumerThreads sets the concurrent consumption goroutine count.
func WithConsumerThreads(n int32) ConsumerOption {
	return func(o *ConsumerOptions) {
		o.Threads = n
	}
}
