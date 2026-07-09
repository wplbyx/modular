package rpc

import (
	"context"
	"net"
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

func TestServerAddrExposesAllocatedPortBeforeStartup(t *testing.T) {
	server, err := NewServer(&config.GRPC{Host: "127.0.0.1", Port: 0}, nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	addr, ok := server.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("Addr() = %T, want *net.TCPAddr", server.Addr())
	}
	if addr.Port == 0 {
		t.Fatal("Addr().Port = 0, want allocated port")
	}

	transport := server.Transport()
	if transport.Protocol != "grpc" || transport.Address != "127.0.0.1" || transport.Port != addr.Port {
		t.Fatalf("Transport() = %+v, addr = %+v", transport, addr)
	}
}
