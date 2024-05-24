package local

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wzshiming/httpmirror"
)

type Local string

// NewLocal returns a new file source
// ./{path}
func NewLocal(root string) (httpmirror.FS, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return Local(root), nil
}

func (l Local) relPath(path string) string {
	return filepath.Join(string(l), filepath.Clean(path))
}

func (l Local) List(ctx context.Context, p string, fn func(fs.FileInfo) error) error {
	return filepath.Walk(l.relPath(p), func(path string, info os.FileInfo, err error) error {
		path, err = filepath.Rel(string(l), path)
		if err != nil {
			return err
		}

		if info == nil {
			return nil
		}

		if info.IsDir() {
			return nil
		}
		path = strings.Replace(path, `\`, `/`, -1)

		return fn(info)
	})
}

func (l Local) Stat(ctx context.Context, p string) (fs.FileInfo, error) {
	return os.Stat(l.relPath(p))
}

func (l Local) Get(ctx context.Context, p string) (io.ReadCloser, error) {
	return os.Open(l.relPath(p))
}

var errUnsupportedPresigned = errors.New("unsupported presigned")

func (l Local) PresignedGet(ctx context.Context, p string, expires time.Duration) (u *url.URL, err error) {
	return nil, errUnsupportedPresigned
}

func (l Local) Put(ctx context.Context, p string, f io.Reader) (err error) {
	rp := l.relPath(p)
	err = os.MkdirAll(filepath.Dir(rp), os.ModePerm)
	if err != nil {
		return err
	}
	w, err := os.OpenFile(rp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.ModePerm)
	if err != nil {
		return err
	}

	_, err = io.Copy(w, f)
	if err != nil {
		return err
	}
	return nil
}

func (l Local) Del(ctx context.Context, p string) error {
	return os.Remove(l.relPath(p))
}
