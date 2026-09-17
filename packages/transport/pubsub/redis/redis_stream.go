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
	"github.com/wplbyx/modular/packages/transport/pubsub/internal/delivery"
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
	started   bool
	closed    bool
	active    map[string]bool
	done      chan struct{}

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
	if o.Block <= 0 {
		o.Block = time.Second
	}
	if o.Workers <= 0 {
		o.Workers = 1
	}
	if o.RecoveryInterval <= 0 || o.ClaimMinIdle <= 0 || o.MaxRetries < 0 || o.Count < 1 {
		return nil, errors.New("invalid Redis Stream recovery, retry or batch settings")
	}
	return &StreamClient{
		active: make(map[string]bool), done: make(chan struct{}),
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

	if c.closed {
		return errors.New("Redis Stream client is closed")
	}
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
	if handler == nil || topic == "" {
		return errors.New("stream and handler are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.started {
		return errors.New("Redis Stream client is closed or already subscribed")
	}
	subscribeOpts := &pubsub.SubscribeOptions{}
	for _, opt := range opts {
		opt(subscribeOpts)
	}
	group := c.opts.Group
	if subscribeOpts.QueueName != "" {
		group = subscribeOpts.QueueName
	}
	if err := c.ensureGroup(ctx, topic, group); err != nil {
		return err
	}
	subCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.started = true
	queue := make(chan goredis.XMessage, c.opts.Workers)
	handler = pubsub.WithMessageMetadata(handler)
	for worker := 0; worker < c.opts.Workers; worker++ {
		c.wg.Add(1)
		go func(id int) {
			defer c.wg.Done()
			for {
				select {
				case <-subCtx.Done():
					return
				case m := <-queue:
					c.handleMessage(subCtx, id, topic, group, handler, m)
					c.mu.Lock()
					delete(c.active, m.ID)
					c.mu.Unlock()
				}
			}
		}(worker)
	}
	c.wg.Add(2)
	go func() { defer c.wg.Done(); c.readMessages(subCtx, topic, group, queue) }()
	go func() { defer c.wg.Done(); c.recoverMessages(subCtx, topic, group, queue) }()
	go func() { c.wg.Wait(); close(c.done) }()
	return nil
}
func (c *StreamClient) schedule(ctx context.Context, queue chan<- goredis.XMessage, m goredis.XMessage) bool {
	c.mu.Lock()
	if c.active[m.ID] {
		c.mu.Unlock()
		return true
	}
	c.active[m.ID] = true
	c.mu.Unlock()
	select {
	case queue <- m:
		return true
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.active, m.ID)
		c.mu.Unlock()
		return false
	}
}
func (c *StreamClient) readMessages(ctx context.Context, stream, group string, queue chan<- goredis.XMessage) {
	cursor := "0"
	for attempt := 0; ctx.Err() == nil; {
		block := c.opts.Block
		if cursor != ">" {
			block = -1
		}
		streams, err := c.client.XReadGroup(ctx, &goredis.XReadGroupArgs{Group: group, Consumer: c.opts.Consumer, Streams: []string{stream, cursor}, Count: c.opts.Count, Block: block}).Result()
		if errors.Is(err, goredis.Nil) {
			if cursor != ">" {
				cursor = ">"
			}
			continue
		}
		if err != nil {
			if !delivery.WaitRetry(ctx, c.opts.RetryBackoff, attempt) {
				return
			}
			attempt++
			continue
		}
		attempt = 0
		count := 0
		for _, batch := range streams {
			for _, m := range batch.Messages {
				count++
				if cursor != ">" {
					cursor = m.ID
				}
				if !c.schedule(ctx, queue, m) {
					return
				}
			}
		}
		if count == 0 && cursor != ">" {
			cursor = ">"
		}
	}
}
func (c *StreamClient) recoverMessages(ctx context.Context, stream, group string, queue chan<- goredis.XMessage) {
	ticker := time.NewTicker(c.opts.RecoveryInterval)
	defer ticker.Stop()
	for {
		cursor := "0-0"
		for {
			messages, next, err := c.client.XAutoClaim(ctx, &goredis.XAutoClaimArgs{Stream: stream, Group: group, Consumer: c.opts.Consumer, MinIdle: c.opts.ClaimMinIdle, Start: cursor, Count: c.opts.Count}).Result()
			if err != nil {
				if ctx.Err() == nil {
					log.Error(ctx, "Redis Stream pending recovery failed", zap.Error(err))
				}
				break
			}
			for _, m := range messages {
				if !c.schedule(ctx, queue, m) {
					return
				}
			}
			if next == "0-0" || next == "" {
				break
			}
			cursor = next
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
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

func (c *StreamClient) handleMessage(ctx context.Context, workerID int, stream, group string, handler pubsub.MessageHandler, m goredis.XMessage) {
	message := streamMessageToMessage(stream, m)
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return
		}
		handlerErr := delivery.CallHandler(ctx, handler, message)
		if handlerErr == nil {
			break
		}
		log.Warn(ctx, "Redis Stream handler failed; retaining message", zap.String("message_id", m.ID), zap.Error(handlerErr))
		if attempt >= c.opts.MaxRetries && c.opts.DLQStream != "" {
			for n := 0; ; n++ {
				if err := c.sendToDLQ(ctx, stream, m, handlerErr); err == nil {
					break
				}
				if !delivery.WaitRetry(ctx, c.opts.RetryBackoff, n) {
					return
				}
			}
			break
		}
		if !delivery.WaitRetry(ctx, c.opts.RetryBackoff, attempt) {
			return
		}
	}
	for attempt := 0; ; attempt++ {
		if err := c.client.XAck(ctx, stream, group, m.ID).Err(); err == nil {
			return
		} else {
			log.Error(ctx, "Redis Stream XACK failed; retrying", zap.Error(err))
		}
		if !delivery.WaitRetry(ctx, c.opts.RetryBackoff, attempt) {
			return
		}
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
	_ = c.Disconnect(ctx)
	c.mu.RLock()
	started := c.started
	c.mu.RUnlock()
	if !started {
		return nil
	}
	select {
	case <-c.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops all consumption without closing the caller-owned Redis connection.
func (c *StreamClient) Close() error {
	c.mu.Lock()
	c.closed = true
	c.connected = false
	if c.cancel != nil {
		c.cancel()
	}
	started := c.started
	c.mu.Unlock()
	if started {
		<-c.done
	}
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
