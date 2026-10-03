package ai

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// runShell выполняет команду в каталоге (для воркспейса агента).
func runShell(dir, command string, timeoutSec int) (string, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", command)
	} else {
		cmd = exec.Command("bash", "-c", command)
	}
	cmd.Dir = dir
	out, err := execWithTimeout(cmd, time.Duration(timeoutSec)*time.Second)
	return string(out), err
}

// execWithTimeout — запуск с таймаутом.
func execWithTimeout(cmd *exec.Cmd, timeout time.Duration) ([]byte, error) {
	type result struct {
		out []byte
		err error
	}
	ch := make(chan result, 1)
	var combined []byte
	cmd.Stdout = &limitWriter{buf: &combined, max: 1 << 21}
	cmd.Stderr = cmd.Stdout
	startErr := cmd.Start()
	if startErr != nil {
		return nil, startErr
	}
	go func() {
		err := cmd.Wait()
		ch <- result{combined, err}
	}()
	select {
	case r := <-ch:
		return r.out, r.err
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return combined, fmt.Errorf("timeout %s", timeout)
	}
}

// limitWriter — буфер с ограничением (2 МБ, чтобы не съесть память).
type limitWriter struct {
	buf *[]byte
	max int
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if len(*w.buf)+len(p) <= w.max {
		*w.buf = append(*w.buf, p...)
	} else {
		free := w.max - len(*w.buf)
		if free > 0 {
			*w.buf = append(*w.buf, p[:free]...)
		}
	}
	return len(p), nil
}

// sha256Hex — хеш для artifact_sha.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// artifactsDir — каталог артефактов сервера.
func (a *Agent) artifactsDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), "swagcore-artifacts")
	}
	return "/opt/swagcore/data/artifacts"
}
