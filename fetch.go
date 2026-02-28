package httpmirror

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"time"
)

// httpHead performs an HTTP HEAD request to retrieve file metadata without downloading the content.
// It returns file information as an fs.FileInfo interface.
//
// Returns ErrNotOK if the response status is not 200 OK.
func httpHead(ctx context.Context, client *http.Client, p string) (fs.FileInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, p, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: http status %d", ErrNotOK, resp.StatusCode)
	}

	return &fileInfo{
		name: p,
		resp: resp,
	}, nil
}

// httpGet performs an HTTP GET request to download file content.
// It returns the response body reader and file information.
//
// The caller is responsible for closing the returned io.ReadCloser.
// Returns ErrNotOK if the response status is not 200 OK.
func httpGet(ctx context.Context, client *http.Client, p string) (io.ReadCloser, *fileInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p, nil)
	if err != nil {
		return nil, nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, nil, fmt.Errorf("%w: http status %d", ErrNotOK, resp.StatusCode)
	}

	return resp.Body, &fileInfo{
		name: p,
		resp: resp,
	}, nil
}

// ErrNotOK is returned when an HTTP response status is not 200 OK.
var ErrNotOK = fmt.Errorf("http status not ok")

var _ fs.FileInfo = (*fileInfo)(nil)

// fileInfo implements fs.FileInfo interface for HTTP responses.
// It extracts file metadata from HTTP response headers.
type fileInfo struct {
	name string
	resp *http.Response
}

// Name returns the name of the file (the URL in this case).
func (f fileInfo) Name() string {
	return f.name
}

// IsDir always returns false as HTTP responses represent files, not directories.
func (f fileInfo) IsDir() bool {
	return false
}

// Mode returns the file mode (always 0 for HTTP responses).
func (f fileInfo) Mode() fs.FileMode {
	return 0
}

// Sys returns the underlying *http.Response object.
func (f fileInfo) Sys() any {
	return f.resp
}

// Size returns the content length from the HTTP response.
func (f fileInfo) Size() int64 {
	return f.resp.ContentLength
}

// ETag returns the ETag header from the HTTP response, which can be used for caching and validation.
func (f fileInfo) ETag() string {
	return f.resp.Header.Get("ETag")
}

// ModTime returns the modification time from the Last-Modified header.
// Returns zero time if the header is missing or cannot be parsed.
func (f fileInfo) ModTime() time.Time {
	lastModified := f.resp.Header.Get("Last-Modified")
	if lastModified == "" {
		return time.Time{}
	}
	t, err := time.Parse(http.TimeFormat, lastModified)
	if err != nil {
		return time.Time{}
	}

	return t
}

// String returns a string representation of the file info.
func (f fileInfo) String() string {
	return fmt.Sprintf("%s %s %d", f.Name(), f.ModTime(), f.Size())
}
