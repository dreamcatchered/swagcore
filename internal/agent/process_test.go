package agent

// Тест на то, что распакованный бинарник находится рядом с проектом.
//
// БАГ (v0.7.0, первый же process-деплой на нодах angelica/dream):
// fetchArtifact распаковывала артефакт в sitesDir(), а проект запускается
// из appsDir(). Артефакт скачивался, но resolveBin() искал бинарник в
// appsDir() и не находил его:
//
//	start: exec: "primegrind.exe": executable file not found in %PATH%
//
// Тест фиксирует: после extractTarGz в каталог проекта бинарник обязан
// запускаться по имени (без абсолютного пути и без PATH).

import (
	"os"
	"path/filepath"
	"testing"
)

// РЕГРЕССИЯ: бинарник из артефакта резолвится относительно каталога проекта.
func TestResolveBin_FindsArtifactInProjectDir(t *testing.T) {
	projectDir := t.TempDir()

	// simulates: артефакт распакован в каталог проекта
	src := makeTarGz(t, map[string]string{"primegrind.exe": "MZ-binary"}, nil)
	if err := extractTarGz(src, projectDir); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}

	got := resolveBin("primegrind.exe", projectDir)
	want := filepath.Join(projectDir, "primegrind.exe")
	if got != want {
		t.Fatalf("resolveBin=%q, ожидалось %q — иначе запуск падает с "+
			"\"executable file not found in %%PATH%%\"", got, want)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("по найденному пути нет файла: %v", err)
	}
}

// resolveBin не должен ломать абсолютные пути.
func TestResolveBin_KeepsAbsolutePath(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "app.exe")
	got := resolveBin(abs, t.TempDir())
	if got != abs {
		t.Fatalf("resolveBin(%q)=%q, ожидался неизменный абсолютный путь", abs, got)
	}
}

// Если бинарника нет ни рядом, ни в PATH — возвращается имя как есть,
// чтобы ошибка запуска была понятной ("не найден"), а не пустой строкой.
func TestResolveBin_MissingKeepsName(t *testing.T) {
	got := resolveBin("nope.exe", t.TempDir())
	if got != "nope.exe" {
		t.Fatalf("resolveBin=%q, ожидалось исходное имя", got)
	}
}
