package aliyun_oss

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const testBaseUrl = "https://cdn.example.com/"

func newTestStorage(t *testing.T) *DiskStorage {
	t.Helper()
	s, err := NewDiskStorage(&LocalDiskStorage{
		RootDir: t.TempDir(),
		BaseUrl: testBaseUrl,
	})
	noErr(t, err)
	return s
}

func md5Hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustErr(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func errContains(t *testing.T, err error, sub string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", sub)
	}
	if !strings.Contains(err.Error(), sub) {
		t.Fatalf("error %q does not contain %q", err.Error(), sub)
	}
}

func assertEq[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("got %v, want %v", got, want)
	}
}

func bytesEq(t *testing.T, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Errorf("bytes differ: got %q, want %q", got, want)
	}
}

func assertNotEq[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got == want {
		t.Errorf("got %v, expected different from %v", got, want)
	}
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	noErr(t, err)
	return b
}

// ---------- NewDiskStorage ----------

func TestNewDiskStorage(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		_, err := NewDiskStorage(nil)
		errContains(t, err, "config is nil")
	})

	t.Run("empty root dir", func(t *testing.T) {
		_, err := NewDiskStorage(&LocalDiskStorage{BaseUrl: testBaseUrl})
		errContains(t, err, "RootDir is empty")
	})

	t.Run("creates root dir and resolves abs", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "nested", "root")
		s, err := NewDiskStorage(&LocalDiskStorage{RootDir: root, BaseUrl: testBaseUrl})
		noErr(t, err)
		abs, err := filepath.Abs(root)
		noErr(t, err)
		assertEq(t, s.rootDir, abs)
		info, err := os.Stat(abs)
		noErr(t, err)
		if !info.IsDir() {
			t.Fatalf("root dir not created")
		}
	})

	t.Run("strips scheme and trailing slash from base url", func(t *testing.T) {
		cases := []string{
			"https://cdn.example.com/",
			"http://cdn.example.com",
			"cdn.example.com/",
			"cdn.example.com",
		}
		for _, in := range cases {
			s, err := NewDiskStorage(&LocalDiskStorage{RootDir: t.TempDir(), BaseUrl: in})
			noErr(t, err)
			assertEq(t, s.baseUrl, "cdn.example.com")
		}
	})
}

// ---------- InitDiskStorage / GetDiskStorage (global singleton) ----------

func TestInitAndGetDiskStorage_Singleton(t *testing.T) {
	t.Cleanup(func() { diskStorageInstance = nil })

	if GetDiskStorage() != nil {
		t.Fatal("diskStorageInstance should be nil before init")
	}

	s1, err := InitDiskStorage(&LocalDiskStorage{RootDir: t.TempDir(), BaseUrl: testBaseUrl})
	noErr(t, err)

	if got := GetDiskStorage(); got != s1 {
		t.Fatal("GetDiskStorage should return the initialized singleton")
	}

	s2, err := InitDiskStorage(&LocalDiskStorage{RootDir: t.TempDir(), BaseUrl: testBaseUrl})
	noErr(t, err)
	if GetDiskStorage() != s2 {
		t.Fatal("GetDiskStorage should return the latest singleton after re-init")
	}
}

// ---------- safeFilePath ----------

func TestSafeFilePath(t *testing.T) {
	s := newTestStorage(t)

	t.Run("empty key", func(t *testing.T) {
		_, err := s.safeFilePath("")
		errContains(t, err, "key is empty")
	})

	t.Run("path traversal escapes root", func(t *testing.T) {
		for _, key := range []string{"../secret", "../../etc/passwd", "a/../../escape"} {
			_, err := s.safeFilePath(key)
			if err == nil {
				t.Fatalf("expected escape error for key %q", key)
			}
			if !strings.Contains(err.Error(), "escapes storage root") {
				t.Fatalf("expected escapes error for key %q, got %v", key, err)
			}
		}
	})

	t.Run("normal key under root", func(t *testing.T) {
		p, err := s.safeFilePath("a/b/c.txt")
		noErr(t, err)
		want := filepath.Join(s.rootDir, "a", "b", "c.txt")
		assertEq(t, p, want)
	})
}

// ---------- GetUsefulUrl ----------

func TestGetUsefulUrl(t *testing.T) {
	s := newTestStorage(t)
	assertEq(t, s.GetUsefulUrl(""), "")
	assertEq(t, s.GetUsefulUrl("a/b.txt"), "cdn.example.com/a/b.txt")
	assertEq(t, s.GetUsefulUrl("/a/b.txt"), "cdn.example.com/a/b.txt")
}

// ---------- Exists ----------

func TestExists(t *testing.T) {
	ctx := context.Background()
	s := newTestStorage(t)

	ok, err := s.Exists(ctx, "missing.txt")
	noErr(t, err)
	assertEq(t, ok, false)

	noErr(t, s.Upload(ctx, "present.txt", strings.NewReader("hi")))

	ok, err = s.Exists(ctx, "present.txt")
	noErr(t, err)
	assertEq(t, ok, true)

	_, err = s.Exists(ctx, "../escape")
	mustErr(t, err)
}

// ---------- Upload + Download roundtrip ----------

func TestUploadAndDownload(t *testing.T) {
	ctx := context.Background()
	s := newTestStorage(t)

	body := []byte("hello disk storage \x00 binary ok")
	noErr(t, s.Upload(ctx, "dir/sub/file.txt", bytes.NewReader(body)))
	if _, err := os.Stat(filepath.Join(s.rootDir, "dir", "sub", "file.txt")); err != nil {
		t.Fatalf("parent dirs not created: %v", err)
	}

	r, err := s.Download(ctx, "dir/sub/file.txt")
	noErr(t, err)
	defer r.Close()
	got, err := io.ReadAll(r)
	noErr(t, err)
	bytesEq(t, got, body)

	t.Run("invalid key", func(t *testing.T) {
		mustErr(t, s.Upload(ctx, "../x", strings.NewReader("bad")))
		_, err := s.Download(ctx, "../x")
		mustErr(t, err)
	})
}

// ---------- Delete ----------

func TestDelete(t *testing.T) {
	ctx := context.Background()
	s := newTestStorage(t)

	noErr(t, s.Upload(ctx, "to_del.txt", strings.NewReader("x")))
	noErr(t, s.Delete(ctx, "to_del.txt"))

	// already deleted -> os.Remove returns an error (single Delete does not skip IsNotExist)
	mustErr(t, s.Delete(ctx, "to_del.txt"))

	mustErr(t, s.Delete(ctx, "../escape"))
}

// ---------- Stat ----------

func TestStat(t *testing.T) {
	ctx := context.Background()
	s := newTestStorage(t)

	body := []byte("stat-me-body")
	noErr(t, s.Upload(ctx, "stat/me.txt", bytes.NewReader(body)))

	item, err := s.Stat(ctx, "stat/me.txt")
	noErr(t, err)
	assertEq(t, item.Key, "stat/me.txt")
	assertEq(t, item.Size, int64(len(body)))
	assertNotEq(t, item.LastModified, int64(0))

	_, err = s.Stat(ctx, "missing")
	mustErr(t, err)
}

// ---------- BatchUpload ----------

func TestBatchUpload(t *testing.T) {
	ctx := context.Background()
	s := newTestStorage(t)

	t.Run("empty", func(t *testing.T) {
		noErr(t, s.BatchUpload(ctx, nil))
	})

	t.Run("all succeed", func(t *testing.T) {
		tasks := []UploadTask{
			{Key: "batch/a.txt", Body: strings.NewReader("aaa")},
			{Key: "batch/b.txt", Body: strings.NewReader("bbb")},
			{Key: "batch/c.txt", Body: strings.NewReader("ccc")},
		}
		noErr(t, s.BatchUpload(ctx, tasks))
		for _, tk := range tasks {
			p, _ := s.safeFilePath(tk.Key)
			if _, err := os.Stat(p); err != nil {
				t.Fatalf("file %s not uploaded: %v", tk.Key, err)
			}
		}
	})

	t.Run("aggregates errors but keeps valid uploads", func(t *testing.T) {
		tasks := []UploadTask{
			{Key: "batch2/ok.txt", Body: strings.NewReader("ok")},
			{Key: "../escape", Body: strings.NewReader("bad")},
		}
		err := s.BatchUpload(ctx, tasks)
		mustErr(t, err)
		errContains(t, err, "escape")
		p, _ := s.safeFilePath("batch2/ok.txt")
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("valid task should still be uploaded: %v", err)
		}
	})
}

// ---------- BatchDelete ----------

func TestBatchDelete(t *testing.T) {
	ctx := context.Background()
	s := newTestStorage(t)

	t.Run("empty input", func(t *testing.T) {
		deleted, err := s.BatchDelete(ctx, nil)
		noErr(t, err)
		if len(deleted) != 0 {
			t.Fatalf("expected no deletions, got %v", deleted)
		}
	})

	t.Run("mixed existing and missing and empty key", func(t *testing.T) {
		noErr(t, s.Upload(ctx, "d/1.txt", strings.NewReader("1")))
		noErr(t, s.Upload(ctx, "d/2.txt", strings.NewReader("2")))

		// empty key ignored; missing key is idempotent (skipped, no error)
		keys := []string{"d/1.txt", "d/2.txt", "d/missing.txt", ""}
		deleted, err := s.BatchDelete(ctx, keys)
		noErr(t, err)
		if len(deleted) != 2 {
			t.Fatalf("expected 2 deleted, got %v", deleted)
		}
	})

	t.Run("invalid key produces error", func(t *testing.T) {
		_, err := s.BatchDelete(ctx, []string{"../escape"})
		mustErr(t, err)
	})
}

// ---------- DeleteByPrefix ----------

func TestDeleteByPrefix(t *testing.T) {
	ctx := context.Background()
	s := newTestStorage(t)

	t.Run("empty prefix errors", func(t *testing.T) {
		err := s.DeleteByPrefix(ctx, "")
		errContains(t, err, "prefix must not be empty")
	})

	t.Run("non-existing prefix returns nil", func(t *testing.T) {
		noErr(t, s.DeleteByPrefix(ctx, "nope/"))
	})

	t.Run("deletes nested tree", func(t *testing.T) {
		files := []string{
			"tree/a.txt",
			"tree/sub/b.txt",
			"tree/sub/deep/c.txt",
		}
		for _, k := range files {
			noErr(t, s.Upload(ctx, k, strings.NewReader("x")))
		}
		// unrelated file must survive
		noErr(t, s.Upload(ctx, "keep/me.txt", strings.NewReader("keep")))

		noErr(t, s.DeleteByPrefix(ctx, "tree/"))

		for _, k := range files {
			p, _ := s.safeFilePath(k)
			if _, err := os.Stat(p); !os.IsNotExist(err) {
				t.Fatalf("file %s should be deleted (err=%v)", k, err)
			}
		}
		exists, err := s.Exists(ctx, "keep/me.txt")
		noErr(t, err)
		assertEq(t, exists, true)
	})
}

// ---------- PrefixIterator ----------

func TestPrefixIterator(t *testing.T) {
	ctx := context.Background()
	s := newTestStorage(t)

	t.Run("non-existing prefix returns nil", func(t *testing.T) {
		noErr(t, s.PrefixIterator(ctx, "nope/", func(ctx context.Context, items ...ObjectItem) error {
			t.Fatal("callback should not be invoked")
			return nil
		}))
	})

	t.Run("prefix points to a single file", func(t *testing.T) {
		noErr(t, s.Upload(ctx, "single.txt", strings.NewReader("only")))
		var seen []ObjectItem
		noErr(t, s.PrefixIterator(ctx, "single.txt", func(ctx context.Context, items ...ObjectItem) error {
			seen = append(seen, items...)
			return nil
		}))
		if len(seen) != 1 || seen[0].Key != "single.txt" {
			t.Fatalf("expected single.txt, got %+v", seen)
		}
	})

	t.Run("prefix points to a dir collects all with slash keys", func(t *testing.T) {
		keys := []string{"pdir/a.txt", "pdir/sub/b.txt", "pdir/sub/deep/c.txt"}
		for _, k := range keys {
			noErr(t, s.Upload(ctx, k, strings.NewReader("x")))
		}
		var seen []string
		noErr(t, s.PrefixIterator(ctx, "pdir/", func(ctx context.Context, items ...ObjectItem) error {
			for _, it := range items {
				seen = append(seen, it.Key)
				if !strings.Contains(it.Key, "/") || strings.Contains(it.Key, "\\") {
					t.Errorf("key %q should be slash-style and not contain backslash", it.Key)
				}
			}
			return nil
		}))
		if len(seen) != len(keys) {
			t.Fatalf("expected %d items, got %d (%v)", len(keys), len(seen), seen)
		}
	})

	t.Run("callback error aborts iteration", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			noErr(t, s.Upload(ctx, "abort/"+string(rune('a'+i))+".txt", strings.NewReader("x")))
		}
		stop := errors.New("stop-now")
		calls := 0
		err := s.PrefixIterator(ctx, "abort/", func(ctx context.Context, items ...ObjectItem) error {
			calls++
			return stop
		})
		if !errors.Is(err, stop) {
			t.Fatalf("expected stop error, got %v", err)
		}
		if calls != 1 {
			t.Fatalf("expected callback to be aborted after first call, got %d calls", calls)
		}
	})
}

// ---------- InitiateMultipartUpload ----------

func TestInitiateMultipartUpload(t *testing.T) {
	ctx := context.Background()
	s := newTestStorage(t)

	t.Run("invalid key", func(t *testing.T) {
		_, err := s.InitiateMultipartUpload(ctx, "../escape")
		mustErr(t, err)
	})

	t.Run("creates session and temp dir", func(t *testing.T) {
		sess, err := s.InitiateMultipartUpload(ctx, "big/file.bin")
		noErr(t, err)
		assertNotEq(t, sess.UploadID, "")
		assertEq(t, sess.Key, "big/file.bin")
		info, err := os.Stat(s.multipartDir(sess.UploadID))
		noErr(t, err)
		if !info.IsDir() {
			t.Fatal("multipart temp dir not created")
		}
	})
}

// ---------- MultipartUpload ----------

func TestMultipartUpload(t *testing.T) {
	ctx := context.Background()
	s := newTestStorage(t)
	sess, err := s.InitiateMultipartUpload(ctx, "mp/file.bin")
	noErr(t, err)

	t.Run("partNumber < 1 rejected", func(t *testing.T) {
		_, err := s.MultipartUpload(ctx, sess, 0, 3, strings.NewReader("abc"))
		errContains(t, err, "partNumber")
	})

	t.Run("etag equals content md5", func(t *testing.T) {
		chunk := []byte("part-1-content")
		resp, err := s.MultipartUpload(ctx, sess, 1, int64(len(chunk)), bytes.NewReader(chunk))
		noErr(t, err)
		assertEq(t, resp.PartNumber, 1)
		assertEq(t, resp.ETag, md5Hex(chunk))
	})
}

// ---------- CompleteMultipartUpload ----------

func TestCompleteMultipartUpload(t *testing.T) {
	ctx := context.Background()
	s := newTestStorage(t)

	t.Run("empty parts errors", func(t *testing.T) {
		sess, err := s.InitiateMultipartUpload(ctx, "cmp/file.bin")
		noErr(t, err)
		err = s.CompleteMultipartUpload(ctx, sess, nil)
		errContains(t, err, "no parts")
	})

	t.Run("merges parts in partNumber order and cleans temp dir", func(t *testing.T) {
		sess, err := s.InitiateMultipartUpload(ctx, "cmp/ordered.bin")
		noErr(t, err)

		p1 := []byte("AAAA")
		p2 := []byte("BBBB")
		// upload out of order: part 2 first, then part 1
		r2, err := s.MultipartUpload(ctx, sess, 2, int64(len(p2)), bytes.NewReader(p2))
		noErr(t, err)
		r1, err := s.MultipartUpload(ctx, sess, 1, int64(len(p1)), bytes.NewReader(p1))
		noErr(t, err)

		parts := []UploadPartResponse{r2, r1} // intentionally reversed
		noErr(t, s.CompleteMultipartUpload(ctx, sess, parts))

		got := readFile(t, filepath.Join(s.rootDir, "cmp", "ordered.bin"))
		bytesEq(t, got, append(append([]byte{}, p1...), p2...))

		if _, err := os.Stat(s.multipartDir(sess.UploadID)); !os.IsNotExist(err) {
			t.Fatalf("temp dir should be removed after complete (err=%v)", err)
		}
	})
}

// ---------- CancelMultipartUpload ----------

func TestCancelMultipartUpload(t *testing.T) {
	ctx := context.Background()
	s := newTestStorage(t)

	sess, err := s.InitiateMultipartUpload(ctx, "can/file.bin")
	noErr(t, err)
	_, err = s.MultipartUpload(ctx, sess, 1, 4, strings.NewReader("part"))
	noErr(t, err)

	mpDir := s.multipartDir(sess.UploadID)
	if _, err := os.Stat(mpDir); err != nil {
		t.Fatalf("temp dir should exist before cancel: %v", err)
	}
	noErr(t, s.CancelMultipartUpload(ctx, sess))
	if _, err := os.Stat(mpDir); !os.IsNotExist(err) {
		t.Fatalf("temp dir should be removed after cancel (err=%v)", err)
	}

	// idempotent: cancelling again returns no error
	noErr(t, s.CancelMultipartUpload(ctx, sess))
}

// ---------- 持久化集成用例（产物保留在 storage/upload 便于人工查看）----------

// TestDiskStorage_PersistToUploadDir 使用 _test 文件同目录下的 upload/ 作为持久根目录，
// 跑完不清理，便于直接进文件夹查看上传/合并产物。每次运行开头会清空该目录以保证可复现。
func TestDiskStorage_PersistToUploadDir(t *testing.T) {
	ctx := context.Background()

	uploadDir := filepath.Join(".", "upload")
	abs, err := filepath.Abs(uploadDir)
	noErr(t, err)

	// 每次运行前清空，保证可复现
	_ = os.RemoveAll(uploadDir)
	noErr(t, os.MkdirAll(uploadDir, 0o755))

	s, err := NewDiskStorage(&LocalDiskStorage{RootDir: uploadDir, BaseUrl: testBaseUrl})
	noErr(t, err)

	t.Run("single upload+download roundtrip", func(t *testing.T) {
		body := []byte("persisted-hello-binary\x00\x01\x02")
		noErr(t, s.Upload(ctx, "docs/readme.txt", bytes.NewReader(body)))
		r, err := s.Download(ctx, "docs/readme.txt")
		noErr(t, err)
		defer r.Close()
		got, err := io.ReadAll(r)
		noErr(t, err)
		bytesEq(t, got, body)
	})

	t.Run("batch upload nested dirs", func(t *testing.T) {
		tasks := []UploadTask{
			{Key: "batch/a.txt", Body: strings.NewReader("AAA")},
			{Key: "batch/sub/b.txt", Body: strings.NewReader("BBB")},
			{Key: "batch/sub/deep/c.txt", Body: strings.NewReader("CCC")},
		}
		noErr(t, s.BatchUpload(ctx, tasks))
	})

	t.Run("multipart full cycle (parts uploaded out of order)", func(t *testing.T) {
		sess, err := s.InitiateMultipartUpload(ctx, "big/blob.bin")
		noErr(t, err)

		p1 := []byte("PART-1-")
		p2 := []byte("PART-2-")
		p3 := []byte("PART-3")

		// 故意乱序：先 part3，再 part1，再 part2
		r3, err := s.MultipartUpload(ctx, sess, 3, int64(len(p3)), bytes.NewReader(p3))
		noErr(t, err)
		assertEq(t, r3.ETag, md5Hex(p3))
		r1, err := s.MultipartUpload(ctx, sess, 1, int64(len(p1)), bytes.NewReader(p1))
		noErr(t, err)
		assertEq(t, r1.ETag, md5Hex(p1))
		r2, err := s.MultipartUpload(ctx, sess, 2, int64(len(p2)), bytes.NewReader(p2))
		noErr(t, err)
		assertEq(t, r2.ETag, md5Hex(p2))

		noErr(t, s.CompleteMultipartUpload(ctx, sess, []UploadPartResponse{r3, r1, r2}))

		merged := append(append(append([]byte{}, p1...), p2...), p3...)
		got := readFile(t, filepath.Join(s.rootDir, filepath.FromSlash("big/blob.bin")))
		bytesEq(t, got, merged)
	})

	t.Run("prefix iterator lists all artifacts", func(t *testing.T) {
		var keys []string
		noErr(t, s.PrefixIterator(ctx, "", func(ctx context.Context, items ...ObjectItem) error {
			for _, it := range items {
				keys = append(keys, it.Key)
			}
			return nil
		}))
		want := []string{
			"docs/readme.txt",
			"batch/a.txt",
			"batch/sub/b.txt",
			"batch/sub/deep/c.txt",
			"big/blob.bin",
		}
		for _, k := range want {
			if !slices.Contains(keys, k) {
				t.Errorf("expected key %q in iterator output, got %v", k, keys)
			}
		}
		if len(keys) != len(want) {
			t.Errorf("expected %d keys, got %d: %v", len(want), len(keys), keys)
		}
	})

	// 跑完保留 upload/ 内容，仅打印路径供人工查看
	t.Logf("persisted artifacts under: %s", abs)
}
