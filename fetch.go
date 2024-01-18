package httpmirror

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"time"
)

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
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	return &fileInfo{
		name: p,
		resp: resp,
	}, nil
}

func httpGet(ctx context.Context, client *http.Client, p string) (io.ReadCloser, fs.FileInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p, nil)
	if err != nil {
		return nil, nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}

	return resp.Body, &fileInfo{
		name: p,
		resp: resp,
	}, nil
}

var _ fs.FileInfo = (*fileInfo)(nil)

type fileInfo struct {
	name string
	resp *http.Response
}

func (f fileInfo) Name() string {
	return f.name
}

func (f fileInfo) IsDir() bool {
	return false
}

func (f fileInfo) Mode() fs.FileMode {
	return 0
}

func (f fileInfo) Sys() any {
	return f.resp
}

func (f fileInfo) Size() int64 {
	return f.resp.ContentLength
}

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

func (f fileInfo) String() string {
	return fmt.Sprintf("%s %s %d", f.Name(), f.ModTime(), f.Size())
}
