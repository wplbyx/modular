package redis

import (
	"context"
	"fmt"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/wplbyx/modular/packages/transport/pubsub"
)

// ---------------------------------------------------------------------------
// Shared test helpers (used by both *_test.go files in this package)
// ---------------------------------------------------------------------------

const testRedisAddr = "127.0.0.1:6379"

// dialRedis returns a client connected to the local Redis, skipping the test
// when the server is unreachable. The client is closed via t.Cleanup.
func dialRedis(t *testing.T) goredis.UniversalClient {
	t.Helper()
	c := goredis.NewClient(&goredis.Options{Addr: testRedisAddr})
	t.Cleanup(func() { _ = c.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		c.Close()
		t.Skipf("local redis at %s unreachable: %v (skipping integration test)", testRedisAddr, err)
	}
	return c
}

// unique appends a short random suffix so concurrent test runs and retries do
// not collide on shared Redis keys.
func unique(name string) string {
	return fmt.Sprintf("%s-%d-%d", name, time.Now().UnixNano(), rand.Int31n(100000))
}

// ---------------------------------------------------------------------------
// Pure-logic tests (no Redis connection required)
// ---------------------------------------------------------------------------

func TestDefaultStreamOptions(t *testing.T) {
	o := DefaultStreamOptions()
	if o.StartID != "$" {
		t.Fatalf("StartID = %q, want \"$\"", o.StartID)
	}
	if o.Block != 1*time.Second {
		t.Fatalf("Block = %v, want 1s", o.Block)
	}
	if o.Count != 100 {
		t.Fatalf("Count = %d, want 100", o.Count)
	}
	if o.Workers != 1 {
		t.Fatalf("Workers = %d, want 1", o.Workers)
	}
	if o.RetryBackoff != 100*time.Millisecond {
		t.Fatalf("RetryBackoff = %v, want 100ms", o.RetryBackoff)
	}
}

func TestStreamOptions_AppliedViaOptions(t *testing.T) {
	o := DefaultStreamOptions()
	WithGroup("g1")(o)
	WithConsumer("c1")(o)
	WithStartID("0")(o)
	WithBlock(500 * time.Millisecond)(o)
	WithCount(50)(o)
	WithWorkers(4)(o)
	WithMaxLen(1000)(o)
	WithStreamRetries(3, 200*time.Millisecond)(o)
	WithDLQStream("dlq")(o)

	if o.Group != "g1" {
		t.Fatalf("Group = %q", o.Group)
	}
	if o.Consumer != "c1" {
		t.Fatalf("Consumer = %q", o.Consumer)
	}
	if o.StartID != "0" {
		t.Fatalf("StartID = %q", o.StartID)
	}
	if o.Block != 500*time.Millisecond {
		t.Fatalf("Block = %v", o.Block)
	}
	if o.Count != 50 {
		t.Fatalf("Count = %d", o.Count)
	}
	if o.Workers != 4 {
		t.Fatalf("Workers = %d", o.Workers)
	}
	if o.MaxLen != 1000 {
		t.Fatalf("MaxLen = %d", o.MaxLen)
	}
	if o.MaxRetries != 3 || o.RetryBackoff != 200*time.Millisecond {
		t.Fatalf("retries = %d backoff = %v", o.MaxRetries, o.RetryBackoff)
	}
	if o.DLQStream != "dlq" {
		t.Fatalf("DLQStream = %q", o.DLQStream)
	}
}

func TestNewStreamClient_RequiresClient(t *testing.T) {
	if _, err := NewStreamClient(WithGroup("g")); err == nil {
		t.Fatalf("NewStreamClient() with no client expected error, got nil")
	}
}

func TestNewStreamClient_RequiresGroup(t *testing.T) {
	if _, err := NewStreamClient(WithStreamClient(dialRedis(t))); err == nil {
		t.Fatalf("NewStreamClient() with no group expected error, got nil")
	}
}

func TestNewStreamClient_SucceedsAndDefaultsConsumer(t *testing.T) {
	c, err := NewStreamClient(WithStreamClient(dialRedis(t)), WithGroup("g"))
	if err != nil {
		t.Fatalf("NewStreamClient() error = %v", err)
	}
	if c == nil {
		t.Fatal("NewStreamClient() returned nil client")
	}
	if c.opts.Consumer == "" {
		t.Fatal("Consumer should default to a generated name when empty")
	}
	if c.IsConnected() {
		t.Fatal("IsConnected() = true before Connect")
	}
}

// TestBuildStreamValues covers the pure-logic mapping from publish options to
// the XADD Values map, including reserved-name collision protection.
func TestBuildStreamValues(t *testing.T) {
	t.Run("payload and key only", func(t *testing.T) {
		v := buildStreamValues("k1", nil, []byte("hello"))
		if got := v[streamFieldPayload]; got != "hello" {
			t.Fatalf("payload = %v, want \"hello\"", got)
		}
		if got := v[streamFieldKey]; got != "k1" {
			t.Fatalf("key = %v, want \"k1\"", got)
		}
		if len(v) != 2 {
			t.Fatalf("extra fields: %v", v)
		}
	})

	t.Run("headers merged, empty key omitted", func(t *testing.T) {
		headers := map[string]string{"event": "created", "trace": "abc"}
		v := buildStreamValues("", headers, []byte("p"))
		if v[streamFieldPayload] != "p" {
			t.Fatalf("payload = %v", v[streamFieldPayload])
		}
		if _, ok := v[streamFieldKey]; ok {
			t.Fatal("key field should be absent when key is empty")
		}
		if v["event"] != "created" || v["trace"] != "abc" {
			t.Fatalf("headers not merged: %v", v)
		}
	})

	t.Run("reserved header names are ignored", func(t *testing.T) {
		// A header named "payload" must not overwrite the real payload.
		headers := map[string]string{"payload": "EVIL", "key": "EVILKEY"}
		v := buildStreamValues("realkey", headers, []byte("realbody"))
		if v[streamFieldPayload] != "realbody" {
			t.Fatalf("payload overwritten by header: %v", v[streamFieldPayload])
		}
		if v[streamFieldKey] != "realkey" {
			t.Fatalf("key overwritten by header: %v", v[streamFieldKey])
		}
	})
}

// TestStreamMessageToMessage covers the XMessage -> pubsub.Message mapping.
func TestStreamMessageToMessage(t *testing.T) {
	m := goredis.XMessage{
		ID: "1234567-0",
		Values: map[string]interface{}{
			streamFieldPayload: "the-body",
			streamFieldKey:     "the-key",
			"event":            "updated",
			"trace":            "xyz",
		},
	}
	got := streamMessageToMessage("orders", m)

	if got.Topic != "orders" {
		t.Fatalf("Topic = %q, want \"orders\"", got.Topic)
	}
	if string(got.Payload) != "the-body" {
		t.Fatalf("Payload = %q, want \"the-body\"", got.Payload)
	}
	if got.Key != "the-key" {
		t.Fatalf("Key = %q, want \"the-key\"", got.Key)
	}
	if got.Headers["event"] != "updated" || got.Headers["trace"] != "xyz" {
		t.Fatalf("Headers = %v", got.Headers)
	}
	if _, ok := got.Headers[streamFieldPayload]; ok {
		t.Fatal("payload leaked into Headers")
	}
	if _, ok := got.Headers[streamFieldKey]; ok {
		t.Fatal("key leaked into Headers")
	}
	if got.Headers[streamIDHeader] != "1234567-0" {
		t.Fatalf("entry id header = %q, want \"1234567-0\"", got.Headers[streamIDHeader])
	}
}

func TestIsBusyGroup(t *testing.T) {
	busy := errStr("BUSYGROUP Consumer Group name already exists")
	if !isBusyGroup(busy) {
		t.Fatal("isBusyGroup should match a BUSYGROUP error")
	}
	other := errStr("WRONGTYPE something else")
	if isBusyGroup(other) {
		t.Fatal("isBusyGroup should not match an unrelated error")
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }

// ---------------------------------------------------------------------------
// Integration tests (require a local Redis at 127.0.0.1:6379; skipped otherwise)
// ---------------------------------------------------------------------------

// cleanupStream removes the stream, its consumer group and (optionally) the DLQ
// stream so that repeated test runs start from a clean state.
func cleanupStream(t *testing.T, rds goredis.UniversalClient, streams ...string) {
	t.Helper()
	ctx := context.Background()
	for _, s := range streams {
		if s == "" {
			continue
		}
		_ = rds.Del(ctx, s).Err()
	}
}

// pendingCount returns the number of pending (unacknowledged) entries for a
// group, retrying briefly since XACK and XPENDING are eventually consistent
// from the test's perspective after the consume loop processes a message.
func pendingCount(t *testing.T, rds goredis.UniversalClient, stream, group string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p, err := rds.XPending(ctx, stream, group).Result()
		if err == nil {
			if p.Count == 0 {
				return 0
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Best-effort final read.
	p, err := rds.XPending(ctx, stream, group).Result()
	if err != nil {
		t.Fatalf("XPENDING %s %s: %v", stream, group, err)
	}
	return p.Count
}

// TestStreamClient_Integration_PublishSubscribe verifies end-to-end stream
// delivery: XADD publish, XREADGROUP consume, XACK on success, and that the
// message fields map back into pubsub.Message correctly.
func TestStreamClient_Integration_PublishSubscribe(t *testing.T) {
	rds := dialRedis(t)
	ctx := context.Background()

	stream := unique("test:stream:ps")
	group := unique("grp")
	cleanupStream(t, rds, stream)
	defer cleanupStream(t, rds, stream)

	sc, err := NewStreamClient(
		WithStreamClient(rds),
		WithGroup(group),
		WithConsumer("c-ps"),
		// Read from the very start so the message we publish is delivered even
		// though it lands in the stream around the same time as Subscribe.
		WithStartID("0"),
		WithBlock(100*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewStreamClient: %v", err)
	}
	if err := sc.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sc.Close()

	got := make(chan pubsub.Message, 4)
	if err := sc.Subscribe(ctx, stream, func(_ context.Context, m pubsub.Message) error {
		got <- m
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Publish a message with a key and a header so we can verify field mapping.
	headers := map[string]string{"event": "created", "trace": "abc"}
	if err := sc.Publish(ctx, stream, []byte("payload-1"),
		pubsub.WithKey("order-1"), pubsub.WithHeaders(headers)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case m := <-got:
		if m.Topic != stream {
			t.Errorf("Topic = %q, want %q", m.Topic, stream)
		}
		if string(m.Payload) != "payload-1" {
			t.Errorf("Payload = %q, want \"payload-1\"", m.Payload)
		}
		if m.Key != "order-1" {
			t.Errorf("Key = %q, want \"order-1\"", m.Key)
		}
		if m.Headers["event"] != "created" || m.Headers["trace"] != "abc" {
			t.Errorf("Headers = %v", m.Headers)
		}
		if m.Headers[streamIDHeader] == "" {
			t.Error("x-stream-id header is empty")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for stream message")
	}

	// Give the worker a moment to XACK, then assert no pending entries remain.
	if n := pendingCount(t, rds, stream, group); n != 0 {
		t.Fatalf("XPENDING count = %d, want 0 (message not acked)", n)
	}
}

// TestStreamClient_Integration_RetryThenSuccess verifies that a handler which
// fails twice then succeeds is retried up to MaxRetries and finally acked.
func TestStreamClient_Integration_RetryThenSuccess(t *testing.T) {
	rds := dialRedis(t)
	ctx := context.Background()

	stream := unique("test:stream:retry")
	group := unique("grp")
	cleanupStream(t, rds, stream)
	defer cleanupStream(t, rds, stream)

	sc, err := NewStreamClient(
		WithStreamClient(rds),
		WithGroup(group),
		WithConsumer("c-retry"),
		WithStartID("0"),
		WithBlock(100*time.Millisecond),
		WithStreamRetries(2, 5*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewStreamClient: %v", err)
	}
	if err := sc.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sc.Close()

	var calls int32
	if err := sc.Subscribe(ctx, stream, func(_ context.Context, m pubsub.Message) error {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 { // fail the first two attempts
			return fmt.Errorf("intentional failure %d", n)
		}
		return nil
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := sc.Publish(ctx, stream, []byte("retry-me")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Wait for 3 handler invocations.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&calls) >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("handler called %d times, want 3", got)
	}

	if n := pendingCount(t, rds, stream, group); n != 0 {
		t.Fatalf("XPENDING count = %d, want 0 (message not acked after success)", n)
	}
}

// TestStreamClient_Integration_DLQ verifies that a message whose handler
// exhausts all retries is forwarded to the DLQ stream and acked on the origin.
func TestStreamClient_Integration_DLQ(t *testing.T) {
	rds := dialRedis(t)
	ctx := context.Background()

	stream := unique("test:stream:dlq")
	group := unique("grp")
	dlq := unique("test:stream:dlq-target")
	cleanupStream(t, rds, stream, dlq)
	defer cleanupStream(t, rds, stream, dlq)

	sc, err := NewStreamClient(
		WithStreamClient(rds),
		WithGroup(group),
		WithConsumer("c-dlq"),
		WithStartID("0"),
		WithBlock(100*time.Millisecond),
		WithStreamRetries(1, 5*time.Millisecond),
		WithDLQStream(dlq),
	)
	if err != nil {
		t.Fatalf("NewStreamClient: %v", err)
	}
	if err := sc.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer sc.Close()

	if err := sc.Subscribe(ctx, stream, func(_ context.Context, m pubsub.Message) error {
		return fmt.Errorf("always fails")
	}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := sc.Publish(ctx, stream, []byte("doomed"), pubsub.WithKey("k-doomed")); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Wait until the origin message is acked (DLQ write happens just before ack).
	if n := pendingCount(t, rds, stream, group); n != 0 {
		t.Fatalf("XPENDING count = %d, want 0 (DLQ'd message not acked)", n)
	}

	// The DLQ stream should contain exactly one entry carrying the metadata.
	// Poll briefly: the DLQ XADD and the ack race against this read in the test.
	var dlqMsgs []goredis.XMessage
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		msgs, err := rds.XRange(ctx, dlq, "-", "+").Result()
		if err != nil {
			t.Fatalf("XRANGE %s: %v", dlq, err)
		}
		if len(msgs) >= 1 {
			dlqMsgs = msgs
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(dlqMsgs) != 1 {
		// Diagnostic: show what's actually in the origin stream and DLQ.
		origin, _ := rds.XRange(ctx, stream, "-", "+").Result()
		t.Fatalf("DLQ entries = %d, want 1 (origin entries=%d, dlq key=%q)",
			len(dlqMsgs), len(origin), dlq)
	}
	values := dlqMsgs[0].Values
	if values["payload"] != "doomed" {
		t.Errorf("DLQ payload = %v, want \"doomed\"", values["payload"])
	}
	if values["x-original-stream"] != stream {
		t.Errorf("DLQ x-original-stream = %v, want %q", values["x-original-stream"], stream)
	}
	if values["x-error"] == "" {
		t.Errorf("DLQ x-error is empty")
	}
	if _, ok := values["x-stream-id"]; !ok {
		t.Errorf("DLQ x-stream-id missing")
	}
}
