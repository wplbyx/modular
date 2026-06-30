package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"go.opentelemetry.io/otel/sdk/resource"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"modular/packages/config"
	"modular/packages/log"
	"modular/packages/registry"
	"modular/packages/transport"
)

// Option 初始化应用时的函数式选项
type Option func(*Application)

// Application 应用程序生命周期管理器
type Application struct {
	ctx      context.Context
	cfg      *config.Application
	resource *resource.Resource

	// 基础设施
	registrar registry.Registrar // 服务注册与发现
	// endpoints are application-managed transport entrypoints.
	endpoints []transport.Endpoint

	registeredServices []*registry.ServiceNode

	// 优雅关闭超时（Option 优先于 config.Application.ShutdownTimeout，零值回退默认 10s）
	shutdownTimeout time.Duration

	// 注意：业务依赖（如 DB Manager, FileStore）建议直接注入到具体的 Server 构造函数中
	// 而不是作为 App 的字段。这里仅作示例，如果需要在 Option 中初始化它们，请确保它们有 Close 方法
	cleanups []cleanup // 用于存储需要在关闭时清理的资源
}

type cleanup struct {
	name string
	fn   func(ctx context.Context) error
}

const defaultShutdownTimeout = 10 * time.Second

// NewApplication 创建应用程序实例
func NewApplication(ctx context.Context, cfg *config.Application, options ...Option) (*Application, error) {
	if cfg == nil {
		return nil, errors.New("config.Application instance is nil")
	}

	app := &Application{
		ctx:       ctx,
		cfg:       cfg, // &customConfig.Application
		endpoints: make([]transport.Endpoint, 0),
		cleanups:  make([]cleanup, 0),
	}

	// 3. 应用用户自定义选项 (例如注入 DB, Server 实例等)
	for _, option := range options {
		option(app)
	}

	return app, nil
}

// --- 常用的 Option 函数 ---

func WithResource(res *resource.Resource) Option {
	return func(app *Application) {
		app.resource = res
	}
}

// WithRegistrar 注入服务注册中心
func WithRegistrar(reg registry.Registrar) Option {
	return func(a *Application) {
		a.registrar = reg
	}
}

// WithEndpoint injects an application-managed transport entrypoint.
func WithEndpoint(endpoint transport.Endpoint) Option {
	return func(a *Application) {
		if endpoint != nil {
			a.endpoints = append(a.endpoints, endpoint)
		}
	}
}

// WithCleanup registers a resource cleanup to run after endpoints stop.
func WithCleanup(name string, fn func(ctx context.Context) error) Option {
	return func(a *Application) {
		if fn != nil {
			a.cleanups = append(a.cleanups, cleanup{name: name, fn: fn})
		}
	}
}

// WithShutdownTimeout sets the graceful shutdown budget used when stopping
// endpoints and running cleanups. Zero falls back to the config value or the
// default. This option takes precedence over config.Application.ShutdownTimeout.
func WithShutdownTimeout(d time.Duration) Option {
	return func(a *Application) {
		a.shutdownTimeout = d
	}
}

// shutdownTimeoutDuration returns the effective shutdown budget: explicit
// Option value first, then config, then the default.
func (app *Application) shutdownTimeoutDuration() time.Duration {
	if app.shutdownTimeout > 0 {
		return app.shutdownTimeout
	}
	if app.cfg != nil && app.cfg.ShutdownTimeout > 0 {
		return app.cfg.ShutdownTimeout
	}
	return defaultShutdownTimeout
}

// Run 启动应用程序
func (app *Application) Run() error {
	if app.resource == nil {
		return errors.New("application.resource is nil")
	}

	log.Info("Run starting application...", zap.String("name", app.cfg.Name))
	log.Info("application resource string: ", app.resource.String())

	ctx, cancel := context.WithCancel(app.ctx)
	defer cancel()

	if err := app.registerEndpoints(ctx); err != nil {
		return err
	}

	group, groupCtx := errgroup.WithContext(ctx)
	stopCh := make(chan error, 1)
	var stopOnce sync.Once

	// stop 执行反注册与停止所有 endpoint，所有 endpoint 共享同一个关闭超时预算。
	// 真实 endpoint 的 Start 通常阻塞且不响应 context（如 http.Server.Serve），
	// 因此需要外部触发 Stop 来解除阻塞，否则 group.Wait() 永远不会返回。
	stop := func() {
		stopOnce.Do(func() {
			timeout, cancelTimeout := context.WithTimeout(context.Background(), app.shutdownTimeoutDuration())
			defer cancelTimeout()
			stopCh <- errors.Join(app.unregisterEndpoints(timeout), app.stopEndpoints(timeout))
		})
	}

	go func() {
		<-groupCtx.Done()
		stop()
	}()

	// 1. 运行所有的服务（并行启动，阻塞式）
	// 任何 endpoint 的 Start 返回（nil 或 error）都视为退出信号，触发整体关闭。
	for _, endpoint := range app.endpoints {
		group.Go(func() error {
			log.Infof("==> Endpoint %v starting... ", endpoint.Name())
			err := endpoint.Start(groupCtx)
			// 无论返回 nil 还是 error，都必须取消 groupCtx：Start 返回意味着该
			// endpoint 已退出（可能静默死亡），不能让其他 endpoint 继续无感知地运行。
			cancel()
			if err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("endpoint %s exited unexpectedly: %w", endpoint.Name(), err)
			}
			return nil
		})
	}

	// 2. 等待所有的服务结束
	runErr := group.Wait()
	cancel()
	stop()

	stopErr := <-stopCh

	// cleanup 阶段使用独立的关闭预算，避免被 stop 阶段挤占。
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), app.shutdownTimeoutDuration())
	defer cleanupCancel()
	cleanupErr := app.runCleanups(cleanupCtx)

	if runErr != nil {
		log.Error("Application stopped with error", zap.Error(runErr))
	}
	if stopErr != nil {
		log.Error("Application endpoint shutdown error", zap.Error(stopErr))
	}
	if cleanupErr != nil {
		log.Error("Application cleanup error", zap.Error(cleanupErr))
	}

	log.Info("Application exited.")
	return errors.Join(runErr, stopErr, cleanupErr)
}

// Close stops endpoints and runs resource cleanups.
func (app *Application) Close(ctx context.Context) error {
	return errors.Join(app.unregisterEndpoints(ctx), app.stopEndpoints(ctx), app.runCleanups(ctx))
}

func (app *Application) registerEndpoints(ctx context.Context) error {
	if app.registrar == nil {
		return nil
	}

	for _, endpoint := range app.endpoints {
		endpointer, ok := endpoint.(transport.Endpointer)
		if !ok {
			continue
		}

		node, err := app.serviceNodeForEndpoint(endpoint, endpointer)
		if err != nil {
			_ = app.unregisterEndpoints(context.Background())
			return err
		}
		if err := app.registrar.Register(ctx, node); err != nil {
			_ = app.unregisterEndpoints(context.Background())
			return fmt.Errorf("register endpoint %s: %w", endpoint.Name(), err)
		}
		app.registeredServices = append(app.registeredServices, node)
	}

	return nil
}

func (app *Application) unregisterEndpoints(ctx context.Context) error {
	if app.registrar == nil || len(app.registeredServices) == 0 {
		return nil
	}

	var joined error
	for i := len(app.registeredServices) - 1; i >= 0; i-- {
		node := app.registeredServices[i]
		if err := app.registrar.Unregister(ctx, node); err != nil {
			joined = errors.Join(joined, fmt.Errorf("unregister endpoint %s: %w", node.ID, err))
		}
	}
	app.registeredServices = nil
	return joined
}

func (app *Application) serviceNodeForEndpoint(endpoint transport.Endpoint, endpointer transport.Endpointer) (*registry.ServiceNode, error) {
	u, err := endpointer.Endpoint()
	if err != nil {
		return nil, fmt.Errorf("endpoint %s url: %w", endpoint.Name(), err)
	}
	if u == nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("endpoint %s url is invalid: %v", endpoint.Name(), u)
	}

	appName := "application"
	appVersion := ""
	if app.cfg != nil {
		if app.cfg.Name != "" {
			appName = app.cfg.Name
		}
		appVersion = app.cfg.Version
	}

	host := u.Hostname()
	portText := u.Port()
	port, _ := strconv.Atoi(portText)

	metadata := map[string]string{
		"protocol":      u.Scheme,
		"endpoint_name": endpoint.Name(),
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		metadata["health_path"] = "/check"
	}

	return &registry.ServiceNode{
		ID:        serviceNodeID(appName, endpoint.Name(), host, portText),
		Name:      appName,
		Version:   appVersion,
		Address:   host,
		Port:      port,
		Metadata:  metadata,
		Endpoints: []string{canonicalEndpointURL(u)},
	}, nil
}

func serviceNodeID(parts ...string) string {
	joined := strings.Join(parts, "-")
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(joined) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func canonicalEndpointURL(u *url.URL) string {
	copied := *u
	copied.RawQuery = ""
	copied.Fragment = ""
	return copied.String()
}

// stopEndpoints 并行停止所有 endpoint，共享同一个 ctx 超时预算。
// 使用 WaitGroup + mutex 而非 errgroup，确保每个 endpoint 的 Stop 都有机会执行完成，
// 而不是遇到第一个错误就取消其余。
func (app *Application) stopEndpoints(ctx context.Context) error {
	var (
		mu     sync.Mutex
		joined error
		wg     sync.WaitGroup
	)

	for _, endpoint := range app.endpoints {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Infof("--> Endpoint %v shutting down...", endpoint.Name())
			if err := endpoint.Stop(ctx); err != nil {
				mu.Lock()
				joined = errors.Join(joined, fmt.Errorf("stop endpoint %s: %w", endpoint.Name(), err))
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	return joined
}

func (app *Application) runCleanups(ctx context.Context) error {
	var joined error
	for i := len(app.cleanups) - 1; i >= 0; i-- {
		cleanup := app.cleanups[i]
		name := cleanup.name
		if name == "" {
			name = "cleanup"
		}
		if err := cleanup.fn(ctx); err != nil {
			joined = errors.Join(joined, fmt.Errorf("%s: %w", name, err))
		}
	}
	return joined
}
