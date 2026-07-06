package rpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"modular/packages/config"
	"modular/packages/core"
	"modular/packages/log"
)

var _ core.Endpoint = (*Server)(nil)

// Server is a gRPC server implementation
type Server struct {
	grpcServer  *grpc.Server
	listener    net.Listener
	health      *health.Server
	config      *config.GRPC
	credentials credentials.TransportCredentials
	unaryInts   []grpc.UnaryServerInterceptor
	streamInts  []grpc.StreamServerInterceptor
	mu          sync.RWMutex
	isRunning   bool
}

// Option defines gRPC server configuration options
type Option func(*Server) error

// RegisterFunc is the service registration function type
type RegisterFunc func(grpc.ServiceRegistrar) error

// NewServer creates a new gRPC server instance
func NewServer(cfg *config.GRPC, register RegisterFunc, opts ...Option) (*Server, error) {
	if cfg == nil {
		cfg = &config.GRPC{}
	}

	healthServer := health.NewServer()

	s := &Server{
		health:    healthServer,
		config:    cfg,
		isRunning: false,
	}

	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	serverOpts := []grpc.ServerOption{}

	if s.credentials != nil {
		serverOpts = append(serverOpts, grpc.Creds(s.credentials))
	}
	if len(s.unaryInts) > 0 {
		serverOpts = append(serverOpts, grpc.ChainUnaryInterceptor(s.unaryInts...))
	}
	if len(s.streamInts) > 0 {
		serverOpts = append(serverOpts, grpc.ChainStreamInterceptor(s.streamInts...))
	}

	s.grpcServer = grpc.NewServer(serverOpts...)

	if register != nil {
		if err := register(s.grpcServer); err != nil {
			return nil, err
		}
	}

	grpc_health_v1.RegisterHealthServer(s.grpcServer, s.health)
	s.health.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	return s, nil
}

// WithUnaryInterceptors adds unary interceptors
func WithUnaryInterceptors(interceptors ...grpc.UnaryServerInterceptor) Option {
	return func(s *Server) error {
		s.unaryInts = append(s.unaryInts, interceptors...)
		return nil
	}
}

// WithStreamInterceptors adds stream interceptors
func WithStreamInterceptors(interceptors ...grpc.StreamServerInterceptor) Option {
	return func(s *Server) error {
		s.streamInts = append(s.streamInts, interceptors...)
		return nil
	}
}

// WithMTLS configures mutual TLS
func WithMTLS(serverCertFile, serverKeyFile, clientCAFile string) Option {
	return func(s *Server) error {
		serverCert, err := tls.LoadX509KeyPair(serverCertFile, serverKeyFile)
		if err != nil {
			return fmt.Errorf("failed to load server certificate: %w", err)
		}

		clientCA, err := os.ReadFile(clientCAFile)
		if err != nil {
			return fmt.Errorf("failed to read client CA certificate: %w", err)
		}
		clientCAPool := x509.NewCertPool()
		if !clientCAPool.AppendCertsFromPEM(clientCA) {
			return errors.New("failed to parse client CA certificate")
		}

		tlsConfig := &tls.Config{
			Certificates: []tls.Certificate{serverCert},
			ClientAuth:   tls.RequireAndVerifyClientCert,
			ClientCAs:    clientCAPool,
			MinVersion:   tls.VersionTLS12,
		}

		s.credentials = credentials.NewTLS(tlsConfig)
		return nil
	}
}

// Name returns the component name for logging.
func (s *Server) Name() string {
	return "gRPC Server"
}

// Startup starts the gRPC server
func (s *Server) Startup(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		return errors.New("gRPC server is already running")
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.config.Host, s.config.Port))
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("failed to create listener: %w", err)
	}
	s.listener = listener
	s.isRunning = true
	s.mu.Unlock()

	log.Infof("gRPC server listening on %s", listener.Addr())

	err = s.grpcServer.Serve(listener)

	s.mu.Lock()
	s.isRunning = false
	s.mu.Unlock()

	if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("gRPC server error: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the gRPC server
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.RLock()
	if !s.isRunning {
		s.mu.RUnlock()
		return nil
	}
	s.mu.RUnlock()

	log.Infof("Gracefully stopping gRPC server...")

	s.health.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

	timeout := 5 * time.Second
	if s.config.ShutdownTimeout > 0 {
		timeout = s.config.ShutdownTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shutdownComplete := make(chan struct{})
	go func() {
		s.grpcServer.GracefulStop()
		close(shutdownComplete)
	}()

	select {
	case <-ctx.Done():
		log.Warnf("gRPC server shutdown timeout, forcing stop: %v", ctx.Err())
		s.grpcServer.Stop()
	case <-shutdownComplete:
		log.Infof("gRPC server gracefully stopped")
	}

	return nil
}

// SetHealthStatus sets the health status for a service
func (s *Server) SetHealthStatus(service string, status grpc_health_v1.HealthCheckResponse_ServingStatus) {
	s.health.SetServingStatus(service, status)
}

// IsRunning checks if the server is running
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRunning
}
