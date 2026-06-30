package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"modular/packages/config"
)

var _ Storage = (*LocalStorage)(nil)

// LocalStorage stores files on the local filesystem and exposes URLs via a static HTTP path.
type LocalStorage struct {
	rootPath      string
	urlPath       string
	publicBaseURL string
	perm          os.FileMode
}

// NewLocalStorage creates a local filesystem storage backend.
func NewLocalStorage(cfg *config.Storage) (*LocalStorage, error) {
	if cfg == nil {
		return nil, fmt.Errorf("storage config is nil")
	}
	if cfg.Local == nil {
		return nil, fmt.Errorf("local storage config is nil")
	}

	rootPath := cfg.Local.RootPath
	if rootPath == "" {
		rootPath = "./uploads"
	}
	urlPath := cfg.Local.URLPath
	if urlPath == "" {
		urlPath = "/uploads"
	}
	perm := cfg.Local.Perm
	if perm == 0 {
		perm = 0644
	}

	return &LocalStorage{
		rootPath:      rootPath,
		urlPath:       urlPath,
		publicBaseURL: cfg.PublicBaseURL,
		perm:          perm,
	}, nil
}

// Upload saves a file under key and returns object metadata.
func (s *LocalStorage) Upload(ctx context.Context, key string, reader io.Reader) (*Object, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	cleanKey, err := cleanObjectKey(key)
	if err != nil {
		return nil, err
	}

	fullPath := filepath.Join(s.rootPath, filepath.FromSlash(cleanKey))
	if err = os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, s.perm)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	h := sha256.New()
	sniffer := &contentSniffer{}
	w := io.MultiWriter(file, h, sniffer)
	size, err := io.Copy(w, reader)
	if err != nil {
		_ = os.Remove(fullPath)
		return nil, err
	}

	objectPath := s.pathForKey(cleanKey)
	return &Object{
		Key:         cleanKey,
		Path:        objectPath,
		URL:         s.GetURL(cleanKey),
		Hash:        hex.EncodeToString(h.Sum(nil)),
		Size:        size,
		ContentType: sniffer.ContentType(),
	}, nil
}

// Download opens a local file by key.
func (s *LocalStorage) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	cleanKey, err := cleanObjectKey(key)
	if err != nil {
		return nil, err
	}
	return os.Open(filepath.Join(s.rootPath, filepath.FromSlash(cleanKey)))
}

// Delete deletes a local file by key.
func (s *LocalStorage) Delete(ctx context.Context, key string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	cleanKey, err := cleanObjectKey(key)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(s.rootPath, filepath.FromSlash(cleanKey)))
}

// Exists checks whether a local file exists by key.
func (s *LocalStorage) Exists(ctx context.Context, key string) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}

	cleanKey, err := cleanObjectKey(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(filepath.Join(s.rootPath, filepath.FromSlash(cleanKey)))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// GetURL returns the public URL for a key.
func (s *LocalStorage) GetURL(key string) string {
	cleanKey, err := cleanObjectKey(key)
	if err != nil {
		return ""
	}
	return strings.TrimRight(s.publicBaseURL, "/") + s.pathForKey(cleanKey)
}

func (s *LocalStorage) pathForKey(key string) string {
	return "/" + strings.Trim(path.Join(strings.Trim(s.urlPath, "/"), key), "/")
}

func cleanObjectKey(key string) (string, error) {
	key = strings.TrimSpace(strings.ReplaceAll(key, "\\", "/"))
	key = strings.TrimLeft(key, "/")
	if key == "" {
		return "", fmt.Errorf("storage key is empty")
	}

	cleanKey := path.Clean(key)
	if cleanKey == "." || cleanKey == ".." || strings.HasPrefix(cleanKey, "../") {
		return "", fmt.Errorf("invalid storage key: %s", key)
	}
	return cleanKey, nil
}

type contentSniffer struct {
	buf []byte
}

func (s *contentSniffer) Write(p []byte) (int, error) {
	if len(s.buf) < 512 {
		remaining := 512 - len(s.buf)
		if len(p) < remaining {
			remaining = len(p)
		}
		s.buf = append(s.buf, p[:remaining]...)
	}
	return len(p), nil
}

func (s *contentSniffer) ContentType() string {
	if len(s.buf) == 0 {
		return "application/octet-stream"
	}
	return http.DetectContentType(s.buf)
}
