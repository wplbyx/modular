package redis

import (
	"context"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/wplbyx/modular/packages/transport/pubsub"
)

// ---------------------------------------------------------------------------
// Pure-logic tests (no Redis connection required)
// ---------------------------------------------------------------------------

func TestDefaultChannelOptions(t *testing.T) {
	o := DefaultChannelOptions()
	if o.ChannelSize != 100 {
		t.Fatalf("DefaultChannelOptions().ChannelSize = %d, want 100", o.ChannelSize)
	}
	if o.Pattern {
		t.Fatalf("DefaultChannelOptions().Pattern = true, want false")
	}
	if o.Client != nil {
		t.Fatalf("DefaultChannelOptions().Client = %v, want nil", o.Client)
	}
}

func TestChannelOptions_AppliedViaOptions(t *testing.T) {
	client := dialRedis(t)

	o := DefaultChannelOptions()
	WithChannelClient(client)(o)
	WithChannelSize(256)(o)
	WithChannelPattern(true)(o)

	if o.Client != client {
		t.Fatalf("WithChannelClient did not set Client")
	}
	if o.ChannelSize != 256 {
		t.Fatalf("ChannelSize = %d, want 256", o.ChannelSize)
	}
	if !o.Pattern {
		t.Fatalf("Pattern = false, want true")
	}
}

func TestNewChannelClient_RequiresClient(t *testing.T) {
	if _, err := NewChannelClient(); err == nil {
		t.Fatalf("NewChannelClient() with no client expected error, got nil")
	}
}

func TestNewChannelClient_SucceedsWithClient(t *testing.T) {
	c, err := NewChannelClient(WithChannelClient(dialRedis(t)))
	if err != nil {
		t.Fatalf("NewChannelClient() error = %v", err)
	}
	if c == nil {
		t.Fatal("NewChannelClient() returned nil client")
	}
	if c.IsConnected() {
		t.Fatal("IsConnected() = true before Connect")
	}
}

// ---------------------------------------------------------------------------
// Integration tests (require a local Redis at 127.0.0.1:6379; skipped otherwise)
// ---------------------------------------------------------------------------

// TestChannelClient_Integration_PublishSubscribe verifies end-to-end delivery
// over a Redis channel: subscribe, publish, handler receives the payload.
func TestChannelClient_Integration_PublishSubscribe(t *testing.T) {
	rds := dialRedis(t)
	ctx := context.Background()

	c, err := NewChannelClient(WithChannelClient(rds))
	if err != nil {
		t.Fatalf("NewChannelClient: %v", err)
	}
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	channel := unique("test:ch:pubsub")
	got := make(chan pubsub.Message, 8)
	if err := c.Subscribe(ctx, channel, func(_ context.Context, m pubsub.Message) error {
		got <- m
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Redis SUBSCRIBE is asynchronous: the server must acknowledge the
	// subscription before PUBLISH will be delivered. Poll until subscription is
	// registered by publishing a priming message, then publish the real ones.
	waitForSubscription(ctx, t, rds, channel)

	payloads := []string{"hello", "world", "redis"}
	for _, p := range payloads {
		if err := c.Publish(ctx, channel, []byte(p)); err != nil {
			t.Fatalf("Publish(%q): %v", p, err)
		}
	}

	for i, want := range payloads {
		select {
		case m := <-got:
			if m.Topic != channel {
				t.Errorf("msg %d Topic = %q, want %q", i, m.Topic, channel)
			}
			if string(m.Payload) != want {
				t.Errorf("msg %d Payload = %q, want %q", i, m.Payload, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for message %d (%q)", i, want)
		}
	}
}

// TestChannelClient_Integration_PatternSubscribe verifies glob-pattern
// subscription via PSUBSCRIBE.
func TestChannelClient_Integration_PatternSubscribe(t *testing.T) {
	rds := dialRedis(t)
	ctx := context.Background()

	c, err := NewChannelClient(WithChannelClient(rds), WithChannelPattern(true))
	if err != nil {
		t.Fatalf("NewChannelClient: %v", err)
	}
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()

	prefix := unique("test:pat")
	pattern := prefix + ".*"
	ready := make(chan struct{}, 1) // closed once a probe is received
	got := make(chan pubsub.Message, 4)
	if err := c.Subscribe(ctx, pattern, func(_ context.Context, m pubsub.Message) error {
		if string(m.Payload) == probePayload {
			select {
			case ready <- struct{}{}:
			default:
			}
			return nil // discard probes
		}
		got <- m
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// PSUBSCRIBE readiness isn't visible via PUBSUB NUMSUB (that counts only
	// exact-channel subscribers). Publish probe messages on a matching channel
	// until the handler acknowledges one, then publish the real message.
	probeCh := prefix + ".probe"
	probeCtx, probeCancel := context.WithTimeout(ctx, 3*time.Second)
	defer probeCancel()
	if err := waitForPatternReady(probeCtx, rds, probeCh, ready); err != nil {
		t.Fatalf("pattern subscription not ready: %v", err)
	}

	target := prefix + ".event"
	if err := c.Publish(ctx, target, []byte("matched")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case m := <-got:
		if m.Topic != target {
			t.Errorf("Topic = %q, want %q", m.Topic, target)
		}
		if string(m.Payload) != "matched" {
			t.Errorf("Payload = %q, want %q", m.Payload, "matched")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for pattern-matched message")
	}
}

// probePayload marks a priming PUBLISH so the PSUBSCRIBE readiness loop can
// confirm the pattern subscription is live without polluting the asserted
// messages. The handler discards any message carrying this payload.
const probePayload = "__ready_probe__"

// waitForSubscription blocks until Redis confirms the client is subscribed to
// the given channel by polling PUBSUB NUMSUB. Unlike publishing a probe, this
// does not inject a stray message into the subscriber's stream.
func waitForSubscription(ctx context.Context, t *testing.T, rds goredis.UniversalClient, channel string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		subs, err := rds.PubSubNumSub(ctx, channel).Result()
		if err == nil && subs[channel] >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("subscription to %s was not registered in time", channel)
}

// waitForPatternReady confirms a PSUBSCRIBE pattern is live by repeatedly
// publishing a probe to a matching channel until the subscriber acknowledges
// receipt via the ready channel.
func waitForPatternReady(ctx context.Context, rds goredis.UniversalClient, probeCh string, ready <-chan struct{}) error {
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ready:
			return nil
		case <-ticker.C:
			_ = rds.Publish(ctx, probeCh, probePayload).Err()
		}
	}
}
