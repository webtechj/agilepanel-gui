package main

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

//go:embed assets/*
var assetsFS embed.FS

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

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("1.1.0")
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

	// Static Assets
	http.HandleFunc("/", basicAuth(serveStatic("index.html", "text/html")))
	http.HandleFunc("/style.css", serveStatic("style.css", "text/css"))
	http.HandleFunc("/app.js", serveStatic("app.js", "application/javascript"))

	// Authentication API Endpoints (Bypass secondary session check but require basic auth)
	http.HandleFunc("/api/auth/status", basicAuth(handleAuthStatusAPI))
	http.HandleFunc("/api/auth/signup", basicAuth(handleAuthSignupAPI))
	http.HandleFunc("/api/auth/login", basicAuth(rateLimitMiddleware(handleAuthLoginAPI)))
	http.HandleFunc("/api/auth/logout", basicAuth(handleAuthLogoutAPI))
	http.HandleFunc("/api/auth/toggle", basicAuth(handleAuthToggleAPI))

	// Core API Endpoints (Require BOTH basic auth and secondary session check)
	http.HandleFunc("/api/status", basicAuth(sessionAuth(handleStatusAPI)))
	http.HandleFunc("/api/sites", basicAuth(sessionAuth(handleSitesAPI)))
	http.HandleFunc("/api/backup/download", basicAuth(sessionAuth(handleBackupDownloadAPI)))
	http.HandleFunc("/api/metrics/history", basicAuth(sessionAuth(handleMetricsHistoryAPI)))
	http.HandleFunc("/api/action", basicAuth(sessionAuth(rateLimitMiddleware(handleCommandExecuteAPI))))

	// Security Audit Endpoint
	http.HandleFunc("/api/security/audit", basicAuth(sessionAuth(handleSecurityAuditAPI)))

	// S3 and Staging API Routes
	http.HandleFunc("/api/settings/s3", basicAuth(sessionAuth(handleS3SettingsAPI)))
	http.HandleFunc("/api/sites/s3-backups", basicAuth(sessionAuth(handleSitesS3BackupsAPI)))
	http.HandleFunc("/api/sites/s3-restore", basicAuth(sessionAuth(handleSitesS3RestoreAPI)))
	http.HandleFunc("/api/sites/local-backups", basicAuth(sessionAuth(handleSitesLocalBackupsAPI)))
	http.HandleFunc("/api/sites/s3-delete", basicAuth(sessionAuth(handleSitesS3DeleteAPI)))
	http.HandleFunc("/api/settings/telegram", basicAuth(sessionAuth(handleTelegramSettingsAPI)))
	http.HandleFunc("/api/sites/upload-import", basicAuth(sessionAuth(handleSitesUploadImportAPI)))
	http.HandleFunc("/api/sites/toggle-staging-unlock", basicAuth(sessionAuth(handleSitesToggleStagingUnlockAPI)))
	http.HandleFunc("/api/sites/update-backup-interval", basicAuth(sessionAuth(handleSitesUpdateBackupIntervalAPI)))
	http.HandleFunc("/api/sites/update-backup-destination", basicAuth(sessionAuth(handleSitesUpdateBackupDestinationAPI)))
	http.HandleFunc("/api/sites/update-s3-backup-versions", basicAuth(sessionAuth(handleSitesUpdateS3BackupVersionsAPI)))
	http.HandleFunc("/api/sites/toggle-s3-enabled", basicAuth(sessionAuth(handleSitesToggleS3EnabledAPI)))
	http.HandleFunc("/api/sites/update-db-credentials", basicAuth(sessionAuth(handleSitesUpdateDbCredentialsAPI)))

	// File Manager Endpoints (Require BOTH basic auth and secondary session check)
	http.HandleFunc("/api/files/list", basicAuth(sessionAuth(handleFileListAPI)))
	http.HandleFunc("/api/files/read", basicAuth(sessionAuth(handleFileReadAPI)))
	http.HandleFunc("/api/files/write", basicAuth(sessionAuth(handleFileWriteAPI)))
	http.HandleFunc("/api/files/create", basicAuth(sessionAuth(handleFileCreateAPI)))
	http.HandleFunc("/api/files/delete", basicAuth(sessionAuth(handleFileDeleteAPI)))
	http.HandleFunc("/api/files/upload", basicAuth(sessionAuth(handleFileUploadAPI)))
	http.HandleFunc("/api/files/zip", basicAuth(sessionAuth(handleFileZipAPI)))
	http.HandleFunc("/api/files/unzip", basicAuth(sessionAuth(handleFileUnzipAPI)))
	http.HandleFunc("/api/files/rename", basicAuth(sessionAuth(handleFileRenameAPI)))
	http.HandleFunc("/api/files/copy", basicAuth(sessionAuth(handleFileCopyAPI)))

	log.Println("AgilePanel GUI Dashboard starting on http://localhost:8889...")
	if err := http.ListenAndServe(":8889", nil); err != nil {
		log.Fatalf("Server startup failed: %v", err)
	}
}
