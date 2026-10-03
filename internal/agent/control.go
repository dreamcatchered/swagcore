// Управляющие операции агента: exec, power, screenshot — кроссплатформенно.
package agent

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/dreamcatchered/swagcore/internal/model"
)

// multiMonitor сообщает, снимает ли takeScreenshot все подключённые экраны.
func multiMonitor() bool { return runtime.GOOS == "windows" }

// displayEnv — окружение для доступа к графической сессии (Linux/X11).
func displayEnv() []string {
	if runtime.GOOS != "linux" {
		return nil
	}
	env := os.Environ()
	for _, k := range []string{"DISPLAY", "WAYLAND_DISPLAY", "XAUTHORITY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// ResultSender — функция отправки Result на сервер.
type ResultSender func(model.Result)

// RunExec выполняет команду через shell ОС и отправляет результат.
func RunExec(task model.ExecTask, send ResultSender, requestID string) {
	res := model.Result{RequestID: requestID, Kind: "exec", Command: task.Command}
	out, err := runShell(task.Command, task.TimeoutSec)
	res.Output = truncate(out, 8000)
	if err != nil {
		res.Error = err.Error()
	}
	res.OK = err == nil
	send(res)
}

// runShell запускает команду через sh (unix) или cmd /c (windows).
func runShell(command string, timeoutSec int) (string, error) {
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	out := &strings.Builder{}
	errOut := &strings.Builder{}
	cmd.Stdout = out
	cmd.Stderr = errOut
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		res := out.String() + errOut.String()
		if err != nil {
			return res, err
		}
		return res, nil
	case <-time.After(time.Duration(timeoutSec) * time.Second):
		_ = cmd.Process.Kill()
		return out.String() + errOut.String(), fmt.Errorf("timeout after %ds", timeoutSec)
	}
}

// RunPower выполняет reboot/shutdown ноды.
func RunPower(task model.PowerTask, send ResultSender) {
	res := model.Result{Kind: "power", Action: task.Action}
	var cmd *exec.Cmd
	switch task.Action {
	case "reboot":
		if runtime.GOOS == "windows" {
			cmd = exec.Command("shutdown", "/r", "/t", "5")
		} else {
			cmd = exec.Command("shutdown", "-r", "+0")
		}
	case "shutdown":
		if runtime.GOOS == "windows" {
			cmd = exec.Command("shutdown", "/s", "/t", "5")
		} else {
			cmd = exec.Command("shutdown", "-h", "+0")
		}
	default:
		res.Error = "unknown action"
		send(res)
		return
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		res.Error = truncate(string(out)+err.Error(), 500)
	} else {
		res.OK = true
		res.Output = "command issued; the node will " + task.Action + " shortly"
	}
	send(res)
}

// RunScreenshot делает скриншот экрана и отправляет на сервер.
func RunScreenshot(send ResultSender, requestID string, upload func(filename string, data []byte) error) {
	res := model.Result{RequestID: requestID, Kind: "screenshot"}
	data, err := takeScreenshot()
	if err != nil {
		res.Error = err.Error()
		send(res)
		return
	}
	name := fmt.Sprintf("node-shot-%d-%04x.png", time.Now().Unix(), time.Now().UnixNano()&0xffff)
	if err := upload(name, data); err != nil {
		res.Error = "upload: " + err.Error()
		send(res)
		return
	}
	res.OK = true
	res.File = name
	res.Output = fmt.Sprintf("%d bytes", len(data))
	send(res)
}

// takeScreenshot — платформенная реализация (см. screenshot_*.go).
func decodeB64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}

var _ = os.Getenv
