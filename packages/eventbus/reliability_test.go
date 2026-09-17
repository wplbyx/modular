package eventbus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	modularlog "github.com/wplbyx/modular/packages/log"
)

func TestBus_CloseDeadlineCancelsHandler(t *testing.T) {
	b, err := New(Config{Capacity: 8}, modularlog.Default())
	require.NoError(t, err)
	started, stopped := make(chan struct{}), make(chan struct{})
	require.NoError(t, b.Subscribe("test", func(ctx context.Context, _ Event) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}))
	require.NoError(t, b.Setup(context.Background()))
	require.NoError(t, b.Publish(context.Background(), Event{Name: "test"}))
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	require.ErrorIs(t, b.Close(ctx), context.DeadlineExceeded)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("handler was not canceled")
	}
}
