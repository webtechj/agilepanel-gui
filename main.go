package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

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

// Historical metrics data structures
type HistoryPoint struct {
	Label string  `json:"label"`
	CPU   float64 `json:"cpu"`
	RAM   float64 `json:"ram"`
	Disk  float64 `json:"disk"`
}

func getHistoryPath() string {
	if runtime.GOOS == "windows" {
		return "./metrics_history.json"
	}
	return "/etc/agilepanel/metrics_history.json"
}

func loadOrCreateHistory() []HistoryPoint {
	path := getHistoryPath()
	var history []HistoryPoint

	file, err := os.Open(path)
	if err == nil {
		defer file.Close()
		if json.NewDecoder(file).Decode(&history) == nil && len(history) > 0 {
			return history
		}
	}

	// Generate 30 days of realistic mock history if empty or missing
	currRam := getRAM()
	ramTotal := currRam.Total
	if ramTotal == 0 {
		ramTotal = 8.0
	}
	currDisk := getDisk()
	diskTotal := currDisk.Total
	if diskTotal == 0 {
		diskTotal = 80.0
	}

	now := time.Now()
	for i := 29; i >= 0; i-- {
		t := now.AddDate(0, 0, -i)
		// Generate semi-random stable curves
		daySeed := float64(t.Day())
		cpuVal := 12.0 + 6.0*math.Sin(daySeed/2.0) + (daySeed * 0.15)
		ramVal := (0.35 + 0.04*math.Cos(daySeed/3.0)) * ramTotal
		diskVal := (0.28 + float64(30-i)*0.0018) * diskTotal

		history = append(history, HistoryPoint{
			Label: t.Format("Jan 02"),
			CPU:   math.Round(cpuVal*10) / 10,
			RAM:   math.Round((ramVal/ramTotal)*100*10) / 10,
			Disk:  math.Round((diskVal/diskTotal)*100*10) / 10,
		})
	}

	saveHistory(history)
	return history
}

func saveHistory(history []HistoryPoint) {
	path := getHistoryPath()
	data, err := json.MarshalIndent(history, "", "  ")
	if err == nil {
		_ = os.WriteFile(path, data, 0644)
	}
}

func recordCurrentMetrics() {
	history := loadOrCreateHistory()
	currRam := getRAM()
	currDisk := getDisk()

	ramPct := 0.0
	if currRam.Total > 0 {
		ramPct = currRam.Pct
	}
	diskPct := 0.0
	if currDisk.Total > 0 {
		diskPct = currDisk.Pct
	}

	newPoint := HistoryPoint{
		Label: time.Now().Format("Jan 02"),
		CPU:   math.Round(getCPU()*10) / 10,
		RAM:   math.Round(ramPct*10) / 10,
		Disk:  math.Round(diskPct*10) / 10,
	}

	if len(history) >= 30 {
		history = history[1:]
	}
	history = append(history, newPoint)
	saveHistory(history)
}

func handleMetricsHistoryAPI(w http.ResponseWriter, r *http.Request) {
	history := loadOrCreateHistory()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(history)
}

// GuiAuthConfig is used for the secondary lock layer
type GuiAuthConfig struct {
	Enabled      bool   `json:"enabled"`
	Username     string `json:"username,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"`
	PinHash      string `json:"pin_hash,omitempty"`
}

var (
	sessionMutex   sync.RWMutex
	activeSessions = make(map[string]time.Time)
)

func getGuiAuthPath() string {
	if runtime.GOOS == "windows" {
		return "./gui_auth.json"
	}
	return "/etc/agilepanel/gui_auth.json"
}

func readGuiAuth() (*GuiAuthConfig, error) {
	path := getGuiAuthPath()
	file, err := os.Open(path)
	if err != nil {
		return &GuiAuthConfig{Enabled: false}, nil
	}
	defer file.Close()

	var config GuiAuthConfig
	if err := json.NewDecoder(file).Decode(&config); err != nil {
		return nil, err
	}
	return &config, nil
}

func writeGuiAuth(config *GuiAuthConfig) error {
	path := getGuiAuthPath()
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func sessionAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authConf, err := readGuiAuth()
		if err != nil || !authConf.Enabled || authConf.PinHash == "" {
			next(w, r)
			return
		}

		cookie, err := r.Cookie("ap_gui_session")
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("Session Required"))
			return
		}

		sessionMutex.RLock()
		expiry, ok := activeSessions[cookie.Value]
		sessionMutex.RUnlock()

		if !ok || time.Now().After(expiry) {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("Session Expired or Invalid"))
			return
		}

		next(w, r)
	}
}

func handleAuthStatusAPI(w http.ResponseWriter, r *http.Request) {
	authConf, err := readGuiAuth()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	initialized := authConf.PinHash != ""
	
	authenticated := false
	cookie, err := r.Cookie("ap_gui_session")
	if err == nil {
		sessionMutex.RLock()
		expiry, ok := activeSessions[cookie.Value]
		sessionMutex.RUnlock()
		if ok && time.Now().Before(expiry) {
			authenticated = true
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"initialized":   initialized,
		"enabled":       authConf.Enabled,
		"authenticated": authenticated,
	})
}

func handleAuthSignupAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	authConf, err := readGuiAuth()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if authConf.PinHash != "" {
		http.Error(w, "GUI panel lock PIN already configured", http.StatusBadRequest)
		return
	}

	var payload struct {
		Pin string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate PIN contains 6-10 digits only
	pinLen := len(payload.Pin)
	if pinLen < 6 || pinLen > 10 {
		http.Error(w, "PIN must be between 6 and 10 digits in length", http.StatusBadRequest)
		return
	}
	for _, r := range payload.Pin {
		if r < '0' || r > '9' {
			http.Error(w, "PIN must contain digits only", http.StatusBadRequest)
			return
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(payload.Pin), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Failed to hash PIN code", http.StatusInternalServerError)
		return
	}

	authConf.PinHash = string(hash)
	authConf.Enabled = true

	if err := writeGuiAuth(authConf); err != nil {
		http.Error(w, "Failed to save GUI auth configurations", http.StatusInternalServerError)
		return
	}

	token := createSession()
	http.SetCookie(w, &http.Cookie{
		Name:     "ap_gui_session",
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(24 * time.Hour),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	w.Write([]byte("Numerical PIN set up successfully"))
}

func handleAuthLoginAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	authConf, err := readGuiAuth()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if authConf.PinHash == "" {
		http.Error(w, "GUI panel lock PIN not configured yet", http.StatusBadRequest)
		return
	}

	var payload struct {
		Pin string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if bcrypt.CompareHashAndPassword([]byte(authConf.PinHash), []byte(payload.Pin)) != nil {
		http.Error(w, "Invalid PIN code entered", http.StatusUnauthorized)
		return
	}

	token := createSession()
	http.SetCookie(w, &http.Cookie{
		Name:     "ap_gui_session",
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(24 * time.Hour),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	w.Write([]byte("Logged in successfully"))
}

func handleAuthLogoutAPI(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("ap_gui_session")
	if err == nil {
		sessionMutex.Lock()
		delete(activeSessions, cookie.Value)
		sessionMutex.Unlock()
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "ap_gui_session",
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
	})

	w.Write([]byte("Logged out successfully"))
}

func handleAuthToggleAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	authConf, err := readGuiAuth()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if authConf.PinHash == "" {
		http.Error(w, "Cannot toggle: set up credentials first", http.StatusBadRequest)
		return
	}

	authConf.Enabled = payload.Enabled
	if err := writeGuiAuth(authConf); err != nil {
		http.Error(w, "Failed to save GUI auth", http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Secondary authentication toggled successfully"))
}

func createSession() string {
	token := fmt.Sprintf("%d", time.Now().UnixNano())
	sessionMutex.Lock()
	activeSessions[token] = time.Now().Add(24 * time.Hour)
	sessionMutex.Unlock()
	return token
}


func getDatabaseSizes() map[string]float64 {
	sizes := make(map[string]float64)
	if runtime.GOOS == "windows" {
		sizes["wordpress_db"] = 45.2
		sizes["laravel_db"] = 12.8
		sizes["mysql"] = 4.1
		sizes["information_schema"] = 0.2
		return sizes
	}

	cmd := exec.Command("mysql", "-N", "-B", "-e", "SELECT table_schema, SUM(data_length + index_length) / 1024 / 1024 FROM information_schema.tables GROUP BY table_schema;")
	out, err := cmd.Output()
	if err != nil {
		return sizes
	}

	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) == 2 {
			dbName := parts[0]
			var size float64
			fmt.Sscanf(parts[1], "%f", &size)
			sizes[dbName] = size
		}
	}
	return sizes
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
		"dbSizes":       getDatabaseSizes(),
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

var (
	cachedPublicIP     string
	cachedPublicIPOnce sync.Once
)

func getPublicIP() string {
	cachedPublicIPOnce.Do(func() {
		client := http.Client{
			Timeout: 2 * 1000 * 1000 * 1000, // 2 seconds
		}
		resp, err := client.Get("https://api.ipify.org")
		if err == nil {
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err == nil {
				ip := strings.TrimSpace(string(body))
				if len(ip) > 0 && !strings.Contains(ip, " ") {
					cachedPublicIP = ip
					return
				}
			}
		}
		cachedPublicIP = "127.0.0.1"
	})
	return cachedPublicIP
}

func handleSitesAPI(w http.ResponseWriter, r *http.Request) {
	state, err := readState()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type SiteResponse struct {
		SiteConfig
		StagingURL      string `json:"staging_url"`
		HasFilesBackup  bool   `json:"has_files_backup"`
		HasDbBackup     bool   `json:"has_db_backup"`
		FilesBackupTime string `json:"files_backup_time"`
		DbBackupTime    string `json:"db_backup_time"`
	}

	var resp []SiteResponse
	ip := getPublicIP()
	for _, s := range state.Sites {
		var parentDir string
		if runtime.GOOS == "windows" {
			parentDir = filepath.Clean(filepath.Join("./var/www", s.Domain))
		} else {
			parentDir = "/var/www/" + s.Domain
		}
		backupDir := filepath.Join(parentDir, "backup")
		filesZip := filepath.Join(backupDir, s.Domain+"-files.zip")
		dbZip := filepath.Join(backupDir, s.Domain+"-db.zip")

		statFiles, errFiles := os.Stat(filesZip)
		statDb, errDb := os.Stat(dbZip)

		var filesTimeStr, dbTimeStr string
		if errFiles == nil {
			filesTimeStr = statFiles.ModTime().Format("2006-01-02 15:04:05")
		}
		if errDb == nil {
			dbTimeStr = statDb.ModTime().Format("2006-01-02 15:04:05")
		}

		resp = append(resp, SiteResponse{
			SiteConfig:      s,
			StagingURL:      fmt.Sprintf("http://%s.%s.sslip.io", s.Domain, ip),
			HasFilesBackup:  errFiles == nil,
			HasDbBackup:     errDb == nil,
			FilesBackupTime: filesTimeStr,
			DbBackupTime:    dbTimeStr,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleBackupDownloadAPI(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	backupType := r.URL.Query().Get("type") // "files" or "db"

	if domain == "" || (backupType != "files" && backupType != "db") {
		http.Error(w, "Invalid parameters", http.StatusBadRequest)
		return
	}

	state, err := readState()
	if err != nil {
		http.Error(w, "Failed to read state", http.StatusInternalServerError)
		return
	}

	valid := false
	for _, s := range state.Sites {
		if strings.EqualFold(s.Domain, domain) {
			valid = true
			break
		}
	}
	if !valid {
		http.Error(w, "Access denied: domain not found", http.StatusForbidden)
		return
	}

	var parentDir string
	if runtime.GOOS == "windows" {
		parentDir = filepath.Clean(filepath.Join("./var/www", domain))
	} else {
		parentDir = "/var/www/" + domain
	}

	backupDir := filepath.Join(parentDir, "backup")
	var zipPath string
	if backupType == "files" {
		zipPath = filepath.Join(backupDir, domain+"-files.zip")
	} else {
		zipPath = filepath.Join(backupDir, domain+"-db.zip")
	}

	info, err := os.Stat(zipPath)
	if os.IsNotExist(err) {
		http.Error(w, "Backup file not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filepath.Base(zipPath)))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	http.ServeFile(w, r, zipPath)
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
	// 1. Check virtual configuration redirects
	slashedPath := strings.ReplaceAll(filepath.ToSlash(path), "\\", "/")
	if slashedPath == "conf/php-fpm.conf" {
		state, err := readState()
		if err == nil {
			for _, s := range state.Sites {
				if strings.EqualFold(s.Domain, domain) {
					var phpConf string
					if runtime.GOOS == "windows" {
						phpConf = filepath.Clean(fmt.Sprintf("./etc/php/%s/fpm/pool.d/%s.conf", s.PHPVersion, domain))
					} else {
						phpConf = fmt.Sprintf("/etc/php/%s/fpm/pool.d/%s.conf", s.PHPVersion, domain)
					}
					_ = os.MkdirAll(filepath.Dir(phpConf), 0755)
					return phpConf, nil
				}
			}
		}
	}

	if slashedPath == "conf/caddy.conf" {
		if runtime.GOOS == "windows" {
			return filepath.Clean("./etc/caddy/Caddyfile"), nil
		}
		return "/etc/caddy/Caddyfile", nil
	}

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

	type FileItem struct {
		Name    string `json:"name"`
		Size    int64  `json:"size"`
		IsDir   bool   `json:"isDir"`
		ModTime string `json:"modTime"`
		Mode    string `json:"mode"`
	}

	// 1. Intercept listing the virtual "conf" folder
	slashedPath := strings.ReplaceAll(filepath.ToSlash(relPath), "\\", "/")
	if slashedPath == "conf" {
		var phpSize, caddySize int64
		phpSize = 1024
		caddySize = 2048
		
		state, err := readState()
		if err == nil {
			for _, s := range state.Sites {
				if strings.EqualFold(s.Domain, domain) {
					var phpConf string
					if runtime.GOOS == "windows" {
						phpConf = fmt.Sprintf("./etc/php/%s/fpm/pool.d/%s.conf", s.PHPVersion, domain)
					} else {
						phpConf = fmt.Sprintf("/etc/php/%s/fpm/pool.d/%s.conf", s.PHPVersion, domain)
					}
					if info, err := os.Stat(phpConf); err == nil {
						phpSize = info.Size()
					}
				}
			}
		}
		
		caddyPath := "/etc/caddy/Caddyfile"
		if runtime.GOOS == "windows" {
			caddyPath = "./etc/caddy/Caddyfile"
		}
		if info, err := os.Stat(caddyPath); err == nil {
			caddySize = info.Size()
		}
		
		nowStr := time.Now().Format("2006-01-02 15:04:05")
		items := []FileItem{
			{Name: "php-fpm.conf", Size: phpSize, IsDir: false, ModTime: nowStr, Mode: "-rw-r--r--"},
			{Name: "caddy.conf", Size: caddySize, IsDir: false, ModTime: nowStr, Mode: "-rw-r--r--"},
		}
		
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(items)
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

	// 2. Inject virtual "conf" directory when listing root
	if relPath == "" || relPath == "." {
		hasConf := false
		for _, item := range items {
			if item.Name == "conf" {
				hasConf = true
				break
			}
		}
		if !hasConf {
			items = append(items, FileItem{
				Name:    "conf",
				Size:    4096,
				IsDir:   true,
				ModTime: time.Now().Format("2006-01-02 15:04:05"),
				Mode:    "drwxr-xr-x",
			})
		}
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

func zipFiles(source, target string) error {
	zipfile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer zipfile.Close()

	archive := zip.NewWriter(zipfile)
	defer archive.Close()

	info, err := os.Stat(source)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.Base(source)
		header.Method = zip.Deflate

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		file, err := os.Open(source)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(writer, file)
		return err
	}

	err = filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		header.Name = filepath.ToSlash(relPath)
		if info.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(writer, file)
		return err
	})

	return err
}

func unzipFiles(source, target string) error {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, file := range reader.File {
		filePath := filepath.Join(target, file.Name)
		if !strings.HasPrefix(filePath, filepath.Clean(target)) {
			return fmt.Errorf("illegal file path inside zip: %s", file.Name)
		}

		if file.FileInfo().IsDir() {
			os.MkdirAll(filePath, os.ModePerm)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
			return err
		}

		dstFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			return err
		}

		srcFile, err := file.Open()
		if err != nil {
			dstFile.Close()
			return err
		}

		_, err = io.Copy(dstFile, srcFile)
		srcFile.Close()
		dstFile.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func handleFileZipAPI(w http.ResponseWriter, r *http.Request) {
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

	zipPath := fullPath + ".zip"
	err = zipFiles(fullPath, zipPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to zip: %v", err), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Zipped successfully"))
}

func handleFileUnzipAPI(w http.ResponseWriter, r *http.Request) {
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

	destDir := filepath.Dir(fullPath)
	err = unzipFiles(fullPath, destDir)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to unzip: %v", err), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Unzipped successfully"))
}

func handleFileRenameAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Domain  string `json:"domain"`
		OldPath string `json:"oldPath"`
		NewPath string `json:"newPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if payload.Domain == "" || payload.OldPath == "" || payload.NewPath == "" {
		http.Error(w, "Domain, oldPath, and newPath required", http.StatusBadRequest)
		return
	}

	oldFullPath, err := validateFilePath(payload.Domain, payload.OldPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	newFullPath, err := validateFilePath(payload.Domain, payload.NewPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	_ = os.MkdirAll(filepath.Dir(newFullPath), 0755)

	err = os.Rename(oldFullPath, newFullPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to rename: %v", err), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Renamed successfully"))
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

	// Periodic metrics logging ticker (every 2 hours)
	go func() {
		recordCurrentMetrics()
		ticker := time.NewTicker(2 * time.Hour)
		for range ticker.C {
			recordCurrentMetrics()
		}
	}()

	http.HandleFunc("/", basicAuth(serveStatic("index.html", "text/html")))
	http.HandleFunc("/style.css", serveStatic("style.css", "text/css"))
	http.HandleFunc("/app.js", serveStatic("app.js", "application/javascript"))

	// Authentication API Endpoints (Bypass secondary session check but require basic auth)
	http.HandleFunc("/api/auth/status", basicAuth(handleAuthStatusAPI))
	http.HandleFunc("/api/auth/signup", basicAuth(handleAuthSignupAPI))
	http.HandleFunc("/api/auth/login", basicAuth(handleAuthLoginAPI))
	http.HandleFunc("/api/auth/logout", basicAuth(handleAuthLogoutAPI))
	http.HandleFunc("/api/auth/toggle", basicAuth(handleAuthToggleAPI))

	// Core API Endpoints (Require BOTH basic auth and secondary session check)
	http.HandleFunc("/api/status", basicAuth(sessionAuth(handleStatusAPI)))
	http.HandleFunc("/api/sites", basicAuth(sessionAuth(handleSitesAPI)))
	http.HandleFunc("/api/backup/download", basicAuth(sessionAuth(handleBackupDownloadAPI)))
	http.HandleFunc("/api/metrics/history", basicAuth(sessionAuth(handleMetricsHistoryAPI)))
	http.HandleFunc("/api/action", basicAuth(sessionAuth(handleCommandExecuteAPI)))

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

	log.Println("AgilePanel GUI Dashboard starting on http://localhost:8889...")
	if err := http.ListenAndServe(":8889", nil); err != nil {
		log.Fatalf("Server startup failed: %v", err)
	}
}

