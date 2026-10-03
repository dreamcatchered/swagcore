//go:build linux

package agent

import (
	"syscall"
)

func gb(b uint64) float64 {
	return float64(b) / (1024 * 1024 * 1024)
}

// diskUsage возвращает used/total в GB для пути.
func diskUsage(path string) (used, total float64) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0
	}
	bsize := uint64(st.Bsize)
	totalB := st.Blocks * bsize
	freeB := st.Bfree * bsize
	return gb(totalB - freeB), gb(totalB)
}
