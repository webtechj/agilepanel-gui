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
