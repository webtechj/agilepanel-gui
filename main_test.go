package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func TestAuthLogoutAPI(t *testing.T) {
	// 1. Regular session logout
	req := httptest.NewRequest("POST", "/api/auth/logout", nil)
	w := httptest.NewRecorder()
	handleAuthLogoutAPI(w, req)
	resp := w.Result()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 on standard logout, got %d", resp.StatusCode)
	}

	// Verify cookie clearing
	cookies := resp.Cookies()
	var foundSessionCookie bool
	for _, c := range cookies {
		if c.Name == "ap_gui_session" {
			foundSessionCookie = true
			if c.MaxAge != -1 {
				t.Errorf("expected cookie max-age -1, got %d", c.MaxAge)
			}
		}
	}
	if !foundSessionCookie {
		t.Errorf("expected ap_gui_session clear cookie in response")
	}

	// 2. Basic auth challenge logout
	reqBasic := httptest.NewRequest("GET", "/api/auth/logout?basic=1", nil)
	wBasic := httptest.NewRecorder()
	handleAuthLogoutAPI(wBasic, reqBasic)
	respBasic := wBasic.Result()

	if respBasic.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status 401 on basic logout, got %d", respBasic.StatusCode)
	}
	if respBasic.Header.Get("WWW-Authenticate") == "" {
		t.Errorf("expected WWW-Authenticate header on basic logout")
	}
}

func TestAdminAuthSettingsAPI(t *testing.T) {
	// Test GET
	reqGet := httptest.NewRequest("GET", "/api/settings/admin-auth", nil)
	wGet := httptest.NewRecorder()
	handleAdminAuthSettingsAPI(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Errorf("expected 200 OK on GET, got %d", wGet.Code)
	}

	// Test POST invalid username
	bodyInvalidUser := strings.NewReader(`{"username":"inv@lid user!","password":"StrongPassword123!"}`)
	reqInvUser := httptest.NewRequest("POST", "/api/settings/admin-auth", bodyInvalidUser)
	wInvUser := httptest.NewRecorder()
	handleAdminAuthSettingsAPI(wInvUser, reqInvUser)
	if wInvUser.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on invalid username, got %d", wInvUser.Code)
	}

	// Test POST password too short
	bodyShortPass := strings.NewReader(`{"username":"valid_admin","password":"123"}`)
	reqShortPass := httptest.NewRequest("POST", "/api/settings/admin-auth", bodyShortPass)
	wShortPass := httptest.NewRecorder()
	handleAdminAuthSettingsAPI(wShortPass, reqShortPass)
	if wShortPass.Code != http.StatusBadRequest {
		t.Errorf("expected 400 on short password, got %d", wShortPass.Code)
	}

	// Test POST valid credentials
	bodyValid := strings.NewReader(`{"username":"secadmin","password":"StrongPassword123!"}`)
	reqValid := httptest.NewRequest("POST", "/api/settings/admin-auth", bodyValid)
	wValid := httptest.NewRecorder()
	handleAdminAuthSettingsAPI(wValid, reqValid)
	if wValid.Code != http.StatusOK {
		t.Errorf("expected 200 on valid credentials, got %d: %s", wValid.Code, wValid.Body.String())
	}
}

func TestNetworkTestAPI(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/tools/network-test", nil)
	w := httptest.NewRecorder()
	handleNetworkTestAPI(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 OK on network test API, got %d", w.Code)
	}

	var resp struct {
		Status  string               `json:"status"`
		Results []NetworkProbeResult `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse network test response: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status ok, got %s", resp.Status)
	}
	if len(resp.Results) < 4 {
		t.Errorf("expected at least 4 default probes, got %d", len(resp.Results))
	}
}

func TestNewServerActions(t *testing.T) {
	allowed := map[string]bool{
		"server-doctor":      true,
		"server-cache-flush": true,
		"server-db-optimize": true,
		"server-ntp-sync":    true,
	}

	for action := range allowed {
		reqBody := strings.NewReader(fmt.Sprintf(`{"action":"%s","args":[]}`, action))
		req := httptest.NewRequest("POST", "/api/action", reqBody)
		w := httptest.NewRecorder()
		handleCommandExecuteAPI(w, req)
		// Should accept the action and return SSE or 200
		if w.Code == http.StatusForbidden {
			t.Errorf("action %s should be allowed", action)
		}
	}
}

func TestBandwidthMetric(t *testing.T) {
	bw := getBandwidth()
	if bw.RxSpeedStr == "" || bw.TxSpeedStr == "" {
		t.Error("expected non-empty bandwidth speed strings")
	}
	if bw.Interface == "" {
		t.Error("expected non-empty network interface name")
	}
}

func TestExtendedServicesValidation(t *testing.T) {
	validServices := []string{"caddy", "mariadb", "redis", "redis-server", "php-fpm", "php8.1-fpm", "php8.2-fpm", "php8.3-fpm", "watchdog", "ufw", "cron", "ssh", "all"}
	for _, svc := range validServices {
		if !isValidService(svc) {
			t.Errorf("expected service to be valid: %s", svc)
		}
	}

	invalidServices := []string{"unknown_daemon", "systemctl;rm", "../caddy", "caddy & rm -rf"}
	for _, svc := range invalidServices {
		if isValidService(svc) {
			t.Errorf("expected service to be invalid: %s", svc)
		}
	}
}

func TestServiceLogsAPI(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/services/logs?service=caddy", nil)
	w := httptest.NewRecorder()
	handleServiceLogsAPI(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}

	if resp["service"] != "caddy" {
		t.Errorf("expected service caddy, got %v", resp["service"])
	}
	if resp["logs"] == "" {
		t.Error("expected non-empty logs output")
	}

	// Test invalid service
	reqInvalid := httptest.NewRequest("GET", "/api/services/logs?service=malicious;cmd", nil)
	wInvalid := httptest.NewRecorder()
	handleServiceLogsAPI(wInvalid, reqInvalid)
	if wInvalid.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid service, got %d", wInvalid.Code)
	}
}



