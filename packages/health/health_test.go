package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

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

func TestManagerTracksStateAndHidesErrors(t *testing.T) {
	manager := NewManager()
	require.NoError(t, manager.Register(checker{name: "database", err: errors.New("secret DSN")}))

	starting := manager.Report(context.Background())
	assert.Equal(t, StateStarting, starting.State)
	assert.Equal(t, StatusFailed, starting.Status)
	assert.Empty(t, starting.Checks[0].Error)

	manager.SetReady()
	detailed := manager.DetailedReport(context.Background())
	assert.Equal(t, StateReady, detailed.State)
	assert.Equal(t, "secret DSN", detailed.Checks[0].Error)

	response := httptest.NewRecorder()
	manager.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.NotContains(t, response.Body.String(), "secret DSN")
}

func TestManagerRejectsDuplicateAndTimesOutChecks(t *testing.T) {
	manager := NewManager(WithCheckTimeout(10*time.Millisecond), WithMaxConcurrency(1))
	blocked := checkerFunc{name: "blocked", check: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	require.NoError(t, manager.Register(blocked))
	require.Error(t, manager.Register(blocked))
	manager.SetReady()

	report := manager.DetailedReport(context.Background())
	assert.Equal(t, StatusFailed, report.Status)
	assert.ErrorContains(t, errors.New(report.Checks[0].Error), context.DeadlineExceeded.Error())
}

func TestManagerBoundsConcurrentChecks(t *testing.T) {
	manager := NewManager(WithMaxConcurrency(2))
	started := make(chan struct{}, 5)
	release := make(chan struct{})
	var running atomic.Int32
	var peak atomic.Int32
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		check := checkerFunc{name: name, check: func(context.Context) error {
			current := running.Add(1)
			for {
				previous := peak.Load()
				if current <= previous || peak.CompareAndSwap(previous, current) {
					break
				}
			}
			started <- struct{}{}
			<-release
			running.Add(-1)
			return nil
		}}
		require.NoError(t, manager.Register(check))
	}
	manager.SetReady()

	done := make(chan Report, 1)
	go func() { done <- manager.DetailedReport(context.Background()) }()
	<-started
	<-started
	select {
	case <-started:
		t.Fatal("more than two checks ran concurrently")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	report := <-done
	assert.Equal(t, StatusOK, report.Status)
	assert.EqualValues(t, 2, peak.Load())
}

type checkerFunc struct {
	name  string
	check func(context.Context) error
}

func (check checkerFunc) Name() string                    { return check.name }
func (check checkerFunc) Check(ctx context.Context) error { return check.check(ctx) }
