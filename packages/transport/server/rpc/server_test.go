package rpc

import (
	"context"
	"errors"
	"net"
	"testing"
	"testing/fstest"

	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/errs"
	"github.com/wplbyx/modular/packages/log"
	modularmetadata "github.com/wplbyx/modular/packages/metadata"
	modulartransport "github.com/wplbyx/modular/packages/transport"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestServerName(t *testing.T) {
	server, err := NewServer(&configitem.GRPC{Host: "127.0.0.1", Port: 0}, nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	if got, want := server.Name(), "gRPC Server"; got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
}

func TestServerShutdownBeforeStart(t *testing.T) {
	server, err := NewServer(&configitem.GRPC{Host: "127.0.0.1", Port: 0}, nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestServerAddrExposesAllocatedPortBeforeStartup(t *testing.T) {
	server, err := NewServer(&configitem.GRPC{Host: "127.0.0.1", Port: 0}, nil)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	addr, ok := server.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("Addr() = %T, want *net.TCPAddr", server.Addr())
	}
	if addr.Port == 0 {
		t.Fatal("Addr().Port = 0, want allocated port")
	}

	transport := server.Transport()
	if transport.Protocol != "grpc" || transport.Address != "127.0.0.1" || transport.Port != addr.Port {
		t.Fatalf("Transport() = %+v, addr = %+v", transport, addr)
	}
}

func TestErrorUnaryInterceptorLocalizesStatus(t *testing.T) {
	handler := testRPCErrorHandler(t)
	interceptor := errorUnaryInterceptor(handler)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"accept-language", "zh-CN",
		"x-request-id", "req-42",
	))
	message := errs.Define("USER_NOT_FOUND", errs.Template("user %v not found", errs.Name("user_id"))).With("user_id", "42")
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/user.v1.User/Get"}, func(context.Context, any) (any, error) {
		return nil, errs.NotFound(message, errs.WithCause(errors.New("database secret")))
	})
	if err == nil {
		t.Fatal("interceptor returned nil error")
	}
	grpcStatus := status.Convert(err)
	if grpcStatus.Code() != codes.NotFound || grpcStatus.Message() != "用户 42 不存在" {
		t.Fatalf("status = %v", grpcStatus)
	}
	if len(grpcStatus.Details()) != 1 || grpcStatus.Details()[0].(*errdetails.ErrorInfo).Reason != "USER_NOT_FOUND" {
		t.Fatalf("details = %#v", grpcStatus.Details())
	}
}

func TestErrorUnaryInterceptorRecoversPanic(t *testing.T) {
	interceptor := errorUnaryInterceptor(testRPCErrorHandler(t))
	_, err := interceptor(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/panic"}, func(context.Context, any) (any, error) {
		panic("token=secret")
	})
	grpcStatus := status.Convert(err)
	if grpcStatus.Code() != codes.Internal || grpcStatus.Message() != "服务暂时不可用" {
		t.Fatalf("status = %v", grpcStatus)
	}
}

func TestErrorStreamInterceptorLocalizesStatus(t *testing.T) {
	interceptor := errorStreamInterceptor(testRPCErrorHandler(t))
	stream := &testServerStream{ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs("accept-language", "en-US"))}
	err := interceptor(nil, stream, &grpc.StreamServerInfo{FullMethod: "/user.v1.User/Watch"}, func(any, grpc.ServerStream) error {
		return errs.NotFound(errs.Define("USER_NOT_FOUND", errs.Template("user %v not found", errs.Name("user_id"))).With("user_id", "7"))
	})
	grpcStatus := status.Convert(err)
	if grpcStatus.Code() != codes.NotFound || grpcStatus.Message() != "User 7 was not found" {
		t.Fatalf("status = %v", grpcStatus)
	}
}

func TestMetadataUnaryInterceptorExtractsRequestContext(t *testing.T) {
	policy := modulartransport.NewPolicy(
		"test",
		modulartransport.WithTracing(false),
		modulartransport.WithAccessLog(false),
		modulartransport.WithProtection(nil),
	)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		modularmetadata.RequestIDKey, "req-42",
		"accept-language", "zh-CN,en;q=0.8",
	))
	_, err := metadataUnaryInterceptor(policy)(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/test/Get"}, func(ctx context.Context, _ any) (any, error) {
		md := modularmetadata.FromContext(ctx)
		if got := md.Get(modularmetadata.RequestIDKey); len(got) != 1 || got[0] != "req-42" {
			t.Fatalf("request ID = %#v", got)
		}
		if got := md.Get(modularmetadata.LanguageKey); len(got) != 1 || got[0] != "zh-CN" {
			t.Fatalf("language = %#v", got)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("interceptor error = %v", err)
	}
}

type testServerStream struct {
	ctx context.Context
}

func (stream *testServerStream) SetHeader(metadata.MD) error  { return nil }
func (stream *testServerStream) SendHeader(metadata.MD) error { return nil }
func (stream *testServerStream) SetTrailer(metadata.MD)       {}
func (stream *testServerStream) Context() context.Context     { return stream.ctx }
func (stream *testServerStream) SendMsg(any) error            { return nil }
func (stream *testServerStream) RecvMsg(any) error            { return nil }

func testRPCErrorHandler(t *testing.T) *errs.Handler {
	t.Helper()
	catalog, err := errs.LoadCatalog(fstest.MapFS{
		"locales/zh-CN.yaml": {Data: []byte("UNKNOWN: '请求失败'\nINTERNAL_ERROR: '服务暂时不可用'\nUSER_NOT_FOUND: '用户 {{.user_id}} 不存在'\n")},
		"locales/en-US.yaml": {Data: []byte("UNKNOWN: 'Request failed'\nINTERNAL_ERROR: 'Service unavailable'\nUSER_NOT_FOUND: 'User {{.user_id}} was not found'\n")},
	}, "locales", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := errs.NewHandler(catalog, log.Default())
	if err != nil {
		t.Fatal(err)
	}
	return handler
}
