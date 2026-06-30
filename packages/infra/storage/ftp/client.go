package ftp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jlaffaye/ftp"

	"holographic/packages/config"
	"holographic/packages/infra/storage"
)

// Ensure FTPStorage implements Storage interface
var _ storage.Storage = (*FTPStorage)(nil)

// FTPStorage implements Storage interface using FTP
type FTPStorage struct {
	client   *ftp.ServerConn
	locker   *sync.Mutex
	address  string
	username string
	password string
	domain   string
	prefix   string
}

// NewFTPStorage creates a new FTP storage instance
func NewFTPStorage(cfg *config.Ftp) (*FTPStorage, error) {
	if cfg == nil {
		return nil, errors.New("ftp config is nil")
	}

	address := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	client, err := ftp.Dial(address)
	if err != nil {
		return nil, fmt.Errorf("connect ftp server failed: %w", err)
	}

	if err = client.Login(cfg.Username, cfg.Password); err != nil {
		return nil, fmt.Errorf("login ftp server failed: %w", err)
	}

	fs := &FTPStorage{
		client:   client,
		locker:   new(sync.Mutex),
		address:  address,
		username: cfg.Username,
		password: cfg.Password,
		domain:   cfg.Domain,
		prefix:   cfg.Prefix,
	}

	return fs, nil
}

// Upload uploads a file and returns object metadata.
func (f *FTPStorage) Upload(ctx context.Context, filename string, reader io.Reader) (*storage.Object, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// Create a new connection for upload
	client, err := ftp.Dial(f.address, ftp.DialWithTimeout(30*time.Second))
	if err != nil {
		return nil, fmt.Errorf("connect ftp server failed: %w", err)
	}
	if err = client.Login(f.username, f.password); err != nil {
		return nil, fmt.Errorf("login ftp server failed: %w", err)
	}
	defer client.Logout()

	// List existing files
	entries, err := client.List(f.prefix)
	if err != nil {
		return nil, fmt.Errorf("list ftp folder failed: %w", err)
	}

	// Parse filename
	names := strings.Split(filename, ".")
	if len(names) < 2 {
		return nil, errors.New("filename error: missing extension")
	}

	// Add timestamp to avoid conflicts
	filename = fmt.Sprintf("%s_%d.%s", names[0], time.Now().UnixMicro(), names[1])

	// Check for duplicates
	for _, entry := range entries {
		if entry.Type == ftp.EntryTypeFile && entry.Name == filename {
			return nil, fmt.Errorf("upload file name duplicate: %s", filename)
		}
	}

	// Upload file
	fp := fmt.Sprintf("%s/%s", f.prefix, filename)
	h := sha256.New()
	sniffer := &contentSniffer{}
	counter := &countingReader{reader: io.TeeReader(reader, io.MultiWriter(h, sniffer))}
	if err = client.Stor(fp, counter); err != nil {
		return nil, fmt.Errorf("upload to ftp server failed: %w", err)
	}

	return &storage.Object{
		Key:         fp,
		Path:        fp,
		URL:         f.GetURL(fp),
		Hash:        hex.EncodeToString(h.Sum(nil)),
		Size:        counter.size,
		ContentType: sniffer.ContentType(),
	}, nil
}

// Download downloads a file by path
func (f *FTPStorage) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	client, err := ftp.Dial(f.address, ftp.DialWithTimeout(30*time.Second))
	if err != nil {
		return nil, fmt.Errorf("connect ftp server failed: %w", err)
	}
	if err = client.Login(f.username, f.password); err != nil {
		_ = client.Quit()
		return nil, fmt.Errorf("login ftp server failed: %w", err)
	}

	resp, err := client.Retr(path)
	if err != nil {
		_ = client.Quit()
		return nil, fmt.Errorf("download from ftp server failed: %w", err)
	}

	return &ftpReadCloser{ReadCloser: resp, client: client}, nil
}

// Delete deletes a file by path
func (f *FTPStorage) Delete(ctx context.Context, path string) error {
	client, err := ftp.Dial(f.address, ftp.DialWithTimeout(30*time.Second))
	if err != nil {
		return fmt.Errorf("connect ftp server failed: %w", err)
	}
	if err = client.Login(f.username, f.password); err != nil {
		return fmt.Errorf("login ftp server failed: %w", err)
	}
	defer client.Logout()

	return client.Delete(path)
}

// Exists checks if a file exists
func (f *FTPStorage) Exists(ctx context.Context, path string) (bool, error) {
	client, err := ftp.Dial(f.address, ftp.DialWithTimeout(30*time.Second))
	if err != nil {
		return false, fmt.Errorf("connect ftp server failed: %w", err)
	}
	if err = client.Login(f.username, f.password); err != nil {
		return false, fmt.Errorf("login ftp server failed: %w", err)
	}
	defer client.Logout()

	// Get the directory and filename
	dir := f.prefix
	filename := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		dir = path[:idx]
		filename = path[idx+1:]
	}

	entries, err := client.List(dir)
	if err != nil {
		return false, err
	}

	for _, entry := range entries {
		if entry.Name == filename {
			return true, nil
		}
	}

	return false, nil
}

// GetURL returns the public URL for a path
func (f *FTPStorage) GetURL(path string) string {
	return fmt.Sprintf("%s%s", f.domain, path)
}

type countingReader struct {
	reader io.Reader
	size   int64
}

type ftpReadCloser struct {
	io.ReadCloser
	client *ftp.ServerConn
}

func (r *ftpReadCloser) Close() error {
	var joined error
	if r.ReadCloser != nil {
		joined = errors.Join(joined, r.ReadCloser.Close())
	}
	if r.client != nil {
		joined = errors.Join(joined, r.client.Quit())
	}
	return joined
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.size += int64(n)
	return n, err
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
