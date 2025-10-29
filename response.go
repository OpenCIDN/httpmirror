package httpmirror

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/wzshiming/sss"
)

var ignoreHeader = map[string]struct{}{
	"Connection": {},
	"Server":     {},
}

// directResponse handles requests without caching by proxying directly to the source.
func (m *MirrorHandler) directResponse(w http.ResponseWriter, r *http.Request) {
	resp, err := m.client().Do(r)
	if err != nil {
		m.errorResponse(w, r, err)
		return
	}
	defer resp.Body.Close()

	header := w.Header()
	for k, v := range resp.Header {
		if _, ok := ignoreHeader[k]; ok {
			continue
		}
		header[k] = v
	}

	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(resp.StatusCode)
	}

	if r.Method == http.MethodGet {
		var body io.Reader = resp.Body

		contentLength := resp.ContentLength
		if contentLength > 0 {
			body = io.LimitReader(body, contentLength)
		}

		if m.Logger != nil {
			m.Logger.Println("Response", r.URL, contentLength)
		}
		_, err = io.Copy(w, body)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				if m.Logger != nil {
					m.Logger.Println("Copy error", r.URL, err)
				}
			}
			return
		}
	}
}

// redirect redirects the client to a signed URL for cached content.
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

// errorResponse sends an HTTP 500 error response with the error message.
func (m *MirrorHandler) errorResponse(w http.ResponseWriter, r *http.Request, err error) {
	e := err.Error()
	if m.Logger != nil {
		m.Logger.Println(e)
	}
	http.Error(w, e, http.StatusInternalServerError)
}

// notFoundResponse sends an HTTP 404 error response.
func (m *MirrorHandler) notFoundResponse(w http.ResponseWriter, r *http.Request) {
	if m.NotFound != nil {
		m.NotFound.ServeHTTP(w, r)
	} else {
		http.NotFound(w, r)
	}
}
