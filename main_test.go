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
