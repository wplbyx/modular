package rpc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestUseClientContextRejectsNilCallback(t *testing.T) {
	err := UseClientContext(context.Background(), nil, WithEndpoint("passthrough:///unused"))
	if err == nil || !strings.Contains(err.Error(), "callback is nil") {
		t.Fatalf("UseClientContext() error = %v", err)
	}
}

func TestGetClientConnectionOptionErrorStopsBeforeDial(t *testing.T) {
	optionErr := errors.New("bad option")
	_, err := GetClientConnection(context.Background(), func(*ClientConfig) error { return optionErr })
	if !errors.Is(err, optionErr) {
		t.Fatalf("GetClientConnection() error = %v, want optionErr", err)
	}
}

func TestGetClientConnectionTimeoutClosesAndReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := GetClientConnection(ctx,
		WithEndpoint("passthrough:///no-server"),
		WithClientTimeout(time.Second),
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GetClientConnection() error = %v, want deadline exceeded", err)
	}
}
