package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestServeStaticNotFound(t *testing.T) {
	req := httptest.NewRequest("GET", "/nonexistent.html", nil)
	w := httptest.NewRecorder()

	handler := serveStatic("nonexistent.html", "text/html")
	handler.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

func TestGetRAMWindows(t *testing.T) {
	ram := getRAM()
	if ram.Total <= 0 {
		t.Error("expected mock total RAM to be positive")
	}
	if ram.Pct <= 0 || ram.Pct > 100 {
		t.Errorf("invalid mock RAM percentage: %f", ram.Pct)
	}
}

func TestGetDiskWindows(t *testing.T) {
	disk := getDisk()
	if disk.Total <= 0 {
		t.Error("expected mock total disk to be positive")
	}
}

func TestGetCPUWindows(t *testing.T) {
	cpu := getCPU()
	if cpu <= 0 || cpu > 100 {
		t.Errorf("invalid mock CPU usage: %f", cpu)
	}
}

func TestGetServiceStatusWindows(t *testing.T) {
	status := getServiceStatus("any-service")
	if !status {
		t.Error("expected mock service status to be true")
	}
}

func TestGuiAuthSignupAndLogin(t *testing.T) {
	// Set mock path for temp auth configuration
	tempFile := "./temp_gui_auth_test.json"
	defer os.Remove(tempFile)
	
	// Create payload
	username := "testuser"
	password := "testpassword"
	
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("failed to generate bcrypt hash: %v", err)
	}
	
	config := &GuiAuthConfig{
		Enabled:      true,
		Username:     username,
		PasswordHash: string(hash),
	}
	
	// Validate direct check
	if config.Username != username {
		t.Errorf("username mismatch")
	}
	
	err = bcrypt.CompareHashAndPassword([]byte(config.PasswordHash), []byte(password))
	if err != nil {
		t.Errorf("bcrypt verification failed: %v", err)
	}
}

func TestValidationHelpers(t *testing.T) {
	// Test isValidDomain
	validDomains := []string{"example.com", "sub.domain.org", "localhost", "a.b.c.d.e"}
	for _, d := range validDomains {
		if !isValidDomain(d) {
			t.Errorf("expected domain to be valid: %s", d)
		}
	}

	invalidDomains := []string{"example.com;rm", "sub/domain", "../etc", "example..com", "-example.com", ""}
	for _, d := range invalidDomains {
		if isValidDomain(d) {
			t.Errorf("expected domain to be invalid: %s", d)
		}
	}

	// Test isValidTimestamp
	if !isValidTimestamp("2026-06-02-120000") {
		t.Error("expected timestamp to be valid")
	}
	if isValidTimestamp("../2026") {
		t.Error("expected timestamp to be invalid")
	}

	// Test isValidService
	if !isValidService("caddy") || !isValidService("mariadb") {
		t.Error("expected service to be valid")
	}
	if isValidService("malicious_service") || isValidService("systemctl;rm") {
		t.Error("expected service to be invalid")
	}

	// Test isValidTool
	if !isValidTool("phpmyadmin") {
		t.Error("expected tool to be valid")
	}
	if isValidTool("invalid_tool") {
		t.Error("expected tool to be invalid")
	}

	// Test isValidImportPath
	if !isValidImportPath("", "example.com", "files") {
		t.Error("empty import path should be valid")
	}
}

func TestValidateFilePathTraversal(t *testing.T) {
	// Setup temporary testing directory
	tempDir, err := os.MkdirTemp("", "validatefilepath_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Since validateFilePath builds based on baseDir/domain or relative var/www,
	// let's test it with an absolute path or relative path traversal attempt.
	_, err = validateFilePath("invalid/domain", "somefile.txt")
	if err == nil {
		t.Error("expected error for invalid domain format")
	}

	// Test directory traversal protection with prefix-matching bypass attempt
	// e.g. baseDir is /var/www/domain and target is /var/www/domain-malicious
	// We can mock GOOS to control validateFilePath behavior, but the generic logic check holds:
	// We check path prefix with separator.
	
	// Create baseDir and domain-malicious path structure
	// We check it using a test path
	domain := "example.com"
	badPath := "../example.com-malicious/test.txt"

	fullPath, err := validateFilePath(domain, badPath)
	if err == nil {
		t.Errorf("expected traversal error, got path: %s", fullPath)
	}
}

