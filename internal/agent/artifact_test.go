package agent

// Тесты распаковки артефактов.
//
// БАГ (v0.7.0, нода angelica): fetchArtifact вызывал внешний tar. На этой
// ноде tar.exe физически нет, поэтому process-деплой с artifact: падал с
// "tar: executable file not found in %PATH%". Тесты фиксируют, что
// распаковка самодостаточна (не требует внешних программ) и безопасна.

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// makeTarGz собирает tar.gz из карты путь->содержимое.
func makeTarGz(t *testing.T, files map[string]string, dirs []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	for _, d := range dirs {
		if err := tw.WriteHeader(&tar.Header{
			Name: d + "/", Typeflag: tar.TypeDir, Mode: 0o755,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

// РЕГРЕССИЯ: обычный архив распаковывается средствами Go, без внешнего tar.
func TestExtractTarGz_Works(t *testing.T) {
	src := makeTarGz(t,
		map[string]string{"app.exe": "BINARY", "conf/app.yml": "k: v"},
		[]string{"conf"},
	)
	dst := filepath.Join(t.TempDir(), "out")

	if err := extractTarGz(src, dst); err != nil {
		t.Fatalf("extractTarGz: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dst, "app.exe"))
	if err != nil {
		t.Fatalf("app.exe не извлечён: %v", err)
	}
	if string(b) != "BINARY" {
		t.Errorf("app.exe=%q, ожидалось BINARY", b)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "conf", "app.yml")); err != nil || string(b) != "k: v" {
		t.Errorf("вложенный файл не извлечён: %v %q", err, b)
	}
	if fi, err := os.Stat(filepath.Join(dst, "conf")); err != nil || !fi.IsDir() {
		t.Errorf("каталог conf не создан: %v", err)
	}
}

// Path traversal: архив не должен уметь писать за пределы каталога.
func TestExtractTarGz_RejectsTraversal(t *testing.T) {
	for _, evil := range []string{
		"../escaped.txt",
		"../../escaped.txt",
		`..\escaped.txt`,
		"sub/../../escaped.txt",
	} {
		src := makeTarGz(t, map[string]string{evil: "pwned"}, nil)
		dst := filepath.Join(t.TempDir(), "out")

		err := extractTarGz(src, dst)
		if err == nil {
			t.Errorf("архив с %q распакован без ошибки — это escape!", evil)
		}
		// и точно ничего не создалось рядом
		if _, statErr := os.Stat(filepath.Join(filepath.Dir(dst), "escaped.txt")); statErr == nil {
			t.Fatalf("файл создан вне каталога распаковки (%q)", evil)
		}
	}
}

// Абсолютный путь не должен приводить к записи ВНЕ каталога.
//
// В tar ведущий «/» по стандарту (USTAR) означает «относительно корня
// архива», а не корень ФС — поэтому имя внутри dest допустимо. Но путь
// с Windows-диском («C:\...») — это уже попытка писать в произвольное
// место, и его обязано отвергнуть.
func TestExtractTarGz_AbsolutePathStaysInside(t *testing.T) {
	// 1. POSIX-стиль: имя нормализуется внутрь dest, файл там и лежит
	src := makeTarGz(t, map[string]string{"/tmp/abs.txt": "x"}, nil)
	dst := filepath.Join(t.TempDir(), "out")
	if err := extractTarGz(src, dst); err != nil {
		t.Fatalf("распаковка с ведущим / упала: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "tmp", "abs.txt")); err != nil {
		t.Errorf("файл не извлечён внутрь dst: %v", err)
	}

	// 2. Диск Windows — отвергаем
	for _, evil := range []string{`C:/Windows/System32/evil.dll`, `C:\evil.dll`} {
		src := makeTarGz(t, map[string]string{evil: "x"}, nil)
		dst := filepath.Join(t.TempDir(), "out")
		if err := extractTarGz(src, dst); err == nil {
			t.Errorf("путь %q принят — это escape!", evil)
		}
	}
}

// Битый архив должен дать понятную ошибку, а не молча «успех».
func TestExtractTarGz_BrokenArchive(t *testing.T) {
	broken := filepath.Join(t.TempDir(), "broken.tar.gz")
	if err := os.WriteFile(broken, []byte("это не gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "out")
	if err := extractTarGz(broken, dst); err == nil {
		t.Fatal("битый архив принят без ошибки")
	}
}

// safeJoin проверяет границы каталога.
func TestSafeJoin(t *testing.T) {
	dest := filepath.Clean("/var/lib/app")
	ok := []string{"a.txt", "sub/b.txt", "./c.txt", "sub/../d.txt"}
	for _, n := range ok {
		got, err := safeJoin(dest, n)
		if err != nil {
			t.Errorf("safeJoin(%q) ошибка: %v", n, err)
			continue
		}
		rel, _ := filepath.Rel(dest, got)
		if rel == ".." || len(rel) > 0 && rel[0] == '.' && rel[1] == '.' {
			t.Errorf("safeJoin(%q) вышел за пределы: %q", n, got)
		}
	}
}
