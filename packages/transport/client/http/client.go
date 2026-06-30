package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Client is the HTTP client interface
type Client interface {
	Post(ctx context.Context, url string, body []byte, headers map[string]string) ([]byte, error)
	PostMultipart(ctx context.Context, url string, fields map[string]string, files map[string][]byte) ([]byte, error)
	PostMultipartWithFile(ctx context.Context, url string, fields map[string]string, filePaths map[string]string) ([]byte, error)
	Download(ctx context.Context, url string, destPath string) error
	Get(ctx context.Context, url string, headers map[string]string) ([]byte, error)
}

// Config contains HTTP client configuration
type Config struct {
	Timeout         time.Duration
	MaxRetries      int
	RetryDelay      time.Duration
	MaxIdleConns    int
	IdleConnTimeout time.Duration
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		Timeout:         30 * time.Second,
		MaxRetries:      3,
		RetryDelay:      1 * time.Second,
		MaxIdleConns:    100,
		IdleConnTimeout: 90 * time.Second,
	}
}

// httpClient implements Client interface
type httpClient struct {
	client *http.Client
	config *Config
}

// NewClient creates a new HTTP client
func NewClient(cfg *Config) Client {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	transport := &http.Transport{
		MaxIdleConns:       cfg.MaxIdleConns,
		IdleConnTimeout:    cfg.IdleConnTimeout,
		DisableCompression: false,
		ForceAttemptHTTP2:  true,
	}

	return &httpClient{
		client: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: transport,
		},
		config: cfg,
	}
}

// Post sends a JSON POST request
func (c *httpClient) Post(ctx context.Context, url string, body []byte, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return c.doRequestWithRetry(req)
}

// PostMultipart sends a multipart/form-data request
func (c *httpClient) PostMultipart(ctx context.Context, url string, fields map[string]string, files map[string][]byte) ([]byte, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("write field %s failed: %w", key, err)
		}
	}

	for fieldName, fileContent := range files {
		part, err := writer.CreateFormFile(fieldName, fieldName)
		if err != nil {
			return nil, fmt.Errorf("create form file %s failed: %w", fieldName, err)
		}
		if _, err := part.Write(fileContent); err != nil {
			return nil, fmt.Errorf("write file %s failed: %w", fieldName, err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	return c.doRequestWithRetry(req)
}

// PostMultipartWithFile sends a multipart/form-data request with file paths
func (c *httpClient) PostMultipartWithFile(ctx context.Context, url string, fields map[string]string, filePaths map[string]string) ([]byte, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("write field %s failed: %w", key, err)
		}
	}

	for fieldName, filePath := range filePaths {
		file, err := os.Open(filePath)
		if err != nil {
			return nil, fmt.Errorf("open file %s failed: %w", filePath, err)
		}
		defer file.Close()

		part, err := writer.CreateFormFile(fieldName, filepath.Base(filePath))
		if err != nil {
			return nil, fmt.Errorf("create form file %s failed: %w", fieldName, err)
		}

		if _, err := io.Copy(part, file); err != nil {
			return nil, fmt.Errorf("copy file %s failed: %w", filePath, err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	return c.doRequestWithRetry(req)
}

// Download downloads a file to the specified path
func (c *httpClient) Download(ctx context.Context, url string, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request failed: %w", err)
	}

	var lastErr error
	for i := 0; i <= c.config.MaxRetries; i++ {
		if i > 0 {
			if err := sleepWithContext(ctx, c.config.RetryDelay); err != nil {
				return err
			}
		}

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode >= 400 {
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("http error: %d", resp.StatusCode)
			continue
		}

		dir := filepath.Dir(destPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			_ = resp.Body.Close()
			return fmt.Errorf("create directory failed: %w", err)
		}

		file, err := os.CreateTemp(dir, filepath.Base(destPath)+".*.tmp")
		if err != nil {
			_ = resp.Body.Close()
			return fmt.Errorf("create file failed: %w", err)
		}
		tmpPath := file.Name()

		if _, err := io.Copy(file, resp.Body); err != nil {
			_ = resp.Body.Close()
			_ = file.Close()
			_ = os.Remove(tmpPath)
			return fmt.Errorf("write file failed: %w", err)
		}
		_ = resp.Body.Close()
		if err := file.Close(); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("close file failed: %w", err)
		}
		if err := os.Rename(tmpPath, destPath); err != nil {
			_ = os.Remove(tmpPath)
			return fmt.Errorf("replace file failed: %w", err)
		}

		return nil
	}

	return fmt.Errorf("download failed after %d retries: %w", c.config.MaxRetries, lastErr)
}

// Get sends a GET request
func (c *httpClient) Get(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return c.doRequestWithRetry(req)
}

// doRequestWithRetry executes a request with retry logic
func (c *httpClient) doRequestWithRetry(req *http.Request) ([]byte, error) {
	var lastErr error
	if err := ensureRequestGetBody(req); err != nil {
		return nil, err
	}

	for i := 0; i <= c.config.MaxRetries; i++ {
		if i > 0 {
			if err := sleepWithContext(req.Context(), c.config.RetryDelay); err != nil {
				return nil, err
			}
		}

		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, fmt.Errorf("reset request body failed: %w", err)
			}
			req.Body = body
		}

		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("read response body failed: %w", err)
			continue
		}

		if resp.StatusCode >= 400 {
			lastErr = fmt.Errorf("http error: %d, body: %s", resp.StatusCode, string(body))
			continue
		}

		return body, nil
	}

	return nil, fmt.Errorf("request failed after %d retries: %w", c.config.MaxRetries, lastErr)
}

func ensureRequestGetBody(req *http.Request) error {
	if req.Body == nil || req.GetBody != nil {
		return nil
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return fmt.Errorf("read request body failed: %w", err)
	}
	_ = req.Body.Close()
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	return nil
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Global HTTP client
var defaultClient Client

// Init initializes the global HTTP client
func Init(cfg *Config) {
	defaultClient = NewClient(cfg)
}

// GetClient returns the global HTTP client
func GetClient() Client {
	if defaultClient == nil {
		defaultClient = NewClient(nil)
	}
	return defaultClient
}

// Post sends a POST request using the global client
func Post(ctx context.Context, url string, body []byte, headers map[string]string) ([]byte, error) {
	return GetClient().Post(ctx, url, body, headers)
}

// PostMultipart sends a multipart request using the global client
func PostMultipart(ctx context.Context, url string, fields map[string]string, files map[string][]byte) ([]byte, error) {
	return GetClient().PostMultipart(ctx, url, fields, files)
}

// Download downloads a file using the global client
func Download(ctx context.Context, url string, destPath string) error {
	return GetClient().Download(ctx, url, destPath)
}

// Get sends a GET request using the global client
func Get(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	return GetClient().Get(ctx, url, headers)
}

// IsNetworkError checks if an error is a network error
func IsNetworkError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF)
}
