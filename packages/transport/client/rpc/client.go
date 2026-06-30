package rpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// ClientConfigOption is a function that configures client options
type ClientConfigOption func(*ClientConfig) error

// ClientConfig contains gRPC client configuration
type ClientConfig struct {
	endpoint      string
	timeout       time.Duration
	credentials   credentials.TransportCredentials
	unaryInts     []grpc.UnaryClientInterceptor
	streamInts    []grpc.StreamClientInterceptor
	rpcOpts       []grpc.DialOption
	balancerName  string
	serverName    string
	enableTracing bool
	enableMetrics bool
}

// UseClient executes a callback with a gRPC connection
func UseClient(callback func(*grpc.ClientConn) error, options ...ClientConfigOption) error {
	connection, err := GetClientConnection(context.Background(), options...)
	if err != nil {
		return errors.Join(err, errors.New("failed to initiate gRPC client connection"))
	}
	defer connection.Close()

	return callback(connection)
}

// GetClientConnection gets a gRPC client connection
func GetClientConnection(ctx context.Context, options ...ClientConfigOption) (*grpc.ClientConn, error) {
	config := defaultClientConfig()
	for _, option := range options {
		if err := option(config); err != nil {
			return nil, err
		}
	}
	if config.endpoint == "" {
		return nil, errors.New("grpc endpoint is empty")
	}

	grpcOpts := []grpc.DialOption{
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy": "` + config.balancerName + `"}`),
	}
	if len(config.unaryInts) > 0 {
		grpcOpts = append(grpcOpts, grpc.WithChainUnaryInterceptor(config.unaryInts...))
	}
	if len(config.streamInts) > 0 {
		grpcOpts = append(grpcOpts, grpc.WithChainStreamInterceptor(config.streamInts...))
	}
	grpcOpts = append(grpcOpts, config.rpcOpts...)

	// Configure TLS
	if config.credentials == nil {
		grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(config.credentials))
	}

	if config.serverName != "" {
		grpcOpts = append(grpcOpts, grpc.WithAuthority(config.serverName))
	}

	conn, err := grpc.NewClient(config.endpoint, grpcOpts...)
	if err != nil {
		return nil, fmt.Errorf("create grpc client %s: %w", config.endpoint, err)
	}
	if config.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, config.timeout)
		defer cancel()
	}
	if err := waitUntilReady(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func defaultClientConfig() *ClientConfig {
	return &ClientConfig{
		timeout:       2000 * time.Millisecond,
		balancerName:  "round_robin",
		enableTracing: true,
	}
}

func waitUntilReady(ctx context.Context, conn *grpc.ClientConn) error {
	conn.Connect()
	for {
		state := conn.GetState()
		switch state {
		case connectivity.Ready:
			return nil
		case connectivity.Shutdown:
			return errors.New("grpc connection is shutdown")
		}
		if !conn.WaitForStateChange(ctx, state) {
			return ctx.Err()
		}
	}
}

// WithEnableTracing enables or disables tracing
func WithEnableTracing(enable bool) ClientConfigOption {
	return func(o *ClientConfig) error {
		o.enableTracing = enable
		return nil
	}
}

// WithClientMetrics enables or disables metrics
func WithClientMetrics(enable bool) ClientConfigOption {
	return func(o *ClientConfig) error {
		o.enableMetrics = enable
		return nil
	}
}

// WithServerName sets the server name
func WithServerName(serverName string) ClientConfigOption {
	return func(o *ClientConfig) error {
		o.serverName = serverName
		return nil
	}
}

// WithEndpoint sets the endpoint
func WithEndpoint(endpoint string) ClientConfigOption {
	return func(o *ClientConfig) error {
		o.endpoint = endpoint
		return nil
	}
}

// WithClientTimeout sets the timeout
func WithClientTimeout(timeout time.Duration) ClientConfigOption {
	return func(o *ClientConfig) error {
		o.timeout = timeout
		return nil
	}
}

// WithClientUnaryInterceptor sets unary interceptors
func WithClientUnaryInterceptor(in ...grpc.UnaryClientInterceptor) ClientConfigOption {
	return func(o *ClientConfig) error {
		o.unaryInts = in
		return nil
	}
}

// WithClientStreamInterceptor sets stream interceptors
func WithClientStreamInterceptor(in ...grpc.StreamClientInterceptor) ClientConfigOption {
	return func(o *ClientConfig) error {
		o.streamInts = in
		return nil
	}
}

// WithClientOptions sets gRPC dial options
func WithClientOptions(opts ...grpc.DialOption) ClientConfigOption {
	return func(o *ClientConfig) error {
		o.rpcOpts = opts
		return nil
	}
}

// WithBalancerName sets the balancer name
func WithBalancerName(name string) ClientConfigOption {
	return func(o *ClientConfig) error {
		o.balancerName = name
		return nil
	}
}
