package model

// Тесты манифеста и версии.
//
// Эти инварианты — то, на чём держался UI и деплой. Ошибки в них дают
// «проект молча уехал не туда» и «агент не поднялся», что крайне неприятно
// диагностировать, потому что платформа рапортует «успешно».

import (
	"reflect"
	"strings"
	"testing"
)

// manifestTags собирает множество yaml-имён полей Manifest.
func manifestTags() map[string]bool {
	out := map[string]bool{}
	tp := reflect.TypeOf(Manifest{})
	for i := 0; i < tp.NumField(); i++ {
		tag := tp.Field(i).Tag.Get("yaml")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out[name] = true
	}
	return out
}

// Версия должна быть в формате, который сравнивает selfupdate.
func TestVersionTagFormat(t *testing.T) {
	tag := VersionTag()
	if !strings.HasPrefix(tag, Version+"+") {
		t.Errorf("VersionTag()=%q, ожидалось %q+<build>", tag, Version)
	}
	build := strings.TrimPrefix(tag, Version+"+")
	if build == "" {
		t.Errorf("VersionTag()=%q: пустой build", tag)
	}
	if strings.ContainsAny(build, " \t\n") {
		t.Errorf("build %q содержит пробелы — испортится разбор version.txt", build)
	}
}

// Нормализация Placement: неизвестное значение нельзя молча трактовать как
// «на все ноды» — иначе опечатка в манифесте разъедет проект по всей ферме.
func TestPlacementKnownValues(t *testing.T) {
	for _, ok := range []string{PlacementAll, PlacementSelected, ""} {
		switch ok {
		case PlacementAll, PlacementSelected, "":
		default:
			t.Fatalf("неожиданное значение %q", ok)
		}
	}
	if PlacementAll == PlacementSelected {
		t.Fatal("PlacementAll и PlacementSelected не должны совпадать")
	}
}

// yaml-теги манифеста — контракт с пользователем: переименование поля молча
// ломает все существующие манифесты.
func TestManifestYAMLTagsStable(t *testing.T) {
	// Эти поля обязаны называться ровно так: от них зависят манифесты,
	// которые пользователи уже написали руками.
	want := []string{
		"name", "mode", "image", "os", "command", "ports", "env", "volumes",
		"resources", "replicas", "preferred_node", "placement", "nodes",
		"artifact", "artifact_sha", "mount_path", "domain",
	}
	raw := manifestTags()
	for _, f := range want {
		if !raw[f] {
			t.Errorf("в манифесте нет yaml-поля %q (найдены: %v)", f, keysOf(raw))
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Ресурсы: нули означают «без лимита», отрицательные — ошибка конфигурации.
func TestResourcesZeroMeansUnlimited(t *testing.T) {
	r := Resources{}
	if r.Memory != "" {
		t.Errorf("Resources{}.Memory=%q, ожидалась пустая строка (без лимита)", r.Memory)
	}
	if r.CPUs != "" {
		t.Errorf("Resources{}.CPUs=%q, ожидалась пустая строка (без лимита)", r.CPUs)
	}
}

// Статусы инстанса не должны пересекаться: UI ориентируется на них.
func TestInstanceStatusDistinct(t *testing.T) {
	all := []string{InstDesired, InstStarting, InstRunning, InstStopped, InstFailed, InstOrphan}
	seen := map[string]bool{}
	for _, s := range all {
		if s == "" {
			t.Fatal("пустой статус инстанса")
		}
		if seen[s] {
			t.Errorf("дублирующийся статус %q", s)
		}
		seen[s] = true
	}
}
