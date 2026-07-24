package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/errs"
	"github.com/wplbyx/modular/packages/health"
	"go.uber.org/zap"
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
	_, err := NewServer(&configitem.HTTP{Host: "127.0.0.1", Port: 0, EnableTLS: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TLS")
}

func TestNewServerDefaultsTimeouts(t *testing.T) {
	srv, err := NewServer(&configitem.HTTP{Host: "127.0.0.1", Port: 0})
	require.NoError(t, err)
	withStop(t, srv)

	assert.Equal(t, defaultReadHeaderTimeout, srv.server.ReadHeaderTimeout)
	assert.Equal(t, defaultReadTimeout, srv.server.ReadTimeout)
	assert.Equal(t, defaultWriteTimeout, srv.server.WriteTimeout)
	assert.Equal(t, defaultIdleTimeout, srv.server.IdleTimeout)
}

func TestNewServerNoWriteTimeout(t *testing.T) {
	srv, err := NewServer(&configitem.HTTP{Host: "127.0.0.1", Port: 0, WriteTimeout: NoWriteTimeout})
	require.NoError(t, err)
	withStop(t, srv)

	assert.Zero(t, srv.server.WriteTimeout)
}

func TestDefaultHealthHandler(t *testing.T) {
	srv, err := NewServer(&configitem.HTTP{Host: "127.0.0.1", Port: 0})
	require.NoError(t, err)
	withStop(t, srv)

	w := doRequest(t, srv, http.MethodGet, DefaultHealthPath)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "ok", w.Body.String())
}

func TestHealthDisabled(t *testing.T) {
	srv, err := NewServer(&configitem.HTTP{Host: "127.0.0.1", Port: 0}, WithHealth(""))
	require.NoError(t, err)
	withStop(t, srv)

	w := doRequest(t, srv, http.MethodGet, DefaultHealthPath)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestReadinessHandlerAndTransport(t *testing.T) {
	srv, err := NewServer(
		&configitem.HTTP{Host: "127.0.0.1", Port: 0},
		WithReadiness("/readyz", testHealthChecker{name: "database", err: errors.New("down")}),
	)
	require.NoError(t, err)
	withStop(t, srv)

	w := doRequest(t, srv, http.MethodGet, "/readyz")
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, "/readyz", srv.Transport().HealthPath)
	assert.Equal(t, http.StatusOK, doRequest(t, srv, http.MethodGet, DefaultHealthPath).Code)
}

func TestServerStartServeAndStop(t *testing.T) {
	srv, err := NewServer(&configitem.HTTP{Host: "127.0.0.1", Port: 0})
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

func TestServerShutdownBeforeStartupReleasesListener(t *testing.T) {
	srv, err := NewServer(&configitem.HTTP{Host: "127.0.0.1", Port: 0})
	require.NoError(t, err)

	addr := srv.Addr().String()
	require.NoError(t, srv.Shutdown(context.Background()))

	lis, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	require.NoError(t, lis.Close())
}

func TestServerAddrExposesAllocatedPort(t *testing.T) {
	srv, err := NewServer(&configitem.HTTP{Host: "127.0.0.1", Port: 0})
	require.NoError(t, err)
	withStop(t, srv)

	addr, ok := srv.Addr().(*net.TCPAddr)
	require.True(t, ok, "Addr() = %T, want *net.TCPAddr", srv.Addr())
	require.NotZero(t, addr.Port)

	transport := srv.Transport()
	require.Equal(t, "http", transport.Protocol)
	require.Equal(t, "127.0.0.1", transport.Address)
	require.Equal(t, addr.Port, transport.Port)
	require.Equal(t, DefaultHealthPath, transport.HealthPath)
}

func TestRegisterRoute(t *testing.T) {
	srv, err := NewServer(&configitem.HTTP{Host: "127.0.0.1", Port: 0})
	require.NoError(t, err)
	withStop(t, srv)

	srv.RegisterRoute(func(e *gin.Engine) {
		e.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	})

	w := doRequest(t, srv, http.MethodGet, "/ping")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "pong", w.Body.String())
}

func TestErrorHandlerLocalizesAndHidesInternalError(t *testing.T) {
	srv, err := NewServer(
		&configitem.HTTP{Host: "127.0.0.1", Port: 0},
		WithErrorHandler(testHTTPErrorHandler(t)),
	)
	require.NoError(t, err)
	withStop(t, srv)

	userNotFound := errs.Define("USER_NOT_FOUND", errs.Template("user %v not found", errs.Name("user_id")))
	srv.RegisterRoute(func(engine *gin.Engine) {
		engine.GET("/users/:id", Wrap(func(ctx *gin.Context) error {
			return errs.NotFound(
				userNotFound.With("user_id", ctx.Param("id")),
				errs.WithCause(errors.New("database password=secret")),
			)
		}))
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	request.Header.Set("Accept-Language", "en-US;q=0.5, zh-CN;q=0.9")
	srv.engine.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.Equal(t, "zh-CN", recorder.Header().Get("Content-Language"))
	assert.NotContains(t, recorder.Body.String(), "secret")
	var body struct {
		Code    int    `json:"code"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Equal(t, http.StatusNotFound, body.Code)
	assert.Equal(t, "USER_NOT_FOUND", body.Reason)
	assert.Equal(t, "用户 42 不存在", body.Message)
}

func TestErrorHandlerRecoversPanicWithoutLeakingIt(t *testing.T) {
	srv, err := NewServer(
		&configitem.HTTP{Host: "127.0.0.1", Port: 0},
		WithErrorHandler(testHTTPErrorHandler(t)),
	)
	require.NoError(t, err)
	withStop(t, srv)
	srv.RegisterRoute(func(engine *gin.Engine) {
		engine.GET("/panic", func(*gin.Context) { panic("token=secret") })
	})

	recorder := doRequest(t, srv, http.MethodGet, "/panic")
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "secret")
	assert.Contains(t, recorder.Body.String(), "INTERNAL_ERROR")
}

func TestErrorHandlerDoesNotOverwriteCommittedResponse(t *testing.T) {
	srv, err := NewServer(
		&configitem.HTTP{Host: "127.0.0.1", Port: 0},
		WithErrorHandler(testHTTPErrorHandler(t)),
	)
	require.NoError(t, err)
	withStop(t, srv)
	srv.RegisterRoute(func(engine *gin.Engine) {
		engine.GET("/committed", Wrap(func(ctx *gin.Context) error {
			ctx.String(http.StatusAccepted, "accepted")
			return errs.BadRequest(errs.Define("TOO_LATE", errs.Template("too late")))
		}))
	})

	recorder := doRequest(t, srv, http.MethodGet, "/committed")
	assert.Equal(t, http.StatusAccepted, recorder.Code)
	assert.Equal(t, "accepted", recorder.Body.String())
}

func testHTTPErrorHandler(t *testing.T) *errs.Handler {
	t.Helper()
	catalog, err := errs.LoadCatalog(fstest.MapFS{
		"locales/zh-CN.yaml": {Data: []byte("UNKNOWN: '请求失败'\nINTERNAL_ERROR: '服务暂时不可用'\nUSER_NOT_FOUND: '用户 {{.user_id}} 不存在'\n")},
		"locales/en-US.yaml": {Data: []byte("UNKNOWN: 'Request failed'\nINTERNAL_ERROR: 'Service unavailable'\nUSER_NOT_FOUND: 'User {{.user_id}} was not found'\n")},
	}, "locales", "zh-CN")
	require.NoError(t, err)
	handler, err := errs.NewHandler(catalog, zap.NewNop())
	require.NoError(t, err)
	return handler
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

type testHealthChecker struct {
	name string
	err  error
}

func (checker testHealthChecker) Name() string { return checker.name }

func (checker testHealthChecker) Check(context.Context) error { return checker.err }

var _ health.Checker = testHealthChecker{}
