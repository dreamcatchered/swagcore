//go:build windows

// CPU и память для Windows: GlobalMemoryStatusEx, GetSystemTimes (kernel32, без CGO).
package agent

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGlobalMem = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
	procSysTimes  = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemTimes")
)

// memoryStatusEx — структура MEMORYSTATUSEX.
type memoryStatusEx struct {
	dwLength                uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

// memInfo возвращает used/total в байтах.
func memInfo() (used, total uint64) {
	var ms memoryStatusEx
	ms.dwLength = uint32(unsafe.Sizeof(ms))
	r1, _, _ := procGlobalMem.Call(uintptr(unsafe.Pointer(&ms)))
	if r1 == 0 {
		return 0, 0
	}
	return ms.ullTotalPhys - ms.ullAvailPhys, ms.ullTotalPhys
}

// fileTime — 64-битное время FILETIME.
type fileTime struct {
	LowDateTime  uint32
	HighDateTime uint32
}

func (f fileTime) u64() uint64 {
	return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime)
}

var prevIdle, prevKernel, prevUser uint64

// cpuPercent считает загрузку CPU как разницу GetSystemTimes между замерами.
func cpuPercent() float64 {
	var idle, kernel, user fileTime
	r1, _, _ := procSysTimes.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if r1 == 0 {
		return 0
	}
	idleU := idle.u64()
	kernelU := kernel.u64()
	userU := user.u64()

	diffIdle := idleU - prevIdle
	diffTotal := (kernelU + userU) - (prevKernel + prevUser)
	prevIdle = idleU
	prevKernel = kernelU
	prevUser = userU

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
