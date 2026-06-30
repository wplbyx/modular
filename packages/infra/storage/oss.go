package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	osscredentials "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"

	"modular/packages/config"
)

var _ Storage = (*OSSStorage)(nil)

type ossObjectClient interface {
	PutObject(context.Context, *aliyunoss.PutObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.PutObjectResult, error)
	GetObject(context.Context, *aliyunoss.GetObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.GetObjectResult, error)
	DeleteObject(context.Context, *aliyunoss.DeleteObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.DeleteObjectResult, error)
	HeadObject(context.Context, *aliyunoss.HeadObjectRequest, ...func(*aliyunoss.Options)) (*aliyunoss.HeadObjectResult, error)
}

// OSSStorage stores files in Alibaba Cloud OSS.
type OSSStorage struct {
	client        ossObjectClient
	bucket        string
	region        string
	endpoint      string
	useCName      bool
	publicBaseURL string
}

func NewOSSStorage(cfg *config.Storage) (*OSSStorage, error) {
	if cfg == nil {
		return nil, fmt.Errorf("storage config is nil")
	}
	if cfg.OSS == nil {
		return nil, fmt.Errorf("oss storage config is nil")
	}
	ossCfg := cfg.OSS
	if ossCfg.Bucket == "" {
		return nil, fmt.Errorf("oss bucket is required")
	}
	if ossCfg.Region == "" {
		return nil, fmt.Errorf("oss region is required")
	}
	if ossCfg.AccessKeyID == "" || ossCfg.AccessKeySecret == "" {
		return nil, fmt.Errorf("oss access key id and access key secret are required")
	}
	endpoint := normalizeEndpoint(ossCfg.Endpoint, ossCfg.DisableSSL)

	sdkCfg := aliyunoss.LoadDefaultConfig().
		WithRegion(ossCfg.Region).
		WithCredentialsProvider(osscredentials.NewStaticCredentialsProvider(
			ossCfg.AccessKeyID,
			ossCfg.AccessKeySecret,
			ossCfg.SecurityToken,
		)).
		WithUseCName(ossCfg.UseCName).
		WithDisableSSL(ossCfg.DisableSSL)
	if endpoint != "" {
		sdkCfg = sdkCfg.WithEndpoint(endpoint)
	}
	if ossCfg.MaxRetries > 0 {
		sdkCfg = sdkCfg.WithRetryMaxAttempts(ossCfg.MaxRetries)
	}
	if ossCfg.Timeout > 0 {
		sdkCfg = sdkCfg.
			WithConnectTimeout(ossCfg.Timeout).
			WithReadWriteTimeout(ossCfg.Timeout).
			WithHttpClient(&http.Client{Timeout: ossCfg.Timeout})
	}

	return &OSSStorage{
		client:        aliyunoss.NewClient(sdkCfg),
		bucket:        ossCfg.Bucket,
		region:        ossCfg.Region,
		endpoint:      endpoint,
		useCName:      ossCfg.UseCName,
		publicBaseURL: cfg.PublicBaseURL,
	}, nil
}

func (s *OSSStorage) Upload(ctx context.Context, key string, reader io.Reader) (*Object, error) {
	cleanKey, err := cleanObjectKey(key)
	if err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, fmt.Errorf("upload reader is nil")
	}

	body, contentType, err := newTrackedUploadReader(reader)
	if err != nil {
		return nil, err
	}

	_, err = s.client.PutObject(ctx, &aliyunoss.PutObjectRequest{
		Bucket:      aliyunoss.Ptr(s.bucket),
		Key:         aliyunoss.Ptr(cleanKey),
		Body:        body,
		ContentType: aliyunoss.Ptr(contentType),
	})
	if err != nil {
		return nil, fmt.Errorf("put oss object %s: %w", cleanKey, err)
	}

	return &Object{
		Key:         cleanKey,
		Path:        cleanKey,
		URL:         s.GetURL(cleanKey),
		Hash:        body.Hash(),
		Size:        body.Size(),
		ContentType: contentType,
	}, nil
}

func (s *OSSStorage) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	cleanKey, err := cleanObjectKey(key)
	if err != nil {
		return nil, err
	}

	out, err := s.client.GetObject(ctx, &aliyunoss.GetObjectRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(cleanKey),
	})
	if err != nil {
		return nil, fmt.Errorf("get oss object %s: %w", cleanKey, err)
	}
	return out.Body, nil
}

func (s *OSSStorage) Delete(ctx context.Context, key string) error {
	cleanKey, err := cleanObjectKey(key)
	if err != nil {
		return err
	}

	_, err = s.client.DeleteObject(ctx, &aliyunoss.DeleteObjectRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(cleanKey),
	})
	if err != nil {
		return fmt.Errorf("delete oss object %s: %w", cleanKey, err)
	}
	return nil
}

func (s *OSSStorage) Exists(ctx context.Context, key string) (bool, error) {
	cleanKey, err := cleanObjectKey(key)
	if err != nil {
		return false, err
	}

	_, err = s.client.HeadObject(ctx, &aliyunoss.HeadObjectRequest{
		Bucket: aliyunoss.Ptr(s.bucket),
		Key:    aliyunoss.Ptr(cleanKey),
	})
	if err == nil {
		return true, nil
	}
	if isOSSNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("head oss object %s: %w", cleanKey, err)
}

func (s *OSSStorage) GetURL(key string) string {
	cleanKey, err := cleanObjectKey(key)
	if err != nil {
		return ""
	}
	fallback := ossDefaultURL(s.bucket, s.region, s.endpoint, cleanKey, s.useCName)
	return objectURL(s.publicBaseURL, fallback, cleanKey)
}

func isOSSNotFound(err error) bool {
	var serviceErr *aliyunoss.ServiceError
	if errors.As(err, &serviceErr) {
		return serviceErr.StatusCode == http.StatusNotFound ||
			serviceErr.Code == "NoSuchKey" ||
			serviceErr.Code == "NoSuchBucket"
	}
	return false
}
