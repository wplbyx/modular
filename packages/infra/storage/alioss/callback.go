package alioss

import (
	"bytes"
	"context"
	"crypto"
	"crypto/md5"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	callbackOKResponse              = `{"Status":"OK"}`
	defaultCallbackMaxBodyBytes     = int64(1 << 20)
	defaultCallbackPublicKeyTimeout = 5 * time.Second
)

var (
	defaultCallbackHTTPClient = &http.Client{Timeout: defaultCallbackPublicKeyTimeout}
	errCallbackBodyTooLarge   = errors.New("callback body is too large")
)

// PublicKeyFetcher 在公钥 URL 解码并校验通过后加载 OSS 回调公钥。
type PublicKeyFetcher func(ctx context.Context, publicKeyURL string) ([]byte, error)

// CallbackPayload 是已验签并解析后的 OSS 回调内容。
type CallbackPayload struct {
	ContentType string
	Values      map[string]string
	RawBody     []byte
}

// CallbackProcessor 处理已验签并解析后的 OSS 回调内容。
type CallbackProcessor func(ctx context.Context, payload CallbackPayload) error

type callbackConfig struct {
	fetcher      PublicKeyFetcher
	maxBodyBytes int64
}

// CallbackOption 配置 NewCallbackHandler。
type CallbackOption func(*callbackConfig)

// WithCallbackPublicKeyFetcher 覆盖默认的 HTTP 公钥加载器。
func WithCallbackPublicKeyFetcher(fetcher PublicKeyFetcher) CallbackOption {
	return func(cfg *callbackConfig) {
		if fetcher != nil {
			cfg.fetcher = fetcher
		}
	}
}

// WithCallbackMaxBodyBytes 设置回调 body 最大读取字节数。
func WithCallbackMaxBodyBytes(maxBytes int64) CallbackOption {
	return func(cfg *callbackConfig) {
		if maxBytes > 0 {
			cfg.maxBodyBytes = maxBytes
		}
	}
}

// NewCallbackHandler 返回标准库 HTTP handler，用于处理 OSS 上传回调。
// 业务服务可以把它挂到任意路由，也可以直接调用底层函数。
func NewCallbackHandler(processor CallbackProcessor, opts ...CallbackOption) http.Handler {
	cfg := callbackConfig{
		fetcher:      defaultCallbackPublicKeyFetcher,
		maxBodyBytes: defaultCallbackMaxBodyBytes,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if processor == nil {
			http.Error(w, "callback processor is nil", http.StatusInternalServerError)
			return
		}
		body, err := readCallbackBody(r.Body, cfg.maxBodyBytes)
		if err != nil {
			if errors.Is(err, errCallbackBodyTooLarge) {
				http.Error(w, "callback body too large", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "read callback body", http.StatusBadRequest)
			return
		}
		if err = VerifyCallbackSignature(r, body, cfg.fetcher); err != nil {
			http.Error(w, "invalid callback signature", http.StatusForbidden)
			return
		}
		payload, err := ParseCallbackPayload(r, body)
		if err != nil {
			http.Error(w, "parse callback body", http.StatusBadRequest)
			return
		}
		if err = processor(r.Context(), payload); err != nil {
			http.Error(w, "process callback", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", fmt.Sprint(len(callbackOKResponse)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(callbackOKResponse))
	})
}

// VerifyCallbackSignature 按 OSS 回调规则校验请求签名：
// 对 path[?query] + "\n" + body 取 MD5 后用 RSA 验签。
func VerifyCallbackSignature(r *http.Request, body []byte, fetcher PublicKeyFetcher) error {
	if r == nil {
		return errors.New("request is nil")
	}
	if fetcher == nil {
		return errors.New("public key fetcher is nil")
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "" {
		return errors.New("authorization header is empty")
	}
	signature, err := base64.StdEncoding.DecodeString(auth)
	if err != nil {
		return fmt.Errorf("decode authorization: %w", err)
	}
	publicKeyURL, err := callbackPublicKeyURL(r)
	if err != nil {
		return err
	}
	publicKeyPEM, err := fetcher(r.Context(), publicKeyURL)
	if err != nil {
		return fmt.Errorf("fetch callback public key: %w", err)
	}
	publicKey, err := parseCallbackPublicKey(publicKeyPEM)
	if err != nil {
		return err
	}
	sum := md5.Sum([]byte(callbackStringToSign(r, body)))
	if err = rsa.VerifyPKCS1v15(publicKey, crypto.MD5, sum[:], signature); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	return nil
}

// ParseCallbackPayload 解析 JSON 或表单编码的 OSS 回调 body。
func ParseCallbackPayload(r *http.Request, body []byte) (CallbackPayload, error) {
	if r == nil {
		return CallbackPayload{}, errors.New("request is nil")
	}
	contentType := r.Header.Get("Content-Type")
	mediaType := strings.TrimSpace(contentType)
	if mediaType != "" {
		if parsed, _, err := mime.ParseMediaType(contentType); err == nil {
			mediaType = parsed
		}
	}
	payload := CallbackPayload{
		ContentType: mediaType,
		Values:      map[string]string{},
		RawBody:     append([]byte(nil), body...),
	}
	switch mediaType {
	case "application/json", "":
		if len(strings.TrimSpace(string(body))) == 0 {
			return payload, nil
		}
		var values map[string]any
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.UseNumber()
		if err := decoder.Decode(&values); err != nil {
			return CallbackPayload{}, fmt.Errorf("decode json callback body: %w", err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			if err != nil {
				return CallbackPayload{}, fmt.Errorf("decode json callback body: %w", err)
			}
			return CallbackPayload{}, errors.New("decode json callback body: multiple JSON values")
		}
		for k, v := range values {
			payload.Values[k] = callbackValueString(v)
		}
		return payload, nil
	case "application/x-www-form-urlencoded":
		values, err := url.ParseQuery(string(body))
		if err != nil {
			return CallbackPayload{}, fmt.Errorf("decode form callback body: %w", err)
		}
		for k, v := range values {
			if len(v) > 0 {
				payload.Values[k] = v[0]
			}
		}
		return payload, nil
	default:
		return CallbackPayload{}, fmt.Errorf("unsupported callback content type %q", mediaType)
	}
}

func callbackPublicKeyURL(r *http.Request) (string, error) {
	encoded := strings.TrimSpace(r.Header.Get("x-oss-pub-key-url"))
	if encoded == "" {
		return "", errors.New("x-oss-pub-key-url header is empty")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode x-oss-pub-key-url: %w", err)
	}
	return normalizeCallbackPublicKeyURL(string(raw))
}

func callbackStringToSign(r *http.Request, body []byte) string {
	path := r.URL.EscapedPath()
	if decoded, err := url.PathUnescape(path); err == nil {
		path = decoded
	}
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	return path + "\n" + string(body)
}

func parseCallbackPublicKey(publicKeyPEM []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(publicKeyPEM)
	if block == nil {
		return nil, errors.New("decode callback public key pem")
	}
	if parsed, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if publicKey, ok := parsed.(*rsa.PublicKey); ok {
			return publicKey, nil
		}
	}
	if publicKey, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return publicKey, nil
	}
	return nil, errors.New("parse callback public key")
}

func callbackValueString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(v)
	}
}

func defaultCallbackPublicKeyFetcher(ctx context.Context, publicKeyURL string) ([]byte, error) {
	publicKeyURL, err := normalizeCallbackPublicKeyURL(publicKeyURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, publicKeyURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := defaultCallbackHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch public key returned status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func readCallbackBody(body io.Reader, maxBytes int64) ([]byte, error) {
	if body == nil {
		return nil, errors.New("callback body is nil")
	}
	if maxBytes <= 0 {
		return io.ReadAll(body)
	}
	data, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errCallbackBodyTooLarge
	}
	return data, nil
}

func normalizeCallbackPublicKeyURL(publicKeyURL string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(publicKeyURL))
	if err != nil {
		return "", fmt.Errorf("parse public key url: %w", err)
	}
	if u.Hostname() != "gosspublic.alicdn.com" {
		return "", errors.New("public key url is not allowed")
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "https"
	case "https":
	default:
		return "", errors.New("public key url must use http or https")
	}
	return u.String(), nil
}
