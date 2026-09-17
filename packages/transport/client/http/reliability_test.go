package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestClient_ResponseLimitAndStreamingUpload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			reader, err := r.MultipartReader()
			if err != nil {
				http.Error(w, "bad multipart", 400)
				return
			}
			part, err := reader.NextPart()
			if err != nil {
				http.Error(w, "no part", 400)
				return
			}
			data, err := io.ReadAll(part)
			if err != nil || string(data) != "streamed" {
				http.Error(w, "wrong body", 400)
				return
			}
		}
		_, _ = io.WriteString(w, "12345")
	}))
	defer server.Close()
	client := NewClient(&Config{MaxResponseBytes: 4})
	_, err := client.Get(context.Background(), server.URL, nil)
	require.ErrorIs(t, err, ErrResponseTooLarge)
	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, []byte("streamed"), 0600))
	client = NewClient(&Config{MaxResponseBytes: 5})
	data, err := client.PostMultipartStream(context.Background(), server.URL, nil, map[string]string{"file": path})
	require.NoError(t, err)
	require.Equal(t, "12345", string(data))
}
