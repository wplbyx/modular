package delivery

import (
	"context"
	"fmt"
	"github.com/wplbyx/modular/packages/transport/pubsub"
	"math/rand/v2"
	"time"
)

// WaitRetry waits with capped exponential backoff and jitter, returning false on cancellation.
func WaitRetry(ctx context.Context, initial time.Duration, attempt int) bool {
	if initial <= 0 {
		initial = 100 * time.Millisecond
	}
	delay := initial
	for i := 0; i < attempt && delay < 30*time.Second; i++ {
		if delay > 15*time.Second {
			delay = 30 * time.Second
			break
		}
		delay *= 2
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	delay = delay/2 + time.Duration(rand.Int64N(max(int64(delay-delay/2), 1)))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

// CallHandler makes a handler panic a retryable failure, preserving the message.
func CallHandler(ctx context.Context, handler pubsub.MessageHandler, message pubsub.Message) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("message handler panic: %v", p)
		}
	}()
	return handler(ctx, message)
}
