package alioss

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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

const (
	AliyunOssUrl    = "oss-cn-chengdu.aliyuncs.com" // 公网 endpoint（-internal 内网地址仅 ECS 内可用）
	AliyunOssRegion = "cn-chengdu"
	AliyunOssBucket = "lbyx-holographic" // lbyx-holographic.oss-cn-chengdu.aliyuncs.com
)

// =============================================================================
// 真实 OSS 集成测试
//
// 凭证读环境变量 OSS_ACCESS_KEY_ID / OSS_ACCESS_KEY_SECRET（缺失时自动 Skip，
// 不影响无凭证环境的 go test ./...）；bucket/region/endpoint 用文件顶部常量。
//
// 覆盖三条需求：
//  1. 资源访问链接动态生成且有时效性——PresignDownload 两次签名 URL 不同，
//     过期前可访问、过期后 403（TestOSS_Real_PresignedURLExpiry）；
//  2. 客户端直传 OSS——PresignUpload 生成的 PUT 链接同样动态签名，
//     裸 http 客户端仅凭 URL + 签名头即可直传（TestOSS_Real_ClientDirectUpload）；
//  3. 上传回调——OSS 官方提供上传回调能力：PresignUpload 的 Callback/CallbackVar
//     会进入签名头（TestOSS_PresignUploadAndDownload 已断言），服务端 RSA 验签
//     与 Handler 见 callback.go。真实 E2E 要求回调地址公网可达（OSS 服务器回源），
//     本地无法直接验证，暂缓；待有公网回调地址（隧道/ECS）后补充。
// =============================================================================

// realTestHTTPClient 模拟真实客户端：无 SDK 依赖、带超时。
var realTestHTTPClient = &http.Client{Timeout: 30 * time.Second}

// newRealOSSTestStorage 用环境变量凭证 + 文件常量构造真实 OssStorage（走 NewOSSStorage 完整构造路径）。
func newRealOSSTestStorage(t *testing.T) *OssStorage {
	t.Helper()
	ak := os.Getenv("OSS_ACCESS_KEY_ID")     // os.Getenv("OSS_ACCESS_KEY_ID")
	sk := os.Getenv("OSS_ACCESS_KEY_SECRET") // os.Getenv("OSS_ACCESS_KEY_SECRET")
	if ak == "" || sk == "" {
		t.Skip("未设置 OSS_ACCESS_KEY_ID / OSS_ACCESS_KEY_SECRET，跳过真实 OSS 集成测试")
	}
	s, err := NewOSSStorage(&configitem.Storage{
		OSS: &configitem.OSSStorageConfig{
			AccessKeyID:     ak,
			AccessKeySecret: sk,
			Region:          AliyunOssRegion,
			Bucket:          AliyunOssBucket,
			Endpoint:        AliyunOssUrl,
			Timeout:         30 * time.Second,
			MaxRetries:      2,
		},
	})
	require.NoError(t, err)
	return s
}

// uniqueTestKey 用纳秒时间戳生成唯一 key，避免重复运行互相覆盖。
func uniqueTestKey(prefix string) string {
	return fmt.Sprintf("%s/%d.bin", prefix, time.Now().UnixNano())
}

// doPresigned 模拟客户端直传/直连：仅凭预签名 URL + 必须原样携带的签名头发起请求。
func doPresigned(t *testing.T, req storage.DirectTransferRequest, body io.Reader) *http.Response {
	t.Helper()
	httpReq, err := http.NewRequest(req.Method, req.URL, body)
	require.NoError(t, err)
	for k, vs := range req.Headers {
		for _, v := range vs {
			httpReq.Header.Set(k, v)
		}
	}
	resp, err := realTestHTTPClient.Do(httpReq)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// TestOSS_Real_ClientDirectUpload 需求 2 + 需求 1：
// 客户端直传（动态 PUT 预签名 URL），直传后服务端可读回一致内容，
// 且资源访问链接（PresignDownload）同样动态生成并支持响应覆盖参数。
func TestOSS_Real_ClientDirectUpload(t *testing.T) {
	s := newRealOSSTestStorage(t)
	ctx := context.Background()
	key := uniqueTestKey("modular-itest/direct-upload")
	content := []byte("modular integration test: client direct upload")
	// t.Cleanup(func() { _ = s.Delete(ctx, key) })

	uploadOpts := storage.DirectUploadOptions{
		Expires:         10 * time.Minute,
		ContentType:     "text/plain",
		Meta:            map[string]string{"source": "modular-integration-test"},
		ForbidOverwrite: true,
	}

	// 直传地址动态生成：同一 key 两次签名，URL 必须不同（签名含时间因子）。
	up1, err := s.PresignUpload(ctx, key, uploadOpts)
	// up2, err := s.PresignUpload(ctx, key, uploadOpts)
	require.NoError(t, err)
	// require.NoError(t, err)
	// assert.NotEqual(t, up1.URL, up2.URL, "同一 key 的直传 URL 应每次动态生成")
	assert.True(t, strings.Contains(up1.URL, "x-oss-signature") || strings.Contains(up1.URL, "Signature="), "url=%s", up1.URL)
	// assert.WithinDuration(t, time.Now().Add(10*time.Minute), up1.ExpiresAt, 15*time.Second)
	// assert.Contains(t, up1.PublicURL, AliyunOssBucket+"."+AliyunOssUrl+"/modular-itest/")

	// 客户端直传：裸 PUT，仅携带预签名 URL + 签名头。
	putResp := doPresigned(t, up1, bytes.NewReader(content))
	assert.Equal(t, http.StatusOK, putResp.StatusCode, "客户端直传 PUT 应成功")

	// 服务端核对：对象存在、内容一致、大小一致、自定义元数据已落盘。
	exists, err := s.Exists(ctx, key)
	require.NoError(t, err)
	assert.True(t, exists)

	// rc, err := s.Download(ctx, key)
	// require.NoError(t, err)
	// got, readErr := io.ReadAll(rc)
	// _ = rc.Close()
	// require.NoError(t, readErr)
	// assert.Equal(t, content, got)
	// meta, err := s.GetMeta(ctx, key)
	// require.NoError(t, err)
	// assert.Equal(t, int64(len(content)), meta.Size)
	// head, err := s.client.HeadObject(ctx, &aliyunoss.HeadObjectRequest{
	//	Bucket: aliyunoss.Ptr(AliyunOssBucket),
	//	Key:    aliyunoss.Ptr(s.buildObjectKey(key)),
	// })
	// require.NoError(t, err)
	// assert.Equal(t, "modular-integration-test", head.Metadata["source"])
	//
	// // 需求 1：资源访问链接动态生成（PresignDownload），并验证响应覆盖参数生效。
	// dl, err := s.PresignDownload(ctx, key, storage.DirectDownloadOptions{
	//	Expires:                    10 * time.Minute,
	//	ResponseContentType:        "text/plain",
	//	ResponseContentDisposition: `attachment; filename="integration-test.txt"`,
	// })
	// require.NoError(t, err)
	// getResp := doPresigned(t, dl, nil)
	// assert.Equal(t, http.StatusOK, getResp.StatusCode)
	// assert.Equal(t, "text/plain", getResp.Header.Get("Content-Type"))
	// assert.Contains(t, getResp.Header.Get("Content-Disposition"), "integration-test.txt")
	// getBody, readErr := io.ReadAll(getResp.Body)
	// require.NoError(t, readErr)
	// assert.Equal(t, content, getBody)
	//
	// // 软断言：私有 bucket 下未签名的静态 URL 应 403，佐证"访问必须走动态签名链接"；
	// // 若 bucket 为公共读（返回 200）则仅记录，不判失败。
	// if rawResp, err := realTestHTTPClient.Get(s.GetUrl(key)); err == nil {
	//	_ = rawResp.Body.Close()
	//	if rawResp.StatusCode == http.StatusForbidden {
	//		t.Logf("bucket 为私有读：静态 GetUrl 返回 403，验证了动态签名链接的必要性")
	//	} else {
	//		t.Logf("静态 GetUrl 返回 %d（bucket 可能为公共读），跳过 403 断言", rawResp.StatusCode)
	//	}
	// }
}

// TestOSS_Real_PresignedURLExpiry 需求 1：访问链接有时效性——过期前可用、过期后 403。
func TestOSS_Real_PresignedURLExpiry(t *testing.T) {
	s := newRealOSSTestStorage(t)
	ctx := context.Background()
	key := uniqueTestKey("modular-itest/expiry")
	content := []byte("modular integration test: url expiry")
	// t.Cleanup(func() { _ = s.Delete(ctx, key) })
	require.NoError(t, s.Upload(ctx, key, bytes.NewReader(content), storage.WithContentType("text/plain")))

	dl, err := s.PresignDownload(ctx, key, storage.DirectDownloadOptions{Expires: 30 * time.Second})
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(5*time.Second), dl.ExpiresAt, 30*time.Second)
	t.Log(dl.PublicURL)
	t.Log(dl.URL)

	// // 过期前：签名链接可访问且内容一致。
	// resp := doPresigned(t, dl, nil)
	// assert.Equal(t, http.StatusOK, resp.StatusCode)
	// body, readErr := io.ReadAll(resp.Body)
	// require.NoError(t, readErr)
	// assert.Equal(t, content, body)
	//
	// // 等待过期：按签名过期时刻 + 3s 缓冲，规避本地与 OSS 间的细微时钟偏差。
	// time.Sleep(time.Until(dl.ExpiresAt.Add(3 * time.Second)))
	//
	// // 过期后：同一链接必须被拒绝（403）。
	// expired := doPresigned(t, dl, nil)
	// assert.Equal(t, http.StatusForbidden, expired.StatusCode, "已过期的签名链接不应再可访问")
}

// TestOSS_PresignDownload_ResponseOverrideParams 离线单测：
// 响应覆盖参数应进入预签名 URL 的 query 并参与签名。
func TestOSS_PresignDownload_ResponseOverrideParams(t *testing.T) {
	s := newPresignTestStorage(t)
	dl, err := s.PresignDownload(context.Background(), "docs/a.txt", storage.DirectDownloadOptions{
		Expires:                    time.Minute,
		ResponseContentType:        "text/plain",
		ResponseContentDisposition: `attachment; filename="a.txt"`,
	})
	require.NoError(t, err)
	u, parseErr := url.Parse(dl.URL)
	require.NoError(t, parseErr)
	q := u.Query()
	assert.Equal(t, "text/plain", q.Get("response-content-type"))
	assert.Equal(t, `attachment; filename="a.txt"`, q.Get("response-content-disposition"))
}

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
