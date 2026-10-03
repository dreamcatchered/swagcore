//go:build !windows

package agent

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
)

// processName читает /proc/<pid>/comm.
func processName(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return "", false
	}
	return string(bytes.TrimSpace(b)), true
}

// spawnDetachedInUnix запускает процесс в отдельной группе (setpgid),
// чтобы он пережил текущий процесс.
func spawnDetachedIn(exe string, args []string, dir string) error {
	cmd := exec.Command(exe, args[1:]...)
	cmd.Dir = dir
	return spawnDetached(cmd)
}