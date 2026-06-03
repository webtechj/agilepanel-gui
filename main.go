package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
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
	S3Endpoint           string   `json:"s3_endpoint,omitempty"`
	S3Region             string   `json:"s3_region,omitempty"`
	S3Bucket             string   `json:"s3_bucket,omitempty"`
	S3AccessKey          string   `json:"s3_access_key,omitempty"`
	S3SecretKey          string   `json:"s3_secret_key,omitempty"`
	TelegramBotToken     string   `json:"telegram_bot_token,omitempty"`
	TelegramChatID       string   `json:"telegram_chat_id,omitempty"`
}

type SiteConfig struct {
	Domain            string    `json:"domain"`
	PHPVersion        string    `json:"php_version"`
	PublicDir         string    `json:"public_dir"`
	DatabaseName      string    `json:"database_name"`
	DatabaseUser      string    `json:"db_user"`
	DatabasePass      string    `json:"db_pass,omitempty"`
	SystemUser        string    `json:"system_user"`
	IsLocked          bool      `json:"is_locked"`
	Type              string    `json:"type,omitempty"`
	StagingUnlocked   bool      `json:"staging_unlocked,omitempty"`
	BackupInterval    string    `json:"backup_interval,omitempty"`
	LastBackupTime    time.Time `json:"last_backup_time,omitempty"`
	BackupDestination string    `json:"backup_destination,omitempty"`
	S3BackupVersions  int       `json:"s3_backup_versions,omitempty"`
	S3Enabled         bool      `json:"s3_enabled,omitempty"`
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

var stateMutex sync.RWMutex

func readState() (*State, error) {
	stateMutex.RLock()
	defer stateMutex.RUnlock()
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

func writeState(s *State) error {
	stateMutex.Lock()
	defer stateMutex.Unlock()
	path := getStatePath()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if _, errStat := os.Stat(path); errStat == nil {
		bakPath := path + ".bak"
		currentData, errRead := os.ReadFile(path)
		if errRead == nil {
			_ = os.WriteFile(bakPath, currentData, 0600)
		}
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0660); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func updateLastBackupTime(domain string) {
	state, err := readState()
	if err == nil {
		for i, s := range state.Sites {
			if strings.EqualFold(s.Domain, domain) {
				state.Sites[i].LastBackupTime = time.Now()
				_ = writeState(state)
				break
			}
		}
	}
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
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("AgilePanel is not configured. Run 'ap server auth [user] [pass]' to set credentials."))
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
	Label     string    `json:"label"`
	Timestamp time.Time `json:"timestamp"`
	CPU       float64   `json:"cpu"`
	RAM       float64   `json:"ram"`
	Disk      float64   `json:"disk"`
	Load      float64   `json:"load"`
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

	// Generate 7 days of realistic 2-hourly mock history (84 points) if empty or missing
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
	for i := 83; i >= 0; i-- {
		t := now.Add(-time.Duration(i*2) * time.Hour)
		// Generate semi-random stable curves
		hourSeed := float64(t.Hour() + t.Day())
		cpuVal := 12.0 + 6.0*math.Sin(hourSeed/2.0) + (float64(t.Day()) * 0.15)
		ramVal := (0.35 + 0.04*math.Cos(hourSeed/3.0)) * ramTotal
		diskVal := (0.28 + float64(83-i)*0.0005) * diskTotal

		history = append(history, HistoryPoint{
			Label:     t.Format("Jan 02 15:04"),
			Timestamp: t,
			CPU:       math.Round(cpuVal*10) / 10,
			RAM:       math.Round((ramVal/ramTotal)*100*10) / 10,
			Disk:      math.Round((diskVal/diskTotal)*100*10) / 10,
			Load:      math.Round((cpuVal/50.0)*100) / 100, // mock load proportional to CPU
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

var lastStatusReportTime time.Time
var lastTelegramAlertTime time.Time

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

	cpuPct := math.Round(getCPU()*10) / 10
	ramPct = math.Round(ramPct*10) / 10
	diskPct = math.Round(diskPct*10) / 10

	loads := getLoadAverages()
	var currentLoad float64
	if len(loads) > 0 {
		currentLoad = loads[0]
	}

	newPoint := HistoryPoint{
		Label:     time.Now().Format("Jan 02 15:04"),
		Timestamp: time.Now(),
		CPU:       cpuPct,
		RAM:       ramPct,
		Disk:      diskPct,
		Load:      currentLoad,
	}

	if len(history) >= 84 {
		history = history[1:]
	}
	history = append(history, newPoint)
	saveHistory(history)

	// Send Telegram Reports/Alerts
	state, err := readState()
	if err == nil && state.Global.TelegramBotToken != "" && state.Global.TelegramChatID != "" {
		host, _ := os.Hostname()

		// Calculate 24h load average from the last 12 samples (representing 24 hours of 2-hourly metrics)
		var avgLoad24h float64
		var count int
		startIdx := len(history) - 12
		if startIdx < 0 {
			startIdx = 0
		}
		var sum float64
		for _, p := range history[startIdx:] {
			sum += p.Load
			count++
		}
		if count > 0 {
			avgLoad24h = sum / float64(count)
		}

		// 1. Periodic Server Status (Every 12 hours)
		if lastStatusReportTime.IsZero() || time.Since(lastStatusReportTime) >= 12*time.Hour {
			lastStatusReportTime = time.Now()
			msg := fmt.Sprintf("📊 <b>AgilePanel Server Status Report</b>\n\n<b>Host:</b> %s\n💻 <b>CPU Usage:</b> %.1f%%\n🧠 <b>RAM Usage:</b> %.1f%% (%.1f GB / %.1f GB)\n💾 <b>Disk Usage:</b> %.1f%% (%.1f GB / %.1f GB)\n⏱️ <b>24h Load Avg:</b> %.2f",
				host, cpuPct, ramPct, currRam.Used, currRam.Total, diskPct, currDisk.Used, currDisk.Total, avgLoad24h)
			_ = SendTelegramNotification(state.Global.TelegramBotToken, state.Global.TelegramChatID, msg)
		}

		// 2. Alert on high resource usage (> 90%)
		if cpuPct > 90.0 || ramPct > 90.0 || diskPct > 90.0 {
			if lastTelegramAlertTime.IsZero() || time.Since(lastTelegramAlertTime) >= 4*time.Hour {
				lastTelegramAlertTime = time.Now()
				msg := fmt.Sprintf("⚠️ <b>AgilePanel Warning: High Resource Usage Alert!</b>\n\n<b>Host:</b> %s\n🚨 CPU: %.1f%% | RAM: %.1f%% | Disk: %.1f%%\nPlease check your server processes immediately.",
					host, cpuPct, ramPct, diskPct)
				_ = SendTelegramNotification(state.Global.TelegramBotToken, state.Global.TelegramChatID, msg)
			}
		}
	}
}

func handleMetricsHistoryAPI(w http.ResponseWriter, r *http.Request) {
	history := loadOrCreateHistory()

	// 1. Get "today" points (last 12 points, representing last 24 hours of 2-hourly metrics)
	var todayPoints []HistoryPoint
	startIdx := len(history) - 12
	if startIdx < 0 {
		startIdx = 0
	}
	for _, p := range history[startIdx:] {
		todayPoints = append(todayPoints, HistoryPoint{
			Label:     p.Timestamp.Format("15:04"),
			Timestamp: p.Timestamp,
			CPU:       p.CPU,
			RAM:       p.RAM,
			Disk:      p.Disk,
			Load:      p.Load,
		})
	}

	// 2. Get "thisweek" points (7 points, one for each day of the last 7 days)
	type dayData struct {
		cpuSum  float64
		ramSum  float64
		diskSum float64
		count   int
		time    time.Time
	}

	dayMap := make(map[string]*dayData)
	var dayKeys []string

	for _, p := range history {
		dayKey := p.Timestamp.Format("2006-01-02")
		if _, exists := dayMap[dayKey]; !exists {
			dayMap[dayKey] = &dayData{time: p.Timestamp}
			dayKeys = append(dayKeys, dayKey)
		}
		d := dayMap[dayKey]
		d.cpuSum += p.CPU
		d.ramSum += p.RAM
		d.diskSum += p.Disk
		d.count++
	}

	var thisWeekPoints []HistoryPoint
	startDayIdx := len(dayKeys) - 7
	if startDayIdx < 0 {
		startDayIdx = 0
	}
	for _, key := range dayKeys[startDayIdx:] {
		d := dayMap[key]
		count := float64(d.count)
		thisWeekPoints = append(thisWeekPoints, HistoryPoint{
			Label:     d.time.Format("Jan 02"),
			Timestamp: d.time,
			CPU:       math.Round((d.cpuSum/count)*10) / 10,
			RAM:       math.Round((d.ramSum/count)*10) / 10,
			Disk:      math.Round((d.diskSum/count)*10) / 10,
		})
	}

	response := map[string]interface{}{
		"today":    todayPoints,
		"thisweek": thisWeekPoints,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
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
	return os.WriteFile(path, data, 0600)
}

func getCSRFToken(sessionToken string) string {
	hash := sha256.Sum256([]byte(sessionToken))
	return hex.EncodeToString(hash[:])
}

func getAgilePanelVersion() string {
	apBin := "ap"
	if runtime.GOOS != "windows" {
		if _, err := os.Stat("/usr/local/bin/ap"); err == nil {
			apBin = "/usr/local/bin/ap"
		}
	} else {
		apBin = "../agilepanel/ap.exe"
	}

	cmd := exec.Command(apBin, "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "1.0.1"
	}
	parts := strings.Fields(out.String())
	if len(parts) >= 3 {
		return parts[2]
	}
	return strings.TrimSpace(out.String())
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

		// CSRF Double-Submit check
		headerToken := r.Header.Get("X-Session-Token")
		expectedCSRF := getCSRFToken(cookie.Value)
		if headerToken == "" || headerToken != expectedCSRF {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("CSRF validation failed: Session Token mismatch"))
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
	secureCookie := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     "ap_gui_session",
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(1 * time.Hour),
		HttpOnly: true,
		Secure:   secureCookie,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "ap_csrf_token",
		Value:    getCSRFToken(token),
		Path:     "/",
		Expires:  time.Now().Add(1 * time.Hour),
		HttpOnly: false,
		Secure:   secureCookie,
		SameSite: http.SameSiteStrictMode,
	})

	w.Write([]byte("Numerical PIN set up successfully"))
}

func logAuditEvent(ip, action, details, status string) {
	logPath := "./agilepanel_audit.log"
	if runtime.GOOS != "windows" {
		logPath = "/var/log/agilepanel_audit.log"
	}
	
	entry := map[string]string{
		"timestamp": time.Now().Format(time.RFC3339),
		"ip":        ip,
		"action":    action,
		"details":   details,
		"status":    status,
	}
	
	data, err := json.Marshal(entry)
	if err == nil {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err == nil {
			defer f.Close()
			_, _ = f.Write(append(data, '\n'))
		}
	}
}

func scanFileForViruses(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if bytes.Contains(data, []byte("X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*")) {
		return fmt.Errorf("virus detected: EICAR test signature")
	}
	if bytes.Contains(data, []byte("<?php")) && (bytes.Contains(data, []byte("eval(")) || bytes.Contains(data, []byte("shell_exec(")) || bytes.Contains(data, []byte("system("))) {
		return fmt.Errorf("malicious PHP script signature detected (web shell)")
	}
	
	if _, err := exec.LookPath("clamscan"); err == nil {
		cmd := exec.Command("clamscan", "--no-summary", path)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("virus scan failed: file is infected or suspicious")
		}
	}
	return nil
}

type TokenBucket struct {
	tokens     float64
	lastRefill time.Time
}

var (
	rateLimiterMutex sync.Mutex
	rateLimiterMap   = make(map[string]*TokenBucket)
)

func rateLimitMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := getClientIP(r)
		now := time.Now()

		rateLimiterMutex.Lock()
		tb, exists := rateLimiterMap[ip]
		if !exists {
			tb = &TokenBucket{
				tokens:     10.0, // max capacity
				lastRefill: now,
			}
			rateLimiterMap[ip] = tb
		}

		// Refill rate: 1 token per 2 seconds (0.5 tokens/sec)
		elapsed := now.Sub(tb.lastRefill).Seconds()
		tb.tokens += elapsed * 0.5
		if tb.tokens > 10.0 {
			tb.tokens = 10.0
		}
		tb.lastRefill = now

		if tb.tokens < 1.0 {
			rateLimiterMutex.Unlock()
			http.Error(w, "Too many requests. Please try again later.", http.StatusTooManyRequests)
			return
		}

		tb.tokens -= 1.0
		rateLimiterMutex.Unlock()

		next(w, r)
	}
}

var (
	loginAttemptsMutex sync.Mutex
	loginAttempts      = make(map[string][]time.Time)
)

func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

func handleAuthLoginAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ip := getClientIP(r)
	loginAttemptsMutex.Lock()
	attempts := loginAttempts[ip]
	now := time.Now()
	var validAttempts []time.Time
	for _, t := range attempts {
		if now.Sub(t) < 60*time.Second {
			validAttempts = append(validAttempts, t)
		}
	}
	loginAttempts[ip] = validAttempts
	if len(validAttempts) >= 5 {
		loginAttemptsMutex.Unlock()
		http.Error(w, "Too many login attempts. Please try again after 60 seconds.", http.StatusTooManyRequests)
		return
	}
	loginAttemptsMutex.Unlock()

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
		loginAttemptsMutex.Lock()
		loginAttempts[ip] = append(loginAttempts[ip], now)
		loginAttemptsMutex.Unlock()
		http.Error(w, "Invalid PIN code entered", http.StatusUnauthorized)
		return
	}

	loginAttemptsMutex.Lock()
	delete(loginAttempts, ip)
	loginAttemptsMutex.Unlock()

	token := createSession()
	secureCookie := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{
		Name:     "ap_gui_session",
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(1 * time.Hour),
		HttpOnly: true,
		Secure:   secureCookie,
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "ap_csrf_token",
		Value:    getCSRFToken(token),
		Path:     "/",
		Expires:  time.Now().Add(1 * time.Hour),
		HttpOnly: false,
		Secure:   secureCookie,
		SameSite: http.SameSiteStrictMode,
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
		HttpOnly: false,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "ap_csrf_token",
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: false,
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
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("Critical security error: failed to generate secure session token: %v", err)
	}
	token := hex.EncodeToString(b)
	sessionMutex.Lock()
	activeSessions[token] = time.Now().Add(1 * time.Hour)
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
		"apVersion":     getAgilePanelVersion(),
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

	if !isValidDomain(domain) {
		http.Error(w, "Invalid domain format", http.StatusBadRequest)
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

	// Command argument sanitization: check all args against safe patterns to prevent injection
	safeArgRegex := regexp.MustCompile(`^[a-zA-Z0-9._-]*$`)
	for _, arg := range payload.Args {
		if !safeArgRegex.MatchString(arg) {
			http.Error(w, "Invalid character detected in command arguments", http.StatusBadRequest)
			return
		}
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
		"server-clean":    true,
	}

	if !allowedActions[payload.Action] {
		http.Error(w, "Action not allowed", http.StatusForbidden)
		return
	}

	// Validate parameters for each action to prevent command injection or parameter manipulation
	switch payload.Action {
	case "site-create":
		if len(payload.Args) < 4 {
			http.Error(w, "Missing arguments for site creation", http.StatusBadRequest)
			return
		}
		domain := payload.Args[0]
		phpVer := payload.Args[1]
		siteType := payload.Args[2]
		dbName := payload.Args[3]

		if !isValidDomain(domain) {
			http.Error(w, "Invalid domain format", http.StatusBadRequest)
			return
		}
		if !regexp.MustCompile(`^[0-9]\.[0-9]$`).MatchString(phpVer) {
			http.Error(w, "Invalid PHP version format", http.StatusBadRequest)
			return
		}
		if !regexp.MustCompile(`^(html|laravel|php|wp)$`).MatchString(siteType) {
			http.Error(w, "Invalid site type", http.StatusBadRequest)
			return
		}
		if dbName != "" && !regexp.MustCompile(`^[a-zA-Z0-9_]+$`).MatchString(dbName) {
			http.Error(w, "Invalid database name format", http.StatusBadRequest)
			return
		}
		if len(payload.Args) > 4 && payload.Args[4] != "" && !isValidImportPath(payload.Args[4], domain, "files") {
			http.Error(w, "Invalid files import path", http.StatusBadRequest)
			return
		}
		if len(payload.Args) > 5 && payload.Args[5] != "" && !isValidImportPath(payload.Args[5], domain, "db") {
			http.Error(w, "Invalid database import path", http.StatusBadRequest)
			return
		}
		for i := 6; i < len(payload.Args); i++ {
			if payload.Args[i] != "" && strings.ContainsAny(payload.Args[i], "\r\n\x00") {
				http.Error(w, "Invalid characters in parameters", http.StatusBadRequest)
				return
			}
		}

	case "site-delete", "site-lock", "site-unlock", "site-cache", "site-reinstall", "site-ssl", "site-perms", "site-backup", "site-backup-db":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing domain argument", http.StatusBadRequest)
			return
		}
		if !isValidDomain(payload.Args[0]) {
			http.Error(w, "Invalid domain format", http.StatusBadRequest)
			return
		}

	case "site-restore":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing domain argument for restore", http.StatusBadRequest)
			return
		}
		if !isValidDomain(payload.Args[0]) {
			http.Error(w, "Invalid domain format", http.StatusBadRequest)
			return
		}
		if len(payload.Args) > 1 && payload.Args[1] != "" && !isValidTimestamp(payload.Args[1]) {
			http.Error(w, "Invalid timestamp format", http.StatusBadRequest)
			return
		}

	case "server-restart":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing service name argument", http.StatusBadRequest)
			return
		}
		if !isValidService(payload.Args[0]) {
			http.Error(w, "Invalid service name", http.StatusBadRequest)
			return
		}

	case "tool-install":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing tool name argument", http.StatusBadRequest)
			return
		}
		if !isValidTool(payload.Args[0]) {
			http.Error(w, "Invalid tool name", http.StatusBadRequest)
			return
		}
	}

	if payload.Action == "server-clean" {
		diskInfo := getDisk()
		if diskInfo.Pct < 80.0 {
			http.Error(w, fmt.Sprintf("Clean up is not required. Disk usage is under 80%% (current usage: %.1f%%).", diskInfo.Pct), http.StatusBadRequest)
			return
		}
	}

	if payload.Action == "site-restore" {
		timestamp := ""
		if len(payload.Args) > 1 {
			timestamp = payload.Args[1]
		}
		handleSiteRestoreAPI(w, r, payload.Args[0], timestamp)
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
		apBin = "../agilepanel/ap.exe" // Local mockup executable
	}

	switch payload.Action {
	case "site-create":
		if len(payload.Args) < 4 {
			http.Error(w, "Missing arguments for site creation", http.StatusBadRequest)
			return
		}
		cmdArgs = []string{"site", "create", payload.Args[0], "--type=" + payload.Args[2], "--php=" + payload.Args[1], "--db=" + payload.Args[3]}
		if len(payload.Args) > 4 && payload.Args[4] != "" {
			cmdArgs = append(cmdArgs, "--import-files=" + payload.Args[4])
		}
		if len(payload.Args) > 5 && payload.Args[5] != "" {
			cmdArgs = append(cmdArgs, "--import-db=" + payload.Args[5])
		}
		if len(payload.Args) > 6 && payload.Args[6] != "" {
			cmdArgs = append(cmdArgs, "--wp-user=" + payload.Args[6])
		}
		if len(payload.Args) > 7 && payload.Args[7] != "" {
			cmdArgs = append(cmdArgs, "--wp-pass=" + payload.Args[7])
		}
		if len(payload.Args) > 8 && payload.Args[8] != "" {
			cmdArgs = append(cmdArgs, "--wp-email=" + payload.Args[8])
		}
		if len(payload.Args) > 9 && payload.Args[9] != "" {
			cmdArgs = append(cmdArgs, "--wp-name=" + payload.Args[9])
		}
	case "site-delete":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing domain argument", http.StatusBadRequest)
			return
		}
		cmdArgs = []string{"site", "delete", payload.Args[0], "-y"}
	case "site-lock":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing domain argument", http.StatusBadRequest)
			return
		}
		cmdArgs = []string{"site", "lock", payload.Args[0], "-y"}
	case "site-unlock":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing domain argument", http.StatusBadRequest)
			return
		}
		cmdArgs = []string{"site", "unlock", payload.Args[0]}
	case "site-cache":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing domain argument", http.StatusBadRequest)
			return
		}
		cmdArgs = []string{"site", "cache-clean", payload.Args[0]}
	case "site-reinstall":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing domain argument", http.StatusBadRequest)
			return
		}
		cmdArgs = []string{"site", "reinstall", payload.Args[0]}
	case "site-ssl":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing domain argument", http.StatusBadRequest)
			return
		}
		cmdArgs = []string{"site", "ssl-renew", payload.Args[0]}
	case "site-perms":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing domain argument", http.StatusBadRequest)
			return
		}
		cmdArgs = []string{"site", "fix-permissions", payload.Args[0]}
	case "site-backup":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing domain argument", http.StatusBadRequest)
			return
		}
		cmdArgs = []string{"site", "backup", payload.Args[0]}
	case "site-backup-db":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing domain argument", http.StatusBadRequest)
			return
		}
		cmdArgs = []string{"site", "backup-db", payload.Args[0]}
	case "server-restart":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing service name argument", http.StatusBadRequest)
			return
		}
		cmdArgs = []string{"server", "restart", payload.Args[0]}
	case "server-tune":
		cmdArgs = []string{"server", "tune"}
	case "server-secure":
		cmdArgs = []string{"server", "secure"}
	case "tool-install":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing tool name argument", http.StatusBadRequest)
			return
		}
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
	case "server-clean":
		cmdArgs = []string{"server", "clean"}
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
		logAuditEvent(getClientIP(r), "execute-command", fmt.Sprintf("Action: %s, Args: %v - Startup error: %v", payload.Action, payload.Args, err), "error")
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
		logAuditEvent(getClientIP(r), "execute-command", fmt.Sprintf("Action: %s, Args: %v - Exit error: %v", payload.Action, payload.Args, err), "error")
		fmt.Fprintf(w, "data: Process finished with Error: %v\n\n", err)
	} else {
		logAuditEvent(getClientIP(r), "execute-command", fmt.Sprintf("Action: %s, Args: %v", payload.Action, payload.Args), "success")
		fmt.Fprintf(w, "data: Process finished successfully!\n\n")
		if payload.Action == "site-backup" && len(payload.Args) > 0 {
			updateLastBackupTime(payload.Args[0])
		}
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
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		w.Write(data)
	}
}

var (
	domainRegex    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*[a-zA-Z0-9]$`)
	timestampRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
)

func isValidDomain(domain string) bool {
	if domain == "localhost" {
		return true
	}
	return domainRegex.MatchString(domain) && !strings.Contains(domain, "..")
}

func isValidTimestamp(ts string) bool {
	return timestampRegex.MatchString(ts) && !strings.Contains(ts, "..")
}

func isValidService(svc string) bool {
	allowed := map[string]bool{
		"caddy":        true,
		"mariadb":      true,
		"redis-server": true,
		"php-fpm":      true,
		"php8.1-fpm":   true,
		"php8.2-fpm":   true,
		"php8.3-fpm":   true,
	}
	return allowed[svc]
}

func isValidTool(tool string) bool {
	allowed := map[string]bool{
		"phpmyadmin":      true,
		"fix-phpmyadmin": true,
	}
	return allowed[tool]
}

func isValidImportPath(path string, domain string, targetType string) bool {
	if path == "" {
		return true
	}
	importDir, err1 := filepath.Abs(filepath.Join(os.TempDir(), "agilepanel_imports"))
	cleaned, err2 := filepath.Abs(path)
	if err1 != nil || err2 != nil {
		return false
	}
	prefix := importDir + string(filepath.Separator)
	if !strings.HasPrefix(cleaned, prefix) {
		return false
	}
	base := filepath.Base(cleaned)
	if targetType == "files" {
		return base == fmt.Sprintf("import_%s_files.zip", domain)
	} else if targetType == "db" {
		return base == fmt.Sprintf("import_%s_db.sql", domain) || base == fmt.Sprintf("import_%s_db.zip", domain)
	}
	return false
}

func validateFilePath(domain string, path string) (string, error) {
	if domain != "" && !isValidDomain(domain) {
		return "", fmt.Errorf("access denied: invalid domain")
	}

	var rootDir string
	if runtime.GOOS == "windows" {
		rootDir = "./var/www"
	} else {
		rootDir = "/var/www"
	}
	absRootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve root directory: %w", err)
	}

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
					absPhpConf, err := filepath.Abs(phpConf)
					if err != nil {
						return "", fmt.Errorf("invalid path: %w", err)
					}
					return absPhpConf, nil
				}
			}
		}
	}

	if slashedPath == "conf/caddy.conf" {
		var caddyFile string
		if runtime.GOOS == "windows" {
			caddyFile = filepath.Clean("./etc/caddy/Caddyfile")
		} else {
			caddyFile = "/etc/caddy/Caddyfile"
		}
		absCaddyFile, err := filepath.Abs(caddyFile)
		if err != nil {
			return "", fmt.Errorf("invalid path: %w", err)
		}
		return absCaddyFile, nil
	}

	baseDir := rootDir
	if domain != "" {
		baseDir = filepath.Join(rootDir, domain)
	}
	absBaseDir, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve base directory: %w", err)
	}

	var fullPath string
	if filepath.IsAbs(path) {
		fullPath = filepath.Clean(path)
	} else {
		fullPath = filepath.Clean(filepath.Join(absBaseDir, path))
	}

	absFullPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve full path: %w", err)
	}

	// Whitelist check: absolute path must resolve within allowedRootDir
	if absFullPath != absRootDir {
		prefixRoot := absRootDir + string(filepath.Separator)
		if !strings.HasPrefix(absFullPath, prefixRoot) {
			return "", fmt.Errorf("access denied: path is outside the allowed root directory")
		}
	}

	// If domain is specified, absolute path must resolve within domain's baseDir
	if domain != "" {
		if absFullPath != absBaseDir {
			prefixBase := absBaseDir + string(filepath.Separator)
			if !strings.HasPrefix(absFullPath, prefixBase) {
				return "", fmt.Errorf("access denied: path is outside the domain directory")
			}
		}
	}

	return absFullPath, nil
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

	err = os.WriteFile(fullPath, []byte(payload.Content), 0640)
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

	ip := getClientIP(r)

	err := r.ParseMultipartForm(32 << 20)
	if err != nil {
		logAuditEvent(ip, "file-upload", "Failed to parse form", "error")
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

	safeFilename := filepath.Base(header.Filename)
	if safeFilename == "." || safeFilename == "/" || strings.Contains(header.Filename, "..") || strings.Contains(safeFilename, "..") {
		logAuditEvent(ip, "file-upload", fmt.Sprintf("Invalid filename attempt: %s", header.Filename), "denied")
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}
	targetPath := filepath.Join(relPath, safeFilename)
	fullPath, err := validateFilePath(domain, targetPath)
	if err != nil {
		logAuditEvent(ip, "file-upload", fmt.Sprintf("Path validation failed: %v", err), "denied")
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	// Restrict dangerous extensions (e.g. php files)
	ext := strings.ToLower(filepath.Ext(safeFilename))
	if ext == ".php" || ext == ".phtml" || ext == ".php3" || ext == ".php4" || ext == ".php5" || ext == ".phps" {
		logAuditEvent(ip, "file-upload", fmt.Sprintf("Blocked php execution script upload: %s", safeFilename), "denied")
		http.Error(w, "Access denied: PHP files cannot be uploaded directly via file manager.", http.StatusForbidden)
		return
	}

	// Validate content type / MIME
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	file.Seek(0, io.SeekStart)
	mime := http.DetectContentType(buf[:n])
	if strings.Contains(mime, "x-php") || strings.Contains(mime, "application/x-httpd-php") {
		logAuditEvent(ip, "file-upload", fmt.Sprintf("Blocked php mime detection: %s", safeFilename), "denied")
		http.Error(w, "Access denied: Malicious script content detected.", http.StatusForbidden)
		return
	}

	out, err := os.Create(fullPath)
	if err != nil {
		logAuditEvent(ip, "file-upload", fmt.Sprintf("Create destination failed: %v", err), "error")
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}
	defer out.Close()

	_, err = io.Copy(out, file)
	if err != nil {
		logAuditEvent(ip, "file-upload", fmt.Sprintf("Save file failed: %v", err), "error")
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	// Virus scan the saved file
	if err := scanFileForViruses(fullPath); err != nil {
		os.Remove(fullPath)
		logAuditEvent(ip, "file-upload", fmt.Sprintf("Virus scan block: %s (%v)", safeFilename, err), "blocked")
		http.Error(w, "Security threat detected: File upload blocked", http.StatusForbidden)
		return
	}

	logAuditEvent(ip, "file-upload", fmt.Sprintf("Successfully uploaded %s to %s", safeFilename, domain), "success")
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

	cleanTarget := filepath.Clean(target)
	prefix := cleanTarget + string(filepath.Separator)

	for _, file := range reader.File {
		filePath := filepath.Join(target, file.Name)
		cleanFilePath := filepath.Clean(filePath)

		if cleanFilePath != cleanTarget && !strings.HasPrefix(cleanFilePath, prefix) {
			return fmt.Errorf("illegal file path inside zip: %s", file.Name)
		}

		if file.FileInfo().IsDir() {
			os.MkdirAll(cleanFilePath, os.ModePerm)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(cleanFilePath), os.ModePerm); err != nil {
			return err
		}

		dstFile, err := os.OpenFile(cleanFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
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

func handleS3SettingsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		state, err := readState()
		if err != nil {
			http.Error(w, "Failed to read state", http.StatusInternalServerError)
			return
		}
		resp := map[string]string{
			"s3_endpoint":   state.Global.S3Endpoint,
			"s3_region":     state.Global.S3Region,
			"s3_bucket":     state.Global.S3Bucket,
			"s3_access_key": state.Global.S3AccessKey,
			"s3_secret_key": state.Global.S3SecretKey,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	if r.Method == http.MethodPost {
		var payload struct {
			Endpoint  string `json:"s3_endpoint"`
			Region    string `json:"s3_region"`
			Bucket    string `json:"s3_bucket"`
			AccessKey string `json:"s3_access_key"`
			SecretKey string `json:"s3_secret_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		state, err := readState()
		if err != nil {
			http.Error(w, "Failed to read state", http.StatusInternalServerError)
			return
		}

		state.Global.S3Endpoint = payload.Endpoint
		state.Global.S3Region = payload.Region
		state.Global.S3Bucket = payload.Bucket
		state.Global.S3AccessKey = payload.AccessKey
		state.Global.S3SecretKey = payload.SecretKey

		if err := writeState(state); err != nil {
			logAuditEvent(getClientIP(r), "update-s3-settings", "Failed to save settings", "error")
			http.Error(w, "Failed to save settings", http.StatusInternalServerError)
			return
		}

		logAuditEvent(getClientIP(r), "update-s3-settings", fmt.Sprintf("Bucket: %s, Endpoint: %s", payload.Bucket, payload.Endpoint), "success")
		w.Write([]byte("S3 settings saved successfully"))
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleSitesS3BackupsAPI(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		http.Error(w, "Domain parameter required", http.StatusBadRequest)
		return
	}

	if !isValidDomain(domain) {
		http.Error(w, "Invalid domain format", http.StatusBadRequest)
		return
	}

	apBin := "ap"
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/usr/local/bin/ap"); err == nil {
			apBin = "/usr/local/bin/ap"
		}
	} else if runtime.GOOS == "windows" {
		apBin = "../agilepanel/ap.exe"
	}

	cmd := exec.Command(apBin, "site", "s3-list", domain, "--json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to list S3 backups: %v (details: %s)", err, stderr.String()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(stdout.Bytes())
}

func handleSitesLocalBackupsAPI(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		http.Error(w, "Domain parameter required", http.StatusBadRequest)
		return
	}

	if !isValidDomain(domain) {
		http.Error(w, "Invalid domain format", http.StatusBadRequest)
		return
	}

	var parentDir string
	if runtime.GOOS == "windows" {
		parentDir = filepath.Clean(filepath.Join("./var/www", domain))
	} else {
		parentDir = filepath.Clean(filepath.Join("/var/www", domain))
	}
	backupDir := filepath.Join(parentDir, "backup")

	var timestamps []string
	entries, err := os.ReadDir(backupDir)
	if err == nil {
		tsMap := make(map[string]bool)
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			filesPrefix := fmt.Sprintf("%s-files-", domain)
			if strings.HasPrefix(name, filesPrefix) && strings.HasSuffix(name, ".zip") {
				ts := strings.TrimSuffix(strings.TrimPrefix(name, filesPrefix), ".zip")
				if len(ts) >= 13 && strings.Contains(ts, "-") {
					tsMap[ts] = true
				}
			}
		}
		for ts := range tsMap {
			timestamps = append(timestamps, ts)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(timestamps)))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(timestamps)
}

func handleSitesS3DeleteAPI(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	timestamp := r.URL.Query().Get("timestamp")
	if !isValidDomain(domain) || !isValidTimestamp(timestamp) {
		http.Error(w, "Invalid domain or timestamp format", http.StatusBadRequest)
		return
	}

	apBin := "ap"
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/usr/local/bin/ap"); err == nil {
			apBin = "/usr/local/bin/ap"
		}
	} else if runtime.GOOS == "windows" {
		apBin = "../agilepanel/ap.exe"
	}

	cmd := exec.Command(apBin, "site", "s3-delete", domain, timestamp)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to delete S3 backup version: %v (details: %s)", err, stderr.String()), http.StatusInternalServerError)
		return
	}

	w.Write([]byte("Backup version deleted successfully"))
}

func SendTelegramNotification(token, chatID, message string) error {
	if token == "" || chatID == "" {
		return nil
	}
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload := map[string]interface{}{
		"chat_id":    chatID,
		"text":       message,
		"parse_mode": "HTML",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := http.Post(apiURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram API error: status %d, response: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

type TelegramUpdateResponse struct {
	Ok     bool             `json:"ok"`
	Result []TelegramUpdate `json:"result"`
}

type TelegramUpdate struct {
	UpdateID int             `json:"update_id"`
	Message  *TelegramMessage `json:"message,omitempty"`
}

type TelegramMessage struct {
	MessageID int          `json:"message_id"`
	From      TelegramUser `json:"from"`
	Chat      TelegramChat `json:"chat"`
	Text      string       `json:"text"`
}

type TelegramUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
}

type TelegramChat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

func startTelegramBotListener() {
	go func() {
		offset := 0
		client := &http.Client{Timeout: 35 * time.Second}
		
		for {
			state, err := readState()
			if err != nil || state.Global.TelegramBotToken == "" || state.Global.TelegramChatID == "" {
				time.Sleep(10 * time.Second)
				continue
			}

			token := state.Global.TelegramBotToken
			chatIDStr := state.Global.TelegramChatID

			apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?timeout=30&offset=%d", token, offset)
			resp, err := client.Get(apiURL)
			if err != nil {
				time.Sleep(10 * time.Second)
				continue
			}

			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				time.Sleep(10 * time.Second)
				continue
			}

			var updateResp TelegramUpdateResponse
			err = json.NewDecoder(resp.Body).Decode(&updateResp)
			resp.Body.Close()

			if err != nil || !updateResp.Ok {
				time.Sleep(10 * time.Second)
				continue
			}

			for _, update := range updateResp.Result {
				if update.UpdateID >= offset {
					offset = update.UpdateID + 1
				}

				if update.Message == nil || update.Message.Text == "" {
					continue
				}

				senderID := update.Message.Chat.ID
				senderIDStr := fmt.Sprintf("%d", senderID)

				if strings.TrimSpace(senderIDStr) != strings.TrimSpace(chatIDStr) {
					continue
				}

				text := strings.TrimSpace(strings.ToLower(update.Message.Text))
				var reply string

				if text == "hi" || text == "/start" || text == "/help" || text == "hello" {
					host, _ := os.Hostname()
					reply = fmt.Sprintf("👋 <b>Hello %s!</b>\n\nI am your <b>AgilePanel Bot</b>. I monitor your VPS and AgilePanel dashboard.\n\n💻 <b>Host:</b> %s\n📈 <b>CPU:</b> %.1f%%\n🧠 <b>RAM:</b> %.1f%% used (%.2f/%.2f GB)\n💾 <b>Disk:</b> %.1f%% used (%.2f/%.2f GB)\n⏱️ <b>Uptime:</b> %s\n\n💬 <b>Available Commands:</b>\n• /status - Get complete services and performance metrics\n• /sites - List all provisioned websites\n• /help - Display this help card",
						update.Message.From.FirstName, host, getCPU(), getRAM().Pct, getRAM().Used, getRAM().Total, getDisk().Pct, getDisk().Used, getDisk().Total, getUptime())
				} else if text == "/status" || text == "status" {
					host, _ := os.Hostname()
					services := map[string]string{
						"Caddy Web Server": getServiceStatusText("caddy"),
						"MariaDB Database": getServiceStatusText("mariadb"),
						"Redis Cache":      getServiceStatusText("redis-server"),
					}
					
					phpSvcMap := make(map[string]bool)
					for _, site := range state.Sites {
						if site.PHPVersion != "" {
							phpSvcMap[site.PHPVersion] = true
						}
					}
					for ver := range phpSvcMap {
						services["PHP "+ver+"-FPM"] = getServiceStatusText("php" + ver + "-fpm")
					}

					var servicesReport strings.Builder
					for svcName, svcStatus := range services {
						servicesReport.WriteString(fmt.Sprintf("• %s: %s\n", svcName, svcStatus))
					}

					history := loadOrCreateHistory()
					var avgLoad24h float64
					var count int
					startIdx := len(history) - 12
					if startIdx < 0 {
						startIdx = 0
					}
					var sum float64
					for _, p := range history[startIdx:] {
						sum += p.Load
						count++
					}
					if count > 0 {
						avgLoad24h = sum / float64(count)
					}

					reply = fmt.Sprintf("📊 <b>VPS System Health Status</b>\n\n<b>Host:</b> %s\n⏱️ <b>Uptime:</b> %s\n📈 <b>CPU Usage:</b> %.1f%%\n🧠 <b>RAM Usage:</b> %.1f%% (%.2f / %.2f GB)\n💾 <b>Disk Usage:</b> %.1f%% (%.2f / %.2f GB)\n⏱️ <b>24h Load Avg:</b> %.2f\n\n⚙️ <b>Daemon Services:</b>\n%s\n🌐 <b>Active Sites:</b> %d",
						host, getUptime(), getCPU(), getRAM().Pct, getRAM().Used, getRAM().Total, getDisk().Pct, getDisk().Used, getDisk().Total, avgLoad24h, servicesReport.String(), len(state.Sites))
				} else if text == "/sites" || text == "sites" {
					if len(state.Sites) == 0 {
						reply = "🌐 <b>Active Sites:</b> None provisioned yet."
					} else {
						var sb strings.Builder
						sb.WriteString("🌐 <b>Active Websites List:</b>\n\n")
						for i, site := range state.Sites {
							framework := site.Type
							if framework == "" {
								framework = "wordpress"
							}
							sb.WriteString(fmt.Sprintf("%d. <b>%s</b> (%s, PHP %s)\n", i+1, site.Domain, strings.ToUpper(framework), site.PHPVersion))
						}
						reply = sb.String()
					}
				} else {
					reply = "🤷‍♂️ <b>Unknown command.</b>\n\nType <b>Hi</b> or <b>/help</b> to see available commands."
				}

				if reply != "" {
					_ = SendTelegramNotification(token, chatIDStr, reply)
				}
			}
		}
	}()
}

func getServiceStatusText(service string) string {
	if getServiceStatus(service) {
		return "🟢 Running"
	}
	return "🔴 Stopped"
}


func handleTelegramSettingsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		state, err := readState()
		if err != nil {
			http.Error(w, "Failed to read state", http.StatusInternalServerError)
			return
		}
		resp := map[string]string{
			"telegram_bot_token": state.Global.TelegramBotToken,
			"telegram_chat_id":   state.Global.TelegramChatID,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	if r.Method == http.MethodPost {
		var payload struct {
			BotToken string `json:"telegram_bot_token"`
			ChatID   string `json:"telegram_chat_id"`
			IsTest   bool   `json:"is_test"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		state, err := readState()
		if err != nil {
			http.Error(w, "Failed to read state", http.StatusInternalServerError)
			return
		}

		if payload.IsTest {
			err := SendTelegramNotification(payload.BotToken, payload.ChatID, "🔔 <b>AgilePanel Telegram Notification Test</b>\n\nYour Telegram integration is working perfectly!")
			if err != nil {
				http.Error(w, fmt.Sprintf("Telegram test notification failed: %v", err), http.StatusBadRequest)
				return
			}
			w.Write([]byte("Test notification sent successfully"))
			return
		}

		state.Global.TelegramBotToken = payload.BotToken
		state.Global.TelegramChatID = payload.ChatID

		if err := writeState(state); err != nil {
			logAuditEvent(getClientIP(r), "update-telegram-settings", "Failed to save settings", "error")
			http.Error(w, "Failed to save settings", http.StatusInternalServerError)
			return
		}

		logAuditEvent(getClientIP(r), "update-telegram-settings", fmt.Sprintf("ChatID: %s", payload.ChatID), "success")
		w.Write([]byte("Telegram settings saved successfully"))
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleSitesUploadImportAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ip := getClientIP(r)

	err := r.ParseMultipartForm(100 << 20)
	if err != nil {
		logAuditEvent(ip, "site-upload-import", "Failed to parse form", "error")
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	domain := r.FormValue("domain")
	targetType := r.FormValue("type")
	if domain == "" || targetType == "" {
		http.Error(w, "Domain and type parameters required", http.StatusBadRequest)
		return
	}

	if !isValidDomain(domain) {
		http.Error(w, "Invalid domain format", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File parameter required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	importDir := filepath.Join(os.TempDir(), "agilepanel_imports")
	_ = os.MkdirAll(importDir, 0755)

	ext := strings.ToLower(filepath.Ext(header.Filename))
	var targetPath string
	if targetType == "files" {
		if ext != ".zip" {
			logAuditEvent(ip, "site-upload-import", fmt.Sprintf("Invalid extension upload attempt: %s", header.Filename), "denied")
			http.Error(w, "Invalid file type. Only .zip is allowed for website files.", http.StatusBadRequest)
			return
		}
		targetPath = filepath.Join(importDir, fmt.Sprintf("import_%s_files.zip", domain))
	} else if targetType == "db" {
		if ext != ".sql" && ext != ".zip" {
			logAuditEvent(ip, "site-upload-import", fmt.Sprintf("Invalid extension upload attempt: %s", header.Filename), "denied")
			http.Error(w, "Invalid file type. Only .sql and .zip are allowed for database.", http.StatusBadRequest)
			return
		}
		targetPath = filepath.Join(importDir, fmt.Sprintf("import_%s_db%s", domain, ext))
	} else {
		http.Error(w, "Invalid target type parameter", http.StatusBadRequest)
		return
	}

	// Validate content type / MIME
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	file.Seek(0, io.SeekStart)
	mime := http.DetectContentType(buf[:n])
	if strings.Contains(mime, "x-php") || strings.Contains(mime, "application/x-httpd-php") {
		logAuditEvent(ip, "site-upload-import", fmt.Sprintf("Blocked PHP mime detection in import: %s", header.Filename), "denied")
		http.Error(w, "Access denied: Malicious script content detected.", http.StatusForbidden)
		return
	}

	out, err := os.Create(targetPath)
	if err != nil {
		logAuditEvent(ip, "site-upload-import", fmt.Sprintf("Create staging file failed: %v", err), "error")
		http.Error(w, "Internal server error: Failed to save uploaded files.", http.StatusInternalServerError)
		return
	}
	defer out.Close()

	_, err = io.Copy(out, file)
	if err != nil {
		logAuditEvent(ip, "site-upload-import", fmt.Sprintf("Save staging file failed: %v", err), "error")
		http.Error(w, "Internal server error: Failed to save uploaded files.", http.StatusInternalServerError)
		return
	}

	// Virus scan the staging file
	if err := scanFileForViruses(targetPath); err != nil {
		os.Remove(targetPath)
		logAuditEvent(ip, "site-upload-import", fmt.Sprintf("Virus scan block in import: %s (%v)", header.Filename, err), "blocked")
		http.Error(w, "Security threat detected: File upload blocked", http.StatusForbidden)
		return
	}

	logAuditEvent(ip, "site-upload-import", fmt.Sprintf("Successfully staged import for %s", domain), "success")
	w.Write([]byte(targetPath))
}


func handleSitesS3RestoreAPI(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	timestamp := r.URL.Query().Get("timestamp")
	if domain == "" || timestamp == "" {
		http.Error(w, "Domain and timestamp parameters required", http.StatusBadRequest)
		return
	}

	if !isValidDomain(domain) || !isValidTimestamp(timestamp) {
		http.Error(w, "Invalid domain or timestamp format", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	fmt.Fprintf(w, "data: Initiating S3 Backup restore for website: %s (timestamp: %s)...\n\n", domain, timestamp)
	flusher.Flush()

	apBin := "ap"
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/usr/local/bin/ap"); err == nil {
			apBin = "/usr/local/bin/ap"
		}
	} else if runtime.GOOS == "windows" {
		apBin = "../agilepanel/ap.exe"
	}

	fmt.Fprintf(w, "data: Step 1: Downloading S3 cloud archives...\n\n")
	flusher.Flush()

	cmd := exec.Command(apBin, "site", "s3-download", domain, timestamp)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(w, "data: ERR: Error piping stdout: %v\n\n", err)
		flusher.Flush()
		return
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		fmt.Fprintf(w, "data: ERR: Error piping stderr: %v\n\n", err)
		flusher.Flush()
		return
	}

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(w, "data: ERR: Startup Download Execution Error: %v\n\n", err)
		flusher.Flush()
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)

	logScanner := func(rd io.Reader, prefix string) {
		defer wg.Done()
		scanner := bufio.NewScanner(rd)
		for scanner.Scan() {
			line := scanner.Text()
			cleaned := stripANSI(line)
			fmt.Fprintf(w, "data: %s%s\n\n", prefix, cleaned)
			flusher.Flush()
		}
	}

	go logScanner(stdoutPipe, "")
	go logScanner(stderrPipe, "ERR: ")

	wg.Wait()

	if err := cmd.Wait(); err != nil {
		fmt.Fprintf(w, "data: ERR: S3 Download failed: %v\n\n", err)
		flusher.Flush()
		return
	}

	fmt.Fprintf(w, "data: Step 2: Extracting files and database to site root...\n\n")
	flusher.Flush()

	handleSiteRestoreAPI(w, r, domain, "")
}

func handleSitesToggleStagingUnlockAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload struct {
		Domain   string `json:"domain"`
		Unlocked bool   `json:"unlocked"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if !isValidDomain(payload.Domain) {
		http.Error(w, "Invalid domain format", http.StatusBadRequest)
		return
	}

	state, err := readState()
	if err != nil {
		http.Error(w, "Failed to read state", http.StatusInternalServerError)
		return
	}

	found := false
	for i, s := range state.Sites {
		if strings.EqualFold(s.Domain, payload.Domain) {
			state.Sites[i].StagingUnlocked = payload.Unlocked
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "Site not found", http.StatusNotFound)
		return
	}

	if err := writeState(state); err != nil {
		logAuditEvent(getClientIP(r), "toggle-staging-unlock", fmt.Sprintf("Domain: %s - Failed to write state", payload.Domain), "error")
		http.Error(w, "Failed to write state", http.StatusInternalServerError)
		return
	}

	apBin := "ap"
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/usr/local/bin/ap"); err == nil {
			apBin = "/usr/local/bin/ap"
		}
	} else if runtime.GOOS == "windows" {
		apBin = "../agilepanel/ap.exe"
	}

	cmd := exec.Command(apBin, "sync")
	if err := cmd.Run(); err != nil {
		log.Printf("Warning: ap sync failed: %v", err)
	}

	logAuditEvent(getClientIP(r), "toggle-staging-unlock", fmt.Sprintf("Domain: %s, Unlocked: %v", payload.Domain, payload.Unlocked), "success")
	w.Write([]byte("Staging unlock state updated successfully"))
}

func handleSitesUpdateBackupIntervalAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload struct {
		Domain   string `json:"domain"`
		Interval string `json:"interval"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if !isValidDomain(payload.Domain) {
		http.Error(w, "Invalid domain format", http.StatusBadRequest)
		return
	}

	state, err := readState()
	if err != nil {
		http.Error(w, "Failed to read state", http.StatusInternalServerError)
		return
	}

	found := false
	for i, s := range state.Sites {
		if strings.EqualFold(s.Domain, payload.Domain) {
			state.Sites[i].BackupInterval = payload.Interval
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "Site not found", http.StatusNotFound)
		return
	}

	if err := writeState(state); err != nil {
		logAuditEvent(getClientIP(r), "update-backup-interval", fmt.Sprintf("Domain: %s - Failed to write state", payload.Domain), "error")
		http.Error(w, "Failed to write state", http.StatusInternalServerError)
		return
	}

	logAuditEvent(getClientIP(r), "update-backup-interval", fmt.Sprintf("Domain: %s, Interval: %s", payload.Domain, payload.Interval), "success")
	w.Write([]byte("Backup interval updated successfully"))
}

func handleSitesUpdateBackupDestinationAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload struct {
		Domain      string `json:"domain"`
		Destination string `json:"destination"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if !isValidDomain(payload.Domain) {
		http.Error(w, "Invalid domain format", http.StatusBadRequest)
		return
	}

	if payload.Destination != "local" && payload.Destination != "s3" {
		http.Error(w, "Invalid destination: must be 'local' or 's3'", http.StatusBadRequest)
		return
	}

	state, err := readState()
	if err != nil {
		http.Error(w, "Failed to read state", http.StatusInternalServerError)
		return
	}

	found := false
	for i, s := range state.Sites {
		if strings.EqualFold(s.Domain, payload.Domain) {
			state.Sites[i].BackupDestination = payload.Destination
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "Site not found", http.StatusNotFound)
		return
	}

	if err := writeState(state); err != nil {
		logAuditEvent(getClientIP(r), "update-backup-destination", fmt.Sprintf("Domain: %s - Failed to write state", payload.Domain), "error")
		http.Error(w, "Failed to write state", http.StatusInternalServerError)
		return
	}

	logAuditEvent(getClientIP(r), "update-backup-destination", fmt.Sprintf("Domain: %s, Destination: %s", payload.Domain, payload.Destination), "success")
	w.Write([]byte("Backup destination updated successfully"))
}

func handleSitesUpdateS3BackupVersionsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload struct {
		Domain   string `json:"domain"`
		Versions int    `json:"versions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if !isValidDomain(payload.Domain) {
		http.Error(w, "Invalid domain format", http.StatusBadRequest)
		return
	}

	if payload.Versions < 1 || payload.Versions > 7 {
		http.Error(w, "S3 backup version count must be between 1 and 7", http.StatusBadRequest)
		return
	}

	state, err := readState()
	if err != nil {
		http.Error(w, "Failed to read state", http.StatusInternalServerError)
		return
	}

	found := false
	for i, s := range state.Sites {
		if strings.EqualFold(s.Domain, payload.Domain) {
			state.Sites[i].S3BackupVersions = payload.Versions
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "Site not found", http.StatusNotFound)
		return
	}

	if err := writeState(state); err != nil {
		logAuditEvent(getClientIP(r), "update-s3-backup-versions", fmt.Sprintf("Domain: %s - Failed to write state", payload.Domain), "error")
		http.Error(w, "Failed to write state", http.StatusInternalServerError)
		return
	}

	logAuditEvent(getClientIP(r), "update-s3-backup-versions", fmt.Sprintf("Domain: %s, Versions: %d", payload.Domain, payload.Versions), "success")
	w.Write([]byte("S3 backup versions updated successfully"))
}

func handleSitesUpdateDbCredentialsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload struct {
		Domain string `json:"domain"`
		User   string `json:"db_user"`
		Pass   string `json:"db_pass"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if !isValidDomain(payload.Domain) {
		http.Error(w, "Invalid domain format", http.StatusBadRequest)
		return
	}

	state, err := readState()
	if err != nil {
		http.Error(w, "Failed to read state", http.StatusInternalServerError)
		return
	}

	found := false
	for i, s := range state.Sites {
		if strings.EqualFold(s.Domain, payload.Domain) {
			state.Sites[i].DatabaseUser = payload.User
			state.Sites[i].DatabasePass = payload.Pass
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "Site not found", http.StatusNotFound)
		return
	}

	if err := writeState(state); err != nil {
		logAuditEvent(getClientIP(r), "update-db-credentials", fmt.Sprintf("Domain: %s - Failed to write state", payload.Domain), "error")
		http.Error(w, "Failed to write state", http.StatusInternalServerError)
		return
	}

	logAuditEvent(getClientIP(r), "update-db-credentials", fmt.Sprintf("Domain: %s, User: %s", payload.Domain, payload.User), "success")
	w.Write([]byte("Database credentials updated successfully"))
}

func handleSitesToggleS3EnabledAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload struct {
		Domain  string `json:"domain"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if !isValidDomain(payload.Domain) {
		http.Error(w, "Invalid domain format", http.StatusBadRequest)
		return
	}

	state, err := readState()
	if err != nil {
		http.Error(w, "Failed to read state", http.StatusInternalServerError)
		return
	}

	found := false
	for i, s := range state.Sites {
		if strings.EqualFold(s.Domain, payload.Domain) {
			state.Sites[i].S3Enabled = payload.Enabled
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "Site not found", http.StatusNotFound)
		return
	}

	if err := writeState(state); err != nil {
		logAuditEvent(getClientIP(r), "toggle-s3-enabled", fmt.Sprintf("Domain: %s - Failed to write state", payload.Domain), "error")
		http.Error(w, "Failed to write state", http.StatusInternalServerError)
		return
	}

	logAuditEvent(getClientIP(r), "toggle-s3-enabled", fmt.Sprintf("Domain: %s, Enabled: %v", payload.Domain, payload.Enabled), "success")
	w.Write([]byte("Site S3 enabled flag toggled successfully"))
}



func checkAndTriggerScheduledBackups() {
	state, err := readState()
	if err != nil {
		return
	}

	apBin := "ap"
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/usr/local/bin/ap"); err == nil {
			apBin = "/usr/local/bin/ap"
		}
	} else if runtime.GOOS == "windows" {
		apBin = "../agilepanel/ap.exe"
	}

	for _, s := range state.Sites {
		if s.BackupInterval == "" || s.BackupInterval == "none" {
			continue
		}

		shouldBackup := false
		if s.LastBackupTime.IsZero() {
			shouldBackup = true
		} else {
			since := time.Since(s.LastBackupTime)
			switch s.BackupInterval {
			case "hourly":
				if since >= 1*time.Hour-5*time.Minute {
					shouldBackup = true
				}
			case "daily":
				if since >= 24*time.Hour-15*time.Minute {
					shouldBackup = true
				}
			case "twice-weekly":
				if since >= 84*time.Hour-30*time.Minute {
					shouldBackup = true
				}
			case "weekly":
				if since >= 7*24*time.Hour-30*time.Minute {
					shouldBackup = true
				}
			case "monthly":
				if since >= 30*24*time.Hour-60*time.Minute {
					shouldBackup = true
				}
			}
		}

		if shouldBackup {
			log.Printf("Scheduler: triggering backup for %s (interval: %s)", s.Domain, s.BackupInterval)
			cmd := exec.Command(apBin, "site", "backup", s.Domain)
			if err := cmd.Run(); err != nil {
				log.Printf("Scheduler Error: Backup command failed for site %s: %v", s.Domain, err)
				if state.Global.TelegramBotToken != "" && state.Global.TelegramChatID != "" {
					host, _ := os.Hostname()
					msg := fmt.Sprintf("🚨 <b>AgilePanel Alert: Backup Failed!</b>\n\n<b>Host:</b> %s\n<b>Site:</b> %s\n<b>Interval:</b> %s\n<b>Error:</b> %v", host, s.Domain, s.BackupInterval, err)
					_ = SendTelegramNotification(state.Global.TelegramBotToken, state.Global.TelegramChatID, msg)
				}
			} else {
				log.Printf("Scheduler: Backup succeeded for site %s", s.Domain)
				updateLastBackupTime(s.Domain)
			}
		}
	}
}

func runAutomatedBackupScheduler() {
	go func() {
		time.Sleep(10 * time.Second)
		checkAndTriggerScheduledBackups()
		
		ticker := time.NewTicker(1 * time.Hour)
		for range ticker.C {
			checkAndTriggerScheduledBackups()
		}
	}()
}

func handleSiteRestoreAPI(w http.ResponseWriter, r *http.Request, domain string, timestamp string) {
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
	var filesZipPath string
	var dbZipPath string
	if timestamp == "" {
		filesZipPath = filepath.Join(backupDir, domain+"-files.zip")
		dbZipPath = filepath.Join(backupDir, domain+"-db.zip")
	} else {
		filesZipPath = filepath.Join(backupDir, fmt.Sprintf("%s-files-%s.zip", domain, timestamp))
		dbZipPath = filepath.Join(backupDir, fmt.Sprintf("%s-db-%s.zip", domain, timestamp))
	}

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
	for i := range state.Sites {
		if strings.EqualFold(state.Sites[i].Domain, domain) {
			targetSite = &state.Sites[i]
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

func startWatchdog() {
	go func() {
		// Wait a bit on startup
		time.Sleep(30 * time.Second)
		ticker := time.NewTicker(5 * time.Minute)
		for range ticker.C {
			// 1. Try to read/parse state.json
			statePath := getStatePath()
			_, err := readState()
			if err != nil {
				log.Printf("Watchdog Alert: Failed to read state.json: %v. Attempting restore from .bak", err)
				bakPath := statePath + ".bak"
				if _, errBak := os.Stat(bakPath); errBak == nil {
					bakData, errRead := os.ReadFile(bakPath)
					if errRead == nil {
						errWrite := os.WriteFile(statePath, bakData, 0660)
						if errWrite == nil {
							log.Println("Watchdog: Successfully restored state.json from backup.")
						} else {
							log.Printf("Watchdog Error: Failed to restore state.json from backup: %v", errWrite)
						}
					}
				}
			}

			// 2. Correct state.json write permissions (0660 or 0600 on unix)
			if runtime.GOOS != "windows" {
				if info, errStat := os.Stat(statePath); errStat == nil {
					if info.Mode() != 0660 && info.Mode() != 0600 {
						_ = os.Chmod(statePath, 0660)
					}
				}
			}

			// 3. Linux only: check and restart dead services
			if runtime.GOOS == "linux" {
				state, errState := readState()
				services := []string{"caddy", "mariadb", "redis-server"}
				
				// Add active PHP-FPM services configured for websites
				if errState == nil {
					phpVersionsSeen := make(map[string]bool)
					for _, site := range state.Sites {
						if site.PHPVersion != "" {
							phpVersionsSeen[site.PHPVersion] = true
						}
					}
					for ver := range phpVersionsSeen {
						services = append(services, "php"+ver+"-fpm")
					}
				}

				for _, svc := range services {
					if !getServiceStatus(svc) {
						log.Printf("Watchdog: Service %s is down! Attempting to restart...", svc)
						restartErr := exec.Command("systemctl", "restart", svc).Run()
						if errState == nil && state.Global.TelegramBotToken != "" && state.Global.TelegramChatID != "" {
							host, _ := os.Hostname()
							var msg string
							if restartErr != nil {
								msg = fmt.Sprintf("🚨 <b>AgilePanel Watchdog Alert!</b>\n\n<b>Host:</b> %s\n<b>Service:</b> <code>%s</code> is <b>DOWN</b> and auto-restart <b>FAILED</b>.\n<b>Error:</b> <code>%v</code>\n\nPlease log in and inspect the service status.", host, svc, restartErr)
							} else {
								msg = fmt.Sprintf("⚠️ <b>AgilePanel Watchdog Self-Healing!</b>\n\n<b>Host:</b> %s\n<b>Service:</b> <code>%s</code> was detected <b>DOWN</b>.\n\n✅ The self-healing watchdog successfully restarted the service and it is now running.", host, svc)
							}
							_ = SendTelegramNotification(state.Global.TelegramBotToken, state.Global.TelegramChatID, msg)
						}
					}
				}
			}
		}
	}()
}

func main() {
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

	log.Println("AgilePanel GUI Dashboard starting on http://localhost:8889...")
	if err := http.ListenAndServe(":8889", nil); err != nil {
		log.Fatalf("Server startup failed: %v", err)
	}
}

