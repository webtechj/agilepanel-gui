package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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
