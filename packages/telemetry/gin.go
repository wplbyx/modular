package telemetry

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	tracerName  = "holographic-server/gin"
	spanNameKey = "span.name"
)

// GinMiddleware 返回一个 Gin 中间件，用于添加链路追踪和指标统计
func GinMiddleware(serviceName string) gin.HandlerFunc {
	tracer := otel.Tracer(tracerName)

	return func(c *gin.Context) {
		// 开始计时
		startTime := time.Now()

		// 创建 span
		spanName := serviceName + ":" + c.Request.URL.Path
		ctx, span := tracer.Start(
			c.Request.Context(),
			spanName,
			trace.WithAttributes(
				attribute.String("http.method", c.Request.Method),
				attribute.String("http.url", c.Request.URL.String()),
				attribute.String("http.host", c.Request.Host),
				attribute.String("http.scheme", c.Request.URL.Scheme),
				attribute.String("http.remote_addr", c.Request.RemoteAddr),
				attribute.String("http.user_agent", c.Request.UserAgent()),
				attribute.String("http.request.header.x-request-id", c.Request.Header.Get("X-Request-ID")),
				attribute.String("server.address", serviceName),
				attribute.String("server.port", strconv.Itoa(int(parsePort(c.Request.URL.Host)))),
			),
		)
		defer span.End()

		// 将 context 更新到 gin.Context 中，以便后续处理使用
		c.Request = c.Request.WithContext(ctx)

		// 存储开始时间和 span，供后续中间件使用
		c.Set("start_time", startTime)
		c.Set("span", span)

		// 处理请求
		c.Next()

		// 请求处理完成后记录响应信息
		span.SetAttributes(
			attribute.Int("http.status_code", c.Writer.Status()),
			attribute.String("http.response.size", strconv.Itoa(c.Writer.Size())),
			attribute.Int("http.response.header_count", len(c.Writer.Header())),
		)

		// 如果有错误，记录到 span
		if len(c.Errors) > 0 {
			span.SetStatus(codes.Error, c.Errors.String())
			span.RecordError(c.Errors.Last())
		} else {
			span.SetStatus(codes.Ok, "")
		}

		// 计算处理时间
		duration := time.Since(startTime)
		span.SetAttributes(
			attribute.Float64("http.duration_ms", float64(duration.Milliseconds())),
		)
	}
}

// parsePort 从 host 中解析端口号
func parsePort(host string) int {
	if host == "" {
		return 80
	}
	// 如果 host 包含端口，提取端口号
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			port, err := strconv.Atoi(host[i+1:])
			if err != nil {
				return 80
			}
			return port
		}
	}
	return 80
}

// GetSpan 从 gin.Context 中获取当前的 span
func GetSpan(c *gin.Context) trace.Span {
	if span, exists := c.Get("span"); exists {
		if s, ok := span.(trace.Span); ok {
			return s
		}
	}
	return nil
}

// GetStartTime 从 gin.Context 中获取请求开始时间
func GetStartTime(c *gin.Context) time.Time {
	if startTime, exists := c.Get("start_time"); exists {
		if t, ok := startTime.(time.Time); ok {
			return t
		}
	}
	return time.Time{}
}
