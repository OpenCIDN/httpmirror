# httpmirror

An HTTP reverse proxy and caching server that mirrors remote files with optional cloud storage backend support.

## Overview

`httpmirror` is a lightweight HTTP proxy server designed to cache and serve remote files efficiently. It acts as a caching layer between clients and remote HTTP servers, optionally storing cached content in cloud storage backends (S3, MinIO, etc.) for persistent and distributed caching.

## Features

- **HTTP Reverse Proxy**: Proxies HTTP GET and HEAD requests to remote servers
- **Intelligent Caching**: Caches remote files to reduce bandwidth and latency
- **Cloud Storage Backend**: Support for S3-compatible storage backends (via SSS library)
- **CIDN Integration**: Optional integration with Content Infrastructure Delivery Network (CIDN) for distributed blob management
- **Configurable Link Expiry**: Set custom expiration times for signed URLs
- **Health Checking**: Optional sync timeout to verify cached content freshness
- **Flexible Host Mapping**: Support for host-from-first-path routing
- **Retry Mechanism**: Configurable retry logic for failed requests
- **Suffix Blocking**: Block requests for specific file suffixes
