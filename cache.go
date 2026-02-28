package httpmirror

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"

	"github.com/wzshiming/sss"
)

func (m *MirrorHandler) responseCache(rw http.ResponseWriter, r *http.Request, file string, info sss.FileInfo) {
	if m.NoRedirect {
		m.serveFromCache(rw, r, file, info)
	} else {
		m.redirect(rw, r, file, info)
	}
}

func (m *MirrorHandler) setHeaders(rw http.ResponseWriter, info sss.FileInfo) {
	if sys := info.Sys(); sys != nil {
		if resp, ok := sys.(sss.FileInfoExpansion); ok {
			if etag := resp.ETag; etag != nil && *etag != "" {
				rw.Header().Set("Etag", *etag)
			}
		}
	}
}

func (m *MirrorHandler) redirect(rw http.ResponseWriter, r *http.Request, file string, info sss.FileInfo) {
	expires := m.LinkExpires
	var url string
	var err error

	if r.Method == http.MethodHead {
		if info == nil {
			info, err = m.RemoteCache.Stat(r.Context(), file)
			if err != nil {
				if m.Logger != nil {
					m.Logger.Println("Stat", file, err)
				}
			}
		}
		if info != nil {
			m.setHeaders(rw, info)
			rw.Header().Set("Content-Type", "application/octet-stream")
			rw.Header().Set("Content-Length", fmt.Sprint(info.Size()))
			rw.Header().Set("Last-Modified", info.ModTime().Format(http.TimeFormat))
			rw.WriteHeader(http.StatusOK)
			return
		} else {
			url, err = m.RemoteCache.SignHead(file, expires)
			if err != nil {
				if m.Logger != nil {
					m.Logger.Println("Sign Head", file, err)
				}
				return
			}
		}
	} else {
		url, err = m.RemoteCache.SignGet(file, expires)
		if err != nil {
			if m.Logger != nil {
				m.Logger.Println("Sign Get", file, err)
			}
			return
		}
	}

	http.Redirect(rw, r, url, http.StatusFound)
	return
}

// serveFromCache serves content directly from the remote cache without redirecting.
// It reads the file from RemoteCache and streams it to the client.
func (m *MirrorHandler) serveFromCache(rw http.ResponseWriter, r *http.Request, file string, info sss.FileInfo) {
	ctx := r.Context()
	m.setHeaders(rw, info)

	if r.Method == http.MethodHead {
		// Get file info if not already provided
		if info == nil {
			var err error
			info, err = m.RemoteCache.Stat(ctx, file)
			if err != nil {
				if m.Logger != nil {
					m.Logger.Println("Stat error for direct serve", file, err)
				}
				m.errorResponse(rw, r, err)
				return
			}
		}

		rw.WriteHeader(http.StatusOK)
		rw.Header().Set("Content-Type", "application/octet-stream")
		rw.Header().Set("Content-Length", fmt.Sprint(info.Size()))
		rw.Header().Set("Last-Modified", info.ModTime().Format(http.TimeFormat))

		return
	}

	// For GET requests, read and stream the content
	reader, info, err := m.RemoteCache.ReaderAndInfo(ctx, file)
	if err != nil {
		if m.Logger != nil {
			m.Logger.Println("Reader error for direct serve", file, err)
		}
		m.errorResponse(rw, r, err)
		return
	}
	defer reader.Close()

	rw.WriteHeader(http.StatusOK)
	rw.Header().Set("Content-Type", "application/octet-stream")
	rw.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	rw.Header().Set("Last-Modified", info.ModTime().Format(http.TimeFormat))

	_, err = io.Copy(rw, reader)
	if err != nil {
		if m.Logger != nil {
			m.Logger.Println("Copy error for direct serve", file, err)
		}
	}
}

func (m *MirrorHandler) cacheResponse(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	file := path.Join(r.Host, r.URL.EscapedPath())

	if err := m.setHuggingFaceHeaders(w, r); err != nil {
		m.errorResponse(w, r, err)
		return
	}

	// Check local cache first (fast path for tee mode with local caching)
	if m.TeeResponse && m.LocalCacheDir != "" {
		localPath := filepath.Join(m.LocalCacheDir, file)
		if m.tryServeFromLocalCache(w, r, localPath, file) {
			return
		}
	}

	cacheInfo, err := m.RemoteCache.Stat(ctx, file)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			m.errorResponse(w, r, ctx.Err())
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
				return
			}
			sourceCancel()

			sourceSize := sourceInfo.Size()
			cacheSize := cacheInfo.Size()
			if cacheSize != 0 && (sourceSize <= 0 || sourceSize == cacheSize) {
				m.responseCache(w, r, file, cacheInfo)
				return
			}

			if m.Logger != nil {
				m.Logger.Println("Source change", file, sourceSize, cacheSize)
			}
		}
	}

	if m.TeeResponse {
		var tee *teeResponse
		val, ok := m.teeCache.Load(file)
		if !ok {
			ch := m.group.DoChan(file, func() (any, error) {
				return m.cacheFileTee(context.Background(), r.URL.String(), file)
			})
			select {
			case <-ctx.Done():
				m.errorResponse(w, r, ctx.Err())
				return
			case result := <-ch:
				if result.Err != nil {
					if m.Logger != nil {
						m.Logger.Println("Tee Cache error", file, result.Err)
					}
					return
				}
				tee, ok = result.Val.(*teeResponse)
				if !ok {
					if m.Logger != nil {
						m.Logger.Println("Tee Cache type assertion error", file)
					}
					return
				}
				if !result.Shared {
					m.teeCache.Store(file, tee)
				}
			}
		} else {
			tee, ok = val.(*teeResponse)
			if !ok {
				if m.Logger != nil {
					m.Logger.Println("Tee Cache type assertion error", file)
				}
				return
			}
		}

		defer tee.Close()
		tee.ServeHTTP(w, r)
		return
	}

	ch := m.group.DoChan(file, func() (any, error) {
		return nil, m.cacheFile(context.Background(), r.URL.String(), file)
	})

	select {
	case <-ctx.Done():
		m.errorResponse(w, r, ctx.Err())
		return
	case result := <-ch:
		if result.Err != nil {
			if cacheInfo != nil {
				if m.Logger != nil {
					m.Logger.Println("Recache error", file, result.Err)
				}
				m.responseCache(w, r, file, cacheInfo)
				return
			}

			if errors.Is(result.Err, ErrNotOK) {
				m.notFoundResponse(w, r)
				return
			}
			m.errorResponse(w, r, result.Err)
			return
		}
		m.responseCache(w, r, file, cacheInfo)
		return
	}
}

func (m *MirrorHandler) cacheFile(ctx context.Context, sourceFile, cacheFile string) error {
	if m.CIDNClient != nil {
		return m.cacheFileWithCIDN(context.Background(), sourceFile, cacheFile)
	}
	return m.cacheFileDirect(context.Background(), sourceFile, cacheFile)
}

func (m *MirrorHandler) cacheFileDirect(ctx context.Context, sourceFile, cacheFile string) error {
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

// tryServeFromLocalCache attempts to serve a file from the local cache directory.
// It checks if the file exists, optionally validates freshness against the source,
// and serves the file using http.ServeContent.
// Returns true if the file was served from local cache, false otherwise.
func (m *MirrorHandler) tryServeFromLocalCache(w http.ResponseWriter, r *http.Request, localPath, file string) bool {
	fi, err := os.Stat(localPath)
	if err != nil || fi.IsDir() || fi.Size() == 0 {
		return false
	}

	if m.CheckSyncTimeout > 0 && m.CIDNClient == nil {
		ctx := r.Context()
		sourceCtx, sourceCancel := context.WithTimeout(ctx, m.CheckSyncTimeout)
		sourceInfo, err := httpHead(sourceCtx, m.client(), r.URL.String())
		sourceCancel()
		if err == nil {
			sourceSize := sourceInfo.Size()
			localSize := fi.Size()
			if sourceSize > 0 && sourceSize != localSize {
				if m.Logger != nil {
					m.Logger.Println("Source change (local cache)", file, sourceSize, localSize)
				}
				return false
			}
		} else {
			if m.Logger != nil {
				m.Logger.Println("Source check failed (local cache), serving stale", file, err)
			}
		}
	}

	f, err := os.Open(localPath)
	if err != nil {
		if m.Logger != nil {
			m.Logger.Println("Local cache open error", file, err)
		}
		return false
	}
	defer f.Close()

	if m.Logger != nil {
		m.Logger.Println("Local Cache Hit", file)
	}

	name := path.Base(r.URL.Path)
	http.ServeContent(w, r, name, fi.ModTime(), f)
	return true
}
