package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/wplbyx/modular/packages/log"
	"github.com/wplbyx/modular/packages/transport/pubsub"
)

// Reserved stream field keys used to carry pubsub.Message metadata inside an
// XMessage's Values map. Avoid colliding with user-supplied header names.
const (
	streamFieldPayload = "payload"
	streamFieldKey     = "key"

	// streamIDHeader carries the XMessage entry ID into pubsub.Message.Headers.
	streamIDHeader = "x-stream-id"
)

// busyGroupErr is the substring Redis returns when XGROUP CREATE targets a
// group that already exists.
const busyGroupErr = "BUSYGROUP"

// StreamClient 使用 Redis Streams 实现 pubsub.Client (发布/订阅) 接口
// (XADD / XREADGROUP / XACK)。它提供可靠的至少一次消息传递：
//
//	消息会被持久化，在处理程序成功后进行确认，在处理程序失败时重试
//	 并且（可选地）在重试次数用尽时重定向到死信流, 通过 Redis 消费者组实现跨实例的负载均衡。
//
// 使用共享的 SubscriberEndpoint：
//
//	sc, _ := redis.NewStreamClient(
//	    redis.WithStreamClient(rds),
//	    redis.WithGroup("events-group"),
//	)
//	ep := pubsub.NewSubscriberEndpoint(
//	    "stream-events",
//	    sc,
//	    "events-stream",
//	    myHandler,
//	    pubsub.WithConnect(sc.Connect),
//	    pubsub.WithDisconnect(sc.Disconnect),
//	)
//	app.WithEndpoint(ep)
//
// 对于无需持久化的“即发即弃”语义，请使用 redis_pubsub.go 中的 ChannelClient。
type StreamClient struct {
	client goredis.UniversalClient
	opts   *StreamOptions

	mu        sync.RWMutex
	cancel    context.CancelFunc
	connected bool

	wg sync.WaitGroup // consume loops
}

// Ensure StreamClient implements pubsub.Client.
var _ pubsub.Client = (*StreamClient)(nil)

// NewStreamClient creates a pubsub.Client backed by Redis Streams.
// It returns an error if no Redis client is provided.
func NewStreamClient(opts ...StreamOption) (*StreamClient, error) {
	o := DefaultStreamOptions()
	for _, opt := range opts {
		opt(o)
	}
	if o.Client == nil {
		return nil, fmt.Errorf("redis client is required")
	}
	if o.Group == "" {
		return nil, fmt.Errorf("redis stream consumer group is required")
	}
	if o.Consumer == "" {
		o.Consumer = defaultConsumerName()
	}
	if o.Workers <= 0 {
		o.Workers = 1
	}
	return &StreamClient{
		client: o.Client,
		opts:   o,
	}, nil
}

// Connect pings the Redis server and marks the client connected. It does not
// create the consumer group here: a group is bound to a specific stream, so it
// is created lazily inside Subscribe.
func (c *StreamClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}
	if err := c.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis stream client ping: %w", err)
	}
	c.connected = true
	log.Info(ctx, "Redis Stream connected")
	return nil
}

// Disconnect marks the client disconnected and cancels any active consume
// loops. It does not close the injected Redis client.
func (c *StreamClient) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.connected = false
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
	return nil
}

// IsConnected reports whether Connect has succeeded.
func (c *StreamClient) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// Publish appends a message to a stream via XADD. Key/Headers from
// pubsub.PublishOption are carried as stream fields; QoS/Retained are ignored.
// When MaxLen > 0 the stream is trimmed approximately to that length.
func (c *StreamClient) Publish(ctx context.Context, topic string, payload []byte, opts ...pubsub.PublishOption) error {
	publishOpts, err := pubsub.ResolvePublishOptions(ctx, pubsub.PublishOptions{}, opts...)
	if err != nil {
		return fmt.Errorf("inject Redis Stream metadata: %w", err)
	}

	args := &goredis.XAddArgs{
		Stream: topic,
		ID:     "*",
		Values: buildStreamValues(publishOpts.Key, publishOpts.Headers, payload),
	}
	if c.opts.MaxLen > 0 {
		args.MaxLen = c.opts.MaxLen
		args.Approx = true
	}

	if err := c.client.XAdd(ctx, args).Err(); err != nil {
		return fmt.Errorf("redis stream XADD to %s: %w", topic, err)
	}
	return nil
}

// Subscribe creates the consumer group for the stream (if missing) and starts
// the configured number of consume loops. It returns immediately; the
// SubscriberEndpoint adapter is responsible for blocking.
//
// A per-call WithQueueName overrides the configured group. The consumer name is
// shared across all workers of this client.
func (c *StreamClient) Subscribe(ctx context.Context, topic string, handler pubsub.MessageHandler, opts ...pubsub.SubscribeOption) error {
	handler = pubsub.WithMessageMetadata(handler)
	subscribeOpts := &pubsub.SubscribeOptions{}
	for _, opt := range opts {
		opt(subscribeOpts)
	}

	group := c.opts.Group
	if subscribeOpts.QueueName != "" {
		group = subscribeOpts.QueueName
	}
	if group == "" {
		return fmt.Errorf("redis stream consumer group is required")
	}

	if err := c.ensureGroup(ctx, topic, group); err != nil {
		return err
	}

	subCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()

	workers := c.opts.Workers
	for i := 0; i < workers; i++ {
		c.wg.Add(1)
		go c.consumeLoop(subCtx, i, topic, group, handler)
	}

	log.Info(ctx, "Redis Stream subscribed",
		zap.String("stream", topic),
		zap.String("group", group),
		zap.String("consumer", c.opts.Consumer),
		zap.Int("workers", workers),
	)
	return nil
}

// ensureGroup creates the consumer group for the stream, ignoring the
// BUSYGROUP error that indicates it already exists.
func (c *StreamClient) ensureGroup(ctx context.Context, stream, group string) error {
	startID := c.opts.StartID
	if startID == "" {
		startID = "$"
	}
	if err := c.client.XGroupCreateMkStream(ctx, stream, group, startID).Err(); err != nil {
		if isBusyGroup(err) {
			return nil
		}
		return fmt.Errorf("redis stream XGROUP CREATE %s group %s: %w", stream, group, err)
	}
	return nil
}

func (c *StreamClient) consumeLoop(ctx context.Context, workerID int, stream, group string, handler pubsub.MessageHandler) {
	defer c.wg.Done()
	log.Info(ctx, "Redis Stream worker started", zap.Int("worker_id", workerID), zap.String("stream", stream))

	args := &goredis.XReadGroupArgs{
		Group:    group,
		Consumer: c.opts.Consumer,
		Streams:  []string{stream, ">"},
		Count:    c.opts.Count,
		Block:    c.opts.Block,
	}

	for {
		select {
		case <-ctx.Done():
			log.Info(ctx, "Redis Stream worker stopping", zap.Int("worker_id", workerID), zap.String("stream", stream))
			return
		default:
		}

		streams, err := c.client.XReadGroup(ctx, args).Result()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// redis.Nil is returned when BLOCK times out with no new messages.
			if errors.Is(err, goredis.Nil) {
				continue
			}
			log.Error(ctx, "Redis Stream XREADGROUP failed", zap.Int("worker_id", workerID), zap.Error(err))
			if !sleepWithContext(ctx, 10*time.Millisecond) {
				return
			}
			continue
		}

		for _, s := range streams {
			for _, m := range s.Messages {
				c.handleMessage(ctx, workerID, stream, group, handler, m)
			}
		}
	}
}

// handleMessage retries the handler, acks on success, and diverts to the DLQ
// (when configured) once retries are exhausted.
func (c *StreamClient) handleMessage(ctx context.Context, workerID int, stream, group string, handler pubsub.MessageHandler, m goredis.XMessage) {
	message := streamMessageToMessage(stream, m)

	var handlerErr error
	for attempt := 0; attempt <= c.opts.MaxRetries; attempt++ {
		handlerErr = handler(ctx, message)
		if handlerErr == nil {
			c.ack(ctx, workerID, stream, group, m.ID)
			return
		}
		if attempt < c.opts.MaxRetries {
			log.Warn(ctx, "Redis Stream handler failed; retrying",
				zap.Int("worker_id", workerID),
				zap.String("stream", stream),
				zap.String("message_id", m.ID),
				zap.Int("attempt", attempt+1),
				zap.Int("max_retries", c.opts.MaxRetries),
				zap.Error(handlerErr),
			)
			if !sleepWithContext(ctx, c.opts.RetryBackoff) {
				return
			}
		}
	}

	log.Warn(ctx, "Redis Stream handler exhausted retries",
		zap.Int("worker_id", workerID),
		zap.String("stream", stream),
		zap.String("message_id", m.ID),
		zap.Error(handlerErr),
	)

	if c.opts.DLQStream != "" {
		if err := c.sendToDLQ(ctx, stream, m, handlerErr); err != nil {
			log.Error(ctx, "Redis Stream DLQ publish failed", zap.Int("worker_id", workerID), zap.String("message_id", m.ID), zap.Error(err))
			// Do not ack: leave the entry pending so it can be reclaimed.
			return
		}
	}
	c.ack(ctx, workerID, stream, group, m.ID)
}

func (c *StreamClient) ack(ctx context.Context, workerID int, stream, group, id string) {
	// TODO: 如果ack失败呢？
	if err := c.client.XAck(ctx, stream, group, id).Err(); err != nil {
		log.Error(ctx, "Redis Stream XACK failed", zap.Int("worker_id", workerID), zap.String("stream", stream), zap.String("message_id", id), zap.Error(err))
	}
}

func (c *StreamClient) sendToDLQ(ctx context.Context, stream string, m goredis.XMessage, reason error) error {
	values := make(map[string]interface{}, len(m.Values)+3)
	for k, v := range m.Values {
		values[k] = v
	}
	values["x-original-stream"] = stream
	values[streamIDHeader] = m.ID
	values["x-error"] = reason.Error()

	return c.client.XAdd(ctx, &goredis.XAddArgs{
		Stream: c.opts.DLQStream,
		ID:     "*",
		Values: values,
	}).Err()
}

// Unsubscribe is a no-op for streams: the consumer group persists on the
// server. Stopping the consume loops happens via Close/Disconnect.
func (c *StreamClient) Unsubscribe(ctx context.Context, topic string) error {
	log.Info(ctx, "Redis Stream unsubscribe requested; use Close to stop consuming", zap.String("stream", topic))
	return nil
}

// Close cancels the consume loops and waits for them to finish. It does not
// close the injected Redis client.
func (c *StreamClient) Close() error {
	_ = c.Disconnect(context.Background())
	c.wg.Wait()
	return nil
}

// streamMessageToMessage converts a Redis XMessage into a pubsub.Message.
// The entry ID is carried under the streamIDHeader key in Headers.
func streamMessageToMessage(stream string, m goredis.XMessage) pubsub.Message {
	msg := pubsub.Message{
		Topic:   stream,
		Headers: make(map[string]string, len(m.Values)),
	}
	for k, v := range m.Values {
		switch k {
		case streamFieldPayload:
			msg.Payload = []byte(toString(v))
		case streamFieldKey:
			msg.Key = toString(v)
		default:
			msg.Headers[k] = toString(v)
		}
	}
	// Make the entry ID visible to handlers for idempotency / correlation.
	msg.Headers[streamIDHeader] = m.ID
	return msg
}

// buildStreamValues converts pubsub publish fields into the Values map for
// XADD. Reserved fields (payload, key) are set first; user headers are then
// merged, but a header that collides with a reserved name is ignored to avoid
// overwriting the message body.
func buildStreamValues(key string, headers map[string]string, payload []byte) map[string]interface{} {
	values := make(map[string]interface{}, len(headers)+2)
	values[streamFieldPayload] = string(payload)
	if key != "" {
		values[streamFieldKey] = key
	}
	for k, v := range headers {
		if k == streamFieldPayload || k == streamFieldKey {
			continue
		}
		values[k] = v
	}
	return values
}

// isBusyGroup reports whether err is the Redis BUSYGROUP error indicating the
// consumer group already exists.
func isBusyGroup(err error) bool {
	return err != nil && strings.Contains(err.Error(), busyGroupErr)
}

// toString coerces an XMessage Values element (interface{}) to a string.
func toString(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	case fmt.Stringer:
		return s.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// defaultConsumerName generates a per-client consumer identifier.
func defaultConsumerName() string {
	return fmt.Sprintf("consumer-%d", time.Now().UnixNano())
}

// sleepWithContext sleeps for d while respecting context cancellation.
// Returns false if the context was cancelled before the timer fired.
func sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
