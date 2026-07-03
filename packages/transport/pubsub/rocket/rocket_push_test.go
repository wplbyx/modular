package rocket

import (
	"testing"
	"time"

	rmq "github.com/apache/rocketmq-clients/golang/v5"
)

// ---------------------------------------------------------------------------
// ProducerOptions
// ---------------------------------------------------------------------------

func TestDefaultProducerOptions(t *testing.T) {
	o := DefaultProducerOptions()
	if o.Endpoint != "" || o.Group != "" || o.Topic != "" {
		t.Fatalf("DefaultProducerOptions = %+v, want all-zero", o)
	}
	if o.MaxAttempts != 0 {
		t.Fatalf("MaxAttempts = %d, want 0", o.MaxAttempts)
	}
}

func TestProducerOptions_AppliedViaOptions(t *testing.T) {
	o := DefaultProducerOptions()
	WithEndpoint("127.0.0.1:8081")(o)
	WithProducerGroup("pg")(o)
	WithProducerTopic("events")(o)
	WithProducerCredentials("ak", "sk")(o)
	WithProducerNameSpace("ns")(o)
	WithProducerMaxAttempts(3)(o)

	if o.Endpoint != "127.0.0.1:8081" {
		t.Fatalf("Endpoint = %q", o.Endpoint)
	}
	if o.Group != "pg" {
		t.Fatalf("Group = %q", o.Group)
	}
	if o.Topic != "events" {
		t.Fatalf("Topic = %q", o.Topic)
	}
	if o.AccessKey != "ak" || o.AccessSecret != "sk" {
		t.Fatalf("Credentials = %q/%q", o.AccessKey, o.AccessSecret)
	}
	if o.NameSpace != "ns" {
		t.Fatalf("NameSpace = %q", o.NameSpace)
	}
	if o.MaxAttempts != 3 {
		t.Fatalf("MaxAttempts = %d", o.MaxAttempts)
	}
}

// TestNewProducer_RequiresEndpoint verifies that a missing endpoint is rejected
// before any network activity. (NewProducer would otherwise try to dial.)
func TestNewProducer_RequiresEndpoint(t *testing.T) {
	if _, err := NewProducer(); err == nil {
		t.Fatal("NewProducer() with no endpoint expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// ConsumerOptions
// ---------------------------------------------------------------------------

func TestDefaultConsumerOptions(t *testing.T) {
	o := DefaultConsumerOptions()
	if o.Endpoint != "" || o.Group != "" || o.Topic != "" {
		t.Fatalf("DefaultConsumerOptions = %+v, want all-zero", o)
	}
	if o.FilterType != "" || o.FilterExpression != "" {
		t.Fatalf("Filter fields not zero: %+v", o)
	}
}

func TestConsumerOptions_AppliedViaOptions(t *testing.T) {
	o := DefaultConsumerOptions()
	WithConsumerEndpoint("127.0.0.1:8081")(o)
	WithConsumerGroup("cg")(o)
	WithConsumerTopic("events")(o)
	WithConsumerCredentials("ak", "sk")(o)
	WithConsumerNameSpace("ns")(o)
	WithConsumerFilter("tagA", "tag")(o)
	WithConsumerAwaitDuration(5 * time.Second)(o)
	WithConsumerMaxCache(2048)(o)
	WithConsumerThreads(64)(o)

	if o.Endpoint != "127.0.0.1:8081" {
		t.Fatalf("Endpoint = %q", o.Endpoint)
	}
	if o.Group != "cg" || o.Topic != "events" {
		t.Fatalf("Group=%q Topic=%q", o.Group, o.Topic)
	}
	if o.AccessKey != "ak" || o.AccessSecret != "sk" {
		t.Fatalf("Credentials = %q/%q", o.AccessKey, o.AccessSecret)
	}
	if o.NameSpace != "ns" {
		t.Fatalf("NameSpace = %q", o.NameSpace)
	}
	if o.FilterExpression != "tagA" || o.FilterType != "tag" {
		t.Fatalf("Filter = %q/%q", o.FilterExpression, o.FilterType)
	}
	if o.AwaitDuration != 5*time.Second {
		t.Fatalf("AwaitDuration = %v", o.AwaitDuration)
	}
	if o.MaxCache != 2048 {
		t.Fatalf("MaxCache = %d", o.MaxCache)
	}
	if o.Threads != 64 {
		t.Fatalf("Threads = %d", o.Threads)
	}
}

// TestNewPushConsumer_Validation verifies the required-field checks run before
// any dial attempt, so they fail fast without a broker.
func TestNewPushConsumer_Validation(t *testing.T) {
	cases := []struct {
		name string
		opts []ConsumerOption
	}{
		{"missing endpoint", []ConsumerOption{WithConsumerGroup("g"), WithConsumerTopic("t")}},
		{"missing group", []ConsumerOption{WithConsumerEndpoint("e"), WithConsumerTopic("t")}},
		{"missing topic", []ConsumerOption{WithConsumerEndpoint("e"), WithConsumerGroup("g")}},
		{"all empty", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewPushConsumer(tc.opts...); err == nil {
				t.Fatalf("NewPushConsumer(%s) expected error, got nil", tc.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Pure helpers (no broker needed)
// ---------------------------------------------------------------------------

// TestNewFilterExpression covers the filter-expression builder. SUB_ALL must be
// returned for an empty expression; otherwise a non-nil expression is built and
// the type flag is honored.
func TestNewFilterExpression(t *testing.T) {
	if got := newFilterExpression("", ""); got != rmq.SUB_ALL {
		t.Fatalf("empty expression should yield SUB_ALL, got a different value")
	}

	tag := newFilterExpression("tagA", "tag")
	if tag == nil {
		t.Fatal("tag filter expression is nil")
	}

	sql := newFilterExpression("a > 1", "sql")
	if sql == nil {
		t.Fatal("sql filter expression is nil")
	}
}
