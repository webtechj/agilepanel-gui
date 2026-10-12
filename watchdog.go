package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

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
						ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
						var stderr bytes.Buffer
						restartCmd := exec.CommandContext(ctx, "systemctl", "restart", svc)
						restartCmd.Stderr = &stderr
						restartErr := restartCmd.Run()
						cancel()
						if restartErr != nil {
							log.Printf("Watchdog Error: Failed to restart %s: %v (stderr: %s)", svc, restartErr, stderr.String())
						}

						if errState == nil && state.Global.TelegramBotToken != "" && state.Global.TelegramChatID != "" {
							host, _ := os.Hostname()
							var msg string
							if restartErr != nil {
								msg = fmt.Sprintf("🚨 <b>AgilePanel Watchdog Alert!</b>\n\n<b>Host:</b> %s\n<b>Service:</b> <code>%s</code> is <b>DOWN</b> and auto-restart <b>FAILED</b>.\n<b>Error:</b> <code>%v</code>\n<b>Stderr:</b> <code>%s</code>\n\nPlease log in and inspect the service status.", host, svc, restartErr, stderr.String())
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
