package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"modular/packages/config"
	"modular/packages/core"
	"modular/packages/log"
	"modular/packages/registry"
)

const defaultShutdownTimeout = 10 * time.Second

// Option 初始化应用时的函数式选项
type Option func(*Application)

// Application 应用程序生命周期管理器，只负责编排，不处理具体逻辑。
//
// 生命周期顺序：
//
//	Resource.Setup()  (FIFO)
//	  → register ServiceNode
//	  → Endpoint.Startup()  (并行阻塞)
//	  → 运行中... 等待退出 ...
//	  → Endpoint.Shutdown()  (并行)
//	  → unregister ServiceNode
//	  → Resource.Close()  (LIFO)
type Application struct {
	ctx context.Context
	cfg *config.Application

	// 服务节点元数据（一个 app 对应一个 node）
	node *core.ServiceNode

	// 基础设施
	registrar      registry.Registrar
	endpoints      []core.Endpoint
	resources      []core.Resource
	readyResources []core.Resource
	shutdownOnce   sync.Once
	shutdownErr    error

	shutdownTimeout time.Duration
}

// NewApplication 创建应用程序实例
func NewApplication(ctx context.Context, cfg *config.Application, options ...Option) (*Application, error) {
	if cfg == nil {
		return nil, errors.New("config.Application instance is nil")
	}

	app := &Application{
		ctx:             ctx,
		cfg:             cfg,
		endpoints:       make([]core.Endpoint, 0),
		resources:       make([]core.Resource, 0),
		readyResources:  make([]core.Resource, 0),
		shutdownTimeout: defaultShutdownTimeout,
	}

	if cfg.ShutdownTimeout > 0 {
		app.shutdownTimeout = cfg.ShutdownTimeout
	}

	for _, option := range options {
		option(app)
	}

	return app, nil
}

// Run 启动应用程序
func (app *Application) Run() error {
	if len(app.endpoints) == 0 {
		log.GetLogger().Warn("application has no endpoints; Run will exit after cleanups")
		return nil
	}

	log.GetLogger().Info("Run starting application... ", zap.String("name", app.cfg.Name))

	// shutdown 在同一个超时预算内完成全部关闭步骤。
	// goroutine 在 groupCtx 取消时触发 shutdown，解除其余 endpoint 的阻塞；
	// group.Wait() 返回后也调一次，Application 内部的 shutdownOnce 保证只执行一次。
	triggerShutdown := func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), app.shutdownTimeout)
		defer shutdownCancel()
		app.Close(shutdownCtx)
	}

	ctx, cancel := context.WithCancel(app.ctx)
	defer cancel()

	// 1. 初始化基础设施资源（FIFO），任一失败 → 关闭已初始化的资源后返回
	if err := app.setupResources(ctx); err != nil {
		triggerShutdown()
		return errors.Join(err, app.shutdownErr)
	}

	// 2. 注册服务节点（纯传值，app 不关心注册细节）
	if err := app.registerNode(ctx); err != nil {
		triggerShutdown()
		return errors.Join(err, app.shutdownErr)
	}

	// 3. 启动运行所有服务
	group, groupCtx := errgroup.WithContext(ctx)

	go func() {
		<-groupCtx.Done()
		triggerShutdown()
	}()

	for _, endpoint := range app.endpoints {
		group.Go(func() error {
			log.Infof("==> Endpoint %v starting... ", endpoint.Name())
			err := endpoint.Startup(groupCtx)
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("endpoint %s exited unexpectedly: %w", endpoint.Name(), err)
			}
			return nil
		})
	}

	runErr := group.Wait()
	if runErr != nil {
		log.GetLogger().Error("Application stopped with error", zap.Error(runErr))
	}

	triggerShutdown()
	if app.shutdownErr != nil {
		log.GetLogger().Error("Application shutdown error", zap.Error(app.shutdownErr))
	}

	log.GetLogger().Info("Application exited.")
	return errors.Join(runErr, app.shutdownErr)
}

// Close 手动触发全部关闭步骤。
func (app *Application) Close(ctx context.Context) error {
	app.shutdownOnce.Do(func() {
		app.shutdownErr = app.shutdown(ctx)
	})
	return app.shutdownErr
}

// shutdown 在同一个超时预算内按顺序执行全部关闭步骤：
// Endpoint.Shutdown [并行] → unregister Node → Resource.Close [LIFO]
func (app *Application) shutdown(ctx context.Context) error {
	var errs error
	if err := app.shutdownEndpoints(ctx); err != nil {
		errs = errors.Join(errs, err)
	}
	if err := app.unregisterNode(ctx); err != nil {
		errs = errors.Join(errs, err)
	}
	if err := app.closeResources(ctx); err != nil {
		errs = errors.Join(errs, err)
	}
	return errs
}

// --- Resource 生命周期 ---

func (app *Application) setupResources(ctx context.Context) error {
	for _, r := range app.resources {
		log.Infof("==> Resource %v initializing...", r.Name())
		if err := r.Setup(ctx); err != nil {
			return fmt.Errorf("init resource %s: %w", r.Name(), err)
		}
		app.readyResources = append(app.readyResources, r)
	}
	return nil
}

func (app *Application) closeResources(ctx context.Context) error {
	var errs error
	for i := len(app.readyResources) - 1; i >= 0; i-- {
		r := app.readyResources[i]
		log.Infof("--> Resource %v closing...", r.Name())
		if err := r.Close(ctx); err != nil {
			errs = errors.Join(errs, fmt.Errorf("close resource %s: %w", r.Name(), err))
		}
	}
	return errs
}

// --- ServiceNode 注册与反注册（纯传值） ---

func (app *Application) registerNode(ctx context.Context) error {
	if app.registrar == nil || app.node == nil {
		return nil
	}
	if err := app.registrar.Register(ctx, app.node); err != nil {
		return fmt.Errorf("register service node: %w", err)
	}
	log.Infof("==> ServiceNode %v registered", app.node.ID)
	return nil
}

func (app *Application) unregisterNode(ctx context.Context) error {
	if app.registrar == nil || app.node == nil {
		return nil
	}
	if err := app.registrar.Unregister(ctx, app.node); err != nil {
		return fmt.Errorf("unregister service node %s: %w", app.node.ID, err)
	}
	log.Infof("--> ServiceNode %v unregistered", app.node.ID)
	return nil
}

// --- Endpoint 停止 ---

func (app *Application) shutdownEndpoints(ctx context.Context) error {
	var (
		errs error
		mu   sync.Mutex
		wg   sync.WaitGroup
	)

	for _, endpoint := range app.endpoints {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Infof("--> Endpoint %v shutting down...", endpoint.Name())
			if err := endpoint.Shutdown(ctx); err != nil {
				mu.Lock()
				errs = errors.Join(errs, fmt.Errorf("stop endpoint %s: %w", endpoint.Name(), err))
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	return errs
}
