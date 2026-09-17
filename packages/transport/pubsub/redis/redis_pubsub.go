package redis

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/wplbyx/modular/packages/log"
	"github.com/wplbyx/modular/packages/transport/pubsub"
	"github.com/wplbyx/modular/packages/transport/pubsub/internal/delivery"
)

// ChannelClient 使用 Redis pubsub.Client 实现 发布/订阅 接口
// (SUBSCRIBE / PSUBSCRIBE + PUBLISH)。
//
//	语义：
//	即发即弃。没有确认机制，没有持久化，并且 没有消费者组负载均衡。 订阅者离线时发布的消息将丢失。
//	消息通过有界队列交给固定 worker；处理失败会记录并计数，不重试。
//
//	使用共享的 SubscriberEndpoint：
//
//	rc, _ := redis.NewChannelClient(redis.WithChannelClient(rds))
//	ep := pubsub.NewSubscriberEndpoint(
//	    "ch-events",
//	    rc,
//	    "events",
//	    myHandler,
//	    pubsub.WithConnect(rc.Connect),
//	    pubsub.WithDisconnect(rc.Disconnect),
//	)
//	app.WithEndpoint(ep)
//
// 为了确保可靠的数据传递（持久化、确认、消费者组、重试、死信队列），请改用 redis_stream.go 中的 StreamClient。
type ChannelClient struct {
	client  goredis.UniversalClient
	options *ChannelOptions

	mu         sync.RWMutex
	pubsub     *goredis.PubSub // active subscription, nil when none
	connected  bool
	subscribed bool

	queue    *delivery.Queue
	closed   bool
	stop     chan struct{}
	done     chan struct{}
	closeErr error
}

var _ pubsub.Client = (*ChannelClient)(nil)

// NewChannelClient creates a pubsub.Client backed by Redis Pub/Sub channels.
// It returns an error if no Redis client is provided.
func NewChannelClient(opts ...ChannelOption) (*ChannelClient, error) {
	o := DefaultChannelOptions()
	for _, opt := range opts {
		opt(o)
	}
	if o.Client == nil {
		return nil, fmt.Errorf("redis client is required")
	}
	return &ChannelClient{
		client:  o.Client,
		options: o, stop: make(chan struct{}), done: make(chan struct{}),
	}, nil
}

// Connect pings the Redis server and marks the client connected.
func (c *ChannelClient) Connect(ctx context.Context) error {
	c.mu.RLock()
	closed, connected := c.closed, c.connected
	c.mu.RUnlock()
	if closed {
		return delivery.ErrClosed
	}
	if connected {
		return nil
	}
	if err := c.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis channel client ping: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return delivery.ErrClosed
	}
	c.connected = true
	return nil
}

// Disconnect closes the active subscription (if any) and marks the client
// disconnected. It does not close the injected Redis client. It blocks until
// accepted work drains or the deadline expires. Only successful shutdown
// guarantees no business handler remains in flight.
func (c *ChannelClient) Disconnect(ctx context.Context) error { return c.CloseContext(ctx) }

func (c *ChannelClient) closeTimeout() time.Duration {
	if c.options.CloseTimeout <= 0 {
		return 30 * time.Second
	}
	return c.options.CloseTimeout
}
func (c *ChannelClient) CloseContext(ctx context.Context) error {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		c.connected = false
		close(c.stop)
		ps, q := c.pubsub, c.queue
		if q != nil {
			q.Stop()
		}
		go func() {
			var err error
			if ps != nil {
				err = ps.Close()
				if errors.Is(err, goredis.ErrClosed) {
					err = nil
				}
			}
			budget, cancel := context.WithTimeout(context.Background(), c.closeTimeout())
			defer cancel()
			if q != nil {
				err = errors.Join(err, q.Close(budget))
			}
			c.mu.Lock()
			c.closeErr = err
			c.mu.Unlock()
			close(c.done)
		}()
	}
	q := c.queue
	c.mu.Unlock()
	select {
	case <-c.done:
		c.mu.RLock()
		defer c.mu.RUnlock()
		return c.closeErr
	case <-ctx.Done():
		if q != nil {
			_ = q.Close(ctx)
		}
		return ctx.Err()
	}
}
func (c *ChannelClient) Stats() pubsub.DeliveryStats {
	c.mu.RLock()
	q := c.queue
	c.mu.RUnlock()
	if q == nil {
		return pubsub.DeliveryStats{}
	}
	return q.Stats()
}

// IsConnected reports whether Connect has succeeded.
func (c *ChannelClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// Publish publishes a payload to a channel. QoS/Retained/Key/Headers from
// pubsub.PublishOption are ignored: Redis Pub/Sub carries only a channel name
// and a payload.
func (c *ChannelClient) Publish(ctx context.Context, topic string, payload []byte, opts ...pubsub.PublishOption) error {
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return delivery.ErrClosed
	}
	if err := c.client.Publish(ctx, topic, payload).Err(); err != nil {
		return fmt.Errorf("redis pub/sub publish to %s: %w", topic, err)
	}
	return nil
}

// Subscribe subscribes to a channel (or glob pattern when WithChannelPattern
// is set) and dispatches messages through a bounded worker queue.
// It returns immediately after the subscription is registered; the
// SubscriberEndpoint adapter is responsible for blocking.
func (c *ChannelClient) Subscribe(ctx context.Context, topic string, handler pubsub.MessageHandler, opts ...pubsub.SubscribeOption) error {
	if topic == "" || handler == nil {
		return fmt.Errorf("topic and handler are required")
	}
	handler = pubsub.WithMessageMetadata(handler)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return delivery.ErrClosed
	}
	if c.subscribed {
		c.mu.Unlock()
		return fmt.Errorf("channel client already subscribed")
	}
	c.subscribed = true
	size := c.options.ChannelSize
	if size <= 0 {
		size = 100
	}
	q := delivery.NewQueue(c.options.Workers, size)
	c.queue = q
	c.mu.Unlock()
	var ps *goredis.PubSub
	if c.options.Pattern {
		ps = c.client.PSubscribe(ctx, topic)
	} else {
		ps = c.client.Subscribe(ctx, topic)
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = ps.Close()
		return delivery.ErrClosed
	}
	c.pubsub = ps
	c.mu.Unlock()
	if _, err := ps.Receive(ctx); err != nil {
		expired, cancel := context.WithCancel(context.Background())
		cancel()
		_ = c.CloseContext(expired)
		return fmt.Errorf("redis subscribe %s: %w", topic, err)
	}
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return delivery.ErrClosed
	}
	go func() {
		stop := context.AfterFunc(ctx, func() { _ = ps.Close(); q.Stop() })
		defer stop()
		for {
			msg, err := ps.ReceiveMessage(ctx)
			if err != nil {
				select {
				case <-c.stop:
					return
				case <-ctx.Done():
					return
				default:
				}
				if !delivery.WaitRetry(ctx, 100*time.Millisecond, 0) {
					return
				}
				continue
			}
			message := pubsub.Message{Topic: msg.Channel, Payload: []byte(msg.Payload)}
			if err = q.Submit(ctx, func(ctx context.Context) error {
				err := delivery.CallHandler(ctx, handler, message)
				if err != nil {
					log.Warn(ctx, "Redis Pub/Sub handler failed", zap.String("channel", message.Topic), zap.Error(err))
				}
				return err
			}, nil, false); err != nil {
				return
			}
		}
	}()
	return nil
}

// Unsubscribe unsubscribes from the given channel (or pattern). The dispatch
// goroutine drains once the PubSub message channel is closed.
func (c *ChannelClient) Unsubscribe(ctx context.Context, topic string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.pubsub == nil {
		return nil
	}

	var err error
	if c.options.Pattern {
		err = c.pubsub.PUnsubscribe(ctx, topic)
	} else {
		err = c.pubsub.Unsubscribe(ctx, topic)
	}
	if err != nil {
		return fmt.Errorf("redis pub/sub unsubscribe from %s: %w", topic, err)
	}
	log.Info(ctx, "Redis Pub/Sub unsubscribed", zap.String("topic", topic))
	return nil
}

// Close closes the active subscription and waits for in-flight dispatch
// goroutines to finish. It does not close the injected Redis client.
func (c *ChannelClient) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), c.closeTimeout())
	defer cancel()
	return c.CloseContext(ctx)
}
