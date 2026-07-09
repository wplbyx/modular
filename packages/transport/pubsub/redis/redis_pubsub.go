package redis

import (
	"context"
	"fmt"
	"sync"

	goredis "github.com/redis/go-redis/v9"

	"github.com/wplbyx/modular/packages/log"
	"github.com/wplbyx/modular/packages/transport/pubsub"
)

// ChannelClient 使用 Redis pubsub.Client 实现 发布/订阅 接口
// (SUBSCRIBE / PSUBSCRIBE + PUBLISH)。
//
//	语义：
//	即发即弃。没有确认机制，没有持久化，并且 没有消费者组负载均衡。 订阅者离线时发布的消息将丢失。
//	每条消息都会被分发到其自身的 goroutine 中处理；处理程序错误会被记录，但其他情况会被忽略。
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

	mu        sync.RWMutex
	pubsub    *goredis.PubSub // active subscription, nil when none
	connected bool

	wg sync.WaitGroup // message-dispatch goroutines
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
		options: o,
	}, nil
}

// Connect pings the Redis server and marks the client connected.
func (c *ChannelClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}
	if err := c.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis channel client ping: %w", err)
	}
	c.connected = true
	log.Info("[Redis Pub/Sub] connected")
	return nil
}

// Disconnect closes the active subscription (if any) and marks the client
// disconnected. It does not close the injected Redis client. It blocks until
// all dispatch goroutines have drained, so callers can be sure no handler is
// in flight when it returns.
func (c *ChannelClient) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	c.connected = false
	var err error
	if c.pubsub != nil {
		err = c.pubsub.Close()
		c.pubsub = nil
	}
	c.mu.Unlock()

	// Wait for goroutines outside the lock: a dispatch/consume goroutine may
	// otherwise be blocked trying to acquire c.mu in Subscribe/Unsubscribe,
	// which would deadlock.
	c.wg.Wait()
	return err
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
	if err := c.client.Publish(ctx, topic, payload).Err(); err != nil {
		return fmt.Errorf("redis pub/sub publish to %s: %w", topic, err)
	}
	return nil
}

// Subscribe subscribes to a channel (or glob pattern when WithChannelPattern
// is set) and dispatches each message to the handler on its own goroutine.
// It returns immediately after the subscription is registered; the
// SubscriberEndpoint adapter is responsible for blocking.
func (c *ChannelClient) Subscribe(ctx context.Context, topic string, handler pubsub.MessageHandler, opts ...pubsub.SubscribeOption) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.pubsub != nil {
		// Close a previous subscription before opening a new one; a single
		// ChannelClient maintains at most one active subscription.
		_ = c.pubsub.Close()
	}

	var ps *goredis.PubSub
	if c.options.Pattern {
		ps = c.client.PSubscribe(ctx, topic)
	} else {
		ps = c.client.Subscribe(ctx, topic)
	}
	if err := ps.Ping(ctx); err != nil {
		_ = ps.Close()
		return fmt.Errorf("redis pub/sub subscribe to %s: %w", topic, err)
	}
	c.pubsub = ps

	size := c.options.ChannelSize
	if size <= 0 {
		size = 100
	}
	msgCh := ps.Channel(goredis.WithChannelSize(size))

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()

		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-msgCh:
				if !ok {
					// Channel closed (e.g. PubSub closed during shutdown).
					return
				}
				c.dispatch(ctx, handler, msg)
			}
		}
	}()

	log.Infof("[Redis Pub/Sub] subscribed to %s", topic)
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
	log.Infof("[Redis Pub/Sub] unsubscribed from %s", topic)
	return nil
}

// Close closes the active subscription and waits for in-flight dispatch
// goroutines to finish. It does not close the injected Redis client.
func (c *ChannelClient) Close() error {
	return c.Disconnect(context.Background())
}

// dispatch invokes the handler on a fresh goroutine, mirroring the MQTT client.
// Handler errors are logged but do not stop consumption: Redis Pub/Sub has no
// acknowledgement to retry against.
func (c *ChannelClient) dispatch(ctx context.Context, handler pubsub.MessageHandler, msg *goredis.Message) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		message := pubsub.Message{
			Topic:   msg.Channel,
			Payload: []byte(msg.Payload),
		}
		if err := handler(ctx, message); err != nil {
			log.Warnf("[Redis Pub/Sub] handler error for channel [%s]: %v", msg.Channel, err)
		}
	}()
}
