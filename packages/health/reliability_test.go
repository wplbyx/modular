package health

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stuckCheck struct {
	calls   atomic.Int32
	release chan struct{}
}

func (*stuckCheck) Name() string                  { return "stuck" }
func (c *stuckCheck) Check(context.Context) error { c.calls.Add(1); <-c.release; return nil }
func TestManager_DoesNotMultiplyStuckChecks(t *testing.T) {
	c := &stuckCheck{release: make(chan struct{})}
	defer close(c.release)
	m := NewManager(WithCheckTimeout(time.Millisecond))
	require.NoError(t, m.Register(c))
	m.SetReady()
	for range 3 {
		require.Equal(t, StatusFailed, m.Report(context.Background()).Status)
	}
	require.Equal(t, int32(1), c.calls.Load())
}
