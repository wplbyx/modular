package http

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"modular/packages/config"
)

// withStop 确保每个构造出的 Server 都会关闭 listener，避免端口泄漏。
func withStop(t *testing.T, srv *Server) {
	t.Helper()
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
}

// doRequest 通过 gin 引擎直接处理请求，免去真实网络往返。
func doRequest(t *testing.T, srv *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	srv.engine.ServeHTTP(w, req)
	return w
}

func TestServerEndpoint(t *testing.T) {
	srv, err := NewServer(&config.HTTP{Host: "0.0.0.0", Port: 8080})
	require.NoError(t, err)
	withStop(t, srv)

	u, err := srv.Endpoint()
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:8080", u.String())
}

func TestServerEndpointTLS(t *testing.T) {
	srv, err := NewServer(&config.HTTP{Host: "localhost", Port: 8443})
	require.NoError(t, err)
	withStop(t, srv)
	srv.enableTLS = true // 模拟 TLS 启用后的端点判定，避免依赖真实证书

	u, err := srv.Endpoint()
	require.NoError(t, err)
	assert.Equal(t, "https://localhost:8443", u.String())
}

func TestServerEndpointIPv6(t *testing.T) {
	srv, err := NewServer(&config.HTTP{Host: "::1", Port: 0})
	if err != nil {
		t.Skipf("环境不支持 IPv6 监听: %v", err)
	}
	withStop(t, srv)

	u, err := srv.Endpoint()
	require.NoError(t, err)
	assert.Equal(t, "http", u.Scheme)
	assert.True(t, strings.HasPrefix(u.Host, "[::1]:"), "unexpected host %q", u.Host)
}

func TestServerEndpointDynamicPort(t *testing.T) {
	srv, err := NewServer(&config.HTTP{Host: "127.0.0.1", Port: 0})
	require.NoError(t, err)
	withStop(t, srv)

	u, err := srv.Endpoint()
	require.NoError(t, err)

	_, portStr, err := net.SplitHostPort(u.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	assert.Greater(t, port, 0)
}

func TestNewServerTLSConfigIncomplete(t *testing.T) {
	_, err := NewServer(&config.HTTP{Host: "127.0.0.1", Port: 0, EnableTLS: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TLS")
}

func TestNewServerDefaultsTimeouts(t *testing.T) {
	srv, err := NewServer(&config.HTTP{Host: "127.0.0.1", Port: 0})
	require.NoError(t, err)
	withStop(t, srv)

	assert.Equal(t, defaultReadHeaderTimeout, srv.server.ReadHeaderTimeout)
	assert.Equal(t, defaultReadTimeout, srv.server.ReadTimeout)
	assert.Equal(t, defaultWriteTimeout, srv.server.WriteTimeout)
	assert.Equal(t, defaultIdleTimeout, srv.server.IdleTimeout)
}

func TestDefaultHealthHandler(t *testing.T) {
	srv, err := NewServer(&config.HTTP{Host: "127.0.0.1", Port: 0})
	require.NoError(t, err)
	withStop(t, srv)

	w := doRequest(t, srv, http.MethodGet, DefaultHealthPath)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "ok", w.Body.String())
}

func TestHealthPathConfigurable(t *testing.T) {
	srv, err := NewServer(&config.HTTP{Host: "127.0.0.1", Port: 0}, WithHealth("/healthz"))
	require.NoError(t, err)
	withStop(t, srv)

	w := doRequest(t, srv, http.MethodGet, "/healthz")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "ok", w.Body.String())
}

func TestHealthDisabled(t *testing.T) {
	srv, err := NewServer(&config.HTTP{Host: "127.0.0.1", Port: 0}, WithHealth(""))
	require.NoError(t, err)
	withStop(t, srv)

	// 关闭健康检查后默认路径未注册，gin 返回 404
	w := doRequest(t, srv, http.MethodGet, DefaultHealthPath)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestServerStartServeAndStop(t *testing.T) {
	srv, err := NewServer(&config.HTTP{Host: "127.0.0.1", Port: 0})
	require.NoError(t, err)

	u, err := srv.Endpoint()
	require.NoError(t, err)

	go func() { _ = srv.Start(context.Background()) }()
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	require.True(t, eventually(func() bool { return srv.IsRunning() }), "server should be running")

	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(u.String() + DefaultHealthPath)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", string(body))

	require.NoError(t, srv.Stop(context.Background()))
	require.True(t, eventually(func() bool { return !srv.IsRunning() }), "server should be stopped")
}

func TestRegisterRoute(t *testing.T) {
	srv, err := NewServer(&config.HTTP{Host: "127.0.0.1", Port: 0})
	require.NoError(t, err)
	withStop(t, srv)

	srv.RegisterRoute(func(e *gin.Engine) {
		e.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	})

	w := doRequest(t, srv, http.MethodGet, "/ping")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "pong", w.Body.String())
}

// eventually 在 2s 内轮询 fn，返回 true 即成功。
func eventually(fn func() bool) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fn()
}
