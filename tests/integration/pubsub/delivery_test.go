//go:build integration

package pubsub_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/wplbyx/modular/packages/transport/pubsub"
	"github.com/wplbyx/modular/packages/transport/pubsub/mqtt"
	redisbus "github.com/wplbyx/modular/packages/transport/pubsub/redis"
	"github.com/wplbyx/modular/packages/transport/pubsub/rocket"
)

func address(t *testing.T, key string) string {
	t.Helper()
	v := os.Getenv(key)
	require.NotEmpty(t, v, "integration tests require %s", key)
	return v
}
func await[T any](t *testing.T, ctx context.Context, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		var zero T
		return zero
	}
}

func TestMQTT_UnacknowledgedDeliverySurvivesReconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	broker := address(t, "MODULAR_TEST_MQTT")
	id := fmt.Sprintf("modular-%d", time.Now().UnixNano())
	topic := id + "/orders"
	opts := []mqtt.Option{mqtt.WithBrokerURL(broker), mqtt.WithClientID(id), func(o *mqtt.Options) { o.CleanSession = false }}
	first, err := mqtt.NewClient(opts...)
	require.NoError(t, err)
	defer first.Close()
	require.NoError(t, first.Connect(ctx))
	entered := make(chan struct{}, 1)
	require.NoError(t, first.Subscribe(ctx, topic, func(ctx context.Context, _ pubsub.Message) error {
		entered <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}, func(o *pubsub.SubscribeOptions) { o.QoS = 1 }))
	publisher, err := mqtt.NewClient(mqtt.WithBrokerURL(broker), mqtt.WithClientID(id+"-publisher"))
	require.NoError(t, err)
	defer publisher.Close()
	require.NoError(t, publisher.Connect(ctx))
	require.NoError(t, publisher.Publish(ctx, topic, []byte("order"), func(o *pubsub.PublishOptions) { o.QoS = 1 }))
	await(t, ctx, entered)
	expired, stop := context.WithCancel(context.Background())
	stop()
	require.Error(t, first.CloseContext(expired))
	require.NoError(t, first.Close())
	second, err := mqtt.NewClient(opts...)
	require.NoError(t, err)
	defer second.Close()
	require.NoError(t, second.Connect(ctx))
	received := make(chan string, 1)
	require.NoError(t, second.Subscribe(ctx, topic, func(_ context.Context, m pubsub.Message) error { received <- string(m.Payload); return nil }, func(o *pubsub.SubscribeOptions) { o.QoS = 1 }))
	require.Equal(t, "order", await(t, ctx, received))
	require.NoError(t, second.CloseContext(ctx))
	require.EqualValues(t, 1, second.Stats().Succeeded)
}

func TestRedis_BoundedDrainAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	r := goredis.NewClient(&goredis.Options{Addr: address(t, "MODULAR_TEST_REDIS")})
	defer r.Close()
	require.NoError(t, r.Ping(ctx).Err())
	c, err := redisbus.NewChannelClient(redisbus.WithChannelClient(r), redisbus.WithChannelWorkers(2), redisbus.WithChannelSize(2))
	require.NoError(t, err)
	defer c.Close()
	topic := fmt.Sprintf("modular-%d", time.Now().UnixNano())
	release := make(chan struct{})
	var active, max atomic.Int32
	require.NoError(t, c.Subscribe(ctx, topic, func(context.Context, pubsub.Message) error {
		n := active.Add(1)
		for old := max.Load(); n > old; old = max.Load() {
			if max.CompareAndSwap(old, n) {
				break
			}
		}
		<-release
		active.Add(-1)
		return nil
	}))
	for i := 0; i < 20; i++ {
		require.NoError(t, r.Publish(ctx, topic, "work").Err())
	}
	require.Eventually(t, func() bool { s := c.Stats(); return s.Active == 2 && s.Queued == 2 }, 3*time.Second, 10*time.Millisecond)
	closed := make(chan error, 1)
	go func() { closed <- c.CloseContext(ctx) }()
	close(release)
	require.NoError(t, await(t, ctx, closed))
	require.LessOrEqual(t, max.Load(), int32(2))
	require.Zero(t, c.Stats().Active)
	require.NoError(t, r.Ping(ctx).Err())
	slow, err := redisbus.NewChannelClient(redisbus.WithChannelClient(r))
	require.NoError(t, err)
	defer slow.Close()
	entered := make(chan struct{}, 1)
	require.NoError(t, slow.Subscribe(ctx, topic, func(ctx context.Context, _ pubsub.Message) error { entered <- struct{}{}; <-ctx.Done(); return nil }))
	require.NoError(t, r.Publish(ctx, topic, "work").Err())
	await(t, ctx, entered)
	expired, stop := context.WithCancel(context.Background())
	stop()
	require.Error(t, slow.CloseContext(expired))
	require.NoError(t, slow.Close())
	require.NoError(t, r.Ping(ctx).Err())
	require.EqualValues(t, 1, slow.Stats().Canceled)
}

func TestRocket_BacklogAndFailedHandlerRedelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	endpoint := address(t, "MODULAR_TEST_ROCKET")
	topic := "modular-reliability"
	opts := []rocket.ConsumerOption{rocket.WithConsumerEndpoint(endpoint), rocket.WithConsumerGroup(topic), rocket.WithConsumerTopic(topic)}
	warm, err := rocket.NewPushConsumer(opts...)
	require.NoError(t, err)
	defer warm.Close()
	received := make(chan struct{}, 10)
	require.NoError(t, warm.Subscribe(ctx, topic, func(context.Context, pubsub.Message) error { received <- struct{}{}; return nil }))
	producer, err := rocket.NewProducer(rocket.WithEndpoint(endpoint), rocket.WithProducerTopic(topic))
	require.NoError(t, err)
	defer producer.Close()
	require.NoError(t, producer.Publish(ctx, topic, []byte("warm")))
	await(t, ctx, received)
	require.NoError(t, warm.CloseContext(ctx))
	next, err := rocket.NewPushConsumer(opts...)
	require.NoError(t, err)
	defer next.Close()
	require.NoError(t, producer.Publish(ctx, topic, []byte("backlog")))
	var attempts atomic.Int32
	done := make(chan string, 10)
	require.NoError(t, next.Subscribe(ctx, topic, func(_ context.Context, m pubsub.Message) error {
		if string(m.Payload) != "backlog" {
			return nil
		}
		if attempts.Add(1) == 1 {
			return errors.New("retry this delivery")
		}
		done <- string(m.Payload)
		return nil
	}))
	require.Equal(t, "backlog", await(t, ctx, done))
	require.GreaterOrEqual(t, attempts.Load(), int32(2))
	require.NoError(t, next.CloseContext(ctx))
}
