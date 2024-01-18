package httpmirror

import (
	"context"
	"io"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7/pkg/s3utils"
)

// MirrorHandler mirror handler
type MirrorHandler struct {
	// RemoteCache is the cache of the remote file system
	RemoteCache FS
	// RedirectLinks is the redirect link
	RedirectLinks func(p string) (string, bool)
	// BaseDomain is the domain name suffix
	BaseDomain string
	// Client  is used without the connect method
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

	host := r.Host
	if !s3utils.IsValidDomain(host) {
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

	if m.RemoteCache == nil || m.RedirectLinks == nil {
		m.directResponse(w, r)
		return
	}

	m.cacheResponse(w, r)
	return
}

func (m *MirrorHandler) cacheResponse(w http.ResponseWriter, r *http.Request) {
	file := path.Join(r.Host, r.URL.Path)
	u, ok := m.RedirectLinks(file)
	if !ok {
		m.notFoundResponse(w, r)
		return
	}

	mut, loaded := m.mut.LoadOrStore(u, &sync.Mutex{})
	if loaded {
		mut := mut.(*sync.Mutex)
		mut.Lock()
		defer mut.Unlock()
	} else {
		mut := mut.(*sync.Mutex)
		mut.Lock()
		defer func() {
			m.mut.Delete(u)
			mut.Unlock()
		}()
	}

	cacheInfo, err := m.RemoteCache.Stat(r.Context(), file)
	if err != nil {
		if m.Logger != nil {
			m.Logger.Println("Cache Miss", u, err)
		}
	} else {
		if m.Logger != nil {
			m.Logger.Println("Cache Hit", u)
		}

		if m.CheckSyncTimeout == 0 {
			http.Redirect(w, r, u, http.StatusFound)
			return
		}

		sourceCtx, sourceCancel := context.WithTimeout(r.Context(), m.CheckSyncTimeout)
		defer sourceCancel()
		sourceInfo, err := httpHead(sourceCtx, m.client(), r.URL.String())
		if err != nil {
			if m.Logger != nil {
				m.Logger.Println("Source Miss", u, err)
			}
			http.Redirect(w, r, u, http.StatusFound)
			return
		}

		sourceSize := sourceInfo.Size()
		cacheSize := cacheInfo.Size()
		if cacheSize != 0 && (sourceSize == 0 || sourceSize == cacheSize) {
			http.Redirect(w, r, u, http.StatusFound)
			return

		}

		if m.Logger != nil {
			m.Logger.Println("Source change", u, sourceSize, cacheSize)
		}
	}

	resp, info, err := httpGet(r.Context(), m.client(), r.URL.String())
	if err != nil {
		m.errorResponse(w, r, err)
		return
	}
	defer resp.Close()

	var body io.Reader = resp

	contentLength := info.Size()
	if contentLength > 0 {
		body = io.LimitReader(body, contentLength)
	}

	if m.Logger != nil {
		m.Logger.Println("Cache", u)
	}
	err = m.RemoteCache.Put(r.Context(), file, body)
	if err != nil {
		m.errorResponse(w, r, err)
		return
	}

	http.Redirect(w, r, u, http.StatusFound)
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
			m.Logger.Println("Response", r.URL)
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
