package app

import (
	"context"
	"errors"
	"github.com/wplbyx/modular/packages/core"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wplbyx/modular/packages/config"
)

// --- test Resource ---

type testResource struct {
	name      string
	initErr   error
	closeErr  error
	initOrder *[]string
}

func (r *testResource) Name() string { return r.name }

func (r *testResource) Setup(ctx context.Context) error {
	if r.initErr != nil {
		return r.initErr
	}
	if r.initOrder != nil {
		*r.initOrder = append(*r.initOrder, r.name)
	}
	return nil
}

func (r *testResource) Close(ctx context.Context) error {
	if r.initOrder != nil {
		*r.initOrder = append(*r.initOrder, r.name)
	}
	return r.closeErr
}

// --- test Endpoint ---

type startBehavior int

const (
	startBlock startBehavior = iota
	startReturnNil
	startReturnErr
)

type testEndpoint struct {
	started       chan struct{}
	stopCount     int
	startBehavior startBehavior
	startErr      error
}

func (e *testEndpoint) Name() string { return "test" }

func (e *testEndpoint) Startup(ctx context.Context) error {
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

func (e *testEndpoint) Shutdown(context.Context) error {
	e.stopCount++
	return nil
}

// --- test Registrar ---

type testRegistrar struct {
	registerErr  error
	registered   []*core.ServiceNode
	unregistered []*core.ServiceNode
}

func (r *testRegistrar) Register(_ context.Context, node *core.ServiceNode) error {
	if r.registerErr != nil {
		return r.registerErr
	}
	r.registered = append(r.registered, node)
	return nil
}

func (r *testRegistrar) Unregister(_ context.Context, node *core.ServiceNode) error {
	r.unregistered = append(r.unregistered, node)
	return nil
}

// --- tests ---

func TestApplicationRunStopsEndpointAndClosesResource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	endpoint := &testEndpoint{started: make(chan struct{})}
	var order []string
	res := &testResource{name: "db", initOrder: &order}

	application, err := NewApplication(ctx, &config.Application{Name: "test"},
		WithResource(res),
		WithEndpoint(endpoint),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- application.Run() }()

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
		t.Fatal("Run() did not return")
	}

	if endpoint.stopCount != 1 {
		t.Fatalf("expected Shutdown=1, got %d", endpoint.stopCount)
	}
	// Setup (FIFO) then Close (LIFO) — single resource: init=first, close=second
	want := []string{"db", "db"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("resource order = %v, want %v", order, want)
	}
}

func TestApplicationRegistersServiceNode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	endpoint := &testEndpoint{started: make(chan struct{})}
	node := core.NewServiceNode(
		"holo", "v1.2.3",
		core.Transport{Protocol: "http", Address: "127.0.0.1", Port: 8080, HealthPath: "/health"},
	)
	reg := &testRegistrar{}

	application, err := NewApplication(ctx, &config.Application{Name: "holo", Version: "v1.2.3"},
		WithRegistrar(reg),
		WithServiceNode(node),
		WithEndpoint(endpoint),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- application.Run() }()

	select {
	case <-endpoint.started:
	case <-time.After(time.Second):
		t.Fatal("endpoint did not start")
	}

	if len(reg.registered) != 1 {
		t.Fatalf("registered = %d", len(reg.registered))
	}
	regNode := reg.registered[0]
	if regNode.Name != "holo" || regNode.Version != "v1.2.3" {
		t.Fatalf("identity not set: %+v", regNode)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return")
	}
	if len(reg.unregistered) != 1 || reg.unregistered[0].ID != node.ID {
		t.Fatalf("unregistered = %+v", reg.unregistered)
	}
}

func TestApplicationRegisterFailureDoesNotStartEndpoint(t *testing.T) {
	ctx := context.Background()
	endpoint := &testEndpoint{started: make(chan struct{})}
	node := core.NewServiceNode(
		"holo", "",
		core.Transport{Protocol: "grpc", Address: "127.0.0.1", Port: 50051},
	)
	reg := &testRegistrar{registerErr: errors.New("registry unavailable")}

	application, err := NewApplication(ctx, &config.Application{Name: "holo"},
		WithRegistrar(reg),
		WithServiceNode(node),
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

func TestApplicationRunResourceInitFails(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("init boom")
	res := &testResource{name: "db", initErr: sentinel}

	application, err := NewApplication(ctx, &config.Application{Name: "test"},
		WithResource(res),
		WithEndpoint(&testEndpoint{started: make(chan struct{})}),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	err = application.Run()
	if err == nil || !strings.Contains(err.Error(), "init boom") {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestApplicationRunParallelStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stopDelay := 100 * time.Millisecond
	var stopCount int64
	ep1 := &slowEndpoint{started: make(chan struct{}), stopDelay: stopDelay, count: &stopCount}
	ep2 := &slowEndpoint{started: make(chan struct{}), stopDelay: stopDelay, count: &stopCount}

	application, err := NewApplication(ctx, &config.Application{Name: "test"},
		WithEndpoint(ep1),
		WithEndpoint(ep2),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- application.Run() }()

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
		if elapsed > 300*time.Millisecond {
			t.Fatalf("stop took %v, expected parallel", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run() did not return")
	}
}

func TestApplicationRunNoEndpointsExitsCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	res := &testResource{name: "only", initOrder: &[]string{}}

	application, err := NewApplication(ctx, &config.Application{Name: "test"},
		WithResource(res),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- application.Run() }()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() hung with no endpoints")
	}
}

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
		WithEndpoint(ep),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- application.Run() }()

	select {
	case runErr := <-errCh:
		if runErr == nil || !errors.Is(runErr, endpointErr) {
			t.Fatalf("Run() error = %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return")
	}
}

// 提前返回路径现在会合并 shutdown 错误（而非丢弃），
// 验证资源初始化失败时，Run 返回的错误同时包含 init 错误与 close 错误。
func TestApplicationRunShutdownErrorJoinedOnEarlyReturn(t *testing.T) {
	ctx := context.Background()
	initErr := errors.New("init boom")
	closeErr := errors.New("close boom")
	var order []string
	ready := &testResource{name: "ready", closeErr: closeErr, initOrder: &order}
	failed := &testResource{name: "failed", initErr: initErr, closeErr: errors.New("failed close should not run"), initOrder: &order}

	application, err := NewApplication(ctx, &config.Application{Name: "test"},
		WithResource(ready),
		WithResource(failed),
		WithEndpoint(&testEndpoint{started: make(chan struct{})}),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	runErr := application.Run()
	if runErr == nil {
		t.Fatal("Run() error = nil")
	}
	if !errors.Is(runErr, initErr) {
		t.Fatalf("Run() error missing init error: %v", runErr)
	}
	if !errors.Is(runErr, closeErr) {
		t.Fatalf("Run() error missing shutdown (close) error: %v", runErr)
	}

	wantOrder := []string{"ready", "ready"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("resource order = %v, want %v", order, wantOrder)
	}
}

func TestApplicationRunDoesNotCloseResourceThatFailedSetup(t *testing.T) {
	ctx := context.Background()
	var order []string
	setupErr := errors.New("second setup boom")
	first := &testResource{name: "first", initOrder: &order}
	second := &testResource{name: "second", initErr: setupErr, initOrder: &order}
	third := &testResource{name: "third", initOrder: &order}

	application, err := NewApplication(ctx, &config.Application{Name: "test"},
		WithResource(first),
		WithResource(second),
		WithResource(third),
		WithEndpoint(&testEndpoint{started: make(chan struct{})}),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	runErr := application.Run()
	if !errors.Is(runErr, setupErr) {
		t.Fatalf("Run() error = %v, want setupErr", runErr)
	}

	want := []string{"first", "first"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("resource order = %v, want %v", order, want)
	}
}

func TestApplicationCloseAndRunShutdownOnlyOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var order []string
	res := &testResource{name: "db", initOrder: &order}
	endpoint := &slowEndpoint{started: make(chan struct{}), stopDelay: 10 * time.Millisecond, count: new(int64)}

	application, err := NewApplication(ctx, &config.Application{Name: "test"},
		WithResource(res),
		WithEndpoint(endpoint),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- application.Run() }()

	select {
	case <-endpoint.started:
	case <-time.After(time.Second):
		t.Fatal("endpoint did not start")
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := application.Close(context.Background()); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		cancel()
	}()
	wg.Wait()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return")
	}

	if got := atomic.LoadInt64(endpoint.count); got != 1 {
		t.Fatalf("endpoint shutdown count = %d, want 1", got)
	}
	wantOrder := []string{"db", "db"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("resource order = %v, want %v", order, wantOrder)
	}
}

// --- helpers ---

type slowEndpoint struct {
	started   chan struct{}
	stopDelay time.Duration
	count     *int64
	mu        sync.Mutex
	stopped   bool
}

func (e *slowEndpoint) Name() string { return "slow" }

func (e *slowEndpoint) Startup(ctx context.Context) error {
	close(e.started)
	<-ctx.Done()
	return ctx.Err()
}

func (e *slowEndpoint) Shutdown(context.Context) error {
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
