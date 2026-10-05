// Selfupdate: скачивание нового бинарника агента с сервера и атомарная подмена.
package agent

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/dreamcatchered/swagcore/internal/model"
)

// StartAutoUpdate запускает фоновую проверку версии сервера каждый 30 минут.
// Сервер отдаёт /download/agent/version.txt со строкой "<Version>+<Build>"
// (напр. "0.6.0+v0600"). Build меняется при КАЖДОЙ пересборке бинарников,
// поэтому пересборка всегда доезжает до нод — даже если номер версии прежний.
func StartAutoUpdate(serverURL string) {
	go func() {
		// Первая проверка — через 30 с после старта, затем раз в 30 минут.
		//
		// Раньше первая проверка была только через 90 с. Из-за этого
		// свежеустановленная нода (и нода после перезагрузки) до полутора
		// минут работала на заведомо устаревшей сборке, а после каждой
		// перезагрузки цикл ожидания начинался заново. 30 с — достаточный
		// запас, чтобы нода успела поднять сессию, и при этом агент не
		// остаётся на старом бинарнике дольше необходимого.
		time.Sleep(30 * time.Second)
		for {
			if err := checkAndUpdate(serverURL); err != nil {
				log.Printf("[agent] autoupdate check: %v", err)
			}
			time.Sleep(30 * time.Minute)
		}
	}()
}

// checkAndUpdate сравнивает свою сборку с серверной и обновляется при необходимости.
//
// БАГ, который это чинит (v0.5.x): сравнение шло только по номеру версии.
// Бинарники пересобрали в 06:17, а version.txt писали в 05:57 — номер остался
// "0.5.2", поэтому ни одна нода (уже "0.5.2") не обновилась и осталась на старой
// сборке навсегда. Теперь сравнивается полная строка версия+сборка.
func checkAndUpdate(serverURL string) error {
	latest, err := fetchLatestVersion(serverURL)
	if err != nil {
		return err
	}
	mine := model.VersionTag()
	if latest == "" || latest == mine {
		return nil
	}
	log.Printf("[agent] new build available: %s -> %s, updating...", mine, latest)
	return downloadAndSwap(serverURL)
}

// fetchLatestVersion получает строку версии с сервера.
func fetchLatestVersion(serverURL string) (string, error) {
	base := strings.TrimSuffix(wsToHTTP(serverURL), "/agent")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(base + "/download/agent/version.txt")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("version.txt http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

// isNewer — сравнение semver. Оставлено для совместимости и для команды
// "обновить принудительно": раньше это была единственная проверка, и именно
// она пропускала пересборки с тем же номером версии.
func isNewer(current, latest string) bool {
	c := strings.SplitN(strings.TrimPrefix(current, "v"), ".", 3)
	l := strings.SplitN(strings.TrimPrefix(latest, "v"), ".", 3)
	if len(c) != 3 || len(l) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		ci, err1 := strconv.Atoi(strings.TrimSpace(c[i]))
		li, err2 := strconv.Atoi(strings.TrimSpace(l[i]))
		if err1 != nil || err2 != nil {
			return false
		}
		if li > ci {
			return true
		}
		if li < ci {
			return false
		}
	}
	return false
}

// downloadAndSwap скачивает свежий бинарник с сервера и подменяет себя.
// Сервер отдаёт /download/agent/{goos}/{goarch}/swagcore-agent(.exe) и .sha256 рядом.
//
// v0.6.0: перед подменой новый бинарник ПРОВЕРЯЕТСЯ запуском (команда version),
// и предыдущий сохраняется как .old (в v0.5.x .old удалялся сразу, отката не
// было — при битом релизе нода оставалась без агента до ручного вмешательства).
func downloadAndSwap(serverURL string) error {
	httpURL := wsToHTTP(serverURL)
	base := strings.TrimSuffix(httpURL, "/agent")
	goos := runtime.GOOS
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	binURL := fmt.Sprintf("%s/download/agent/%s/%s/swagcore-agent%s", base, goos, runtime.GOARCH, ext)
	shaURL := binURL + ".sha256"

	client := &http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
	}}

	// чексумма
	resp, err := client.Get(shaURL)
	if err != nil {
		return fmt.Errorf("fetch sha256: %w", err)
	}
	sumBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("sha256 http %d", resp.StatusCode)
	}
	wantSum := strings.TrimSpace(strings.SplitN(string(sumBody), " ", 2)[0])
	if wantSum == "" {
		return fmt.Errorf("empty sha256 manifest")
	}

	// бинарник
	resp, err = client.Get(binURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download http %d", resp.StatusCode)
	}

	me, err := os.Executable()
	if err != nil {
		return err
	}
	newPath := me + ".new"
	oldPath := me + ".old"

	f, err := os.Create(newPath)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(f, h), resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, wantSum) {
		_ = os.Remove(newPath)
		return fmt.Errorf("checksum mismatch: got %s want %s", got, wantSum)
	}
	if err := os.Chmod(newPath, 0o755); err != nil {
		return err
	}

	// проверка: новый бинарник должен запускаться и рапортовать свою сборку
	if out, err := exec.Command(newPath, "version").CombinedOutput(); err != nil {
		_ = os.Remove(newPath)
		return fmt.Errorf("new binary failed self-check: %v (%s)", err, truncate(string(out), 200))
	} else {
		log.Printf("[agent] downloaded build %s (self-check ok)", strings.TrimSpace(string(out)))
	}

	// атомарная подмена: старый -> .old, новый -> на место
	if err := os.Rename(me, oldPath); err != nil {
		return err
	}
	if err := os.Rename(newPath, me); err != nil {
		_ = os.Rename(oldPath, me) // откат
		return err
	}
	// .old НЕ удаляем — ручной откат одной командой
	fmt.Println("[agent] updated binary at", filepath.Base(me), "- restarting service...")
	// служба перезапустится сама (Restart=always / SCM), текущий процесс выходим
	prepareSelfUpdate() // без сервис-менеджера перезапускаем себя сами
	os.Exit(0)
	return nil
}

// wsToHTTP преобразует ws:// -> http://, wss:// -> https://.
func wsToHTTP(u string) string {
	u = strings.TrimPrefix(u, "ws://")
	if strings.HasPrefix(u, "wss://") {
		u = "https://" + strings.TrimPrefix(u, "wss://")
	}
	if !strings.HasPrefix(u, "http") {
		u = "http://" + u
	}
	return u
}
