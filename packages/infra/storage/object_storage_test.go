package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"modular/packages/config"
)

func TestNewStorageObjectBackends(t *testing.T) {
	_, err := NewStorage(&config.Storage{
		Type: "s3",
		S3: &config.S3StorageConfig{
			AccessKeyID:     "id",
			SecretAccessKey: "secret",
			Region:          "us-east-1",
			Bucket:          "bucket",
		},
	})
	if err != nil {
		t.Fatalf("NewStorage(s3) error = %v", err)
	}

	_, err = NewStorage(&config.Storage{
		Type: "minio",
		Minio: &config.MinioStorageConfig{
			AccessKeyID:     "id",
			SecretAccessKey: "secret",
			Region:          "us-east-1",
			Bucket:          "bucket",
			Endpoint:        "localhost:9000",
			DisableSSL:      true,
		},
	})
	if err != nil {
		t.Fatalf("NewStorage(minio) error = %v", err)
	}

	_, err = NewStorage(&config.Storage{
		Type: "oss",
		OSS: &config.OSSStorageConfig{
			AccessKeyID:     "id",
			AccessKeySecret: "secret",
			Region:          "cn-hangzhou",
			Bucket:          "bucket",
		},
	})
	if err != nil {
		t.Fatalf("NewStorage(oss) error = %v", err)
	}
}

func TestS3StorageOperations(t *testing.T) {
	client := &fakeS3Client{
		objects:      make(map[string][]byte),
		contentTypes: make(map[string]string),
	}
	store := &S3Storage{
		client:        client,
		bucket:        "bucket",
		region:        "us-east-1",
		publicBaseURL: "https://cdn.example.com/static",
	}

	obj, err := store.Upload(context.Background(), "dir/file.txt", bytes.NewBufferString("hello"))
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if obj.Size != 5 || obj.Hash == "" || obj.URL != "https://cdn.example.com/static/dir/file.txt" {
		t.Fatalf("Upload() object = %+v", obj)
	}
	if obj.ContentType != "text/plain; charset=utf-8" || client.contentTypes["dir/file.txt"] != obj.ContentType {
		t.Fatalf("content type object = %q, request = %q", obj.ContentType, client.contentTypes["dir/file.txt"])
	}

	exists, err := store.Exists(context.Background(), "dir/file.txt")
	if err != nil || !exists {
		t.Fatalf("Exists() = %v, %v", exists, err)
	}

	reader, err := store.Download(context.Background(), "dir/file.txt")
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	data, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(data) != "hello" {
		t.Fatalf("Download() = %q", data)
	}

	if err := store.Delete(context.Background(), "dir/file.txt"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	exists, err = store.Exists(context.Background(), "dir/file.txt")
	if err != nil || exists {
		t.Fatalf("Exists() after delete = %v, %v", exists, err)
	}
}

func TestOSSStorageOperations(t *testing.T) {
	client := &fakeOSSClient{
		objects:      make(map[string][]byte),
		contentTypes: make(map[string]string),
	}
	store := &OSSStorage{
		client:        client,
		bucket:        "bucket",
		region:        "cn-hangzhou",
		publicBaseURL: "https://cdn.example.com",
	}

	obj, err := store.Upload(context.Background(), "dir/file.txt", bytes.NewBufferString("hello"))
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if obj.Size != 5 || obj.Hash == "" || obj.URL != "https://cdn.example.com/dir/file.txt" {
		t.Fatalf("Upload() object = %+v", obj)
	}
	if obj.ContentType != "text/plain; charset=utf-8" || client.contentTypes["dir/file.txt"] != obj.ContentType {
		t.Fatalf("content type object = %q, request = %q", obj.ContentType, client.contentTypes["dir/file.txt"])
	}

	exists, err := store.Exists(context.Background(), "dir/file.txt")
	if err != nil || !exists {
		t.Fatalf("Exists() = %v, %v", exists, err)
	}

	reader, err := store.Download(context.Background(), "dir/file.txt")
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	data, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(data) != "hello" {
		t.Fatalf("Download() = %q", data)
	}

	if err := store.Delete(context.Background(), "dir/file.txt"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	exists, err = store.Exists(context.Background(), "dir/file.txt")
	if err != nil || exists {
		t.Fatalf("Exists() after delete = %v, %v", exists, err)
	}
}

type fakeS3Client struct {
	objects      map[string][]byte
	contentTypes map[string]string
}

func (f *fakeS3Client) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	data, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.objects[aws.ToString(in.Key)] = data
	f.contentTypes[aws.ToString(in.Key)] = aws.ToString(in.ContentType)
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3Client) GetObject(_ context.Context, in *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	data, ok := f.objects[aws.ToString(in.Key)]
	if !ok {
		return nil, &s3types.NotFound{}
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data))}, nil
}

func (f *fakeS3Client) DeleteObject(_ context.Context, in *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	delete(f.objects, aws.ToString(in.Key))
	return &s3.DeleteObjectOutput{}, nil
}

func (f *fakeS3Client) HeadObject(_ context.Context, in *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	if _, ok := f.objects[aws.ToString(in.Key)]; !ok {
		return nil, &s3types.NotFound{}
	}
	return &s3.HeadObjectOutput{}, nil
}

type fakeOSSClient struct {
	objects      map[string][]byte
	contentTypes map[string]string
}

func (f *fakeOSSClient) PutObject(_ context.Context, in *aliyunoss.PutObjectRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.PutObjectResult, error) {
	data, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	key := aliyunoss.ToString(in.Key)
	f.objects[key] = data
	f.contentTypes[key] = aliyunoss.ToString(in.ContentType)
	return &aliyunoss.PutObjectResult{}, nil
}

func (f *fakeOSSClient) GetObject(_ context.Context, in *aliyunoss.GetObjectRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.GetObjectResult, error) {
	data, ok := f.objects[aliyunoss.ToString(in.Key)]
	if !ok {
		return nil, ossNotFound()
	}
	return &aliyunoss.GetObjectResult{Body: io.NopCloser(bytes.NewReader(data))}, nil
}

func (f *fakeOSSClient) DeleteObject(_ context.Context, in *aliyunoss.DeleteObjectRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.DeleteObjectResult, error) {
	delete(f.objects, aliyunoss.ToString(in.Key))
	return &aliyunoss.DeleteObjectResult{}, nil
}

func (f *fakeOSSClient) HeadObject(_ context.Context, in *aliyunoss.HeadObjectRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.HeadObjectResult, error) {
	if _, ok := f.objects[aliyunoss.ToString(in.Key)]; !ok {
		return nil, ossNotFound()
	}
	return &aliyunoss.HeadObjectResult{}, nil
}

func ossNotFound() error {
	return &aliyunoss.ServiceError{StatusCode: http.StatusNotFound, Code: "NoSuchKey"}
}
