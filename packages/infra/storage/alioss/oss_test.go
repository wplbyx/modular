package alioss

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	aliyunoss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wplbyx/modular/packages/config/configitem"
	"github.com/wplbyx/modular/packages/infra/storage"
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

func newPresignTestStorage(t *testing.T) *OssStorage {
	t.Helper()
	cfg := aliyunoss.LoadDefaultConfig().
		WithRegion("cn-hangzhou").
		WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test-ak", "test-sk")).
		WithEndpoint("oss-cn-hangzhou.aliyuncs.com")
	return &OssStorage{
		client:        aliyunoss.NewClient(cfg),
		bucket:        "test-bucket",
		region:        "cn-hangzhou",
		endpoint:      "https://oss-cn-hangzhou.aliyuncs.com",
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

func TestOSS_ImplementsDirectStorage(t *testing.T) {
	var _ storage.DirectStorage = (*OssStorage)(nil)
}

func TestOSS_PresignUploadAndDownload(t *testing.T) {
	s := newPresignTestStorage(t)
	expires := 15 * time.Minute

	upload, err := s.PresignUpload(context.Background(), "images/a.png", storage.DirectUploadOptions{
		Expires:         expires,
		ContentType:     "image/png",
		ContentMD5:      "1B2M2Y8AsgTpgAmY7PhCfg==",
		Meta:            map[string]string{"tenant": "acme"},
		ForbidOverwrite: true,
		Callback:        "callback-base64",
		CallbackVar:     "callback-var-base64",
	})
	require.NoError(t, err)

	assert.Equal(t, "prefix/images/a.png", upload.Key)
	assert.Equal(t, http.MethodPut, upload.Method)
	assert.Contains(t, upload.URL, "test-bucket.oss-cn-hangzhou.aliyuncs.com/prefix/images/a.png")
	assert.True(t, strings.Contains(upload.URL, "Signature=") || strings.Contains(upload.URL, "x-oss-signature="), "url=%s", upload.URL)
	assert.NotContains(t, upload.URL, "test-sk")
	assert.NotContains(t, upload.URL, "test-token")
	assert.Equal(t, "https://cdn.example.com/prefix/images/a.png", upload.PublicURL)
	assert.WithinDuration(t, time.Now().Add(expires), upload.ExpiresAt, 3*time.Second)
	assert.Equal(t, "image/png", upload.Headers.Get("Content-Type"))
	assert.Equal(t, "1B2M2Y8AsgTpgAmY7PhCfg==", upload.Headers.Get("Content-MD5"))
	assert.Equal(t, "acme", upload.Headers.Get("x-oss-meta-tenant"))
	assert.Equal(t, "true", upload.Headers.Get("x-oss-forbid-overwrite"))
	assert.Equal(t, "callback-base64", upload.Headers.Get("x-oss-callback"))
	assert.Equal(t, "callback-var-base64", upload.Headers.Get("x-oss-callback-var"))
	assert.Empty(t, upload.Body)

	download, err := s.PresignDownload(context.Background(), "images/a.png", storage.DirectDownloadOptions{Expires: expires})
	require.NoError(t, err)
	assert.Equal(t, "prefix/images/a.png", download.Key)
	assert.Equal(t, http.MethodGet, download.Method)
	assert.Contains(t, download.URL, "test-bucket.oss-cn-hangzhou.aliyuncs.com/prefix/images/a.png")
	assert.Empty(t, download.Headers)
	assert.Empty(t, download.Body)
	assert.Equal(t, "https://cdn.example.com/prefix/images/a.png", download.PublicURL)
}

func TestOSS_PresignMultipartDirectUpload(t *testing.T) {
	s := newPresignTestStorage(t)

	initReq, err := s.PresignMultipartInitiate(context.Background(), "videos/movie.mp4", storage.DirectMultipartInitiateOptions{
		Expires:     time.Minute,
		ContentType: "video/mp4",
	})
	require.NoError(t, err)
	assert.Equal(t, "prefix/videos/movie.mp4", initReq.Key)
	assert.Equal(t, http.MethodPost, initReq.Method)
	assert.Contains(t, initReq.URL, "uploads")
	assert.Equal(t, "video/mp4", initReq.Headers.Get("Content-Type"))
	assert.Empty(t, initReq.Body)

	partReq, err := s.PresignMultipartUploadPart(context.Background(), "videos/movie.mp4", "upload-1", 2, storage.DirectMultipartPartOptions{
		Expires:    time.Minute,
		ContentMD5: "1B2M2Y8AsgTpgAmY7PhCfg==",
	})
	require.NoError(t, err)
	assert.Equal(t, "prefix/videos/movie.mp4", partReq.Key)
	assert.Equal(t, http.MethodPut, partReq.Method)
	assert.Contains(t, partReq.URL, "uploadId=upload-1")
	assert.Contains(t, partReq.URL, "partNumber=2")
	assert.Equal(t, "1B2M2Y8AsgTpgAmY7PhCfg==", partReq.Headers.Get("Content-MD5"))
	assert.Empty(t, partReq.Body)

	completeReq, err := s.PresignMultipartComplete(context.Background(), "videos/movie.mp4", "upload-1", []storage.UploadPartResponse{
		{PartNumber: 3, ETag: "etag-3"},
		{PartNumber: 1, ETag: "etag-1"},
		{PartNumber: 2, ETag: "etag-2"},
	}, storage.DirectMultipartCompleteOptions{
		Expires:     time.Minute,
		Callback:    "callback-base64",
		CallbackVar: "callback-var-base64",
	})
	require.NoError(t, err)
	assert.Equal(t, "prefix/videos/movie.mp4", completeReq.Key)
	assert.Equal(t, http.MethodPost, completeReq.Method)
	assert.Contains(t, completeReq.URL, "uploadId=upload-1")
	assert.Equal(t, "callback-base64", completeReq.Headers.Get("x-oss-callback"))
	assert.Equal(t, "callback-var-base64", completeReq.Headers.Get("x-oss-callback-var"))
	assert.Contains(t, string(completeReq.Body), "<PartNumber>1</PartNumber>")
	assert.Less(t, strings.Index(string(completeReq.Body), "<PartNumber>1</PartNumber>"), strings.Index(string(completeReq.Body), "<PartNumber>2</PartNumber>"))
	assert.Less(t, strings.Index(string(completeReq.Body), "<PartNumber>2</PartNumber>"), strings.Index(string(completeReq.Body), "<PartNumber>3</PartNumber>"))

	abortReq, err := s.PresignMultipartAbort(context.Background(), "videos/movie.mp4", "upload-1", storage.DirectMultipartAbortOptions{Expires: time.Minute})
	require.NoError(t, err)
	assert.Equal(t, "prefix/videos/movie.mp4", abortReq.Key)
	assert.Equal(t, http.MethodDelete, abortReq.Method)
	assert.Contains(t, abortReq.URL, "uploadId=upload-1")
	assert.Empty(t, abortReq.Body)
}

func TestOSS_PresignRejectsSecurityToken(t *testing.T) {
	s, err := NewOSSStorage(&configitem.Storage{
		PublicBaseURL: "https://cdn.example.com",
		OSS: &configitem.OSSStorageConfig{
			AccessKeyID:     "test-ak",
			AccessKeySecret: "test-sk",
			SecurityToken:   "test-token",
			Region:          "cn-hangzhou",
			Bucket:          "test-bucket",
			Endpoint:        "oss-cn-hangzhou.aliyuncs.com",
			BaseDir:         "prefix",
		},
	})
	require.NoError(t, err)

	_, err = s.PresignUpload(context.Background(), "images/a.png", storage.DirectUploadOptions{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "security token")
}

func TestOSS_PresignRejectsInvalidInputs(t *testing.T) {
	s := newPresignTestStorage(t)

	_, err := s.PresignUpload(context.Background(), "", storage.DirectUploadOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key is empty")

	_, err = s.PresignDownload(context.Background(), "", storage.DirectDownloadOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key is empty")

	_, err = s.PresignMultipartInitiate(context.Background(), "", storage.DirectMultipartInitiateOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key is empty")

	_, err = s.PresignMultipartUploadPart(context.Background(), "", "upload-1", 1, storage.DirectMultipartPartOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key is empty")

	_, err = s.PresignMultipartComplete(context.Background(), "", "upload-1", []storage.UploadPartResponse{{PartNumber: 1, ETag: "etag-1"}}, storage.DirectMultipartCompleteOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key is empty")

	_, err = s.PresignMultipartAbort(context.Background(), "", "upload-1", storage.DirectMultipartAbortOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key is empty")

	_, err = s.PresignMultipartUploadPart(context.Background(), "file.bin", "upload-1", 0, storage.DirectMultipartPartOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "partNumber must be >= 1")

	_, err = s.PresignMultipartUploadPart(context.Background(), "file.bin", "", 1, storage.DirectMultipartPartOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "uploadID is empty")

	_, err = s.PresignMultipartComplete(context.Background(), "file.bin", "", []storage.UploadPartResponse{{PartNumber: 1, ETag: "etag-1"}}, storage.DirectMultipartCompleteOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "uploadID is empty")

	_, err = s.PresignMultipartAbort(context.Background(), "file.bin", "", storage.DirectMultipartAbortOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "uploadID is empty")

	_, err = s.PresignMultipartComplete(context.Background(), "file.bin", "upload-1", nil, storage.DirectMultipartCompleteOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no parts to complete")

	tooLong := 8 * 24 * time.Hour
	_, err = s.PresignUpload(context.Background(), "file.bin", storage.DirectUploadOptions{Expires: tooLong})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expires must not be greater than 7 days")

	_, err = s.PresignDownload(context.Background(), "file.bin", storage.DirectDownloadOptions{Expires: tooLong})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expires must not be greater than 7 days")

	_, err = s.PresignMultipartInitiate(context.Background(), "file.bin", storage.DirectMultipartInitiateOptions{Expires: tooLong})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expires must not be greater than 7 days")

	_, err = s.PresignMultipartUploadPart(context.Background(), "file.bin", "upload-1", 1, storage.DirectMultipartPartOptions{Expires: tooLong})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expires must not be greater than 7 days")

	_, err = s.PresignMultipartComplete(context.Background(), "file.bin", "upload-1", []storage.UploadPartResponse{{PartNumber: 1, ETag: "etag-1"}}, storage.DirectMultipartCompleteOptions{Expires: tooLong})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expires must not be greater than 7 days")

	_, err = s.PresignMultipartAbort(context.Background(), "file.bin", "upload-1", storage.DirectMultipartAbortOptions{Expires: tooLong})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expires must not be greater than 7 days")
}

func TestOSS_PresignUsesEscapedObjectKeys(t *testing.T) {
	s := newPresignTestStorage(t)

	req, err := s.PresignUpload(context.Background(), "docs/a b.txt", storage.DirectUploadOptions{})
	require.NoError(t, err)
	u, err := url.Parse(req.URL)
	require.NoError(t, err)
	assert.Equal(t, "/prefix/docs/a b.txt", u.Path)
	assert.True(t, strings.Contains(req.URL, "Signature=") || strings.Contains(req.URL, "x-oss-signature="), "url=%s", req.URL)
	assert.WithinDuration(t, time.Now().Add(15*time.Minute), req.ExpiresAt, 3*time.Second)
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
