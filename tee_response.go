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
	"sync"

	"github.com/wzshiming/ioswmr"
)

type teeResponse struct {
	fileInfo  fs.FileInfo
	swmr      ioswmr.SWMR
	tmp       *os.File
	teeCache  *sync.Map
	etag      string
	cacheFile string
}

func (t *teeResponse) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	size := t.fileInfo.Size()

	if size > 0 {
		rs := t.swmr.NewReadSeeker(0, int(size))
		defer rs.Close()
		name := path.Base(r.URL.Path)
		w.Header().Set("ETag", t.etag)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprint(size))
		http.ServeContent(w, r, name, t.fileInfo.ModTime(), rs)
	} else {
		rs := t.swmr.NewReader(0)
		defer rs.Close()
		w.Header().Set("ETag", t.etag)
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
	_ = os.Remove(t.tmp.Name())
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

	tmp, err := os.CreateTemp("", "mirror-tee-*")
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
		fileInfo:  info,
		swmr:      swmr,
		tmp:       tmp,
		etag:      info.ETag(),
		teeCache:  &m.teeCache,
		cacheFile: cacheFile,
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
			return
		}

		if contentLength > 0 && n != contentLength {
			err = fmt.Errorf("copied %d bytes, expected %d", n, contentLength)
			if m.Logger != nil {
				m.Logger.Println("Cache copy error", cacheFile, err)
			}
			_ = fw.Cancel(context.Background())
			return
		}

		err = fw.Commit(context.Background())
		if err != nil {
			if m.Logger != nil {
				m.Logger.Println("Cache Commit error", cacheFile, err)
			}
			return
		}
		if m.Logger != nil {
			m.Logger.Println("Tee Cached", cacheFile, contentLength, n)
		}
	}()

	return tee, nil
}
