package redis

import (
	"context"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/wplbyx/modular/packages/transport/pubsub/internal/delivery"
	"sync/atomic"
	"testing"
	"time"
)

type ownedClient struct {
	goredis.UniversalClient
	closes atomic.Int32
}

func (c *ownedClient) Close() error { c.closes.Add(1); return nil }
func TestChannelClient_CloseCancelsAndPreservesInjectedClient(t *testing.T) {
	sdk := &ownedClient{}
	c, err := NewChannelClient(WithChannelClient(sdk))
	require.NoError(t, err)
	c.queue = delivery.NewQueue(1, 1)
	entered := make(chan struct{})
	require.NoError(t, c.queue.Submit(context.Background(), func(ctx context.Context) error { close(entered); <-ctx.Done(); return nil }, nil, false))
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, c.CloseContext(ctx), context.DeadlineExceeded)
	require.NoError(t, c.Close())
	require.NoError(t, c.Close())
	require.Zero(t, sdk.closes.Load())
	require.Error(t, c.Connect(context.Background()))
	require.EqualValues(t, 1, c.Stats().Canceled)
}
