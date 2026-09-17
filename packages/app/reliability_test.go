package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/core"
	"github.com/wplbyx/modular/packages/health"
	modularlog "github.com/wplbyx/modular/packages/log"
)

type delayedRegistrar struct{ entered, release, removed chan struct{} }

func (r *delayedRegistrar) Register(context.Context, *core.ServiceNode) error {
	close(r.entered)
	<-r.release
	return nil
}

type stuckEndpoint struct{ started, release chan struct{} }

func (*stuckEndpoint) Name() string                    { return "stuck" }
func (e *stuckEndpoint) Startup(context.Context) error { close(e.started); <-e.release; return nil }
func (*stuckEndpoint) Shutdown(context.Context) error  { return nil }
func TestApplication_StuckEndpointBoundsRunAndKeepsResources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := &stuckEndpoint{make(chan struct{}), make(chan struct{})}
	defer close(e.release)
	var closed atomic.Bool
	r := core.NewFuncResource("db", nil, func(context.Context) error { closed.Store(true); return nil })
	a, err := NewApplication(ctx, &configitem.Application{Name: "test", ShutdownTimeout: 20 * time.Millisecond}, modularlog.Default(), WithEndpoint(e), WithResource(r))
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- a.Run() }()
	<-e.started
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("Run exceeded shutdown budget")
	}
	require.False(t, closed.Load())
}
func (r *delayedRegistrar) Unregister(context.Context, *core.ServiceNode) error {
	close(r.removed)
	return nil
}
func TestApplication_LateRegistrationCannotBecomeReady(t *testing.T) {
	r := &delayedRegistrar{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	manager := health.NewManager()
	a, err := NewApplication(context.Background(), &configitem.Application{Name: "test", ShutdownTimeout: 30 * time.Millisecond}, modularlog.Default(), WithRegistrar(r), WithServiceNode(&core.ServiceNode{ID: "node"}), WithHealthManager(manager))
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- a.Run() }()
	<-r.entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = a.Close(ctx)
	close(r.release)
	select {
	case <-r.removed:
	case <-ctx.Done():
		t.Fatal("late registration was not removed")
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("Run did not finish")
	}
	require.Equal(t, health.StateDraining, manager.State())
}
