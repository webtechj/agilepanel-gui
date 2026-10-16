package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

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
		"site-create":    true,
		"site-delete":    true,
		"site-lock":      true,
		"site-unlock":    true,
		"site-cache":     true,
		"site-reinstall": true,
		"site-ssl":       true,
		"site-perms":     true,
		"site-backup":    true,
		"site-backup-db": true,
		"site-restore":   true,
		"server-restart": true,
		"server-tune":    true,
		"server-secure":  true,
		"tool-install":   true,
		"tool-fix":       true,
		"repair":         true,
		"sync":           true,
		"update":         true,
		"upgrade":        true,
		"server-clean":   true,
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
			cmdArgs = append(cmdArgs, "--import-files="+payload.Args[4])
		}
		if len(payload.Args) > 5 && payload.Args[5] != "" {
			cmdArgs = append(cmdArgs, "--import-db="+payload.Args[5])
		}
		if len(payload.Args) > 6 && payload.Args[6] != "" {
			cmdArgs = append(cmdArgs, "--wp-user="+payload.Args[6])
		}
		if len(payload.Args) > 7 && payload.Args[7] != "" {
			cmdArgs = append(cmdArgs, "--wp-pass="+payload.Args[7])
		}
		if len(payload.Args) > 8 && payload.Args[8] != "" {
			cmdArgs = append(cmdArgs, "--wp-email="+payload.Args[8])
		}
		if len(payload.Args) > 9 && payload.Args[9] != "" {
			cmdArgs = append(cmdArgs, "--wp-name="+payload.Args[9])
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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, apBin, cmdArgs...)

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
			cleaned := stripANSI(line)
			fmt.Fprintf(w, "data: %s%s\n\n", prefix, cleaned)
			flusher.Flush()
		}
	}

	go logScanner(stdoutPipe, "")
	go logScanner(stderrPipe, "ERR: ")

	wg.Wait()

	if err := cmd.Wait(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			logAuditEvent(getClientIP(r), "execute-command", fmt.Sprintf("Action: %s, Args: %v - Timeout error", payload.Action, payload.Args), "error")
			fmt.Fprintf(w, "data: Process timed out after 15 minutes.\n\n")
		} else {
			logAuditEvent(getClientIP(r), "execute-command", fmt.Sprintf("Action: %s, Args: %v - Exit error: %v", payload.Action, payload.Args, err), "error")
			fmt.Fprintf(w, "data: Process finished with Error: %v\n\n", err)
		}
	} else {
		logAuditEvent(getClientIP(r), "execute-command", fmt.Sprintf("Action: %s, Args: %v", payload.Action, payload.Args), "success")
		fmt.Fprintf(w, "data: Process finished successfully!\n\n")
		if payload.Action == "site-backup" && len(payload.Args) > 0 {
			updateLastBackupTime(payload.Args[0])
		}
	}
	invalidateServiceCache()
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
			if (str[i] >= 'A' && str[i] <= 'Z') || (str[i] >= 'a' && str[i] <= 'z') {
				inEscape = false
			}
			continue
		}
		sb.WriteByte(str[i])
	}
	return sb.String()
}
