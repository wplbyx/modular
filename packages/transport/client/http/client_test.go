package http

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoRetriesReplayableIdempotentRequest(t *testing.T) {
	var attempts atomic.Int32
	firstBody := &trackingBody{Reader: strings.NewReader("temporary")}
	client := testClient(func(*http.Request) (*http.Response, error) {
		if attempts.Add(1) == 1 {
			return responseWithBody(http.StatusInternalServerError, firstBody), nil
		}
		return textResponse(http.StatusOK, "ok"), nil
	})
	request := newRequest(t, http.MethodGet, nil)

	response, err := client.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.EqualValues(t, 2, attempts.Load())
	assert.True(t, firstBody.closed.Load(), "intermediate response body was not closed")
}

func TestDoDoesNotRetryUnsafePost(t *testing.T) {
	var attempts atomic.Int32
	client := testClient(func(*http.Request) (*http.Response, error) {
		attempts.Add(1)
		return textResponse(http.StatusInternalServerError, "failed"), nil
	})
	request := newRequest(t, http.MethodPost, bytes.NewReader([]byte("body")))

	response, err := client.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.EqualValues(t, 1, attempts.Load())
}

func TestDoRetriesPostWithIdempotencyKey(t *testing.T) {
	var attempts atomic.Int32
	client := testClient(func(*http.Request) (*http.Response, error) {
		if attempts.Add(1) == 1 {
			return textResponse(http.StatusTooManyRequests, "retry"), nil
		}
		return textResponse(http.StatusCreated, "created"), nil
	})
	request := newRequest(t, http.MethodPost, bytes.NewReader([]byte("body")))
	request.Header.Set("Idempotency-Key", "operation-1")

	response, err := client.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.Equal(t, http.StatusCreated, response.StatusCode)
	assert.EqualValues(t, 2, attempts.Load())
}

func TestDoDoesNotRetryNonReplayableBody(t *testing.T) {
	var attempts atomic.Int32
	client := testClient(func(*http.Request) (*http.Response, error) {
		attempts.Add(1)
		return textResponse(http.StatusInternalServerError, "failed"), nil
	})
	request := newRequest(t, http.MethodPut, io.NopCloser(strings.NewReader("body")))
	require.Nil(t, request.GetBody)

	response, err := client.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.EqualValues(t, 1, attempts.Load())
}

func TestDoRetriesOnlyTemporaryNetworkErrors(t *testing.T) {
	t.Run("temporary", func(t *testing.T) {
		var attempts atomic.Int32
		client := testClient(func(*http.Request) (*http.Response, error) {
			if attempts.Add(1) == 1 {
				return nil, temporaryError{}
			}
			return textResponse(http.StatusOK, "ok"), nil
		})
		response, err := client.Do(newRequest(t, http.MethodGet, nil))
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		assert.EqualValues(t, 2, attempts.Load())
	})

	t.Run("permanent", func(t *testing.T) {
		var attempts atomic.Int32
		permanentErr := errors.New("invalid address")
		client := testClient(func(*http.Request) (*http.Response, error) {
			attempts.Add(1)
			return nil, permanentErr
		})
		response, err := client.Do(newRequest(t, http.MethodGet, nil))
		require.ErrorIs(t, err, permanentErr)
		assert.Nil(t, response)
		assert.EqualValues(t, 1, attempts.Load())
	})
}

func TestDoCustomPolicyCanAuthorizePatch(t *testing.T) {
	var attempts atomic.Int32
	client := NewClient(&Config{
		MaxRetries: 1,
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			if attempts.Add(1) == 1 {
				return textResponse(http.StatusConflict, "conflict"), nil
			}
			return textResponse(http.StatusOK, "ok"), nil
		}),
		RetryPolicy: func(_ *http.Request, response *http.Response, _ error) bool {
			return response != nil && response.StatusCode == http.StatusConflict
		},
	})
	request := newRequest(t, http.MethodPatch, bytes.NewReader([]byte("body")))

	response, err := client.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	assert.EqualValues(t, 2, attempts.Load())
}

func TestDoHonorsContextDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := NewClient(&Config{
		MaxRetries: 1,
		RetryDelay: time.Hour,
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			cancel()
			return textResponse(http.StatusServiceUnavailable, "down"), nil
		}),
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com", nil)
	require.NoError(t, err)

	response, err := client.Do(request)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, response)
}

func TestRetryAfterAndExponentialBackoff(t *testing.T) {
	client := NewClient(&Config{RetryDelay: time.Second, MaxRetryDelay: 3 * time.Second})
	assert.Equal(t, time.Second, client.retryDelay(0, nil))
	assert.Equal(t, 2*time.Second, client.retryDelay(1, nil))
	assert.Equal(t, 3*time.Second, client.retryDelay(2, nil))

	response := textResponse(http.StatusTooManyRequests, "retry")
	response.Header.Set("Retry-After", "7")
	assert.Equal(t, 7*time.Second, client.retryDelay(0, response))
	require.NoError(t, response.Body.Close())
}

func TestDownloadRetriesAndAtomicallyReplacesDestination(t *testing.T) {
	var attempts atomic.Int32
	client := testClient(func(*http.Request) (*http.Response, error) {
		if attempts.Add(1) == 1 {
			return textResponse(http.StatusInternalServerError, "temporary"), nil
		}
		return textResponse(http.StatusOK, "new content"), nil
	})
	destination := filepath.Join(t.TempDir(), "nested", "file.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(destination), 0o755))
	require.NoError(t, os.WriteFile(destination, []byte("old content"), 0o644))

	require.NoError(t, client.Download(context.Background(), "https://example.com/file.txt", destination))
	content, err := os.ReadFile(destination)
	require.NoError(t, err)
	assert.Equal(t, "new content", string(content))
	assert.EqualValues(t, 2, attempts.Load())
}

func TestDownloadKeepsDestinationOnHTTPError(t *testing.T) {
	client := testClient(func(*http.Request) (*http.Response, error) {
		return textResponse(http.StatusBadGateway, "failed"), nil
	})
	destination := filepath.Join(t.TempDir(), "file.txt")
	require.NoError(t, os.WriteFile(destination, []byte("old content"), 0o644))

	require.Error(t, client.Download(context.Background(), "https://example.com/file.txt", destination))
	content, err := os.ReadFile(destination)
	require.NoError(t, err)
	assert.Equal(t, "old content", string(content))
}

func testClient(roundTrip func(*http.Request) (*http.Response, error)) *Client {
	return NewClient(&Config{
		Timeout:    time.Second,
		MaxRetries: 1,
		Transport:  roundTripFunc(roundTrip),
	})
}

func newRequest(t *testing.T, method string, body io.Reader) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, "https://example.com", body)
	require.NoError(t, err)
	return request
}

func textResponse(status int, body string) *http.Response {
	return responseWithBody(status, io.NopCloser(strings.NewReader(body)))
}

func responseWithBody(status int, body io.ReadCloser) *http.Response {
	return &http.Response{StatusCode: status, Body: body, Header: make(http.Header)}
}

type trackingBody struct {
	io.Reader
	closed atomic.Bool
}

func (body *trackingBody) Close() error {
	body.closed.Store(true)
	return nil
}

type temporaryError struct{}

func (temporaryError) Error() string   { return "temporary network error" }
func (temporaryError) Timeout() bool   { return false }
func (temporaryError) Temporary() bool { return true }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
