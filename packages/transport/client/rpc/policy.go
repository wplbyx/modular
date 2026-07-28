package rpc

import (
	"context"
	"time"

	modulartransport "github.com/wplbyx/modular/packages/transport"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcmetadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func metadataUnaryClientInterceptor(policy *modulartransport.Policy) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, request, reply any, connection *grpc.ClientConn, invoke grpc.UnaryInvoker, options ...grpc.CallOption) error {
		requestCtx, err := injectGRPCContext(ctx, policy)
		if err != nil {
			return err
		}
		return invoke(requestCtx, method, request, reply, connection, options...)
	}
}

func metadataStreamClientInterceptor(policy *modulartransport.Policy) grpc.StreamClientInterceptor {
	return func(ctx context.Context, description *grpc.StreamDesc, connection *grpc.ClientConn, method string, streamer grpc.Streamer, options ...grpc.CallOption) (grpc.ClientStream, error) {
		requestCtx, err := injectGRPCContext(ctx, policy)
		if err != nil {
			return nil, err
		}
		return streamer(requestCtx, description, connection, method, options...)
	}
}

func accessUnaryClientInterceptor(policy *modulartransport.Policy) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, request, reply any, connection *grpc.ClientConn, invoke grpc.UnaryInvoker, options ...grpc.CallOption) error {
		started := time.Now()
		err := invoke(ctx, method, request, reply, connection, options...)
		fields := []zap.Field{
			zap.String("operation", method),
			zap.String("grpc_code", status.Code(err).String()),
			zap.Duration("duration", time.Since(started)),
		}
		if isGRPCClientFailure(err) {
			policy.Logger().Error(ctx, "gRPC client request completed", append(fields, zap.Error(err))...)
		} else {
			policy.Logger().Info(ctx, "gRPC client request completed", fields...)
		}
		return err
	}
}

func accessStreamClientInterceptor(policy *modulartransport.Policy) grpc.StreamClientInterceptor {
	return func(ctx context.Context, description *grpc.StreamDesc, connection *grpc.ClientConn, method string, streamer grpc.Streamer, options ...grpc.CallOption) (grpc.ClientStream, error) {
		started := time.Now()
		stream, err := streamer(ctx, description, connection, method, options...)
		fields := []zap.Field{
			zap.String("operation", method),
			zap.String("grpc_code", status.Code(err).String()),
			zap.Duration("duration", time.Since(started)),
		}
		if isGRPCClientFailure(err) {
			policy.Logger().Error(ctx, "gRPC client stream opened", append(fields, zap.Error(err))...)
		} else {
			policy.Logger().Info(ctx, "gRPC client stream opened", fields...)
		}
		return stream, err
	}
}

func protectionUnaryClientInterceptor(policy *modulartransport.Policy) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, request, reply any, connection *grpc.ClientConn, invoke grpc.UnaryInvoker, options ...grpc.CallOption) error {
		done, err := policy.Protection().Allow(ctx, method)
		if err != nil {
			return err
		}
		err = invoke(ctx, method, request, reply, connection, options...)
		if isGRPCClientFailure(err) {
			done(err)
		} else {
			done(nil)
		}
		return err
	}
}

func protectionStreamClientInterceptor(policy *modulartransport.Policy) grpc.StreamClientInterceptor {
	return func(ctx context.Context, description *grpc.StreamDesc, connection *grpc.ClientConn, method string, streamer grpc.Streamer, options ...grpc.CallOption) (grpc.ClientStream, error) {
		done, err := policy.Protection().Allow(ctx, method)
		if err != nil {
			return nil, err
		}
		stream, err := streamer(ctx, description, connection, method, options...)
		if isGRPCClientFailure(err) {
			done(err)
		} else {
			done(nil)
		}
		return stream, err
	}
}

func injectGRPCContext(ctx context.Context, policy *modulartransport.Policy) (context.Context, error) {
	outgoing, _ := grpcmetadata.FromOutgoingContext(ctx)
	outgoing = outgoing.Copy()
	if outgoing == nil {
		outgoing = grpcmetadata.MD{}
	}
	if err := policy.Propagator().Inject(ctx, modulartransport.GRPCMetadataCarrier{Metadata: outgoing}); err != nil {
		return ctx, err
	}
	return grpcmetadata.NewOutgoingContext(ctx, outgoing), nil
}

func isGRPCClientFailure(err error) bool {
	if err == nil {
		return false
	}
	switch status.Code(err) {
	case codes.Unknown, codes.DeadlineExceeded, codes.Internal, codes.Unavailable, codes.DataLoss:
		return true
	default:
		return false
	}
}
