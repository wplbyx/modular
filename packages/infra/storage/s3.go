package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"holographic/packages/config"
)

var _ Storage = (*S3Storage)(nil)

type s3ObjectClient interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	DeleteObject(context.Context, *s3.DeleteObjectInput, ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

type s3Settings struct {
	AccessKeyID     string
	SecretAccessKey string
	Region          string
	Bucket          string
	Endpoint        string
	DisableSSL      bool
	ForcePathStyle  bool
	Timeout         time.Duration
	MaxRetries      int
	PublicBaseURL   string
}

// S3Storage stores files in AWS S3 or any S3-compatible object storage.
type S3Storage struct {
	client         s3ObjectClient
	bucket         string
	region         string
	endpoint       string
	forcePathStyle bool
	publicBaseURL  string
}

func NewS3Storage(cfg *config.Storage) (*S3Storage, error) {
	if cfg == nil {
		return nil, fmt.Errorf("storage config is nil")
	}
	if cfg.S3 == nil {
		return nil, fmt.Errorf("s3 storage config is nil")
	}
	return newS3Storage(s3Settings{
		AccessKeyID:     cfg.S3.AccessKeyID,
		SecretAccessKey: cfg.S3.SecretAccessKey,
		Region:          cfg.S3.Region,
		Bucket:          cfg.S3.Bucket,
		Endpoint:        cfg.S3.Endpoint,
		DisableSSL:      cfg.S3.DisableSSL,
		ForcePathStyle:  cfg.S3.ForcePathStyle,
		Timeout:         cfg.S3.Timeout,
		MaxRetries:      cfg.S3.MaxRetries,
		PublicBaseURL:   cfg.PublicBaseURL,
	})
}

func NewMinIOStorage(cfg *config.Storage) (*S3Storage, error) {
	if cfg == nil {
		return nil, fmt.Errorf("storage config is nil")
	}
	if cfg.Minio == nil {
		return nil, fmt.Errorf("minio storage config is nil")
	}
	forcePathStyle := cfg.Minio.ForcePathStyle
	if !forcePathStyle {
		forcePathStyle = true
	}
	return newS3Storage(s3Settings{
		AccessKeyID:     cfg.Minio.AccessKeyID,
		SecretAccessKey: cfg.Minio.SecretAccessKey,
		Region:          cfg.Minio.Region,
		Bucket:          cfg.Minio.Bucket,
		Endpoint:        cfg.Minio.Endpoint,
		DisableSSL:      cfg.Minio.DisableSSL,
		ForcePathStyle:  forcePathStyle,
		Timeout:         cfg.Minio.Timeout,
		MaxRetries:      cfg.Minio.MaxRetries,
		PublicBaseURL:   cfg.PublicBaseURL,
	})
}

func newS3Storage(settings s3Settings) (*S3Storage, error) {
	if settings.Bucket == "" {
		return nil, fmt.Errorf("s3 bucket is required")
	}
	settings.Endpoint = normalizeEndpoint(settings.Endpoint, settings.DisableSSL)
	if settings.Region == "" {
		settings.Region = "us-east-1"
	}
	if settings.AccessKeyID == "" || settings.SecretAccessKey == "" {
		return nil, fmt.Errorf("s3 access key id and secret access key are required")
	}

	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(settings.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(settings.AccessKeyID, settings.SecretAccessKey, "")),
	}
	if settings.Timeout > 0 {
		loadOpts = append(loadOpts, awsconfig.WithHTTPClient(&http.Client{Timeout: settings.Timeout}))
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = settings.ForcePathStyle
		if settings.Endpoint != "" {
			o.BaseEndpoint = aws.String(strings.TrimRight(settings.Endpoint, "/"))
		}
		if settings.MaxRetries > 0 {
			o.RetryMaxAttempts = settings.MaxRetries
		}
	})

	return &S3Storage{
		client:         client,
		bucket:         settings.Bucket,
		region:         settings.Region,
		endpoint:       strings.TrimRight(settings.Endpoint, "/"),
		forcePathStyle: settings.ForcePathStyle,
		publicBaseURL:  settings.PublicBaseURL,
	}, nil
}

func (s *S3Storage) Upload(ctx context.Context, key string, reader io.Reader) (*Object, error) {
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

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(cleanKey),
		Body:        body,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return nil, fmt.Errorf("put s3 object %s: %w", cleanKey, err)
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

func (s *S3Storage) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	cleanKey, err := cleanObjectKey(key)
	if err != nil {
		return nil, err
	}

	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(cleanKey),
	})
	if err != nil {
		return nil, fmt.Errorf("get s3 object %s: %w", cleanKey, err)
	}
	return out.Body, nil
}

func (s *S3Storage) Delete(ctx context.Context, key string) error {
	cleanKey, err := cleanObjectKey(key)
	if err != nil {
		return err
	}

	_, err = s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(cleanKey),
	})
	if err != nil {
		return fmt.Errorf("delete s3 object %s: %w", cleanKey, err)
	}
	return nil
}

func (s *S3Storage) Exists(ctx context.Context, key string) (bool, error) {
	cleanKey, err := cleanObjectKey(key)
	if err != nil {
		return false, err
	}

	_, err = s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(cleanKey),
	})
	if err == nil {
		return true, nil
	}
	if isS3NotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("head s3 object %s: %w", cleanKey, err)
}

func (s *S3Storage) GetURL(key string) string {
	cleanKey, err := cleanObjectKey(key)
	if err != nil {
		return ""
	}
	fallback := s3DefaultURL(s.bucket, s.region, cleanKey)
	if s.endpoint != "" {
		fallback = joinEndpointPath(s.endpoint, s.bucket, cleanKey, s.forcePathStyle)
	}
	return objectURL(s.publicBaseURL, fallback, cleanKey)
}

func isS3NotFound(err error) bool {
	var notFound *s3types.NotFound
	if errors.As(err, &notFound) {
		return true
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NotFound", "NoSuchKey", "NoSuchBucket":
			return true
		}
	}
	return false
}
