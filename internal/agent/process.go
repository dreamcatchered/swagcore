// Process-mode: запуск нативных приложений без Docker.
// Агент скачивает артефакт, запускает команду в фоновом режиме (detached),
// пишет pid + метаданные и следит за процессом (watchdog перезапускает при падении).
package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dreamcatchered/swagcore/internal/model"
)

// procMeta — метаданные запущенного process-проекта.
type procMeta struct {
	PID      int               `json:"pid"`
	Started  string            `json:"started"`
	Command  string            `json:"command"`
	WorkDir  string            `json:"workdir"`
	Artifact string            `json:"artifact,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}


// procDir — каталог проекта.
func procDir(name string) string {
	return filepath.Join(appsDir(), containerName(name))
}

// procMetaPath — путь к meta.json.
func procMetaPath(name string) string {
	return filepath.Join(procDir(name), "meta.json")
}

// readProcMeta читает метаданные запущенного процесса.
func readProcMeta(name string) (*procMeta, error) {
	raw, err := os.ReadFile(procMetaPath(name))
	if err != nil {
		return nil, err
	}
	var m procMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// RunProcess — реализация деплоя для mode=process.
func RunProcess(task model.DeployTask) (string, error) {
	dir := procDir(task.Name)
	logPath := filepath.Join(dir, "app.log")

	// каталог
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	// артефакт: скачать/распаковать если задан
	if task.ArtifactURL != "" {
		if err := fetchArtifact(task.ArtifactURL, task.ArtifactSHA, task.Name, currentToken()); err != nil {
			return "", fmt.Errorf("artifact: %w", err)
		}
	}

	// остановить предыдущий экземпляр, если жив
	stopProcess(task.Name)
	stopStaleProcesses(dir)
	time.Sleep(1200 * time.Millisecond)

	if strings.TrimSpace(task.Command) == "" {
		return "", fmt.Errorf("process mode требует command")
	}

	// команда: shell-парсинг
	argv := parseCommand(task.Command)
	if len(argv) == 0 {
		return "", fmt.Errorf("bad command %q", task.Command)
	}
	argv[0] = resolveBin(argv[0], dir)

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range task.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", fmt.Errorf("open log: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := spawnDetached(cmd); err != nil {
		_ = logFile.Close()
		return "", fmt.Errorf("start: %w", err)
	}
	_ = logFile.Close()

	meta := procMeta{
		PID:      cmd.Process.Pid,
		Started:  time.Now().Format(time.RFC3339),
		Command:  task.Command,
		WorkDir:  dir,
		Artifact: task.ArtifactURL,
		Env:      task.Env,
	}
	raw, _ := json.MarshalIndent(meta, "", "  ")
	_ = os.WriteFile(procMetaPath(task.Name), raw, 0o644)

	// проверим, что процесс не умер мгновенно
	time.Sleep(800 * time.Millisecond)
	if !pidAlive(meta.PID) {
		return "", fmt.Errorf("process exited immediately\n%s", logTail(logPath, 20))
	}
	return fmt.Sprintf("pid=%d dir=%s", meta.PID, dir), nil
}

// StopProcess — остановка (remove=true — удалить каталог и мету).
func StopProcess(name string, remove bool) error {
	stopProcess(name)
	if remove {
		_ = os.RemoveAll(procDir(name))
	}
	return nil
}

// resolveBin — если бинарник относительный и лежит в dir, вернуть полный путь.
func resolveBin(name, dir string) string {
	if filepath.IsAbs(name) {
		return name
	}
	cand := filepath.Join(dir, name)
	if _, err := os.Stat(cand); err == nil {
		return cand
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		if _, err := os.Stat(cand + ".exe"); err == nil {
			return cand + ".exe"
		}
	}
	return name
}

// stopProcess — тихая остановка по meta.json.
func stopProcess(name string) {
	m, err := readProcMeta(name)
	if err != nil {
		return
	}
	if pidAlive(m.PID) {
		killTree(m.PID)
	}
	_ = os.Remove(procMetaPath(name))
}

// stopStaleProcesses — страховка от гонок: убивает все процессы, чья командная
// строка содержит каталог проекта (meta.json мог устареть и прежний экземпляр
// продолжал держать порт, из-за чего новый падал с EADDRINUSE).
func stopStaleProcesses(dir string) {
	switch runtime.GOOS {
	case "windows":
		dirPat := strings.ReplaceAll(strings.ReplaceAll(dir, `\`, `\\`), "'", "''")
		ps := fmt.Sprintf(`Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -like '*%s*' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }`, dirPat)
		_ = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps).Run()
	default:
		_ = exec.Command("pkill", "-f", dir).Run()
	}
}

// logTail — последние n строк лога.
func logTail(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ListProcesses возвращает фактическое состояние всех process-проектов на ноде.
// Используется в recon-ответе: сервер сверяет его с БД и передеплоит то, что
// упало (в т.ч. после ребута, когда pid из meta.json уже не существует).
func ListProcesses() []model.ProcessInfo {
	out := []model.ProcessInfo{}
	entries, err := os.ReadDir(appsDir())
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), containerPrefix) {
			continue
		}
		name := strings.TrimPrefix(e.Name(), containerPrefix)
		m, err := readProcMeta(name)
		if err != nil {
			continue // не запущен / нет метаданных
		}
		out = append(out, model.ProcessInfo{
			Name:    name,
			Unit:    e.Name(),
			PID:     m.PID,
			Alive:   pidAlive(m.PID),
			Started: m.Started,
		})
	}
	return out
}

// WatchProcess — воркер-вотчдог: раз в 20с проверяет все process-проекты,
// перезапускает упавшие той же командой.
func WatchProcess() {
	// небольшая пауза перед первой проверкой, чтобы дать деплою время стартовать
	time.Sleep(20 * time.Second)
	for {
		restartDeadProjects()
		time.Sleep(20 * time.Second)
	}
}

// restartDeadProjects — один проход watchdog'а по всем process-проектам.
func restartDeadProjects() {
	entries, err := os.ReadDir(appsDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := strings.TrimPrefix(e.Name(), containerPrefix)
		m, err := readProcMeta(name)
		if err != nil {
			continue // не запущен
		}
		if pidAlive(m.PID) {
			continue
		}
		task := model.DeployTask{
			Name:        name,
			Mode:        "process",
			Command:     m.Command,
			ArtifactURL: m.Artifact,
			Env:         m.Env,
		}
		if _, err := RunProcess(task); err != nil {
			log.Printf("watchdog: %s restart failed: %v", name, err)
		} else {
			log.Printf("watchdog: %s restarted (was dead)", name)
		}
	}
}
