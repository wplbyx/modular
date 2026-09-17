package rocket

import (
	"context"
	"errors"
	rmq "github.com/apache/rocketmq-clients/golang/v5"
	"github.com/stretchr/testify/require"
	"github.com/wplbyx/modular/packages/transport/pubsub"
	"sync/atomic"
	"testing"
	"time"
)

type fakePush struct {
	start func() error
	stop  func() error
	stops atomic.Int32
}

func (f *fakePush) Start() error {
	if f.start != nil {
		return f.start()
	}
	return nil
}
func (f *fakePush) GracefulStop() error {
	f.stops.Add(1)
	if f.stop != nil {
		return f.stop()
	}
	return nil
}
func (f *fakePush) Subscribe(string, *rmq.FilterExpression) error { return nil }
func (f *fakePush) Unsubscribe(string) error                      { return nil }
func newTestConsumer(t *testing.T) *PushConsumer {
	t.Helper()
	c, e := NewPushConsumer(WithConsumerEndpoint("127.0.0.1:1"), WithConsumerGroup("tests"), WithConsumerTopic("orders"))
	require.NoError(t, e)
	return c
}
func TestPushConsumer_InstallsHandlerBeforeStart(t *testing.T) {
	c := newTestConsumer(t)
	require.Nil(t, c.pc)
	var result rmq.ConsumerResult
	f := &fakePush{start: func() error { result = c.deliver(pubsub.Message{Topic: "orders"}); return nil }}
	c.factory = func(*rmq.Config, ...rmq.PushConsumerOption) (pushClient, error) { return f, nil }
	require.NoError(t, c.Subscribe(context.Background(), "orders", func(context.Context, pubsub.Message) error { return nil }))
	require.Equal(t, rmq.SUCCESS, result)
	require.Equal(t, rmq.FAILURE, c.deliver(pubsub.Message{Topic: "missing"}))
	require.NoError(t, c.Close())
	require.NoError(t, c.Close())
	require.EqualValues(t, 1, f.stops.Load())
	require.Error(t, c.Subscribe(context.Background(), "orders", func(context.Context, pubsub.Message) error { return nil }))
}
func TestPushConsumer_StartupFailureCleansUp(t *testing.T) {
	c := newTestConsumer(t)
	f := &fakePush{start: func() error { return errors.New("start failed") }}
	c.factory = func(*rmq.Config, ...rmq.PushConsumerOption) (pushClient, error) { return f, nil }
	require.Error(t, c.Subscribe(context.Background(), "orders", func(context.Context, pubsub.Message) error { return nil }))
	require.NoError(t, c.Close())
	require.EqualValues(t, 1, f.stops.Load())
}
func TestPushConsumer_LateStartCannotReviveClosedConsumer(t *testing.T) {
	c := newTestConsumer(t)
	entered, release := make(chan struct{}), make(chan struct{})
	f := &fakePush{start: func() error { close(entered); <-release; return nil }}
	c.factory = func(*rmq.Config, ...rmq.PushConsumerOption) (pushClient, error) { return f, nil }
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() {
		returned <- c.Subscribe(ctx, "orders", func(context.Context, pubsub.Message) error { return nil })
	}()
	<-entered
	cancel()
	select {
	case err := <-returned:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("startup cancellation blocked")
	}
	require.Equal(t, rmq.FAILURE, c.deliver(pubsub.Message{Topic: "orders"}))
	close(release)
	require.NoError(t, c.Close())
	require.EqualValues(t, 1, f.stops.Load())
}
func TestPushConsumer_PanicRetriesAndDeadlineCancels(t *testing.T) {
	c := newTestConsumer(t)
	f := &fakePush{}
	c.factory = func(*rmq.Config, ...rmq.PushConsumerOption) (pushClient, error) { return f, nil }
	require.NoError(t, c.Subscribe(context.Background(), "orders", func(context.Context, pubsub.Message) error { panic("failure") }))
	require.Equal(t, rmq.FAILURE, c.deliver(pubsub.Message{Topic: "orders"}))
	entered, finished := make(chan struct{}), make(chan struct{})
	c.handlers.Store("orders", consumerHandler{context.Background(), func(ctx context.Context, _ pubsub.Message) error { close(entered); <-ctx.Done(); return nil }})
	f.stop = func() error { <-finished; return nil }
	result := make(chan rmq.ConsumerResult, 1)
	go func() { result <- c.deliver(pubsub.Message{Topic: "orders"}); close(finished) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, c.CloseContext(ctx), context.Canceled)
	require.Equal(t, rmq.FAILURE, <-result)
	require.NoError(t, c.Close())
}

func TestPushConsumer_DrainsEvenWhenSDKStopReturnsEarly(t *testing.T) {
	c := newTestConsumer(t)
	f := &fakePush{}
	c.factory = func(*rmq.Config, ...rmq.PushConsumerOption) (pushClient, error) { return f, nil }
	entered, release := make(chan struct{}), make(chan struct{})
	require.NoError(t, c.Subscribe(context.Background(), "orders", func(ctx context.Context, _ pubsub.Message) error {
		close(entered)
		<-release
		return ctx.Err()
	}))
	result := make(chan rmq.ConsumerResult, 1)
	go func() { result <- c.deliver(pubsub.Message{Topic: "orders"}) }()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- c.Close() }()
	require.Eventually(t, func() bool { return f.stops.Load() == 1 }, time.Second, time.Millisecond)
	select {
	case <-closed:
		t.Fatal("closed before business handler drained")
	default:
	}
	close(release)
	require.Equal(t, rmq.SUCCESS, <-result)
	require.NoError(t, <-closed)
}
