package http

import (
	"testing"

	"modular/packages/config"
)

func TestServerEndpoint(t *testing.T) {
	server := NewServer(&config.HTTP{Host: "0.0.0.0", Port: 8080})

	u, err := server.Endpoint()
	if err != nil {
		t.Fatalf("Endpoint() error = %v", err)
	}
	if got, want := u.String(), "http://127.0.0.1:8080"; got != want {
		t.Fatalf("Endpoint() = %q, want %q", got, want)
	}
}

func TestServerEndpointTLS(t *testing.T) {
	server := NewServer(&config.HTTP{Host: "localhost", Port: 8443, EnableTLS: true})

	u, err := server.Endpoint()
	if err != nil {
		t.Fatalf("Endpoint() error = %v", err)
	}
	if got, want := u.String(), "https://localhost:8443"; got != want {
		t.Fatalf("Endpoint() = %q, want %q", got, want)
	}
}
