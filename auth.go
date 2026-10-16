package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type GuiAuthConfig struct {
	Enabled      bool   `json:"enabled"`
	Username     string `json:"username,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"`
	PinHash      string `json:"pin_hash,omitempty"`
}

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
	dir := filepath.Dir(path)
	_ = os.MkdirAll(dir, 0755)

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

type SessionMetadata struct {
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
	CreatedAt time.Time `json:"created_at"`
	Expiry    time.Time `json:"expiry"`
}

// Global active sessions map: token -> expiry, and token -> metadata
var (
	sessionMutex       sync.RWMutex
	activeSessions     = make(map[string]time.Time)
	activeSessionMetas = make(map[string]SessionMetadata)
)

func createSession() string {
	return createSessionWithReq(nil)
}

func createSessionWithReq(r *http.Request) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("Critical security error: failed to generate secure session token: %v", err)
	}
	token := hex.EncodeToString(b)
	expiry := time.Now().Add(1 * time.Hour)

	ip := "127.0.0.1"
	ua := "Browser Session"
	if r != nil {
		ip = getClientIP(r)
		ua = r.UserAgent()
		if len(ua) > 60 {
			ua = ua[:60] + "..."
		}
	}

	sessionMutex.Lock()
	activeSessions[token] = expiry
	activeSessionMetas[token] = SessionMetadata{
		IP:        ip,
		UserAgent: ua,
		CreatedAt: time.Now(),
		Expiry:    expiry,
	}
	sessionMutex.Unlock()
	return token
}

type SessionDisplayItem struct {
	TokenPrefix string `json:"token_prefix"`
	TokenKey    string `json:"token_key"`
	IP          string `json:"ip"`
	UserAgent   string `json:"user_agent"`
	CreatedAt   string `json:"created_at"`
	ExpiresIn   string `json:"expires_in"`
	IsCurrent   bool   `json:"is_current"`
}

func getActiveSessionsList(currentCookieToken string) []SessionDisplayItem {
	sessionMutex.RLock()
	defer sessionMutex.RUnlock()

	now := time.Now()
	var list []SessionDisplayItem
	for token, expiry := range activeSessions {
		if now.After(expiry) {
			continue
		}
		meta, ok := activeSessionMetas[token]
		ip := "127.0.0.1"
		ua := "Browser Client"
		createdAt := now
		if ok {
			ip = meta.IP
			ua = meta.UserAgent
			createdAt = meta.CreatedAt
		}

		prefix := token
		if len(prefix) > 8 {
			prefix = prefix[:8] + "..."
		}

		remMin := int(expiry.Sub(now).Minutes())
		remStr := fmt.Sprintf("%dm", remMin)
		if remMin <= 0 {
			remStr = "<1m"
		}

		tokenKey := token
		if len(tokenKey) > 12 {
			tokenKey = tokenKey[:12]
		}

		list = append(list, SessionDisplayItem{
			TokenPrefix: prefix,
			TokenKey:    tokenKey,
			IP:          ip,
			UserAgent:   ua,
			CreatedAt:   createdAt.Format("Jan 02 15:04"),
			ExpiresIn:   remStr,
			IsCurrent:   token == currentCookieToken,
		})
	}
	return list
}

func revokeSessionByKey(tokenKey string) bool {
	sessionMutex.Lock()
	defer sessionMutex.Unlock()

	for token := range activeSessions {
		if strings.HasPrefix(token, tokenKey) {
			delete(activeSessions, token)
			delete(activeSessionMetas, token)
			return true
		}
	}
	return false
}

func revokeAllOtherSessions(currentCookieToken string) int {
	sessionMutex.Lock()
	defer sessionMutex.Unlock()

	count := 0
	for token := range activeSessions {
		if token != currentCookieToken {
			delete(activeSessions, token)
			delete(activeSessionMetas, token)
			count++
		}
	}
	return count
}

type TokenBucket struct {
	tokens     float64
	lastRefill time.Time
}

var (
	rateLimiterMutex sync.Mutex
	rateLimiterMap   = make(map[string]*TokenBucket)

	loginAttemptsMutex sync.Mutex
	loginAttempts      = make(map[string][]time.Time)
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

func logAuditEvent(ip, action, details, status string) {
	entry := fmt.Sprintf("[%s] IP=%s Action=%s Status=%s Details=%s\n",
		time.Now().Format(time.RFC3339), ip, action, status, details)

	var logPath string
	if runtime.GOOS == "windows" {
		logPath = "./audit.log"
	} else {
		logPath = "/var/log/agilepanel-audit.log"
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err == nil {
		defer f.Close()
		_, _ = f.WriteString(entry)
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

// sessionAuth middleware verifies the secondary session token if GUI lock is enabled
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

	token := createSessionWithReq(r)
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

	token := createSessionWithReq(r)
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
	if err == nil && cookie.Value != "" {
		sessionMutex.Lock()
		delete(activeSessions, cookie.Value)
		delete(activeSessionMetas, cookie.Value)
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

	if r.URL.Query().Get("basic") == "1" {
		w.Header().Set("WWW-Authenticate", `Basic realm="AgilePanel Dashboard"`)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("Logged out successfully"))
		return
	}

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
