package main

import (
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

//go:embed assets/*
var assetsFS embed.FS

type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (w gzipResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}

func (w gzipResponseWriter) Flush() {
	if f, ok := w.Writer.(*gzip.Writer); ok {
		_ = f.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func gzipMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next(w, r)
			return
		}
		if r.Header.Get("Upgrade") != "" || r.Header.Get("Accept") == "text/event-stream" || strings.HasPrefix(r.URL.Path, "/api/terminal/") {
			next(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		gzw := gzipResponseWriter{Writer: gz, ResponseWriter: w}
		next(gzw, r)
	}
}

// serveStatic reads files directly from the embedded resources FS with ETag caching support.
func serveStatic(filename string, contentType string) http.HandlerFunc {
	path := "assets/" + filename
	data, err := assetsFS.ReadFile(path)
	var etag string
	if err == nil {
		h := sha256.Sum256(data)
		etag = `W/"` + hex.EncodeToString(h[:8]) + `"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(fmt.Sprintf("%s not found", filename)))
			return
		}

		if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("ETag", etag)
		if filename == "index.html" {
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600, must-revalidate")
		}
		w.Write(data)
	}
}

// securityHeadersMiddleware applies essential security headers to all responses
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(), camera=(), microphone=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none';")
		next.ServeHTTP(w, r)
	})
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("1.1.8")
		return
	}
	getCPU()

	// Start self-healing watchdog
	startWatchdog()

	// Start Telegram chatbot listener
	startTelegramBotListener()

	// Session cleanup goroutine (runs every hour)
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		for range ticker.C {
			sessionMutex.Lock()
			now := time.Now()
			for token, expiry := range activeSessions {
				if now.After(expiry) {
					delete(activeSessions, token)
					delete(activeSessionMetas, token)
				}
			}
			sessionMutex.Unlock()
		}
	}()

	// Periodic metrics logging ticker (every 2 hours)
	go func() {
		recordCurrentMetrics()
		ticker := time.NewTicker(2 * time.Hour)
		for range ticker.C {
			recordCurrentMetrics()
		}
	}()

	// Start background automated backup scheduler
	runAutomatedBackupScheduler()

	// Static Assets with Gzip compression
	http.HandleFunc("/", gzipMiddleware(basicAuth(serveStatic("index.html", "text/html"))))
	http.HandleFunc("/style.css", gzipMiddleware(serveStatic("style.css", "text/css")))
	http.HandleFunc("/app.js", gzipMiddleware(serveStatic("app.js", "application/javascript")))

	// Authentication API Endpoints (Bypass secondary session check but require basic auth)
	http.HandleFunc("/api/auth/status", gzipMiddleware(basicAuth(handleAuthStatusAPI)))
	http.HandleFunc("/api/auth/signup", basicAuth(csrfAndOriginMiddleware(handleAuthSignupAPI)))
	http.HandleFunc("/api/auth/login", rateLimitMiddleware(basicAuth(handleAuthLoginAPI)))
	http.HandleFunc("/api/auth/logout", basicAuth(handleAuthLogoutAPI))
	http.HandleFunc("/api/auth/toggle", basicAuth(csrfAndOriginMiddleware(handleAuthToggleAPI)))

	// Core API Endpoints (Require BOTH basic auth and secondary session check)
	http.HandleFunc("/api/status", gzipMiddleware(basicAuth(sessionAuth(handleStatusAPI))))
	http.HandleFunc("/api/sites", gzipMiddleware(basicAuth(sessionAuth(handleSitesAPI))))
	http.HandleFunc("/api/backup/download", basicAuth(sessionAuth(handleBackupDownloadAPI)))
	http.HandleFunc("/api/metrics/history", gzipMiddleware(basicAuth(sessionAuth(handleMetricsHistoryAPI))))
	http.HandleFunc("/api/action", basicAuth(sessionAuth(rateLimitMiddleware(csrfAndOriginMiddleware(handleCommandExecuteAPI)))))
	http.HandleFunc("/api/services/logs", gzipMiddleware(basicAuth(sessionAuth(handleServiceLogsAPI))))

	// Security Hub Endpoints
	http.HandleFunc("/api/security/audit", gzipMiddleware(basicAuth(sessionAuth(handleSecurityAuditAPI))))
	http.HandleFunc("/api/security/logs", gzipMiddleware(basicAuth(sessionAuth(handleSecurityLogsAPI))))
	http.HandleFunc("/api/security/firewall", gzipMiddleware(basicAuth(sessionAuth(handleSecurityFirewallAPI))))
	http.HandleFunc("/api/security/firewall/rule", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleSecurityFirewallRuleAPI))))
	http.HandleFunc("/api/security/sessions", gzipMiddleware(basicAuth(sessionAuth(handleSecuritySessionsAPI))))
	http.HandleFunc("/api/security/sessions/revoke", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleSecurityRevokeSessionAPI))))
	http.HandleFunc("/api/security/ssl", gzipMiddleware(basicAuth(sessionAuth(handleSecuritySSLAPI))))
	http.HandleFunc("/api/security/remediate", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleSecurityRemediateAPI))))

	// S3 and Staging API Routes
	http.HandleFunc("/api/settings/s3", gzipMiddleware(basicAuth(sessionAuth(csrfAndOriginMiddleware(handleS3SettingsAPI)))))
	http.HandleFunc("/api/settings/admin-auth", gzipMiddleware(basicAuth(sessionAuth(csrfAndOriginMiddleware(handleAdminAuthSettingsAPI)))))
	http.HandleFunc("/api/tools/network-test", gzipMiddleware(basicAuth(sessionAuth(handleNetworkTestAPI))))
	http.HandleFunc("/api/sites/s3-backups", gzipMiddleware(basicAuth(sessionAuth(handleSitesS3BackupsAPI))))
	http.HandleFunc("/api/sites/s3-restore", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleSitesS3RestoreAPI))))
	http.HandleFunc("/api/sites/local-backups", gzipMiddleware(basicAuth(sessionAuth(handleSitesLocalBackupsAPI))))
	http.HandleFunc("/api/sites/s3-delete", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleSitesS3DeleteAPI))))
	http.HandleFunc("/api/settings/telegram", gzipMiddleware(basicAuth(sessionAuth(csrfAndOriginMiddleware(handleTelegramSettingsAPI)))))
	http.HandleFunc("/api/sites/upload-import", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleSitesUploadImportAPI))))
	http.HandleFunc("/api/sites/toggle-staging-unlock", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleSitesToggleStagingUnlockAPI))))
	http.HandleFunc("/api/sites/update-backup-interval", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleSitesUpdateBackupIntervalAPI))))
	http.HandleFunc("/api/sites/update-backup-destination", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleSitesUpdateBackupDestinationAPI))))
	http.HandleFunc("/api/sites/update-s3-backup-versions", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleSitesUpdateS3BackupVersionsAPI))))
	http.HandleFunc("/api/sites/toggle-s3-enabled", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleSitesToggleS3EnabledAPI))))
	http.HandleFunc("/api/sites/update-db-credentials", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleSitesUpdateDbCredentialsAPI))))
	http.HandleFunc("/api/sites/update-php", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleSitesUpdatePHPAPI))))

	// File Manager Endpoints (Require BOTH basic auth and secondary session check)
	http.HandleFunc("/api/files/list", gzipMiddleware(basicAuth(sessionAuth(handleFileListAPI))))
	http.HandleFunc("/api/files/read", basicAuth(sessionAuth(handleFileReadAPI)))
	http.HandleFunc("/api/files/write", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleFileWriteAPI))))
	http.HandleFunc("/api/files/create", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleFileCreateAPI))))
	http.HandleFunc("/api/files/delete", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleFileDeleteAPI))))
	http.HandleFunc("/api/files/batch-delete", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleFileBatchDeleteAPI))))
	http.HandleFunc("/api/files/chmod", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleFileChmodAPI))))
	http.HandleFunc("/api/files/size", gzipMiddleware(basicAuth(sessionAuth(handleFolderSizeAPI))))
	http.HandleFunc("/api/files/upload", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleFileUploadAPI))))
	http.HandleFunc("/api/files/zip", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleFileZipAPI))))
	http.HandleFunc("/api/files/unzip", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleFileUnzipAPI))))
	http.HandleFunc("/api/files/rename", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleFileRenameAPI))))
	http.HandleFunc("/api/files/copy", basicAuth(sessionAuth(csrfAndOriginMiddleware(handleFileCopyAPI))))

	port := os.Getenv("AGILEPANEL_PORT")
	if port == "" {
		port = "8889"
	}
	addr := ":" + port

	noSSL := os.Getenv("AGILEPANEL_NO_SSL") == "1" || os.Getenv("AGILEPANEL_NO_SSL") == "true"
	for _, arg := range os.Args[1:] {
		if arg == "--no-ssl" || arg == "--http" {
			noSSL = true
			break
		}
	}

	rootHandler := securityHeadersMiddleware(http.DefaultServeMux)

	if noSSL {
		log.Printf("AgilePanel GUI Dashboard starting on http://localhost:%s (Insecure Plaintext Mode)...", port)
		server := &http.Server{
			Addr:              addr,
			Handler:           rootHandler,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       120 * time.Second,
			WriteTimeout:      120 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		if err := server.ListenAndServe(); err != nil {
			log.Fatalf("Server startup failed: %v", err)
		}
		return
	}

	// Ensure SSL certificates exist or generate self-signed cert on first install/boot
	certFile, keyFile, err := ensureSSLCertificates()
	if err != nil {
		log.Fatalf("[SSL Error] Failed to initialize SSL certificates: %v", err)
	}

	log.Printf("👑 AgilePanel GUI Dashboard running securely on https://localhost:%s (HTTP auto-redirects on :%s)...", port, port)
	if err := startDualProtocolServer(addr, rootHandler, certFile, keyFile); err != nil {
		log.Fatalf("Server startup failed: %v", err)
	}
}
