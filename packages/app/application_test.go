package app

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"testing"
	"time"

	"modular/packages/config"
	"modular/packages/registry"
)

func TestApplicationRunStopsEndpointAndRunsCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	endpoint := &testEndpoint{started: make(chan struct{})}
	var cleanupOrder []string

	application, err := NewApplication(ctx, &config.Application{Name: "test"},
		WithEndpoint(endpoint),
		WithCleanup("first", func(context.Context) error {
			cleanupOrder = append(cleanupOrder, "first")
			return nil
		}),
		WithCleanup("second", func(context.Context) error {
			cleanupOrder = append(cleanupOrder, "second")
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Run()
	}()

	select {
	case <-endpoint.started:
	case <-time.After(time.Second):
		t.Fatal("endpoint did not start")
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after cancellation")
	}

	if endpoint.stopCount != 1 {
		t.Fatalf("expected endpoint Stop to be called once, got %d", endpoint.stopCount)
	}
	if want := []string{"second", "first"}; !reflect.DeepEqual(cleanupOrder, want) {
		t.Fatalf("cleanup order = %v, want %v", cleanupOrder, want)
	}
}

func TestApplicationRegistersDiscoverableEndpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	endpointURL, _ := url.Parse("http://127.0.0.1:8080")
	endpoint := &testDiscoverableEndpoint{
		testEndpoint: testEndpoint{started: make(chan struct{})},
		url:          endpointURL,
	}
	registrar := &testRegistrar{}

	application, err := NewApplication(ctx, &config.Application{Name: "holo", Version: "v1.2.3"},
		WithRegistrar(registrar),
		WithEndpoint(endpoint),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Run()
	}()

	select {
	case <-endpoint.started:
	case <-time.After(time.Second):
		t.Fatal("endpoint did not start")
	}

	if len(registrar.registered) != 1 {
		t.Fatalf("registered services = %d", len(registrar.registered))
	}
	node := registrar.registered[0]
	if node.Name != "holo" ||
		node.Version != "v1.2.3" ||
		node.Address != "127.0.0.1" ||
		node.Port != 8080 ||
		node.Metadata["protocol"] != "http" ||
		node.Metadata["health_path"] != "/check" ||
		node.Metadata["endpoint_name"] != endpoint.Name() ||
		len(node.Endpoints) != 1 ||
		node.Endpoints[0] != "http://127.0.0.1:8080" {
		t.Fatalf("registered node = %+v", node)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after cancellation")
	}
	if len(registrar.unregistered) != 1 || registrar.unregistered[0].ID != node.ID {
		t.Fatalf("unregistered services = %+v", registrar.unregistered)
	}
}

func TestApplicationRegisterFailureDoesNotStartEndpoint(t *testing.T) {
	ctx := context.Background()
	endpointURL, _ := url.Parse("grpc://127.0.0.1:50051")
	endpoint := &testDiscoverableEndpoint{
		testEndpoint: testEndpoint{started: make(chan struct{})},
		url:          endpointURL,
	}
	registrar := &testRegistrar{registerErr: errors.New("registry unavailable")}

	application, err := NewApplication(ctx, &config.Application{Name: "holo"},
		WithRegistrar(registrar),
		WithEndpoint(endpoint),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	if err := application.Run(); err == nil {
		t.Fatal("Run() error = nil")
	}
	select {
	case <-endpoint.started:
		t.Fatal("endpoint started after registration failure")
	default:
	}
}

type testEndpoint struct {
	started   chan struct{}
	stopCount int
}

func (e *testEndpoint) Name() string {
	return "test"
}

func (e *testEndpoint) Start(ctx context.Context) error {
	close(e.started)
	<-ctx.Done()
	return ctx.Err()
}

func (e *testEndpoint) Stop(context.Context) error {
	e.stopCount++
	return nil
}

type testDiscoverableEndpoint struct {
	testEndpoint
	url *url.URL
}

func (e *testDiscoverableEndpoint) Endpoint() (*url.URL, error) {
	return e.url, nil
}

type testRegistrar struct {
	registerErr  error
	registered   []*registry.ServiceNode
	unregistered []*registry.ServiceNode
}

func (r *testRegistrar) Register(_ context.Context, service *registry.ServiceNode) error {
	if r.registerErr != nil {
		return r.registerErr
	}
	r.registered = append(r.registered, service)
	return nil
}

func (r *testRegistrar) Unregister(_ context.Context, service *registry.ServiceNode) error {
	r.unregistered = append(r.unregistered, service)
	return nil
}

func (r *testRegistrar) GetService(context.Context, string) ([]*registry.ServiceNode, error) {
	return nil, nil
}

func (r *testRegistrar) Subscribe(context.Context, string) error {
	return nil
}
