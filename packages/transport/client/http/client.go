package http

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxRetryResponseDrain = 64 << 10

// RetryPolicy decides whether a replayable request should be retried.
// A custom policy may explicitly authorize POST, PATCH, or extension methods.
type RetryPolicy func(request *http.Request, response *http.Response, err error) bool

// Config contains HTTP client and retry configuration.
type Config struct {
	Timeout         time.Duration
	MaxRetries      int
	RetryDelay      time.Duration
	MaxRetryDelay   time.Duration
	MaxIdleConns    int
	IdleConnTimeout time.Duration
	Transport       http.RoundTripper
	RetryPolicy     RetryPolicy
}

// DefaultConfig returns conservative defaults suitable for general API calls.
func DefaultConfig() *Config {
	return &Config{
		Timeout:         30 * time.Second,
		MaxRetries:      3,
		RetryDelay:      time.Second,
		MaxRetryDelay:   30 * time.Second,
		MaxIdleConns:    100,
		IdleConnTimeout: 90 * time.Second,
	}
}

// Client is an explicit, dependency-injected HTTP client.
type Client struct {
	client *http.Client
	config Config
}

// NewClient creates a concrete HTTP client. A nil config uses DefaultConfig.
func NewClient(cfg *Config) *Client {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	config := *cfg
	transport := config.Transport
	if transport == nil {
		transport = &http.Transport{
			MaxIdleConns:       config.MaxIdleConns,
			IdleConnTimeout:    config.IdleConnTimeout,
			DisableCompression: false,
			ForceAttemptHTTP2:  true,
		}
	}
	return &Client{
		client: &http.Client{Timeout: config.Timeout, Transport: transport},
		config: config,
	}
}

// Do executes a request and retries only when the request is replayable and the
// method is idempotent, carries Idempotency-Key, or is approved by RetryPolicy.
// The caller owns the final response body.
func (client *Client) Do(request *http.Request) (*http.Response, error) {
	if request == nil {
		return nil, errors.New("http request is nil")
	}
	maxRetries := client.config.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}

	for attempt := 0; ; attempt++ {
		if attempt > 0 && request.GetBody != nil {
			body, err := request.GetBody()
			if err != nil {
				return nil, fmt.Errorf("reset request body: %w", err)
			}
			request.Body = body
		}

		response, err := client.client.Do(request)
		if contextErr := request.Context().Err(); contextErr != nil {
			closeRetryResponse(response)
			return nil, contextErr
		}
		if attempt >= maxRetries || !client.shouldRetry(request, response, err) {
			return response, err
		}

		delay := client.retryDelay(attempt, response)
		closeRetryResponse(response)
		if err := sleepWithContext(request.Context(), delay); err != nil {
			return nil, err
		}
	}
}

// Get sends a GET request and returns a successful response body.
func (client *Client) Get(ctx context.Context, url string, headers map[string]string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	setHeaders(request, headers)
	return client.read(request)
}

// Post sends a JSON POST request. It is retried only when headers contain an
// Idempotency-Key or a custom RetryPolicy approves it.
func (client *Client) Post(ctx context.Context, url string, body []byte, headers map[string]string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	setHeaders(request, headers)
	return client.read(request)
}

// PostMultipart sends an in-memory multipart/form-data request.
func (client *Client) PostMultipart(
	ctx context.Context,
	url string,
	fields map[string]string,
	files map[string][]byte,
) ([]byte, error) {
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("write field %s: %w", key, err)
		}
	}
	for fieldName, content := range files {
		part, err := writer.CreateFormFile(fieldName, fieldName)
		if err != nil {
			return nil, fmt.Errorf("create form file %s: %w", fieldName, err)
		}
		if _, err := part.Write(content); err != nil {
			return nil, fmt.Errorf("write file %s: %w", fieldName, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}
	return client.postMultipart(ctx, url, body.Bytes(), writer.FormDataContentType())
}

// PostMultipartWithFile sends a multipart request populated from local files.
func (client *Client) PostMultipartWithFile(
	ctx context.Context,
	url string,
	fields map[string]string,
	filePaths map[string]string,
) ([]byte, error) {
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			return nil, fmt.Errorf("write field %s: %w", key, err)
		}
	}
	for fieldName, filePath := range filePaths {
		if err := appendMultipartFile(writer, fieldName, filePath); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}
	return client.postMultipart(ctx, url, body.Bytes(), writer.FormDataContentType())
}

// Download downloads a successful response into a same-directory temporary
// file and atomically replaces destPath only after the full body is written.
func (client *Client) Download(ctx context.Context, url, destPath string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("download request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return newResponseError(response)
	}

	directory := filepath.Dir(destPath)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create download directory: %w", err)
	}
	file, err := os.CreateTemp(directory, filepath.Base(destPath)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary download: %w", err)
	}
	temporaryPath := file.Name()
	keepTemporary := true
	defer func() {
		_ = file.Close()
		if keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	if _, err := io.Copy(file, response.Body); err != nil {
		return fmt.Errorf("write download: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close download: %w", err)
	}
	if err := os.Rename(temporaryPath, destPath); err != nil {
		return fmt.Errorf("replace download: %w", err)
	}
	keepTemporary = false
	return nil
}

func (client *Client) postMultipart(ctx context.Context, url string, body []byte, contentType string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Content-Type", contentType)
	return client.read(request)
}

func (client *Client) read(request *http.Request) ([]byte, error) {
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, newResponseError(response)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	return body, nil
}

func (client *Client) shouldRetry(request *http.Request, response *http.Response, err error) bool {
	if request.Context().Err() != nil || !isReplayable(request) {
		return false
	}
	if client.config.RetryPolicy != nil {
		return client.config.RetryPolicy(request, response, err)
	}
	if !isRetryableMethod(request) {
		return false
	}
	if err != nil {
		return isTemporaryNetworkError(err)
	}
	return response != nil && isRetryableStatus(response.StatusCode)
}

func (client *Client) retryDelay(attempt int, response *http.Response) time.Duration {
	if response != nil {
		if delay, ok := parseRetryAfter(response.Header.Get("Retry-After"), time.Now()); ok {
			return delay
		}
	}
	delay := client.config.RetryDelay
	if delay <= 0 {
		return 0
	}
	for i := 0; i < attempt; i++ {
		if client.config.MaxRetryDelay > 0 && delay >= client.config.MaxRetryDelay/2 {
			return client.config.MaxRetryDelay
		}
		delay *= 2
	}
	if client.config.MaxRetryDelay > 0 && delay > client.config.MaxRetryDelay {
		return client.config.MaxRetryDelay
	}
	return delay
}

func isReplayable(request *http.Request) bool {
	return request.Body == nil || request.Body == http.NoBody || request.GetBody != nil
}

func isRetryableMethod(request *http.Request) bool {
	switch request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace, http.MethodPut, http.MethodDelete:
		return true
	case http.MethodPost, http.MethodPatch:
		return request.Header.Get("Idempotency-Key") != ""
	default:
		return false
	}
}

func isRetryableStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests ||
		status >= 500 && status <= 599
}

func isTemporaryNetworkError(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary())
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	delay := when.Sub(now)
	if delay < 0 {
		delay = 0
	}
	return delay, true
}

func closeRetryResponse(response *http.Response) {
	if response == nil || response.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxRetryResponseDrain))
	_ = response.Body.Close()
}

func appendMultipartFile(writer *multipart.Writer, fieldName, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open file %s: %w", filePath, err)
	}
	part, err := writer.CreateFormFile(fieldName, filepath.Base(filePath))
	if err == nil {
		_, err = io.Copy(part, file)
	}
	closeErr := file.Close()
	if err != nil {
		return fmt.Errorf("append file %s: %w", filePath, err)
	}
	if closeErr != nil {
		return fmt.Errorf("close file %s: %w", filePath, closeErr)
	}
	return nil
}

func setHeaders(request *http.Request, headers map[string]string) {
	for key, value := range headers {
		request.Header.Set(key, value)
	}
}

func newResponseError(response *http.Response) error {
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if readErr != nil {
		return fmt.Errorf("http status %d; read error body: %w", response.StatusCode, readErr)
	}
	return fmt.Errorf("http status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
