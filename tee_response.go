package httpmirror

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sync"

	"github.com/wzshiming/ioswmr"
)

type teeResponse struct {
	fileInfo       fs.FileInfo
	swmr           ioswmr.SWMR
	tmp            *os.File
	teeCache       *sync.Map
	cacheFile      string
	localCachePath string // when set, rename tmp to this path on completion and keep the file
}

func (t *teeResponse) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	size := t.fileInfo.Size()

	if size > 0 {
		rs := t.swmr.NewReadSeeker(0, int(size))
		defer rs.Close()
		name := path.Base(r.URL.Path)
		http.ServeContent(w, r, name, t.fileInfo.ModTime(), rs)
	} else {
		rs := t.swmr.NewReader(0)
		defer rs.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = io.Copy(w, rs)
		}
	}
}

func (t *teeResponse) Close() error {
	if !t.swmr.IsClosed() {
		return nil
	}
	if t.swmr.Using() != 0 {
		return nil
	}
	t.teeCache.Delete(t.cacheFile)
	err := t.tmp.Close()
	if err != nil {
		return err
	}
	if t.localCachePath == "" {
		_ = os.Remove(t.tmp.Name())
	}
	return nil
}

func (m *MirrorHandler) cacheFileTee(ctx context.Context, sourceFile, cacheFile string) (*teeResponse, error) {
	resp, info, err := httpGet(ctx, m.client(), sourceFile)
	if err != nil {
		return nil, err
	}

	var body io.Reader = resp

	contentLength := info.Size()
	if contentLength == 0 {
		_ = resp.Close()
		return nil, ErrNotOK
	}

	if m.Logger != nil {
		m.Logger.Println("Tee Cache", cacheFile, contentLength)
	}

	var tmp *os.File
	var localCachePath string

	if m.LocalCacheDir != "" {
		localCachePath = filepath.Join(m.LocalCacheDir, cacheFile)
		if err := os.MkdirAll(filepath.Dir(localCachePath), 0o750); err != nil {
			_ = resp.Close()
			return nil, err
		}
		tmp, err = os.Create(localCachePath + ".tmp")
	} else {
		tmp, err = os.CreateTemp("", "mirror-tee-*")
	}
	if err != nil {
		_ = resp.Close()
		return nil, err
	}

	fw, err := m.RemoteCache.Writer(ctx, cacheFile)
	if err != nil {
		if m.Logger != nil {
			m.Logger.Println("Cache writer error", cacheFile, contentLength, err)
		}
		_ = resp.Close()
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, err
	}

	swmr := ioswmr.NewSWMR(tmp)

	tee := &teeResponse{
		fileInfo:       info,
		swmr:           swmr,
		tmp:            tmp,
		teeCache:       &m.teeCache,
		cacheFile:      cacheFile,
		localCachePath: localCachePath,
	}

	go func() {
		defer tee.Close()
		defer resp.Close()
		defer fw.Close()
		defer swmr.Close()

		w := io.MultiWriter(swmr, fw)
		n, err := io.Copy(w, body)
		if err != nil && !errors.Is(err, io.EOF) {
			if m.Logger != nil {
				m.Logger.Println("SWMR copy error", cacheFile, contentLength, n, err)
			}
			_ = fw.Cancel(context.Background())
			if localCachePath != "" {
				_ = os.Remove(tmp.Name())
			}
			return
		}

		if contentLength > 0 && n != contentLength {
			err = fmt.Errorf("copied %d bytes, expected %d", n, contentLength)
			if m.Logger != nil {
				m.Logger.Println("Cache copy error", cacheFile, err)
			}
			_ = fw.Cancel(context.Background())
			if localCachePath != "" {
				_ = os.Remove(tmp.Name())
			}
			return
		}

		err = fw.Commit(context.Background())
		if err != nil {
			if m.Logger != nil {
				m.Logger.Println("Cache Commit error", cacheFile, err)
			}
			if localCachePath != "" {
				_ = os.Remove(tmp.Name())
			}
			return
		}

		if localCachePath != "" {
			if err := os.Rename(tmp.Name(), localCachePath); err != nil {
				if m.Logger != nil {
					m.Logger.Println("Local cache rename error", cacheFile, err)
				}
			}
		}

		if m.Logger != nil {
			m.Logger.Println("Tee Cached", cacheFile, contentLength, n)
		}
	}()

	return tee, nil
}
