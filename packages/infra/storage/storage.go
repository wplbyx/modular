package storage

import (
	"context"
	"fmt"
	"io"

	"modular/packages/config"
)

// Storage is the interface for file storage operations
type Storage interface {
	// Upload uploads a file and returns object metadata.
	Upload(ctx context.Context, key string, reader io.Reader) (*Object, error)

	// Download downloads a file by path
	Download(ctx context.Context, path string) (io.ReadCloser, error)

	// Delete deletes a file by path
	Delete(ctx context.Context, path string) error

	// Exists checks if a file exists
	Exists(ctx context.Context, path string) (bool, error)

	// GetURL returns the public URL for a path
	GetURL(path string) string
}

// Object contains metadata for a stored file.
type Object struct {
	Key         string `json:"key"`
	Path        string `json:"path"`
	URL         string `json:"url"`
	Hash        string `json:"hash"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

// NewStorage creates a storage backend from config.
func NewStorage(cfg *config.Storage) (Storage, error) {
	if cfg == nil {
		return nil, fmt.Errorf("storage config is nil")
	}

	switch cfg.Type {
	case "", "local":
		return NewLocalStorage(cfg)
	case "s3":
		return NewS3Storage(cfg)
	case "minio":
		return NewMinIOStorage(cfg)
	case "oss":
		return NewOSSStorage(cfg)
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", cfg.Type)
	}
}

// FileStorage is an extended interface with additional operations
type FileStorage interface {
	Storage

	// List lists files in a directory
	List(ctx context.Context, prefix string) ([]FileInfo, error)

	// Move moves a file from one path to another
	Move(ctx context.Context, src, dst string) error

	// Copy copies a file from one path to another
	Copy(ctx context.Context, src, dst string) error
}

// FileInfo contains information about a file
type FileInfo struct {
	Name         string
	Size         int64
	LastModified int64
	IsDir        bool
}

// UploadOptions contains options for file upload
type UploadOptions struct {
	ContentType string
	Metadata    map[string]string
	ACL         string // access control (public-read, private, etc.)
}
