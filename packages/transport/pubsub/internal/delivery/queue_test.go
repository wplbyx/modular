package delivery

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueue_DrainBackpressureAndRetry(t *testing.T) {
	q := NewQueue(1, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	var attempts atomic.Int32
	require.NoError(t, q.Submit(context.Background(), func(context.Context) error { close(entered); <-release; return nil }, nil, false))
	<-entered
	ack := make(chan struct{})
	require.NoError(t, q.Submit(context.Background(), func(context.Context) error {
		if attempts.Add(1) == 1 {
			panic("retry")
		}
		return nil
	}, func() { close(ack) }, true))
	rejected := make(chan error, 1)
	go func() {
		rejected <- q.Submit(context.Background(), func(context.Context) error { return nil }, nil, false)
	}()
	q.Stop()
	require.ErrorIs(t, <-rejected, ErrClosed)
	select {
	case <-ack:
		t.Fatal("ack before handler completed")
	default:
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, q.Close(ctx))
	require.EqualValues(t, 2, q.Stats().Succeeded)
	require.EqualValues(t, 1, q.Stats().Failed)
	require.EqualValues(t, 1, q.Stats().Retried)
}
func TestQueue_DeadlineCancelsWithoutAck(t *testing.T) {
	q := NewQueue(1, 1)
	entered, exited := make(chan struct{}), make(chan struct{})
	var ack atomic.Bool
	require.NoError(t, q.Submit(context.Background(), func(ctx context.Context) error { close(entered); <-ctx.Done(); close(exited); return nil }, func() { ack.Store(true) }, true))
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, q.Close(ctx), context.Canceled)
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("handler was not canceled")
	}
	require.NoError(t, q.Close(context.Background()))
	require.False(t, ack.Load())
	require.EqualValues(t, 1, q.Stats().Canceled)
}
func TestQueue_PreservesValuesButDrainsAfterParentCancellation(t *testing.T) {
	type key struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "value"))
	q := NewQueue(1, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	require.NoError(t, q.Submit(ctx, func(ctx context.Context) error {
		close(entered)
		<-release
		if ctx.Value(key{}) != "value" {
			return errors.New("missing value")
		}
		result <- ctx.Err()
		return nil
	}, nil, false))
	<-entered
	cancel()
	close(release)
	require.NoError(t, q.Close(context.Background()))
	require.NoError(t, <-result)
}
