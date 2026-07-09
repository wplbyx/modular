package rocket

import (
	"context"
	"fmt"
	"io"
	"sync"

	rmq "github.com/apache/rocketmq-clients/golang/v5"

	"github.com/wplbyx/modular/packages/log"
	"github.com/wplbyx/modular/packages/transport/pubsub"
)

// Ensure PushConsumer implements pubsub.Subscriber.
var _ pubsub.Subscriber = (*PushConsumer)(nil)

// Ensure PushConsumer implements io.Closer.
var _ io.Closer = (*PushConsumer)(nil)

// PushConsumer implements pubsub.Subscriber using a RocketMQ 5.x PushConsumer
// (the Push delivery model).
//
// The v5 PushConsumer requires, at construction time, both a message listener
// and at least one initial subscription. Therefore NewPushConsumer registers
// the ConsumerOptions.Topic subscription up front together with a single
// dispatch listener that routes each message to the handler registered for its
// topic via Subscribe. Additional topics can be added at runtime through
// Subscribe.
//
// Handler contract: returning nil from the handler acks the message (the
// listener returns SUCCESS); returning an error schedules broker-side retry
// (the listener returns FAILURE, and the broker redelivers per its retry
// policy).
//
// Field mapping for MessageView -> pubsub.Message:
//   - GetTopic()           -> Topic
//   - GetBody()            -> Payload
//   - GetKeys()[0]         -> Key (when present)
//   - GetTag()             -> Headers["tag"] (when present)
//   - GetProperties()      -> Headers[*] (merged)
//
// Blocking: Subscribe returns immediately once the handler is registered and
// the runtime subscription is recorded. Blocking until shutdown is the
// SubscriberEndpoint adapter's responsibility.
//
// Usage with the shared SubscriberEndpoint:
//
//	c, _ := rocket.NewPushConsumer(
//	    rocket.WithConsumerEndpoint("127.0.0.1:8081"),
//	    rocket.WithConsumerGroup("events-group"),
//	    rocket.WithConsumerTopic("events"),
//	)
//	ep := pubsub.NewSubscriberEndpoint("rocket-events", c, "events", myHandler)
//	app.WithEndpoint(ep)
type PushConsumer struct {
	pc     rmq.PushConsumer
	opts   *ConsumerOptions
	mu     sync.RWMutex
	closed bool

	// handlers maps topic -> handler. The dispatch listener reads from here for
	// every delivered message.
	handlers sync.Map
}

// NewPushConsumer creates a RocketMQ PushConsumer, registers the initial topic
// subscription and dispatch listener, starts it, and returns it. The caller
// must Close it when done.
func NewPushConsumer(opts ...ConsumerOption) (*PushConsumer, error) {
	o := DefaultConsumerOptions()
	for _, opt := range opts {
		opt(o)
	}
	if o.Endpoint == "" {
		return nil, fmt.Errorf("rocketmq consumer endpoint is required")
	}
	if o.Group == "" {
		return nil, fmt.Errorf("rocketmq consumer group is required")
	}
	if o.Topic == "" {
		return nil, fmt.Errorf("rocketmq consumer topic is required")
	}

	c := &PushConsumer{opts: o}

	rmqOpts := []rmq.PushConsumerOption{
		rmq.WithPushSubscriptionExpressions(map[string]*rmq.FilterExpression{
			o.Topic: newFilterExpression(o.FilterExpression, o.FilterType),
		}),
		rmq.WithPushMessageListener(&rmq.FuncMessageListener{
			Consume: c.dispatch,
		}),
	}
	if o.AwaitDuration > 0 {
		rmqOpts = append(rmqOpts, rmq.WithPushAwaitDuration(o.AwaitDuration))
	}
	if o.MaxCache > 0 {
		rmqOpts = append(rmqOpts, rmq.WithPushMaxCacheMessageCount(o.MaxCache))
	}
	if o.Threads > 0 {
		rmqOpts = append(rmqOpts, rmq.WithPushConsumptionThreadCount(o.Threads))
	}

	pc, err := rmq.NewPushConsumer(&rmq.Config{
		Endpoint:      o.Endpoint,
		NameSpace:     o.NameSpace,
		ConsumerGroup: o.Group,
		Credentials:   buildCredentials(o.AccessKey, o.AccessSecret),
	}, rmqOpts...)
	if err != nil {
		return nil, fmt.Errorf("rocketmq new push consumer: %w", err)
	}
	if err := pc.Start(); err != nil {
		return nil, fmt.Errorf("rocketmq push consumer start: %w", err)
	}
	c.pc = pc

	log.Infof("[RocketMQ] push consumer started (endpoint=%s, group=%s, topic=%s)",
		o.Endpoint, o.Group, o.Topic)
	return c, nil
}

// Subscribe registers a handler for a topic and records the runtime
// subscription with the broker. For the initial topic (ConsumerOptions.Topic)
// the subscription already exists; for additional topics this contacts the
// broker to add it. Subscribe returns immediately.
func (c *PushConsumer) Subscribe(ctx context.Context, topic string, handler pubsub.MessageHandler, opts ...pubsub.SubscribeOption) error {
	if topic == "" {
		return fmt.Errorf("rocketmq subscribe topic is required")
	}

	c.handlers.Store(topic, handler)

	// The initial topic is already subscribed at construction; registering it
	// again would re-fetch route metadata, so skip it.
	if topic != c.opts.Topic {
		filter := newFilterExpression(c.opts.FilterExpression, c.opts.FilterType)
		if err := c.pc.Subscribe(topic, filter); err != nil {
			c.handlers.Delete(topic)
			return fmt.Errorf("rocketmq subscribe %s: %w", topic, err)
		}
	}

	log.Infof("[RocketMQ] subscribed to topic %s", topic)
	return nil
}

// Unsubscribe removes the handler and the runtime subscription for a topic.
// The initial topic cannot be unsubscribed without re-creating the consumer.
func (c *PushConsumer) Unsubscribe(ctx context.Context, topic string) error {
	c.handlers.Delete(topic)
	if err := c.pc.Unsubscribe(topic); err != nil {
		return fmt.Errorf("rocketmq unsubscribe %s: %w", topic, err)
	}
	log.Infof("[RocketMQ] unsubscribed from topic %s", topic)
	return nil
}

// Close gracefully stops the push consumer.
func (c *PushConsumer) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	log.Info("[RocketMQ] push consumer closing...")
	return c.pc.GracefulStop()
}

// dispatch is the single message listener registered with the PushConsumer. It
// looks up the handler for the message's topic and invokes it. A nil handler
// error acks the message (SUCCESS); an error schedules a retry (FAILURE).
func (c *PushConsumer) dispatch(mv *rmq.MessageView) rmq.ConsumerResult {
	val, ok := c.handlers.Load(mv.GetTopic())
	if !ok {
		// No handler for this topic: ack to avoid an unbounded retry loop.
		log.Warnf("[RocketMQ] no handler registered for topic %s, acking", mv.GetTopic())
		return rmq.SUCCESS
	}
	handler := val.(pubsub.MessageHandler)

	message := messageFromMessageView(mv)
	if err := handler(context.Background(), message); err != nil {
		log.Warnf("[RocketMQ] handler error for topic %s: %v", mv.GetTopic(), err)
		return rmq.FAILURE
	}
	return rmq.SUCCESS
}

// messageFromMessageView converts a RocketMQ MessageView into a pubsub.Message.
func messageFromMessageView(mv *rmq.MessageView) pubsub.Message {
	msg := pubsub.Message{
		Topic:   mv.GetTopic(),
		Payload: mv.GetBody(),
		Headers: make(map[string]string),
	}
	if keys := mv.GetKeys(); len(keys) > 0 {
		msg.Key = keys[0]
	}
	if tag := mv.GetTag(); tag != nil && *tag != "" {
		msg.Headers["tag"] = *tag
	}
	for k, v := range mv.GetProperties() {
		msg.Headers[k] = v
	}
	return msg
}

// newFilterExpression builds a v5 FilterExpression from the option fields.
// Empty expression subscribes to all ("*"); otherwise the configured type
// ("tag" default, or "sql") is honored.
func newFilterExpression(expression, filterType string) *rmq.FilterExpression {
	if expression == "" {
		return rmq.SUB_ALL
	}
	if filterType == "sql" {
		return rmq.NewFilterExpressionWithType(expression, rmq.SQL92)
	}
	return rmq.NewFilterExpression(expression)
}
