//go:build !linux && !windows

package agent

// uptimeSec — заглушка для прочих ОС.
func uptimeSec() uint64 { return 0 }
