package rocket

import (
	"context"
	"fmt"
	"io"

	rmq "github.com/apache/rocketmq-clients/golang/v5"
	"github.com/apache/rocketmq-clients/golang/v5/credentials"

	"modular/packages/log"
	"modular/packages/transport/pubsub"
)

// Ensure Producer implements pubsub.Publisher.
var _ pubsub.Publisher = (*Producer)(nil)

// Ensure Producer implements io.Closer.
var _ io.Closer = (*Producer)(nil)

// Producer implements pubsub.Publisher using the RocketMQ 5.x gRPC client.
//
// Field mapping for pubsub.PublishOption -> RocketMQ Message:
//   - WithKey      -> Message.SetKeys (RocketMQ message key)
//   - Headers[tag] -> Message.SetTag (special header "tag")
//   - Headers[*]   -> Message.AddProperty (carried as a message property)
//   - QoS/Retained -> ignored (not applicable to RocketMQ)
//
// Usage with the shared SubscriberEndpoint is not needed for a publisher; use
// NewProducer directly and call Publish as needed.
type Producer struct {
	rp   rmq.Producer
	opts *ProducerOptions
}

// NewProducer creates a RocketMQ Producer, starts it, and returns it. The
// caller must Close it when done.
func NewProducer(opts ...ProducerOption) (*Producer, error) {
	o := DefaultProducerOptions()
	for _, opt := range opts {
		opt(o)
	}
	if o.Endpoint == "" {
		return nil, fmt.Errorf("rocketmq producer endpoint is required")
	}

	rmqOpts := []rmq.ProducerOption{}
	if o.Topic != "" {
		rmqOpts = append(rmqOpts, rmq.WithTopics(o.Topic))
	}
	if o.MaxAttempts > 0 {
		rmqOpts = append(rmqOpts, rmq.WithMaxAttempts(o.MaxAttempts))
	}

	rp, err := rmq.NewProducer(&rmq.Config{
		Endpoint:    o.Endpoint,
		NameSpace:   o.NameSpace,
		Credentials: buildCredentials(o.AccessKey, o.AccessSecret),
	}, rmqOpts...)
	if err != nil {
		return nil, fmt.Errorf("rocketmq new producer: %w", err)
	}
	if err := rp.Start(); err != nil {
		return nil, fmt.Errorf("rocketmq producer start: %w", err)
	}

	log.Infof("[RocketMQ] producer started (endpoint=%s)", o.Endpoint)
	return &Producer{rp: rp, opts: o}, nil
}

// Publish sends a message to the given topic synchronously. When topic is empty
// the producer's default topic is used.
func (p *Producer) Publish(ctx context.Context, topic string, payload []byte, opts ...pubsub.PublishOption) error {
	publishOpts := &pubsub.PublishOptions{}
	for _, opt := range opts {
		opt(publishOpts)
	}

	if topic == "" {
		topic = p.opts.Topic
	}
	if topic == "" {
		return fmt.Errorf("rocketmq publish topic is required")
	}

	msg := &rmq.Message{
		Topic: topic,
		Body:  payload,
	}
	if publishOpts.Key != "" {
		msg.SetKeys(publishOpts.Key)
	}
	// The "tag" header maps to the RocketMQ tag; remaining headers become
	// message properties.
	for k, v := range publishOpts.Headers {
		if k == "tag" {
			msg.SetTag(v)
			continue
		}
		msg.AddProperty(k, v)
	}

	if _, err := p.rp.Send(ctx, msg); err != nil {
		return fmt.Errorf("rocketmq publish to %s: %w", topic, err)
	}
	return nil
}

// Close gracefully stops the producer.
func (p *Producer) Close() error {
	log.Info("[RocketMQ] producer closing...")
	return p.rp.GracefulStop()
}

// buildCredentials returns the credentials to use for the SDK Config. The v5
// client's Config declares Credentials as validate:"required"; the SDK does not
// run the validator itself, but we always return a non-nil struct so callers
// with a no-auth broker can simply omit the option.
func buildCredentials(accessKey, accessSecret string) *credentials.SessionCredentials {
	return &credentials.SessionCredentials{
		AccessKey:    accessKey,
		AccessSecret: accessSecret,
	}
}
