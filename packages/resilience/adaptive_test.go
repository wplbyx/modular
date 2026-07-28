package resilience

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/go-kratos/aegis/ratelimit"

	"github.com/wplbyx/modular/packages/errs"
)

func TestAdaptiveProtectionAdmitsAndCompletes(t *testing.T) {
	protection := NewAdaptiveProtection(AdaptiveConfig{CPUThreshold: 1000})
	done, err := protection.Allow(context.Background(), "GET /orders")
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	done(nil)
	done(errors.New("ignored duplicate completion"))

	if protection.Stats().InFlight != 0 {
		t.Fatalf("in-flight = %d, want 0", protection.Stats().InFlight)
	}
}

func TestAdaptiveProtectionBoundsOperationBreakers(t *testing.T) {
	protection := NewAdaptiveProtection(AdaptiveConfig{CPUThreshold: 1000, MaxOperations: 1})
	for _, operation := range []string{"GET /one", "GET /two"} {
		done, err := protection.Allow(context.Background(), operation)
		if err != nil {
			t.Fatalf("Allow(%q) error = %v", operation, err)
		}
		done(nil)
	}
	if got := len(protection.breakers); got != 1 {
		t.Fatalf("breaker count = %d, want 1", got)
	}
}

func TestAdaptiveProtectionRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewAdaptiveProtection(AdaptiveConfig{}).Allow(ctx, "GET /orders")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Allow() error = %v, want context.Canceled", err)
	}
}

func TestAdaptiveErrorsKeepStableBoundaryCodes(t *testing.T) {
	protection := NewAdaptiveProtection(AdaptiveConfig{CPUThreshold: 1000})
	protection.limiter = rejectingLimiter{}
	_, err := protection.Allow(context.Background(), "GET /orders")
	if !IsAdaptiveLimit(err) {
		t.Fatalf("Allow() error = %v, want adaptive limit", err)
	}
	if code := errs.Code(err); code != http.StatusTooManyRequests {
		t.Fatalf("limit code = %d, want %d", code, http.StatusTooManyRequests)
	}
}

type rejectingLimiter struct{}

func (rejectingLimiter) Allow() (ratelimit.DoneFunc, error) {
	return nil, ratelimit.ErrLimitExceed
}
