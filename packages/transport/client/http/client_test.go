package http

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadRetriesAndReplacesDestination(t *testing.T) {
	var attempts int32
	client := testHTTPClient(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet {
			t.Fatalf("method = %s", req.Method)
		}
		if atomic.AddInt32(&attempts, 1) == 1 {
			return textResponse(http.StatusInternalServerError, "temporary"), nil
		}
		return textResponse(http.StatusOK, "new content"), nil
	})

	dest := filepath.Join(t.TempDir(), "nested", "file.txt")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(dest, []byte("old content"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := client.Download(context.Background(), "https://example.com/file.txt", dest); err != nil {
		t.Fatalf("Download() error = %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "new content" {
		t.Fatalf("downloaded data = %q", data)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d", attempts)
	}
}

func TestDownloadKeepsDestinationOnHTTPError(t *testing.T) {
	client := testHTTPClient(func(*http.Request) (*http.Response, error) {
		return textResponse(http.StatusBadGateway, "failed"), nil
	})

	dest := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(dest, []byte("old content"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if err := client.Download(context.Background(), "https://example.com/file.txt", dest); err == nil {
		t.Fatal("Download() error = nil")
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "old content" {
		t.Fatalf("destination data = %q", data)
	}
}

func testHTTPClient(fn func(*http.Request) (*http.Response, error)) *httpClient {
	return &httpClient{
		client: &http.Client{Transport: roundTripFunc(fn)},
		config: &Config{
			Timeout:         time.Second,
			MaxRetries:      1,
			RetryDelay:      0,
			MaxIdleConns:    2,
			IdleConnTimeout: time.Second,
		},
	}
}

func textResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}
