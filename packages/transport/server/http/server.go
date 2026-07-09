package http

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/wplbyx/modular/packages/config"
	"github.com/wplbyx/modular/packages/core"
	"github.com/wplbyx/modular/packages/log"
)

var _ core.Endpoint = (*Server)(nil)

// 默认超时与优雅关闭时长，在配置缺失（值为 0）时兜底使用。
const (
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 30 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 120 * time.Second
	defaultShutdownTimeout   = 5 * time.Second

	// DefaultHealthPath 是默认的健康检查路径。
	DefaultHealthPath = "/health"
)

// RegisterRouteFunc 路由注册函数类型
type RegisterRouteFunc func(engine *gin.Engine)

// Server 将 gin.Engine 封装为 core.Endpoint。
//
// 通过 NewServer 创建时会立即占用监听端口（构造即监听），
// 因此即便配置 Port=0，构造完成后即可获得真实端口。
type Server struct {
	server   *http.Server
	engine   *gin.Engine
	listener net.Listener
	cfg      *config.HTTP

	enableTLS   bool
	tlsCertFile string
	tlsKeyFile  string

	// 运行态
	mu        sync.RWMutex
	isRunning bool

	// 由 option 写入、在 NewServer 各阶段消费的配置字段
	mode           string
	logger         zapLogger
	middlewares    []gin.HandlerFunc
	h2c            bool
	baseContextFn  func(net.Listener) context.Context
	healthPath     string
	healthHandler  gin.HandlerFunc
	healthDisabled bool
}

// NewServer 创建并预监听一个 HTTP 服务。
func NewServer(cfg *config.HTTP, opts ...ServerOption) (*Server, error) {
	if cfg == nil {
		cfg = &config.HTTP{}
	}

	srv := &Server{cfg: cfg}

	// 1. 先应用 option（仅写入字段，不触碰 engine）
	for _, opt := range opts {
		opt(srv)
	}

	// 2. gin 模式必须在 gin.New() 之前生效
	if srv.mode != "" {
		gin.SetMode(srv.mode)
	}

	// 3. 创建引擎：注册 Recovery 防止 panic；若注入了 logger 则改用 zap 版本
	srv.engine = gin.New()
	if srv.logger != nil {
		srv.engine.Use(ginLogger(srv.logger), ginRecovery(srv.logger))
	} else {
		srv.engine.Use(gin.Recovery())
	}
	for _, m := range srv.middlewares {
		srv.engine.Use(m)
	}

	// 4. 解析监听地址（IPv6 安全拼接）
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	// 5. 构造即监听：Port=0 时立即获得操作系统分配的真实端口
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("http listen %s: %w", addr, err)
	}
	srv.listener = lis

	// 6. TLS 校验（配置启用时）
	if cfg.EnableTLS {
		if cfg.TLSCertFile == "" || cfg.TLSKeyFile == "" {
			_ = lis.Close()
			return nil, errors.New("enableTLS 要求同时配置 TLSCertFile 和 TLSKeyFile")
		}
		if _, err := os.Stat(cfg.TLSCertFile); err != nil {
			_ = lis.Close()
			return nil, fmt.Errorf("TLS 证书不可读: %w", err)
		}
		if _, err := os.Stat(cfg.TLSKeyFile); err != nil {
			_ = lis.Close()
			return nil, fmt.Errorf("TLS 私钥不可读: %w", err)
		}
		srv.enableTLS = true
		srv.tlsCertFile = cfg.TLSCertFile
		srv.tlsKeyFile = cfg.TLSKeyFile
	}

	// 7. 组装 http.Server；可选 h2c 明文 HTTP/2
	handler := http.Handler(srv.engine)
	if srv.h2c {
		handler = h2c.NewHandler(srv.engine, &http2.Server{})
	}
	srv.server = &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: orDuration(cfg.ReadHeaderTimeout, defaultReadHeaderTimeout),
		ReadTimeout:       orDuration(cfg.ReadTimeout, defaultReadTimeout),
		WriteTimeout:      orDuration(cfg.WriteTimeout, defaultWriteTimeout),
		IdleTimeout:       orDuration(cfg.IdleTimeout, defaultIdleTimeout),
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	if srv.baseContextFn != nil {
		srv.server.BaseContext = srv.baseContextFn
	}

	// 8. 默认健康检查路由
	srv.registerHealth()

	return srv, nil
}

// Name 返回组件名，用于日志区分。
func (s *Server) Name() string {
	return "HTTP Server"
}

// Startup 阻塞式启动 HTTP 服务，直至 Shutdown 被调用或发生错误。
func (s *Server) Startup(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		return errors.New("http server is already running")
	}
	s.isRunning = true
	s.mu.Unlock()

	log.Infof("HTTP server listening on %s", s.server.Addr)

	var err error
	if s.enableTLS {
		err = s.server.ServeTLS(s.listener, s.tlsCertFile, s.tlsKeyFile)
	} else {
		err = s.server.Serve(s.listener)
	}

	s.mu.Lock()
	s.isRunning = false
	s.mu.Unlock()

	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown 优雅关闭服务。
func (s *Server) Shutdown(ctx context.Context) error {
	timeout := defaultShutdownTimeout
	if s.cfg != nil && s.cfg.ShutdownTimeout > 0 {
		timeout = s.cfg.ShutdownTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return s.server.Shutdown(ctx)
}

// RegisterRoute 注册业务路由
func (s *Server) RegisterRoute(registers ...RegisterRouteFunc) {
	for _, register := range registers {
		register(s.engine)
	}
}

// Engine 返回底层 gin.Engine
func (s *Server) Engine() *gin.Engine {
	return s.engine
}

// Server 返回底层 *http.Server，便于高级配置。
func (s *Server) Server() *http.Server {
	return s.server
}

// IsRunning 返回服务是否处于运行态
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRunning
}

func orDuration(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}
