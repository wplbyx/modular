package rpc

import (
	"context"
	"testing"

	"modular/packages/config"
)

func TestServerEndpoint(t *testing.T) {
	server, err := NewServer(&config.GRPC{Host: "0.0.0.0", Port: 50051}, nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	u, err := server.Endpoint()
	if err != nil {
		t.Fatalf("Endpoint() error = %v", err)
	}
	if got, want := u.String(), "grpc://127.0.0.1:50051"; got != want {
		t.Fatalf("Endpoint() = %q, want %q", got, want)
	}
}

func TestServerStopBeforeStart(t *testing.T) {
	server, err := NewServer(&config.GRPC{Host: "127.0.0.1", Port: 50051}, nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	if err := server.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}
