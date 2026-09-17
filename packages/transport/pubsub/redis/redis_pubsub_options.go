package redis

import (
	goredis "github.com/redis/go-redis/v9"
	"time"
)

// ChannelOptions configures a ChannelClient.
type ChannelOptions struct {
	Workers      int
	CloseTimeout time.Duration
	// Client is the injected go-redis client. Required.
	Client goredis.UniversalClient

	// ChannelSize is the capacity of the pending business-delivery queue.
	// Defaults to 100 when <= 0.
	ChannelSize int

	// Pattern selects glob-pattern subscription (PSubscribe) instead of exact
	// channel subscription (Subscribe).
	Pattern bool
}

// ChannelOption is a function that configures ChannelOptions.
type ChannelOption func(*ChannelOptions)

// DefaultChannelOptions returns ChannelOptions with sensible defaults.
func DefaultChannelOptions() *ChannelOptions {
	return &ChannelOptions{
		ChannelSize: 100, Workers: 8, CloseTimeout: 30 * time.Second,
	}
}

// WithChannelClient sets the injected go-redis client.
func WithChannelClient(c goredis.UniversalClient) ChannelOption {
	return func(o *ChannelOptions) {
		o.Client = c
	}
}

// WithChannelSize sets the pending business-delivery capacity.
func WithChannelSize(size int) ChannelOption {
	return func(o *ChannelOptions) {
		o.ChannelSize = size
	}
}

// WithChannelPattern selects glob-pattern (PSUBSCRIBE) subscription when
// pattern is true, or exact-channel (SUBSCRIBE) subscription otherwise.
func WithChannelPattern(pattern bool) ChannelOption {
	return func(o *ChannelOptions) {
		o.Pattern = pattern
	}
}

// WithChannelWorkers bounds concurrent business handlers.
func WithChannelWorkers(n int) ChannelOption { return func(o *ChannelOptions) { o.Workers = n } }

// WithChannelCloseTimeout sets the default drain deadline.
func WithChannelCloseTimeout(d time.Duration) ChannelOption {
	return func(o *ChannelOptions) { o.CloseTimeout = d }
}
