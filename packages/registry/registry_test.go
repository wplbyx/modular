package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"modular/packages/core"
)

func TestBuildConsulTarget(t *testing.T) {
	target := BuildConsulTarget("hello-service")
	assert.Equal(t, "consul:///hello-service", target)
}

func TestBuildTargetWithScheme(t *testing.T) {
	assert.Equal(t, "k8s:///hello-service", BuildTarget("k8s", "hello-service"))
}

func TestGRPCResolverBuilderSchemeCanBeConfigured(t *testing.T) {
	b := NewGRPCResolverBuilderWithScheme("k8s", nil)
	assert.Equal(t, "k8s", b.Scheme())
}

func TestConsulGetServiceUsesContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health/service/orders" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(200 * time.Millisecond):
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()

	reg, err := NewConsulRegistry(server.URL)
	if err != nil {
		t.Fatalf("NewConsulRegistry() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = reg.GetService(ctx, "orders")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GetService() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("GetService() took %v, context was not applied", elapsed)
	}
}

func TestConsulHealthCheckByProtocol(t *testing.T) {
	httpCheck := consulHealthCheck(core.Transport{Protocol: "http", Address: "127.0.0.1", Port: 8080, HealthPath: "/ready"})
	if httpCheck.HTTP != "http://127.0.0.1:8080/ready" ||
		httpCheck.TCP != "" ||
		httpCheck.GRPC != "" {
		t.Fatalf("http check = %+v", httpCheck)
	}

	defaultHTTPCheck := consulHealthCheck(core.Transport{Protocol: "http", Address: "127.0.0.1", Port: 8080})
	if defaultHTTPCheck.HTTP != "http://127.0.0.1:8080/health" {
		t.Fatalf("default http check = %+v", defaultHTTPCheck)
	}

	grpcCheck := consulHealthCheck(core.Transport{Protocol: "grpc", Address: "127.0.0.1", Port: 50051})
	if grpcCheck.GRPC != "127.0.0.1:50051" || grpcCheck.HTTP != "" || grpcCheck.TCP != "" {
		t.Fatalf("grpc check = %+v", grpcCheck)
	}

	mqttCheck := consulHealthCheck(core.Transport{Protocol: "mqtt", Address: "127.0.0.1", Port: 1883})
	if mqttCheck.TCP != "127.0.0.1:1883" || mqttCheck.HTTP != "" || mqttCheck.GRPC != "" {
		t.Fatalf("mqtt check = %+v", mqttCheck)
	}
}

func TestNewServiceNodeUsage(t *testing.T) {
	node := core.NewServiceNode(
		"test-service", "v1.0.0",
		core.Transport{Protocol: "http", Address: "127.0.0.1", Port: 8080},
	)
	assert.Equal(t, "test-service", node.Name)
	assert.Equal(t, "v1.0.0", node.Version)
	assert.Contains(t, node.Endpoints(), "http://127.0.0.1:8080")
}
