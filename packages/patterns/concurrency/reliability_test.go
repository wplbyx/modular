package concurrency

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMemoizer_ZeroValueConcurrentLoadsAndExplicitPurge(t *testing.T) {
	var m Memoizer[string, string]
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := m.GetOrLoad(context.Background(), "key", func(context.Context) (string, error) { return "value", nil })
			if err != nil || value != "value" {
				t.Errorf("GetOrLoad = %q, %v", value, err)
			}
		}()
	}
	wg.Wait()
	require.Zero(t, m.PurgeExpired())
	expiring := NewMemoizer[string, string](time.Nanosecond)
	expiring.Set("key", "value")
	require.Eventually(t, func() bool { return expiring.PurgeExpired() == 1 }, time.Second, time.Millisecond)
	require.Zero(t, expiring.Len())
}
