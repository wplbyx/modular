package rocket

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	rmq "github.com/apache/rocketmq-clients/golang/v5"
	"go.uber.org/zap"

	"github.com/wplbyx/modular/packages/log"
	"github.com/wplbyx/modular/packages/transport/pubsub"
	"github.com/wplbyx/modular/packages/transport/pubsub/internal/delivery"
)

// Ensure PushConsumer implements pubsub.Subscriber.
var _ pubsub.Subscriber = (*PushConsumer)(nil)

// Ensure PushConsumer implements io.Closer.
var _ io.Closer = (*PushConsumer)(nil)

// PushConsumer implements pubsub.Subscriber using a RocketMQ 5.x PushConsumer
// (the Push delivery model).
//
// The v5 PushConsumer requires, at construction time, both a message listener
// and at least one initial subscription. The first Subscribe installs the
// initial handler before creating and starting the SDK. Additional topics
// can be added at runtime through Subscribe.
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
	pc       pushClient
	opts     *ConsumerOptions
	mu       sync.RWMutex
	closed   bool
	failed   bool
	gate     chan struct{}
	stop     chan struct{}
	done     chan struct{}
	ctx      context.Context
	cancel   context.CancelFunc
	closeErr error
	stats    pubsub.DeliveryStats
	factory  func(*rmq.Config, ...rmq.PushConsumerOption) (pushClient, error)

	// handlers maps topic -> handler. The dispatch listener reads from here for
	// every delivered message.
	handlers sync.Map
	active   sync.WaitGroup
}

// NewPushConsumer validates configuration. The first Subscribe installs its
// handler before constructing and starting the SDK consumer.
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

	ctx, cancel := context.WithCancel(context.Background())
	c := &PushConsumer{opts: o, gate: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}), ctx: ctx, cancel: cancel,
		factory: func(cfg *rmq.Config, opts ...rmq.PushConsumerOption) (pushClient, error) {
			return rmq.NewPushConsumer(cfg, opts...)
		}}
	return c, nil
}

// pushClient is the SDK boundary used by lifecycle regression tests.
type pushClient interface {
	Start() error
	GracefulStop() error
	Subscribe(string, *rmq.FilterExpression) error
	Unsubscribe(string) error
}

func (c *PushConsumer) start() (startErr error) {
	defer func() {
		if startErr != nil {
			c.mu.Lock()
			c.failed = true
			c.mu.Unlock()
		}
	}()
	o := c.opts
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

	pc, err := c.factory(&rmq.Config{
		Endpoint:      o.Endpoint,
		NameSpace:     o.NameSpace,
		ConsumerGroup: o.Group,
		Credentials:   buildCredentials(o.AccessKey, o.AccessSecret),
	}, rmqOpts...)
	if err != nil {
		return fmt.Errorf("rocketmq new push consumer: %w", err)
	}
	c.pc = pc
	if err := pc.Start(); err != nil {
		return fmt.Errorf("rocketmq push consumer start: %w", err)
	}

	log.Info(context.Background(), "RocketMQ push consumer started",
		zap.String("endpoint", o.Endpoint),
		zap.String("group", o.Group),
		zap.String("topic", o.Topic),
	)
	return nil
}

// Subscribe registers a handler for a topic and records the runtime
// subscription with the broker. For the initial topic (ConsumerOptions.Topic)
// the subscription already exists; for additional topics this contacts the
// broker to add it. Subscribe returns immediately.
// operation admits at most one SDK operation, including one that outlives its caller.
func (c *PushConsumer) operation(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.stop:
		return delivery.ErrClosed
	}
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		<-c.gate
		return delivery.ErrClosed
	}
	done := make(chan error, 1)
	go func() { defer func() { <-c.gate }(); done <- fn() }()
	select {
	case err := <-done:
		if err == nil {
			c.mu.RLock()
			closed := c.closed
			c.mu.RUnlock()
			if closed {
				return delivery.ErrClosed
			}
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-c.stop:
		return delivery.ErrClosed
	}
}

type consumerHandler struct {
	ctx     context.Context
	handler pubsub.MessageHandler
}

func (c *PushConsumer) Subscribe(ctx context.Context, topic string, handler pubsub.MessageHandler, opts ...pubsub.SubscribeOption) error {
	if topic == "" || handler == nil {
		return fmt.Errorf("topic and handler are required")
	}
	err := c.operation(ctx, func() error {
		if c.pc == nil && topic != c.opts.Topic {
			return fmt.Errorf("first subscription must match initial topic %s", c.opts.Topic)
		}
		if _, exists := c.handlers.Load(topic); exists {
			return fmt.Errorf("topic already subscribed: %s", topic)
		}
		c.handlers.Store(topic, consumerHandler{context.WithoutCancel(ctx), pubsub.WithMessageMetadata(handler)})
		if c.pc == nil {
			return c.start()
		}
		if err := c.pc.Subscribe(topic, newFilterExpression(c.opts.FilterExpression, c.opts.FilterType)); err != nil {
			c.handlers.Delete(topic)
			return err
		}
		return nil
	})
	if err != nil && (ctx.Err() != nil || c.pcFailed()) {
		budget, cancel := context.WithCancel(context.Background())
		cancel()
		_ = c.CloseContext(budget)
	}
	return err
}

// pcFailed records a failed initial SDK construction or start.
func (c *PushConsumer) pcFailed() bool { c.mu.RLock(); defer c.mu.RUnlock(); return c.failed }

func (c *PushConsumer) Unsubscribe(ctx context.Context, topic string) error {
	return c.operation(ctx, func() error {
		if c.pc == nil {
			return nil
		}
		if err := c.pc.Unsubscribe(topic); err != nil {
			return err
		}
		c.handlers.Delete(topic)
		return nil
	})
}

func (c *PushConsumer) closeTimeout() time.Duration {
	if c.opts.CloseTimeout <= 0 {
		return 30 * time.Second
	}
	return c.opts.CloseTimeout
}
func (c *PushConsumer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), c.closeTimeout())
	defer cancel()
	return c.CloseContext(ctx)
}
func (c *PushConsumer) CloseContext(ctx context.Context) error {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.stop)
		go func() {
			timer := time.AfterFunc(c.closeTimeout(), c.cancel)
			defer timer.Stop()
			c.gate <- struct{}{}
			var err error
			if c.pc != nil {
				err = c.pc.GracefulStop()
			}
			c.active.Wait()
			<-c.gate
			c.cancel()
			c.mu.Lock()
			c.closeErr = err
			c.mu.Unlock()
			close(c.done)
		}()
	}
	c.mu.Unlock()
	select {
	case <-c.done:
		c.mu.RLock()
		defer c.mu.RUnlock()
		return c.closeErr
	case <-ctx.Done():
		c.cancel()
		return ctx.Err()
	}
}
func (c *PushConsumer) Stats() pubsub.DeliveryStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stats
}

// dispatch is the single message listener registered with the PushConsumer. It
// looks up the handler for the message's topic and invokes it. A nil handler
// error acks the message (SUCCESS); an error schedules a retry (FAILURE).
func (c *PushConsumer) dispatch(mv *rmq.MessageView) rmq.ConsumerResult {
	if mv.GetDeliveryAttempt() > 1 {
		c.mu.Lock()
		c.stats.Retried++
		c.mu.Unlock()
	}
	return c.deliver(messageFromMessageView(mv))
}

func (c *PushConsumer) deliver(message pubsub.Message) rmq.ConsumerResult {
	c.mu.Lock()
	if c.closed {
		c.stats.Canceled++
		c.mu.Unlock()
		return rmq.FAILURE
	}
	c.stats.Active++
	c.active.Add(1)
	c.mu.Unlock()
	defer c.active.Done()
	defer func() { c.mu.Lock(); c.stats.Active--; c.mu.Unlock() }()
	val, ok := c.handlers.Load(message.Topic)
	if !ok {
		c.mu.Lock()
		c.stats.Failed++
		c.mu.Unlock()
		return rmq.FAILURE
	}
	sub := val.(consumerHandler)
	ctx, cancel := context.WithCancel(sub.ctx)
	stop := context.AfterFunc(c.ctx, cancel)
	defer stop()
	defer cancel()
	err := delivery.CallHandler(ctx, sub.handler, message)
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil || c.ctx.Err() != nil {
		c.stats.Canceled++
		return rmq.FAILURE
	}
	if err != nil {
		c.stats.Failed++
		log.Warn(ctx, "RocketMQ handler failed", zap.String("topic", message.Topic), zap.Error(err))
		return rmq.FAILURE
	}
	c.stats.Succeeded++
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
