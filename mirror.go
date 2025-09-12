package httpmirror

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/wzshiming/sss"
)

// MirrorHandler mirror handler
type MirrorHandler struct {
	// RemoteCache is the cache of the remote file system
	RemoteCache *sss.SSS
	// LinkExpires is the expires of links
	LinkExpires time.Duration
	// BaseDomain is the domain name suffix
	BaseDomain string
	// Client is used without the connect method
	Client *http.Client
	// ProxyDial specifies the optional proxyDial function for
	// establishing the transport connection.
	ProxyDial func(context.Context, string, string) (net.Conn, error)
	// NotFound Not proxy requests
	NotFound http.Handler
	// Logger error log
	Logger Logger
	// CheckSyncTimeout is the timeout for checking the sync
	CheckSyncTimeout time.Duration
	// HostFromFirstPath is the host from the first path
	HostFromFirstPath bool

	// BlockSuffix is for block some source
	BlockSuffix []string

	mut sync.Map
}

type Logger interface {
	Println(v ...interface{})
}

func (m *MirrorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	path := r.URL.Path
	if len(path) == 0 || strings.HasSuffix(path, "/") {
		m.notFoundResponse(w, r)
		return
	}
	if len(m.BlockSuffix) != 0 {
		for _, suffix := range m.BlockSuffix {
			if strings.HasSuffix(path, suffix) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		}
	}

	host := r.Host
	if m.HostFromFirstPath {
		paths := strings.Split(path[1:], "/")
		host = paths[0]
		path = "/" + strings.Join(paths[1:], "/")
		if path == "/" {
			m.notFoundResponse(w, r)
			return
		}

		r.Host = host
		r.URL.Path = path
	}

	if !strings.Contains(host, ".") || !isValidDomain(host) {
		m.notFoundResponse(w, r)
		return
	}

	if m.BaseDomain != "" {
		if !strings.HasSuffix(host, m.BaseDomain) {
			m.notFoundResponse(w, r)
			return
		}
		host = host[:len(r.Host)-len(m.BaseDomain)]
	}

	r.RequestURI = ""
	r.URL.Host = host
	r.URL.Scheme = "https"
	r.URL.RawQuery = ""
	r.URL.ForceQuery = false

	if m.Logger != nil {
		m.Logger.Println("Request", r.URL)
	}

	if m.RemoteCache == nil {
		m.directResponse(w, r)
		return
	}

	m.cacheResponse(w, r)
	return
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
			m.redirect(w, r, file, cacheInfo)
			doneCache()
			return
		}

		sourceCtx, sourceCancel := context.WithTimeout(ctx, m.CheckSyncTimeout)
		sourceInfo, err := httpHead(sourceCtx, m.client(), r.URL.String())
		if err != nil {
			sourceCancel()
			if m.Logger != nil {
				m.Logger.Println("Source Miss", file, err)
			}
			m.redirect(w, r, file, cacheInfo)
			doneCache()
			return
		}
		sourceCancel()

		sourceSize := sourceInfo.Size()
		cacheSize := cacheInfo.Size()
		if cacheSize != 0 && (sourceSize <= 0 || sourceSize == cacheSize) {
			m.redirect(w, r, file, cacheInfo)
			doneCache()
			return
		}

		if m.Logger != nil {
			m.Logger.Println("Source change", file, sourceSize, cacheSize)
		}
	}

	errCh := make(chan error, 1)

	go func() {
		defer doneCache()
		err = m.cacheFile(context.Background(), file, r.URL.String(), file)
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
		m.redirect(w, r, file, nil)
		return
	}
}

func (m *MirrorHandler) cacheFile(ctx context.Context, key, sourceFile, cacheFile string) error {
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
	fw, err := m.RemoteCache.Writer(ctx, key)
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
			m.errorResponse(w, r, err)
			return
		}
	}
}

func (m *MirrorHandler) errorResponse(w http.ResponseWriter, r *http.Request, err error) {
	e := err.Error()
	if m.Logger != nil {
		m.Logger.Println(e)
	}
	http.Error(w, e, http.StatusInternalServerError)
}

func (m *MirrorHandler) notFoundResponse(w http.ResponseWriter, r *http.Request) {
	if m.NotFound != nil {
		m.NotFound.ServeHTTP(w, r)
	} else {
		http.NotFound(w, r)
	}
}

var ignoreHeader = map[string]struct{}{
	"Connection": {},
	"Server":     {},
}

func (m *MirrorHandler) client() *http.Client {
	if m.Client != nil {
		return m.Client
	}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: m.proxyDial,
		},
	}
}

func (m *MirrorHandler) proxyDial(ctx context.Context, network, address string) (net.Conn, error) {
	proxyDial := m.ProxyDial
	if proxyDial == nil {
		var dialer net.Dialer
		proxyDial = dialer.DialContext
	}
	return proxyDial(ctx, network, address)
}

// isValidDomain validates if input string is a valid domain name.
func isValidDomain(host string) bool {
	// See RFC 1035, RFC 3696.
	host = strings.TrimSpace(host)
	if len(host) == 0 || len(host) > 255 {
		return false
	}
	// host cannot start or end with "-"
	if host[len(host)-1:] == "-" || host[:1] == "-" {
		return false
	}
	// host cannot start or end with "_"
	if host[len(host)-1:] == "_" || host[:1] == "_" {
		return false
	}
	// host cannot start with a "."
	if host[:1] == "." {
		return false
	}
	// All non alphanumeric characters are invalid.
	if strings.ContainsAny(host, "`~!@#$%^&*()+={}[]|\\\"';:><?/") {
		return false
	}
	// No need to regexp match, since the list is non-exhaustive.
	// We let it valid and fail later.
	return true
}
