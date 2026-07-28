package http

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"github.com/wplbyx/modular/packages/errs"
	"github.com/wplbyx/modular/packages/log"
	"go.uber.org/zap"
)

const requestIDHeader = "X-Request-Id"

var panicMessage = errs.Define("INTERNAL_ERROR", errs.Template("internal server error"))

// ErrorHandlerFunc 是可直接返回 error 的 Gin handler。
type ErrorHandlerFunc func(*gin.Context) error

// Wrap 将可返回 error 的 handler 适配为 gin.HandlerFunc。
func Wrap(handler ErrorHandlerFunc) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if handler == nil {
			return
		}
		if err := handler(ctx); err != nil {
			_ = ctx.Error(err)
			ctx.Abort()
		}
	}
}

func errorMiddleware(handler *errs.Handler) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				panicErr := errs.InternalServer(
					panicMessage,
					errs.WithCause(fmt.Errorf("panic: %v", recovered)),
					errs.WithField("panic_stack", string(debug.Stack())),
				)
				handleHTTPError(ctx, handler, panicErr)
				ctx.Abort()
			}
		}()

		ctx.Next()
		if len(ctx.Errors) == 0 {
			return
		}
		handleHTTPError(ctx, handler, ctx.Errors.Last().Err)
	}
}

func defaultErrorMiddleware(logger log.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error(ctx.Request.Context(), "http handler panic",
					zap.Any("panic", recovered),
					zap.ByteString("stack", debug.Stack()),
				)
				ctx.AbortWithStatus(http.StatusInternalServerError)
			}
		}()

		ctx.Next()
		if len(ctx.Errors) == 0 || ctx.Writer.Written() {
			return
		}
		ctx.AbortWithStatus(errs.Code(ctx.Errors.Last().Err))
	}
}

func handleHTTPError(ctx *gin.Context, handler *errs.Handler, err error) {
	operation := ctx.FullPath()
	if operation == "" {
		operation = ctx.Request.URL.Path
	}
	requestID := ctx.Writer.Header().Get(requestIDHeader)
	if requestID == "" {
		requestID = ctx.Request.Header.Get(requestIDHeader)
	}
	result := handler.Handle(ctx.Request.Context(), err, errs.RequestInfo{
		Transport: "http",
		Operation: ctx.Request.Method + " " + operation,
		RequestID: requestID,
		Language:  ctx.GetHeader("Accept-Language"),
	})
	if result.Error == nil || ctx.Writer.Written() {
		return
	}
	ctx.Header("Content-Language", result.Locale)
	ctx.JSON(int(result.Error.Code), result.Error)
}
