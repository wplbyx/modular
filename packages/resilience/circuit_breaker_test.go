package resilience

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCircuitBreaker_OldSuccessDoesNotCloseNewGeneration(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{FailureThreshold: 1, SuccessThreshold: 1, Timeout: time.Millisecond})
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_ = cb.Execute(context.Background(), func() error { close(started); <-release; return nil })
	}()
	<-started
	require.Error(t, cb.Execute(context.Background(), func() error { return errors.New("failure") }))
	require.Eventually(t, func() bool { return cb.State() == StateHalfOpen }, time.Second, time.Millisecond)
	close(release)
	<-done
	require.Equal(t, StateHalfOpen, cb.State())
}
