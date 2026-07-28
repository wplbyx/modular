package metadata

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestPropagatorScopesAndSensitiveKeys(t *testing.T) {
	propagator := NewPropagator(WithAllowedSensitive(Authorization))
	incoming := MapCarrier{
		GlobalPrefix + "tenant": "acme",
		LocalPrefix + "debug":   "yes",
		Authorization:           "Bearer token",
		Cookie:                  "private=1",
		"accept-language":       "zh-CN, en;q=0.8",
	}

	ctx, err := propagator.Extract(context.Background(), incoming)
	if err != nil {
		t.Fatal(err)
	}
	md := FromContext(ctx)
	if got := md.Get(GlobalPrefix + "tenant"); len(got) != 1 || got[0] != "acme" {
		t.Fatalf("global metadata = %v", got)
	}
	if got := md.Get(LocalPrefix + "debug"); len(got) != 1 || got[0] != "yes" {
		t.Fatalf("local metadata = %v", got)
	}
	if got := md.Get(LanguageKey); len(got) != 1 || got[0] != "zh-CN" {
		t.Fatalf("language = %v", got)
	}
	if got := md.Get(Cookie); len(got) != 0 {
		t.Fatalf("cookie should not be captured: %v", got)
	}

	outgoing := MapCarrier{}
	if err := propagator.Inject(ctx, outgoing); err != nil {
		t.Fatal(err)
	}
	if outgoing[GlobalPrefix+"tenant"] != "acme" {
		t.Fatalf("global propagation = %v", outgoing)
	}
	if _, ok := outgoing[LocalPrefix+"debug"]; ok {
		t.Fatalf("local metadata propagated: %v", outgoing)
	}
	if outgoing[Authorization] != "Bearer token" {
		t.Fatalf("authorization propagation = %v", outgoing)
	}
}

func TestMetadataIsImmutable(t *testing.T) {
	values := map[string][]string{GlobalPrefix + "tenant": {"acme"}}
	md, err := New(ScopeGlobal, values)
	if err != nil {
		t.Fatal(err)
	}
	values[GlobalPrefix+"tenant"][0] = "changed"
	got := md.Get(GlobalPrefix + "tenant")
	got[0] = "also changed"
	if actual := md.Get(GlobalPrefix + "tenant")[0]; actual != "acme" {
		t.Fatalf("metadata mutated: %q", actual)
	}
}

func TestRequestIDIsGenerated(t *testing.T) {
	ctx, err := NewPropagator().Extract(context.Background(), MapCarrier{})
	if err != nil {
		t.Fatal(err)
	}
	if got := FromContext(ctx).Get(RequestIDKey); len(got) != 1 || got[0] == "" {
		t.Fatalf("request id = %v", got)
	}
}

func TestPropagatorHonorsExpandedKeyLimit(t *testing.T) {
	key := GlobalPrefix + strings.Repeat("a", defaultMaxKey)
	carrier := MapCarrier{key: "value"}
	propagator := NewPropagator(WithLimits(defaultMaxKeys, len(key), defaultMaxSize))

	ctx, err := propagator.Extract(context.Background(), carrier)
	if err != nil {
		t.Fatal(err)
	}
	if values := FromContext(ctx).Get(key); len(values) != 1 || values[0] != "value" {
		t.Fatalf("metadata value = %v", values)
	}
}

func TestDefaultPropagatorReadsGlobalTracePolicyAtCallTime(t *testing.T) {
	original := otel.GetTextMapPropagator()
	propagator := NewPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(original) })

	incoming := MapCarrier{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	ctx, err := propagator.Extract(context.Background(), incoming)
	if err != nil {
		t.Fatal(err)
	}
	if span := trace.SpanContextFromContext(ctx); !span.IsValid() || !span.IsRemote() {
		t.Fatalf("span context = %v", span)
	}

	outgoing := MapCarrier{}
	if err := propagator.Inject(ctx, outgoing); err != nil {
		t.Fatal(err)
	}
	if outgoing["traceparent"] == "" {
		t.Fatalf("traceparent was not injected: %v", outgoing)
	}
}
