package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type SecurityCheckItem struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Passed      bool   `json:"passed"`
	StatusText  string `json:"statusText"`
	Severity    string `json:"severity"` // "high", "medium", "low"
	Detail      string `json:"detail,omitempty"`
}

type SecurityAuditResult struct {
	Score      int                 `json:"score"`      // 0 - 100
	Grade      string              `json:"grade"`      // "A+", "A", "B", "C", "D"
	IsMock     bool                `json:"isMock"`
	Checks     []SecurityCheckItem `json:"checks"`
	CustomSSH  string              `json:"customSSH,omitempty"`
	OpenPorts  []string            `json:"openPorts,omitempty"`
	Timestamp  string              `json:"timestamp"`
}

func performSecurityAudit() *SecurityAuditResult {
	result := &SecurityAuditResult{
		IsMock: runtime.GOOS == "windows",
	}

	state, _ := readState()
	hasCredentials := state != nil && state.Global.AdminUser != "" && state.Global.AdminPasswordHash != ""

	// 1. UFW Firewall Check
	ufwCheck := SecurityCheckItem{
		ID:          "ufw",
		Title:       "UFW Firewall Policy",
		Description: "Firewall should be active and strictly allow only SSH, HTTP, HTTPS, and AgilePanel ports.",
		Severity:    "high",
	}

	if runtime.GOOS == "windows" {
		ufwCheck.Passed = true
		ufwCheck.StatusText = "Active & Configured (Mock)"
		ufwCheck.Detail = "Ports 22, 80, 443, 8888, 8889 allowed"
		result.OpenPorts = []string{"22", "80", "443", "8888", "8889"}
	} else {
		// Check if ufw is installed and active
		cmd := exec.Command("ufw", "status")
		out, err := cmd.Output()
		if err == nil && strings.Contains(strings.ToLower(string(out)), "status: active") {
			ufwCheck.Passed = true
			ufwCheck.StatusText = "Active & Enforced"
			
			// Detect custom SSH port from sshd_config if present
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

	// 2. phpMyAdmin Basic Auth Check
	pmaCheck := SecurityCheckItem{
		ID:          "phpmyadmin",
		Title:       "phpMyAdmin Auth Guard",
		Description: "Database manager must be protected by HTTP Basic Authentication.",
		Severity:    "high",
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

	// 3. SSH Password Policy Check
	sshCheck := SecurityCheckItem{
		ID:          "ssh",
		Title:       "SSH Password Policy",
		Description: "Root SSH logins should require public key auth with password login prohibited.",
		Severity:    "high",
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
				sshCheck.Detail = "PermitRootLogin not restricted. Run 'ap server secure'."
			}
		} else {
			sshCheck.Passed = false
			sshCheck.StatusText = "Unknown"
			sshCheck.Detail = "Could not read /etc/ssh/sshd_config"
		}
	}
	result.Checks = append(result.Checks, sshCheck)

	// 4. Root Password Age Policy Check
	pwageCheck := SecurityCheckItem{
		ID:          "pwage",
		Title:       "Password Age Rotation",
		Description: "Account passwords should have a maximum validity policy of 30 days.",
		Severity:    "medium",
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

	// 5. GUI Panel Lock Check
	guiAuth, _ := readGuiAuth()
	guiLockCheck := SecurityCheckItem{
		ID:          "guilock",
		Title:       "GUI Secondary PIN Lock",
		Description: "Panel dashboard protected by secondary numerical PIN lock and rate limiters.",
		Severity:    "medium",
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

	// Calculate overall score & grade
	passedCount := 0
	totalWeight := 0
	earnedWeight := 0
	for _, c := range result.Checks {
		weight := 20
		if c.Severity == "high" {
			weight = 25
		}
		totalWeight += weight
		if c.Passed {
			passedCount++
			earnedWeight += weight
		}
	}

	result.Score = (earnedWeight * 100) / totalWeight
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
