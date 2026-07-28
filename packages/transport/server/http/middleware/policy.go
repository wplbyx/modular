package middleware

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/wplbyx/modular/packages/errs"
	"github.com/wplbyx/modular/packages/metadata"
	modulartransport "github.com/wplbyx/modular/packages/transport"
)

var invalidMetadataMessage = errs.Define("INVALID_METADATA", errs.Template("invalid request metadata"))

// Metadata extracts safe request metadata and publishes the request ID.
func Metadata(policy *modulartransport.Policy) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		requestCtx, err := policy.Propagator().Extract(
			ctx.Request.Context(),
			modulartransport.HTTPHeaderCarrier{Header: ctx.Request.Header},
		)
		if err != nil {
			_ = ctx.Error(errs.BadRequest(
				invalidMetadataMessage,
				errs.WithCause(err),
			))
			ctx.Abort()
			return
		}
		ctx.Request = ctx.Request.WithContext(requestCtx)
		if requestID := metadata.FromContext(requestCtx).Get(metadata.RequestIDKey); len(requestID) > 0 {
			ctx.Header("X-Request-Id", requestID[0])
		}
		ctx.Next()
	}
}

// Protection applies adaptive admission and records only server failures as
// breaker failures. Client errors do not describe service health.
func Protection(policy *modulartransport.Policy) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		protection := policy.Protection()
		if protection == nil {
			ctx.Next()
			return
		}
		operation := ctx.Request.Method + " " + ctx.FullPath()
		if ctx.FullPath() == "" {
			operation = ctx.Request.Method + " " + ctx.Request.URL.Path
		}
		done, err := protection.Allow(ctx.Request.Context(), operation)
		if err != nil {
			_ = ctx.Error(err)
			ctx.Abort()
			return
		}

		ctx.Next()
		if ctx.Writer.Status() >= http.StatusInternalServerError {
			if len(ctx.Errors) > 0 {
				done(ctx.Errors.Last().Err)
				return
			}
			done(fmt.Errorf("http response status %d", ctx.Writer.Status()))
			return
		}
		done(nil)
	}
}
