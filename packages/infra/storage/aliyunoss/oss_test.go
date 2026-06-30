package aliyunoss

import (
	"bytes"
	"context"
	"errors"
	"io"
	"modular/packages/infra/storage"
	"testing"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockOSSClient 实现 ossClient，按需覆盖单个方法。
type mockOSSClient struct {
	putObject      func(context.Context, *aliyunoss.PutObjectRequest) (*aliyunoss.PutObjectResult, error)
	getObject      func(context.Context, *aliyunoss.GetObjectRequest) (*aliyunoss.GetObjectResult, error)
	deleteObject   func(context.Context, *aliyunoss.DeleteObjectRequest) (*aliyunoss.DeleteObjectResult, error)
	headObject     func(context.Context, *aliyunoss.HeadObjectRequest) (*aliyunoss.HeadObjectResult, error)
	deleteMulti    func(context.Context, *aliyunoss.DeleteMultipleObjectsRequest) (*aliyunoss.DeleteMultipleObjectsResult, error)
	listV2         func(context.Context, *aliyunoss.ListObjectsV2Request) (*aliyunoss.ListObjectsV2Result, error)
	initMultipart  func(context.Context, *aliyunoss.InitiateMultipartUploadRequest) (*aliyunoss.InitiateMultipartUploadResult, error)
	uploadPart     func(context.Context, *aliyunoss.UploadPartRequest) (*aliyunoss.UploadPartResult, error)
	completeMulti  func(context.Context, *aliyunoss.CompleteMultipartUploadRequest) (*aliyunoss.CompleteMultipartUploadResult, error)
	abortMultipart func(context.Context, *aliyunoss.AbortMultipartUploadRequest) (*aliyunoss.AbortMultipartUploadResult, error)
}

func (m *mockOSSClient) PutObject(ctx context.Context, r *aliyunoss.PutObjectRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.PutObjectResult, error) {
	return m.putObject(ctx, r)
}
func (m *mockOSSClient) GetObject(ctx context.Context, r *aliyunoss.GetObjectRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.GetObjectResult, error) {
	return m.getObject(ctx, r)
}
func (m *mockOSSClient) DeleteObject(ctx context.Context, r *aliyunoss.DeleteObjectRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.DeleteObjectResult, error) {
	return m.deleteObject(ctx, r)
}
func (m *mockOSSClient) HeadObject(ctx context.Context, r *aliyunoss.HeadObjectRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.HeadObjectResult, error) {
	return m.headObject(ctx, r)
}
func (m *mockOSSClient) DeleteMultipleObjects(ctx context.Context, r *aliyunoss.DeleteMultipleObjectsRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.DeleteMultipleObjectsResult, error) {
	return m.deleteMulti(ctx, r)
}
func (m *mockOSSClient) ListObjectsV2(ctx context.Context, r *aliyunoss.ListObjectsV2Request, _ ...func(*aliyunoss.Options)) (*aliyunoss.ListObjectsV2Result, error) {
	return m.listV2(ctx, r)
}
func (m *mockOSSClient) InitiateMultipartUpload(ctx context.Context, r *aliyunoss.InitiateMultipartUploadRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.InitiateMultipartUploadResult, error) {
	return m.initMultipart(ctx, r)
}
func (m *mockOSSClient) UploadPart(ctx context.Context, r *aliyunoss.UploadPartRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.UploadPartResult, error) {
	return m.uploadPart(ctx, r)
}
func (m *mockOSSClient) CompleteMultipartUpload(ctx context.Context, r *aliyunoss.CompleteMultipartUploadRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.CompleteMultipartUploadResult, error) {
	return m.completeMulti(ctx, r)
}
func (m *mockOSSClient) AbortMultipartUpload(ctx context.Context, r *aliyunoss.AbortMultipartUploadRequest, _ ...func(*aliyunoss.Options)) (*aliyunoss.AbortMultipartUploadResult, error) {
	return m.abortMultipart(ctx, r)
}

func newMockOSSStorage(c ossClient) *OssStorage {
	return &OssStorage{client: c, bucket: "bk", region: "cn-hangzhou", endpoint: "", useCName: false, publicBaseURL: "https://cdn.example.com", baseDir: "prefix"}
}

func TestOSS_UploadDownloadDelete(t *testing.T) {
	var gotKey string
	m := &mockOSSClient{
		putObject: func(_ context.Context, r *aliyunoss.PutObjectRequest) (*aliyunoss.PutObjectResult, error) {
			gotKey = *r.Key
			return &aliyunoss.PutObjectResult{}, nil
		},
		getObject: func(_ context.Context, r *aliyunoss.GetObjectRequest) (*aliyunoss.GetObjectResult, error) {
			return &aliyunoss.GetObjectResult{Body: io.NopCloser(bytes.NewReader([]byte("hello")))}, nil
		},
		deleteObject: func(_ context.Context, r *aliyunoss.DeleteObjectRequest) (*aliyunoss.DeleteObjectResult, error) {
			return &aliyunoss.DeleteObjectResult{}, nil
		},
	}
	s := newMockOSSStorage(m)
	require.NoError(t, s.Upload(context.Background(), "a/b.txt", bytes.NewReader([]byte("hello"))))
	assert.Equal(t, "prefix/a/b.txt", gotKey) // baseDir 前缀生效

	rc, err := s.Download(context.Background(), "a/b.txt")
	require.NoError(t, err)
	b, _ := io.ReadAll(rc)
	rc.Close()
	assert.Equal(t, "hello", string(b))

	require.NoError(t, s.Delete(context.Background(), "a/b.txt"))
}

func TestOSS_Exists_Stat(t *testing.T) {
	notFound := &aliyunoss.ServiceError{StatusCode: 404, Code: "NoSuchKey"}
	m := &mockOSSClient{
		headObject: func(_ context.Context, r *aliyunoss.HeadObjectRequest) (*aliyunoss.HeadObjectResult, error) {
			// 实现会把 key 拼成完整 objectKey（baseDir="prefix" 前缀）
			if *r.Key == "prefix/missing" {
				return nil, notFound
			}
			return &aliyunoss.HeadObjectResult{ContentLength: 42}, nil
		},
	}
	s := newMockOSSStorage(m)
	exists, err := s.Exists(context.Background(), "missing")
	require.NoError(t, err)
	assert.False(t, exists)

	exists, err = s.Exists(context.Background(), "present")
	require.NoError(t, err)
	assert.True(t, exists)

	item, err := s.Stat(context.Background(), "present")
	require.NoError(t, err)
	assert.Equal(t, int64(42), item.Size)
}

func TestOSS_BatchDelete_Quiet(t *testing.T) {
	var seenQuiet bool
	m := &mockOSSClient{
		deleteMulti: func(_ context.Context, r *aliyunoss.DeleteMultipleObjectsRequest) (*aliyunoss.DeleteMultipleObjectsResult, error) {
			seenQuiet = r.Delete.Quiet
			// 静默模式不返回已删除列表
			return &aliyunoss.DeleteMultipleObjectsResult{}, nil
		},
	}
	s := newMockOSSStorage(m)
	deleted, err := s.BatchDelete(context.Background(), []string{"a", "b"}, storage.WithQuiet(true))
	require.NoError(t, err)
	assert.True(t, seenQuiet)
	assert.Empty(t, deleted) // quiet 模式无返回
}

func TestOSS_BatchDelete_Verbose(t *testing.T) {
	var seenQuiet bool
	m := &mockOSSClient{
		deleteMulti: func(_ context.Context, r *aliyunoss.DeleteMultipleObjectsRequest) (*aliyunoss.DeleteMultipleObjectsResult, error) {
			seenQuiet = r.Delete.Quiet
			// 非静默模式：服务端返回已删除对象完整 objectKey（含 baseDir 前缀）
			return &aliyunoss.DeleteMultipleObjectsResult{
				DeletedObjects: []aliyunoss.DeletedInfo{
					{Key: aliyunoss.Ptr("prefix/a")},
					{Key: aliyunoss.Ptr("prefix/b")},
				},
			}, nil
		},
	}
	s := newMockOSSStorage(m)
	// 注意：不设置 WithQuiet(true)，走 verbose 分支
	deleted, err := s.BatchDelete(context.Background(), []string{"a", "b"})
	require.NoError(t, err)
	assert.False(t, seenQuiet)
	// baseDir("prefix")+"/" 前缀被剥离，还原为相对 key
	assert.Equal(t, []string{"a", "b"}, deleted)
}

func TestOSS_PrefixIterator_Pagination(t *testing.T) {
	calls := 0
	m := &mockOSSClient{
		listV2: func(_ context.Context, r *aliyunoss.ListObjectsV2Request) (*aliyunoss.ListObjectsV2Result, error) {
			calls++
			if calls == 1 {
				return &aliyunoss.ListObjectsV2Result{
					IsTruncated:           true,
					NextContinuationToken: aliyunoss.Ptr("tok2"),
					Contents: []aliyunoss.ObjectProperties{
						{Key: aliyunoss.Ptr("prefix/1"), Size: 10},
					},
				}, nil
			}
			return &aliyunoss.ListObjectsV2Result{
				IsTruncated: false,
				Contents: []aliyunoss.ObjectProperties{
					{Key: aliyunoss.Ptr("prefix/2"), Size: 20},
				},
			}, nil
		},
	}
	s := newMockOSSStorage(m)
	var keys []string
	err := s.PrefixIterator(context.Background(), "prefix", func(_ context.Context, items ...storage.ObjectItem) error {
		for _, it := range items {
			keys = append(keys, it.Key)
		}
		return nil
	})
	require.NoError(t, err)
	// mock 返回完整 objectKey "prefix/1"、"prefix/2"；PrefixIterator 剥离 baseDir("prefix")+"/" 前缀后得到相对 key "1"、"2"
	assert.Equal(t, []string{"1", "2"}, keys)
	assert.Equal(t, 2, calls)
}

func TestOSS_MultipartFlow(t *testing.T) {
	m := &mockOSSClient{
		initMultipart: func(_ context.Context, r *aliyunoss.InitiateMultipartUploadRequest) (*aliyunoss.InitiateMultipartUploadResult, error) {
			return &aliyunoss.InitiateMultipartUploadResult{Bucket: aliyunoss.Ptr("bk"), Key: r.Key, UploadId: aliyunoss.Ptr("uid-1")}, nil
		},
		uploadPart: func(_ context.Context, r *aliyunoss.UploadPartRequest) (*aliyunoss.UploadPartResult, error) {
			return &aliyunoss.UploadPartResult{ETag: aliyunoss.Ptr("etag-" + (*r.UploadId))}, nil
		},
		completeMulti: func(_ context.Context, r *aliyunoss.CompleteMultipartUploadRequest) (*aliyunoss.CompleteMultipartUploadResult, error) {
			if *r.UploadId != "uid-1" {
				return nil, errors.New("bad upload id")
			}
			return &aliyunoss.CompleteMultipartUploadResult{}, nil
		},
		abortMultipart: func(_ context.Context, r *aliyunoss.AbortMultipartUploadRequest) (*aliyunoss.AbortMultipartUploadResult, error) {
			return &aliyunoss.AbortMultipartUploadResult{}, nil
		},
	}
	s := newMockOSSStorage(m)
	ctx := context.Background()
	sess, err := s.InitiateMultipartUpload(ctx, "big/file")
	require.NoError(t, err)
	assert.Equal(t, "uid-1", sess.UploadID)
	assert.Equal(t, "prefix/big/file", sess.Key) // 完整 objectKey

	pr, err := s.MultipartUpload(ctx, sess, 1, 5, bytes.NewReader([]byte("part1")))
	require.NoError(t, err)
	assert.Equal(t, 1, pr.PartNumber)
	assert.Equal(t, "etag-uid-1", pr.ETag)

	require.NoError(t, s.CompleteMultipartUpload(ctx, sess, []storage.UploadPartResponse{{PartNumber: 1, ETag: "etag-uid-1"}}))
	require.NoError(t, s.CancelMultipartUpload(ctx, sess))
}
