package agent

// Единый каталог данных агента.
//
// v0.6.0 (баг): appsDir() и sitesDir() были захардкожены на
// C:\ProgramData\swagcore и /var/lib/swagcore, игнорируя флаг --data.
// Из-за этого установка в профиль пользователя (без прав администратора)
// клала файлы проектов в C:\ProgramData\swagcore\apps, тогда как сам агент,
// его instance.id и логи лежали в %LOCALAPPDATA%\swagcore — то есть в двух
// разных местах. Теперь все производные каталоги считаются от --data.

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

var (
	dataMu     sync.RWMutex
	dataRoot   string
)

// SetDataDir запоминает корневой каталог данных агента (значение --data).
// Вызывается один раз при старте, до любых операций с проектами.
func SetDataDir(dir string) {
	if dir == "" {
		dir = defaultDataDir()
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	dataMu.Lock()
	dataRoot = dir
	dataMu.Unlock()
	_ = os.MkdirAll(dir, 0o755)
}

// DataDir возвращает корневой каталог данных агента.
func DataDir() string {
	dataMu.RLock()
	d := dataRoot
	dataMu.RUnlock()
	if d != "" {
		return d
	}
	return defaultDataDir()
}

// appsDir — каталог process-проектов (каждый проект в подкаталоге).
func appsDir() string { return filepath.Join(DataDir(), "apps") }

// sitesDir — каталог docker-проектов с распакованными артефактами.
func sitesDir() string { return filepath.Join(DataDir(), "sites") }

// defaultDataDir — каталог по умолчанию (совпадает с cmd/agent/main.go).
func defaultDataDir() string {
	switch runtime.GOOS {
	case "windows":
		return `C:\ProgramData\swagcore`
	case "darwin":
		return "/usr/local/var/swagcore"
	default:
		return "/var/lib/swagcore"
	}
}
