package rocket

// ProducerOptions configures a RocketMQ Producer. The Producer is shared across
// delivery models (a future Pull consumer uses the same Publisher).
//
// The RocketMQ v5 client talks to a single gRPC endpoint (not a NameServer
// list), so the configuration model differs from the 4.x remoting client.
type ProducerOptions struct {
	// Endpoint is the RocketMQ 5.x gRPC endpoint (e.g. "127.0.0.1:8081").
	// Required.
	Endpoint string

	// Group is the producer group name (required for transaction messages).
	Group string

	// Topic is the default topic used by Publish when the caller passes an
	// empty topic.
	Topic string

	// AccessKey / AccessSecret are optional credentials. When both are empty a
	// no-auth placeholder is used; pass real values for ACL-enabled brokers.
	AccessKey    string
	AccessSecret string

	// NameSpace is the tenant namespace (optional).
	NameSpace string

	// MaxAttempts is the send retry count applied on the client side. <=0 keeps
	// the SDK default.
	MaxAttempts int32
}

// ProducerOption configures a ProducerOptions.
type ProducerOption func(*ProducerOptions)

// DefaultProducerOptions returns ProducerOptions with sensible defaults.
func DefaultProducerOptions() *ProducerOptions {
	return &ProducerOptions{}
}

// WithEndpoint sets the RocketMQ 5.x gRPC endpoint.
func WithEndpoint(endpoint string) ProducerOption {
	return func(o *ProducerOptions) {
		o.Endpoint = endpoint
	}
}

// WithProducerGroup sets the producer group name.
func WithProducerGroup(group string) ProducerOption {
	return func(o *ProducerOptions) {
		o.Group = group
	}
}

// WithProducerTopic sets the default topic used when Publish receives an empty
// topic.
func WithProducerTopic(topic string) ProducerOption {
	return func(o *ProducerOptions) {
		o.Topic = topic
	}
}

// WithProducerCredentials sets the access key / secret for ACL-enabled brokers.
func WithProducerCredentials(accessKey, accessSecret string) ProducerOption {
	return func(o *ProducerOptions) {
		o.AccessKey = accessKey
		o.AccessSecret = accessSecret
	}
}

// WithProducerNameSpace sets the tenant namespace.
func WithProducerNameSpace(nameSpace string) ProducerOption {
	return func(o *ProducerOptions) {
		o.NameSpace = nameSpace
	}
}

// WithProducerMaxAttempts sets the client-side send retry count.
func WithProducerMaxAttempts(maxAttempts int32) ProducerOption {
	return func(o *ProducerOptions) {
		o.MaxAttempts = maxAttempts
	}
}
