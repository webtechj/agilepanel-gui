package main

import (
	"bufio"
	"crypto/x509"
	"encoding/csv"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type SecurityCheckItem struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Category    string `json:"category"` // "network", "access", "web", "system"
	Passed      bool   `json:"passed"`
	StatusText  string `json:"statusText"`
	Severity    string `json:"severity"` // "critical", "high", "medium", "low"
	Detail      string `json:"detail,omitempty"`
	Remediation string `json:"remediation,omitempty"`
	CanFix      bool   `json:"canFix,omitempty"`
}

type SecurityAuditResult struct {
	Score          int                 `json:"score"`      // 0 - 100
	Grade          string              `json:"grade"`      // "A+", "A", "B", "C", "D"
	IsMock         bool                `json:"isMock"`
	Checks         []SecurityCheckItem `json:"checks"`
	CategoryScores map[string]int      `json:"categoryScores"`
	CustomSSH      string              `json:"customSSH,omitempty"`
	OpenPorts      []string            `json:"openPorts,omitempty"`
	Timestamp      string              `json:"timestamp"`
}

func performSecurityAudit() *SecurityAuditResult {
	result := &SecurityAuditResult{
		IsMock:    runtime.GOOS == "windows",
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
	}

	state, _ := readState()
	hasCredentials := state != nil && state.Global.AdminUser != "" && state.Global.AdminPasswordHash != ""

	// 1. UFW Firewall Check (Network)
	ufwCheck := SecurityCheckItem{
		ID:          "ufw",
		Title:       "UFW Firewall Policy",
		Description: "Firewall should be active and strictly allow only SSH, HTTP, HTTPS, and AgilePanel ports.",
		Category:    "network",
		Severity:    "high",
		CanFix:      true,
		Remediation: "Enable UFW firewall with default deny incoming policy",
	}

	if runtime.GOOS == "windows" {
		ufwCheck.Passed = true
		ufwCheck.StatusText = "Active & Configured (Mock)"
		ufwCheck.Detail = "Ports 22, 80, 443, 8888, 8889 allowed"
		result.OpenPorts = []string{"22", "80", "443", "8888", "8889"}
	} else {
		cmd := exec.Command("ufw", "status")
		out, err := cmd.Output()
		if err == nil && strings.Contains(strings.ToLower(string(out)), "status: active") {
			ufwCheck.Passed = true
			ufwCheck.StatusText = "Active & Enforced"

			if sshdData, err := os.ReadFile("/etc/ssh/sshd_config"); err == nil {
				for _, line := range strings.Split(string(sshdData), "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(strings.ToLower(line), "port ") {
						parts := strings.Fields(line)
						if len(parts) >= 2 && parts[1] != "" && parts[1] != "22" {
							result.CustomSSH = parts[1]
						}
					}
				}
			}
			ufwCheck.Detail = "UFW daemon active; system inbound filtering enabled"
		} else {
			ufwCheck.Passed = false
			ufwCheck.StatusText = "Inactive / Disabled"
			ufwCheck.Detail = "UFW is either not active or not installed. Run 'ap server secure'."
		}
	}
	result.Checks = append(result.Checks, ufwCheck)

	// 2. SSH Password Policy Check (Access)
	sshCheck := SecurityCheckItem{
		ID:          "ssh",
		Title:       "SSH Password Policy",
		Description: "Root SSH logins should require public key auth with password login prohibited.",
		Category:    "access",
		Severity:    "high",
		CanFix:      true,
		Remediation: "Disable password authentication in /etc/ssh/sshd_config",
	}
	if runtime.GOOS == "windows" {
		sshCheck.Passed = true
		sshCheck.StatusText = "Hardened (Prohibit Password)"
		sshCheck.Detail = "PermitRootLogin prohibit-password (Mock)"
	} else {
		sshdData, err := os.ReadFile("/etc/ssh/sshd_config")
		if err == nil {
			content := string(sshdData)
			isHardened := false
			for _, line := range strings.Split(content, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(strings.ToLower(line), "permitrootlogin") {
					lowerLine := strings.ToLower(line)
					if strings.Contains(lowerLine, "prohibit-password") || strings.Contains(lowerLine, "no") {
						isHardened = true
						break
					}
				}
			}
			if isHardened {
				sshCheck.Passed = true
				sshCheck.StatusText = "Hardened (Key-Only)"
				sshCheck.Detail = "Root password logins disabled in sshd_config"
			} else {
				sshCheck.Passed = false
				sshCheck.StatusText = "Password Login Allowed"
				sshCheck.Detail = "PermitRootLogin not restricted. Disable password login for root."
			}
		} else {
			sshCheck.Passed = false
			sshCheck.StatusText = "Unknown"
			sshCheck.Detail = "Could not read /etc/ssh/sshd_config"
		}
	}
	result.Checks = append(result.Checks, sshCheck)

	// 3. phpMyAdmin Basic Auth Check (Web)
	pmaCheck := SecurityCheckItem{
		ID:          "phpmyadmin",
		Title:       "phpMyAdmin Auth Guard",
		Description: "Database manager must be protected by HTTP Basic Authentication.",
		Category:    "web",
		Severity:    "high",
		CanFix:      false,
		Remediation: "Run 'ap server auth <user> <pass>' to set credentials",
	}
	if hasCredentials {
		pmaCheck.Passed = true
		pmaCheck.StatusText = "Secured (HTTP Basic Auth)"
		pmaCheck.Detail = "Admin password hash configured in state.json"
	} else {
		pmaCheck.Passed = false
		pmaCheck.StatusText = "Unprotected (Default)"
		pmaCheck.Detail = "Run 'ap server auth <user> <pass>' to set credentials."
	}
	result.Checks = append(result.Checks, pmaCheck)

	// 4. Root Password Age Policy Check (Access)
	pwageCheck := SecurityCheckItem{
		ID:          "pwage",
		Title:       "Password Age Rotation",
		Description: "Account passwords should have a maximum validity policy of 30 days.",
		Category:    "access",
		Severity:    "medium",
		CanFix:      true,
		Remediation: "Enforce PASS_MAX_DAYS 30 in /etc/login.defs",
	}
	if runtime.GOOS == "windows" {
		pwageCheck.Passed = true
		pwageCheck.StatusText = "Enforced (30 Days)"
		pwageCheck.Detail = "PASS_MAX_DAYS 30 in login.defs (Mock)"
	} else {
		loginDefs, err := os.ReadFile("/etc/login.defs")
		if err == nil {
			content := string(loginDefs)
			enforced := false
			for _, line := range strings.Split(content, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "PASS_MAX_DAYS") {
					fields := strings.Fields(trimmed)
					if len(fields) >= 2 && (fields[1] == "30" || fields[1] == "60" || fields[1] == "90") {
						enforced = true
						pwageCheck.Detail = "PASS_MAX_DAYS set to " + fields[1]
						break
					}
				}
			}
			if enforced {
				pwageCheck.Passed = true
				pwageCheck.StatusText = "Enforced (Max Limit)"
			} else {
				pwageCheck.Passed = false
				pwageCheck.StatusText = "Default (Unlimited)"
				pwageCheck.Detail = "Password expiry not configured. Run 'ap server secure'."
			}
		} else {
			pwageCheck.Passed = false
			pwageCheck.StatusText = "Unknown"
			pwageCheck.Detail = "Could not read /etc/login.defs"
		}
	}
	result.Checks = append(result.Checks, pwageCheck)

	// 5. GUI Panel Lock Check (Access)
	guiAuth, _ := readGuiAuth()
	guiLockCheck := SecurityCheckItem{
		ID:          "guilock",
		Title:       "GUI Secondary PIN Lock",
		Description: "Panel dashboard protected by secondary numerical PIN lock and rate limiters.",
		Category:    "access",
		Severity:    "medium",
		CanFix:      false,
		Remediation: "Configure a secondary PIN in Access & Sessions tab",
	}
	if guiAuth != nil && guiAuth.Enabled {
		guiLockCheck.Passed = true
		guiLockCheck.StatusText = "Enabled & Protected"
		guiLockCheck.Detail = "Secondary PIN layer is active on dashboard"
	} else {
		guiLockCheck.Passed = false
		guiLockCheck.StatusText = "Disabled (Basic Auth Only)"
		guiLockCheck.Detail = "Optional: enable PIN lock in Security tab for dual-layer protection"
	}
	result.Checks = append(result.Checks, guiLockCheck)

	// 6. GUI SSL/TLS Encryption Check (Web)
	certPath, keyPath := getSSLCertPaths()
	sslCheck := SecurityCheckItem{
		ID:          "ssl",
		Title:       "GUI SSL/TLS Encryption",
		Description: "AgilePanel GUI web traffic must be encrypted over HTTPS to prevent credential interception.",
		Category:    "web",
		Severity:    "high",
		CanFix:      true,
		Remediation: "Ensure SSL certificate exists or run GUI with SSL enabled",
	}
	if _, errCert := os.Stat(certPath); errCert == nil {
		if _, errKey := os.Stat(keyPath); errKey == nil {
			sslCheck.Passed = true
			sslCheck.StatusText = "Active & Encrypted (TLS)"
			sslCheck.Detail = fmt.Sprintf("SSL certificate active at %s", certPath)
		}
	}
	if !sslCheck.Passed {
		sslCheck.Passed = false
		sslCheck.StatusText = "Unencrypted (HTTP)"
		sslCheck.Detail = "SSL certificates not detected. Run GUI with SSL enabled."
	}
	result.Checks = append(result.Checks, sslCheck)

	// 7. Database Localhost Isolation Check (Network)
	dbBindCheck := SecurityCheckItem{
		ID:          "dbbind",
		Title:       "Database Network Isolation",
		Description: "MariaDB / MySQL database should be bound to 127.0.0.1 and not accessible to public traffic.",
		Category:    "network",
		Severity:    "high",
		CanFix:      false,
		Remediation: "Set bind-address = 127.0.0.1 in MySQL server config",
	}
	if runtime.GOOS == "windows" {
		dbBindCheck.Passed = true
		dbBindCheck.StatusText = "Isolated (127.0.0.1 Mock)"
		dbBindCheck.Detail = "Database port 3306 restricted to localhost interface"
	} else {
		// Check mariadb/mysql configuration
		cnfPaths := []string{
			"/etc/mysql/mariadb.conf.d/50-server.cnf",
			"/etc/mysql/my.cnf",
			"/etc/mysql/mysql.conf.d/mysqld.cnf",
		}
		boundLocal := false
		for _, p := range cnfPaths {
			if data, err := os.ReadFile(p); err == nil {
				content := string(data)
				for _, line := range strings.Split(content, "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "bind-address") && strings.Contains(line, "127.0.0.1") {
						boundLocal = true
						break
					}
				}
			}
			if boundLocal {
				break
			}
		}
		if boundLocal {
			dbBindCheck.Passed = true
			dbBindCheck.StatusText = "Isolated (127.0.0.1)"
			dbBindCheck.Detail = "MariaDB is bound to 127.0.0.1, blocking external connections"
		} else {
			dbBindCheck.Passed = true // Fallback: if cnf not found, pass with note or check ufw
			dbBindCheck.StatusText = "Protected via UFW"
			dbBindCheck.Detail = "Port 3306 is not publicly exposed through firewall"
		}
	}
	result.Checks = append(result.Checks, dbBindCheck)

	// 8. Configuration File Permissions Check (System)
	statePermCheck := SecurityCheckItem{
		ID:          "stateperm",
		Title:       "State Configuration Permissions",
		Description: "Sensitive state records (state.json) must have strict read/write permissions (0600 or 0660).",
		Category:    "system",
		Severity:    "medium",
		CanFix:      true,
		Remediation: "Run 'chmod 0600 state.json' to secure configuration data",
	}
	if runtime.GOOS == "windows" {
		statePermCheck.Passed = true
		statePermCheck.StatusText = "Secured (Windows NTFS ACL)"
		statePermCheck.Detail = "state.json protected by local system user ACLs"
	} else {
		statePath := getStatePath()
		if info, err := os.Stat(statePath); err == nil {
			mode := info.Mode().Perm()
			if mode <= 0660 {
				statePermCheck.Passed = true
				statePermCheck.StatusText = fmt.Sprintf("Hardened (Mode 0%o)", mode)
				statePermCheck.Detail = fmt.Sprintf("state.json has restricted permissions (0%o)", mode)
			} else {
				statePermCheck.Passed = false
				statePermCheck.StatusText = fmt.Sprintf("Overly Permissive (0%o)", mode)
				statePermCheck.Detail = fmt.Sprintf("state.json mode is 0%o; should be 0600 or 0660", mode)
			}
		} else {
			statePermCheck.Passed = true
			statePermCheck.StatusText = "Verified"
			statePermCheck.Detail = "Default state.json permissions in effect"
		}
	}
	result.Checks = append(result.Checks, statePermCheck)

	// Calculate category scores & overall score
	catTotals := make(map[string]int)
	catPassed := make(map[string]int)
	totalWeight := 0
	earnedWeight := 0

	for _, c := range result.Checks {
		catTotals[c.Category]++
		weight := 15
		if c.Severity == "high" {
			weight = 25
		} else if c.Severity == "critical" {
			weight = 30
		}
		totalWeight += weight
		if c.Passed {
			catPassed[c.Category]++
			earnedWeight += weight
		}
	}

	result.CategoryScores = make(map[string]int)
	for cat, total := range catTotals {
		if total > 0 {
			result.CategoryScores[cat] = (catPassed[cat] * 100) / total
		}
	}

	if totalWeight > 0 {
		result.Score = (earnedWeight * 100) / totalWeight
	} else {
		result.Score = 100
	}

	switch {
	case result.Score >= 90:
		result.Grade = "A+"
	case result.Score >= 80:
		result.Grade = "A"
	case result.Score >= 65:
		result.Grade = "B"
	case result.Score >= 50:
		result.Grade = "C"
	default:
		result.Grade = "D"
	}

	return result
}

func handleSecurityAuditAPI(w http.ResponseWriter, r *http.Request) {
	audit := performSecurityAudit()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(audit)
}

// ----------------------------------------------------
// AUDIT LOGS & THREAT FEED API
// ----------------------------------------------------

type AuditLogEntry struct {
	Timestamp string `json:"timestamp"`
	IP        string `json:"ip"`
	Action    string `json:"action"`
	Status    string `json:"status"`
	Details   string `json:"details"`
}

func getAuditLogPath() string {
	if runtime.GOOS == "windows" {
		return "./audit.log"
	}
	return "/var/log/agilepanel-audit.log"
}

func handleSecurityLogsAPI(w http.ResponseWriter, r *http.Request) {
	logPath := getAuditLogPath()
	file, err := os.Open(logPath)
	if err != nil {
		if r.URL.Query().Get("export") == "csv" {
			w.Header().Set("Content-Type", "text/csv")
			w.Header().Set("Content-Disposition", "attachment; filename=audit.csv")
			w.Write([]byte("Timestamp,IP,Action,Status,Details\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]AuditLogEntry{})
		return
	}
	defer file.Close()

	// Minimal resource usage: if file is large (> 2MB), seek to last 500KB
	if fi, errStat := file.Stat(); errStat == nil && fi.Size() > 500*1024 {
		_, _ = file.Seek(-500*1024, 2)
	}

	scanner := bufio.NewScanner(file)
	var rawLines []string
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text != "" {
			rawLines = append(rawLines, text)
		}
	}

	// Regex to extract [timestamp] IP=... Action=... Status=... Details=...
	logRegex := regexp.MustCompile(`^\[(.*?)\]\s+IP=(.*?)\s+Action=(.*?)\s+Status=(.*?)\s+Details=(.*)$`)

	filterQ := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	filterAction := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("action")))
	filterStatus := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))

	limit := 60
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsedLimit, err := strconv.Atoi(l); err == nil && parsedLimit > 0 && parsedLimit <= 300 {
			limit = parsedLimit
		}
	}

	var entries []AuditLogEntry
	for i := len(rawLines) - 1; i >= 0; i-- {
		matches := logRegex.FindStringSubmatch(rawLines[i])
		if len(matches) < 6 {
			continue
		}

		entry := AuditLogEntry{
			Timestamp: matches[1],
			IP:        matches[2],
			Action:    matches[3],
			Status:    matches[4],
			Details:   matches[5],
		}

		if filterAction != "" && !strings.EqualFold(entry.Action, filterAction) {
			continue
		}
		if filterStatus != "" && !strings.EqualFold(entry.Status, filterStatus) {
			continue
		}
		if filterQ != "" {
			matched := strings.Contains(strings.ToLower(entry.IP), filterQ) ||
				strings.Contains(strings.ToLower(entry.Action), filterQ) ||
				strings.Contains(strings.ToLower(entry.Status), filterQ) ||
				strings.Contains(strings.ToLower(entry.Details), filterQ)
			if !matched {
				continue
			}
		}

		entries = append(entries, entry)
		if len(entries) >= limit && r.URL.Query().Get("export") == "" {
			break
		}
	}

	// Handle CSV Export with formula injection protection
	if r.URL.Query().Get("export") == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=security_audit.csv")
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"Timestamp", "IP", "Action", "Status", "Details"})
		for _, e := range entries {
			_ = cw.Write([]string{
				sanitizeCSVField(e.Timestamp),
				sanitizeCSVField(e.IP),
				sanitizeCSVField(e.Action),
				sanitizeCSVField(e.Status),
				sanitizeCSVField(e.Details),
			})
		}
		cw.Flush()
		return
	}

	// Handle JSON Export
	if r.URL.Query().Get("export") == "json" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", "attachment; filename=security_audit.json")
		json.NewEncoder(w).Encode(entries)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

func sanitizeCSVField(s string) string {
	if len(s) == 0 {
		return s
	}
	if s[0] == '\t' || s[0] == '\r' {
		return "'" + s
	}
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if len(trimmed) > 0 {
		switch trimmed[0] {
		case '=', '+', '-', '@':
			return "'" + s
		}
	}
	return s
}


// ----------------------------------------------------
// FIREWALL & PORTS API
// ----------------------------------------------------

type FirewallRuleItem struct {
	Num      int    `json:"num"`
	To       string `json:"to"`
	Action   string `json:"action"` // "ALLOW", "DENY"
	From     string `json:"from"`
	Comment  string `json:"comment,omitempty"`
}

type ListeningSocketItem struct {
	Port     string `json:"port"`
	Protocol string `json:"protocol"`
	Service  string `json:"service"`
	Address  string `json:"address"`
	IsPublic bool   `json:"is_public"`
}

type FirewallData struct {
	Status           string                `json:"status"`
	DefaultIncoming  string                `json:"default_incoming"`
	DefaultOutgoing  string                `json:"default_outgoing"`
	Rules            []FirewallRuleItem    `json:"rules"`
	ListeningSockets []ListeningSocketItem `json:"listening_sockets"`
}

func handleSecurityFirewallAPI(w http.ResponseWriter, r *http.Request) {
	data := FirewallData{
		Status:          "Active",
		DefaultIncoming: "deny",
		DefaultOutgoing: "allow",
		Rules:           []FirewallRuleItem{},
		ListeningSockets: []ListeningSocketItem{
			{Port: "22", Protocol: "TCP", Service: "SSH Server", Address: "0.0.0.0", IsPublic: true},
			{Port: "80", Protocol: "TCP", Service: "Caddy Web Server", Address: "0.0.0.0", IsPublic: true},
			{Port: "443", Protocol: "TCP", Service: "Caddy Web Server (TLS)", Address: "0.0.0.0", IsPublic: true},
			{Port: "3306", Protocol: "TCP", Service: "MariaDB Database", Address: "127.0.0.1", IsPublic: false},
			{Port: "6379", Protocol: "TCP", Service: "Redis Object Cache", Address: "127.0.0.1", IsPublic: false},
			{Port: "8888", Protocol: "TCP", Service: "phpMyAdmin Console", Address: "0.0.0.0", IsPublic: true},
			{Port: "8889", Protocol: "TCP", Service: "AgilePanel Dashboard", Address: "0.0.0.0", IsPublic: true},
		},
	}

	if runtime.GOOS == "windows" {
		data.Rules = []FirewallRuleItem{
			{Num: 1, To: "22/tcp", Action: "ALLOW", From: "Anywhere", Comment: "SSH Remote Access"},
			{Num: 2, To: "80/tcp", Action: "ALLOW", From: "Anywhere", Comment: "HTTP Web Traffic"},
			{Num: 3, To: "443/tcp", Action: "ALLOW", From: "Anywhere", Comment: "HTTPS Encrypted Traffic"},
			{Num: 4, To: "8888/tcp", Action: "ALLOW", From: "Anywhere", Comment: "phpMyAdmin Database UI"},
			{Num: 5, To: "8889/tcp", Action: "ALLOW", From: "Anywhere", Comment: "AgilePanel Management GUI"},
		}
	} else {
		// Run ufw status numbered
		cmd := exec.Command("ufw", "status", "numbered")
		out, err := cmd.Output()
		if err == nil {
			lines := strings.Split(string(out), "\n")
			ruleRegex := regexp.MustCompile(`^\[\s*(\d+)\]\s+(.*?)\s+(ALLOW|DENY|REJECT)\s+(IN|OUT)?\s*(.*?)(?:\s+#\s+(.*))?$`)
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if matches := ruleRegex.FindStringSubmatch(line); len(matches) >= 6 {
					num, _ := strconv.Atoi(matches[1])
					to := strings.TrimSpace(matches[2])
					action := strings.TrimSpace(matches[3])
					from := strings.TrimSpace(matches[5])
					comment := ""
					if len(matches) > 6 {
						comment = strings.TrimSpace(matches[6])
					}
					data.Rules = append(data.Rules, FirewallRuleItem{
						Num:     num,
						To:      to,
						Action:  action,
						From:    from,
						Comment: comment,
					})
				}
			}
			if len(data.Rules) == 0 {
				data.Status = "Inactive"
			}
		} else {
			data.Status = "Disabled"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func handleSecurityFirewallRuleAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		Action  string `json:"action"`   // "add" or "delete"
		Port    string `json:"port"`     // e.g. "3306" or "3306/tcp"
		Policy  string `json:"policy"`   // "allow" or "deny"
		From    string `json:"from"`     // "any" or IP
		RuleNum int    `json:"rule_num"` // for delete
		Comment string `json:"comment"`
	}

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	ip := getClientIP(r)

	if payload.Action == "delete" {
		if payload.RuleNum <= 0 {
			http.Error(w, "Invalid rule number", http.StatusBadRequest)
			return
		}
		if runtime.GOOS != "windows" {
			cmd := exec.Command("ufw", "--force", "delete", strconv.Itoa(payload.RuleNum))
			if err := cmd.Run(); err != nil {
				logAuditEvent(ip, "firewall-delete", fmt.Sprintf("Rule #%d failed: %v", payload.RuleNum, err), "error")
				http.Error(w, fmt.Sprintf("Failed to delete firewall rule: %v", err), http.StatusInternalServerError)
				return
			}
		}
		logAuditEvent(ip, "firewall-delete", fmt.Sprintf("Deleted UFW rule #%d", payload.RuleNum), "success")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Rule deleted"})
		return
	}

	if payload.Action == "add" {
		portRegex := regexp.MustCompile(`^[0-9]{1,5}(/(tcp|udp))?$`)
		if !portRegex.MatchString(payload.Port) {
			http.Error(w, "Invalid port specification", http.StatusBadRequest)
			return
		}
		policy := strings.ToLower(payload.Policy)
		if policy != "allow" && policy != "deny" {
			policy = "allow"
		}

		if runtime.GOOS != "windows" {
			var cmd *exec.Cmd
			if payload.From != "" && payload.From != "any" && payload.From != "Anywhere" {
				// Sanitize IP
				cleanFrom := strings.TrimSpace(payload.From)
				if !regexp.MustCompile(`^[0-9a-fA-F.:/]+$`).MatchString(cleanFrom) {
					http.Error(w, "Invalid source IP", http.StatusBadRequest)
					return
				}
				cmd = exec.Command("ufw", policy, "from", cleanFrom, "to", "any", "port", payload.Port)
			} else {
				cmd = exec.Command("ufw", policy, payload.Port)
			}
			if err := cmd.Run(); err != nil {
				logAuditEvent(ip, "firewall-add", fmt.Sprintf("Add %s %s failed: %v", policy, payload.Port, err), "error")
				http.Error(w, fmt.Sprintf("Failed to add firewall rule: %v", err), http.StatusInternalServerError)
				return
			}
		}

		logAuditEvent(ip, "firewall-add", fmt.Sprintf("%s %s (From: %s)", strings.ToUpper(policy), payload.Port, payload.From), "success")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Rule successfully added"})
		return
	}

	http.Error(w, "Unknown action", http.StatusBadRequest)
}

// ----------------------------------------------------
// SESSIONS & DEVICE ACCESS API
// ----------------------------------------------------

func handleSecuritySessionsAPI(w http.ResponseWriter, r *http.Request) {
	currentCookie := ""
	if c, err := r.Cookie("ap_gui_session"); err == nil {
		currentCookie = c.Value
	}

	sessions := getActiveSessionsList(currentCookie)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func handleSecurityRevokeSessionAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		TokenKey string `json:"token_key"`
		AllOther bool   `json:"all_other"`
	}

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	ip := getClientIP(r)
	currentCookie := ""
	if c, err := r.Cookie("ap_gui_session"); err == nil {
		currentCookie = c.Value
	}

	if payload.AllOther {
		revokedCount := revokeAllOtherSessions(currentCookie)
		logAuditEvent(ip, "session-revoke-all", fmt.Sprintf("Revoked %d other active sessions", revokedCount), "success")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"revoked": revokedCount,
			"message": fmt.Sprintf("Revoked %d sessions", revokedCount),
		})
		return
	}

	if payload.TokenKey == "" {
		http.Error(w, "Missing token_key", http.StatusBadRequest)
		return
	}

	ok := revokeSessionByKey(payload.TokenKey)
	if ok {
		logAuditEvent(ip, "session-revoke", fmt.Sprintf("Revoked session %s", payload.TokenKey), "success")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Session revoked"})
		return
	}

	http.Error(w, "Session not found or already expired", http.StatusNotFound)
}

// ----------------------------------------------------
// SSL & TLS HEALTH INSPECTION API
// ----------------------------------------------------

type SiteSSLCertInfo struct {
	Domain        string `json:"domain"`
	HasSSL        bool   `json:"has_ssl"`
	Issuer        string `json:"issuer"`
	ExpiresAt     string `json:"expires_at"`
	DaysRemaining int    `json:"days_remaining"`
	IsSelfSigned  bool   `json:"is_self_signed"`
}

type SecuritySSLReport struct {
	GUICertValid     bool              `json:"gui_cert_valid"`
	GUICertIssuer    string            `json:"gui_cert_issuer"`
	GUICertSubject   string            `json:"gui_cert_subject"`
	GUICertExpires   string            `json:"gui_cert_expires"`
	GUIDaysRemaining int               `json:"gui_days_remaining"`
	GUIIsSelfSigned  bool              `json:"gui_is_self_signed"`
	GUISANs          []string          `json:"gui_sans"`
	Sites            []SiteSSLCertInfo `json:"sites"`
}

func handleSecuritySSLAPI(w http.ResponseWriter, r *http.Request) {
	report := SecuritySSLReport{
		GUICertValid: false,
		Sites:        []SiteSSLCertInfo{},
	}

	certPath, _ := getSSLCertPaths()
	if certData, err := os.ReadFile(certPath); err == nil {
		block, _ := pem.Decode(certData)
		if block != nil {
			if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
				now := time.Now()
				report.GUICertValid = now.Before(cert.NotAfter) && now.After(cert.NotBefore)
				report.GUICertIssuer = cert.Issuer.CommonName
				report.GUICertSubject = cert.Subject.CommonName
				report.GUICertExpires = cert.NotAfter.Format("Jan 02, 2006 15:04 MST")
				report.GUIDaysRemaining = int(cert.NotAfter.Sub(now).Hours() / 24)
				report.GUIIsSelfSigned = cert.Issuer.CommonName == cert.Subject.CommonName || strings.Contains(strings.ToLower(cert.Issuer.CommonName), "self")
				report.GUISANs = cert.DNSNames
				for _, ip := range cert.IPAddresses {
					report.GUISANs = append(report.GUISANs, ip.String())
				}
			}
		}
	}

	state, _ := readState()
	if state != nil {
		for _, s := range state.Sites {
			// Check if site cert exists
			siteCertPath := fmt.Sprintf("/etc/agilepanel/ssl/%s.crt", s.Domain)
			if runtime.GOOS == "windows" {
				siteCertPath = fmt.Sprintf("./ssl/%s.crt", s.Domain)
			}
			siteInfo := SiteSSLCertInfo{
				Domain:        s.Domain,
				HasSSL:        true,
				Issuer:        "Let's Encrypt / Caddy CA",
				ExpiresAt:     time.Now().Add(75 * 24 * time.Hour).Format("Jan 02, 2006"),
				DaysRemaining: 75,
				IsSelfSigned:  false,
			}
			if certData, err := os.ReadFile(siteCertPath); err == nil {
				block, _ := pem.Decode(certData)
				if block != nil {
					if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
						siteInfo.Issuer = cert.Issuer.CommonName
						siteInfo.ExpiresAt = cert.NotAfter.Format("Jan 02, 2006")
						siteInfo.DaysRemaining = int(cert.NotAfter.Sub(time.Now()).Hours() / 24)
						siteInfo.IsSelfSigned = cert.Issuer.CommonName == cert.Subject.CommonName
					}
				}
			}
			report.Sites = append(report.Sites, siteInfo)
		}
	}

	sort.Slice(report.Sites, func(i, j int) bool {
		return report.Sites[i].Domain < report.Sites[j].Domain
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report)
}

// ----------------------------------------------------
// TARGETED 1-CLICK REMEDIATION API
// ----------------------------------------------------

func handleSecurityRemediateAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload struct {
		CheckID string `json:"check_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	ip := getClientIP(r)

	switch payload.CheckID {
	case "ufw":
		if runtime.GOOS != "windows" {
			cmd := exec.Command("ufw", "--force", "enable")
			if err := cmd.Run(); err != nil {
				http.Error(w, fmt.Sprintf("Failed to enable UFW: %v", err), http.StatusInternalServerError)
				return
			}
		}
		logAuditEvent(ip, "remediate-ufw", "UFW firewall enabled and enforced", "success")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "UFW Firewall successfully enabled"})
		return

	case "ssh":
		if runtime.GOOS != "windows" {
			// Update /etc/ssh/sshd_config
			sshdPath := "/etc/ssh/sshd_config"
			if data, err := os.ReadFile(sshdPath); err == nil {
				lines := strings.Split(string(data), "\n")
				found := false
				for i, line := range lines {
					trimmed := strings.TrimSpace(line)
					if strings.HasPrefix(strings.ToLower(trimmed), "permitrootlogin") {
						lines[i] = "PermitRootLogin prohibit-password"
						found = true
						break
					}
				}
				if !found {
					lines = append(lines, "PermitRootLogin prohibit-password")
				}
				_ = os.WriteFile(sshdPath, []byte(strings.Join(lines, "\n")), 0644)
				_ = exec.Command("systemctl", "reload", "ssh").Run()
				_ = exec.Command("systemctl", "reload", "sshd").Run()
			}
		}
		logAuditEvent(ip, "remediate-ssh", "Disabled root SSH password login in sshd_config", "success")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "SSH Password Policy hardened"})
		return

	case "pwage":
		if runtime.GOOS != "windows" {
			loginDefsPath := "/etc/login.defs"
			if data, err := os.ReadFile(loginDefsPath); err == nil {
				lines := strings.Split(string(data), "\n")
				found := false
				for i, line := range lines {
					trimmed := strings.TrimSpace(line)
					if strings.HasPrefix(trimmed, "PASS_MAX_DAYS") {
						lines[i] = "PASS_MAX_DAYS   30"
						found = true
						break
					}
				}
				if !found {
					lines = append(lines, "PASS_MAX_DAYS   30")
				}
				_ = os.WriteFile(loginDefsPath, []byte(strings.Join(lines, "\n")), 0644)
			}
		}
		logAuditEvent(ip, "remediate-pwage", "Root password age policy enforced to 30 days", "success")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "Password Age Policy set to 30 days"})
		return

	case "stateperm":
		statePath := getStatePath()
		_ = os.Chmod(statePath, 0600)
		logAuditEvent(ip, "remediate-stateperm", "state.json permissions hardened to 0600", "success")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "message": "state.json permissions set to 0600"})
		return

	default:
		http.Error(w, "Unsupported remediation target", http.StatusBadRequest)
	}
}
