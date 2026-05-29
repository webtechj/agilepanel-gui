package main

import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
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

	status := map[string]interface{}{
		"cpu":       getCPU(),
		"ram":       getRAM(),
		"disk":      getDisk(),
		"services":  services,
		"siteCount": len(state.Sites),
		"global": map[string]interface{}{
			"admin_user":         state.Global.AdminUser,
			"default_php":        state.Global.DefaultPHPVersion,
			"supported_php":      state.Global.SupportedPHPVersions,
			"has_credentials":    state.Global.AdminUser != "" && state.Global.AdminPasswordHash != "",
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
		cmdArgs = []string{"site", "delete", payload.Args[0]}
	case "site-lock":
		cmdArgs = []string{"site", "lock", payload.Args[0]}
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
	
	// Set confirmation flow overrides since we can't do interactive prompting inside UI easily
	// We pass yes via stdin or environment if needed, but for locked operations we will force prompt bypassing
	// For `delete` and `lock` commands, double confirmation input is needed.
	// Since AgilePanel commands normally prompt for typed confirmations (e.g. typing domain name),
	// we feed the confirmation inputs directly to stdin!
	if payload.Action == "site-delete" || payload.Action == "site-lock" {
		stdin, err := cmd.StdinPipe()
		if err == nil {
			go func() {
				defer stdin.Close()
				// Send 'y' for standard confirmation, and domain name for secondary double check
				domain := payload.Args[0]
				fmt.Fprintln(stdin, "y")
				fmt.Fprintln(stdin, domain)
			}()
		}
	}

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

func main() {
	// Initialize CPU tracker baseline
	getCPU()

	// Routers
	http.HandleFunc("/", basicAuth(serveStatic("index.html", "text/html")))
	http.HandleFunc("/style.css", serveStatic("style.css", "text/css"))
	http.HandleFunc("/app.js", serveStatic("app.js", "application/javascript"))

	http.HandleFunc("/api/status", basicAuth(handleStatusAPI))
	http.HandleFunc("/api/sites", basicAuth(handleSitesAPI))
	http.HandleFunc("/api/action", basicAuth(handleCommandExecuteAPI))

	log.Println("AgilePanel GUI Dashboard starting on http://localhost:8889...")
	if err := http.ListenAndServe(":8889", nil); err != nil {
		log.Fatalf("Server startup failed: %v", err)
	}
}

