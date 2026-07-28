package middleware

import (
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"
	modularlog "github.com/wplbyx/modular/packages/log"
	"go.uber.org/zap"
)

// GinLogger records one access event after the handler chain completes.
func GinLogger(logger modularlog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", c.FullPath()),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("duration", time.Since(started)),
		}
		if len(c.Errors) > 0 || c.Writer.Status() >= http.StatusInternalServerError {
			logger.Error(c.Request.Context(), "http request completed", fields...)
			return
		}
		logger.Info(c.Request.Context(), "http request completed", fields...)
	}
}

// GinRecovery converts an unhandled panic into an internal response.
func GinRecovery(logger modularlog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error(c.Request.Context(), "http handler panic",
					zap.Any("panic", recovered),
					zap.ByteString("stack", debug.Stack()),
				)
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}
