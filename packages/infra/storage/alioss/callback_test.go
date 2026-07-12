package alioss

import (
	"bytes"
	"context"
	"crypto"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifyCallbackSignature_AcceptsValidOSSRequest(t *testing.T) {
	privateKey, publicKeyPEM := testCallbackKeyPair(t)
	body := []byte(`{"bucket":"test-bucket","object":"prefix/a.txt","size":"42"}`)
	req := signedCallbackRequest(t, privateKey, "/callbacks/oss?tenant=acme", body)

	err := VerifyCallbackSignature(req, body, func(ctx context.Context, publicKeyURL string) ([]byte, error) {
		assert.Equal(t, "https://gosspublic.alicdn.com/test-public-key.pem", publicKeyURL)
		return publicKeyPEM, nil
	})
	require.NoError(t, err)
}

func TestVerifyCallbackSignature_RejectsTamperedBody(t *testing.T) {
	privateKey, publicKeyPEM := testCallbackKeyPair(t)
	body := []byte(`{"object":"prefix/a.txt"}`)
	req := signedCallbackRequest(t, privateKey, "/callbacks/oss", body)

	err := VerifyCallbackSignature(req, []byte(`{"object":"prefix/b.txt"}`), func(ctx context.Context, publicKeyURL string) ([]byte, error) {
		return publicKeyPEM, nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signature verification failed")
}

func TestVerifyCallbackSignature_RejectsInvalidPublicKeyURL(t *testing.T) {
	privateKey, _ := testCallbackKeyPair(t)
	body := []byte(`{"object":"prefix/a.txt"}`)
	req := signedCallbackRequest(t, privateKey, "/callbacks/oss", body)
	req.Header.Set("x-oss-pub-key-url", base64.StdEncoding.EncodeToString([]byte("https://evil.example.com/key.pem")))

	err := VerifyCallbackSignature(req, body, func(ctx context.Context, publicKeyURL string) ([]byte, error) {
		t.Fatalf("fetcher should not be called for invalid public key URL")
		return nil, nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "public key url is not allowed")
}

func TestVerifyCallbackSignature_UsesURLDecodedPathAndRawQuery(t *testing.T) {
	privateKey, publicKeyPEM := testCallbackKeyPair(t)
	body := []byte(`{"object":"prefix/a/b.txt"}`)
	req := httptest.NewRequest(http.MethodPost, "/callbacks/a%2Fb?x=a%2Fb", bytes.NewReader(body))
	req.Header.Set("x-oss-pub-key-url", base64.StdEncoding.EncodeToString([]byte("https://gosspublic.alicdn.com/test-public-key.pem")))
	req.Header.Set("Authorization", signCallbackAuth(t, privateKey, "/callbacks/a/b?x=a%2Fb\n"+string(body)))

	err := VerifyCallbackSignature(req, body, func(ctx context.Context, publicKeyURL string) ([]byte, error) {
		return publicKeyPEM, nil
	})
	require.NoError(t, err)
}

func TestParseCallbackPayload_SupportsJSONAndForm(t *testing.T) {
	jsonReq := httptest.NewRequest(http.MethodPost, "/callbacks/oss", strings.NewReader(`{"bucket":"test-bucket","size":42,"ok":true}`))
	jsonReq.Header.Set("Content-Type", "application/json")

	payload, err := ParseCallbackPayload(jsonReq, []byte(`{"bucket":"test-bucket","size":42,"ok":true}`))
	require.NoError(t, err)
	assert.Equal(t, "test-bucket", payload.Values["bucket"])
	assert.Equal(t, "42", payload.Values["size"])
	assert.Equal(t, "true", payload.Values["ok"])

	formReq := httptest.NewRequest(http.MethodPost, "/callbacks/oss", strings.NewReader("bucket=test-bucket&object=prefix%2Fa.txt"))
	formReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	payload, err = ParseCallbackPayload(formReq, []byte("bucket=test-bucket&object=prefix%2Fa.txt"))
	require.NoError(t, err)
	assert.Equal(t, "test-bucket", payload.Values["bucket"])
	assert.Equal(t, "prefix/a.txt", payload.Values["object"])
}

func TestCallbackHandler_VerifiesParsesAndProcessesRequest(t *testing.T) {
	privateKey, publicKeyPEM := testCallbackKeyPair(t)
	body := []byte(`{"bucket":"test-bucket","object":"prefix/a.txt"}`)
	req := signedCallbackRequest(t, privateKey, "/callbacks/oss", body)
	req.Header.Set("Content-Type", "application/json")

	var received CallbackPayload
	handler := NewCallbackHandler(func(ctx context.Context, payload CallbackPayload) error {
		received = payload
		return nil
	}, WithCallbackPublicKeyFetcher(func(ctx context.Context, publicKeyURL string) ([]byte, error) {
		return publicKeyPEM, nil
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"Status":"OK"}`, w.Body.String())
	assert.Equal(t, "prefix/a.txt", received.Values["object"])
}

func TestCallbackHandler_RejectsOversizedBodyBeforeVerification(t *testing.T) {
	privateKey, _ := testCallbackKeyPair(t)
	body := []byte(`{"object":"` + strings.Repeat("a", 64) + `"}`)
	req := signedCallbackRequest(t, privateKey, "/callbacks/oss", body)
	req.Header.Set("Content-Type", "application/json")

	handler := NewCallbackHandler(func(ctx context.Context, payload CallbackPayload) error {
		t.Fatalf("processor should not be called for oversized body")
		return nil
	}, WithCallbackMaxBodyBytes(16), WithCallbackPublicKeyFetcher(func(ctx context.Context, publicKeyURL string) ([]byte, error) {
		t.Fatalf("public key fetcher should not be called for oversized body")
		return nil, nil
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestCallbackHandler_RejectsBadMethodAndProcessorError(t *testing.T) {
	privateKey, publicKeyPEM := testCallbackKeyPair(t)
	handler := NewCallbackHandler(func(ctx context.Context, payload CallbackPayload) error {
		return errors.New("processor failed")
	}, WithCallbackPublicKeyFetcher(func(ctx context.Context, publicKeyURL string) ([]byte, error) {
		return publicKeyPEM, nil
	}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/callbacks/oss", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)

	body := []byte(`{"object":"prefix/a.txt"}`)
	req := signedCallbackRequest(t, privateKey, "/callbacks/oss", body)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCallbackHandler_ProcessesLargeSignedBody(t *testing.T) {
	privateKey, publicKeyPEM := testCallbackKeyPair(t)
	body := []byte(`{"object":"` + strings.Repeat("a", 1<<20) + `"}`)
	req := signedCallbackRequest(t, privateKey, "/callbacks/oss", body)
	req.Header.Set("Content-Type", "application/json")

	var received CallbackPayload
	handler := NewCallbackHandler(func(ctx context.Context, payload CallbackPayload) error {
		received = payload
		return nil
	}, WithCallbackPublicKeyFetcher(func(ctx context.Context, publicKeyURL string) ([]byte, error) {
		return publicKeyPEM, nil
	}), WithCallbackMaxBodyBytes(int64(len(body))))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, received.Values["object"], 1<<20)
}

func TestCallbackHandler_CachesPublicKeyByDefault(t *testing.T) {
	privateKey, publicKeyPEM := testCallbackKeyPair(t)
	body := []byte(`{"object":"prefix/a.txt"}`)
	var fetches int32
	handler := NewCallbackHandler(func(ctx context.Context, payload CallbackPayload) error {
		return nil
	}, WithCallbackPublicKeyFetcher(func(ctx context.Context, publicKeyURL string) ([]byte, error) {
		atomic.AddInt32(&fetches, 1)
		return publicKeyPEM, nil
	}))

	for range 2 {
		req := signedCallbackRequest(t, privateKey, "/callbacks/oss", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&fetches))
}

func TestCallbackHandler_CanDisablePublicKeyCache(t *testing.T) {
	privateKey, publicKeyPEM := testCallbackKeyPair(t)
	body := []byte(`{"object":"prefix/a.txt"}`)
	var fetches int32
	handler := NewCallbackHandler(func(ctx context.Context, payload CallbackPayload) error {
		return nil
	}, WithCallbackPublicKeyCacheTTL(0), WithCallbackPublicKeyFetcher(func(ctx context.Context, publicKeyURL string) ([]byte, error) {
		atomic.AddInt32(&fetches, 1)
		return publicKeyPEM, nil
	}))

	for range 2 {
		req := signedCallbackRequest(t, privateKey, "/callbacks/oss", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	}
	assert.Equal(t, int32(2), atomic.LoadInt32(&fetches))
}

func TestCallbackHandler_RefetchesExpiredPublicKeyCache(t *testing.T) {
	privateKey, publicKeyPEM := testCallbackKeyPair(t)
	body := []byte(`{"object":"prefix/a.txt"}`)
	var fetches int32
	handler := NewCallbackHandler(func(ctx context.Context, payload CallbackPayload) error {
		return nil
	}, WithCallbackPublicKeyCacheTTL(time.Nanosecond), WithCallbackPublicKeyFetcher(func(ctx context.Context, publicKeyURL string) ([]byte, error) {
		atomic.AddInt32(&fetches, 1)
		return publicKeyPEM, nil
	}))

	req := signedCallbackRequest(t, privateKey, "/callbacks/oss", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	time.Sleep(time.Millisecond)

	req = signedCallbackRequest(t, privateKey, "/callbacks/oss", body)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int32(2), atomic.LoadInt32(&fetches))
}

func signedCallbackRequest(t *testing.T, privateKey *rsa.PrivateKey, target string, body []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	publicKeyURL := "https://gosspublic.alicdn.com/test-public-key.pem"
	req.Header.Set("x-oss-pub-key-url", base64.StdEncoding.EncodeToString([]byte(publicKeyURL)))
	req.Header.Set("Authorization", signCallbackAuth(t, privateKey, callbackStringToSign(req, body)))
	return req
}

func signCallbackAuth(t *testing.T, privateKey *rsa.PrivateKey, stringToSign string) string {
	t.Helper()
	sum := md5.Sum([]byte(stringToSign))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.MD5, sum[:])
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(signature)
}

func testCallbackKeyPair(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)
	return privateKey, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}
