package httpmirror

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHttpHead(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		contentLength  int64
		lastModified   string
		expectedErr    error
		expectFileInfo bool
	}{
		{
			name:           "successful head request",
			statusCode:     http.StatusOK,
			contentLength:  1024,
			lastModified:   time.Now().Format(http.TimeFormat),
			expectedErr:    nil,
			expectFileInfo: true,
		},
		{
			name:           "not found",
			statusCode:     http.StatusNotFound,
			contentLength:  0,
			expectedErr:    ErrNotOK,
			expectFileInfo: false,
		},
		{
			name:           "server error",
			statusCode:     http.StatusInternalServerError,
			contentLength:  0,
			expectedErr:    ErrNotOK,
			expectFileInfo: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead {
					t.Errorf("Expected HEAD method, got %s", r.Method)
				}
				if tt.lastModified != "" {
					w.Header().Set("Last-Modified", tt.lastModified)
				}
				if tt.contentLength > 0 {
					w.Header().Set("Content-Length", fmt.Sprintf("%d", tt.contentLength))
				}
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			client := server.Client()
			info, err := httpHead(context.Background(), client, server.URL)

			if tt.expectedErr != nil {
				if err == nil {
					t.Errorf("Expected error %v, got nil", tt.expectedErr)
				} else if !errors.Is(err, tt.expectedErr) {
					t.Errorf("Expected error %v, got %v", tt.expectedErr, err)
				}
			} else if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if tt.expectFileInfo && info == nil {
				t.Error("Expected file info, got nil")
			} else if !tt.expectFileInfo && info != nil {
				t.Error("Expected nil file info, got non-nil")
			}

			if info != nil {
				if info.Name() != server.URL {
					t.Errorf("Expected name %s, got %s", server.URL, info.Name())
				}
				if info.IsDir() {
					t.Error("Expected IsDir to be false")
				}
			}
		})
	}
}

func TestHttpGet(t *testing.T) {
	tests := []struct {
		name          string
		statusCode    int
		contentLength int64
		body          string
		expectedErr   error
		expectBody    bool
	}{
		{
			name:          "successful get request",
			statusCode:    http.StatusOK,
			contentLength: 5,
			body:          "hello",
			expectedErr:   nil,
			expectBody:    true,
		},
		{
			name:          "not found",
			statusCode:    http.StatusNotFound,
			contentLength: 0,
			body:          "",
			expectedErr:   ErrNotOK,
			expectBody:    false,
		},
		{
			name:          "server error",
			statusCode:    http.StatusInternalServerError,
			contentLength: 0,
			body:          "",
			expectedErr:   ErrNotOK,
			expectBody:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("Expected GET method, got %s", r.Method)
				}
				w.WriteHeader(tt.statusCode)
				if tt.body != "" {
					w.Write([]byte(tt.body))
				}
			}))
			defer server.Close()

			client := server.Client()
			body, info, err := httpGet(context.Background(), client, server.URL)

			if tt.expectedErr != nil {
				if err == nil {
					t.Errorf("Expected error %v, got nil", tt.expectedErr)
				} else if !errors.Is(err, tt.expectedErr) {
					t.Errorf("Expected error %v, got %v", tt.expectedErr, err)
				}
			} else if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if tt.expectBody {
				if body == nil {
					t.Error("Expected body, got nil")
				} else {
					defer body.Close()
				}
				if info == nil {
					t.Error("Expected file info, got nil")
				}
			} else {
				if body != nil {
					t.Error("Expected nil body, got non-nil")
					body.Close()
				}
				if info != nil {
					t.Error("Expected nil file info, got non-nil")
				}
			}
		})
	}
}

func TestFileInfo(t *testing.T) {
	t.Run("fileInfo methods", func(t *testing.T) {
		lastModified := time.Now().Truncate(time.Second)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "1024")
			w.Header().Set("Last-Modified", lastModified.Format(http.TimeFormat))
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := server.Client()
		info, err := httpHead(context.Background(), client, server.URL)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if info.Name() != server.URL {
			t.Errorf("Expected name %s, got %s", server.URL, info.Name())
		}

		if info.IsDir() {
			t.Error("Expected IsDir to be false")
		}

		if info.Mode() != 0 {
			t.Errorf("Expected mode 0, got %v", info.Mode())
		}

		if info.Sys() == nil {
			t.Error("Expected Sys to return non-nil")
		}

		if _, ok := info.Sys().(*http.Response); !ok {
			t.Error("Expected Sys to return *http.Response")
		}

		// ModTime should match lastModified
		if !info.ModTime().Equal(lastModified) {
			t.Errorf("Expected ModTime %v, got %v", lastModified, info.ModTime())
		}

		// Test String method (type assert to access it)
		if fi, ok := info.(interface{ String() string }); ok {
			str := fi.String()
			if str == "" {
				t.Error("Expected non-empty string representation")
			}
		} else {
			t.Error("Expected fileInfo to have String method")
		}
	})

	t.Run("fileInfo with missing Last-Modified header", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "512")
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := server.Client()
		info, err := httpHead(context.Background(), client, server.URL)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		// ModTime should return zero time
		if !info.ModTime().IsZero() {
			t.Errorf("Expected zero time, got %v", info.ModTime())
		}
	})

	t.Run("fileInfo with invalid Last-Modified header", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "512")
			w.Header().Set("Last-Modified", "invalid-date")
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client := server.Client()
		info, err := httpHead(context.Background(), client, server.URL)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		// ModTime should return zero time when parsing fails
		if !info.ModTime().IsZero() {
			t.Errorf("Expected zero time, got %v", info.ModTime())
		}
	})
}

func TestHttpHeadWithContext(t *testing.T) {
	t.Run("context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		client := server.Client()
		_, err := httpHead(ctx, client, server.URL)

		if err == nil {
			t.Error("Expected error due to context cancellation")
		}
	})
}

func TestHttpGetWithContext(t *testing.T) {
	t.Run("context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		client := server.Client()
		_, _, err := httpGet(ctx, client, server.URL)

		if err == nil {
			t.Error("Expected error due to context cancellation")
		}
	})
}
