package middleware

import (
	"strings"

	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	// RequestIdHeader is the header key for request ID
	RequestIdHeader = "X-Request-Id"
)

// NewRequestId returns a request ID middleware
func NewRequestId(prefix string) gin.HandlerFunc {
	return requestid.New(
		requestid.WithGenerator(func() string {
			return prefix + strings.Replace(uuid.New().String(), "-", "", -1)
		}),
		requestid.WithCustomHeaderStrKey(RequestIdHeader),
	)
}
