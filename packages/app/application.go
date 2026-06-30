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

	"holographic/packages/config"
	"holographic/packages/log"
	"holographic/packages/registry"
	"holographic/packages/transport"
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

	// 注意：业务依赖（如 DB Manager, FileStore）建议直接注入到具体的 Server 构造函数中
	// 而不是作为 App 的字段。这里仅作示例，如果需要在 Option 中初始化它们，请确保它们有 Close 方法
	cleanups []cleanup // 用于存储需要在关闭时清理的资源
}

type cleanup struct {
	name string
	fn   func(ctx context.Context) error
}

// NewApplication 创建应用程序实例
func NewApplication(ctx context.Context, cfg *config.Application, options ...Option) (*Application, error) {

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

// Run 启动应用程序
func (app *Application) Run() error {
	appName := ""
	if app.cfg != nil {
		appName = app.cfg.Name
	}
	log.Info("Starting application...", zap.String("name", appName))

	if app.resource != nil {
		log.Info("application resource string: ", app.resource.String())
	}

	ctx, cancel := context.WithCancel(app.ctx)
	defer cancel()

	if err := app.registerEndpoints(ctx); err != nil {
		return err
	}

	group, groupCtx := errgroup.WithContext(ctx)
	stopCh := make(chan error, 1)
	var stopOnce sync.Once

	stop := func() {
		stopOnce.Do(func() {
			timeout, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			stopCh <- errors.Join(app.unregisterEndpoints(timeout), app.stopEndpoints(timeout))
		})
	}

	go func() {
		<-groupCtx.Done()
		stop()
	}()

	// 1. 运行所有的服务
	for _, endpoint := range app.endpoints {
		ep := endpoint
		group.Go(func() error {
			// 启动服务(阻塞式), 只要有一个服务失败，整个实例都需要停止
			log.Infof("==> Endpoint %v starting... ", ep.Name())
			if err := ep.Start(groupCtx); err != nil && !errors.Is(err, context.Canceled) {
				cancel()
				return fmt.Errorf("endpoint %s exited unexpectedly: %w", ep.Name(), err)
			}
			return nil
		})
	}

	// 2. 等待所有的服务结束
	runErr := group.Wait()
	cancel()
	stop()

	stopErr := <-stopCh
	cleanupErr := app.runCleanups(context.Background())

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

func (app *Application) stopEndpoints(ctx context.Context) error {
	var joined error
	for i := len(app.endpoints) - 1; i >= 0; i-- {
		endpoint := app.endpoints[i]
		log.Infof("--> Endpoint %v shutting down...", endpoint.Name())
		if err := endpoint.Stop(ctx); err != nil {
			joined = errors.Join(joined, fmt.Errorf("stop endpoint %s: %w", endpoint.Name(), err))
		}
	}
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
