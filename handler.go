package httpmirror

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/OpenCIDN/cidn/pkg/clientset/versioned"
	informers "github.com/OpenCIDN/cidn/pkg/informers/externalversions/task/v1alpha1"
	"github.com/wzshiming/sss"
)

// MirrorHandler is the main HTTP handler that processes requests and manages caching.
//
// It acts as a reverse proxy, optionally caching responses in a remote storage backend.
// When RemoteCache is configured, it caches files and redirects clients to signed URLs.
// When RemoteCache is nil, it proxies requests directly to the source.
//
// The handler supports both simple proxying and advanced features like:
//   - Cloud storage caching via RemoteCache
//   - CIDN-based distributed blob management
//   - Content freshness checking with CheckSyncTimeout
//   - Host extraction from URL path with HostFromFirstPath
//   - File suffix blocking via BlockSuffix
type MirrorHandler struct {
	// RemoteCache is the cache of the remote file system.
	// When set, files are cached in the storage backend and clients
	// are redirected to signed URLs. When nil, requests are proxied directly.
	RemoteCache *sss.SSS

	// LinkExpires is the expiration duration for signed URLs.
	// Only used when RemoteCache is configured.
	// Default should be 24 hours.
	LinkExpires time.Duration

	// BaseDomain is the domain name suffix to filter requests.
	// If set, only requests to hosts ending with this suffix are processed.
	// Leave empty to process all valid domain requests.
	BaseDomain string

	// Client is the HTTP client used for requests to source servers.
	// If nil, a default client with ProxyDial will be created.
	Client *http.Client

	// ProxyDial specifies the optional proxy dial function for
	// establishing transport connections to source servers.
	ProxyDial func(context.Context, string, string) (net.Conn, error)

	// NotFound is the handler for requests that don't match any proxy rules.
	// If nil, http.NotFound is used.
	NotFound http.Handler

	// Logger is used for error and informational logging.
	// If nil, no logging is performed.
	Logger Logger

	// CheckSyncTimeout is the timeout for checking if cached content
	// is synchronized with the source. When > 0, the handler verifies
	// that cached files match the source size before serving.
	// Set to 0 to disable sync checking.
	CheckSyncTimeout time.Duration

	// HostFromFirstPath enables extracting the target host from the first
	// path segment instead of the Host header.
	// When true, URLs like /example.com/path/file become requests to
	// https://example.com/path/file
	HostFromFirstPath bool

	// BlockSuffix is a list of file suffixes to block.
	// Requests for files ending with these suffixes return 403 Forbidden.
	// Example: []string{".exe", ".msi"}
	BlockSuffix []string

	// NoRedirect disables HTTP redirects to signed URLs for cached content.
	// When true, the handler serves cached content directly instead of
	// redirecting clients to signed URLs from RemoteCache.
	// This is useful for clients that don't handle redirects well or when
	// you want the proxy to serve all traffic directly.
	NoRedirect bool

	mut sync.Map

	// CIDNClient is the Kubernetes client for CIDN integration.
	// When set along with RemoteCache, enables distributed blob management.
	CIDNClient versioned.Interface

	// CIDNBlobInformer watches for CIDN Blob resource changes.
	// Used to monitor blob sync status when CIDN is enabled.
	CIDNBlobInformer informers.BlobInformer

	// CIDNDestination is the destination name for CIDN blobs.
	// Typically set to the storage backend scheme (e.g., "s3").
	CIDNDestination string
}

// Logger provides a simple logging interface for the mirror handler.
type Logger interface {
	// Println logs a message with the provided arguments.
	Println(v ...interface{})
}

// ServeHTTP implements the http.Handler interface.
// It processes HTTP GET and HEAD requests, optionally caching responses.
//
// Request processing:
//  1. Validates request method (only GET and HEAD allowed)
//  2. Extracts target host and path
//  3. Applies filters (BlockSuffix, BaseDomain, valid domain check)
//  4. Routes to cacheResponse if RemoteCache is set, otherwise directResponse
//
// Returns:
//   - 405 Method Not Allowed for non-GET/HEAD requests
//   - 403 Forbidden for blocked suffixes
//   - 404 Not Found for invalid paths or domains
//   - 302 Found (redirect) for cached files
//   - 500 Internal Server Error for failures
//   - 200 OK for successful proxied or cached responses
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
