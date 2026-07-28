package rpc

import (
	"context"
	"fmt"
	"runtime/debug"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/wplbyx/modular/packages/errs"
	modularmetadata "github.com/wplbyx/modular/packages/metadata"
)

var panicMessage = errs.Define("INTERNAL_ERROR", errs.Template("internal server error"))

func errorUnaryInterceptor(handler *errs.Handler) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (reply any, returnedErr error) {
		ctx, requestInfo := rpcRequestInfo(ctx, info.FullMethod)
		defer func() {
			if recovered := recover(); recovered != nil {
				panicErr := errs.InternalServer(
					panicMessage,
					errs.WithCause(fmt.Errorf("panic: %v", recovered)),
					errs.WithField("panic_stack", string(debug.Stack())),
				)
				result := handler.Handle(ctx, panicErr, requestInfo)
				reply = nil
				returnedErr = result.Error
				setContentLanguage(ctx, result.Locale)
			}
		}()

		reply, returnedErr = next(ctx, req)
		if returnedErr == nil {
			return reply, nil
		}
		result := handler.Handle(ctx, returnedErr, requestInfo)
		setContentLanguage(ctx, result.Locale)
		return nil, result.Error
	}
}

func errorStreamInterceptor(handler *errs.Handler) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) (returnedErr error) {
		ctx, requestInfo := rpcRequestInfo(stream.Context(), info.FullMethod)
		wrapped := &contextServerStream{ServerStream: stream, ctx: ctx}
		defer func() {
			if recovered := recover(); recovered != nil {
				panicErr := errs.InternalServer(
					panicMessage,
					errs.WithCause(fmt.Errorf("panic: %v", recovered)),
					errs.WithField("panic_stack", string(debug.Stack())),
				)
				result := handler.Handle(ctx, panicErr, requestInfo)
				returnedErr = result.Error
				setContentLanguage(ctx, result.Locale)
			}
		}()

		returnedErr = next(srv, wrapped)
		if returnedErr == nil {
			return nil
		}
		result := handler.Handle(ctx, returnedErr, requestInfo)
		setContentLanguage(ctx, result.Locale)
		return result.Error
	}
}

type contextServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (stream *contextServerStream) Context() context.Context { return stream.ctx }

func rpcRequestInfo(ctx context.Context, operation string) (context.Context, errs.RequestInfo) {
	md := modularmetadata.FromContext(ctx)
	requestID := firstMetadata(md.Get(modularmetadata.RequestIDKey))
	language := firstMetadata(md.Get(modularmetadata.LanguageKey))
	if requestID == "" || language == "" {
		incoming, _ := metadata.FromIncomingContext(ctx)
		if requestID == "" {
			requestID = firstMetadata(incoming.Get(modularmetadata.RequestIDKey))
		}
		if language == "" {
			language = firstMetadata(incoming.Get("accept-language"))
		}
	}
	return ctx, errs.RequestInfo{
		Transport: "grpc",
		Operation: operation,
		RequestID: requestID,
		Language:  language,
	}
}

func setContentLanguage(ctx context.Context, locale string) {
	if locale != "" {
		_ = grpc.SetHeader(ctx, metadata.Pairs("content-language", locale))
	}
}

func firstMetadata(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
