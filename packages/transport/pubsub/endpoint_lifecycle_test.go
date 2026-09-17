package pubsub

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"sync/atomic"
	"testing"
	"time"
)

type lifecycleSubscriber struct {
	subscribe func(context.Context) error
	closes    atomic.Int32
	legacy    atomic.Int32
}

func (s *lifecycleSubscriber) Subscribe(ctx context.Context, _ string, _ MessageHandler, _ ...SubscribeOption) error {
	if s.subscribe != nil {
		return s.subscribe(ctx)
	}
	return nil
}
func (s *lifecycleSubscriber) Unsubscribe(context.Context, string) error { return nil }
func (s *lifecycleSubscriber) Close() error                              { s.legacy.Add(1); return nil }
func (s *lifecycleSubscriber) CloseContext(context.Context) error        { s.closes.Add(1); return nil }
func TestEndpoint_CloseBeforeStartAndIdempotence(t *testing.T) {
	s := &lifecycleSubscriber{}
	e := NewSubscriberEndpoint("test", s, "topic", func(context.Context, Message) error { return nil })
	require.NoError(t, e.Shutdown(context.Background()))
	require.NoError(t, e.Shutdown(context.Background()))
	require.Error(t, e.Startup(context.Background()))
	require.Error(t, e.Ready(context.Background()))
	require.EqualValues(t, 1, s.closes.Load())
	require.Zero(t, s.legacy.Load())
}
func TestEndpoint_FailedStartupWakesReady(t *testing.T) {
	failure := errors.New("subscribe failed")
	s := &lifecycleSubscriber{subscribe: func(context.Context) error { return failure }}
	e := NewSubscriberEndpoint("test", s, "topic", func(context.Context, Message) error { return nil })
	require.ErrorIs(t, e.Startup(context.Background()), failure)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.ErrorIs(t, e.Ready(ctx), failure)
	require.NoError(t, e.Shutdown(ctx))
}
func TestEndpoint_LateSubscribeCannotPublishReady(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s := &lifecycleSubscriber{subscribe: func(context.Context) error { close(entered); <-release; return nil }}
	e := NewSubscriberEndpoint("test", s, "topic", func(context.Context, Message) error { return nil })
	result := make(chan error, 1)
	go func() { result <- e.Startup(context.Background()) }()
	<-entered
	require.Error(t, e.Startup(context.Background()))
	require.NoError(t, e.Shutdown(context.Background()))
	close(release)
	require.Error(t, <-result)
	require.Error(t, e.Ready(context.Background()))
}
