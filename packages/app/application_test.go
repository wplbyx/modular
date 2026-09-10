package app

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/core"
	"github.com/wplbyx/modular/packages/health"
	modularlog "github.com/wplbyx/modular/packages/log"
)

// --- test Resource ---

type testResource struct {
	name      string
	initErr   error
	closeErr  error
	initOrder *[]string
}

func TestNewApplication_RegistrarRequiresServiceNode(t *testing.T) {
	application, err := NewApplication(
		context.Background(),
		&configitem.Application{Name: "test"},
		modularlog.Default(),
		WithRegistrar(&testRegistrar{}),
	)

	if application != nil {
		t.Fatal("NewApplication() application is not nil")
	}
	if err == nil || !strings.Contains(err.Error(), "service node is required") {
		t.Fatalf("NewApplication() error = %v", err)
	}
}

func TestApplicationCloseBeforeRun(t *testing.T) {
	endpoint := &testEndpoint{started: make(chan struct{})}
	application, err := NewApplication(
		context.Background(),
		&configitem.Application{Name: "test"},
		modularlog.Default(),
		WithEndpoint(endpoint),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	if err := application.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := application.Run(); !errors.Is(err, ErrApplicationAlreadyRun) {
		t.Fatalf("Run() error = %v, want ErrApplicationAlreadyRun", err)
	}
	if endpoint.stopCount != 0 {
		t.Fatalf("Shutdown count = %d, want 0", endpoint.stopCount)
	}
}

func TestApplicationRejectsDuplicateRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	endpoint := &testEndpoint{started: make(chan struct{})}
	application, err := NewApplication(
		ctx,
		&configitem.Application{Name: "test"},
		modularlog.Default(),
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

	if err := application.Run(); !errors.Is(err, ErrApplicationAlreadyRun) {
		t.Fatalf("second Run() error = %v, want ErrApplicationAlreadyRun", err)
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("first Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first Run() did not return")
	}
}

func TestApplicationRequiresLogger(t *testing.T) {
	application, err := NewApplication(
		context.Background(),
		&configitem.Application{Name: "test"},
		nil,
	)
	if application != nil || err == nil {
		t.Fatalf("NewApplication() = (%v, %v), want nil application and error", application, err)
	}
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

func (e *testEndpoint) Ready(ctx context.Context) error {
	select {
	case <-e.started:
		return nil
	case <-ctx.Done():
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
	registeredCh chan struct{}
}

func (r *testRegistrar) Register(_ context.Context, node *core.ServiceNode) error {
	if r.registerErr != nil {
		return r.registerErr
	}
	r.registered = append(r.registered, node)
	if r.registeredCh != nil {
		close(r.registeredCh)
	}
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

	application, err := NewApplication(ctx, &configitem.Application{Name: "test"}, modularlog.Default(),
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
	reg := &testRegistrar{registeredCh: make(chan struct{})}

	application, err := NewApplication(ctx, &configitem.Application{Name: "holo", Version: "v1.2.3"}, modularlog.Default(),
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

	select {
	case <-reg.registeredCh:
	case <-time.After(time.Second):
		t.Fatal("service node was not registered")
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

func TestApplicationRegisterFailureCleansUpStartedEndpoint(t *testing.T) {
	ctx := context.Background()
	endpoint := &testEndpoint{started: make(chan struct{})}
	node := core.NewServiceNode(
		"holo", "",
		core.Transport{Protocol: "grpc", Address: "127.0.0.1", Port: 50051},
	)
	reg := &testRegistrar{registerErr: errors.New("registry unavailable")}

	application, err := NewApplication(ctx, &configitem.Application{Name: "holo"}, modularlog.Default(),
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
		if endpoint.stopCount != 1 {
			t.Fatalf("endpoint shutdown count = %d, want 1", endpoint.stopCount)
		}
	default:
		t.Fatal("endpoint did not start before registration")
	}
}

func TestApplicationRunResourceInitFails(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("init boom")
	res := &testResource{name: "db", initErr: sentinel}

	application, err := NewApplication(ctx, &configitem.Application{Name: "test"}, modularlog.Default(),
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

	application, err := NewApplication(ctx, &configitem.Application{Name: "test"}, modularlog.Default(),
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

func TestApplicationRunNoEndpointsWaitsAndManagesResources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var order []string
	res := &testResource{name: "only", initOrder: &order}

	application, err := NewApplication(ctx, &configitem.Application{Name: "test"}, modularlog.Default(),
		WithResource(res),
	)
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- application.Run() }()

	select {
	case err := <-errCh:
		t.Fatalf("Run() returned before cancellation: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	require.NoError(t, <-errCh)
	assert.Equal(t, []string{"only", "only"}, order)
}

func TestApplicationWaitsForEndpointReadinessBeforeRegister(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{})
	endpoint := &gatedReadyEndpoint{started: make(chan struct{}), ready: ready}
	registered := make(chan struct{})
	reg := &testRegistrar{registeredCh: registered}
	node := core.NewServiceNode("test", "v1", core.Transport{Protocol: "http", Address: "127.0.0.1", Port: 8080})
	application, err := NewApplication(ctx, &configitem.Application{Name: "test"}, modularlog.Default(),
		WithEndpoint(endpoint), WithRegistrar(reg), WithServiceNode(node),
	)
	require.NoError(t, err)
	errCh := make(chan error, 1)
	go func() { errCh <- application.Run() }()
	<-endpoint.started

	select {
	case <-registered:
		t.Fatal("service node registered before endpoint readiness")
	case <-time.After(20 * time.Millisecond):
	}
	close(ready)
	select {
	case <-registered:
	case <-time.After(time.Second):
		t.Fatal("service node was not registered after readiness")
	}
	cancel()
	require.NoError(t, <-errCh)
}

func TestApplicationHealthAndShutdownOrdering(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := &eventRecorder{}
	ready := make(chan struct{})
	endpoint := &orderedEndpoint{started: make(chan struct{}), ready: ready, events: events}
	resource := &orderedResource{events: events}
	manager := health.NewManager()
	registrar := &orderedRegistrar{events: events, manager: manager, registered: make(chan struct{})}
	node := core.NewServiceNode("test", "v1", core.Transport{Protocol: "http", Address: "127.0.0.1", Port: 8080})

	application, err := NewApplication(ctx, &configitem.Application{Name: "test"}, modularlog.Default(),
		WithResource(resource),
		WithEndpoint(endpoint),
		WithRegistrar(registrar),
		WithServiceNode(node),
		WithHealthManager(manager),
	)
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() { errCh <- application.Run() }()
	<-endpoint.started
	assert.Equal(t, health.StateStarting, manager.State())
	close(ready)
	<-registrar.registered
	require.Eventually(t, func() bool { return manager.State() == health.StateReady }, time.Second, time.Millisecond)
	report := manager.DetailedReport(context.Background())
	require.Len(t, report.Checks, 1)
	assert.Equal(t, "ordered", report.Checks[0].Name)
	assert.Equal(t, health.StatusOK, report.Checks[0].Status)

	cancel()
	require.NoError(t, <-errCh)
	assert.Equal(t, health.StateDraining, manager.State())
	assert.Equal(t, []string{
		"resource-setup",
		"endpoint-startup",
		"endpoint-ready",
		"register-starting",
		"unregister-draining",
		"endpoint-shutdown",
		"resource-close",
	}, events.Values())
}

func TestApplicationShutdownTimeoutBoundsBlockingResource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	block := make(chan struct{})
	closeStarted := make(chan struct{})
	resource := core.NewManagedResource(
		"blocking",
		func(context.Context) (int, error) { return 1, nil },
		func(context.Context, int) error {
			close(closeStarted)
			<-block
			return nil
		},
	)
	endpoint := &testEndpoint{started: make(chan struct{})}
	application, err := NewApplication(
		ctx,
		&configitem.Application{Name: "test", ShutdownTimeout: 20 * time.Millisecond},
		modularlog.Default(),
		WithResource(resource),
		WithEndpoint(endpoint),
	)
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() { errCh <- application.Run() }()
	<-endpoint.started
	cancel()
	<-closeStarted
	select {
	case runErr := <-errCh:
		require.ErrorIs(t, runErr, context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("Run blocked past shutdown timeout")
	}
	close(block)
	require.NoError(t, resource.Close(context.Background()))
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

	application, err := NewApplication(ctx, &configitem.Application{Name: "test"}, modularlog.Default(),
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

	application, err := NewApplication(ctx, &configitem.Application{Name: "test"}, modularlog.Default(),
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

	application, err := NewApplication(ctx, &configitem.Application{Name: "test"}, modularlog.Default(),
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

	application, err := NewApplication(ctx, &configitem.Application{Name: "test"}, modularlog.Default(),
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

func (e *slowEndpoint) Ready(ctx context.Context) error {
	select {
	case <-e.started:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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

type gatedReadyEndpoint struct {
	started chan struct{}
	ready   chan struct{}
}

func (e *gatedReadyEndpoint) Name() string { return "gated" }

func (e *gatedReadyEndpoint) Startup(ctx context.Context) error {
	close(e.started)
	<-ctx.Done()
	return ctx.Err()
}

func (e *gatedReadyEndpoint) Ready(ctx context.Context) error {
	select {
	case <-e.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *gatedReadyEndpoint) Shutdown(context.Context) error { return nil }

type eventRecorder struct {
	mu     sync.Mutex
	events []string
}

func (recorder *eventRecorder) Add(event string) {
	recorder.mu.Lock()
	recorder.events = append(recorder.events, event)
	recorder.mu.Unlock()
}

func (recorder *eventRecorder) Values() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.events...)
}

type orderedResource struct{ events *eventRecorder }

func (resource *orderedResource) Name() string { return "ordered" }
func (resource *orderedResource) Setup(context.Context) error {
	resource.events.Add("resource-setup")
	return nil
}
func (resource *orderedResource) Close(context.Context) error {
	resource.events.Add("resource-close")
	return nil
}
func (*orderedResource) Check(context.Context) error { return nil }

type orderedEndpoint struct {
	started chan struct{}
	ready   chan struct{}
	events  *eventRecorder
}

func (endpoint *orderedEndpoint) Name() string { return "ordered" }
func (endpoint *orderedEndpoint) Startup(ctx context.Context) error {
	endpoint.events.Add("endpoint-startup")
	close(endpoint.started)
	<-ctx.Done()
	return ctx.Err()
}
func (endpoint *orderedEndpoint) Ready(ctx context.Context) error {
	select {
	case <-endpoint.ready:
		endpoint.events.Add("endpoint-ready")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (endpoint *orderedEndpoint) Shutdown(context.Context) error {
	endpoint.events.Add("endpoint-shutdown")
	return nil
}

type orderedRegistrar struct {
	events     *eventRecorder
	manager    *health.Manager
	registered chan struct{}
}

func (registrar *orderedRegistrar) Register(context.Context, *core.ServiceNode) error {
	registrar.events.Add("register-" + string(registrar.manager.State()))
	close(registrar.registered)
	return nil
}
func (registrar *orderedRegistrar) Unregister(context.Context, *core.ServiceNode) error {
	registrar.events.Add("unregister-" + string(registrar.manager.State()))
	return nil
}
