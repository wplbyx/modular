package redis

import (
	"context"
	"testing"
)

func TestNewRedisClient_NilConfig(t *testing.T) {
	_, err := NewRedisClient(context.Background(), nil)
	if err == nil {
		t.Fatalf("NewRedisClient(nil) expected error, got nil")
	}
}
