package filedisk

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wplbyx/modular/packages/infra/storage"
)

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestDiskStorage_FailedOverwritePreservesObject(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	require.NoError(t, s.Upload(ctx, "object", strings.NewReader("original")))
	require.Error(t, s.Upload(ctx, "object", io.MultiReader(strings.NewReader("partial"), brokenReader{})))
	body, err := s.Download(ctx, "object")
	require.NoError(t, err)
	defer body.Close()
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, "original", string(data))
}

func TestDiskStorage_InvalidSessionsAndFailedMerge(t *testing.T) {
	s := newTestStorage(t)
	ctx := context.Background()
	for _, id := range []string{"", "..", "../outside", "not-a-uuid"} {
		session := storage.MultipartUploadSession{UploadID: id, Key: "object"}
		require.Error(t, s.CancelMultipartUpload(ctx, session))
		_, err := s.MultipartUpload(ctx, session, 1, 1, strings.NewReader("x"))
		require.Error(t, err)
	}
	require.NoError(t, s.Upload(ctx, "object", strings.NewReader("original")))
	session, err := s.InitiateMultipartUpload(ctx, "object")
	require.NoError(t, err)
	defer s.CancelMultipartUpload(ctx, session)
	part, err := s.MultipartUpload(ctx, session, 1, 3, strings.NewReader("new"))
	require.NoError(t, err)
	require.Error(t, s.CompleteMultipartUpload(ctx, session, []storage.UploadPartResponse{part, {PartNumber: 2}}))
	body, err := s.Download(ctx, "object")
	require.NoError(t, err)
	defer body.Close()
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, "original", string(data))
}

func TestDiskStorage_RejectsSymlinkEscape(t *testing.T) {
	s := newTestStorage(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(s.rootDir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	require.Error(t, s.Upload(context.Background(), "link/object", strings.NewReader("escape")))
	_, err := os.Stat(filepath.Join(outside, "object"))
	require.True(t, os.IsNotExist(err))
}

func TestDiskStorage_RejectsForeignMultipartSession(t *testing.T) {
	s := newTestStorage(t)
	other := newTestStorage(t)
	ctx := context.Background()
	session, err := s.InitiateMultipartUpload(ctx, "object")
	require.NoError(t, err)
	defer s.CancelMultipartUpload(ctx, session)
	_, err = other.MultipartUpload(ctx, session, 1, 1, strings.NewReader("x"))
	require.Error(t, err)
	changed := storage.MultipartUploadSession{UploadID: session.UploadID, Key: "another"}
	require.Error(t, s.CancelMultipartUpload(ctx, changed))
}
