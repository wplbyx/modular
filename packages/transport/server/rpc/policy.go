package rpc

import (
	"context"
	"runtime/debug"
	"time"

	modularmetadata "github.com/wplbyx/modular/packages/metadata"
	modulartransport "github.com/wplbyx/modular/packages/transport"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcmetadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func recoveryUnaryInterceptor(policy *modulartransport.Policy) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (reply any, returnedErr error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				policy.Logger().Error(ctx, "gRPC handler panic",
					zap.String("operation", info.FullMethod),
					zap.Any("panic", recovered),
					zap.ByteString("stack", debug.Stack()),
				)
				reply = nil
				returnedErr = status.Error(codes.Internal, "internal server error")
			}
		}()
		return next(ctx, request)
	}
}

func recoveryStreamInterceptor(policy *modulartransport.Policy) grpc.StreamServerInterceptor {
	return func(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) (returnedErr error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				policy.Logger().Error(stream.Context(), "gRPC stream handler panic",
					zap.String("operation", info.FullMethod),
					zap.Any("panic", recovered),
					zap.ByteString("stack", debug.Stack()),
				)
				returnedErr = status.Error(codes.Internal, "internal server error")
			}
		}()
		return next(server, stream)
	}
}

func metadataUnaryInterceptor(policy *modulartransport.Policy) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		requestCtx, err := extractGRPCContext(ctx, policy)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "invalid request metadata")
		}
		setGRPCRequestID(requestCtx)
		return next(requestCtx, request)
	}
}

func metadataStreamInterceptor(policy *modulartransport.Policy) grpc.StreamServerInterceptor {
	return func(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		requestCtx, err := extractGRPCContext(stream.Context(), policy)
		if err != nil {
			return status.Error(codes.InvalidArgument, "invalid request metadata")
		}
		setGRPCRequestID(requestCtx)
		return next(server, &contextServerStream{ServerStream: stream, ctx: requestCtx})
	}
}

func accessUnaryInterceptor(policy *modulartransport.Policy) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		started := time.Now()
		reply, err := next(ctx, request)
		fields := []zap.Field{
			zap.String("operation", info.FullMethod),
			zap.String("grpc_code", status.Code(err).String()),
			zap.Duration("duration", time.Since(started)),
		}
		if isGRPCServerFailure(err) {
			policy.Logger().Error(ctx, "gRPC request completed", append(fields, zap.Error(err))...)
		} else {
			policy.Logger().Info(ctx, "gRPC request completed", fields...)
		}
		return reply, err
	}
}

func accessStreamInterceptor(policy *modulartransport.Policy) grpc.StreamServerInterceptor {
	return func(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		started := time.Now()
		err := next(server, stream)
		fields := []zap.Field{
			zap.String("operation", info.FullMethod),
			zap.String("grpc_code", status.Code(err).String()),
			zap.Duration("duration", time.Since(started)),
		}
		if isGRPCServerFailure(err) {
			policy.Logger().Error(stream.Context(), "gRPC stream completed", append(fields, zap.Error(err))...)
		} else {
			policy.Logger().Info(stream.Context(), "gRPC stream completed", fields...)
		}
		return err
	}
}

func protectionUnaryInterceptor(policy *modulartransport.Policy) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		done, err := policy.Protection().Allow(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}
		reply, err := next(ctx, request)
		if isGRPCServerFailure(err) {
			done(err)
		} else {
			done(nil)
		}
		return reply, err
	}
}

func protectionStreamInterceptor(policy *modulartransport.Policy) grpc.StreamServerInterceptor {
	return func(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		done, err := policy.Protection().Allow(stream.Context(), info.FullMethod)
		if err != nil {
			return err
		}
		err = next(server, stream)
		if isGRPCServerFailure(err) {
			done(err)
		} else {
			done(nil)
		}
		return err
	}
}

func extractGRPCContext(ctx context.Context, policy *modulartransport.Policy) (context.Context, error) {
	incoming, _ := grpcmetadata.FromIncomingContext(ctx)
	return policy.Propagator().Extract(ctx, modulartransport.GRPCMetadataCarrier{Metadata: incoming})
}

func setGRPCRequestID(ctx context.Context) {
	values := modularmetadata.FromContext(ctx).Get(modularmetadata.RequestIDKey)
	if len(values) > 0 {
		_ = grpc.SetHeader(ctx, grpcmetadata.Pairs(modularmetadata.RequestIDKey, values[0]))
	}
}

func isGRPCServerFailure(err error) bool {
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
