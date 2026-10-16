package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnsureSSLCertificates(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "agilepanel_ssl_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	t.Setenv("AGILEPANEL_SSL_DIR", tempDir)

	certPath, keyPath, err := ensureSSLCertificates()
	if err != nil {
		t.Fatalf("ensureSSLCertificates failed: %v", err)
	}

	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		t.Errorf("cert file does not exist at %s", certPath)
	}
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		t.Errorf("key file does not exist at %s", keyPath)
	}

	// Verify certificate contents
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("failed to read cert file: %v", err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("failed to decode certificate PEM block")
	}

	parsedCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("failed to parse x509 certificate: %v", err)
	}

	if parsedCert.Subject.CommonName != "AgilePanel GUI" {
		t.Errorf("expected CommonName 'AgilePanel GUI', got %q", parsedCert.Subject.CommonName)
	}
	if len(parsedCert.Subject.Organization) == 0 || parsedCert.Subject.Organization[0] != "AgilePanel" {
		t.Errorf("expected Organization 'AgilePanel', got %v", parsedCert.Subject.Organization)
	}

	// Check SANs
	foundLocalhost := false
	for _, name := range parsedCert.DNSNames {
		if name == "localhost" {
			foundLocalhost = true
			break
		}
	}
	if !foundLocalhost {
		t.Errorf("expected 'localhost' in DNSNames: %v", parsedCert.DNSNames)
	}

	foundLoopback := false
	for _, ip := range parsedCert.IPAddresses {
		if ip.String() == "127.0.0.1" {
			foundLoopback = true
			break
		}
	}
	if !foundLoopback {
		t.Errorf("expected 127.0.0.1 in IPAddresses: %v", parsedCert.IPAddresses)
	}

	// Second call should reuse the existing certificate without error
	certPath2, keyPath2, err := ensureSSLCertificates()
	if err != nil {
		t.Fatalf("second ensureSSLCertificates call failed: %v", err)
	}
	if certPath2 != certPath || keyPath2 != keyPath {
		t.Errorf("expected paths to match, got (%s, %s) vs (%s, %s)", certPath2, keyPath2, certPath, keyPath)
	}
}

func TestDualProtocolServer(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "agilepanel_dual_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	certPath := filepath.Join(tempDir, "gui.crt")
	keyPath := filepath.Join(tempDir, "gui.key")
	if err := generateSelfSignedCert(certPath, keyPath); err != nil {
		t.Fatalf("generateSelfSignedCert failed: %v", err)
	}

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("SECURE_GUI_OK"))
	})

	// Find an available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	testAddr := listener.Addr().String()
	_ = listener.Close()

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- startDualProtocolServer(testAddr, testHandler, certPath, keyPath)
	}()

	// Wait briefly for server to bind
	time.Sleep(200 * time.Millisecond)

	// 1. Test HTTPS request
	httpsClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 5 * time.Second,
	}

	httpsResp, err := httpsClient.Get("https://" + testAddr + "/test-page")
	if err != nil {
		t.Fatalf("HTTPS request failed: %v", err)
	}
	defer httpsResp.Body.Close()

	if httpsResp.StatusCode != http.StatusOK {
		t.Errorf("expected HTTPS status 200, got %d", httpsResp.StatusCode)
	}
	body, _ := io.ReadAll(httpsResp.Body)
	if string(body) != "SECURE_GUI_OK" {
		t.Errorf("expected body 'SECURE_GUI_OK', got %q", string(body))
	}

	// 2. Test HTTP request (expect 301 redirect to HTTPS)
	httpClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // don't follow, capture the 301
		},
		Timeout: 5 * time.Second,
	}

	httpResp, err := httpClient.Get("http://" + testAddr + "/test-page")
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("expected HTTP status 301, got %d", httpResp.StatusCode)
	}
	location := httpResp.Header.Get("Location")
	if !strings.HasPrefix(location, "https://") || !strings.Contains(location, "/test-page") {
		t.Errorf("expected Location starting with 'https://' and containing '/test-page', got %q", location)
	}
}
