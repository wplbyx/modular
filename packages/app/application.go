package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/core"
	"github.com/wplbyx/modular/packages/health"
	"github.com/wplbyx/modular/packages/log"
	"github.com/wplbyx/modular/packages/registry"
)

const defaultShutdownTimeout = 10 * time.Second

var (
	// ErrApplicationAlreadyRun 表示 Application 已经启动过或已被关闭。
	ErrApplicationAlreadyRun = errors.New("application has already run")
)

// Option 初始化应用时的函数式选项。
type Option func(*Application)

type applicationState uint8

const (
	applicationNew applicationState = iota
	applicationRunning
	applicationStopping
	applicationStopped
)

// Application 是应用程序生命周期编排器，不处理业务逻辑。
//
// 生命周期顺序：Resource.Setup（FIFO）-> Endpoint.Startup/Ready（并行）->
// 注册 ServiceNode -> 进入 Ready。关闭时先进入 Draining 并反注册 ServiceNode，
// 再 Endpoint.Shutdown（并行）-> Resource.Close（LIFO）。
type Application struct {
	ctx context.Context
	cfg *configitem.Application

	node       *core.ServiceNode
	registrar  registry.Registrar
	endpoints  []core.Endpoint
	resources  []core.Resource
	logger     log.Logger
	readiness  *health.Manager
	registered bool

	// initDone 发布初始化集合；关闭只在初始化结束后访问它们。
	readyResources  []core.Resource
	activeEndpoints []core.Endpoint

	stateLock sync.Mutex
	state     applicationState
	runCancel context.CancelFunc

	initDone     chan struct{}
	shutdownDone chan struct{}
	startupWG    sync.WaitGroup
	shutdownOnce sync.Once
	shutdownErr  error

	shutdownTimeout time.Duration
}

// NewApplication 创建应用程序实例。
func NewApplication(ctx context.Context, cfg *configitem.Application, logger log.Logger, options ...Option) (*Application, error) {
	if ctx == nil {
		return nil, errors.New("application context is nil")
	}
	if cfg == nil {
		return nil, errors.New("config.Application instance is nil")
	}
	if logger == nil {
		return nil, errors.New("application logger is nil")
	}

	application := &Application{
		ctx:             ctx,
		cfg:             cfg,
		endpoints:       make([]core.Endpoint, 0),
		resources:       make([]core.Resource, 0),
		readyResources:  make([]core.Resource, 0),
		activeEndpoints: make([]core.Endpoint, 0),
		logger:          logger.Named("application"),
		state:           applicationNew,
		shutdownTimeout: defaultShutdownTimeout,
		initDone:        make(chan struct{}), shutdownDone: make(chan struct{}),
	}
	if cfg.ShutdownTimeout > 0 {
		application.shutdownTimeout = cfg.ShutdownTimeout
	}

	for _, option := range options {
		if option != nil {
			option(application)
		}
	}
	if application.registrar != nil && application.node == nil {
		return nil, errors.New("service node is required when registrar is configured")
	}
	if application.readiness != nil {
		for _, resource := range application.resources {
			checker, ok := resource.(health.Checker)
			if !ok {
				continue
			}
			if err := application.readiness.Register(checker); err != nil {
				return nil, fmt.Errorf("register resource readiness %s: %w", resource.Name(), err)
			}
		}
	}

	return application, nil
}

// Run 启动应用程序。每个 Application 最多只能调用一次 Run。
func (application *Application) Run() (returnedErr error) {
	runCtx, cancel, err := application.startRun()
	if err != nil {
		return err
	}
	defer func() {
		cancel()
		application.finishRun()
		if returnedErr != nil {
			application.logger.Error(application.ctx, "application stopped with error", zap.Error(returnedErr))
		}
		application.logger.Info(application.ctx, "application exited")
	}()
	application.logger.Info(runCtx, "application starting", zap.String("name", application.cfg.Name))
	group, groupCtx := errgroup.WithContext(runCtx)
	groupResult := make(chan error, 1)
	prepareResult := make(chan error, 1)
	go func() {
		prepareResult <- func() error {
			defer close(application.initDone)
			err := application.setupResources(groupCtx)
			if err == nil && groupCtx.Err() == nil {
				application.startEndpoints(group, groupCtx)
			}
			go func() { groupResult <- group.Wait() }()
			return err
		}()
	}()
	var prepareErr error
	select {
	case prepareErr = <-prepareResult:
	case <-runCtx.Done():
		prepareErr = runCtx.Err()
	}
	// With no endpoints errgroup.Wait cancels groupCtx immediately; readiness uses runCtx instead.
	readyCtx := groupCtx
	if len(application.endpoints) == 0 {
		readyCtx = runCtx
	}
	if prepareErr == nil {
		prepareErr = callWithContext(readyCtx, func() error { return application.waitEndpointsReady(readyCtx) })
	}
	if prepareErr == nil {
		prepareErr = readyCtx.Err()
	}
	if prepareErr == nil {
		prepareErr = application.registerNode(readyCtx)
	}
	if prepareErr == nil {
		application.stateLock.Lock()
		if application.state == applicationRunning && readyCtx.Err() == nil {
			if application.readiness != nil {
				application.readiness.SetReady()
			}
		}
		application.stateLock.Unlock()
		<-readyCtx.Done()
	}
	shutdownErr := application.Close(context.Background())
	var groupErr error
	if !errors.Is(shutdownErr, context.DeadlineExceeded) {
		groupErr = <-groupResult
	} else {
		select {
		case groupErr = <-groupResult:
		default:
		}
	}
	if errors.Is(prepareErr, context.Canceled) || errors.Is(prepareErr, runCtx.Err()) {
		prepareErr = nil
	}
	if errors.Is(groupErr, context.Canceled) {
		groupErr = nil
	}
	return errors.Join(prepareErr, groupErr, shutdownErr)
}

// Close 的调用者独立等待；首次关闭拥有应用配置的总关闭预算。
func (application *Application) Close(ctx context.Context) error {
	application.stateLock.Lock()
	if application.state == applicationNew {
		application.state = applicationStopped
		application.stateLock.Unlock()
		return nil
	}
	if application.state == applicationStopped {
		err := application.shutdownErr
		application.stateLock.Unlock()
		return err
	}
	application.state = applicationStopping
	if application.readiness != nil {
		application.readiness.SetDraining()
	}
	if application.runCancel != nil {
		application.runCancel()
	}
	application.stateLock.Unlock()
	application.shutdownOnce.Do(func() {
		go func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), application.shutdownTimeout)
			defer cancel()
			err := application.shutdown(shutdownCtx)
			application.stateLock.Lock()
			application.shutdownErr = err
			application.stateLock.Unlock()
			close(application.shutdownDone)
		}()
	})
	select {
	case <-application.shutdownDone:
		application.stateLock.Lock()
		defer application.stateLock.Unlock()
		return application.shutdownErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (application *Application) startRun() (context.Context, context.CancelFunc, error) {
	application.stateLock.Lock()
	defer application.stateLock.Unlock()

	if application.state != applicationNew {
		return nil, nil, ErrApplicationAlreadyRun
	}
	runCtx, cancel := context.WithCancel(application.ctx)
	application.state = applicationRunning
	application.runCancel = cancel
	return runCtx, cancel, nil
}

func (application *Application) finishRun() {
	application.stateLock.Lock()
	application.state = applicationStopped
	application.runCancel = nil
	application.stateLock.Unlock()
}

// shutdown 在同一个超时预算内按顺序执行全部关闭步骤。
func (application *Application) shutdown(ctx context.Context) error {
	select {
	case <-application.initDone:
	case <-ctx.Done():
		return fmt.Errorf("shutdown waiting for resource setup: %w", ctx.Err())
	}

	if application.readiness != nil {
		application.readiness.SetDraining()
	}
	unregisterErr := application.unregisterNode(ctx)
	endpointErr := application.shutdownEndpoints(ctx)
	startupErr := callWithContext(ctx, func() error { application.startupWG.Wait(); return nil })
	if startupErr != nil {
		return errors.Join(unregisterErr, endpointErr, fmt.Errorf("endpoint Startup still running: %w", startupErr))
	}
	if ctx.Err() != nil {
		return errors.Join(unregisterErr, endpointErr, ctx.Err())
	}
	return errors.Join(unregisterErr, endpointErr, application.closeResources(ctx))
}

func (application *Application) setupResources(ctx context.Context) error {
	for _, resource := range application.resources {
		if err := ctx.Err(); err != nil {
			return err
		}
		application.logger.Info(ctx, "resource initializing", zap.String("resource", resource.Name()))
		if err := resource.Setup(ctx); err != nil {
			return fmt.Errorf("init resource %s: %w", resource.Name(), err)
		}
		application.readyResources = append(application.readyResources, resource)
	}
	return nil
}

func (application *Application) closeResources(ctx context.Context) error {
	var errs error
	for i := len(application.readyResources) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return errors.Join(errs, err)
		}
		resource := application.readyResources[i]
		application.logger.Info(ctx, "resource closing", zap.String("resource", resource.Name()))
		if err := callWithContext(ctx, func() error { return resource.Close(ctx) }); err != nil {
			errs = errors.Join(errs, fmt.Errorf("close resource %s: %w", resource.Name(), err))
		}
	}
	return errs
}

func (application *Application) registerNode(ctx context.Context) error {
	if application.registrar == nil {
		return nil
	}
	return callWithContext(ctx, func() error {
		if err := application.registrar.Register(ctx, application.node); err != nil {
			return fmt.Errorf("register service node: %w", err)
		}
		application.stateLock.Lock()
		if application.state == applicationRunning && ctx.Err() == nil {
			application.registered = true
			application.stateLock.Unlock()
			return nil
		}
		application.stateLock.Unlock()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), application.shutdownTimeout)
		defer cancel()
		err := application.registrar.Unregister(cleanupCtx, application.node)
		if err != nil {
			application.logger.Error(cleanupCtx, "late registration cleanup failed", zap.Error(err))
		}
		return errors.Join(context.Canceled, err)
	})
}

func (application *Application) unregisterNode(ctx context.Context) error {
	application.stateLock.Lock()
	registered := application.registered
	application.registered = false
	application.stateLock.Unlock()
	if !registered {
		return nil
	}
	if err := callWithContext(ctx, func() error {
		return application.registrar.Unregister(ctx, application.node)
	}); err != nil {
		return fmt.Errorf("unregister service node %s: %w", application.node.ID, err)
	}
	application.logger.Info(ctx, "service node unregistered", zap.String("node", application.node.ID))
	return nil
}

func (application *Application) startEndpoints(
	group *errgroup.Group,
	ctx context.Context,
) {
	for _, endpoint := range application.endpoints {
		application.activeEndpoints = append(application.activeEndpoints, endpoint)
		application.startupWG.Add(1)
		group.Go(func() error {
			defer application.startupWG.Done()
			if ctx.Err() != nil {
				return nil
			}
			application.logger.Info(ctx, "endpoint starting", zap.String("endpoint", endpoint.Name()))
			err := endpoint.Startup(ctx)
			if err == nil && ctx.Err() == nil {
				return fmt.Errorf("endpoint %s exited unexpectedly", endpoint.Name())
			}
			if err != nil && ctx.Err() == nil {
				return fmt.Errorf("endpoint %s exited unexpectedly: %w", endpoint.Name(), err)
			}
			return nil
		})
	}
}

func (application *Application) waitEndpointsReady(ctx context.Context) error {
	group, readyCtx := errgroup.WithContext(ctx)
	for _, endpoint := range application.activeEndpoints {
		readyEndpoint, ok := endpoint.(core.ReadyEndpoint)
		if !ok {
			continue
		}
		group.Go(func() error {
			if err := readyEndpoint.Ready(readyCtx); err != nil {
				return fmt.Errorf("endpoint %s readiness: %w", endpoint.Name(), err)
			}
			return nil
		})
	}
	return group.Wait()
}

func (application *Application) shutdownEndpoints(ctx context.Context) error {
	var (
		errs error
		mu   sync.Mutex
		wait sync.WaitGroup
	)

	for _, endpoint := range application.activeEndpoints {
		wait.Add(1)
		go func() {
			defer wait.Done()
			application.logger.Info(ctx, "endpoint shutting down", zap.String("endpoint", endpoint.Name()))
			if err := endpoint.Shutdown(ctx); err != nil {
				mu.Lock()
				errs = errors.Join(errs, fmt.Errorf("stop endpoint %s: %w", endpoint.Name(), err))
				mu.Unlock()
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wait.Wait()
		close(done)
	}()
	snapshotErrors := func() error {
		mu.Lock()
		defer mu.Unlock()
		return errs
	}
	select {
	case <-done:
		return snapshotErrors()
	case <-ctx.Done():
		return errors.Join(snapshotErrors(), fmt.Errorf("shutdown endpoints: %w", ctx.Err()))
	}
}

// callWithContext bounds lifecycle callbacks that fail to return after ctx is done.
// The callback still owns the responsibility to observe ctx and release its goroutine.
func callWithContext(ctx context.Context, call func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
