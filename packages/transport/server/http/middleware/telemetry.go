package middleware

import (
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

// OpenTelemetry integrates trace and metric
func OpenTelemetry(servername string) gin.HandlerFunc {
	return otelgin.Middleware(servername)
}
