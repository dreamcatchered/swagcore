package main

// Инварианты запуска агента.
//
// Каждый тест здесь закрывает реальный баг из продакшена.
//
// ГЛАВНЫЙ — TestSvcArgs_NoRunSubcommand. В v0.6.0 buildSvcArgs добавлял
// подкоманду "run" в binPath службы Windows. Процесс, стартовавший с "run",
// уходил в бесконечный foreground-цикл и НИКОГДА не вызывал
// svc.Run() -> StartServiceCtrlDispatcher(). SCM без рукопожатия рвал
// службу по 30-секундному таймауту (Event ID 7009) — нода подключалась на
// пару секунд и отваливалась, а после перезагрузки ПК не поднималась вообще.
// Баг стоил реальной диагностики на живой ноде angelica.

import (
	"slices"
	"strings"
	"testing"

	"github.com/dreamcatchered/swagcore/internal/agent"
)

// РЕГРЕССИЯ: в аргументах службы не должно быть подкоманды "run".
func TestSvcArgs_NoRunSubcommand(t *testing.T) {
	cfg := agent.Config{
		ServerURL: "wss://core.swag.best/agent",
		Token:     "tok",
		DataDir:   `C:\ProgramData\swagcore`,
		Name:      "angelica",
	}
	args := buildSvcArgs(cfg)

	for _, a := range args {
		if a == "run" || a == "foreground" {
			t.Fatalf("buildSvcArgs содержит подкоманду %q: процесс уйдёт в foreground и "+
				"не вызовет StartServiceCtrlDispatcher — SCM убьёт службу по таймауту "+
				"(Event 7009), и агент не поднимется после перезагрузки. args=%v", a, args)
		}
	}
}

// РЕГРЕССИЯ: подкоманд быть не должно вообще — первый аргумент это флаг.
func TestSvcArgs_StartsWithFlag(t *testing.T) {
	cfg := agent.Config{ServerURL: "wss://x/agent", Token: "t", DataDir: "d"}
	args := buildSvcArgs(cfg)
	if len(args) == 0 {
		t.Fatal("buildSvcArgs вернул пустой список")
	}
	if !strings.HasPrefix(args[0], "--") {
		t.Fatalf("первый аргумент %q не является флагом — SCM передаст его как подкоманду: %v", args[0], args)
	}
}

// Обязательные параметры должны доезжать до сервиса.
func TestSvcArgs_CarriesRequiredFlags(t *testing.T) {
	cfg := agent.Config{
		ServerURL: "wss://core.swag.best/agent",
		Token:     "secret-token",
		DataDir:   `C:\ProgramData\swagcore`,
	}
	args := buildSvcArgs(cfg)

	// helper: значение после флага
	val := func(flag string) (string, bool) {
		for i, a := range args {
			if a == flag && i+1 < len(args) {
				return args[i+1], true
			}
		}
		return "", false
	}

	if v, ok := val("--server"); !ok || v != cfg.ServerURL {
		t.Errorf("--server: %q (ok=%v), ожидалось %q", v, ok, cfg.ServerURL)
	}
	if v, ok := val("--token"); !ok || v != cfg.Token {
		t.Errorf("--token: %q (ok=%v), ожидалось %q", v, ok, cfg.Token)
	}
	if v, ok := val("--data"); !ok || v != cfg.DataDir {
		t.Errorf("--data: %q (ok=%v), ожидалось %q", v, ok, cfg.DataDir)
	}
}

// Имя ноды должно попадать в аргументы, иначе в UI будет DESKTOP-XXXXX
// вместо имени, которое пользователь ввёл при подключении.
func TestSvcArgs_CarriesNodeName(t *testing.T) {
	cfg := agent.Config{ServerURL: "wss://x/agent", Token: "t", DataDir: "d", Name: "angelica"}
	args := buildSvcArgs(cfg)
	if !slices.Contains(args, "--name") || !slices.Contains(args, "angelica") {
		t.Fatalf("buildSvcArgs не передаёт --name angelica: %v", args)
	}
}

// Пустое имя не должно превращаться в пустой --name (иначе парсер флагов
// начнёт считать следующий аргумент значением --name).
func TestSvcArgs_NoEmptyName(t *testing.T) {
	cfg := agent.Config{ServerURL: "wss://x/agent", Token: "t", DataDir: "d"}
	args := buildSvcArgs(cfg)
	for i, a := range args {
		if a == "--name" {
			t.Fatalf("--name передан без значения: %v", args)
		}
		if a == "" {
			t.Fatalf("пустой аргумент в позиции %d: %v", i, args)
		}
	}
}

// Лимиты и флаг docker пробрасываются только когда заданы.
func TestSvcArgs_OptionalFlags(t *testing.T) {
	base := agent.Config{ServerURL: "wss://x/agent", Token: "t", DataDir: "d"}

	plain := buildSvcArgs(base)
	for _, bad := range []string{"--max-mem", "--max-disk", "--no-docker"} {
		if slices.Contains(plain, bad) {
			t.Errorf("флаг %s присутствует без настройки: %v", bad, plain)
		}
	}

	full := buildSvcArgs(agent.Config{
		ServerURL: "wss://x/agent", Token: "t", DataDir: "d",
		MaxMemMB: 2048, MaxDiskGB: 50, NoDocker: true, Name: "n1",
	})
	for _, want := range []string{"--max-mem", "2048", "--max-disk", "50", "--no-docker", "--name", "n1"} {
		if !slices.Contains(full, want) {
			t.Errorf("нет %q в %v", want, full)
		}
	}
}

// splitSubcommand должен находить подкоманду где угодно в argv и не путать
// значения флагов с подкомандами.
func TestSplitSubcommand(t *testing.T) {
	cases := []struct {
		argv     []string
		wantSub  string
		wantRest int
	}{
		{[]string{"run", "--server", "x"}, "run", 2},
		{[]string{"--server", "x", "run"}, "run", 2},
		{[]string{"install", "--token", "t", "--name", "n"}, "install", 4},
		{[]string{"--data", "C:\\d"}, "", 2},
		{[]string{"version"}, "version", 0},
	}
	for _, c := range cases {
		sub, rest := splitSubcommand(c.argv)
		if sub != c.wantSub {
			t.Errorf("argv=%v: sub=%q, ожидалось %q", c.argv, sub, c.wantSub)
		}
		if len(rest) != c.wantRest {
			t.Errorf("argv=%v: rest=%v (%d), ожидалось %d аргументов", c.argv, rest, len(rest), c.wantRest)
		}
	}
}
