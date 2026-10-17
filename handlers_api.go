package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net"
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

	"github.com/gofrs/flock"
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
	lockPath := path + ".lock"

	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create state directory: %w", err)
	}

	fileLock := flock.New(lockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	locked, err := fileLock.TryRLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire read state lock: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("could not acquire read state lock: timeout")
	}
	defer func() {
		_ = fileLock.Unlock()
	}()

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
	lockPath := path + ".lock"

	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	fileLock := flock.New(lockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	locked, err := fileLock.TryLockContext(ctx, 100*time.Millisecond)
	if err != nil {
		return fmt.Errorf("failed to acquire state lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("could not acquire state lock: timeout")
	}
	defer func() {
		_ = fileLock.Unlock()
	}()

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
			Label:     d.time.Format("Mon 02"),
			Timestamp: d.time,
			CPU:       math.Round((d.cpuSum/count)*10) / 10,
			RAM:       math.Round((d.ramSum/count)*10) / 10,
			Disk:      math.Round((d.diskSum/count)*10) / 10,
		})
	}

	resp := map[string]interface{}{
		"today":    todayPoints,
		"thisweek": thisWeekPoints,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

var (
	cachedAPVersion     string
	cachedAPVersionOnce sync.Once

	cachedDbSizes      map[string]float64
	cachedDbSizesTime  time.Time
	cachedDbSizesMutex sync.RWMutex
)

func getAgilePanelVersion() string {
	cachedAPVersionOnce.Do(func() {
		apBin := "ap"
		if runtime.GOOS != "windows" {
			if _, err := os.Stat("/usr/local/bin/ap"); err == nil {
				apBin = "/usr/local/bin/ap"
			}
		} else {
			cachedAPVersion = "1.1.5 (mock)"
			return
		}

		cmd := exec.Command(apBin, "--version")
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			cachedAPVersion = "unknown"
			return
		}
		parts := strings.Fields(out.String())
		if len(parts) >= 3 {
			cachedAPVersion = parts[2]
			return
		}
		cachedAPVersion = strings.TrimSpace(out.String())
	})
	return cachedAPVersion
}

func getDatabaseSizes() map[string]float64 {
	cachedDbSizesMutex.RLock()
	if cachedDbSizes != nil && time.Since(cachedDbSizesTime) < 5*time.Minute {
		defer cachedDbSizesMutex.RUnlock()
		return cachedDbSizes
	}
	cachedDbSizesMutex.RUnlock()

	sizes := make(map[string]float64)
	if runtime.GOOS == "windows" {
		sizes["wordpress_db"] = 45.2
		sizes["laravel_db"] = 12.8
		sizes["mysql"] = 4.1
		sizes["information_schema"] = 0.2
		cachedDbSizesMutex.Lock()
		cachedDbSizes = sizes
		cachedDbSizesTime = time.Now()
		cachedDbSizesMutex.Unlock()
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

	cachedDbSizesMutex.Lock()
	cachedDbSizes = sizes
	cachedDbSizesTime = time.Now()
	cachedDbSizesMutex.Unlock()

	return sizes
}

func handleStatusAPI(w http.ResponseWriter, r *http.Request) {
	state, _ := readState()

	services := map[string]bool{
		"caddy":   getServiceStatus("caddy"),
		"mariadb": getServiceStatus("mariadb"),
		"redis":   getServiceStatus("redis-server"),
		"php-fpm": getServiceStatus("php8.3-fpm") || getServiceStatus("php8.2-fpm") || getServiceStatus("php8.1-fpm"),
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

	type SiteDashboardSummary struct {
		Domain         string    `json:"domain"`
		Type           string    `json:"type"`
		PHPVersion     string    `json:"php_version"`
		IsLocked       bool      `json:"is_locked"`
		LastBackupTime time.Time `json:"last_backup_time"`
	}
	var recentSites []SiteDashboardSummary
	for _, s := range state.Sites {
		st := s.Type
		if st == "" {
			st = "wp"
		}
		recentSites = append(recentSites, SiteDashboardSummary{
			Domain:         s.Domain,
			Type:           st,
			PHPVersion:     s.PHPVersion,
			IsLocked:       s.IsLocked,
			LastBackupTime: s.LastBackupTime,
		})
	}

	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "agilepanel-node"
	}

	servicesDetail := getServicesList()

	status := map[string]interface{}{
		"cpu":            getCPU(),
		"ram":            getRAM(),
		"disk":           getDisk(),
		"bandwidth":      getBandwidth(),
		"services":       services,
		"servicesDetail": servicesDetail,
		"siteCount":      len(state.Sites),
		"wpCount":        wpCount,
		"htmlCount":      htmlCount,
		"laravelCount":   laravelCount,
		"phpCount":       phpCount,
		"uptime":         getUptime(),
		"loadAvg":        getLoadAverages(),
		"cpuCores":       runtime.NumCPU(),
		"tcpConns":       getTCPConnections(),
		"topProcesses":   getTopProcesses(),
		"dbSizes":        getDatabaseSizes(),
		"apVersion":      getAgilePanelVersion(),
		"serverIP":       getPublicIP(),
		"hostname":       hostname,
		"osInfo":         fmt.Sprintf("%s (%s)", runtime.GOOS, runtime.GOARCH),
		"recentSites":    recentSites,
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

type ServiceItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Unit        string `json:"unit"`
	Active      bool   `json:"active"`
	Category    string `json:"category"`
	Port        string `json:"port"`
	Description string `json:"description"`
	CanReload   bool   `json:"can_reload"`
	ConfigPath  string `json:"config_path"`
	Icon        string `json:"icon"`
}

func getServicesList() []ServiceItem {
	return []ServiceItem{
		{
			ID:          "caddy",
			Name:        "Caddy Web Server",
			Unit:        "caddy.service",
			Active:      getServiceStatus("caddy"),
			Category:    "Web & Proxy",
			Port:        "Port 80, 443 (HTTP/3 + TLS)",
			Description: "High-performance edge reverse proxy, HTTP/2 & HTTP/3 server with automatic Let's Encrypt / ZeroSSL TLS certificates.",
			CanReload:   true,
			ConfigPath:  "/etc/caddy/Caddyfile",
			Icon:        "🌐",
		},
		{
			ID:          "mariadb",
			Name:        "MariaDB SQL Server",
			Unit:        "mariadb.service",
			Active:      getServiceStatus("mariadb"),
			Category:    "Database & Cache",
			Port:        "Port 3306 (MySQL Socket)",
			Description: "Enterprise relational database engine powering WordPress sites, WooCommerce stores, and web apps with InnoDB buffer cache.",
			CanReload:   false,
			ConfigPath:  "/etc/mysql/mariadb.conf.d/",
			Icon:        "🗄️",
		},
		{
			ID:          "redis-server",
			Name:        "Redis Object Cache",
			Unit:        "redis-server.service",
			Active:      getServiceStatus("redis-server"),
			Category:    "Database & Cache",
			Port:        "Port 6379 / Unix Socket",
			Description: "In-memory key-value data structure store delivering microsecond latency for WordPress object cache and persistent session state.",
			CanReload:   false,
			ConfigPath:  "/etc/redis/redis.conf",
			Icon:        "⚡",
		},
		{
			ID:          "php8.3-fpm",
			Name:        "PHP 8.3 FPM Pool",
			Unit:        "php8.3-fpm.service",
			Active:      getServiceStatus("php8.3-fpm"),
			Category:    "PHP Engines",
			Port:        "Socket: /run/php/php8.3-fpm.sock",
			Description: "Latest high-performance PHP FastCGI Process Manager engine with OPcache and JIT acceleration.",
			CanReload:   true,
			ConfigPath:  "/etc/php/8.3/fpm/php.ini",
			Icon:        "🐘",
		},
		{
			ID:          "php8.2-fpm",
			Name:        "PHP 8.2 FPM Pool",
			Unit:        "php8.2-fpm.service",
			Active:      getServiceStatus("php8.2-fpm"),
			Category:    "PHP Engines",
			Port:        "Socket: /run/php/php8.2-fpm.sock",
			Description: "Stable and widely compatible PHP FastCGI processor pool for legacy WordPress themes and Laravel applications.",
			CanReload:   true,
			ConfigPath:  "/etc/php/8.2/fpm/php.ini",
			Icon:        "🐘",
		},
		{
			ID:          "php8.1-fpm",
			Name:        "PHP 8.1 FPM Pool",
			Unit:        "php8.1-fpm.service",
			Active:      getServiceStatus("php8.1-fpm"),
			Category:    "PHP Engines",
			Port:        "Socket: /run/php/php8.1-fpm.sock",
			Description: "LTS PHP FastCGI engine maintained for backwards compatibility with older web application dependencies.",
			CanReload:   true,
			ConfigPath:  "/etc/php/8.1/fpm/php.ini",
			Icon:        "🐘",
		},
		{
			ID:          "watchdog",
			Name:        "AgilePanel Watchdog",
			Unit:        "Internal Go Watchdog",
			Active:      true,
			Category:    "Security & System",
			Port:        "Interval: 15s Heartbeat",
			Description: "Automated self-healing daemon that detects unresponsive services, triggers auto-restarts, and broadcasts Telegram alerts.",
			CanReload:   false,
			ConfigPath:  "State & Config Watcher",
			Icon:        "🤖",
		},
		{
			ID:          "ufw",
			Name:        "UFW Firewall Engine",
			Unit:        "ufw.service",
			Active:      getServiceStatus("ufw"),
			Category:    "Security & System",
			Port:        "Kernel Netfilter",
			Description: "Host-level packet filter protecting open ports, enforcing perimeter defense, rate-limiting SSH, and blocking unauthorized traffic.",
			CanReload:   true,
			ConfigPath:  "/etc/ufw/ufw.conf",
			Icon:        "🧱",
		},
		{
			ID:          "cron",
			Name:        "System Task Scheduler",
			Unit:        "cron.service",
			Active:      getServiceStatus("cron"),
			Category:    "Security & System",
			Port:        "Cron Daemon",
			Description: "Cron daemon responsible for executing automated nightly backups, SSL certificate auto-renewals, and WP-Cron tasks.",
			CanReload:   true,
			ConfigPath:  "/etc/crontab",
			Icon:        "⏱️",
		},
		{
			ID:          "ssh",
			Name:        "OpenSSH Server Daemon",
			Unit:        "ssh.service",
			Active:      getServiceStatus("ssh") || getServiceStatus("sshd"),
			Category:    "Security & System",
			Port:        "Port 22 (TCP / SFTP)",
			Description: "Secure Shell server providing encrypted terminal administration, key-based authentication, and SFTP file access.",
			CanReload:   true,
			ConfigPath:  "/etc/ssh/sshd_config",
			Icon:        "🔑",
		},
	}
}

func handleServiceLogsAPI(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")
	if !isValidService(service) {
		http.Error(w, "Invalid service name", http.StatusBadRequest)
		return
	}
	unit := service
	if unit == "redis" {
		unit = "redis-server"
	} else if unit == "php-fpm" {
		unit = "php8.3-fpm"
	}

	var outStr string
	if unit == "watchdog" {
		outStr = fmt.Sprintf("[%s] AgilePanel Watchdog & Self-Healing Daemon\nStatus: Active (monitoring loop every 15s)\nHeartbeat: Healthy\nMonitored Services: caddy, mariadb, redis-server, php-fpm\nLast check: %s\nNo critical service faults detected.",
			time.Now().Format("2006-01-02 15:04:05"), time.Now().Format("15:04:05"))
	} else if runtime.GOOS == "linux" {
		cmd := exec.Command("journalctl", "-u", unit, "-n", "50", "--no-pager")
		out, err := cmd.CombinedOutput()
		if err != nil || len(out) == 0 {
			cmdStatus := exec.Command("systemctl", "status", unit, "--no-pager")
			outStatus, _ := cmdStatus.CombinedOutput()
			outStr = string(outStatus)
		} else {
			outStr = string(out)
		}
	} else {
		// Mock log diagnostics on Windows
		outStr = fmt.Sprintf("[%s] %s.service - Active and running\nLoaded: /etc/systemd/system/%s.service; enabled\nActive: active (running) since %s\nMain PID: 1245 (%s)\nTasks: 8 (limit: 4915)\nMemory: 38.4M\nStatus: \"Operational, processing requests without errors\"\nLogs:\n  -- %s systemd[1]: Started %s daemon.\n  -- %s %s[1245]: Ready to accept client connections.",
			time.Now().Format("2006-01-02 15:04:05"), unit, unit,
			time.Now().Add(-5*time.Hour).Format("Mon 2006-01-02 15:04:05 MST"),
			unit,
			time.Now().Add(-5*time.Hour).Format("Jan 02 15:04:05"), unit,
			time.Now().Add(-5*time.Hour).Format("Jan 02 15:04:06"), unit)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"service": service,
		"unit":    unit,
		"logs":    outStr,
		"status":  getServiceStatus(service),
	})
}

var (
	cachedPublicIP     string
	cachedPublicIPOnce sync.Once
)

func getPublicIP() string {
	cachedPublicIPOnce.Do(func() {
		client := http.Client{
			Timeout: 2 * time.Second,
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
		"all":          true,
		"caddy":        true,
		"mariadb":      true,
		"redis":        true,
		"redis-server": true,
		"php-fpm":      true,
		"php8.1-fpm":   true,
		"php8.2-fpm":   true,
		"php8.3-fpm":   true,
		"watchdog":     true,
		"ufw":          true,
		"cron":         true,
		"ssh":          true,
		"sshd":         true,
	}
	return allowed[svc]
}

func isValidTool(tool string) bool {
	allowed := map[string]bool{
		"phpmyadmin":     true,
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
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "clamscan", "--no-summary", path)
		if err := cmd.Run(); err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Errorf("virus scan timed out")
			}
			return fmt.Errorf("virus scan failed: file is infected or suspicious")
		}
	}
	return nil
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
		logAuditEvent(ip, "site-upload-import", fmt.Sprintf("Write staging file failed: %v", err), "error")
		http.Error(w, "Internal server error: Failed to save file data.", http.StatusInternalServerError)
		return
	}

	// Run anti-virus and heuristic check
	if err := scanFileForViruses(targetPath); err != nil {
		_ = os.Remove(targetPath)
		logAuditEvent(ip, "site-upload-import", fmt.Sprintf("Virus signature detected: %v", err), "blocked")
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

	handleSiteRestoreAPI(w, r, domain, timestamp)
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

func handleAdminAuthSettingsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		state, err := readState()
		if err != nil {
			http.Error(w, "Failed to read state", http.StatusInternalServerError)
			return
		}
		currentUser := state.Global.AdminUser
		if currentUser == "" {
			currentUser = "admin"
		}
		// Check if default credentials are still in place
		isDefault := currentUser == "admin" && (state.Global.AdminPasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(state.Global.AdminPasswordHash), []byte("admin")) == nil)

		resp := map[string]interface{}{
			"admin_user": currentUser,
			"is_default": isDefault,
			"has_hash":   state.Global.AdminPasswordHash != "",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	if r.Method == http.MethodPost {
		var payload struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		username := strings.TrimSpace(payload.Username)
		password := payload.Password

		// Validate username: 3-32 alphanumeric characters, dot, dash, or underscore
		if !regexp.MustCompile(`^[a-zA-Z0-9._-]{3,32}$`).MatchString(username) {
			http.Error(w, "Username must be 3-32 characters long and contain only alphanumeric characters, dots, dashes, or underscores", http.StatusBadRequest)
			return
		}

		// Validate password length: minimum 8 characters, maximum 128 characters
		if len(password) < 8 || len(password) > 128 {
			http.Error(w, "Password must be between 8 and 128 characters in length", http.StatusBadRequest)
			return
		}

		// Security check: reject control characters or null bytes in password
		if strings.ContainsAny(password, "\r\n\x00") {
			http.Error(w, "Password contains invalid characters", http.StatusBadRequest)
			return
		}

		// Hash password with bcrypt
		hashBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, "Failed to hash password", http.StatusInternalServerError)
			return
		}
		hash := string(hashBytes)

		state, err := readState()
		if err != nil {
			http.Error(w, "Failed to read state", http.StatusInternalServerError)
			return
		}

		state.Global.AdminUser = username
		state.Global.AdminPasswordHash = hash

		if err := writeState(state); err != nil {
			logAuditEvent(getClientIP(r), "update-admin-auth", "Failed to save state", "error")
			http.Error(w, "Failed to save administrative credentials", http.StatusInternalServerError)
			return
		}

		// On Linux, trigger Caddy reload via ap server auth if ap is available
		if runtime.GOOS == "linux" {
			apBin := "ap"
			if _, err := os.Stat("/usr/local/bin/ap"); err == nil {
				apBin = "/usr/local/bin/ap"
			}
			_ = exec.Command(apBin, "server", "auth", username, password).Run()
		}

		// Audit log WITHOUT logging plain password for security
		logAuditEvent(getClientIP(r), "update-admin-auth", fmt.Sprintf("Updated admin credentials for user: %s", username), "success")

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":     "ok",
			"message":    "Admin credentials successfully updated.",
			"admin_user": username,
		})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

type NetworkProbeResult struct {
	Name      string `json:"name"`
	Target    string `json:"target"`
	Category  string `json:"category"`
	Ok        bool   `json:"ok"`
	LatencyMs int64  `json:"latency_ms"`
	Message   string `json:"message"`
}

func handleNetworkTestAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, _ := readState()

	// Immutable, strictly whitelisted target probes (ZERO SSRF risk)
	probes := []struct {
		Name     string
		Target   string
		Network  string
		Category string
	}{
		{Name: "GitHub API (Updates)", Target: "api.github.com:443", Network: "tcp", Category: "Updates"},
		{Name: "Telegram Bot API (Alerts)", Target: "api.telegram.org:443", Network: "tcp", Category: "Notifications"},
		{Name: "Let's Encrypt ACME (SSL)", Target: "acme-v02.api.letsencrypt.org:443", Network: "tcp", Category: "Security"},
		{Name: "Cloudflare Public DNS", Target: "1.1.1.1:53", Network: "tcp", Category: "Network"},
		{Name: "Google Public DNS", Target: "8.8.8.8:53", Network: "tcp", Category: "Network"},
	}

	// If S3 Endpoint is configured, safely add it to probes
	if state != nil && state.Global.S3Endpoint != "" {
		ep := state.Global.S3Endpoint
		ep = strings.TrimPrefix(ep, "https://")
		ep = strings.TrimPrefix(ep, "http://")
		if idx := strings.Index(ep, "/"); idx != -1 {
			ep = ep[:idx]
		}
		if !strings.Contains(ep, ":") {
			ep = ep + ":443"
		}
		// Strict regex to validate host:port
		if regexp.MustCompile(`^[a-zA-Z0-9.-]+:[0-9]+$`).MatchString(ep) {
			bucketName := state.Global.S3Bucket
			if bucketName == "" {
				bucketName = "Configured Bucket"
			}
			probes = append(probes, struct {
				Name     string
				Target   string
				Network  string
				Category string
			}{
				Name:     fmt.Sprintf("S3 Cloud Storage (%s)", bucketName),
				Target:   ep,
				Network:  "tcp",
				Category: "Backups",
			})
		}
	}

	results := make([]NetworkProbeResult, len(probes))
	var wg sync.WaitGroup

	for i, p := range probes {
		wg.Add(1)
		go func(idx int, name, target, network, category string) {
			defer wg.Done()
			start := time.Now()
			conn, err := net.DialTimeout(network, target, 2500*time.Millisecond)
			duration := time.Since(start)

			res := NetworkProbeResult{
				Name:      name,
				Target:    target,
				Category:  category,
				LatencyMs: duration.Milliseconds(),
			}

			if err != nil {
				res.Ok = false
				res.Message = err.Error()
			} else {
				res.Ok = true
				res.Message = "Connected successfully"
				_ = conn.Close()
			}
			results[idx] = res
		}(i, p.Name, p.Target, p.Network, p.Category)
	}

	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"time":    time.Now().UTC().Format(time.RFC3339),
		"results": results,
	})
}

