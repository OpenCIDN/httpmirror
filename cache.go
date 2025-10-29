package httpmirror

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"

	"github.com/wzshiming/sss"
)

// cacheResponse handles requests with caching enabled.
// It checks the cache, fetches from source if needed, and manages concurrent requests.
func (m *MirrorHandler) cacheResponse(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	file := path.Join(r.Host, r.URL.EscapedPath())

	closeValue, loaded := m.mut.LoadOrStore(file, make(chan struct{}))
	closeCh := closeValue.(chan struct{})
	for loaded {
		select {
		case <-ctx.Done():
			m.errorResponse(w, r, ctx.Err())
			return
		case <-closeCh:
		}
		closeValue, loaded = m.mut.LoadOrStore(file, make(chan struct{}))
		closeCh = closeValue.(chan struct{})
	}

	doneCache := func() {
		m.mut.Delete(file)
		close(closeCh)
	}

	cacheInfo, err := m.RemoteCache.Stat(ctx, file)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			m.errorResponse(w, r, ctx.Err())
			doneCache()
			return
		}
		if m.Logger != nil {
			m.Logger.Println("Cache Miss", file, err)
		}
	} else {
		if m.Logger != nil {
			m.Logger.Println("Cache Hit", file)
		}

		if m.CheckSyncTimeout == 0 {
			m.responseCache(w, r, file, cacheInfo)
			doneCache()
			return
		}

		if m.CIDNClient == nil {
			sourceCtx, sourceCancel := context.WithTimeout(ctx, m.CheckSyncTimeout)
			sourceInfo, err := httpHead(sourceCtx, m.client(), r.URL.String())
			if err != nil {
				sourceCancel()
				if m.Logger != nil {
					m.Logger.Println("Source Miss", file, err)
				}
				m.responseCache(w, r, file, cacheInfo)
				doneCache()
				return
			}
			sourceCancel()

			sourceSize := sourceInfo.Size()
			cacheSize := cacheInfo.Size()
			if cacheSize != 0 && (sourceSize <= 0 || sourceSize == cacheSize) {
				m.responseCache(w, r, file, cacheInfo)
				doneCache()
				return
			}

			if m.Logger != nil {
				m.Logger.Println("Source change", file, sourceSize, cacheSize)
			}
		}
	}

	errCh := make(chan error, 1)

	go func() {
		defer doneCache()
		var err error
		if m.CIDNClient != nil {
			err = m.cacheFileWithCIDN(context.Background(), r.URL.String(), file)
		} else {
			err = m.cacheFile(context.Background(), r.URL.String(), file)
		}
		errCh <- err
	}()

	select {
	case <-ctx.Done():
		m.errorResponse(w, r, ctx.Err())
		return
	case err := <-errCh:
		if err != nil {
			if errors.Is(err, ErrNotOK) {
				m.notFoundResponse(w, r)
				return
			}
			m.errorResponse(w, r, err)
			return
		}
		m.responseCache(w, r, file, nil)
		return
	}
}

// cacheFile downloads and caches a file from the source.
func (m *MirrorHandler) cacheFile(ctx context.Context, sourceFile, cacheFile string) error {
	resp, info, err := httpGet(ctx, m.client(), sourceFile)
	if err != nil {
		return err
	}
	defer resp.Close()

	var body io.Reader = resp

	contentLength := info.Size()
	if contentLength == 0 {
		return ErrNotOK
	}

	if m.Logger != nil {
		m.Logger.Println("Cache", cacheFile, contentLength)
	}
	fw, err := m.RemoteCache.Writer(ctx, cacheFile)
	if err != nil {
		if m.Logger != nil {
			m.Logger.Println("Cache writer error", cacheFile, contentLength, err)
		}
		return err
	}
	defer fw.Close()

	n, err := io.Copy(fw, body)
	if err != nil {
		if m.Logger != nil {
			m.Logger.Println("Cache copy error", cacheFile, contentLength, err)
		}
		_ = fw.Cancel(context.Background())
		return err
	}

	if contentLength > 0 && n != contentLength {
		err = fmt.Errorf("copied %d bytes, expected %d", n, contentLength)
		if m.Logger != nil {
			m.Logger.Println("Cache copy error", cacheFile, err)
		}
		_ = fw.Cancel(context.Background())
		return err
	}

	err = fw.Commit(ctx)
	if err != nil {
		if m.Logger != nil {
			m.Logger.Println("Cache Commit error", cacheFile, err)
		}
		return err
	}
	if m.Logger != nil {
		m.Logger.Println("Cached", cacheFile, contentLength)
	}

	return nil
}

// responseCache serves a cached file to the client.
func (m *MirrorHandler) responseCache(rw http.ResponseWriter, r *http.Request, file string, info sss.FileInfo) {
	if m.NoRedirect {
		m.serveFromCache(rw, r, file, info)
	} else {
		m.redirect(rw, r, file, info)
	}
}
