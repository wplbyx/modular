package alioss

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"golang.org/x/sync/errgroup"

	"modular/packages/config"
	"modular/packages/infra/storage"
)

var _ storage.Storage = (*OssStorage)(nil)

// ossClient 隔离 v2 Client，便于测试 mock。*oss.Client 天然满足该接口。
type ossClient interface {
	PutObject(context.Context, *oss.PutObjectRequest, ...func(*oss.Options)) (*oss.PutObjectResult, error)
	GetObject(context.Context, *oss.GetObjectRequest, ...func(*oss.Options)) (*oss.GetObjectResult, error)
	DeleteObject(context.Context, *oss.DeleteObjectRequest, ...func(*oss.Options)) (*oss.DeleteObjectResult, error)
	HeadObject(context.Context, *oss.HeadObjectRequest, ...func(*oss.Options)) (*oss.HeadObjectResult, error)
	DeleteMultipleObjects(context.Context, *oss.DeleteMultipleObjectsRequest, ...func(*oss.Options)) (*oss.DeleteMultipleObjectsResult, error)
	ListObjectsV2(context.Context, *oss.ListObjectsV2Request, ...func(*oss.Options)) (*oss.ListObjectsV2Result, error)
	InitiateMultipartUpload(context.Context, *oss.InitiateMultipartUploadRequest, ...func(*oss.Options)) (*oss.InitiateMultipartUploadResult, error)
	UploadPart(context.Context, *oss.UploadPartRequest, ...func(*oss.Options)) (*oss.UploadPartResult, error)
	CompleteMultipartUpload(context.Context, *oss.CompleteMultipartUploadRequest, ...func(*oss.Options)) (*oss.CompleteMultipartUploadResult, error)
	AbortMultipartUpload(context.Context, *oss.AbortMultipartUploadRequest, ...func(*oss.Options)) (*oss.AbortMultipartUploadResult, error)
}

// applyIOOptions 将可选参数合并为 IOOptions。
func applyIOOptions(opts []storage.IOOption) *storage.IOOptions {
	o := &storage.IOOptions{}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// OssStorage 是 Storage 的阿里云 OSS（v2 SDK）实现。
type OssStorage struct {
	client        ossClient
	bucket        string
	region        string
	endpoint      string
	useCName      bool
	publicBaseURL string
	baseDir       string // 对象 key 前缀（不带首尾斜杠）
}

// NewOSSStorage 构造 OSS Storage。
func NewOSSStorage(cfg *config.Storage) (*OssStorage, error) {
	if cfg == nil || cfg.OSS == nil {
		return nil, errors.New("oss storage config is nil")
	}
	oc := cfg.OSS
	if oc.Bucket == "" || oc.Region == "" || oc.AccessKeyID == "" || oc.AccessKeySecret == "" {
		return nil, errors.New("oss bucket/region/access-key are required")
	}
	endpoint := normalizeEndpoint(oc.Endpoint, oc.DisableSSL)

	sdkCfg := oss.LoadDefaultConfig().
		WithRegion(oc.Region).
		WithCredentialsProvider(credentials.NewStaticCredentialsProvider(oc.AccessKeyID, oc.AccessKeySecret, oc.SecurityToken)).
		WithUseCName(oc.UseCName).
		WithDisableSSL(oc.DisableSSL)
	if endpoint != "" {
		sdkCfg = sdkCfg.WithEndpoint(endpoint)
	}
	if oc.MaxRetries > 0 {
		sdkCfg = sdkCfg.WithRetryMaxAttempts(oc.MaxRetries)
	}
	if oc.Timeout > 0 {
		sdkCfg = sdkCfg.
			WithConnectTimeout(oc.Timeout).
			WithReadWriteTimeout(oc.Timeout).
			WithHttpClient(&http.Client{Timeout: oc.Timeout})
	}

	return &OssStorage{
		client:        oss.NewClient(sdkCfg),
		bucket:        oc.Bucket,
		region:        oc.Region,
		endpoint:      endpoint,
		useCName:      oc.UseCName,
		publicBaseURL: cfg.PublicBaseURL,
		baseDir:       strings.Trim(oc.BaseDir, "/"),
	}, nil
}

// buildObjectKey 把相对 key 拼成 OSS 内部 objectKey。
func (s *OssStorage) buildObjectKey(key string) string {
	key = strings.TrimPrefix(key, "/")
	if s.baseDir != "" {
		return s.baseDir + "/" + key
	}
	return key
}

// GetUsefulUrl 生成可直接访问的完整 URL。
func (s *OssStorage) GetUsefulUrl(key string) string {
	if key == "" {
		return ""
	}
	fallback := ossDefaultURL(s.bucket, s.region, s.endpoint, s.buildObjectKey(key), s.useCName)
	return objectURL(s.publicBaseURL, fallback, s.buildObjectKey(key))
}

// Exists 检查对象是否存在。
func (s *OssStorage) Exists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &oss.HeadObjectRequest{
		Bucket: oss.Ptr(s.bucket),
		Key:    oss.Ptr(s.buildObjectKey(key)),
	})
	if err == nil {
		return true, nil
	}
	if isOSSNotFound(err) {
		return false, nil
	}
	return false, err
}

// Upload 上传单个文件。
func (s *OssStorage) Upload(ctx context.Context, key string, body io.Reader, opts ...storage.IOOption) error {
	o := applyIOOptions(opts)
	req := &oss.PutObjectRequest{
		Bucket: oss.Ptr(s.bucket),
		Key:    oss.Ptr(s.buildObjectKey(key)),
		Body:   body,
	}
	if o.ContentType != "" {
		req.ContentType = oss.Ptr(o.ContentType)
	}
	if len(o.Meta) > 0 {
		req.Metadata = o.Meta
	}
	if _, err := s.client.PutObject(ctx, req); err != nil {
		return fmt.Errorf("put oss object %s: %w", key, err)
	}
	return nil
}

// Delete 删除单个文件。
func (s *OssStorage) Delete(ctx context.Context, key string, _ ...storage.IOOption) error {
	if _, err := s.client.DeleteObject(ctx, &oss.DeleteObjectRequest{
		Bucket: oss.Ptr(s.bucket),
		Key:    oss.Ptr(s.buildObjectKey(key)),
	}); err != nil {
		return fmt.Errorf("delete oss object %s: %w", key, err)
	}
	return nil
}

// Download 下载单个文件，调用方需关闭返回的 io.ReadCloser。
func (s *OssStorage) Download(ctx context.Context, key string, _ ...storage.IOOption) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &oss.GetObjectRequest{
		Bucket: oss.Ptr(s.bucket),
		Key:    oss.Ptr(s.buildObjectKey(key)),
	})
	if err != nil {
		return nil, fmt.Errorf("get oss object %s: %w", key, err)
	}
	return out.Body, nil
}

// Stat 获取元信息。
func (s *OssStorage) Stat(ctx context.Context, key string) (storage.ObjectItem, error) {
	res, err := s.client.HeadObject(ctx, &oss.HeadObjectRequest{
		Bucket: oss.Ptr(s.bucket),
		Key:    oss.Ptr(s.buildObjectKey(key)),
	})
	if err != nil {
		return storage.ObjectItem{}, err
	}
	item := storage.ObjectItem{Key: key, Size: res.ContentLength}
	if res.LastModified != nil {
		item.LastModified = res.LastModified.Unix()
	}
	return item, nil
}

// BatchUpload 批量上传（errgroup 并发，错误聚合）。
func (s *OssStorage) BatchUpload(ctx context.Context, tasks []storage.UploadTask, opts ...storage.IOOption) error {
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
			return nil
		})
	}
	_ = eg.Wait()
	return errors.Join(errs...)
}

// BatchDelete 批量删除，返回成功删除的 key（相对路径）。
func (s *OssStorage) BatchDelete(ctx context.Context, keys []string, opts ...storage.IOOption) ([]string, error) {
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
	for start := 0; start < len(keys); start += batchSize {
		end := start + batchSize
		if end > len(keys) {
			end = len(keys)
		}
		objects := make([]oss.ObjectIdentifier, 0, end-start)
		for _, k := range keys[start:end] {
			if k == "" {
				continue
			}
			objects = append(objects, oss.ObjectIdentifier{Key: oss.Ptr(s.buildObjectKey(k))})
		}
		if len(objects) == 0 {
			continue
		}
		res, err := s.client.DeleteMultipleObjects(ctx, &oss.DeleteMultipleObjectsRequest{
			Bucket: oss.Ptr(s.bucket),
			Delete: &oss.Delete{Objects: objects, Quiet: o.Quiet},
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("batch[%d-%d]: %w", start, end-1, err))
			continue
		}
		if o.Quiet {
			// 静默模式：OSS 不返回已删除对象列表，故无法回填成功 key。
			// 无错误即视为本批全部成功，调用方在 quiet 模式下不期望返回明细。
		} else {
			for _, d := range res.DeletedObjects {
				if d.Key != nil {
					deleted = append(deleted, strings.TrimPrefix(*d.Key, prefix))
				}
			}
		}
	}
	return deleted, errors.Join(errs...)
}

// DeleteByPrefix 按前缀删除（PrefixIterator + 每 1000 条 BatchDelete）。
func (s *OssStorage) DeleteByPrefix(ctx context.Context, prefix string, opts ...storage.IOOption) error {
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

// PrefixIterator 流式分页遍历。
func (s *OssStorage) PrefixIterator(ctx context.Context, prefix string, callback storage.ListCallback) error {
	fullPrefix := s.buildObjectKey(prefix)
	strip := ""
	if s.baseDir != "" {
		strip = s.baseDir + "/"
	}
	var continuationToken string
	for {
		res, err := s.client.ListObjectsV2(ctx, &oss.ListObjectsV2Request{
			Bucket:            oss.Ptr(s.bucket),
			Prefix:            oss.Ptr(fullPrefix),
			ContinuationToken: oss.Ptr(continuationToken),
			MaxKeys:           1000,
		})
		if err != nil {
			return err
		}
		items := make([]storage.ObjectItem, 0, len(res.Contents))
		for _, obj := range res.Contents {
			k := ""
			if obj.Key != nil {
				k = strings.TrimPrefix(*obj.Key, strip)
			}
			items = append(items, storage.ObjectItem{Key: k, Size: obj.Size})
		}
		if err = callback(ctx, items...); err != nil {
			return err
		}
		if !res.IsTruncated {
			break
		}
		if res.NextContinuationToken != nil {
			continuationToken = *res.NextContinuationToken
		} else {
			break
		}
	}
	return nil
}

// InitiateMultipartUpload 初始化分片上传。session.Key 存完整 objectKey。
func (s *OssStorage) InitiateMultipartUpload(ctx context.Context, key string) (storage.MultipartUploadSession, error) {
	objKey := s.buildObjectKey(key)
	res, err := s.client.InitiateMultipartUpload(ctx, &oss.InitiateMultipartUploadRequest{
		Bucket: oss.Ptr(s.bucket),
		Key:    oss.Ptr(objKey),
	})
	if err != nil {
		return storage.MultipartUploadSession{}, fmt.Errorf("InitiateMultipartUpload: %w", err)
	}
	return storage.MultipartUploadSession{UploadID: oss.ToString(res.UploadId), Key: objKey}, nil
}

// MultipartUpload 上传单个分片。
func (s *OssStorage) MultipartUpload(ctx context.Context, session storage.MultipartUploadSession, partNumber int, partSize int64, body io.Reader) (storage.UploadPartResponse, error) {
	if partNumber < 1 {
		return storage.UploadPartResponse{}, errors.New("partNumber must be >= 1")
	}
	res, err := s.client.UploadPart(ctx, &oss.UploadPartRequest{
		Bucket:     oss.Ptr(s.bucket),
		Key:        oss.Ptr(session.Key),
		UploadId:   oss.Ptr(session.UploadID),
		PartNumber: int32(partNumber),
		Body:       body,
	})
	if err != nil {
		return storage.UploadPartResponse{}, fmt.Errorf("UploadPart %d: %w", partNumber, err)
	}
	return storage.UploadPartResponse{PartNumber: partNumber, ETag: oss.ToString(res.ETag)}, nil
}

// CompleteMultipartUpload 完成分片上传。
func (s *OssStorage) CompleteMultipartUpload(ctx context.Context, session storage.MultipartUploadSession, parts []storage.UploadPartResponse, _ ...storage.IOOption) error {
	if len(parts) == 0 {
		return errors.New("no parts to complete")
	}
	ossParts := make([]oss.UploadPart, 0, len(parts))
	for _, p := range parts {
		ossParts = append(ossParts, oss.UploadPart{PartNumber: int32(p.PartNumber), ETag: oss.Ptr(p.ETag)})
	}
	_, err := s.client.CompleteMultipartUpload(ctx, &oss.CompleteMultipartUploadRequest{
		Bucket:                  oss.Ptr(s.bucket),
		Key:                     oss.Ptr(session.Key),
		UploadId:                oss.Ptr(session.UploadID),
		CompleteMultipartUpload: &oss.CompleteMultipartUpload{Parts: ossParts},
	})
	return err
}

// CancelMultipartUpload 取消分片上传。
func (s *OssStorage) CancelMultipartUpload(ctx context.Context, session storage.MultipartUploadSession) error {
	_, err := s.client.AbortMultipartUpload(ctx, &oss.AbortMultipartUploadRequest{
		Bucket:   oss.Ptr(s.bucket),
		Key:      oss.Ptr(session.Key),
		UploadId: oss.Ptr(session.UploadID),
	})
	return err
}

// --- 辅助 ---

func isOSSNotFound(err error) bool {
	var se *oss.ServiceError
	if errors.As(err, &se) {
		return se.StatusCode == http.StatusNotFound || se.Code == "NoSuchKey" || se.Code == "NoSuchBucket"
	}
	return false
}

func objectURL(publicBaseURL, fallbackBaseURL, key string) string {
	base := strings.TrimSpace(publicBaseURL)
	if base == "" {
		base = fallbackBaseURL
	}
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(key, "/")
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

func ossDefaultURL(bucket, region, endpoint, key string, useCName bool) string {
	key = strings.TrimLeft(key, "/")
	if endpoint != "" {
		if useCName {
			return strings.TrimRight(endpoint, "/") + "/" + key
		}
		return joinEndpointPath(endpoint, bucket, key)
	}
	if bucket == "" || region == "" {
		return ""
	}
	return fmt.Sprintf("https://%s.oss-%s.aliyuncs.com/%s", bucket, region, key)
}

func joinEndpointPath(endpoint, bucket, key string) string {
	endpoint = strings.TrimRight(endpoint, "/")
	if endpoint == "" {
		return ""
	}
	// 虚拟主机风格：bucket.endpoint/key
	u := endpoint
	if !strings.Contains(endpoint, "://") {
		u = "https://" + endpoint
	}
	// 简化：直接拼 bucket 前缀
	host := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	return "https://" + bucket + "." + host + "/" + strings.Trim(path.Join("/", key), "/")
}
