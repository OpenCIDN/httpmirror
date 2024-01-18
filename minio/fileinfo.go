package minio

import (
	"fmt"
	"io/fs"
	"path"
	"time"

	"github.com/minio/minio-go/v7"
)

var _ fs.FileInfo = (*fileInfo)(nil)

type fileInfo struct {
	obj minio.ObjectInfo
}

func (f fileInfo) Name() string {
	return path.Base(f.obj.Key)
}

func (f fileInfo) IsDir() bool {
	return false
}

func (f fileInfo) Mode() fs.FileMode {
	return 0
}

func (f fileInfo) Sys() any {
	return f.obj
}

func (f fileInfo) Size() int64 {
	return f.obj.Size
}

func (f fileInfo) ModTime() time.Time {
	return f.obj.LastModified
}

func (f fileInfo) String() string {
	return fmt.Sprintf("%s %s %d", f.Name(), f.ModTime(), f.Size())
}
