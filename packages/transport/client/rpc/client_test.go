package rpc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	modularmetadata "github.com/wplbyx/modular/packages/metadata"
	modulartransport "github.com/wplbyx/modular/packages/transport"
	"google.golang.org/grpc"
	grpcmetadata "google.golang.org/grpc/metadata"
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

func TestMetadataUnaryClientInterceptorInjectsOutgoingContext(t *testing.T) {
	policy := modulartransport.NewPolicy(
		"test-client",
		modulartransport.WithTracing(false),
		modulartransport.WithAccessLog(false),
		modulartransport.WithProtection(nil),
	)
	ctx := modularmetadata.MustWith(context.Background(), modularmetadata.ScopeGlobal, modularmetadata.RequestIDKey, "req-42")
	interceptor := metadataUnaryClientInterceptor(policy)
	err := interceptor(ctx, "/test/Get", nil, nil, nil, func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		outgoing, ok := grpcmetadata.FromOutgoingContext(ctx)
		if !ok || len(outgoing.Get(modularmetadata.RequestIDKey)) != 1 || outgoing.Get(modularmetadata.RequestIDKey)[0] != "req-42" {
			t.Fatalf("outgoing metadata = %#v", outgoing)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("interceptor error = %v", err)
	}
}
