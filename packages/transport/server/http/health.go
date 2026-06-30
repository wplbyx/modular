package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// registerHealth 按配置注册健康检查路由。
// 未显式配置时使用 DefaultHealthPath 与最简 200 "ok" 处理函数。
func (s *Server) registerHealth() {
	if s.healthDisabled {
		return
	}
	path := s.healthPath
	if path == "" {
		path = DefaultHealthPath
	}
	handler := s.healthHandler
	if handler == nil {
		handler = defaultHealthHandler
	}
	s.engine.GET(path, handler)
}

// defaultHealthHandler 返回 200 "ok" 的最简健康检查处理函数。
func defaultHealthHandler(c *gin.Context) {
	c.String(http.StatusOK, "ok")
}
