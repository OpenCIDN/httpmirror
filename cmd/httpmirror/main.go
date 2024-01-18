package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"errors"
	"fmt"
	"github.com/wzshiming/httpmirror"
	"github.com/wzshiming/httpmirror/minio"
	"time"
)

var (
	address           string
	endpoint          string
	bucket            string
	accessKeyID       string
	accessKeySecret   string
	hostFromFirstPath bool
	checkSyncTimeout  time.Duration
)

func init() {
	flag.StringVar(&address, "address", ":8080", "listen on the address")
	flag.StringVar(&endpoint, "endpoint", "", "endpoint")
	flag.StringVar(&bucket, "bucket", "", "bucket")
	flag.StringVar(&accessKeyID, "access-key-id", "", "access key id")
	flag.StringVar(&accessKeySecret, "access-key-secret", "", "access key secret")
	flag.BoolVar(&hostFromFirstPath, "host-from-first-path", false, "host from first path")
	flag.DurationVar(&checkSyncTimeout, "check-sync-timeout", 0, "check sync timeout")

	flag.Parse()
}

func main() {
	logger := log.New(os.Stderr, "[http mirror] ", log.LstdFlags)

	var client *minio.Minio

	if endpoint != "" {
		c, err := minio.NewMinio(minio.Config{
			Endpoint:        endpoint,
			Bucket:          bucket,
			AccessKeyID:     accessKeyID,
			AccessKeySecret: accessKeySecret,
		})
		if err != nil {
			logger.Println("failed to create minio client:", err)
			os.Exit(1)
		}
		client = c
	}

	ph := &httpmirror.MirrorHandler{
		Client: &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("stopped after 10 redirects")
				}
				logger.Println("redirect", req.URL)
				return nil
			},
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
			},
		},
		Logger:      logger,
		RemoteCache: client,
		RedirectLinks: func(p string) (string, bool) {
			return fmt.Sprintf("https://%s.%s/%s", bucket, endpoint, p), true
		},
		CheckSyncTimeout:  checkSyncTimeout,
		HostFromFirstPath: hostFromFirstPath,
	}

	logger.Println("listen on", address)
	err := http.ListenAndServe(address, ph)
	if err != nil {
		logger.Println(err)
		os.Exit(1)
	}
}
