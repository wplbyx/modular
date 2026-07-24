package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type checker struct {
	name string
	err  error
}

func (check checker) Name() string                { return check.name }
func (check checker) Check(context.Context) error { return check.err }

func TestRunReturnsDeterministicAggregate(t *testing.T) {
	report := Run(
		context.Background(),
		checker{name: "redis"},
		checker{name: "database", err: errors.New("connection refused")},
	)

	assert.Equal(t, StatusFailed, report.Status)
	require.Len(t, report.Checks, 2)
	assert.Equal(t, "database", report.Checks[0].Name)
	assert.Equal(t, StatusFailed, report.Checks[0].Status)
	assert.Equal(t, "connection refused", report.Checks[0].Error)
	assert.Equal(t, "redis", report.Checks[1].Name)
}

func TestHandlerStatusCodes(t *testing.T) {
	t.Run("ready", func(t *testing.T) {
		response := httptest.NewRecorder()
		Handler(checker{name: "database"}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
		assert.Equal(t, http.StatusOK, response.Code)
		assert.JSONEq(t, `{"status":"ok","checks":[{"name":"database","status":"ok"}]}`, response.Body.String())
	})

	t.Run("not ready", func(t *testing.T) {
		response := httptest.NewRecorder()
		Handler(checker{name: "database", err: errors.New("down")}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
		assert.Equal(t, http.StatusServiceUnavailable, response.Code)
		assert.Contains(t, response.Body.String(), `"status":"failed"`)
	})
}
