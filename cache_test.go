package httpmirror

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wzshiming/ioswmr"
)

func Test_tryServeFromLocalCache(t *testing.T) {
	t.Run("file not found", func(t *testing.T) {
		m := &MirrorHandler{}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "https://example.com/test.txt", nil)

		served := m.tryServeFromLocalCache(w, r, "/nonexistent/path/file.txt", "example.com/test.txt")
		if served {
			t.Error("expected false for nonexistent file")
		}
	})

	t.Run("empty file", func(t *testing.T) {
		dir := t.TempDir()
		localPath := filepath.Join(dir, "empty.txt")
		if err := os.WriteFile(localPath, []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}

		m := &MirrorHandler{}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "https://example.com/empty.txt", nil)

		served := m.tryServeFromLocalCache(w, r, localPath, "example.com/empty.txt")
		if served {
			t.Error("expected false for empty file")
		}
	})

	t.Run("directory path", func(t *testing.T) {
		dir := t.TempDir()

		m := &MirrorHandler{}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "https://example.com/dir", nil)

		served := m.tryServeFromLocalCache(w, r, dir, "example.com/dir")
		if served {
			t.Error("expected false for directory")
		}
	})

	t.Run("serves valid file", func(t *testing.T) {
		dir := t.TempDir()
		localPath := filepath.Join(dir, "test.txt")
		content := []byte("hello world")
		if err := os.WriteFile(localPath, content, 0o644); err != nil {
			t.Fatal(err)
		}

		m := &MirrorHandler{}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "https://example.com/test.txt", nil)

		served := m.tryServeFromLocalCache(w, r, localPath, "example.com/test.txt")
		if !served {
			t.Fatal("expected true for valid file")
		}
		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
		if w.Body.String() != string(content) {
			t.Errorf("expected body %q, got %q", string(content), w.Body.String())
		}
	})

	t.Run("serves HEAD request", func(t *testing.T) {
		dir := t.TempDir()
		localPath := filepath.Join(dir, "test.txt")
		content := []byte("hello world")
		if err := os.WriteFile(localPath, content, 0o644); err != nil {
			t.Fatal(err)
		}

		m := &MirrorHandler{}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodHead, "https://example.com/test.txt", nil)

		served := m.tryServeFromLocalCache(w, r, localPath, "example.com/test.txt")
		if !served {
			t.Fatal("expected true for valid file")
		}
		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d", w.Code)
		}
		if w.Body.Len() != 0 {
			t.Errorf("expected empty body for HEAD, got %d bytes", w.Body.Len())
		}
	})
}

func Test_teeResponse_Close_localCache(t *testing.T) {
	t.Run("without local cache deletes tmp file", func(t *testing.T) {
		dir := t.TempDir()
		tmpFile, err := os.CreateTemp(dir, "test-tee-*")
		if err != nil {
			t.Fatal(err)
		}
		tmpPath := tmpFile.Name()

		var teeCache sync.Map
		swmr := ioswmr.NewSWMR(tmpFile)
		swmr.Close()

		tee := &teeResponse{
			tmp:            tmpFile,
			swmr:           swmr,
			teeCache:       &teeCache,
			cacheFile:      "test/file",
			localCachePath: "",
		}

		err = tee.Close()
		if err != nil {
			t.Fatal(err)
		}

		if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
			t.Error("expected tmp file to be removed when localCachePath is empty")
		}
	})

	t.Run("with local cache keeps file", func(t *testing.T) {
		dir := t.TempDir()
		localPath := filepath.Join(dir, "cached.txt")
		content := []byte("cached content")
		if err := os.WriteFile(localPath, content, 0o644); err != nil {
			t.Fatal(err)
		}

		tmpFile, err := os.Open(localPath)
		if err != nil {
			t.Fatal(err)
		}

		var teeCache sync.Map
		swmr := ioswmr.NewSWMR(tmpFile)
		swmr.Close()

		tee := &teeResponse{
			tmp:            tmpFile,
			swmr:           swmr,
			teeCache:       &teeCache,
			cacheFile:      "test/file",
			localCachePath: localPath,
		}

		err = tee.Close()
		if err != nil {
			t.Fatal(err)
		}

		// File should still exist when localCachePath is set
		if _, err := os.Stat(localPath); err != nil {
			t.Errorf("expected local cache file to still exist: %v", err)
		}
	})
}
