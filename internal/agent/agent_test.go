package agent

// Тесты агента: идентичность машины, выбор имени ноды, разбор команд.
//
// Закрывают баги, найденные на живых нодах 05.10.2026.

import (
	"os"
	"path/filepath"
	"testing"
)

// РЕГРЕССИЯ: введённое пользователем имя должно побеждать имя ОС.
// В v0.6.0 агент слал os.Hostname(), и нода «angelica» отображалась в UI как
// DESKTOP-955JQI1 — её было невозможно опознать глазами.
func TestDisplayName_PrefersConfiguredName(t *testing.T) {
	if got := displayName("angelica", "DESKTOP-955JQI1"); got != "angelica" {
		t.Fatalf("получено %q, ожидалось angelica", got)
	}
}

// Без --name берём имя ОС.
func TestDisplayName_FallsBackToHostname(t *testing.T) {
	if got := displayName("", "DESKTOP-955JQI1"); got != "DESKTOP-955JQI1" {
		t.Fatalf("получено %q, ожидалось имя ОС", got)
	}
}

// Пробелы вокруг имени не должны отправляться на сервер.
func TestDisplayName_TrimsWhitespace(t *testing.T) {
	if got := displayName("  angelica \r\n", "DESKTOP-955JQI1"); got != "angelica" {
		t.Fatalf("получено %q, ожидалось angelica (без пробелов)", got)
	}
	// из одних пробелов имя не считается заданным
	if got := displayName("   ", "DESKTOP-955JQI1"); got != "DESKTOP-955JQI1" {
		t.Fatalf("получено %q, ожидалось имя ОС", got)
	}
}

// InstanceID обязан переживать перезапуск: один и тот же data-dir даёт один
// и тот же ID. Иначе после каждого перезапуска сервер считал бы ноду новой
// и каскадно удалял старую запись вместе с её проектами.
func TestInstanceID_StableAcrossRestarts(t *testing.T) {
	dir := t.TempDir()

	first := loadOrCreateInstanceID(dir)
	second := loadOrCreateInstanceID(dir)
	if first != second {
		t.Fatalf("InstanceID нестабилен: %q != %q при одном data-dir", first, second)
	}
	if len(first) < 16 {
		t.Fatalf("InstanceID слишком короткий: %q", first)
	}

	// файл должен лежать в data-dir и переживать новый вызов
	if _, err := os.Stat(filepath.Join(dir, "instance.id")); err != nil {
		t.Fatalf("instance.id не создан: %v", err)
	}
}

// Битый instance.id (слишком короткий) должен быть перегенерирован, а
// валидный — прочитан как есть.
func TestInstanceID_CorruptFileRegeneratedValidKept(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instance.id")

	// 1. битый файл -> перегенерировали и ЗАПИСАЛИ новый
	if err := os.WriteFile(path, []byte("short\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := loadOrCreateInstanceID(dir)
	if len(first) < 16 {
		t.Fatalf("битый instance.id не перегенерирован, получено %q", first)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != first+"\n" {
		t.Fatalf("перегенерированный ID не записан на диск: %q, ожидалось %q", onDisk, first+"\n")
	}

	// 2. валидный файл -> читается как есть, без перегенерации
	valid := strings32()
	if err := os.WriteFile(path, []byte(valid+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadOrCreateInstanceID(dir); got != valid {
		t.Fatalf("валидный instance.id прочитан как %q, ожидалось %q", got, valid)
	}
}

func strings32() string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

// Два агента на РАЗНЫХ машинах должны получить разные ID.
func TestInstanceID_DiffersPerDataDir(t *testing.T) {
	a := loadOrCreateInstanceID(t.TempDir())
	b := loadOrCreateInstanceID(t.TempDir())
	if a == b {
		t.Fatalf("разные машины получили одинаковый InstanceID %q — анти-дубль сработает неверно", a)
	}
}
