//go:build windows

package agent

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// spawnDetached запускает процесс без окна (не мешает пользователю ноды).
func spawnDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP,
	}
	return cmd.Start()
}

// pidAlive проверяет, жив ли процесс.
// ВАЖНО: одного успешного OpenProcess мало — мёртвый процесс без родителя-реапера
// может остаться как zombie-запись. Дополнительно проверяем ExitCode != STILL_ACTIVE.
func pidAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == uint32(259) // STILL_ACTIVE
}

// killTree убивает дерево процессов (приложение может плодить детей).
func killTree(pid int) {
	cmd := exec.Command("taskkill", "/F", "/T", "/PID", itoa(pid))
	_ = cmd.Run()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
