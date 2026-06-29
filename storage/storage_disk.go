package aliyun_oss

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"luffa_micro_services/pkg/logger"
)

// 编译期接口断言：确保 DiskStorage 完整实现 Storage 接口
var _ Storage = (*DiskStorage)(nil)

var diskStorageInstance *DiskStorage

// DiskStorage 是 Storage 接口的本地磁盘实现，跨平台兼容（Linux/Unix/Windows）。
// key 统一为 URL 风格的相对路径（用 "/" 分隔），内部由 filepath 转换为平台路径。
// Meta/ContentType 在磁盘实现中不持久化（opts 中传入会被静默忽略）。
type DiskStorage struct {
	rootDir string // 存储根目录的绝对路径
	baseUrl string // 访问域名（已剥离协议前缀和尾斜杠）
}

// NewDiskStorage 构造一个新的本地磁盘 Storage 实例
func NewDiskStorage(config *LocalDiskStorage) (*DiskStorage, error) {
	if config == nil {
		return nil, errors.New("local disk storage config is nil")
	}
	if config.RootDir == "" {
		return nil, errors.New("LocalDiskStorage.RootDir is empty")
	}

	rootDir, err := filepath.Abs(config.RootDir)
	if err != nil {
		return nil, fmt.Errorf("resolve root dir: %w", err)
	}
	if err = os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("create root dir: %w", err)
	}

	baseUrl := config.BaseUrl
	baseUrl = strings.TrimPrefix(baseUrl, "https://")
	baseUrl = strings.TrimPrefix(baseUrl, "http://")
	baseUrl = strings.TrimRight(baseUrl, "/")

	return &DiskStorage{
		rootDir: rootDir,
		baseUrl: baseUrl,
	}, nil
}

// InitDiskStorage 初始化全局单例
func InitDiskStorage(config *LocalDiskStorage) (*DiskStorage, error) {
	s, err := NewDiskStorage(config)
	if err != nil {
		return nil, err
	}
	diskStorageInstance = s
	logger.Infof("init local disk storage done, root: %s", s.rootDir)
	return s, nil
}

// GetDiskStorage 获取全局单例
func GetDiskStorage() *DiskStorage {
	return diskStorageInstance
}

// safeFilePath 将相对 key 转为安全的本地路径，防止路径穿越（如 key 含 "../"）。
// key 使用 "/" 作为分隔符（URL 风格），内部通过 filepath.FromSlash 转为平台分隔符。
func (s *DiskStorage) safeFilePath(key string) (string, error) {
	if key == "" {
		return "", errors.New("key is empty")
	}
	full := filepath.Join(s.rootDir, filepath.FromSlash(key))
	rel, err := filepath.Rel(s.rootDir, full)
	if err != nil {
		return "", fmt.Errorf("invalid key %q: %w", key, err)
	}
	// rel 若以 ".." 开头说明路径逃逸出 rootDir
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("invalid key %q: escapes storage root", key)
	}
	return full, nil
}

// multipartDir 返回指定 uploadID 的分片临时目录（位于系统临时目录下，不污染 rootDir）
func (s *DiskStorage) multipartDir(uploadID string) string {
	return filepath.Join(os.TempDir(), "aliyun_oss_disk", uploadID)
}

// GetUsefulUrl 生成可直接访问的完整 URL：baseUrl + "/" + key
func (s *DiskStorage) GetUsefulUrl(key string) string {
	if key == "" {
		return ""
	}
	return s.baseUrl + "/" + strings.TrimLeft(key, "/")
}

// Exists 检查文件是否存在
func (s *DiskStorage) Exists(ctx context.Context, key string) (bool, error) {
	p, err := s.safeFilePath(key)
	if err != nil {
		return false, err
	}
	if _, err = os.Stat(p); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, err
	}
}

// Upload 上传单个文件（opts 中的 Meta/ContentType 在磁盘实现中被忽略）
func (s *DiskStorage) Upload(ctx context.Context, key string, body io.Reader, opts ...IOOption) error {
	p, err := s.safeFilePath(key)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, body)
	return err
}

// Delete 删除单个文件
func (s *DiskStorage) Delete(ctx context.Context, key string, opts ...IOOption) error {
	p, err := s.safeFilePath(key)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// Download 下载单个文件，调用方需关闭返回的 io.ReadCloser
func (s *DiskStorage) Download(ctx context.Context, key string, opts ...IOOption) (io.ReadCloser, error) {
	p, err := s.safeFilePath(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// Stat 获取单个文件的元信息
func (s *DiskStorage) Stat(ctx context.Context, key string) (ObjectItem, error) {
	p, err := s.safeFilePath(key)
	if err != nil {
		return ObjectItem{}, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return ObjectItem{}, err
	}
	return ObjectItem{
		Key:          key,
		Size:         info.Size(),
		LastModified: info.ModTime().Unix(),
	}, nil
}

// BatchUpload 批量上传，errgroup 控制并发，全跑完后聚合错误
func (s *DiskStorage) BatchUpload(ctx context.Context, tasks []UploadTask, opts ...IOOption) error {
	if len(tasks) == 0 {
		return nil
	}
	o := applyIOOptions(opts)
	concurrency := o.ConcurrentNum
	if concurrency <= 0 {
		concurrency = 5
	}
	eg := new(errgroup.Group)
	eg.SetLimit(concurrency)
	var (
		mu   sync.Mutex
		errs []error
	)
	for _, task := range tasks {
		eg.Go(func() error {
			if err := s.Upload(ctx, task.Key, task.Body, opts...); err != nil {
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
func (s *DiskStorage) BatchDelete(ctx context.Context, keys []string, opts ...IOOption) ([]string, error) {
	var (
		deleted []string
		errs    []error
	)
	for _, key := range keys {
		if key == "" {
			continue
		}
		p, err := s.safeFilePath(key)
		if err != nil {
			errs = append(errs, fmt.Errorf("delete %s: %w", key, err))
			continue
		}
		if err = os.Remove(p); err != nil {
			if os.IsNotExist(err) {
				continue // 幂等，不存在的文件不计入失败
			}
			errs = append(errs, fmt.Errorf("delete %s: %w", key, err))
			continue
		}
		deleted = append(deleted, key)
	}
	return deleted, errors.Join(errs...)
}

// DeleteByPrefix 按前缀删除所有文件（遍历 + 分批删除，内存峰值受控）
func (s *DiskStorage) DeleteByPrefix(ctx context.Context, prefix string, opts ...IOOption) error {
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
	err := s.PrefixIterator(ctx, prefix, func(ctx context.Context, items ...ObjectItem) error {
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
func (s *DiskStorage) PrefixIterator(ctx context.Context, prefix string, callback ListCallback) error {
	walkRoot := filepath.Join(s.rootDir, filepath.FromSlash(prefix))
	info, err := os.Stat(walkRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 前缀目录不存在，无文件可遍历
		}
		return err
	}
	// prefix 指向单个文件时，回调它自身
	if !info.IsDir() {
		rel, _ := filepath.Rel(s.rootDir, walkRoot)
		return callback(ctx, ObjectItem{
			Key:          filepath.ToSlash(rel),
			Size:         info.Size(),
			LastModified: info.ModTime().Unix(),
		})
	}

	const batchSize = 1000
	var batch []ObjectItem
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := callback(ctx, batch...); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}

	err = filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(s.rootDir, path)
		if err != nil {
			return err
		}
		batch = append(batch, ObjectItem{
			Key:          filepath.ToSlash(rel), // 统一用 "/" 输出 key
			Size:         fi.Size(),
			LastModified: fi.ModTime().Unix(),
		})
		if len(batch) >= batchSize {
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
func (s *DiskStorage) InitiateMultipartUpload(ctx context.Context, key string) (MultipartUploadSession, error) {
	if _, err := s.safeFilePath(key); err != nil {
		return MultipartUploadSession{}, err
	}
	uploadID := uuid.NewString()
	if err := os.MkdirAll(s.multipartDir(uploadID), 0o755); err != nil {
		return MultipartUploadSession{}, fmt.Errorf("create multipart temp dir: %w", err)
	}
	return MultipartUploadSession{UploadID: uploadID, Key: key}, nil
}

// MultipartUpload 上传单个分片到临时目录，返回 ETag（分片内容的 MD5）
func (s *DiskStorage) MultipartUpload(ctx context.Context, session MultipartUploadSession, partNumber int, partSize int64, body io.Reader) (UploadPartResponse, error) {
	if partNumber < 1 {
		return UploadPartResponse{}, errors.New("partNumber must be >= 1")
	}
	partPath := filepath.Join(s.multipartDir(session.UploadID), fmt.Sprintf("part_%d", partNumber))
	f, err := os.Create(partPath)
	if err != nil {
		return UploadPartResponse{}, err
	}
	defer f.Close()
	h := md5.New()
	if _, err = io.Copy(io.MultiWriter(f, h), body); err != nil {
		return UploadPartResponse{}, err
	}
	return UploadPartResponse{
		PartNumber: partNumber,
		ETag:       hex.EncodeToString(h.Sum(nil)),
	}, nil
}

// CompleteMultipartUpload 按 PartNumber 升序合并所有分片到最终路径，清理临时目录
func (s *DiskStorage) CompleteMultipartUpload(ctx context.Context, session MultipartUploadSession, parts []UploadPartResponse, opts ...IOOption) error {
	if len(parts) == 0 {
		return errors.New("no parts to complete")
	}
	// 防御性排序，确保按 PartNumber 升序合并（接口要求调用方升序传入）
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })

	targetPath, err := s.safeFilePath(session.Key)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return err
	}
	dst, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer dst.Close()

	mpDir := s.multipartDir(session.UploadID)
	for _, p := range parts {
		partPath := filepath.Join(mpDir, fmt.Sprintf("part_%d", p.PartNumber))
		f, err := os.Open(partPath)
		if err != nil {
			return fmt.Errorf("open part %d: %w", p.PartNumber, err)
		}
		if _, err = io.Copy(dst, f); err != nil {
			f.Close()
			return fmt.Errorf("merge part %d: %w", p.PartNumber, err)
		}
		f.Close()
	}
	// 合并成功，清理临时目录
	_ = os.RemoveAll(mpDir)
	return nil
}

// CancelMultipartUpload 取消分片上传，删除临时分片目录
func (s *DiskStorage) CancelMultipartUpload(ctx context.Context, session MultipartUploadSession) error {
	return os.RemoveAll(s.multipartDir(session.UploadID))
}
