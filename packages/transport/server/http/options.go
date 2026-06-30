package http

import (
	"github.com/gin-gonic/gin"
)

// ServerOption is a function that configures the server
type ServerOption func(*Server)

// WithMiddleware adds middleware to the server
func WithMiddleware(middleware ...gin.HandlerFunc) ServerOption {
	return func(s *Server) {
		s.engine.Use(middleware...)
	}
}

// WithMode sets the gin mode
func WithMode(mode string) ServerOption {
	return func(s *Server) {
		gin.SetMode(mode)
	}
}
