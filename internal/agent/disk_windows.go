//go:build windows

// Диск для Windows: GetDiskFreeSpaceEx по корню системного диска.
package agent

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procDiskFree = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// diskUsage возвращает used/total в ГБ.
func diskUsage(path string) (usedGB, totalGB float64) {
	if path == "/" {
		path = `C:\`
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0
	}
	var freeBytes, totalBytes, totalFree uint64
	r1, _, _ := procDiskFree.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeBytes)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r1 == 0 {
		return 0, 0
	}
	used := totalBytes - freeBytes
	gb := float64(1024 * 1024 * 1024)
	return float64(used) / gb, float64(totalBytes) / gb
}
