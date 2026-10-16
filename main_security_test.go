package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSecurityPathWhitelistValidation(t *testing.T) {
	// Root dir setup for test
	var root string
	if runtime.GOOS == "windows" {
		root = "./var/www"
	} else {
		root = "/var/www"
	}
	absRoot, _ := filepath.Abs(root)
	_ = os.MkdirAll(absRoot, 0755)
	defer func() {
		if runtime.GOOS == "windows" {
			_ = os.RemoveAll("./var")
		}
	}()

	tests := []struct {
		domain    string
		path      string
		shouldErr bool
	}{
		{"", "test.txt", false},
		{"example.com", "test.txt", false},
		{"example.com", "../example.com-malicious/test.txt", true},
		{"example.com", "../../passwd", true},
		{"invalid/domain", "test.txt", true},
	}

	for _, tc := range tests {
		_, err := validateFilePath(tc.domain, tc.path)
		if tc.shouldErr && err == nil {
			t.Errorf("expected validation error for domain %q path %q, got nil", tc.domain, tc.path)
		} else if !tc.shouldErr && err != nil {
			t.Errorf("expected no error for domain %q path %q, got: %v", tc.domain, tc.path, err)
		}
	}
}

func TestSecurityRateLimiter(t *testing.T) {
	// Clear rate limits
	rateLimiterMutex.Lock()
	rateLimiterMap = make(map[string]*TokenBucket)
	rateLimiterMutex.Unlock()

	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewBuffer([]byte("{}")))
	req.RemoteAddr = "12.34.56.78:1234"

	// Mock handler
	handler := rateLimitMiddleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Make 11 requests (limit capacity is 10 tokens)
	for i := 0; i < 11; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		resp := w.Result()
		if i < 10 {
			if resp.StatusCode != http.StatusOK {
				t.Errorf("request %d: expected status 200, got %d", i, resp.StatusCode)
			}
		} else {
			if resp.StatusCode != http.StatusTooManyRequests {
				t.Errorf("request %d (exceeding limit): expected status 429, got %d", i, resp.StatusCode)
			}
		}
	}
}

func TestSecuritySessionGeneration(t *testing.T) {
	token := createSession()
	if len(token) != 64 { // hex string of 32 bytes should be 64 characters
		t.Errorf("expected token length of 64 characters, got %d", len(token))
	}

	sessionMutex.RLock()
	expiry, exists := activeSessions[token]
	sessionMutex.RUnlock()

	if !exists {
		t.Fatalf("expected token %q to be active", token)
	}

	dur := time.Until(expiry)
	if dur <= 55*time.Minute || dur > 1*time.Hour {
		t.Errorf("expected session duration to be approx 1 hour, got %v", dur)
	}
}

func TestSecurityCommandArgumentSanitization(t *testing.T) {
	safeArgRegex := regexp.MustCompile(`^[a-zA-Z0-9._-]*$`)
	
	validArgs := []string{"wp", "example.com", "db_name", "8.3", "import_files.zip"}
	for _, arg := range validArgs {
		if !safeArgRegex.MatchString(arg) {
			t.Errorf("expected argument %q to be valid", arg)
		}
	}

	invalidArgs := []string{"wp;rm", "example.com|", "db name", "8.3&", "../etc"}
	for _, arg := range invalidArgs {
		if safeArgRegex.MatchString(arg) {
			t.Errorf("expected argument %q to be invalid", arg)
		}
	}
}

func TestSecurityAuditEndpoint(t *testing.T) {
	audit := performSecurityAudit()
	if audit == nil {
		t.Fatal("expected audit result to not be nil")
	}
	if audit.Score < 0 || audit.Score > 100 {
		t.Errorf("invalid audit score: %d", audit.Score)
	}
	if len(audit.Checks) == 0 {
		t.Error("expected at least one security check in audit")
	}

	req := httptest.NewRequest("GET", "/api/security/audit", nil)
	w := httptest.NewRecorder()
	handleSecurityAuditAPI(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestServeStaticETag(t *testing.T) {
	handler := serveStatic("style.css", "text/css")
	req := httptest.NewRequest("GET", "/style.css", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Error("expected non-empty ETag header")
	}

	// Conditional request with matching If-None-Match
	req2 := httptest.NewRequest("GET", "/style.css", nil)
	req2.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)

	resp2 := w2.Result()
	if resp2.StatusCode != http.StatusNotModified {
		t.Errorf("expected status 304 Not Modified, got %d", resp2.StatusCode)
	}
}

func TestGzipMiddleware(t *testing.T) {
	testPayload := strings.Repeat("AgilePanel High Performance Dashboard ", 100)
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(testPayload))
	})
	wrapped := gzipMiddleware(dummyHandler)

	// 1. Request with Accept-Encoding: gzip
	reqGzip := httptest.NewRequest("GET", "/api/status", nil)
	reqGzip.Header.Set("Accept-Encoding", "gzip, deflate")
	recGzip := httptest.NewRecorder()
	wrapped.ServeHTTP(recGzip, reqGzip)

	resGzip := recGzip.Result()
	if resGzip.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("expected Content-Encoding: gzip, got %q", resGzip.Header.Get("Content-Encoding"))
	}
	gr, err := gzip.NewReader(resGzip.Body)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gr.Close()
	decompressed, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("failed to decompress gzip body: %v", err)
	}
	if string(decompressed) != testPayload {
		t.Errorf("decompressed payload mismatch")
	}

	// 2. Request without Accept-Encoding: gzip
	reqPlain := httptest.NewRequest("GET", "/api/status", nil)
	recPlain := httptest.NewRecorder()
	wrapped.ServeHTTP(recPlain, reqPlain)

	resPlain := recPlain.Result()
	if resPlain.Header.Get("Content-Encoding") == "gzip" {
		t.Errorf("expected no gzip encoding for non-gzip request")
	}
	plainBody, _ := io.ReadAll(resPlain.Body)
	if string(plainBody) != testPayload {
		t.Errorf("plain payload mismatch")
	}

	// 3. Streaming endpoint should bypass gzip
	reqTail := httptest.NewRequest("GET", "/api/terminal/tail", nil)
	reqTail.Header.Set("Accept-Encoding", "gzip")
	recTail := httptest.NewRecorder()
	wrapped.ServeHTTP(recTail, reqTail)
	resTail := recTail.Result()
	if resTail.Header.Get("Content-Encoding") == "gzip" {
		t.Errorf("streaming endpoints should bypass gzip compression")
	}
}

func TestFileChmodValidation(t *testing.T) {
	// Setup temporary testing directory and file
	tempDir, err := os.MkdirTemp("", "chmod_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Invalid octal mode tests
	invalidModes := []string{"0999", "888", "invalid", "07777", "0755;rm -rf", ""}
	for _, m := range invalidModes {
		body, _ := json.Marshal(map[string]interface{}{
			"domain": "example.com",
			"path":   "test.txt",
			"mode":   m,
		})
		req := httptest.NewRequest("POST", "/api/files/chmod", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		handleFileChmodAPI(rec, req)
		res := rec.Result()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("expected mode %q to be rejected with 400 Bad Request, got %d", m, res.StatusCode)
		}
	}

	// Traversal path attack test
	traversalBody, _ := json.Marshal(map[string]interface{}{
		"domain": "example.com",
		"path":   "../../etc/passwd",
		"mode":   "0644",
	})
	req := httptest.NewRequest("POST", "/api/files/chmod", bytes.NewReader(traversalBody))
	rec := httptest.NewRecorder()
	handleFileChmodAPI(rec, req)
	res := rec.Result()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("expected path traversal in chmod to be rejected with 403 Forbidden, got %d", res.StatusCode)
	}
}

func TestFileBatchDeleteSecurity(t *testing.T) {
	// Test traversal in batch delete
	payload, _ := json.Marshal(map[string]interface{}{
		"domain": "example.com",
		"paths":  []string{"../../etc/shadow", "valid_file.txt"},
	})
	req := httptest.NewRequest("POST", "/api/files/batch-delete", bytes.NewReader(payload))
	rec := httptest.NewRecorder()
	handleFileBatchDeleteAPI(rec, req)

	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", res.StatusCode)
	}

	var resp struct {
		Success bool     `json:"success"`
		Deleted int      `json:"deleted"`
		Errors  []string `json:"errors"`
	}
	json.NewDecoder(res.Body).Decode(&resp)

	// Check that the directory traversal path was caught in errors
	foundError := false
	for _, errStr := range resp.Errors {
		if strings.Contains(errStr, "access denied") || strings.Contains(errStr, "path traversal") || strings.Contains(errStr, "shadow") {
			foundError = true
			break
		}
	}
	if !foundError {
		t.Errorf("expected directory traversal to be caught in batch delete errors: %v", resp.Errors)
	}
}

func TestServiceStatusCache(t *testing.T) {
	// Clear cache
	invalidateServiceCache()

	// Initial fetch
	status1 := getServiceStatus("caddy")
	
	// Second fetch should hit cache within TTL
	status2 := getServiceStatus("caddy")
	if status1 != status2 {
		t.Errorf("cached service status mismatch: %v vs %v", status1, status2)
	}

	// Invalidation clears cache cleanly
	invalidateServiceCache()
	status3 := getServiceStatus("caddy")
	if status1 != status3 {
		t.Errorf("post-invalidation service status mismatch: %v vs %v", status1, status3)
	}
}
