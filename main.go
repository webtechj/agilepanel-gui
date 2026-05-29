package main

import (
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// GlobalConfig matches the structure in state.json
type GlobalConfig struct {
	UUID                 string   `json:"uuid,omitempty"`
	DefaultPHPVersion    string   `json:"default_php_version"`
	SupportedPHPVersions []string `json:"supported_php_versions"`
	CaddyPath            string   `json:"caddy_path"`
	CaddyConfigPath      string   `json:"caddy_config_path"`
	RedisSocketPath      string   `json:"redis_socket_path"`
	AdminUser            string   `json:"admin_user"`
	AdminPasswordHash    string   `json:"admin_password_hash"`
	AdminName            string   `json:"admin_name,omitempty"`
	AdminEmail           string   `json:"admin_email,omitempty"`
}

type SiteConfig struct {
	Domain       string `json:"domain"`
	PHPVersion   string `json:"php_version"`
	PublicDir    string `json:"public_dir"`
	DatabaseName string `json:"database_name"`
	DatabaseUser string `json:"db_user"`
	DatabasePass string `json:"db_pass,omitempty"`
	SystemUser   string `json:"system_user"`
	IsLocked     bool   `json:"is_locked"`
	Type         string `json:"type,omitempty"`
}

type State struct {
	Global GlobalConfig `json:"global"`
	Sites  []SiteConfig `json:"sites"`
}

func getStatePath() string {
	if val := os.Getenv("AGILEPANEL_STATE_PATH"); val != "" {
		return val
	}
	if runtime.GOOS == "windows" {
		return "./state.json"
	}
	return "/etc/agilepanel/state.json"
}

func readState() (*State, error) {
	path := getStatePath()
	file, err := os.Open(path)
	if err != nil {
		// Fallback for mock or local dev
		return &State{
			Global: GlobalConfig{
				DefaultPHPVersion:    "8.3",
				SupportedPHPVersions: []string{"8.1", "8.2", "8.3"},
				AdminUser:            "admin",
				AdminPasswordHash:    "", // Trigger fallback
			},
			Sites: []SiteConfig{},
		}, nil
	}
	defer file.Close()

	var s State
	if err := json.NewDecoder(file).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

// BasicAuth middleware using credentials from state.json
func basicAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, err := readState()
		if err != nil {
			http.Error(w, "State read error", http.StatusInternalServerError)
			return
		}

		user, pass, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="AgilePanel Dashboard"`)
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("Unauthorized"))
			return
		}

		expectedUser := state.Global.AdminUser
		expectedHash := state.Global.AdminPasswordHash

		// Check for default config fallback
		if expectedUser == "" || expectedHash == "" {
			if user == "admin" && pass == "admin" {
				next(w, r)
				return
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="AgilePanel Dashboard"`)
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("Credentials not set in panel. Use 'admin/admin' fallback or run 'ap server auth [user] [pass]'"))
			return
		}

		if user != expectedUser {
			w.Header().Set("WWW-Authenticate", `Basic realm="AgilePanel Dashboard"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		err = bcrypt.CompareHashAndPassword([]byte(expectedHash), []byte(pass))
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="AgilePanel Dashboard"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}


func handleStatusAPI(w http.ResponseWriter, r *http.Request) {
	state, _ := readState()

	services := map[string]bool{
		"caddy":    getServiceStatus("caddy"),
		"mariadb":  getServiceStatus("mariadb"),
		"redis":    getServiceStatus("redis-server"),
		"php-fpm":  getServiceStatus("php8.3-fpm") || getServiceStatus("php8.2-fpm") || getServiceStatus("php8.1-fpm"),
	}

	wpCount := 0
	htmlCount := 0
	laravelCount := 0
	phpCount := 0
	for _, s := range state.Sites {
		switch s.Type {
		case "html":
			htmlCount++
		case "laravel":
			laravelCount++
		case "php":
			phpCount++
		case "wp", "woocommerce", "":
			wpCount++
		default:
			wpCount++
		}
	}

	status := map[string]interface{}{
		"cpu":          getCPU(),
		"ram":          getRAM(),
		"disk":         getDisk(),
		"services":     services,
		"siteCount":    len(state.Sites),
		"wpCount":      wpCount,
		"htmlCount":     htmlCount,
		"laravelCount":  laravelCount,
		"phpCount":      phpCount,
		"uptime":        getUptime(),
		"loadAvg":       getLoadAverages(),
		"tcpConns":      getTCPConnections(),
		"topProcesses":  getTopProcesses(),
		"global": map[string]interface{}{
			"admin_user":      state.Global.AdminUser,
			"default_php":     state.Global.DefaultPHPVersion,
			"supported_php":   state.Global.SupportedPHPVersions,
			"has_credentials": state.Global.AdminUser != "" && state.Global.AdminPasswordHash != "",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

func handleSitesAPI(w http.ResponseWriter, r *http.Request) {
	state, err := readState()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state.Sites)
}

func handleCommandExecuteAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Action string   `json:"action"`
		Args   []string `json:"args"`
	}

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Restrict commands to safe ap execution schema
	allowedActions := map[string]bool{
		"site-create":     true,
		"site-delete":     true,
		"site-lock":       true,
		"site-unlock":     true,
		"site-cache":      true,
		"site-reinstall":  true,
		"site-ssl":        true,
		"site-perms":      true,
		"site-backup":     true,
		"site-backup-db":  true,
		"site-restore":    true,
		"server-restart":  true,
		"server-tune":     true,
		"server-secure":   true,
		"tool-install":    true,
		"tool-fix":        true,
		"repair":          true,
		"sync":            true,
		"update":          true,
		"upgrade":         true,
	}

	if !allowedActions[payload.Action] {
		http.Error(w, "Action not allowed", http.StatusForbidden)
		return
	}

	if payload.Action == "site-restore" {
		handleSiteRestoreAPI(w, r, payload.Args[0])
		return
	}

	// Prepare exact commands for native `ap` bin
	var cmdArgs []string

	// Determine binary path (runs local dev version or system version)
	apBin := "ap"
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/usr/local/bin/ap"); err == nil {
			apBin = "/usr/local/bin/ap"
		}
	} else if runtime.GOOS == "windows" {
		apBin = "../VPSops/ap.exe" // Local mockup executable
	}

	switch payload.Action {
	case "site-create":
		cmdArgs = append([]string{"site", "create"}, payload.Args...)
	case "site-delete":
		cmdArgs = []string{"site", "delete", payload.Args[0], "-y"}
	case "site-lock":
		cmdArgs = []string{"site", "lock", payload.Args[0], "-y"}
	case "site-unlock":
		cmdArgs = []string{"site", "unlock", payload.Args[0]}
	case "site-cache":
		cmdArgs = []string{"site", "cache-clean", payload.Args[0]}
	case "site-reinstall":
		cmdArgs = []string{"site", "reinstall", payload.Args[0]}
	case "site-ssl":
		cmdArgs = []string{"site", "ssl-renew", payload.Args[0]}
	case "site-perms":
		cmdArgs = []string{"site", "fix-permissions", payload.Args[0]}
	case "site-backup":
		cmdArgs = []string{"site", "backup", payload.Args[0]}
	case "site-backup-db":
		cmdArgs = []string{"site", "backup-db", payload.Args[0]}
	case "server-restart":
		cmdArgs = []string{"server", "restart", payload.Args[0]}
	case "server-tune":
		cmdArgs = []string{"server", "tune"}
	case "server-secure":
		cmdArgs = []string{"server", "secure"}
	case "tool-install":
		cmdArgs = []string{"tool", "install", payload.Args[0]}
	case "tool-fix":
		cmdArgs = []string{"tool", "fix-phpmyadmin"}
	case "repair":
		cmdArgs = []string{"repair"}
	case "sync":
		cmdArgs = []string{"sync"}
	case "update":
		cmdArgs = []string{"update"}
	case "upgrade":
		cmdArgs = []string{"upgrade"}
	}

	// Stream execution logs back as Server-Sent Events (SSE) or chunked transfer
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	// Print visual logs
	fmt.Fprintf(w, "data: Running Command: %s %s...\n\n", apBin, strings.Join(cmdArgs, " "))
	flusher.Flush()

	cmd := exec.Command(apBin, cmdArgs...)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(w, "data: Error pipe stdout: %v\n\n", err)
		flusher.Flush()
		return
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		fmt.Fprintf(w, "data: Error pipe stderr: %v\n\n", err)
		flusher.Flush()
		return
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(w, "data: Startup Execution Error: %v\n\n", err)
		flusher.Flush()
		return
	}

	// Read outputs concurrently
	var wg sync.WaitGroup
	wg.Add(2)

	logScanner := func(r io.Reader, prefix string) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			// Clean ANSI escape color codes from CLI output
			cleaned := stripANSI(line)
			fmt.Fprintf(w, "data: %s%s\n\n", prefix, cleaned)
			flusher.Flush()
		}
	}

	go logScanner(stdoutPipe, "")
	go logScanner(stderrPipe, "ERR: ")

	wg.Wait()

	if err := cmd.Wait(); err != nil {
		fmt.Fprintf(w, "data: Process finished with Error: %v\n\n", err)
	} else {
		fmt.Fprintf(w, "data: Process finished successfully!\n\n")
	}
	flusher.Flush()
}

func stripANSI(str string) string {
	var sb strings.Builder
	inEscape := false
	for i := 0; i < len(str); i++ {
		if str[i] == '\033' || (i+1 < len(str) && str[i] == 0x1B) {
			inEscape = true
			continue
		}
		if inEscape {
			if str[i] == 'm' {
				inEscape = false
			}
			continue
		}
		sb.WriteByte(str[i])
	}
	return sb.String()
}

//go:embed assets/*
var assetsFS embed.FS

// serveStatic reads files directly from the embedded resources FS.
func serveStatic(filename string, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := "assets/" + filename
		data, err := assetsFS.ReadFile(path)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(fmt.Sprintf("%s not found", filename)))
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Write(data)
	}
}

func validateFilePath(domain string, path string) (string, error) {
	baseDir := "/var/www"
	if domain != "" {
		baseDir = filepath.Clean(filepath.Join("/var/www", domain))
	} else if runtime.GOOS == "windows" {
		baseDir = filepath.Clean("./var/www")
	}

	if runtime.GOOS == "windows" {
		if domain != "" {
			baseDir = filepath.Clean(filepath.Join("./var/www", domain))
		}
	}

	var fullPath string
	if filepath.IsAbs(path) {
		fullPath = filepath.Clean(path)
	} else {
		fullPath = filepath.Clean(filepath.Join(baseDir, path))
	}

	if !strings.HasPrefix(fullPath, baseDir) {
		return "", fmt.Errorf("access denied: directory traversal detected")
	}

	return fullPath, nil
}

func handleFileListAPI(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	relPath := r.URL.Query().Get("path")
	if domain == "" {
		http.Error(w, "Domain parameter required", http.StatusBadRequest)
		return
	}

	fullPath, err := validateFilePath(domain, relPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	// Local development mock setup if directories don't exist
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		_ = os.MkdirAll(fullPath, 0755)
		if strings.HasSuffix(fullPath, "htdocs") {
			_ = os.WriteFile(filepath.Join(fullPath, "index.html"), []byte("<h1>Mock Webroot</h1>"), 0644)
		}
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read directory: %v", err), http.StatusInternalServerError)
		return
	}

	type FileItem struct {
		Name    string `json:"name"`
		Size    int64  `json:"size"`
		IsDir   bool   `json:"isDir"`
		ModTime string `json:"modTime"`
		Mode    string `json:"mode"`
	}

	var items []FileItem
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		items = append(items, FileItem{
			Name:    entry.Name(),
			Size:    info.Size(),
			IsDir:   entry.IsDir(),
			ModTime: info.ModTime().Format("2006-01-02 15:04:05"),
			Mode:    info.Mode().String(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

func handleFileReadAPI(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	relPath := r.URL.Query().Get("path")
	if domain == "" || relPath == "" {
		http.Error(w, "Domain and path parameters required", http.StatusBadRequest)
		return
	}

	fullPath, err := validateFilePath(domain, relPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read file: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write(data)
}

func handleFileWriteAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Domain  string `json:"domain"`
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if payload.Domain == "" || payload.Path == "" {
		http.Error(w, "Domain and path required", http.StatusBadRequest)
		return
	}

	fullPath, err := validateFilePath(payload.Domain, payload.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	err = os.WriteFile(fullPath, []byte(payload.Content), 0644)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to write file: %v", err), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("File saved successfully"))
}

func handleFileCreateAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Domain string `json:"domain"`
		Path   string `json:"path"`
		Name   string `json:"name"`
		IsDir  bool   `json:"isDir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if payload.Domain == "" || payload.Name == "" {
		http.Error(w, "Domain and name required", http.StatusBadRequest)
		return
	}

	targetPath := filepath.Join(payload.Path, payload.Name)
	fullPath, err := validateFilePath(payload.Domain, targetPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	var createErr error
	if payload.IsDir {
		createErr = os.MkdirAll(fullPath, 0755)
	} else {
		parent := filepath.Dir(fullPath)
		_ = os.MkdirAll(parent, 0755)
		var file *os.File
		file, createErr = os.Create(fullPath)
		if createErr == nil {
			file.Close()
		}
	}

	if createErr != nil {
		http.Error(w, fmt.Sprintf("Failed to create: %v", createErr), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Created successfully"))
}

func handleFileDeleteAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Domain string `json:"domain"`
		Path   string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if payload.Domain == "" || payload.Path == "" {
		http.Error(w, "Domain and path required", http.StatusBadRequest)
		return
	}

	fullPath, err := validateFilePath(payload.Domain, payload.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	// Secure boundary
	if fullPath == "/var/www/"+payload.Domain || fullPath == "/var/www/"+payload.Domain+"/htdocs" || fullPath == "./var/www/"+payload.Domain || fullPath == "./var/www/"+payload.Domain+"/htdocs" {
		http.Error(w, "Access denied: cannot delete webroot folders", http.StatusForbidden)
		return
	}

	err = os.RemoveAll(fullPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to delete: %v", err), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Deleted successfully"))
}

func handleFileUploadAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseMultipartForm(32 << 20)
	if err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	domain := r.FormValue("domain")
	relPath := r.FormValue("path")
	if domain == "" {
		http.Error(w, "Domain parameter required", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File parameter required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	targetPath := filepath.Join(relPath, header.Filename)
	fullPath, err := validateFilePath(domain, targetPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	out, err := os.Create(fullPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create destination file: %v", err), http.StatusInternalServerError)
		return
	}
	defer out.Close()

	_, err = io.Copy(out, file)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to save file: %v", err), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("File uploaded successfully"))
}

func handleSiteRestoreAPI(w http.ResponseWriter, r *http.Request, domain string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	fmt.Fprintf(w, "data: Starting Restore for website: %s...\n\n", domain)
	flusher.Flush()

	var parentDir string
	if runtime.GOOS == "windows" {
		parentDir = filepath.Clean(filepath.Join("./var/www", domain))
	} else {
		parentDir = "/var/www/" + domain
	}

	backupDir := filepath.Join(parentDir, "backup")
	filesZipPath := filepath.Join(backupDir, domain+"-files.zip")
	dbZipPath := filepath.Join(backupDir, domain+"-db.zip")

	if _, err := os.Stat(filesZipPath); err != nil {
		fmt.Fprintf(w, "data: ERR: Files backup zip not found at: %s. Restore failed.\n\n", filesZipPath)
		flusher.Flush()
		return
	}

	fmt.Fprintf(w, "data: Restoring web public files from ZIP...\n\n")
	flusher.Flush()

	if runtime.GOOS == "windows" {
		fmt.Fprintf(w, "data: Mock: Unzipped %s to %s successfully.\n\n", filesZipPath, parentDir)
		flusher.Flush()
	} else {
		restoreFilesCmd := exec.Command("unzip", "-o", "-q", filesZipPath, "-d", parentDir)
		restoreFilesCmd.Dir = parentDir
		var stderr bytes.Buffer
		restoreFilesCmd.Stderr = &stderr
		if err := restoreFilesCmd.Run(); err != nil {
			fmt.Fprintf(w, "data: ERR: Failed to extract files ZIP: %v (stderr: %s)\n\n", err, stderr.String())
			flusher.Flush()
			return
		}
		fmt.Fprintf(w, "data: Web files restored successfully.\n\n")
		flusher.Flush()
	}

	state, _ := readState()
	var targetSite *SiteConfig
	for _, s := range state.Sites {
		if strings.EqualFold(s.Domain, domain) {
			targetSite = &s
			break
		}
	}

	hasDB := targetSite != nil && targetSite.DatabaseName != "" && targetSite.Type != "html"
	if hasDB {
		if _, err := os.Stat(dbZipPath); err == nil {
			fmt.Fprintf(w, "data: Restoring database schema from ZIP...\n\n")
			flusher.Flush()

			if runtime.GOOS == "windows" {
				fmt.Fprintf(w, "data: Mock: Restored database for %s successfully.\n\n", domain)
				flusher.Flush()
			} else {
				tmpExtractDir := "/tmp/" + domain + "_db_restore"
				_ = os.RemoveAll(tmpExtractDir)
				_ = os.MkdirAll(tmpExtractDir, 0755)
				defer os.RemoveAll(tmpExtractDir)

				unzipDBCmd := exec.Command("unzip", "-o", "-q", dbZipPath, "-d", tmpExtractDir)
				if err := unzipDBCmd.Run(); err != nil {
					fmt.Fprintf(w, "data: ERR: Failed to extract database ZIP: %v\n\n", err)
					flusher.Flush()
					return
				}

				entries, err := os.ReadDir(tmpExtractDir)
				if err != nil || len(entries) == 0 {
					fmt.Fprintf(w, "data: ERR: No SQL database file found inside ZIP.\n\n")
					flusher.Flush()
					return
				}
				sqlFile := filepath.Join(tmpExtractDir, entries[0].Name())

				fmt.Fprintf(w, "data: Importing SQL schema into MariaDB database %s...\n\n", targetSite.DatabaseName)
				flusher.Flush()

				importCmd := exec.Command("mysql", "-u"+targetSite.DatabaseUser, "-p"+targetSite.DatabasePass, targetSite.DatabaseName)
				sqlReader, rErr := os.Open(sqlFile)
				if rErr != nil {
					fmt.Fprintf(w, "data: ERR: Failed to read SQL file: %v\n\n", rErr)
					flusher.Flush()
					return
				}
				defer sqlReader.Close()
				importCmd.Stdin = sqlReader

				var importStderr bytes.Buffer
				importCmd.Stderr = &importStderr
				if err := importCmd.Run(); err != nil {
					fmt.Fprintf(w, "data: ERR: MariaDB schema import failed: %v (stderr: %s)\n\n", err, importStderr.String())
					flusher.Flush()
					return
				}

				fmt.Fprintf(w, "data: Database schema imported successfully.\n\n")
				flusher.Flush()
			}
		} else {
			fmt.Fprintf(w, "data: Database ZIP not found at: %s. Skipping database restore.\n\n", dbZipPath)
			flusher.Flush()
		}
	} else {
		fmt.Fprintf(w, "data: Static HTML site. Skipping database restore.\n\n")
		flusher.Flush()
	}

	if runtime.GOOS == "linux" && targetSite != nil {
		fmt.Fprintf(w, "data: Restoring correct file ownership/permissions...\n\n")
		flusher.Flush()
		_ = exec.Command("chown", "-R", targetSite.SystemUser+":www-data", "/var/www/"+domain+"/htdocs").Run()
		_ = exec.Command("chmod", "-R", "0755", "/var/www/"+domain+"/htdocs").Run()
	}

	fmt.Fprintf(w, "data: Process finished successfully!\n\n")
	flusher.Flush()
}

func main() {
	getCPU()

	http.HandleFunc("/", basicAuth(serveStatic("index.html", "text/html")))
	http.HandleFunc("/style.css", serveStatic("style.css", "text/css"))
	http.HandleFunc("/app.js", serveStatic("app.js", "application/javascript"))

	http.HandleFunc("/api/status", basicAuth(handleStatusAPI))
	http.HandleFunc("/api/sites", basicAuth(handleSitesAPI))
	http.HandleFunc("/api/action", basicAuth(handleCommandExecuteAPI))

	// File Manager Endpoints
	http.HandleFunc("/api/files/list", basicAuth(handleFileListAPI))
	http.HandleFunc("/api/files/read", basicAuth(handleFileReadAPI))
	http.HandleFunc("/api/files/write", basicAuth(handleFileWriteAPI))
	http.HandleFunc("/api/files/create", basicAuth(handleFileCreateAPI))
	http.HandleFunc("/api/files/delete", basicAuth(handleFileDeleteAPI))
	http.HandleFunc("/api/files/upload", basicAuth(handleFileUploadAPI))

	log.Println("AgilePanel GUI Dashboard starting on http://localhost:8889...")
	if err := http.ListenAndServe(":8889", nil); err != nil {
		log.Fatalf("Server startup failed: %v", err)
	}
}

