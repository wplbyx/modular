package rpc

import (
	"context"
	"testing"

	"github.com/wplbyx/modular/packages/config"
)

func TestServerName(t *testing.T) {
	server, err := NewServer(&config.GRPC{Host: "0.0.0.0", Port: 50051}, nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	if got, want := server.Name(), "gRPC Server"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
}

func TestServerShutdownBeforeStart(t *testing.T) {
	server, err := NewServer(&config.GRPC{Host: "127.0.0.1", Port: 50051}, nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}
