package http

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"modular/packages/config"
)

func withStop(t *testing.T, srv *Server) {
	t.Helper()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
}

func doRequest(t *testing.T, srv *Server, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, nil)
	srv.engine.ServeHTTP(w, req)
	return w
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

func TestHealthDisabled(t *testing.T) {
	srv, err := NewServer(&config.HTTP{Host: "127.0.0.1", Port: 0}, WithHealth(""))
	require.NoError(t, err)
	withStop(t, srv)

	w := doRequest(t, srv, http.MethodGet, DefaultHealthPath)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestServerStartServeAndStop(t *testing.T) {
	srv, err := NewServer(&config.HTTP{Host: "127.0.0.1", Port: 0})
	require.NoError(t, err)

	// 浠?listener 鑾峰彇鐪熷疄绔彛锛屾瀯閫?URL
	tcpAddr := srv.listener.Addr().(*net.TCPAddr)
	url := fmt.Sprintf("http://127.0.0.1:%d", tcpAddr.Port)

	go func() { _ = srv.Startup(context.Background()) }()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	require.True(t, eventually(func() bool { return srv.IsRunning() }), "server should be running")

	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(url + DefaultHealthPath)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "ok", string(body))

	require.NoError(t, srv.Shutdown(context.Background()))
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
