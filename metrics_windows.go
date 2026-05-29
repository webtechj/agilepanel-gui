//go:build windows

package main

type MemInfo struct {
	Total float64 `json:"total"`
	Used  float64 `json:"used"`
	Pct   float64 `json:"pct"`
}

func getRAM() MemInfo {
	return MemInfo{Total: 8.0, Used: 2.4, Pct: 30.0}
}

func getDisk() MemInfo {
	return MemInfo{Total: 100.0, Used: 45.0, Pct: 45.0}
}

func getCPU() float64 {
	return 12.5
}

func getServiceStatus(service string) bool {
	return true
}

func getUptime() string {
	return "5h 32m"
}

func getLoadAverages() []float64 {
	return []float64{0.12, 0.08, 0.05}
}

func getTCPConnections() int {
	return 35
}

func getTopProcesses() []map[string]interface{} {
	return []map[string]interface{}{
		{"pid": 1204, "cpu": 1.2, "mem": 4.5, "comm": "mariadbd"},
		{"pid": 1308, "cpu": 0.8, "mem": 2.1, "comm": "php-fpm"},
		{"pid": 1540, "cpu": 0.4, "mem": 0.8, "comm": "caddy"},
	}
}

