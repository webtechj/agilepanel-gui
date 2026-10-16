package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// getSSLCertPaths returns the absolute paths for the GUI certificate and private key.
func getSSLCertPaths() (certPath, keyPath string) {
	dir := os.Getenv("AGILEPANEL_SSL_DIR")
	if dir == "" {
		if runtime.GOOS == "windows" {
			dir = "./ssl"
		} else {
			dir = "/etc/agilepanel/ssl"
		}
	}
	return filepath.Join(dir, "gui.crt"), filepath.Join(dir, "gui.key")
}

// ensureSSLCertificates checks if SSL certificate and private key exist.
// If they do not exist or are invalid, it generates a fresh self-signed ECDSA certificate.
func ensureSSLCertificates() (string, string, error) {
	certPath, keyPath := getSSLCertPaths()

	// Check if both files exist and are valid
	if _, errCert := os.Stat(certPath); errCert == nil {
		if _, errKey := os.Stat(keyPath); errKey == nil {
			if _, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
				log.Printf("[SSL] Using existing SSL certificate at %s", certPath)
				return certPath, keyPath, nil
			}
			log.Printf("[SSL] Existing certificate or key at %s is corrupted or invalid. Regenerating...", certPath)
		}
	}

	log.Printf("[SSL] Generating fresh self-signed SSL certificate for AgilePanel GUI...")
	if err := generateSelfSignedCert(certPath, keyPath); err != nil {
		return "", "", fmt.Errorf("failed to generate self-signed certificate: %w", err)
	}

	log.Printf("[SSL] Self-signed SSL certificate successfully installed at %s", certPath)
	return certPath, keyPath, nil
}

// generateSelfSignedCert creates an ECDSA P-256 self-signed certificate valid for 10 years
// with SANs populated with localhost, local hostname, 127.0.0.1, ::1, and local IP addresses.
func generateSelfSignedCert(certPath, keyPath string) error {
	dir := filepath.Dir(certPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create SSL directory %s: %w", dir, err)
	}

	// Generate ECDSA P-256 private key
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %w", err)
	}

	// Serial number
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNum, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return fmt.Errorf("failed to generate serial number: %w", err)
	}

	// Gather IP addresses for SANs
	ipAddresses := []net.IP{
		net.ParseIP("127.0.0.1"),
		net.ParseIP("::1"),
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				if ipNet.IP.To4() != nil || ipNet.IP.To16() != nil {
					ipAddresses = append(ipAddresses, ipNet.IP)
				}
			}
		}
	}

	// Gather DNS names for SANs
	dnsNames := []string{"localhost"}
	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		dnsNames = append(dnsNames, hostname)
	}

	template := x509.Certificate{
		SerialNumber: serialNum,
		Subject: pkix.Name{
			CommonName:         "AgilePanel GUI",
			Organization:       []string{"AgilePanel"},
			OrganizationalUnit: []string{"Server Management Dashboard"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour), // mitigate clock drift
		NotAfter:              time.Now().AddDate(10, 0, 0),   // 10 years validity
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		IPAddresses:           ipAddresses,
		DNSNames:              dnsNames,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privKey.PublicKey, privKey)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(privKey)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	// Save key with restricted permissions (0600)
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write key file: %w", err)
	}

	// Save certificate (0644)
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return fmt.Errorf("failed to write cert file: %w", err)
	}

	return nil
}

// peekConn wraps a net.Conn with a prefix buffer to replay the first sniffed byte.
type peekConn struct {
	net.Conn
	r io.Reader
}

func (c *peekConn) Read(b []byte) (int, error) {
	return c.r.Read(b)
}

// chanListener implements net.Listener over a channel of connections.
type chanListener struct {
	addr    net.Addr
	connCh  chan net.Conn
	closeCh chan struct{}
	once    sync.Once
}

func newChanListener(addr net.Addr) *chanListener {
	return &chanListener{
		addr:    addr,
		connCh:  make(chan net.Conn, 128),
		closeCh: make(chan struct{}),
	}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case conn, ok := <-l.connCh:
		if !ok {
			return nil, errors.New("listener closed")
		}
		return conn, nil
	case <-l.closeCh:
		return nil, errors.New("listener closed")
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() {
		close(l.closeCh)
	})
	return nil
}

func (l *chanListener) Addr() net.Addr {
	return l.addr
}

// startDualProtocolServer starts a dual HTTP + HTTPS server on a single port.
// Connections starting with TLS handshake byte (0x16) are processed over HTTPS.
// Plain HTTP connections are instantly redirected to HTTPS (301 Moved Permanently).
func startDualProtocolServer(addr string, mainHandler http.Handler, certFile, keyFile string) error {
	baseListener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}
	defer baseListener.Close()

	tlsChan := newChanListener(baseListener.Addr())
	httpChan := newChanListener(baseListener.Addr())
	stopCh := make(chan struct{})
	var closeOnce sync.Once

	closeAll := func() {
		closeOnce.Do(func() {
			close(stopCh)
			_ = tlsChan.Close()
			_ = httpChan.Close()
		})
	}

	// HTTP redirect server
	httpRedirectServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host
			if host == "" {
				host = r.URL.Host
			}
			if host == "" {
				host = "localhost:8889"
			}
			target := "https://" + host + r.RequestURI
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		}),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		_ = httpRedirectServer.Serve(httpChan)
	}()

	// Connection multiplexer loop
	go func() {
		for {
			conn, err := baseListener.Accept()
			if err != nil {
				select {
				case <-stopCh:
					return
				default:
					return
				}
			}

			go func(c net.Conn) {
				// 4-second sniff deadline to prevent slowloris or idle probes from hanging
				_ = c.SetReadDeadline(time.Now().Add(4 * time.Second))
				var buf [1]byte
				n, err := c.Read(buf[:])
				_ = c.SetReadDeadline(time.Time{})

				if err != nil || n == 0 {
					_ = c.Close()
					return
				}

				wrapped := &peekConn{
					Conn: c,
					r:    io.MultiReader(bytes.NewReader(buf[:]), c),
				}

				// 0x16 = TLS Handshake record type
				if buf[0] == 0x16 {
					select {
					case tlsChan.connCh <- wrapped:
					case <-stopCh:
						_ = wrapped.Close()
					}
				} else {
					select {
					case httpChan.connCh <- wrapped:
					case <-stopCh:
						_ = wrapped.Close()
					}
				}
			}(conn)
		}
	}()

	// Load TLS Certificate KeyPair
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		closeAll()
		return fmt.Errorf("failed to load TLS key pair: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},
	}

	tlsListener := tls.NewListener(tlsChan, tlsConfig)
	httpsServer := &http.Server{
		Handler:      mainHandler,
		TLSConfig:    tlsConfig,
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	defer closeAll()
	return httpsServer.Serve(tlsListener)
}
