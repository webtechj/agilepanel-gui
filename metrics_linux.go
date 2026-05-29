//go:build linux

package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

type MemInfo struct {
	Total float64 `json:"total"`
	Used  float64 `json:"used"`
	Pct   float64 `json:"pct"`
}

func getRAM() MemInfo {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return MemInfo{}
	}
	defer file.Close()

	var memTotal, memAvailable float64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		var val float64
		if strings.HasPrefix(line, "MemTotal:") {
			fmt.Sscanf(line, "MemTotal: %f", &val)
			memTotal = val / 1024.0 / 1024.0 // to GB
		} else if strings.HasPrefix(line, "MemAvailable:") {
			fmt.Sscanf(line, "MemAvailable: %f", &val)
			memAvailable = val / 1024.0 / 1024.0 // to GB
		}
	}

	if memTotal > 0 {
		used := memTotal - memAvailable
		return MemInfo{
			Total: memTotal,
			Used:  used,
			Pct:   (used / memTotal) * 100.0,
		}
	}
	return MemInfo{}
}

func getDisk() MemInfo {
	var stat syscall.Statfs_t
	wd, err := os.Getwd()
	if err != nil {
		wd = "/"
	}
	err = syscall.Statfs(wd, &stat)
	if err != nil {
		return MemInfo{}
	}

	total := float64(stat.Blocks) * float64(stat.Bsize) / 1024.0 / 1024.0 / 1024.0 // GB
	free := float64(stat.Bfree) * float64(stat.Bsize) / 1024.0 / 1024.0 / 1024.0   // GB
	used := total - free

	var pct float64
	if total > 0 {
		pct = (used / total) * 100.0
	}

	return MemInfo{
		Total: total,
		Used:  used,
		Pct:   pct,
	}
}

var lastUser, lastNice, lastSystem, lastIdle, lastIowait, lastIrq, lastSoftirq, lastSteal uint64
var lastCalculated float64

func getCPU() float64 {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0
	}
	defer file.Close()

	var user, nice, system, idle, iowait, irq, softirq, steal uint64
	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "cpu ") {
			fmt.Sscanf(line, "cpu %d %d %d %d %d %d %d %d", &user, &nice, &system, &idle, &iowait, &irq, &softirq, &steal)
		}
	}

	prevIdle := lastIdle + lastIowait
	idleTime := idle + iowait

	prevNonIdle := lastUser + lastNice + lastSystem + lastIrq + lastSoftirq + lastSteal
	nonIdle := user + nice + system + irq + softirq + steal

	prevTotal := prevIdle + prevNonIdle
	total := idleTime + nonIdle

	totalDiff := total - prevTotal
	idleDiff := idleTime - prevIdle

	lastUser = user
	lastNice = nice
	lastSystem = system
	lastIdle = idle
	lastIowait = iowait
	lastIrq = irq
	lastSoftirq = softirq
	lastSteal = steal

	if totalDiff > 0 {
		lastCalculated = float64(totalDiff-idleDiff) / float64(totalDiff) * 100.0
	}
	return lastCalculated
}

func getServiceStatus(service string) bool {
	cmd := exec.Command("systemctl", "is-active", service)
	err := cmd.Run()
	return err == nil
}
