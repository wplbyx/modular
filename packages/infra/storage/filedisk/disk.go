package filedisk

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/wplbyx/modular/packages/config/configitem"
	"golang.org/x/sync/errgroup"

	"github.com/wplbyx/modular/packages/infra/storage"
)

// 编译期接口断言：确保 DiskStorage 完整实现 Storage 接口
var _ storage.Storage = (*DiskStorage)(nil)

// DiskStorage 是 Storage 接口的本地磁盘实现，跨平台兼容（Linux/Unix/Windows）。
// key 统一为 URL 风格的相对路径（用 "/" 分隔），内部由 filepath 转换为平台路径。
// Meta/ContentType 在磁盘实现中不持久化（opts 中传入会被静默忽略）。
type DiskStorage struct {
	multipartMu sync.Mutex
	rootDir     string // 存储根目录的绝对路径
	baseUrl     string // 访问域名（已剥离协议前缀和尾斜杠）
}

// NewDiskStorage 构造一个新的本地磁盘 Storage 实例。
func NewDiskStorage(cfg *configitem.Storage) (*DiskStorage, error) {
	if cfg == nil || cfg.Disk == nil {
		return nil, errors.New("disk storage config is nil")
	}
	if cfg.Disk.RootDir == "" {
		return nil, errors.New("DiskStorageConfig.RootDir is empty")
	}

	rootDir, err := filepath.Abs(cfg.Disk.RootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve root dir: %w", err)
	}
	if err = os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("create root dir: %w", err)
	}

	baseUrl := cfg.Disk.BaseUrl
	baseUrl = strings.TrimPrefix(baseUrl, "https://")
	baseUrl = strings.TrimPrefix(baseUrl, "http://")
	baseUrl = strings.TrimRight(baseUrl, "/")

	return &DiskStorage{rootDir: rootDir, baseUrl: baseUrl}, nil
}

// GetUsefulUrl 生成可直接访问的完整 URL：baseUrl + "/" + key
func (s *DiskStorage) GetUrl(key string) string {
	if key == "" {
		return ""
	}
	return s.baseUrl + "/" + strings.TrimLeft(key, "/")
}

// GetMeta 获取单个文件的元信息
func (s *DiskStorage) GetMeta(ctx context.Context, key string) (storage.ObjectItem, error) {
	root, name, err := s.objectRoot(ctx, key)
	if err != nil {
		return storage.ObjectItem{}, err
	}
	defer root.Close()
	info, err := root.Stat(name)
	if err != nil {
		return storage.ObjectItem{}, err
	}
	return storage.ObjectItem{Key: key, Size: info.Size(), LastModified: info.ModTime().Unix()}, nil
}

// Exists 检查文件是否存在
func (s *DiskStorage) Exists(ctx context.Context, key string) (bool, error) {
	root, name, err := s.objectRoot(ctx, key)
	if err != nil {
		return false, err
	}
	defer root.Close()
	_, err = root.Stat(name)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

// Upload 上传单个文件（opts 中的 Meta/ContentType 在磁盘实现中被忽略）
func (s *DiskStorage) Upload(ctx context.Context, key string, body io.Reader, opts ...storage.IOConfigOptionFunc) error {
	root, name, err := s.objectRoot(ctx, key)
	if err != nil {
		return err
	}
	defer root.Close()
	return atomicWrite(ctx, root, name, body)
}

// Delete 删除单个文件
func (s *DiskStorage) Delete(ctx context.Context, key string, opts ...storage.IOConfigOptionFunc) error {
	root, name, err := s.objectRoot(ctx, key)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.Remove(name)
}

// Download 下载单个文件，调用方需关闭返回的 io.ReadCloser
func (s *DiskStorage) Download(ctx context.Context, key string, opts ...storage.IOConfigOptionFunc) (io.ReadCloser, error) {
	root, name, err := s.objectRoot(ctx, key)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.Open(name)
}

// BatchUpload 批量上传，errgroup 控制并发，全跑完后聚合错误
func (s *DiskStorage) BatchUpload(ctx context.Context, tasks []storage.UploadTask, options ...storage.IOConfigOptionFunc) error {
	if len(tasks) == 0 {
		return nil
	}

	option := storage.ApplyIOOptions(options)
	concurrency := option.ConcurrentNum
	if concurrency <= 0 {
		concurrency = 5
	}

	errs := make([]error, 0, len(tasks))
	mu := sync.Mutex{}
	eg := new(errgroup.Group)
	eg.SetLimit(concurrency)
	for _, task := range tasks {
		eg.Go(func() error {
			if err := s.Upload(ctx, task.Key, task.Body, options...); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("upload %s: %w", task.Key, err))
				mu.Unlock()
			}
			return nil // 返回 nil，保证所有任务都跑完后再聚合错误
		})
	}
	_ = eg.Wait()
	return errors.Join(errs...)
}

// BatchDelete 批量删除，返回成功删除的 key 列表（不存在的 key 视为已删除，跳过不报错）
func (s *DiskStorage) BatchDelete(ctx context.Context, keys []string, opts ...storage.IOConfigOptionFunc) ([]string, error) {
	var deleted []string
	var failures []error
	for _, key := range keys {
		if key == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return deleted, errors.Join(append(failures, err)...)
		}
		if err := s.Delete(ctx, key, opts...); err != nil {
			if !os.IsNotExist(err) {
				failures = append(failures, fmt.Errorf("delete %s: %w", key, err))
			}
			continue
		}
		deleted = append(deleted, key)
	}
	return deleted, errors.Join(failures...)
}

// DeleteByPrefix 按前缀删除所有文件（遍历 + 分批删除，内存峰值受控）
func (s *DiskStorage) DeleteByPrefix(ctx context.Context, prefix string, opts ...storage.IOConfigOptionFunc) error {
	if prefix == "" {
		return errors.New("DeleteByPrefix: prefix must not be empty")
	}

	const deleteBatch = 1000
	var batch []string
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		_, err := s.BatchDelete(ctx, batch, opts...)
		batch = batch[:0]
		return err
	}

	err := s.PrefixIterator(ctx, prefix, func(ctx context.Context, items ...storage.ObjectItem) error {
		for _, item := range items {
			batch = append(batch, item.Key)
			if len(batch) >= deleteBatch {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

// PrefixIterator 迭代遍历指定前缀目录下的所有文件，分页流式回调
func (s *DiskStorage) PrefixIterator(ctx context.Context, prefix string, callback storage.ListCallback) error {
	if callback == nil {
		return errors.New("list callback is nil")
	}
	if prefix == "" {
		prefix = "."
	}
	root, name, err := s.objectRoot(ctx, prefix)
	if err != nil {
		return err
	}
	defer root.Close()
	var batch []storage.ObjectItem
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := callback(ctx, batch...)
		batch = nil
		return err
	}
	err = fs.WalkDir(root.FS(), filepath.ToSlash(name), func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if os.IsNotExist(walkErr) && path == filepath.ToSlash(name) {
				return nil
			}
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		info, err := root.Stat(filepath.FromSlash(path))
		if err != nil {
			return err
		}
		batch = append(batch, storage.ObjectItem{Key: path, Size: info.Size(), LastModified: info.ModTime().Unix()})
		if len(batch) >= 1000 {
			return flush()
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

// InitiateMultipartUpload 初始化分片上传（用 UUID 生成 uploadID，创建临时目录）
func (s *DiskStorage) InitiateMultipartUpload(ctx context.Context, key string) (storage.MultipartUploadSession, error) {
	root, _, err := s.objectRoot(ctx, key)
	if err != nil {
		return storage.MultipartUploadSession{}, err
	}
	root.Close()
	session := storage.MultipartUploadSession{UploadID: uuid.NewString(), Key: key}
	base, err := s.multipartRoot()
	if err != nil {
		return storage.MultipartUploadSession{}, err
	}
	defer base.Close()
	if err = base.Mkdir(session.UploadID, 0700); err != nil {
		return storage.MultipartUploadSession{}, err
	}
	data, _ := json.Marshal(multipartBinding{Root: s.rootDir, Key: key})
	if err = base.WriteFile(filepath.Join(session.UploadID, "session.json"), data, 0600); err != nil {
		_ = base.RemoveAll(session.UploadID)
		return storage.MultipartUploadSession{}, err
	}
	return session, nil
}

// CompleteMultipartUpload 按 PartNumber 升序合并所有分片到最终路径，清理临时目录
func (s *DiskStorage) CompleteMultipartUpload(ctx context.Context, session storage.MultipartUploadSession, parts []storage.UploadPartResponse, opts ...storage.IOConfigOptionFunc) error {
	s.multipartMu.Lock()
	defer s.multipartMu.Unlock()
	if len(parts) == 0 {
		return errors.New("no parts to complete")
	}
	base, err := s.validateSession(session, false)
	if err != nil {
		return err
	}
	defer base.Close()
	ordered := append([]storage.UploadPartResponse(nil), parts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].PartNumber < ordered[j].PartNumber })
	for i, part := range ordered {
		if part.PartNumber < 1 || (i > 0 && part.PartNumber == ordered[i-1].PartNumber) {
			return errors.New("invalid or duplicate part number")
		}
	}
	reader := &partSequenceReader{root: base, uploadID: session.UploadID, parts: ordered}
	defer reader.Close()
	if err = s.Upload(ctx, session.Key, reader, opts...); err != nil {
		return err
	}
	if err = reader.Close(); err != nil {
		return err
	}

	return base.RemoveAll(session.UploadID)
}

// CancelMultipartUpload 取消分片上传，删除临时分片目录
func (s *DiskStorage) CancelMultipartUpload(ctx context.Context, session storage.MultipartUploadSession) error {
	s.multipartMu.Lock()
	defer s.multipartMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	base, err := s.validateSession(session, true)
	if err != nil {
		return err
	}
	defer base.Close()
	return base.RemoveAll(session.UploadID)
}

// MultipartUpload 上传单个分片到临时目录，返回 ETag（分片内容的 MD5）
func (s *DiskStorage) MultipartUpload(ctx context.Context, session storage.MultipartUploadSession, partNumber int, partSize int64, body io.Reader) (storage.UploadPartResponse, error) {
	s.multipartMu.Lock()
	defer s.multipartMu.Unlock()
	if partNumber < 1 {
		return storage.UploadPartResponse{}, errors.New("partNumber must be >= 1")
	}
	base, err := s.validateSession(session, false)
	if err != nil {
		return storage.UploadPartResponse{}, err
	}
	defer base.Close()
	h := md5.New()
	if err = atomicWrite(ctx, base, filepath.Join(session.UploadID, fmt.Sprintf("part_%d", partNumber)), io.TeeReader(body, h)); err != nil {
		return storage.UploadPartResponse{}, err
	}
	return storage.UploadPartResponse{PartNumber: partNumber, ETag: hex.EncodeToString(h.Sum(nil))}, nil
}

// GenKeyToFilePath 将相对 key 转为安全的本地路径，防止路径穿越（如 key 含 "../"）。
// key 使用 "/" 作为分隔符（URL 风格），内部通过 filepath.FromSlash 转为平台分隔符。
func (s *DiskStorage) GenKeyToFilePath(key string) (string, error) {
	if key == "" {
		return "", errors.New("key is empty")
	}

	full := filepath.Join(s.rootDir, filepath.FromSlash(key))
	rel, err := filepath.Rel(s.rootDir, full)
	if err != nil {
		return "", fmt.Errorf("invalid key %q: %w", key, err)
	}
	// rel 若以 ".." 开头说明路径逃逸出 rootDir
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid key %q: escapes storage root", key)
	}
	return full, nil
}

// MultipartTempDir 返回指定 uploadID 的分片临时目录（位于系统临时目录下，不污染 rootDir）
func (s *DiskStorage) MultipartTempDir(uploadID string) string {
	return filepath.Join(os.TempDir(), "upload_temp_dir", uploadID)
}

// objectRoot 将每次文件操作约束在存储根目录，禁止符号链接逃逸。
func (s *DiskStorage) objectRoot(ctx context.Context, key string) (*os.Root, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	path, err := s.GenKeyToFilePath(key)
	if err != nil {
		return nil, "", err
	}
	name, err := filepath.Rel(s.rootDir, path)
	if err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(s.rootDir)
	return root, name, err
}

type multipartBinding struct {
	Root string
	Key  string
}

func (s *DiskStorage) multipartRoot() (*os.Root, error) {
	dir := filepath.Join(os.TempDir(), "upload_temp_dir")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	return os.OpenRoot(dir)
}
func (s *DiskStorage) validateSession(session storage.MultipartUploadSession, allowMissing bool) (*os.Root, error) {
	id, err := uuid.Parse(session.UploadID)
	if err != nil || id.String() != session.UploadID {
		return nil, errors.New("invalid multipart upload ID")
	}
	if _, err = s.GenKeyToFilePath(session.Key); err != nil {
		return nil, err
	}
	base, err := s.multipartRoot()
	if err != nil {
		return nil, err
	}
	data, err := base.ReadFile(filepath.Join(session.UploadID, "session.json"))
	if allowMissing && os.IsNotExist(err) {
		return base, nil
	}
	var binding multipartBinding
	if err == nil {
		err = json.Unmarshal(data, &binding)
	}
	if err == nil && (binding.Root != s.rootDir || binding.Key != session.Key) {
		err = errors.New("multipart session belongs to another storage or key")
	}
	if err != nil {
		base.Close()
		return nil, err
	}
	return base, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
func atomicWrite(ctx context.Context, root *os.Root, name string, body io.Reader) error {
	if body == nil {
		return errors.New("upload body is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.MkdirAll(filepath.Dir(name), 0755); err != nil {
		return err
	}
	temporary := filepath.Join(filepath.Dir(name), ".modular-"+uuid.NewString()+".tmp")
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, copyErr := io.Copy(file, contextReader{ctx, body})
	closeErr := file.Close()
	if err = errors.Join(copyErr, closeErr, ctx.Err()); err != nil {
		return err
	}
	return root.Rename(temporary, name)
}

// partSequenceReader opens one part at a time, validating its hash before advancing.
type partSequenceReader struct {
	root     *os.Root
	uploadID string
	parts    []storage.UploadPartResponse
	index    int
	file     *os.File
	hash     hash.Hash
}

func (r *partSequenceReader) Read(buffer []byte) (int, error) {
	for r.index < len(r.parts) {
		part := r.parts[r.index]
		if r.file == nil {
			file, err := r.root.Open(filepath.Join(r.uploadID, fmt.Sprintf("part_%d", part.PartNumber)))
			if err != nil {
				return 0, err
			}
			r.file = file
			r.hash = md5.New()
		}
		n, err := r.file.Read(buffer)
		_, _ = r.hash.Write(buffer[:n])
		if err == io.EOF {
			closeErr := r.file.Close()
			r.file = nil
			if closeErr != nil {
				return n, closeErr
			}
			if part.ETag != "" && part.ETag != hex.EncodeToString(r.hash.Sum(nil)) {
				return n, errors.New("part ETag mismatch")
			}
			r.index++
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
	return 0, io.EOF
}
func (r *partSequenceReader) Close() error {
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}
