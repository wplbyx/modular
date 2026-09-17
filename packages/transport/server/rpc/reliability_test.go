package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/resilience"
	"github.com/wplbyx/modular/packages/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type resultProtection struct{ results chan error }

func (p *resultProtection) Allow(context.Context, string) (resilience.DoneFunc, error) {
	return func(err error) { p.results <- err }, nil
}
func TestServer_PanicCompletesUnaryAndStreamProtection(t *testing.T) {
	p := &resultProtection{make(chan error, 4)}
	s, err := NewServer(&configitem.GRPC{Host: "127.0.0.1"}, nil, WithPolicy(transport.NewPolicy("test", transport.WithProtection(p))), WithUnaryInterceptors(func(context.Context, any, *grpc.UnaryServerInfo, grpc.UnaryHandler) (any, error) { panic("unary") }), WithStreamInterceptors(func(any, grpc.ServerStream, *grpc.StreamServerInfo, grpc.StreamHandler) error { panic("stream") }))
	require.NoError(t, err)
	defer s.Shutdown(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { _ = s.Startup(ctx) }()
	require.NoError(t, s.Ready(ctx))
	conn, err := grpc.NewClient(s.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()
	client := grpc_health_v1.NewHealthClient(conn)
	_, err = client.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.Equal(t, codes.Internal, status.Code(err))
	stream, err := client.Watch(ctx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Equal(t, codes.Internal, status.Code(err))
	for range 2 {
		select {
		case err := <-p.results:
			require.Error(t, err)
		case <-ctx.Done():
			t.Fatal("protection completion missing")
		}
	}
	select {
	case <-p.results:
		t.Fatal("duplicate completion")
	default:
	}
}
