//go:build linux

package agent

import (
	"os/exec"
	"syscall"
	"time"
)

// spawnDetached запускает процесс в отдельной группе (легко убить дерево).
func spawnDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd.Start()
}

// pidAlive проверяет, жив ли процесс.
func pidAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// killTree убивает группу процессов: сначала TERM, потом KILL.
func killTree(pid int) {
	// группа = pid (Setpgid без Pgid → ребёнок становится лидером группы)
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
