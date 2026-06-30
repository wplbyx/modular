package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	osscredentials "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"golang.org/x/sync/errgroup"

	"modular/packages/config"
)

var _ Storage = (*OssStorage)(nil)

// ossClient 隔离 v2 Client，便于测试 mock。*aliyunoss.Client 天然满足该接口。
type ossClient interface {
	PutObject(context.Context, *aliyunoss.PutObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.PutObjectResult, error)
	GetObject(context.Context, *aliyunoss.GetObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.GetObjectResult, error)
	DeleteObject(context.Context, *aliyunoss.DeleteObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.DeleteObjectResult, error)
	HeadObject(context.Context, *aliyunoss.HeadObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.HeadObjectResult, error)
	DeleteMultipleObjects(context.Context, *aliyunoss.DeleteMultipleObjectsRequest, ...func(*aliyunoss.Options)) (*aliyunoss.DeleteMultipleObjectsResult, error)
	ListObjectsV2(context.Context, *aliyunoss.ListObjectsV2Request, ...func(*aliyunoss.Options)) (*aliyunoss.ListObjectsV2Result, error)
	InitiateMultipartUpload(context.Context, *aliyunoss.InitiateMultipartUploadRequest, ...func(*aliyunoss.Options)) (*aliyunoss.InitiateMultipartUploadResult, error)
	UploadPart(context.Context, *aliyunoss.UploadPartRequest, ...func(*aliyunoss.Options)) (*aliyunoss.UploadPartResult, error)
	CompleteMultipartUpload(context.Context, *aliyunoss.CompleteMultipartUploadRequest, ...func(*aliyunoss.Options)) (*aliyunoss.CompleteMultipartUploadResult, error)
	AbortMultipartUpload(context.Context, *aliyunoss.AbortMultipartUploadRequest, ...func(*aliyunoss.Options)) (*aliyunoss.AbortMultipartUploadResult, error)
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

	sdkCfg := aliyunoss.LoadDefaultConfig().
		WithRegion(oc.Region).
		WithCredentialsProvider(osscredentials.NewStaticCredentialsProvider(oc.AccessKeyID, oc.AccessKeySecret, oc.SecurityToken)).
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
		client:        aliyunoss.NewClient(sdkCfg),
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
	_, err := s.client.HeadObject(ctx, &aliyunoss.HeadObjectRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(s.buildObjectKey(key)),
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
func (s *OssStorage) Upload(ctx context.Context, key string, body io.Reader, opts ...IOOption) error {
	o := applyIOOptions(opts)
	req := &aliyunoss.PutObjectRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(s.buildObjectKey(key)),
		Body:   body,
	}
	if o.ContentType != "" {
		req.ContentType = aliyunoss.Ptr(o.ContentType)
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
func (s *OssStorage) Delete(ctx context.Context, key string, _ ...IOOption) error {
	if _, err := s.client.DeleteObject(ctx, &aliyunoss.DeleteObjectRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(s.buildObjectKey(key)),
	}); err != nil {
		return fmt.Errorf("delete oss object %s: %w", key, err)
	}
	return nil
}

// Download 下载单个文件，调用方需关闭返回的 io.ReadCloser。
func (s *OssStorage) Download(ctx context.Context, key string, _ ...IOOption) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &aliyunoss.GetObjectRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(s.buildObjectKey(key)),
	})
	if err != nil {
		return nil, fmt.Errorf("get oss object %s: %w", key, err)
	}
	return out.Body, nil
}

// Stat 获取元信息。
func (s *OssStorage) Stat(ctx context.Context, key string) (ObjectItem, error) {
	res, err := s.client.HeadObject(ctx, &aliyunoss.HeadObjectRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(s.buildObjectKey(key)),
	})
	if err != nil {
		return ObjectItem{}, err
	}
	item := ObjectItem{Key: key, Size: res.ContentLength}
	if res.LastModified != nil {
		item.LastModified = res.LastModified.Unix()
	}
	return item, nil
}

// BatchUpload 批量上传（errgroup 并发，错误聚合）。
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
			return nil
		})
	}
	_ = eg.Wait()
	return errors.Join(errs...)
}

// BatchDelete 批量删除，返回成功删除的 key（相对路径）。
func (s *OssStorage) BatchDelete(ctx context.Context, keys []string, opts ...IOOption) ([]string, error) {
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
		objects := make([]aliyunoss.ObjectIdentifier, 0, end-start)
		for _, k := range keys[start:end] {
			if k == "" {
				continue
			}
			objects = append(objects, aliyunoss.ObjectIdentifier{Key: aliyunoss.Ptr(s.buildObjectKey(k))})
		}
		if len(objects) == 0 {
			continue
		}
		res, err := s.client.DeleteMultipleObjects(ctx, &aliyunoss.DeleteMultipleObjectsRequest{
			Bucket: aliyunoss.Ptr(s.bucket),
			Delete: &aliyunoss.Delete{Objects: objects, Quiet: o.Quiet},
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

// PrefixIterator 流式分页遍历。
func (s *OssStorage) PrefixIterator(ctx context.Context, prefix string, callback ListCallback) error {
	fullPrefix := s.buildObjectKey(prefix)
	strip := ""
	if s.baseDir != "" {
		strip = s.baseDir + "/"
	}
	var continuationToken string
	for {
		res, err := s.client.ListObjectsV2(ctx, &aliyunoss.ListObjectsV2Request{
			Bucket:            aliyunoss.Ptr(s.bucket),
			Prefix:            aliyunoss.Ptr(fullPrefix),
			ContinuationToken: aliyunoss.Ptr(continuationToken),
			MaxKeys:           1000,
		})
		if err != nil {
			return err
		}
		items := make([]ObjectItem, 0, len(res.Contents))
		for _, obj := range res.Contents {
			k := ""
			if obj.Key != nil {
				k = strings.TrimPrefix(*obj.Key, strip)
			}
			items = append(items, ObjectItem{Key: k, Size: obj.Size})
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
func (s *OssStorage) InitiateMultipartUpload(ctx context.Context, key string) (MultipartUploadSession, error) {
	objKey := s.buildObjectKey(key)
	res, err := s.client.InitiateMultipartUpload(ctx, &aliyunoss.InitiateMultipartUploadRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(objKey),
	})
	if err != nil {
		return MultipartUploadSession{}, fmt.Errorf("InitiateMultipartUpload: %w", err)
	}
	return MultipartUploadSession{UploadID: aliyunoss.ToString(res.UploadId), Key: objKey}, nil
}

// MultipartUpload 上传单个分片。
func (s *OssStorage) MultipartUpload(ctx context.Context, session MultipartUploadSession, partNumber int, partSize int64, body io.Reader) (UploadPartResponse, error) {
	if partNumber < 1 {
		return UploadPartResponse{}, errors.New("partNumber must be >= 1")
	}
	res, err := s.client.UploadPart(ctx, &aliyunoss.UploadPartRequest{
		Bucket:     aliyunoss.Ptr(s.bucket),
		Key:        aliyunoss.Ptr(session.Key),
		UploadId:   aliyunoss.Ptr(session.UploadID),
		PartNumber: int32(partNumber),
		Body:       body,
	})
	if err != nil {
		return UploadPartResponse{}, fmt.Errorf("UploadPart %d: %w", partNumber, err)
	}
	return UploadPartResponse{PartNumber: partNumber, ETag: aliyunoss.ToString(res.ETag)}, nil
}

// CompleteMultipartUpload 完成分片上传。
func (s *OssStorage) CompleteMultipartUpload(ctx context.Context, session MultipartUploadSession, parts []UploadPartResponse, _ ...IOOption) error {
	if len(parts) == 0 {
		return errors.New("no parts to complete")
	}
	ossParts := make([]aliyunoss.UploadPart, 0, len(parts))
	for _, p := range parts {
		ossParts = append(ossParts, aliyunoss.UploadPart{PartNumber: int32(p.PartNumber), ETag: aliyunoss.Ptr(p.ETag)})
	}
	_, err := s.client.CompleteMultipartUpload(ctx, &aliyunoss.CompleteMultipartUploadRequest{
		Bucket:                aliyunoss.Ptr(s.bucket),
		Key:                   aliyunoss.Ptr(session.Key),
		UploadId:              aliyunoss.Ptr(session.UploadID),
		CompleteMultipartUpload: &aliyunoss.CompleteMultipartUpload{Parts: ossParts},
	})
	return err
}

// CancelMultipartUpload 取消分片上传。
func (s *OssStorage) CancelMultipartUpload(ctx context.Context, session MultipartUploadSession) error {
	_, err := s.client.AbortMultipartUpload(ctx, &aliyunoss.AbortMultipartUploadRequest{
		Bucket:   aliyunoss.Ptr(s.bucket),
		Key:      aliyunoss.Ptr(session.Key),
		UploadId: aliyunoss.Ptr(session.UploadID),
	})
	return err
}

// --- 辅助 ---

func isOSSNotFound(err error) bool {
	var se *aliyunoss.ServiceError
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
