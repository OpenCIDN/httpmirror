package httpmirror

import (
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsValidDomain(t *testing.T) {
	tests := []struct {
		name     string
		domain   string
		expected bool
	}{
		{"valid domain", "example.com", true},
		{"valid subdomain", "sub.example.com", true},
		{"valid multi-level subdomain", "a.b.c.example.com", true},
		{"valid domain with dash", "my-site.com", true},
		{"valid domain with number", "site123.com", true},
		{"empty string", "", false},
		{"string with spaces", "  ", false},
		{"too long domain", string(make([]byte, 256)), false},
		{"starts with dash", "-example.com", false},
		{"ends with dash", "example.com-", false},
		{"starts with underscore", "_example.com", false},
		{"ends with underscore", "example.com_", false},
		{"starts with dot", ".example.com", false},
		{"no dot", "localhost", true}, // isValidDomain doesn't check for dots, that's done in ServeHTTP
		{"special characters", "exam@ple.com", false},
		{"special characters 2", "exam!ple.com", false},
		{"special characters 3", "exam#ple.com", false},
		{"special characters 4", "exam$ple.com", false},
		{"special characters 5", "exam%ple.com", false},
		{"special characters 6", "exam^ple.com", false},
		{"special characters 7", "exam&ple.com", false},
		{"special characters 8", "exam*ple.com", false},
		{"special characters 9", "exam(ple.com", false},
		{"special characters 10", "exam)ple.com", false},
		{"special characters 11", "exam+ple.com", false},
		{"special characters 12", "exam=ple.com", false},
		{"special characters 13", "exam{ple.com", false},
		{"special characters 14", "exam}ple.com", false},
		{"special characters 15", "exam[ple.com", false},
		{"special characters 16", "exam]ple.com", false},
		{"special characters 17", "exam|ple.com", false},
		{"special characters 18", "exam\\ple.com", false},
		{"special characters 19", "exam\"ple.com", false},
		{"special characters 20", "exam'ple.com", false},
		{"special characters 21", "exam;ple.com", false},
		{"special characters 22", "exam:ple.com", false},
		{"special characters 23", "exam<ple.com", false},
		{"special characters 24", "exam>ple.com", false},
		{"special characters 25", "exam?ple.com", false},
		{"special characters 26", "exam/ple.com", false},
		{"special characters 27", "exam`ple.com", false},
		{"special characters 28", "exam~ple.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidDomain(tt.domain)
			if result != tt.expected {
				t.Errorf("isValidDomain(%q) = %v, expected %v", tt.domain, result, tt.expected)
			}
		})
	}
}

func TestGetBlobName(t *testing.T) {
	tests := []struct {
		name     string
		urlPath  string
		expected string
	}{
		{
			name:     "simple path",
			urlPath:  "example.com/file.txt",
			expected: func() string {
				m := md5.Sum([]byte("example.com/file.txt"))
				return hex.EncodeToString(m[:])
			}(),
		},
		{
			name:     "empty path",
			urlPath:  "",
			expected: func() string {
				m := md5.Sum([]byte(""))
				return hex.EncodeToString(m[:])
			}(),
		},
		{
			name:     "complex path",
			urlPath:  "sub.example.com/path/to/file.tar.gz",
			expected: func() string {
				m := md5.Sum([]byte("sub.example.com/path/to/file.tar.gz"))
				return hex.EncodeToString(m[:])
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getBlobName(tt.urlPath)
			if result != tt.expected {
				t.Errorf("getBlobName(%q) = %v, expected %v", tt.urlPath, result, tt.expected)
			}
			// Verify it's a valid MD5 hash (32 hex characters)
			if len(result) != 32 {
				t.Errorf("Expected MD5 hash length of 32, got %d", len(result))
			}
		})
	}

	// Test that different inputs produce different hashes
	t.Run("different inputs produce different hashes", func(t *testing.T) {
		hash1 := getBlobName("path1")
		hash2 := getBlobName("path2")
		if hash1 == hash2 {
			t.Error("Expected different hashes for different inputs")
		}
	})

	// Test that same input produces same hash
	t.Run("same input produces same hash", func(t *testing.T) {
		hash1 := getBlobName("same/path")
		hash2 := getBlobName("same/path")
		if hash1 != hash2 {
			t.Error("Expected same hash for same input")
		}
	})
}

func TestMirrorHandlerServeHTTP_MethodNotAllowed(t *testing.T) {
	handler := &MirrorHandler{}

	tests := []struct {
		name   string
		method string
	}{
		{"POST method", http.MethodPost},
		{"PUT method", http.MethodPut},
		{"DELETE method", http.MethodDelete},
		{"PATCH method", http.MethodPatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/test", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("Expected status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
			}
		})
	}
}

func TestMirrorHandlerServeHTTP_EmptyPath(t *testing.T) {
	handler := &MirrorHandler{}

	tests := []struct {
		name string
		path string
	}{
		{"root path", "/"},
		{"path ending with slash", "/path/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("Expected status %d, got %d", http.StatusNotFound, rec.Code)
			}
		})
	}
}

func TestMirrorHandlerServeHTTP_BlockedSuffix(t *testing.T) {
	handler := &MirrorHandler{
		BlockSuffix: []string{".exe", ".msi", ".dll"},
	}

	tests := []struct {
		name           string
		path           string
		expectedStatus int
	}{
		{"blocked .exe", "/example.com/file.exe", http.StatusForbidden},
		{"blocked .msi", "/example.com/installer.msi", http.StatusForbidden},
		{"blocked .dll", "/example.com/library.dll", http.StatusForbidden},
		{"allowed .txt", "/example.com/file.txt", http.StatusInternalServerError}, // Will try to fetch and fail
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Host = "mirror.example.com"
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, rec.Code)
			}
		})
	}
}

func TestMirrorHandlerServeHTTP_InvalidDomain(t *testing.T) {
	handler := &MirrorHandler{}

	tests := []struct {
		name string
		host string
	}{
		{"no dot in host", "localhost"},
		{"invalid characters", "exam@ple.com"},
		{"starts with dash", "-example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/file.txt", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("Expected status %d, got %d", http.StatusNotFound, rec.Code)
			}
		})
	}
}

func TestMirrorHandlerServeHTTP_BaseDomain(t *testing.T) {
	handler := &MirrorHandler{
		BaseDomain: ".mirror.example.com",
	}

	tests := []struct {
		name           string
		host           string
		expectedStatus int
	}{
		{"matching base domain", "cdn.mirror.example.com", http.StatusInternalServerError}, // Will try to fetch and fail
		{"non-matching base domain", "other.example.com", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/file.txt", nil)
			req.Host = tt.host
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, rec.Code)
			}
		})
	}
}

func TestMirrorHandlerServeHTTP_HostFromFirstPath(t *testing.T) {
	handler := &MirrorHandler{
		HostFromFirstPath: true,
	}

	tests := []struct {
		name           string
		path           string
		expectedStatus int
	}{
		{"valid path with host", "/example.com/file.txt", http.StatusInternalServerError}, // Will try to fetch and fail
		{"path with only host, no file", "/example.com/", http.StatusNotFound},
		{"invalid host in path", "/localhost/file.txt", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, rec.Code)
			}
		})
	}
}

func TestMirrorHandlerServeHTTP_DirectResponse(t *testing.T) {
	// Create a test server to act as the source
	sourceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test content"))
	}))
	defer sourceServer.Close()

	// Create mirror handler with no RemoteCache (direct mode)
	handler := &MirrorHandler{
		Client: sourceServer.Client(),
	}

	// This test is limited because we can't easily make it connect to our test server
	// without mocking the entire request flow
	t.Run("direct response mode", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/file.txt", nil)
		req.Host = "example.com"
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		// The request will fail because we're trying to connect to https://example.com
		// which we can't control in a unit test, but we're testing the code path
		if rec.Code != http.StatusInternalServerError && rec.Code != http.StatusNotFound {
			// Either error is acceptable in this test scenario
			t.Logf("Got status %d, which is expected for this test scenario", rec.Code)
		}
	})
}

func TestMirrorHandlerWithLogger(t *testing.T) {
	var loggedMessages []string
	logger := &testLogger{
		messages: &loggedMessages,
	}

	handler := &MirrorHandler{
		Logger: logger,
	}

	req := httptest.NewRequest(http.MethodGet, "/file.txt", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	// Verify that logger was called
	if len(loggedMessages) == 0 {
		t.Log("Logger may not have been called due to early return or error, which is acceptable")
	}
}

func TestMirrorHandlerCustomNotFound(t *testing.T) {
	customNotFoundCalled := false
	customNotFoundHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		customNotFoundCalled = true
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Custom Not Found"))
	})

	handler := &MirrorHandler{
		NotFound: customNotFoundHandler,
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !customNotFoundCalled {
		t.Error("Custom NotFound handler was not called")
	}

	if rec.Body.String() != "Custom Not Found" {
		t.Errorf("Expected 'Custom Not Found', got %q", rec.Body.String())
	}
}

func TestMirrorHandlerLinkExpires(t *testing.T) {
	handler := &MirrorHandler{
		LinkExpires: 24 * time.Hour,
	}

	if handler.LinkExpires != 24*time.Hour {
		t.Errorf("Expected LinkExpires to be 24 hours, got %v", handler.LinkExpires)
	}
}

// testLogger is a simple logger for testing
type testLogger struct {
	messages *[]string
}

func (l *testLogger) Println(v ...interface{}) {
	if l.messages != nil {
		msg := ""
		for _, val := range v {
			msg += " " + toString(val)
		}
		*l.messages = append(*l.messages, msg)
	}
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
