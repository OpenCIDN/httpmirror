package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/spf13/pflag"

	"github.com/wzshiming/httpmirror"
	"github.com/wzshiming/httpseek"
	"github.com/wzshiming/sss"
)

var (
	address                 string
	storageURL              string
	linkExpires             time.Duration
	hostFromFirstPath       bool
	checkSyncTimeout        time.Duration
	ContinuationGetInterval time.Duration
	ContinuationGetRetry    int
	BlockSuffix             []string
)

func init() {
	pflag.StringVar(&address, "address", ":8080", "listen on the address")
	pflag.StringVar(&storageURL, "storage-url", "", "storage url")
	pflag.DurationVar(&linkExpires, "link-expires", 24*time.Hour, "link expires")
	pflag.BoolVar(&hostFromFirstPath, "host-from-first-path", false, "host from first path")
	pflag.DurationVar(&checkSyncTimeout, "check-sync-timeout", 0, "check sync timeout")
	pflag.DurationVar(&ContinuationGetInterval, "continuation-get-interval", 0, "continuation get interval")
	pflag.IntVar(&ContinuationGetRetry, "continuation-get-retry", 0, "continuation get retry")
	pflag.StringSliceVar(&BlockSuffix, "block-suffix", nil, "Block source suffix")

	pflag.Parse()
}

func main() {
	logger := log.New(os.Stderr, "[http mirror] ", log.LstdFlags)

	var client *sss.SSS

	if storageURL != "" {
		c, err := sss.NewSSS(sss.WithURL(storageURL))
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
		Logger:            logger,
		RemoteCache:       client,
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
