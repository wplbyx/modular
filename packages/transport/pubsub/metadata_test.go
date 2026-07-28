package pubsub

import (
	"context"
	"testing"

	"github.com/wplbyx/modular/packages/metadata"
)

func TestResolvePublishOptionsInjectsGlobalMetadataWithoutMutatingHeaders(t *testing.T) {
	ctx := metadata.MustWith(context.Background(), metadata.ScopeGlobal, metadata.RequestIDKey, "req-42")
	original := map[string]string{"custom": "value"}
	resolved, err := ResolvePublishOptions(ctx, PublishOptions{}, WithHeaders(original))
	if err != nil {
		t.Fatalf("ResolvePublishOptions() error = %v", err)
	}
	if resolved.Headers[metadata.RequestIDKey] != "req-42" || resolved.Headers["custom"] != "value" {
		t.Fatalf("resolved headers = %#v", resolved.Headers)
	}
	if _, exists := original[metadata.RequestIDKey]; exists {
		t.Fatal("caller-owned headers were mutated")
	}
}

func TestWithMessageMetadataRestoresContext(t *testing.T) {
	message := Message{Headers: map[string]string{metadata.RequestIDKey: "req-42"}}
	var requestID string
	handler := WithMessageMetadata(func(ctx context.Context, _ Message) error {
		requestID = metadata.FromContext(ctx).Get(metadata.RequestIDKey)[0]
		return nil
	})
	if err := handler(context.Background(), message); err != nil {
		t.Fatalf("handler error = %v", err)
	}
	if requestID != "req-42" {
		t.Fatalf("request ID = %q, want req-42", requestID)
	}
}
