package httpmirror

import (
	"context"
	"io"
	"io/fs"
	"net/url"
	"time"
)

type FS interface {
	List(ctx context.Context, p string, fn func(fs.FileInfo) error) error
	Stat(ctx context.Context, p string) (fs.FileInfo, error)
	Get(ctx context.Context, p string) (f io.ReadCloser, err error)
	Put(ctx context.Context, p string, f io.Reader) (err error)
	Del(ctx context.Context, p string) error

	PresignedGet(ctx context.Context, p string, expires time.Duration) (u *url.URL, err error)
}
