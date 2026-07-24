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
// 生命周期顺序：Resource.Setup（FIFO）-> 注册 ServiceNode ->
// Endpoint.Startup（并行阻塞）-> Endpoint.Shutdown（并行）->
// 反注册 ServiceNode -> Resource.Close（LIFO）。
type Application struct {
	ctx context.Context
	cfg *configitem.Application

	node       *core.ServiceNode
	registrar  registry.Registrar
	endpoints  []core.Endpoint
	resources  []core.Resource
	logger     *zap.Logger
	registered bool

	// lifecycleLock 防止启动准备和关闭过程交错修改生命周期集合。
	lifecycleLock   sync.Mutex
	readyResources  []core.Resource
	activeEndpoints []core.Endpoint

	stateLock sync.Mutex
	state     applicationState
	runCancel context.CancelFunc

	shutdownOnce sync.Once
	shutdownErr  error

	shutdownTimeout time.Duration
}

// NewApplication 创建应用程序实例。
func NewApplication(ctx context.Context, cfg *configitem.Application, options ...Option) (*Application, error) {
	if ctx == nil {
		return nil, errors.New("application context is nil")
	}
	if cfg == nil {
		return nil, errors.New("config.Application instance is nil")
	}

	application := &Application{
		ctx:             ctx,
		cfg:             cfg,
		endpoints:       make([]core.Endpoint, 0),
		resources:       make([]core.Resource, 0),
		readyResources:  make([]core.Resource, 0),
		activeEndpoints: make([]core.Endpoint, 0),
		state:           applicationNew,
		shutdownTimeout: defaultShutdownTimeout,
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

	return application, nil
}

// Run 启动应用程序。每个 Application 最多只能调用一次 Run。
func (application *Application) Run() error {
	runCtx, cancel, err := application.startRun()
	if err != nil {
		return err
	}
	defer func() {
		cancel()
		application.finishRun()
	}()

	if len(application.endpoints) == 0 {
		application.getLogger().Warn("application has no endpoints; Run will exit immediately")
		return nil
	}

	application.getLogger().Info("application starting", zap.String("name", application.cfg.Name))

	triggerShutdown := func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), application.shutdownTimeout)
		defer shutdownCancel()
		_ = application.Close(shutdownCtx)
	}

	group, groupCtx := errgroup.WithContext(runCtx)
	application.lifecycleLock.Lock()
	prepareErr := application.setupResources(groupCtx)
	if prepareErr == nil {
		prepareErr = application.registerNode(groupCtx)
	}
	if prepareErr == nil {
		prepareErr = groupCtx.Err()
	}
	if prepareErr == nil {
		application.startEndpoints(group, groupCtx, cancel)
	}
	application.lifecycleLock.Unlock()

	if prepareErr != nil {
		triggerShutdown()
		return errors.Join(prepareErr, application.shutdownErr)
	}

	go func() {
		<-groupCtx.Done()
		triggerShutdown()
	}()

	runErr := group.Wait()
	triggerShutdown()

	if runErr != nil {
		application.getLogger().Error("application stopped with error", zap.Error(runErr))
	}
	if application.shutdownErr != nil {
		application.getLogger().Error("application shutdown failed", zap.Error(application.shutdownErr))
	}
	application.getLogger().Info("application exited")
	return errors.Join(runErr, application.shutdownErr)
}

// Close 手动触发关闭。Close 在 Run 前调用会关闭 Application，但不会触发任何依赖的生命周期。
func (application *Application) Close(ctx context.Context) error {
	application.stateLock.Lock()
	switch application.state {
	case applicationNew:
		application.state = applicationStopped
		application.stateLock.Unlock()
		return nil
	case applicationStopped:
		err := application.shutdownErr
		application.stateLock.Unlock()
		return err
	case applicationRunning:
		application.state = applicationStopping
	}
	if application.runCancel != nil {
		application.runCancel()
	}
	application.stateLock.Unlock()

	application.shutdownOnce.Do(func() {
		application.shutdownErr = application.shutdown(ctx)
	})
	return application.shutdownErr
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
	application.lifecycleLock.Lock()
	defer application.lifecycleLock.Unlock()

	return errors.Join(
		application.shutdownEndpoints(ctx),
		application.unregisterNode(ctx),
		application.closeResources(ctx),
	)
}

func (application *Application) setupResources(ctx context.Context) error {
	for _, resource := range application.resources {
		application.getLogger().Info("resource initializing", zap.String("resource", resource.Name()))
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
		resource := application.readyResources[i]
		application.getLogger().Info("resource closing", zap.String("resource", resource.Name()))
		if err := resource.Close(ctx); err != nil {
			errs = errors.Join(errs, fmt.Errorf("close resource %s: %w", resource.Name(), err))
		}
	}
	return errs
}

func (application *Application) registerNode(ctx context.Context) error {
	if application.registrar == nil {
		return nil
	}
	if err := application.registrar.Register(ctx, application.node); err != nil {
		return fmt.Errorf("register service node: %w", err)
	}
	application.registered = true
	application.getLogger().Info("service node registered", zap.String("node", application.node.ID))
	return nil
}

func (application *Application) unregisterNode(ctx context.Context) error {
	if !application.registered {
		return nil
	}
	application.registered = false
	if err := application.registrar.Unregister(ctx, application.node); err != nil {
		return fmt.Errorf("unregister service node %s: %w", application.node.ID, err)
	}
	application.getLogger().Info("service node unregistered", zap.String("node", application.node.ID))
	return nil
}

func (application *Application) startEndpoints(
	group *errgroup.Group,
	ctx context.Context,
	cancel context.CancelFunc,
) {
	for _, endpoint := range application.endpoints {
		application.activeEndpoints = append(application.activeEndpoints, endpoint)
		group.Go(func() error {
			if ctx.Err() != nil {
				return nil
			}
			application.getLogger().Info("endpoint starting", zap.String("endpoint", endpoint.Name()))
			err := endpoint.Startup(ctx)
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("endpoint %s exited unexpectedly: %w", endpoint.Name(), err)
			}
			return nil
		})
	}
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
			application.getLogger().Info("endpoint shutting down", zap.String("endpoint", endpoint.Name()))
			if err := endpoint.Shutdown(ctx); err != nil {
				mu.Lock()
				errs = errors.Join(errs, fmt.Errorf("stop endpoint %s: %w", endpoint.Name(), err))
				mu.Unlock()
			}
		}()
	}

	wait.Wait()
	return errs
}

func (application *Application) getLogger() *zap.Logger {
	if application.logger != nil {
		return application.logger
	}
	return log.GetLogger()
}
