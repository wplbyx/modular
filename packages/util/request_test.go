package util

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDoRequestRejectsNilInputs(t *testing.T) {
	if err := DoRequest(nil, nil, nil, func([]byte) error { return nil }); err == nil {
		t.Fatal("DoRequest() nil meta error = nil")
	}

	meta := &Meta{Scheme: "http", Host: "example.com", Method: http.MethodGet}
	if err := DoRequest(meta, nil, nil, nil); err == nil {
		t.Fatal("DoRequest() nil callback error = nil")
	}
}

func TestDoRequestReturnsStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("teapot"))
	}))
	defer server.Close()

	meta := metaFromServer(t, server.URL, http.MethodGet)
	err := DoRequest(meta, nil, nil, func([]byte) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "status 418") || !strings.Contains(err.Error(), "teapot") {
		t.Fatalf("DoRequest() error = %v, want status body", err)
	}
}

func TestDoRequestWithContextAndClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != "42" {
			t.Fatalf("query id = %q", r.URL.Query().Get("id"))
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	meta := metaFromServer(t, server.URL, http.MethodPost)
	var got []byte
	err := DoRequestWithContext(context.Background(), server.Client(), meta, map[string]interface{}{"id": int64(42)}, map[string]string{"name": "alice"}, func(body []byte) error {
		got = append(got, body...)
		return nil
	})
	if err != nil {
		t.Fatalf("DoRequestWithContext() error = %v", err)
	}
	if !bytes.Contains(got, []byte(`"ok"`)) {
		t.Fatalf("response body = %q", got)
	}
}

func TestDoRequestPropagatesCallbackError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	callbackErr := errors.New("callback boom")
	meta := metaFromServer(t, server.URL, http.MethodGet)
	err := DoRequest(meta, nil, nil, func([]byte) error { return callbackErr })
	if !errors.Is(err, callbackErr) {
		t.Fatalf("DoRequest() error = %v, want callbackErr", err)
	}
}

func metaFromServer(t *testing.T, rawURL string, method string) *Meta {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	return &Meta{Scheme: parsed.Scheme, Host: parsed.Host, Path: parsed.Path, Method: method}
}
