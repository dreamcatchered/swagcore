package agent

// Самообновление: перезапуск процесса.
//
// v0.6.0 (баг): после подмены бинарника агент делал os.Exit(0) и рассчитывал,
// что его перезапустит systemd / SCM. Если агент запущен как обычный процесс
// (например, автозагрузка Windows без прав администратора), его никто не
// поднимал — и после ПЕРВОГО ЖЕ самообновления нода молча отваливалась.
// На практике это и случилось: агент на Windows-ПК обновился и больше не вернулся.
//
// Теперь, если сервис-менеджера нет, агент запускает свежий экземпляр сам.

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// isServiceManaged сообщает, запустил ли нас сервис-менеджер (systemd / SCM).
func isServiceManaged() bool {
	if os.Getenv("SWAGCORE_SERVICE") == "1" {
		return true
	}
	switch runtime.GOOS {
	case "windows":
		// Под SCM родительский процесс — services.exe
		if name, ok := processName(os.Getppid()); ok {
			return strings.EqualFold(name, "services.exe")
		}
		return false
	default:
		// systemd выставляет INVOCATION_ID в окружение сервиса
		if os.Getenv("INVOCATION_ID") != "" {
			return true
		}
		if name, ok := processName(os.Getppid()); ok {
			if strings.HasPrefix(name, "systemd") {
				return true
			}
		}
		// суид-родитель (init) — тоже считаем управляемым
		return os.Getppid() == 1
	}
}

// prepareSelfUpdate вызывается перед os.Exit(0) после подмены бинарника.
func prepareSelfUpdate() {
	if isServiceManaged() {
		return // перезапустит systemd / SCM
	}
	if err := relaunchSelf(); err != nil {
		log.Printf("[agent] самообновление: не удалось перезапустить себя (%v) — запустите агента вручную", err)
		return
	}
	log.Println("[agent] самообновление: новый экземпляр агента запущен")
}

// relaunchSelf запускает свежую копию агента в фоне.
func relaunchSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// os.Args[0] — путь к СТАРОМУ бинарнику, его не переносим; остальные
	// аргументы (run --server … --token …) копируем как есть.
	args := []string{exe}
	for _, a := range os.Args[1:] {
		if a == os.Args[0] {
			continue
		}
		args = append(args, a)
	}
	return spawnDetachedIn(exe, args, filepath.Dir(exe))
}
