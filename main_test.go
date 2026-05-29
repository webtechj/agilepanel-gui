package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
