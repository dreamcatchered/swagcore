package agent

// identity — постоянный идентификатор МАШИНЫ для анти-дубля нод.
//
// Проблема (v0.5.x): на Windows-машине оказалось ДВЕ записи автозагрузки, каждая
// со своим токеном. Сервер видел два разных token_hash -> создавал две ноды с
// одинаковым hostname, обе работали, обе получали один и тот же проект
// (placement: all) и убивали друг друга гонкой за порт.
//
// Решение: агент один раз генерирует UUID в своём data-каталоге и шлёт его в
// каждом hello. Сервер, увидев две ноды с одним instance_id, оставляет новую и
// удаляет старый дубль каскадом.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	instOnce sync.Once
	instID   string
)

// InstanceID возвращает постоянный ID этой машины (создаётся при первом запуске).
func InstanceID(dataDir string) string {
	instOnce.Do(func() {
		instID = loadOrCreateInstanceID(dataDir)
	})
	return instID
}

func instanceFile(dataDir string) string {
	return filepath.Join(dataDir, "instance.id")
}

func loadOrCreateInstanceID(dataDir string) string {
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	f := instanceFile(dataDir)
	if b, err := os.ReadFile(f); err == nil {
		if s := strings.TrimSpace(string(b)); len(s) >= 16 {
			return s
		}
	}
	id := newRandomID()
	_ = os.MkdirAll(dataDir, 0o755)
	if err := os.WriteFile(f, []byte(id+"\n"), 0o644); err != nil {
		// не смогли записать — всё равно работаем, просто без анти-дубля
		return id
	}
	return id
}


// newRandomID — 16 случайных байт в hex (32 символа).
func newRandomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// крайне маловероятный fallback: время + pid
		return hex.EncodeToString([]byte(fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())))
	}
	return hex.EncodeToString(b)
}
