package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/OpenCIDN/cidn/pkg/clientset/versioned"
	"github.com/OpenCIDN/cidn/pkg/informers/externalversions"
	"github.com/OpenCIDN/httpmirror"
	"github.com/spf13/pflag"
	"github.com/wzshiming/httpseek"
	"github.com/wzshiming/sss"
	"k8s.io/client-go/tools/clientcmd"
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
	NoRedirect              bool

	Kubeconfig            string
	Master                string
	InsecureSkipTLSVerify bool
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
	pflag.BoolVar(&NoRedirect, "no-redirect", false, "Serve cached content directly instead of redirecting to signed URLs")

	pflag.StringVar(&Kubeconfig, "kubeconfig", Kubeconfig, "Path to the kubeconfig file to use")
	pflag.StringVar(&Master, "master", Master, "The address of the Kubernetes API server")
	pflag.BoolVar(&InsecureSkipTLSVerify, "insecure-skip-tls-verify", false, "If true, the server's certificate will not be checked for validity. This will make your HTTPS connections insecure")

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
		NoRedirect:        NoRedirect,
	}

	if (Kubeconfig != "" || Master != "") && storageURL != "" {
		u, err := url.Parse(storageURL)
		if err != nil {
			logger.Println("failed to parse storage URL:", err)
			os.Exit(1)
		}
		config, err := clientcmd.BuildConfigFromFlags(Master, Kubeconfig)
		if err != nil {
			logger.Println("error getting config:", err)
			os.Exit(1)
		}
		config.TLSClientConfig.Insecure = InsecureSkipTLSVerify

		clientset, err := versioned.NewForConfig(config)
		if err != nil {
			logger.Println("error creating clientset:", err)
			os.Exit(1)
		}

		ph.CIDNClient = clientset
		ph.CIDNDestination = u.Scheme

		sharedInformerFactory := externalversions.NewSharedInformerFactory(clientset, 0)
		ph.CIDNBlobInformer = sharedInformerFactory.Task().V1alpha1().Blobs()
		go ph.CIDNBlobInformer.Informer().RunWithContext(context.Background())
	}

	logger.Println("listen on", address)
	err := http.ListenAndServe(address, ph)
	if err != nil {
		logger.Println(err)
		os.Exit(1)
	}
}
