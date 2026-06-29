package aliyun_oss

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"golang.org/x/sync/errgroup"

	"luffa_micro_services/app/pkg/options"
	"luffa_micro_services/pkg/logger"
)

// 编译期接口断言：确保 OssStorage 完整实现 Storage 接口
var _ Storage = (*OssStorage)(nil)

var ossStorageInstance *OssStorage

// OssStorage 是 Storage 接口的阿里云 OSS 实现。
// key 统一为"相对路径"，内部由 baseDir 拼接成 OSS objectKey；
// GetUsefulUrl 输出可直接访问的完整 URL（https:// + baseUrl + baseDir + key）。
type OssStorage struct {
	baseUrl string      // 访问域名（不带协议和首尾斜杠）
	baseDir string      // OSS 对象前缀（不带首尾斜杠）
	bucket  *oss.Bucket // oss Bucket 对象
}

// NewOSSStorage 构造一个新的 OSS Storage 实例
func NewOSSStorage(config *options.AliyunOSS) (*OssStorage, error) {
	if config == nil {
		return nil, errors.New("aliyun oss config is nil")
	}
	timeoutSeconds := 30
	if config.Timeout > 0 {
		timeoutSeconds = config.Timeout
	}
	timeoutDuration := time.Duration(timeoutSeconds) * time.Second

	client, err := oss.New(config.Endpoint, config.AccessKeyID, config.AccessKeySecret,
		oss.Timeout(int64(timeoutSeconds), int64(timeoutSeconds)),
		oss.HTTPClient(&http.Client{Timeout: timeoutDuration}),
	)
	if err != nil {
		return nil, fmt.Errorf("create oss client: %w", err)
	}
	bucket, err := client.Bucket(config.BucketName)
	if err != nil {
		return nil, fmt.Errorf("get oss bucket: %w", err)
	}

	// 规整 baseUrl：剥离协议前缀和首尾斜杠
	baseUrl := config.Domain
	baseUrl = strings.TrimPrefix(baseUrl, "https://")
	baseUrl = strings.TrimPrefix(baseUrl, "http://")
	baseUrl = strings.Trim(baseUrl, "/")

	return &OssStorage{
		baseDir: strings.Trim(config.BaseDir, "/"),
		baseUrl: baseUrl,
		bucket:  bucket,
	}, nil
}

// InitOSSStorage 初始化全局单例
func InitOSSStorage(config *options.AliyunOSS) (*OssStorage, error) {
	s, err := NewOSSStorage(config)
	if err != nil {
		return nil, err
	}
	ossStorageInstance = s
	logger.Infof("init aliyun oss storage done")
	return s, nil
}

// GetOSSStorage 获取全局单例
func GetOSSStorage() *OssStorage {
	return ossStorageInstance
}

// buildObjectKey 将相对 key 拼接为 OSS 内部完整 objectKey
func (s *OssStorage) buildObjectKey(key string) string {
	key = strings.TrimPrefix(key, "/")
	if s.baseDir != "" {
		return s.baseDir + "/" + key
	}
	return key
}

// applyIOOptions 应用可选参数
func applyIOOptions(opts []IOOption) *IOOptions {
	o := &IOOptions{}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// toOSSOptions 将 IOOptions 转换为 oss.Option
func (s *OssStorage) toOSSOptions(o *IOOptions) []oss.Option {
	var options []oss.Option
	if o.ContentType != "" {
		options = append(options, oss.ContentType(o.ContentType))
	}
	for k, v := range o.Meta {
		options = append(options, oss.Meta(k, v))
	}
	if o.VersionID != "" {
		options = append(options, oss.VersionId(o.VersionID))
	}
	return options
}

// GetUsefulUrl 生成可直接访问的完整 URL
func (s *OssStorage) GetUsefulUrl(key string) string {
	if key == "" {
		return ""
	}
	return "https://" + s.baseUrl + "/" + s.buildObjectKey(key)
}

// Exists 检查文件是否存在
func (s *OssStorage) Exists(ctx context.Context, key string) (bool, error) {
	return s.bucket.IsObjectExist(s.buildObjectKey(key))
}

// Upload 上传单个文件
func (s *OssStorage) Upload(ctx context.Context, key string, body io.Reader, opts ...IOOption) error {
	o := applyIOOptions(opts)
	return s.bucket.PutObject(s.buildObjectKey(key), body, s.toOSSOptions(o)...)
}

// Delete 删除单个文件
func (s *OssStorage) Delete(ctx context.Context, key string, opts ...IOOption) error {
	return s.bucket.DeleteObject(s.buildObjectKey(key))
}

// Download 下载单个文件，调用方需在使用完毕后关闭返回的 io.ReadCloser
func (s *OssStorage) Download(ctx context.Context, key string, opts ...IOOption) (io.ReadCloser, error) {
	o := applyIOOptions(opts)
	return s.bucket.GetObject(s.buildObjectKey(key), s.toOSSOptions(o)...)
}

// Stat 获取单个文件的元信息
func (s *OssStorage) Stat(ctx context.Context, key string) (ObjectItem, error) {
	headers, err := s.bucket.GetObjectMeta(s.buildObjectKey(key))
	if err != nil {
		return ObjectItem{}, err
	}
	item := ObjectItem{Key: key}
	if v := headers.Get("Content-Length"); v != "" {
		if size, err := strconv.ParseInt(v, 10, 64); err == nil {
			item.Size = size
		}
	}
	if v := headers.Get("Last-Modified"); v != "" {
		if t, err := time.Parse(time.RFC1123, v); err == nil {
			item.LastModified = t.Unix()
		}
	}
	return item, nil
}

// BatchUpload 批量上传。使用 errgroup 控制并发，所有任务全部执行完毕后聚合错误返回。
// 闭包返回 nil（错误经 Mutex 收集），确保 errgroup 不会因首个错误提前取消其余任务。
func (s *OssStorage) BatchUpload(ctx context.Context, tasks []UploadTask, opts ...IOOption) error {
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

// BatchDelete 批量删除，返回成功删除的 key 列表（相对路径）
func (s *OssStorage) BatchDelete(ctx context.Context, keys []string, opts ...IOOption) ([]string, error) {
	objectKeys := make([]string, 0, len(keys))
	for _, k := range keys {
		if k == "" {
			continue
		}
		objectKeys = append(objectKeys, s.buildObjectKey(k))
	}
	if len(objectKeys) == 0 {
		return nil, nil
	}

	o := applyIOOptions(opts)
	prefix := ""
	if s.baseDir != "" {
		prefix = s.baseDir + "/"
	}

	var (
		deleted []string
		errs    []error
	)
	const batchSize = 1000
	for start := 0; start < len(objectKeys); start += batchSize {
		end := start + batchSize
		if end > len(objectKeys) {
			end = len(objectKeys)
		}
		resp, err := s.bucket.DeleteObjects(objectKeys[start:end], oss.DeleteObjectsQuiet(o.Quiet))
		if err != nil {
			errs = append(errs, fmt.Errorf("batch[%d-%d]: %w", start, end-1, err))
			continue
		}
		if o.Quiet {
			// 静默模式：无错误即认为本批全部成功
			for _, ok := range objectKeys[start:end] {
				deleted = append(deleted, strings.TrimPrefix(ok, prefix))
			}
		} else {
			// 详细模式：OSS 返回实际删除的 key
			for _, dk := range resp.DeletedObjects {
				deleted = append(deleted, strings.TrimPrefix(dk, prefix))
			}
		}
	}
	return deleted, errors.Join(errs...)
}

// DeleteByPrefix 按前缀删除所有文件（遍历 + 分批删除，内存峰值受控）
// 注意：遍历过程中删除对象可能使部分后续页跳过，若需确保删空，建议循环调用直到 PrefixIterator 返回空。
func (s *OssStorage) DeleteByPrefix(ctx context.Context, prefix string, opts ...IOOption) error {
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

// PrefixIterator 迭代遍历指定前缀的文件，通过回调分页流式返回
func (s *OssStorage) PrefixIterator(ctx context.Context, prefix string, callback ListCallback) error {
	fullPrefix := s.buildObjectKey(prefix)
	strip := ""
	if s.baseDir != "" {
		strip = s.baseDir + "/"
	}
	var continueToken string
	for {
		result, err := s.bucket.ListObjectsV2(oss.Prefix(fullPrefix), oss.ContinuationToken(continueToken))
		if err != nil {
			logger.ErrorfC(ctx, "oss ListObjectsV2 failed: %v", err)
			return err
		}
		items := make([]ObjectItem, 0, len(result.Objects))
		for _, obj := range result.Objects {
			items = append(items, ObjectItem{
				Key:          strings.TrimPrefix(obj.Key, strip), // 还原相对路径
				Size:         obj.Size,
				LastModified: obj.LastModified.Unix(),
			})
		}
		if err = callback(ctx, items...); err != nil {
			return err
		}
		if !result.IsTruncated {
			break
		}
		continueToken = result.NextContinuationToken
	}
	return nil
}

// InitiateMultipartUpload 初始化分片上传
func (s *OssStorage) InitiateMultipartUpload(ctx context.Context, key string) (MultipartUploadSession, error) {
	result, err := s.bucket.InitiateMultipartUpload(s.buildObjectKey(key))
	if err != nil {
		return MultipartUploadSession{}, fmt.Errorf("InitiateMultipartUpload: %w", err)
	}
	//result.Bucket
	//s.bucket.BucketName
	// Key 存完整 objectKey，后续分片操作直接复用，无需再拼前缀
	return MultipartUploadSession{UploadID: result.UploadID, Key: result.Key}, nil
}

// CompleteMultipartUpload 完成分片上传（支持 WithMeta 等选项）
func (s *OssStorage) CompleteMultipartUpload(ctx context.Context, session MultipartUploadSession, parts []UploadPartResponse, opts ...IOOption) error {
	imur := oss.InitiateMultipartUploadResult{Key: session.Key, UploadID: session.UploadID}
	ossParts := make([]oss.UploadPart, 0, len(parts))
	for _, p := range parts {
		ossParts = append(ossParts, oss.UploadPart{PartNumber: p.PartNumber, ETag: p.ETag})
	}
	_, err := s.bucket.CompleteMultipartUpload(imur, ossParts, s.toOSSOptions(applyIOOptions(opts))...)
	return err
}

// CancelMultipartUpload 取消分片上传，清理已上传的临时分片
func (s *OssStorage) CancelMultipartUpload(ctx context.Context, session MultipartUploadSession) error {
	imur := oss.InitiateMultipartUploadResult{Key: session.Key, UploadID: session.UploadID}
	return s.bucket.AbortMultipartUpload(imur)
}

// MultipartUpload 上传单个分片
func (s *OssStorage) MultipartUpload(ctx context.Context, session MultipartUploadSession, partNumber int, partSize int64, body io.Reader) (UploadPartResponse, error) {
	imur := oss.InitiateMultipartUploadResult{Key: session.Key, UploadID: session.UploadID}
	part, err := s.bucket.UploadPart(imur, body, partSize, partNumber)
	if err != nil {
		return UploadPartResponse{}, fmt.Errorf("UploadPart %d: %w", partNumber, err)
	}
	return UploadPartResponse{PartNumber: part.PartNumber, ETag: part.ETag}, nil
}
