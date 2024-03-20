package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/wzshiming/httpmirror"
	"github.com/wzshiming/httpmirror/minio"
	"github.com/wzshiming/httpseek"
)

var (
	address                 string
	endpoint                string
	bucket                  string
	accessKeyID             string
	accessKeySecret         string
	redirectLinks           string
	hostFromFirstPath       bool
	checkSyncTimeout        time.Duration
	ContinuationGetInterval time.Duration
)

func init() {
	flag.StringVar(&address, "address", ":8080", "listen on the address")
	flag.StringVar(&endpoint, "s3-endpoint", "", "endpoint")
	flag.StringVar(&bucket, "s3-bucket", "", "bucket")
	flag.StringVar(&accessKeyID, "s3-access-key-id", "", "access key id")
	flag.StringVar(&accessKeySecret, "s3-access-key-secret", "", "access key secret")
	flag.StringVar(&redirectLinks, "s3-redirect-links", "", "redirect links")
	flag.BoolVar(&hostFromFirstPath, "host-from-first-path", false, "host from first path")
	flag.DurationVar(&checkSyncTimeout, "check-sync-timeout", 0, "check sync timeout")
	flag.DurationVar(&ContinuationGetInterval, "continuation-get-interval", 0, "continuation get interval")

	flag.Parse()
}

func main() {
	logger := log.New(os.Stderr, "[http mirror] ", log.LstdFlags)

	var client httpmirror.FS

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

	var transport http.RoundTripper = http.DefaultTransport

	if ContinuationGetInterval > 0 {
		transport = httpseek.NewMustReaderTransport(transport, func(r *http.Request, err error) error {
			logger.Println("Retry cache", r.URL)
			time.Sleep(ContinuationGetInterval)
			return nil
		})
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
			Transport: transport,
		},
		Logger:      logger,
		RemoteCache: client,
		RedirectLinks: func(p string) (string, bool) {
			return fmt.Sprintf("%s/%s", redirectLinks, p), true
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
