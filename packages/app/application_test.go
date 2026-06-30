package app

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/resource"

	"modular/packages/config"
	"modular/packages/registry"
)

// testResource returns a minimal non-nil resource so that Run() passes its nil check.
func testResource() *resource.Resource {
	res, _ := resource.Merge(resource.Empty(), resource.Empty())
	return res
}

func TestApplicationRunStopsEndpointAndRunsCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	endpoint := &testEndpoint{started: make(chan struct{})}
	var cleanupOrder []string

	application, err := NewApplication(ctx, &config.Application{Name: "test"},
		WithResource(testResource()),
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
		WithResource(testResource()),
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
		WithResource(testResource()),
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

// TestApplicationRunEndpointNilExitTriggersShutdown verifies that when an
// endpoint's Start returns nil (silent exit), the application shuts down and
// other endpoints are stopped.
func TestApplicationRunEndpointNilExitTriggersShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ep1 returns nil immediately from Start (simulating a silent exit).
	ep1 := &testEndpoint{
		started:       make(chan struct{}),
		startBehavior: startReturnNil,
	}
	// ep2 blocks on ctx until Stop is called.
	ep2 := &testEndpoint{started: make(chan struct{})}

	application, err := NewApplication(ctx, &config.Application{Name: "test"},
		WithResource(testResource()),
		WithEndpoint(ep1),
		WithEndpoint(ep2),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Run()
	}()

	select {
	case <-ep1.started:
	case <-time.After(time.Second):
		t.Fatal("ep1 did not start")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after ep1 nil exit")
	}

	if ep2.stopCount != 1 {
		t.Fatalf("expected ep2 Stop to be called once, got %d", ep2.stopCount)
	}
}

// TestApplicationRunEndpointErrorPropagated verifies that an error returned
// from Start is propagated through Run.
func TestApplicationRunEndpointErrorPropagated(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	endpointErr := errors.New("start failed")
	ep := &testEndpoint{
		started:       make(chan struct{}),
		startBehavior: startReturnErr,
		startErr:      endpointErr,
	}

	application, err := NewApplication(ctx, &config.Application{Name: "test"},
		WithResource(testResource()),
		WithEndpoint(ep),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Run()
	}()

	select {
	case runErr := <-errCh:
		if runErr == nil {
			t.Fatal("Run() error = nil, want non-nil")
		}
		if !errors.Is(runErr, endpointErr) {
			t.Fatalf("Run() error = %v, want it to wrap %v", runErr, endpointErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after endpoint error")
	}
}

// TestApplicationRunCleanupTimeout verifies that a blocking cleanup does not
// hang Run forever; it must return within the shutdown timeout.
func TestApplicationRunCleanupTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ep := &testEndpoint{started: make(chan struct{})}

	application, err := NewApplication(ctx, &config.Application{Name: "test"},
		WithResource(testResource()),
		WithEndpoint(ep),
		WithCleanup("blocking", func(innerCtx context.Context) error {
			<-innerCtx.Done()
			return innerCtx.Err()
		}),
		WithShutdownTimeout(200*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Run()
	}()

	select {
	case <-ep.started:
	case <-time.After(time.Second):
		t.Fatal("endpoint did not start")
	}

	cancel()

	select {
	case <-errCh:
		// Run returned within a reasonable time despite the blocking cleanup.
	case <-time.After(5 * time.Second):
		t.Fatal("Run() hung on blocking cleanup")
	}
}

// TestApplicationRunParallelStop verifies that endpoints are stopped in
// parallel: total stop time should be close to max(durations), not sum.
func TestApplicationRunParallelStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stopDelay := 100 * time.Millisecond

	var stopCount int64
	ep1 := &slowEndpoint{
		started:   make(chan struct{}),
		stopDelay: stopDelay,
		count:     &stopCount,
	}
	ep2 := &slowEndpoint{
		started:   make(chan struct{}),
		stopDelay: stopDelay,
		count:     &stopCount,
	}

	application, err := NewApplication(ctx, &config.Application{Name: "test"},
		WithResource(testResource()),
		WithEndpoint(ep1),
		WithEndpoint(ep2),
		WithShutdownTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Run()
	}()

	select {
	case <-ep1.started:
	case <-time.After(time.Second):
		t.Fatal("ep1 did not start")
	}
	select {
	case <-ep2.started:
	case <-time.After(time.Second):
		t.Fatal("ep2 did not start")
	}

	cancel()

	start := time.Now()
	select {
	case err := <-errCh:
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		// Parallel: ~100ms. Serial would be ~200ms. Allow generous 300ms margin.
		if elapsed > 300*time.Millisecond {
			t.Fatalf("stop took %v, expected parallel (~%v)", elapsed, stopDelay)
		}
		if atomic.LoadInt64(&stopCount) != 2 {
			t.Fatalf("expected both endpoints stopped, got %d", stopCount)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run() did not return")
	}
}

// --- test helpers ---

type startBehavior int

const (
	startBlock     startBehavior = iota // block on ctx.Done(), return ctx.Err()
	startReturnNil                      // return nil immediately
	startReturnErr                      // return startErr immediately
)

type testEndpoint struct {
	started       chan struct{}
	stopCount     int
	startBehavior startBehavior
	startErr      error
}

func (e *testEndpoint) Name() string {
	return "test"
}

func (e *testEndpoint) Start(ctx context.Context) error {
	close(e.started)
	switch e.startBehavior {
	case startReturnNil:
		return nil
	case startReturnErr:
		return e.startErr
	default:
		<-ctx.Done()
		return ctx.Err()
	}
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

// slowEndpoint blocks on ctx in Start and sleeps for stopDelay in Stop.
type slowEndpoint struct {
	started   chan struct{}
	stopDelay time.Duration
	count     *int64
	mu        sync.Mutex
	stopped   bool
}

func (e *slowEndpoint) Name() string { return "slow" }

func (e *slowEndpoint) Start(ctx context.Context) error {
	close(e.started)
	<-ctx.Done()
	return ctx.Err()
}

func (e *slowEndpoint) Stop(context.Context) error {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return nil
	}
	e.stopped = true
	e.mu.Unlock()
	time.Sleep(e.stopDelay)
	atomic.AddInt64(e.count, 1)
	return nil
}
