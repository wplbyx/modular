package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

func objectURL(publicBaseURL, fallbackBaseURL, key string) string {
	baseURL := strings.TrimSpace(publicBaseURL)
	if baseURL == "" {
		baseURL = fallbackBaseURL
	}
	if baseURL == "" {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(key, "/")
}

func joinEndpointPath(endpoint, bucket, key string, forcePathStyle bool) string {
	endpoint = strings.TrimRight(endpoint, "/")
	if endpoint == "" {
		return ""
	}

	if forcePathStyle {
		return endpoint + "/" + strings.Trim(path.Join(bucket, key), "/")
	}

	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return endpoint + "/" + strings.TrimLeft(key, "/")
	}
	u.Host = bucket + "." + u.Host
	u.Path = "/" + strings.TrimLeft(key, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func normalizeEndpoint(endpoint string, disableSSL bool) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return strings.TrimRight(endpoint, "/")
	}
	scheme := "https://"
	if disableSSL {
		scheme = "http://"
	}
	return strings.TrimRight(scheme+endpoint, "/")
}

func s3DefaultURL(bucket, region, key string) string {
	if bucket == "" || region == "" {
		return ""
	}
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", bucket, region, strings.TrimLeft(key, "/"))
}

func ossDefaultURL(bucket, region, endpoint, key string, useCName bool) string {
	if endpoint != "" {
		if useCName {
			return strings.TrimRight(endpoint, "/") + "/" + strings.TrimLeft(key, "/")
		}
		return joinEndpointPath(endpoint, bucket, key, false)
	}
	if bucket == "" || region == "" {
		return ""
	}
	return fmt.Sprintf("https://%s.oss-%s.aliyuncs.com/%s", bucket, region, strings.TrimLeft(key, "/"))
}

type trackedUploadReader struct {
	reader io.Reader
	hash   hash.Hash
	size   int64
}

func newTrackedUploadReader(reader io.Reader) (*trackedUploadReader, string, error) {
	if reader == nil {
		return nil, "", fmt.Errorf("upload reader is nil")
	}

	prefix := make([]byte, 512)
	n, err := io.ReadFull(reader, prefix)
	switch {
	case err == nil:
	case errorsIsEOF(err):
	default:
		return nil, "", fmt.Errorf("read upload prefix: %w", err)
	}
	prefix = prefix[:n]

	contentType := "application/octet-stream"
	if len(prefix) > 0 {
		contentType = http.DetectContentType(prefix)
	}

	return &trackedUploadReader{
		reader: io.MultiReader(bytes.NewReader(prefix), reader),
		hash:   sha256.New(),
	}, contentType, nil
}

func (r *trackedUploadReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.size += int64(n)
		_, _ = r.hash.Write(p[:n])
	}
	return n, err
}

func (r *trackedUploadReader) Size() int64 {
	return r.size
}

func (r *trackedUploadReader) Hash() string {
	return hex.EncodeToString(r.hash.Sum(nil))
}

func errorsIsEOF(err error) bool {
	return err == io.EOF || err == io.ErrUnexpectedEOF
}
