//go:build !linux && !windows

// Заглушки CPU/памяти для прочих ОС.
package agent

func cpuPercent() float64           { return 0 }
func memInfo() (used, total uint64) { return 0, 0 }
