package resilience

import (
	"context"
	"testing"
	"time"
)

func TestRateLimiterMiddlewareWaitsForToken(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{Name: "test", Rate: 50, Burst: 1})
	exec := RateLimiterMiddleware(rl)(func(context.Context) error { return nil })

	if err := exec(context.Background()); err != nil {
		t.Fatalf("first exec error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := exec(ctx); err != nil {
		t.Fatalf("second exec error = %v", err)
	}
}
