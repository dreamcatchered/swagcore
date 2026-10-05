package agent

// Распаковка артефактов без внешних программ.
//
// БАГ (v0.7.0, найден на живых нодах 05.10.2026): fetchArtifact распаковывал
// tar.gz вызовом внешней программы:
//
//	exec.Command("tar", "-xzf", tarPath, "-C", dir)
//
// На Windows tar.exe есть не везде — на ноде angelica (Windows 10 Pro
// 19045) его физически нет, в PATH пусто. Любой process-проект с
// artifact: падал с
//
//	artifact: tar: exec: "tar": executable file not found in %PATH%
//
// то есть фича «приложить артефакт» была сломана на части машин, и
// диагностировать это можно было только на самой ноде.
//
// Теперь распаковка идёт целиком на Go (archive/tar + compress/gzip):
// внешних зависимостей нет, поведение одинаково на Windows и Linux,
// а пути внутри архива дополнительно защищены от выхода за пределы
// каталога (path traversal вида ../../Windows/System32/cmd.exe).

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// maxArtifactFileSize — предохранитель от «zip-бомбы» (5 ГБ на файл).
const maxArtifactFileSize = 5 << 30

// maxArtifactTotalSize — суммарный предел на распакованное содержимое (20 ГБ).
const maxArtifactTotalSize = 20 << 30

// safeJoin соединяет dest и имя из архива, не давая выйти за пределы dest.
func safeJoin(dest, name string) (string, error) {
	// Имя из архива — это «/»-разделители (POSIX), независимо от ОС.
	slashed := strings.ReplaceAll(name, `\`, "/")

	// Ведущий «/» в tar означает «относительно корня», а не корень ФС.
	slashed = strings.TrimLeft(slashed, "/")
	if slashed == "" {
		return "", fmt.Errorf("пустое имя в архиве: %q", name)
	}
	// Явный Windows-диск в архиве — escape.
	if len(slashed) >= 2 && slashed[1] == ':' {
		return "", fmt.Errorf("небезопасный путь в архиве: %q", name)
	}

	// Нормализуем ДО проверки: «a/../../x» схлопывается в «../x», и такой
	// путь обязан быть отвергнут, а не «случайно» уехать внутрь dest.
	norm := path.Clean(slashed)
	if norm == ".." || strings.HasPrefix(norm, "../") {
		return "", fmt.Errorf("путь выходит за пределы каталога: %q", name)
	}

	joined := filepath.Join(dest, filepath.FromSlash(norm))

	// Контрольная проверка: результат обязан лежать внутри dest.
	rel, err := filepath.Rel(dest, joined)
	if err != nil {
		return "", fmt.Errorf("небезопасный путь в архиве: %q", name)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("путь выходит за пределы каталога: %q", name)
	}
	return joined, nil
}

// extractTarGz распаковывает tar.gz в dest.
func extractTarGz(tarPath, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dest, err)
	}

	f, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", tarPath, err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()

	var total int64
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}

		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}

		case tar.TypeReg:
			if hdr.Size < 0 || hdr.Size > maxArtifactFileSize {
				return fmt.Errorf("файл %q слишком большой: %d байт", hdr.Name, hdr.Size)
			}
			total += hdr.Size
			if total > maxArtifactTotalSize {
				return fmt.Errorf("архив распаковывается в %d байт — превышен предел", total)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o755|0o644)
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}
			if _, err := io.Copy(out, tr); err != nil { //nolint:gosec // путь проверен safeJoin
				out.Close()
				return fmt.Errorf("write %s: %w", target, err)
			}
			if err := out.Close(); err != nil {
				return fmt.Errorf("close %s: %w", target, err)
			}

		case tar.TypeSymlink, tar.TypeLink:
			// Симлинки в пользовательских артефактах не нужны, а вот
			// проверить их корректность мы не можем — молча пропускаем,
			// чтобы не превращать распаковку в escape hatch.
			continue

		default:
			// остальные типы (fifo, device) игнорируем
		}
	}
}
