//go:build linux

package agent

import (
	"os"
	"strconv"
	"strings"
)

// uptimeSec — /proc/uptime.
func uptimeSec() uint64 {
	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(raw))
	if len(f) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0
	}
	return uint64(v)
}
