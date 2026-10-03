//go:build !linux && !windows

package agent

import "os/exec"

// Заглушки для прочих ОС.
func spawnDetached(cmd *exec.Cmd) error { return cmd.Start() }
func pidAlive(pid int) bool             { return false }
func killTree(pid int)                  {}
