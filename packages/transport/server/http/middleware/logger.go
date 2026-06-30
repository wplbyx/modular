package middleware

import (
	"time"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
)

// GinLogger returns a gin logger middleware
func GinLogger(logger ginzap.ZapLogger) gin.HandlerFunc {
	return ginzap.Ginzap(logger, time.RFC3339, true)
}

// GinRecovery returns a gin recovery middleware
func GinRecovery(logger ginzap.ZapLogger) gin.HandlerFunc {
	return ginzap.RecoveryWithZap(logger, true)
}
