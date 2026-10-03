//go:build windows

package agent

import "golang.org/x/sys/windows"

// uptimeSec через GetTickCount64 (kernel32).
func uptimeSec() uint64 {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	proc := kernel32.NewProc("GetTickCount64")
	v, _, _ := proc.Call()
	return uint64(v / 1000)
}
