package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/spf13/pflag"

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
	linkExpires             time.Duration
	hostFromFirstPath       bool
	checkSyncTimeout        time.Duration
	ContinuationGetInterval time.Duration
	ContinuationGetRetry    int
	BlockSuffix             []string
)

func init() {
	pflag.StringVar(&address, "address", ":8080", "listen on the address")
	pflag.StringVar(&endpoint, "s3-endpoint", "", "endpoint")
	pflag.StringVar(&bucket, "s3-bucket", "", "bucket")
	pflag.StringVar(&accessKeyID, "s3-access-key-id", "", "access key id")
	pflag.StringVar(&accessKeySecret, "s3-access-key-secret", "", "access key secret")
	pflag.StringVar(&redirectLinks, "s3-redirect-links", "", "redirect links")
	pflag.DurationVar(&linkExpires, "s3-link-expires", 0, "link expires")
	pflag.BoolVar(&hostFromFirstPath, "host-from-first-path", false, "host from first path")
	pflag.DurationVar(&checkSyncTimeout, "check-sync-timeout", 0, "check sync timeout")
	pflag.DurationVar(&ContinuationGetInterval, "continuation-get-interval", 0, "continuation get interval")
	pflag.IntVar(&ContinuationGetRetry, "continuation-get-retry", 0, "continuation get retry")
	pflag.StringSliceVar(&BlockSuffix, "block-suffix", nil, "Block source suffix")

	pflag.Parse()
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
		transport = httpseek.NewMustReaderTransport(transport, func(r *http.Request, retry int, err error) error {
			if ContinuationGetRetry > 0 && retry >= ContinuationGetRetry {
				return err
			}
			logger.Println("Retry cache", r.URL, retry, err)
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
		LinkExpires:       linkExpires,
		CheckSyncTimeout:  checkSyncTimeout,
		HostFromFirstPath: hostFromFirstPath,
		BlockSuffix:       BlockSuffix,
	}

	logger.Println("listen on", address)
	err := http.ListenAndServe(address, ph)
	if err != nil {
		logger.Println(err)
		os.Exit(1)
	}
}
