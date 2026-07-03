package redis

import (
	goredis "github.com/redis/go-redis/v9"
)

// ChannelOptions configures a ChannelClient.
type ChannelOptions struct {
	// Client is the injected go-redis client. Required.
	Client goredis.UniversalClient

	// ChannelSize is the buffer size of the go-redis PubSub message channel.
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
		ChannelSize: 100,
	}
}

// WithChannelClient sets the injected go-redis client.
func WithChannelClient(c goredis.UniversalClient) ChannelOption {
	return func(o *ChannelOptions) {
		o.Client = c
	}
}

// WithChannelSize sets the PubSub message-channel buffer size.
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
