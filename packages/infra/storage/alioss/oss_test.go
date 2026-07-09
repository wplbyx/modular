package alioss

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"modular/packages/infra/storage"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestStorage 起一个本地 httptest.Server，构造指向它的真实 *oss.Client。
// 与官方 SDK 自测（client_mock_test.go）同款：用 WithEndpoint 把请求劫持到本地。
func newTestStorage(t *testing.T, h http.HandlerFunc) *OssStorage {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cfg := aliyunoss.LoadDefaultConfig().
		WithRegion("cn-hangzhou").
		WithCredentialsProvider(credentials.NewAnonymousCredentialsProvider()).
		WithEndpoint(srv.URL)
	return &OssStorage{
		client:        aliyunoss.NewClient(cfg),
		bucket:        "test-bucket",
		region:        "cn-hangzhou",
		endpoint:      "",
		useCName:      false,
		publicBaseURL: "https://cdn.example.com",
		baseDir:       "prefix",
	}
}

// writeOSSError 回报一个 OSS 风格的 XML 错误（如 404 NoSuchKey）。
func writeOSSError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("X-Oss-Request-Id", "test-req-id")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<Error>
  <Code>%s</Code>
  <Message>not found</Message>
  <RequestId>test-req-id</RequestId>
</Error>`, code)
}

func TestOSS_UploadDownloadDelete(t *testing.T) {
	var gotPath string
	s := newTestStorage(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut: // Upload → PUT /test-bucket/{objectKey}
			gotPath = r.URL.Path
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", "etag-1")
			w.WriteHeader(http.StatusOK)
		case http.MethodGet: // Download → GET /test-bucket/{objectKey}
			w.Header().Set("Content-Length", "5")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("hello"))
		case http.MethodDelete: // Delete → DELETE /test-bucket/{objectKey}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	})

	// Upload：路径应以 objectKey（含 baseDir 前缀）结尾，nil option 应被忽略。
	require.NoError(t, s.Upload(context.Background(), "a/b.txt", bytes.NewReader([]byte("hello")), storage.IOConfigOptionFunc(nil)))
	assert.True(t, strings.HasSuffix(gotPath, "prefix/a/b.txt"), "path=%s", gotPath)

	// Download：应拿到上传内容
	rc, err := s.Download(context.Background(), "a/b.txt")
	require.NoError(t, err)
	b, _ := io.ReadAll(rc)
	rc.Close()
	assert.Equal(t, "hello", string(b))

	require.NoError(t, s.Delete(context.Background(), "a/b.txt"))
}

func TestOSS_Exists_GetMeta(t *testing.T) {
	s := newTestStorage(t, func(w http.ResponseWriter, r *http.Request) {
		// Exists / GetMeta 都走 HeadObject，路径形如 /test-bucket/prefix/{key}
		if r.Method != http.MethodHead {
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if strings.HasSuffix(r.URL.Path, "prefix/missing") {
			writeOSSError(w, http.StatusNotFound, "NoSuchKey")
			return
		}
		w.Header().Set("Content-Length", "42")
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		w.WriteHeader(http.StatusOK)
	})

	// missing → NotFound 被识别为 false（不报错）
	exists, err := s.Exists(context.Background(), "missing")
	require.NoError(t, err)
	assert.False(t, exists)

	// present → true
	exists, err = s.Exists(context.Background(), "present")
	require.NoError(t, err)
	assert.True(t, exists)

	// GetMeta → Content-Length 透传为 Size
	item, err := s.GetMeta(context.Background(), "present")
	require.NoError(t, err)
	assert.Equal(t, int64(42), item.Size)
}

func TestOSS_BatchDelete_Quiet(t *testing.T) {
	var seenQuiet bool
	s := newTestStorage(t, func(w http.ResponseWriter, r *http.Request) {
		// POST /?delete= 请求 body XML 里带 <Quiet>...</Quiet>
		body, _ := io.ReadAll(r.Body)
		seenQuiet = strings.Contains(string(body), "<Quiet>true</Quiet>")
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		// quiet 模式不返回已删除列表
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><DeleteResult></DeleteResult>`))
	})

	deleted, err := s.BatchDelete(context.Background(), []string{"a", "b"}, storage.WithQuiet(true))
	require.NoError(t, err)
	assert.True(t, seenQuiet)
	assert.Empty(t, deleted) // quiet 模式无返回
}

func TestOSS_BatchDelete_Verbose(t *testing.T) {
	var seenQuiet bool
	s := newTestStorage(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seenQuiet = strings.Contains(string(body), "<Quiet>true</Quiet>")
		// 非 quiet 模式：服务端回完整 objectKey（含 baseDir 前缀）
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<DeleteResult>
  <Deleted><Key>prefix/a</Key></Deleted>
  <Deleted><Key>prefix/b</Key></Deleted>
</DeleteResult>`))
	})

	// 不设 WithQuiet → 走 verbose 分支
	deleted, err := s.BatchDelete(context.Background(), []string{"a", "b"})
	require.NoError(t, err)
	assert.False(t, seenQuiet)
	// baseDir("prefix")+"/" 前缀被剥离
	assert.Equal(t, []string{"a", "b"}, deleted)
}

func TestOSS_PrefixIterator_Pagination(t *testing.T) {
	calls := 0
	s := newTestStorage(t, func(w http.ResponseWriter, r *http.Request) {
		// GET /?list-type=2&... ，第二页带 continuation-token=tok2
		calls++
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		if calls == 1 {
			// 第一页：截断，给 NextContinuationToken + 一条 key
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <IsTruncated>true</IsTruncated>
  <NextContinuationToken>tok2</NextContinuationToken>
  <Contents><Key>prefix/1</Key><Size>10</Size></Contents>
</ListBucketResult>`))
			return
		}
		// 第二页：收尾
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <IsTruncated>false</IsTruncated>
  <Contents><Key>prefix/2</Key><Size>20</Size></Contents>
</ListBucketResult>`))
	})

	var keys []string
	err := s.PrefixIterator(context.Background(), "prefix", func(_ context.Context, items ...storage.ObjectItem) error {
		for _, it := range items {
			keys = append(keys, it.Key)
		}
		return nil
	})
	require.NoError(t, err)
	// 返回完整 objectKey "prefix/1"/"prefix/2"，剥离 baseDir 前缀后得 "1"/"2"
	assert.Equal(t, []string{"1", "2"}, keys)
	assert.Equal(t, 2, calls)
}

func TestOSS_MultipartFlow(t *testing.T) {
	s := newTestStorage(t, func(w http.ResponseWriter, r *http.Request) {
		// POST /{key}?uploads=            → InitiateMultipartUpload
		// PUT  /{key}?partNumber=&uploadId= → UploadPart
		// POST /{key}?uploadId=            → CompleteMultipartUpload
		// DELETE /{key}?uploadId=          → AbortMultipartUpload
		switch {
		case r.Method == http.MethodPost && r.URL.Query().Has("uploads"):
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<InitiateMultipartUploadResult>
  <Bucket>test-bucket</Bucket>
  <Key>prefix/big/file</Key>
  <UploadId>uid-1</UploadId>
</InitiateMultipartUploadResult>`)
		case r.Method == http.MethodPut && r.URL.Query().Has("uploadId"):
			// UploadPart：回 ETag 头（原样透传，不加引号）
			_, _ = io.Copy(io.Discard, r.Body)
			uid := r.URL.Query().Get("uploadId")
			w.Header().Set("ETag", "etag-"+uid)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.Query().Has("uploadId"):
			// CompleteMultipartUpload：校验 uploadId
			if r.URL.Query().Get("uploadId") != "uid-1" {
				writeOSSError(w, http.StatusBadRequest, "InvalidArgument")
				return
			}
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && r.URL.Query().Has("uploadId"):
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
		}
	})

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

func TestOSS_CompleteMultipartUploadSortsParts(t *testing.T) {
	var completeBody string
	s := newTestStorage(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !r.URL.Query().Has("uploadId") {
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		completeBody = string(body)
		w.WriteHeader(http.StatusOK)
	})

	sess := storage.MultipartUploadSession{UploadID: "uid-1", Key: "prefix/big/file"}
	err := s.CompleteMultipartUpload(context.Background(), sess, []storage.UploadPartResponse{
		{PartNumber: 3, ETag: "etag-3"},
		{PartNumber: 1, ETag: "etag-1"},
		{PartNumber: 2, ETag: "etag-2"},
	})
	require.NoError(t, err)

	idx1 := strings.Index(completeBody, "<PartNumber>1</PartNumber>")
	idx2 := strings.Index(completeBody, "<PartNumber>2</PartNumber>")
	idx3 := strings.Index(completeBody, "<PartNumber>3</PartNumber>")
	if idx1 == -1 || idx2 == -1 || idx3 == -1 || !(idx1 < idx2 && idx2 < idx3) {
		t.Fatalf("multipart complete body not sorted: %s", completeBody)
	}
}

func TestOSSDefaultURLPreservesHTTPEndpoint(t *testing.T) {
	got := ossDefaultURL("bucket", "", "http://oss.example.com", "a/b.txt", false)
	want := "http://bucket.oss.example.com/a/b.txt"
	if got != want {
		t.Fatalf("ossDefaultURL() = %q, want %q", got, want)
	}
}
