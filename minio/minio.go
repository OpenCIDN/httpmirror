package minio

import (
	"context"
	"io"
	"io/fs"
	"path"
	"path/filepath"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Minio struct {
	client   *minio.Client
	prefix   string
	bucket   string
	endpoint string
}

type Config struct {
	Endpoint string
	Region   string
	Bucket   string
	Prefix   string
	Secure   bool

	AccessKeyID     string
	AccessKeySecret string
}

// NewMinio create a new minio client
func NewMinio(conf Config) (*Minio, error) {
	opt := &minio.Options{
		Secure: conf.Secure,
		Region: conf.Region,
	}
	if conf.AccessKeyID != "" && conf.AccessKeySecret != "" {
		opt.Creds = credentials.NewStaticV4(conf.AccessKeyID, conf.AccessKeySecret, "")
	}
	client, err := minio.New(conf.Endpoint, opt)
	if err != nil {
		return nil, err
	}

	return &Minio{
		client:   client,
		prefix:   conf.Prefix,
		bucket:   conf.Bucket,
		endpoint: conf.Endpoint,
	}, nil

}

func (m *Minio) relPath(p string) string {
	return path.Join(m.prefix, filepath.Clean(p))
}

func (m *Minio) List(ctx context.Context, p string, fn func(fs.FileInfo) error) error {
	objectCh := m.client.ListObjects(ctx, m.bucket, minio.ListObjectsOptions{
		Prefix: m.relPath(p),
	})

	for object := range objectCh {
		err := fn(fileInfo{object})
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Minio) Stat(ctx context.Context, p string) (fs.FileInfo, error) {
	object, err := m.client.StatObject(ctx, m.bucket, m.relPath(p), minio.StatObjectOptions{})
	if err != nil {
		return nil, err
	}
	return fileInfo{object}, nil
}

func (m *Minio) Get(ctx context.Context, p string) (f io.ReadCloser, err error) {
	object, err := m.client.GetObject(ctx, m.bucket, m.relPath(p), minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	return object, nil
}

func (m *Minio) Put(ctx context.Context, p string, f io.Reader) (err error) {
	_, err = m.client.PutObject(ctx, m.bucket, m.relPath(p), f, -1, minio.PutObjectOptions{})
	if err != nil {
		return err
	}
	return nil
}

func (m *Minio) Del(ctx context.Context, p string) error {
	return m.client.RemoveObject(ctx, m.bucket, m.relPath(p), minio.RemoveObjectOptions{})
}
