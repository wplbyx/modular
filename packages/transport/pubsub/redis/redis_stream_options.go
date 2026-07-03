package redis

import (
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// StreamOptions configures a StreamClient.
type StreamOptions struct {
	// Client is the injected go-redis client. Required.
	Client goredis.UniversalClient

	// Group is the consumer group used by XREADGROUP. Required at construction
	// unless the caller supplies WithQueueName on every Subscribe call. A
	// per-Subscribe QueueName overrides this value.
	Group string

	// Consumer is the consumer name within the group. Defaults to a generated
	// value when empty.
	Consumer string

	// StartID is the stream ID passed to XGROUP CREATE as the starting point
	// for the group. Defaults to "$" (only new messages).
	StartID string

	// Block is the XREADGROUP BLOCK duration. Defaults to 1s.
	Block time.Duration

	// Count is the per-XREADGROUP count. Defaults to 100.
	Count int64

	// Workers is the number of concurrent consume loops. Defaults to 1.
	Workers int

	// MaxLen trims the stream on publish via XADD MAXLEN ~ N when > 0.
	MaxLen int64

	// MaxRetries is the number of handler retries before a message is acked and
	// (if configured) diverted to DLQStream. Defaults to 0 (no retries).
	MaxRetries int

	// RetryBackoff is the sleep between handler retries. Defaults to 100ms.
	RetryBackoff time.Duration

	// DLQStream is the dead-letter stream for messages that exhaust retries.
	// When empty, such messages are acked and dropped.
	DLQStream string
}

// StreamOption is a function that configures StreamOptions.
type StreamOption func(*StreamOptions)

// DefaultStreamOptions returns StreamOptions with sensible defaults.
func DefaultStreamOptions() *StreamOptions {
	return &StreamOptions{
		StartID:      "$",
		Block:        1 * time.Second,
		Count:        100,
		Workers:      1,
		RetryBackoff: 100 * time.Millisecond,
	}
}

// WithStreamClient sets the injected go-redis client.
func WithStreamClient(c goredis.UniversalClient) StreamOption {
	return func(o *StreamOptions) {
		o.Client = c
	}
}

// WithGroup sets the consumer group.
func WithGroup(group string) StreamOption {
	return func(o *StreamOptions) {
		o.Group = group
	}
}

// WithConsumer sets the consumer name within the group.
func WithConsumer(consumer string) StreamOption {
	return func(o *StreamOptions) {
		o.Consumer = consumer
	}
}

// WithStartID sets the group's starting stream ID ("$", "0", or an explicit ID).
func WithStartID(id string) StreamOption {
	return func(o *StreamOptions) {
		o.StartID = id
	}
}

// WithBlock sets the XREADGROUP BLOCK duration.
func WithBlock(d time.Duration) StreamOption {
	return func(o *StreamOptions) {
		o.Block = d
	}
}

// WithCount sets the per-XREADGROUP count.
func WithCount(n int64) StreamOption {
	return func(o *StreamOptions) {
		o.Count = n
	}
}

// WithWorkers sets the number of concurrent consume loops.
func WithWorkers(n int) StreamOption {
	return func(o *StreamOptions) {
		o.Workers = n
	}
}

// WithMaxLen sets the approximate MAXLEN trimming applied on publish.
func WithMaxLen(n int64) StreamOption {
	return func(o *StreamOptions) {
		o.MaxLen = n
	}
}

// WithStreamRetries sets handler retry behavior before a message is acked and
// (if configured) diverted to DLQStream.
func WithStreamRetries(maxRetries int, backoff time.Duration) StreamOption {
	return func(o *StreamOptions) {
		o.MaxRetries = maxRetries
		if backoff > 0 {
			o.RetryBackoff = backoff
		}
	}
}

// WithDLQStream configures a dead-letter stream for messages that exhaust
// retries.
func WithDLQStream(name string) StreamOption {
	return func(o *StreamOptions) {
		o.DLQStream = name
	}
}
