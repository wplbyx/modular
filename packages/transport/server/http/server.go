package http

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/trace"

	"holographic/packages/config"
	"holographic/packages/log"
	"holographic/packages/transport"
)

var (
	_ transport.Endpoint   = (*Server)(nil)
	_ transport.Endpointer = (*Server)(nil)
)

// RegisterRouteFunc is the route registration function type
type RegisterRouteFunc func(engine *gin.Engine)

// Server wraps gin.Engine as a transport endpoint.
type Server struct {
	server      *http.Server
	engine      *gin.Engine
	enableTLS   bool
	tlsCertFile string
	tlsKeyFile  string
}

// NewServer creates a new HTTP server
func NewServer(cfg *config.HTTP, opts ...ServerOption) *Server {
	srv := &Server{}
	if cfg == nil {
		cfg = &config.HTTP{}
	}

	srv.engine = gin.Default()
	for _, option := range opts {
		option(srv)
	}

	srv.server = &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:           srv.engine,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
	srv.enableTLS = cfg.EnableTLS
	srv.tlsCertFile = cfg.TLSCertFile
	srv.tlsKeyFile = cfg.TLSKeyFile

	srv.engine.GET("/check", func(c *gin.Context) {
		span := trace.SpanFromContext(c.Request.Context())
		span.AddEvent("check handler start")
		c.String(http.StatusOK, "check success")
	})

	return srv
}

// Name returns the server name
func (s *Server) Name() string {
	return "HTTP Server"
}

// Endpoint returns the externally discoverable HTTP endpoint URL.
func (s *Server) Endpoint() (*url.URL, error) {
	if s == nil || s.server == nil {
		return nil, errors.New("http server is nil")
	}

	host, port, err := splitAdvertiseAddr(s.server.Addr)
	if err != nil {
		return nil, err
	}
	scheme := "http"
	if s.enableTLS {
		scheme = "https"
	}

	return &url.URL{
		Scheme: scheme,
		Host:   netJoinHostPort(host, port),
	}, nil
}

// Start starts the HTTP server
func (s *Server) Start(_ context.Context) error {
	log.Infof("HTTP server listening on %s", s.server.Addr)
	var err error
	if s.enableTLS {
		err = s.server.ListenAndServeTLS(s.tlsCertFile, s.tlsKeyFile)
	} else {
		err = s.server.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

// Stop stops the HTTP server
func (s *Server) Stop(ctx context.Context) error {
	if err := s.server.Shutdown(ctx); err != nil {
		return err
	}
	return nil
}

// RegisterMiddlewares registers middleware
func (s *Server) RegisterMiddlewares(middlewares ...gin.HandlerFunc) {
	s.engine.Use(middlewares...)
}

// RegisterRoute registers routes
func (s *Server) RegisterRoute(registers ...RegisterRouteFunc) {
	for _, register := range registers {
		register(s.engine)
	}
}

// Engine returns the underlying gin.Engine
func (s *Server) Engine() *gin.Engine {
	return s.engine
}

func splitAdvertiseAddr(addr string) (string, string, error) {
	host, port, err := netSplitHostPort(addr)
	if err != nil {
		return "", "", fmt.Errorf("parse server address %q: %w", addr, err)
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	if port == "" {
		return "", "", fmt.Errorf("server address %q has empty port", addr)
	}
	return host, port, nil
}

func netSplitHostPort(addr string) (string, string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err == nil {
		return host, port, nil
	}
	if strings.Count(addr, ":") == 1 && !strings.HasPrefix(addr, "[") {
		parts := strings.SplitN(addr, ":", 2)
		return parts[0], parts[1], nil
	}
	return "", "", err
}

func netJoinHostPort(host, port string) string {
	if _, err := strconv.Atoi(port); err == nil {
		return net.JoinHostPort(host, port)
	}
	return host + ":" + port
}
