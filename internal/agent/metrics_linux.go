//go:build linux

// CPU и память для Linux: /proc/stat, /proc/meminfo.
package agent

import (
	"os"
	"strconv"
	"strings"
)

// cpuPercent считает загрузку CPU как разницу между замерами /proc/stat.
func cpuPercent() float64 {
	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}
	line := strings.SplitN(string(raw), "\n", 2)[0]
	fields := strings.Fields(strings.TrimPrefix(line, "cpu "))
	if len(fields) < 4 {
		return 0
	}
	var vals []uint64
	for _, f := range fields {
		v, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			break
		}
		vals = append(vals, v)
	}
	total := vals[0] + vals[1] + vals[2] + vals[3]
	for _, v := range vals[4:] {
		total += v
	}
	idle := vals[3]

	diffTotal := total - lastCPU[0]
	diffIdle := idle - lastCPU[1]
	lastCPU[0] = total
	lastCPU[1] = idle
	if diffTotal == 0 {
		return 0
	}
	pct := float64(diffTotal-diffIdle) / float64(diffTotal) * 100
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct
}

// memInfo читает /proc/meminfo (значения в байтах).
func memInfo() (used, total uint64) {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	var available uint64
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			total = v * 1024
		case "MemAvailable:":
			available = v * 1024
		}
	}
	if total == 0 {
		return 0, 0
	}
	return total - available, total
}
