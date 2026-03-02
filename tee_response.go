package httpmirror

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"

	"github.com/wzshiming/ioswmr"
)

type teeResponse struct {
	fileInfo fs.FileInfo
	swmr     ioswmr.SWMR
	etag     string
}

func (t *teeResponse) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	size := t.fileInfo.Size()

	if size > 0 {
		rs := t.swmr.NewReadSeeker(0, int(size))
		defer rs.Close()
		name := path.Base(r.URL.Path)
		if t.etag != "" {
			w.Header().Set("ETag", t.etag)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, name, t.fileInfo.ModTime(), rs)
	} else {
		rs := t.swmr.NewReader(0)
		defer rs.Close()
		if t.etag != "" {
			w.Header().Set("ETag", t.etag)
		}
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = io.Copy(w, rs)
		}
	}
}

func (m *MirrorHandler) cacheFileTee(ctx context.Context, sourceFile, cacheFile string) (*teeResponse, error) {
	body, info, err := httpGet(ctx, m.client(), sourceFile, true)
	if err != nil {
		return nil, err
	}

	contentLength := info.Size()
	if contentLength == 0 {
		_ = body.Close()
		return nil, ErrNotOK
	}

	if m.Logger != nil {
		m.Logger.Println("Tee Cache", cacheFile, contentLength)
	}

	fw, err := m.RemoteCache.Writer(ctx, cacheFile)
	if err != nil {
		if m.Logger != nil {
			m.Logger.Println("Cache writer error", cacheFile, contentLength, err)
		}
		_ = body.Close()
		return nil, err
	}

	swmr := ioswmr.NewSWMR(
		ioswmr.NewMemoryOrTemporaryFileBuffer(nil, nil),
		ioswmr.WithAutoClose(),
		ioswmr.WithBeforeCloseFunc(func() {
			m.teeCache.Delete(cacheFile)
			if m.Logger != nil {
				m.Logger.Println("Tee Cache closed", cacheFile, err)
			}
		}),
	)

	tee := &teeResponse{
		fileInfo: info,
		swmr:     swmr,
		etag:     info.ETag(),
	}
	sw := swmr.Writer()

	go func() {
		defer body.Close()
		_, err := io.Copy(sw, body)
		_ = sw.CloseWithError(err)
	}()

	go func() {
		r := swmr.NewReader(0)
		defer r.Close()

		defer fw.Close()
		n, err := io.Copy(fw, r)
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
