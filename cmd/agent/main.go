// swagcore-agent — клиентский агент ноды (фоновый сервис).
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/kardianos/service"

	"github.com/dreamcatchered/swagcore/internal/agent"
	"github.com/dreamcatchered/swagcore/internal/model"
)

const svcName = "swagcore-agent"
const svcDisplayName = "swagCore Agent"
const svcDesc = "swagCore node agent — connects to core server and runs assigned projects"

// knownSubcommands — подкоманды, которые могут стоять где угодно в argv
// (как до, так и после флагов).
var knownSubcommands = map[string]bool{
	"install": true, "uninstall": true, "start": true, "stop": true,
	"status": true, "run": true, "foreground": true, "doctor": true,
	"version": true,
}

// splitSubcommand отделяет подкоманду от флагов.
func splitSubcommand(argv []string) (sub string, rest []string) {
	for _, a := range argv {
		if knownSubcommands[a] {
			sub = a
			continue
		}
		rest = append(rest, a)
	}
	return sub, rest
}

// program — реализация service.Interface.
type program struct {
	cfg  agent.Config
	exit chan struct{}
}

func (p *program) Start(s service.Service) error {
	p.exit = make(chan struct{})
	go p.run()
	return nil
}

func (p *program) Stop(s service.Service) error {
	select {
	case <-p.exit:
	default:
		close(p.exit)
	}
	return nil
}

func (p *program) run() {
	go agent.WatchProcess()
	agent.RunForever(p.cfg)
}

func main() {
	sub, flagArgs := splitSubcommand(os.Args[1:])

	// `version` обрабатываем ДО подключения лога к файлу.
	// Вывод этой команды разбирают машинно: install.ps1 читает из него
	// строку сборки, а selfupdate проверяет так скачанный бинарник.
	// Если бы агент при этом писал в stderr (лог настроен на
	// os.Stderr + файл), то в PowerShell 5.1 с $ErrorActionPreference="Stop"
	// stderr нативной команды превращался в NativeCommandError и УБИВАЛ
	// установку. Поэтому version обязан молчать в stderr и печатать
	// ровно одну строку.
	if sub == "version" {
		fmt.Println(model.VersionTag())
		return
	}

	// Лог в файл поднимаем ДО всего: иначе падение на старте (например,
	// служба не смогла зарегистрироваться в SCM) не оставит следов.
	agent.SetupFileLogging(agent.DataDirFromArgs(os.Args[1:]))



	fs := flag.NewFlagSet("swagcore-agent", flag.ContinueOnError)
	server := fs.String("server", envOr("SWAGCORE_SERVER", "ws://127.0.0.1:8181/agent"), "server WS URL")
	token := fs.String("token", envOr("SWAGCORE_TOKEN", ""), "node token")
	data := fs.String("data", defaultDataDir(), "data dir")
	nodeName := fs.String("name", envOr("SWAGCORE_NODE_NAME", ""), "node name shown in UI (default: OS hostname)")
	maxMem := fs.Int("max-mem", 0, "RAM limit for containers, MB (0 = no limit)")
	maxDisk := fs.Int("max-disk", 0, "disk limit for platform projects, GB (0 = no limit)")
	noDocker := fs.Bool("no-docker", false, "hide docker from scheduler")
	_ = fs.Parse(flagArgs)

	agent.SetDataDir(*data)
	cfg := agent.Config{
		ServerURL: *server, Token: *token, DataDir: *data, Name: *nodeName,
		MaxMemMB: *maxMem, MaxDiskGB: *maxDisk, NoDocker: *noDocker,
	}

	// version — печатает полную сборку (номер + хеш). Этот вызов использует
	// selfupdate для проверки скачанного бинарника перед подменой.
	if sub == "version" {
		fmt.Println(model.VersionTag())
		return
	}

	svcConfig := &service.Config{
		Name:        svcName,
		DisplayName: svcDisplayName,
		Description: svcDesc,
		Option: service.KeyValue{
			"Restart": "always", // systemd
		},
		Arguments: buildSvcArgs(cfg),
	}

	prg := &program{cfg: cfg}
	svc, err := service.New(prg, svcConfig)
	if err != nil {
		log.Fatal(err)
	}

	switch sub {
	case "install":
		must(svc.Install())
		fmt.Println("service installed. start: systemctl start " + svcName)
		return
	case "uninstall":
		_ = svc.Stop()
		must(svc.Uninstall())
		fmt.Println("service uninstalled")
		return
	case "start":
		must(svc.Start())
		return
	case "stop":
		_ = svc.Stop()
		return
	case "status":
		status, err := svc.Status()
		if err != nil {
			fmt.Println("status:", err)
			return
		}
		fmt.Println("status:", status)
		return
	case "run", "foreground":
		go agent.WatchProcess()
		agent.RunForever(cfg)
		return
	case "doctor":
		doDoctor(cfg)
		return
	}

	// запуск под управлением service manager; если manager недоступен
	// (ручной запуск) — работаем в foreground.
	runErr := svc.Run()
	if runErr != nil {
		if isNotAService(runErr) {
			go agent.WatchProcess()
			agent.RunForever(cfg)
			return
		}
		log.Fatal(runErr)
	}
}

// buildSvcArgs — аргументы, с которыми СЕРВИС-МЕНЕДЖЕР запускает агента.
//
// БАГ (v0.6.0, найден на ноде angelica): сюда попадал подкомандный аргумент
// "run". Из-за этого при старте из-под SCM выполнялась ветка
//
//	case "run", "foreground": agent.RunForever(cfg)
//
// то есть агент СРАЗУ уходил в бесконечный foreground-цикл и НИКОГДА не
// вызывал svc.Run() -> StartServiceCtrlDispatcher(). А SCM без этого
// рукопожатия не считает службу запущенной: через 30 с прилетает
// Event ID 7009 "A timeout was reached while waiting for the service to
// connect", служба помечается как не отвечающая и останавливается.
//
// Итог был ровно такой: нода подключалась на пару секунд (процесс-то работал)
// и отваливалась — после перезагрузки ПК агент не поднимался вообще.
//
// Теперь в binPath НЕТ подкоманды: процесс, стартовавший без подкоманды,
// доходит до svc.Run() и корректно регистрируется в SCM / systemd.
func buildSvcArgs(cfg agent.Config) []string {
	args := []string{"--server", cfg.ServerURL, "--token", cfg.Token, "--data", cfg.DataDir}
	if cfg.MaxMemMB > 0 {
		args = append(args, "--max-mem", fmt.Sprintf("%d", cfg.MaxMemMB))
	}
	if cfg.MaxDiskGB > 0 {
		args = append(args, "--max-disk", fmt.Sprintf("%d", cfg.MaxDiskGB))
	}
	if cfg.NoDocker {
		args = append(args, "--no-docker")
	}
	if cfg.Name != "" {
		args = append(args, "--name", cfg.Name)
	}
	return args
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func isNotAService(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	// kardianos возвращает разные строки на разных платформах при отсутствии service manager
	return strings.Contains(s, "not supported") ||
		strings.Contains(s, "service manager") ||
		strings.Contains(s, "Unknown service") ||
		strings.Contains(s, "The system cannot find the file specified") ||
		strings.Contains(s, "Access is denied") && runtime.GOOS == "windows"
}

func doDoctor(cfg agent.Config) {
	fmt.Println("build:     ", model.VersionTag())
	fmt.Println("server:    ", cfg.ServerURL)
	fmt.Println("data dir:  ", cfg.DataDir)
	fmt.Println("docker:    ", agent.DockerAvailable())
	fmt.Println("instance:  ", agent.InstanceID(cfg.DataDir))
	hostname, _ := os.Hostname()
	fmt.Println("hostname:  ", hostname)
	fmt.Println("os/arch:   ", runtime.GOOS+"/"+runtime.GOARCH)
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Println("WARNING: docker CLI not found in PATH")
	} else {
		fmt.Println("docker CLI:", "ok")
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

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
