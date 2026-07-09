package http

import (
	"context"
	"net"

	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	mw "github.com/wplbyx/modular/packages/transport/server/http/middleware"
)

// ServerOption 用于配置 Server。option 仅写入 Server 字段，
// 真正生效（如注册中间件、设置 mode）由 NewServer 在合适阶段统一完成，
// 因此 option 之间的应用顺序不影响正确性。
type ServerOption func(*Server)

// zapLogger 是日志中间件期望的 logger 接口（与 ginzap.ZapLogger 等价），
// *zap.Logger 天然满足该接口。
type zapLogger = ginzap.ZapLogger

// ginLogger / ginRecovery 将 logger 适配为 gin 中间件，
// 集中在此处以避免 server.go 直接依赖具体日志实现。
func ginLogger(l zapLogger) gin.HandlerFunc   { return mw.GinLogger(l) }
func ginRecovery(l zapLogger) gin.HandlerFunc { return mw.GinRecovery(l) }

// WithLogger 注入 zap logger，启用结构化访问日志与 panic recovery 日志。
// 未调用时，默认仅注册 gin.Recovery()（无访问日志）。
func WithLogger(logger *zap.Logger) ServerOption {
	return func(s *Server) {
		s.logger = logger
	}
}

// WithMiddleware 追加业务中间件，注册顺序与调用顺序一致，
// 且位于 recovery / logger 之后（即处于更内层）。
func WithMiddleware(middlewares ...gin.HandlerFunc) ServerOption {
	return func(s *Server) {
		s.middlewares = append(s.middlewares, middlewares...)
	}
}

// WithMode 设置 gin 运行模式（debug/release/test），
// 在 gin.New() 之前生效。
func WithMode(mode string) ServerOption {
	return func(s *Server) {
		s.mode = mode
	}
}

// WithHealth 配置健康检查路由。
//   - path: 健康检查路径，为空表示关闭默认健康检查；
//   - handler: 可选的自定义处理函数，缺省时返回 200 "ok"。
func WithHealth(path string, handler ...gin.HandlerFunc) ServerOption {
	return func(s *Server) {
		if path == "" {
			s.healthDisabled = true
			return
		}
		s.healthPath = path
		if len(handler) > 0 {
			s.healthHandler = handler[0]
		}
	}
}

// WithTLS 启用 TLS 并指定证书 / 私钥路径，覆盖配置中的 TLS 字段。
func WithTLS(certFile, keyFile string) ServerOption {
	return func(s *Server) {
		s.cfg.EnableTLS = true
		s.cfg.TLSCertFile = certFile
		s.cfg.TLSKeyFile = keyFile
	}
}

// WithH2C 启用明文 HTTP/2 (h2c)，适用于无 TLS 但需要 HTTP/2 的场景。
func WithH2C() ServerOption {
	return func(s *Server) {
		s.h2c = true
	}
}

// WithBaseContext 设置 http.Server.BaseContext，
// 用于为每个请求提供自定义的基础 context（参数为新连接的 listener）。
func WithBaseContext(fn func(net.Listener) context.Context) ServerOption {
	return func(s *Server) {
		s.baseContextFn = fn
	}
}
