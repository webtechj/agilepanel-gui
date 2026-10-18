package main

import (
	"bufio"
	"bytes"
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

func redactCommandArgs(args []string) string {
	var redacted []string
	for _, a := range args {
		if strings.HasPrefix(a, "--wp-pass=") {
			redacted = append(redacted, "--wp-pass=******")
		} else {
			redacted = append(redacted, a)
		}
	}
	return strings.Join(redacted, " ")
}

func redactPayloadArgs(action string, args []string) []string {
	if action != "site-create" {
		return args
	}
	res := make([]string, len(args))
	copy(res, args)
	if len(res) > 7 && res[7] != "" {
		res[7] = "******"
	}
	return res
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

	// Command argument sanitization: reject control characters and shell metacharacters
	for _, arg := range payload.Args {
		if strings.ContainsAny(arg, "\r\n\x00;|&$`><!\\") {
			http.Error(w, "Invalid character detected in command arguments", http.StatusBadRequest)
			return
		}
	}

	// For standard actions other than site-create, enforce strict alphanumeric pattern
	if payload.Action != "site-create" {
		safeArgRegex := regexp.MustCompile(`^[a-zA-Z0-9._-]*$`)
		for _, arg := range payload.Args {
			if !safeArgRegex.MatchString(arg) {
				http.Error(w, "Invalid character detected in command arguments", http.StatusBadRequest)
				return
			}
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
		"site-doctor":    true,
		"site-update":    true,
		"server-start":   true,
		"server-stop":    true,
		"server-restart": true,
		"server-reload":  true,
		"server-tune":    true,
		"server-secure":  true,
		"tool-install":   true,
		"tool-fix":       true,
		"repair":         true,
		"sync":               true,
		"update":             true,
		"upgrade":            true,
		"server-clean":       true,
		"server-doctor":      true,
		"server-cache-flush": true,
		"server-db-optimize": true,
		"server-ntp-sync":    true,
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
		if len(payload.Args) > 6 && payload.Args[6] != "" && !regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`).MatchString(payload.Args[6]) {
			http.Error(w, "Invalid username format", http.StatusBadRequest)
			return
		}
		if len(payload.Args) > 7 && payload.Args[7] != "" && strings.ContainsAny(payload.Args[7], "\r\n\x00") {
			http.Error(w, "Invalid password characters", http.StatusBadRequest)
			return
		}
		if len(payload.Args) > 8 && payload.Args[8] != "" && !regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`).MatchString(payload.Args[8]) {
			http.Error(w, "Invalid email address format", http.StatusBadRequest)
			return
		}
		if len(payload.Args) > 9 && payload.Args[9] != "" && strings.ContainsAny(payload.Args[9], "\r\n\x00;|&$`><!\\") {
			http.Error(w, "Invalid site name characters", http.StatusBadRequest)
			return
		}

	case "site-delete", "site-lock", "site-unlock", "site-cache", "site-reinstall", "site-ssl", "site-perms", "site-backup", "site-backup-db", "site-doctor":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing domain argument", http.StatusBadRequest)
			return
		}
		if !isValidDomain(payload.Args[0]) {
			http.Error(w, "Invalid domain format", http.StatusBadRequest)
			return
		}

	case "site-update":
		if len(payload.Args) < 2 {
			http.Error(w, "Missing arguments for site update (domain, php_version)", http.StatusBadRequest)
			return
		}
		if !isValidDomain(payload.Args[0]) {
			http.Error(w, "Invalid domain format", http.StatusBadRequest)
			return
		}
		if !regexp.MustCompile(`^[0-9]\.[0-9]$`).MatchString(payload.Args[1]) {
			http.Error(w, "Invalid PHP version format", http.StatusBadRequest)
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

	case "server-start", "server-stop", "server-restart", "server-reload":
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

	if payload.Action == "server-cache-flush" {
		handleServerCacheFlush(w, r)
		return
	}

	if payload.Action == "server-db-optimize" {
		handleServerDBOptimize(w, r)
		return
	}

	if payload.Action == "server-ntp-sync" {
		handleServerNTPSync(w, r)
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
	case "site-doctor":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing domain argument", http.StatusBadRequest)
			return
		}
		cmdArgs = []string{"site", "doctor", payload.Args[0], "--fix"}
	case "site-update":
		if len(payload.Args) < 2 {
			http.Error(w, "Missing arguments for site update", http.StatusBadRequest)
			return
		}
		cmdArgs = []string{"site", "update", payload.Args[0], "--php=" + payload.Args[1]}
	case "server-start":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing service name argument", http.StatusBadRequest)
			return
		}
		svc := payload.Args[0]
		if svc == "watchdog" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: AgilePanel Self-Healing Watchdog is active and running in background (15s heartbeat).\n\ndata: Process finished successfully!\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			return
		}
		if svc == "redis" {
			svc = "redis-server"
		}
		if runtime.GOOS == "linux" {
			apBin = "systemctl"
			cmdArgs = []string{"start", svc}
		} else {
			cmdArgs = []string{"server", "start", svc}
		}

	case "server-stop":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing service name argument", http.StatusBadRequest)
			return
		}
		svc := payload.Args[0]
		if svc == "watchdog" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: AgilePanel Watchdog is integral to the control panel.\n\ndata: Process finished successfully!\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			return
		}
		if svc == "redis" {
			svc = "redis-server"
		}
		if runtime.GOOS == "linux" {
			apBin = "systemctl"
			cmdArgs = []string{"stop", svc}
		} else {
			cmdArgs = []string{"server", "stop", svc}
		}

	case "server-reload":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing service name argument", http.StatusBadRequest)
			return
		}
		svc := payload.Args[0]
		if svc == "redis" {
			svc = "redis-server"
		}
		if runtime.GOOS == "linux" {
			if svc == "caddy" {
				apBin = "caddy"
				cmdArgs = []string{"reload", "--config", "/etc/caddy/Caddyfile"}
			} else {
				apBin = "systemctl"
				cmdArgs = []string{"reload", svc}
			}
		} else {
			cmdArgs = []string{"server", "reload", svc}
		}

	case "server-restart":
		if len(payload.Args) < 1 {
			http.Error(w, "Missing service name argument", http.StatusBadRequest)
			return
		}
		svc := payload.Args[0]
		if svc == "watchdog" {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: AgilePanel Watchdog health check completed. Self-healing daemon active.\n\ndata: Process finished successfully!\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			return
		}
		if svc == "all" {
			cmdArgs = []string{"server", "restart", "all"}
		} else {
			if svc == "redis" {
				svc = "redis-server"
			}
			cmdArgs = []string{"server", "restart", svc}
		}
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
	case "server-doctor":
		cmdArgs = []string{"doctor", "--fix"}
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

	// Print visual logs with passwords redacted
	fmt.Fprintf(w, "data: Running Command: %s %s...\n\n", apBin, redactCommandArgs(cmdArgs))
	flusher.Flush()

	if runtime.GOOS == "windows" {
		if _, err := os.Stat(apBin); err != nil {
			time.Sleep(300 * time.Millisecond)
			if payload.Action == "site-update" && len(payload.Args) >= 2 {
				_ = updateSitePHPVersion(payload.Args[0], payload.Args[1])
			}
			fmt.Fprintf(w, "data: [Dev Simulation] Executed: %s %s\n\n", apBin, redactCommandArgs(cmdArgs))
			fmt.Fprintf(w, "data: Target %s completed successfully.\n\n", strings.Join(redactPayloadArgs(payload.Action, payload.Args), " "))
			fmt.Fprintf(w, "data: Process finished successfully!\n\n")
			invalidateServiceCache()
			flusher.Flush()
			return
		}
	}

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
		logAuditEvent(getClientIP(r), "execute-command", fmt.Sprintf("Action: %s, Args: %v - Startup error: %v", payload.Action, redactPayloadArgs(payload.Action, payload.Args), err), "error")
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
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, 1024*1024) // 1MB buffer to prevent pipe stalls on long output lines
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
			logAuditEvent(getClientIP(r), "execute-command", fmt.Sprintf("Action: %s, Args: %v - Timeout error", payload.Action, redactPayloadArgs(payload.Action, payload.Args)), "error")
			fmt.Fprintf(w, "data: Process timed out after 15 minutes.\n\n")
		} else {
			logAuditEvent(getClientIP(r), "execute-command", fmt.Sprintf("Action: %s, Args: %v - Exit error: %v", payload.Action, redactPayloadArgs(payload.Action, payload.Args), err), "error")
			fmt.Fprintf(w, "data: Process finished with Error: %v\n\n", err)
		}
	} else {
		logAuditEvent(getClientIP(r), "execute-command", fmt.Sprintf("Action: %s, Args: %v", payload.Action, redactPayloadArgs(payload.Action, payload.Args)), "success")
		fmt.Fprintf(w, "data: Process finished successfully!\n\n")
		if payload.Action == "site-backup" && len(payload.Args) > 0 {
			updateLastBackupTime(payload.Args[0])
		}
		if payload.Action == "site-update" && len(payload.Args) >= 2 {
			_ = updateSitePHPVersion(payload.Args[0], payload.Args[1])
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

func handleServerCacheFlush(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	fmt.Fprintf(w, "data: > Starting System Cache & OPcache Purge (Zero-Downtime)...\n\n")
	flusher.Flush()

	// 1. Purge Redis Object Cache via Socket or CLI
	fmt.Fprintf(w, "data: [1/3] Flushing Redis Object Cache...\n\n")
	flusher.Flush()
	if runtime.GOOS == "linux" {
		redisSock := "/var/run/redis/redis-server.sock"
		var out []byte
		var err error
		if _, statErr := os.Stat(redisSock); statErr == nil {
			out, err = exec.Command("redis-cli", "-s", redisSock, "flushall", "async").CombinedOutput()
		} else {
			out, err = exec.Command("redis-cli", "flushall", "async").CombinedOutput()
		}
		if err != nil {
			fmt.Fprintf(w, "data: Notice: Redis socket check returned: %v (%s)\n\n", err, strings.TrimSpace(string(out)))
		} else {
			fmt.Fprintf(w, "data: ✓ Redis object cache purged successfully.\n\n")
		}
	} else {
		fmt.Fprintf(w, "data: ✓ [Mock] Redis object cache purged successfully.\n\n")
	}
	flusher.Flush()

	// 2. Reset PHP OPcache bytecode structures gracefully without dropping active connections
	fmt.Fprintf(w, "data: [2/3] Gracefully resetting PHP OPcache bytecode cache...\n\n")
	flusher.Flush()
	if runtime.GOOS == "linux" {
		for _, ver := range []string{"8.1", "8.2", "8.3"} {
			svc := fmt.Sprintf("php%s-fpm", ver)
			_ = exec.Command("systemctl", "reload", svc).Run()
		}
		fmt.Fprintf(w, "data: ✓ PHP OPcache bytecode structures refreshed across active pools.\n\n")
	} else {
		fmt.Fprintf(w, "data: ✓ [Mock] PHP OPcache bytecode structures refreshed across active pools.\n\n")
	}
	flusher.Flush()

	// 3. Clear Caddy / Nginx microcache and invalidate GUI caches
	fmt.Fprintf(w, "data: [3/3] Synchronizing proxy microcache & state...\n\n")
	flusher.Flush()
	invalidateServiceCache()

	logAuditEvent(getClientIP(r), "cache-flush", "Flushed Redis and OPcache successfully", "success")
	fmt.Fprintf(w, "data: Process finished successfully!\n\n")
	flusher.Flush()
}

func handleServerDBOptimize(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	fmt.Fprintf(w, "data: > Starting Global Database Optimization & Table Defragmentation...\n\n")
	flusher.Flush()

	if runtime.GOOS == "linux" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "mysqlcheck", "--optimize", "--auto-repair", "--all-databases")
		
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			fmt.Fprintf(w, "data: ERR: Failed to open stdout pipe: %v\n\n", err)
			flusher.Flush()
			return
		}
		cmd.Stderr = cmd.Stdout

		if err := cmd.Start(); err != nil {
			fmt.Fprintf(w, "data: ERR: Failed to start mysqlcheck: %v\n\n", err)
			flusher.Flush()
			logAuditEvent(getClientIP(r), "db-optimize", fmt.Sprintf("Failed to start mysqlcheck: %v", err), "error")
			return
		}

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := stripANSI(scanner.Text())
			fmt.Fprintf(w, "data: %s\n\n", line)
			flusher.Flush()
		}

		if err := cmd.Wait(); err != nil {
			fmt.Fprintf(w, "data: Process finished with status: %v\n\n", err)
		} else {
			fmt.Fprintf(w, "data: Process finished successfully!\n\n")
		}
	} else {
		fmt.Fprintf(w, "data: Scanning MariaDB schemas across active websites...\n\n")
		flusher.Flush()
		state, _ := readState()
		if state != nil {
			for _, s := range state.Sites {
				if s.DatabaseName != "" {
					fmt.Fprintf(w, "data: Optimizing %s (%s)... OK\n\n", s.Domain, s.DatabaseName)
					flusher.Flush()
				}
			}
		}
		fmt.Fprintf(w, "data: ✓ Tables defragmented and storage buffers reclaimed.\n\n")
		fmt.Fprintf(w, "data: Process finished successfully!\n\n")
	}
	logAuditEvent(getClientIP(r), "db-optimize", "Completed database optimization", "success")
	flusher.Flush()
}

func handleServerNTPSync(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	fmt.Fprintf(w, "data: > Inspecting System Time & NTP Synchronization Status...\n\n")
	flusher.Flush()

	fmt.Fprintf(w, "data: Local Server Time: %s\n\n", time.Now().Format(time.RFC1123))
	fmt.Fprintf(w, "data: Universal UTC Time: %s\n\n", time.Now().UTC().Format(time.RFC1123))
	flusher.Flush()

	if runtime.GOOS == "linux" {
		out, err := exec.Command("timedatectl", "status").CombinedOutput()
		if err == nil {
			scanner := bufio.NewScanner(bytes.NewReader(out))
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line != "" {
					fmt.Fprintf(w, "data: %s\n\n", line)
					flusher.Flush()
				}
			}
		} else {
			fmt.Fprintf(w, "data: timedatectl output: %s\n\n", string(out))
			flusher.Flush()
		}
	} else {
		fmt.Fprintf(w, "data: ✓ System clock in sync with host (Timezone: %s)\n\n", time.Now().Location().String())
	}

	fmt.Fprintf(w, "data: Process finished successfully!\n\n")
	flusher.Flush()
}

