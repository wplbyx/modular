package caching

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWriteBehind_FlushReportsWriterFailure(t *testing.T) {
	want := errors.New("database failed")
	wb := NewWriteBehind(newMemoryCache(), time.Minute, 2, WithWriteBehindWriter(func(context.Context, string, string) error { return want }))
	defer wb.Stop()
	require.NoError(t, wb.Set(context.Background(), "key", "value"))
	require.ErrorIs(t, wb.FlushContext(context.Background()), want)
}

func TestRefreshAhead_CloseWaitsAndCancelsRefresh(t *testing.T) {
	cache := newMemoryCache()
	r := NewRefreshAhead(cache, 40*time.Millisecond, 30*time.Millisecond)
	started := make(chan struct{})
	var calls atomic.Int32
	loader := func(ctx context.Context) (string, error) {
		if calls.Add(1) == 1 {
			return "initial", nil
		}
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	_, err := r.GetContext(context.Background(), "key", loader)
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	for range 10 {
		_, err = r.GetContext(context.Background(), "key", loader)
		require.NoError(t, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, r.Close(ctx), context.DeadlineExceeded)
	require.NoError(t, r.Close(context.Background()))
	require.Equal(t, int32(2), calls.Load())
}
func TestWriteBehind_ClosedSetDoesNotChangeCache(t *testing.T) {
	cache := newMemoryCache()
	wb := NewWriteBehind(cache, time.Minute, 2, WithWriteBehindWriter(func(context.Context, string, string) error { return nil }))
	wb.Stop()
	require.Error(t, wb.Set(context.Background(), "key", "value"))
	_, err := cache.Get(context.Background(), "key")
	require.ErrorIs(t, err, ErrCacheMiss)
}

func TestWriteBehind_FullQueueDoesNotChangeCache(t *testing.T) {
	cache := newMemoryCache()
	started, release := make(chan struct{}), make(chan struct{})
	w := NewWriteBehind(cache, time.Minute, 1, WithWriteBehindWriter(func(_ context.Context, key, value string) error {
		if key == "first" {
			close(started)
			<-release
		}
		return nil
	}))
	t.Cleanup(func() { close(release); w.Stop() })
	require.NoError(t, w.Set(context.Background(), "first", "one"))
	<-started
	require.NoError(t, w.Set(context.Background(), "second", "two"))
	require.Error(t, w.Set(context.Background(), "third", "three"))
	_, err := cache.Get(context.Background(), "third")
	require.ErrorIs(t, err, ErrCacheMiss)
}
