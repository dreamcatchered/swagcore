// Executor выполняет задачи на ноде через docker CLI.
package agent

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dreamcatchered/swagcore/internal/model"
)

// containerPrefix — общий префикс имён контейнеров и каталогов проектов.
const containerPrefix = "swag_"

// containerName — детерминированное имя контейнера проекта.
func containerName(projectName string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9_.-]`)
	return containerPrefix + re.ReplaceAllString(strings.ToLower(projectName), "_")
}

// execDocker запускает docker CLI с аргументами, возвращает stdout/stderr.
func execDocker(args ...string) (string, string, error) {
	cmd := exec.Command("docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

// DockerAvailable проверяет доступность docker CLI.
func DockerAvailable() bool {
	_, _, err := execDocker("version", "--format", "{{.Server.Version}}")
	return err == nil
}

// Pull подтягивает образ. Если образ уже есть локально (например, собран
// на ноде или загружен через docker load) — pull пропускается: так работают
// локальные образы без реестра.
func Pull(image string) (string, error) {
	if out, errOut, err := execDocker("image", "inspect", image); err == nil {
		return "image " + image + " exists locally, skip pull: " + truncate(out+errOut, 120), nil
	}
	out, errOut, err := execDocker("pull", image)
	if err != nil {
		return out + errOut, fmt.Errorf("docker pull: %w", err)
	}
	return out, nil
}

// Run запускает проект: docker-режим или process-режим (нативное приложение).
func Run(task model.DeployTask) (string, error) {
	deployMu.Lock()
	defer deployMu.Unlock()
	if task.Mode == "process" {
		return RunProcess(task)
	}
	name := containerName(task.Name)

	// останавливаем и удаляем предыдущую версию
	_, _, _ = execDocker("rm", "-f", name)

	args := []string{"run", "-d",
		"--name", name,
		"--restart", "unless-stopped",
		"--label", "swagcore.project=" + task.Name,
		"--label", "swagcore.managed=true",
	}

	// артефакт: скачиваем tar.gz и распаковываем в каталог проекта
	if task.ArtifactURL != "" && task.MountPath != "" {
		if err := fetchArtifact(task.ArtifactURL, task.ArtifactSHA, name, currentToken()); err != nil {
			return "", fmt.Errorf("artifact: %w", err)
		}
		dataDir := filepath.Join(sitesDir(), name)
		// hostname.txt — нода пишет своё имя рядом с артефактом, чтобы проект
		// мог показать, на какой машине он запущен (statusboard и т.п.)
		host, _ := os.Hostname()
		if host != "" {
			_ = os.WriteFile(filepath.Join(dataDir, "hostname.txt"), []byte(host+"\n"), 0o644)
		}
		args = append(args, "-v", dataDir+":"+task.MountPath)
	}

	if task.Memory != "" {
		args = append(args, "--memory", task.Memory)
	}
	if task.CPUs != "" {
		args = append(args, "--cpus", task.CPUs)
	}
	for _, p := range task.Ports {
		args = append(args, "-p", normalizePort(p))
	}
	for k, v := range task.Env {
		args = append(args, "-e", k+"="+v)
	}
	// служебные env о ноде (проект может показать, где он живёт)
	if host, err := os.Hostname(); err == nil && host != "" {
		args = append(args, "-e", "SWAG_NODE_HOST="+host)
	}
	for _, v := range task.Volumes {
		args = append(args, "-v", v)
	}
	args = append(args, task.Image)
	if task.Command != "" {
		args = append(args, parseCommand(task.Command)...)
	}

	out, errOut, err := execDocker(args...)
	if err != nil {
		return out + errOut, fmt.Errorf("docker run: %w", err)
	}
	return out, nil
}

// Stop останавливает проект (docker или process).
func Stop(projectName string) error {
	if isProcessProject(projectName) {
		return StopProcess(projectName, false)
	}
	name := containerName(projectName)
	_, errOut, err := execDocker("stop", name)
	if err != nil {
		return fmt.Errorf("docker stop: %w (%s)", err, strings.TrimSpace(errOut))
	}
	return nil
}

// isProcessProject — проект управляется в process-режиме (есть meta.json).
func isProcessProject(projectName string) bool {
	_, err := os.Stat(procMetaPath(projectName))
	return err == nil
}

// processLogs — tail app.log для process-проекта.
func processLogs(projectName, tail string) string {
	n := 200
	if v, err := strconv.Atoi(strings.TrimSpace(tail)); err == nil && v > 0 {
		n = v
	}
	return logTail(filepath.Join(procDir(projectName), "app.log"), n)
}

// Remove удаляет проект (docker-контейнер или process-каталог).
func Remove(projectName string) error {
	if isProcessProject(projectName) {
		return StopProcess(projectName, true)
	}
	name := containerName(projectName)
	_, errOut, err := execDocker("rm", "-f", name)
	if err != nil {
		return fmt.Errorf("docker rm: %w (%s)", err, strings.TrimSpace(errOut))
	}
	return nil
}

// State возвращает статус проекта: running/stopped/missing (docker или process).
func State(projectName string) string {
	if isProcessProject(projectName) {
		m, err := readProcMeta(projectName)
		if err != nil || !pidAlive(m.PID) {
			return "missing"
		}
		return "running"
	}
	name := containerName(projectName)
	stdout, _, err := execDocker("inspect", "-f", "{{.State.Status}}", name)
	if err != nil || stdout == "" {
		return "missing"
	}
	return strings.TrimSpace(stdout)
}

// Logs возвращает логи проекта (tail): app.log для process, docker logs иначе.
func Logs(projectName, tail string) (string, error) {
	if isProcessProject(projectName) {
		return processLogs(projectName, tail), nil
	}
	name := containerName(projectName)
	if tail == "" {
		tail = "200"
	}
	stdout, errOut, err := execDocker("logs", "--tail", tail, name)
	if err != nil && stdout == "" {
		return "", fmt.Errorf("docker logs: %w (%s)", err, strings.TrimSpace(errOut))
	}
	return stdout + errOut, nil
}

// LogsFollow возвращает reader потока логов контейнера.
func LogsFollow(projectName string) (*bufio.Reader, func(), error) {
	name := containerName(projectName)
	cmd := exec.Command("docker", "logs", "--tail", "50", "-f", name)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	cancel := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	return bufio.NewReader(stdout), cancel, nil
}

// normalizePort приводит спецификацию порта к безопасной форме.
// "80" (только контейнерный) и "80:80" (привилегированный/занятый хост-порт)
// -> "случайныйСвободный:80": низкие хост-порты на нодах часто заняты (nginx и
// т.п.). Конкретные высокие хост-порты вида "8200:80" оставляем как указано.
func normalizePort(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || strings.Contains(p, "/") {
		return p
	}
	if i := strings.Index(p, ":"); i >= 0 {
		host := strings.TrimSpace(p[:i])
		if n, err := strconv.Atoi(host); err != nil || n <= 0 || n >= 1024 {
			return p // "8200:80", ":80" и прочее — не трогаем
		}
		// низкий хост-порт (<1024) — заменяем на свободный
		return freeHostPort() + p[i:]
	}
	return freeHostPort() + ":" + p
}

// freeHostPort находит свободный TCP-порт у ОС.
func freeHostPort() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "0" // пусть docker сам выберет ephemeral-порт
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return strconv.Itoa(port)
}

// deployMu — сериализует деплои на ноде: параллельные docker run одного
// проекта (реконсилер + drift) конфликтуют за имя контейнера.
var deployMu sync.Mutex

// ListManaged возвращает список контейнеров платформы на этой ноде.
func ListManaged() ([]model.ContainerInfo, error) {
	stdout, errOut, err := execDocker(
		"ps", "-a",
		"--filter", "label=swagcore.managed=true",
		"--format", "{{.Names}}|{{.Image}}|{{.Status}}|{{.Label \"swagcore.project\"}}",
	)
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w (%s)", err, strings.TrimSpace(errOut))
	}
	var out []model.ContainerInfo
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		info := model.ContainerInfo{Name: parts[0], Image: parts[1], Status: parts[2]}
		if len(parts) > 3 {
			info.Project = parts[3]
		}
		out = append(out, info)
	}
	return out, nil
}

// RunningContainers — число запущенных контейнеров (для метрик).
func RunningContainers() int {
	stdout, _, err := execDocker("ps", "-q")
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}


// fetchArtifact скачивает tar.gz артефакта и распаковывает в sitesDir/<name>.
// Скачивание в .tmp + атомарная распаковка, чтобы при сбое не оставить битые файлы.
// ВАЖНО: projectName приходит уже в виде container-name ("swag_xxx") — не оборачиваем повторно.
// fetchArtifact скачивает tar.gz артефакта проекта и распаковывает.
// Заголовок X-Node-Token обязателен: с v0.6.0 /artifacts/ закрыт авторизацией
// (раньше исходники проектов скачивал кто угодно по ссылке).
func fetchArtifact(url, shaSum, projectName, token string) error {
	dir := filepath.Join(sitesDir(), projectName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tarPath := filepath.Join(dir, "artifact.tar.gz")
	if err := download(url, tarPath, token); err != nil {
		return err
	}
	if shaSum != "" {
		got, err := fileSHA256(tarPath)
		if err != nil {
			return err
		}
		if got != shaSum {
			return fmt.Errorf("artifact checksum mismatch")
		}
	}
	// распаковка: tar есть на всех Linux и в Windows 10+
	cmd := exec.Command("tar", "-xzf", tarPath, "-C", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tar: %v: %s", err, strings.TrimSpace(string(out)))
	}
	os.Remove(tarPath)
	return nil
}

func download(url, path, token string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("X-Node-Token", token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("artifact http %d", resp.StatusCode)
	}
	f, err := os.Create(path + ".tmp")
	if err != nil {
		return err
	}
	_, err = io.Copy(f, resp.Body)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(path+".tmp", path)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
