//go:build !linux && !windows

package agent

// diskUsage — заглушка для прочих ОС (не Linux/Windows).
func diskUsage(path string) (used, total float64) {
	return 0, 0
}
