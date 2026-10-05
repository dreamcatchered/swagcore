// Client — WS-клиент агента: соединение с сервером, обработка команд.
package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dreamcatchered/swagcore/internal/model"
)

// Config — конфиг агента.
type Config struct {
	ServerURL  string // ws(s)://host/agent
	Token      string
	DataDir    string
	Name       string // имя ноды в UI; пусто = взять имя ОС
	MaxMemMB   int    // лимит RAM для проектов платформы на этой ноде
	MaxDiskGB  int    // лимит диска для проектов платформы (ГБ)
	MaxCPUs    float64 // лимит CPU (ядер)
	NoDocker   bool   // принудительно скрыть docker от планировщика
}

// RunForever — главный цикл: подключение, переподключение с бэкоффом.
func RunForever(cfg Config) {
	if cfg.Token == "" {
		log.Fatal("[agent] no token: pass --token or set SWAGCORE_TOKEN")
	}
	// Каталог данных влияет на ВСЕ производные пути (apps/, sites/, instance.id).
	// В v0.5.x --data применялся только к логам, а каталоги проектов были
	// захардкожены — при установке в профиль пользователя (без прав админа)
	// файлы проектов разъезжались в C:\ProgramData, а агент жил в %LOCALAPPDATA%.
	SetDataDir(cfg.DataDir)
	// фоновая авто-проверка сборки (каждые 30 минут) + самообновление
	StartAutoUpdate(cfg.ServerURL)

	backoff := 5 * time.Second
	for {
		if err := runOnce(cfg); err != nil {
			log.Printf("[agent] session ended: %v; reconnect in %s", err, backoff)
		}
		// Пауза между попытками должна прерываться запросом остановки,
		// иначе агент после stop() ещё до 60 секунд не выходил.
		select {
		case <-time.After(backoff):
		case <-shutdownCh:
			log.Println("[agent] получен запрос остановки — выход")
			return
		}
		if backoff < 60*time.Second {
			backoff *= 2
		}
	}
}

// displayName — имя ноды для hello: явное из --name важнее имени ОС.
func displayName(cfgName, osHostname string) string {
	if n := strings.TrimSpace(cfgName); n != "" {
		return n
	}
	return osHostname
}

// uploadFile отправляет файл на сервер (multipart /upload/{nodeID}).
func uploadFile(cfg Config, name string, data []byte) error {
	serverURL := cfg.ServerURL
	httpURL := wsToHTTP(serverURL)
	base := strings.TrimSuffix(httpURL, "/agent")
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, err := w.CreateFormFile("file", name)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, base+"/upload/0", body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Node-Token", cfg.Token)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("upload http %d", resp.StatusCode)
	}
	return nil
}

var (
	shutdownOnce sync.Once
	shutdownCh   = make(chan struct{})
)

// RequestShutdown просит агента штатно завершиться.
//
// БАГ (v0.7.0, найден на ноде dream): program.Stop() в cmd/agent закрывал
// свой канал, но RunForever его никогда не проверял и крутился вечно. В
// итоге `Restart-Service swagcore-agent` отдавал «Не удалось остановить
// службу», SCM ждал таймаут и убивал процесс принудительно. Из-за этого
// не работали ни перезапуск, ни нормальное uninstall, ни аккуратное
// обновление — а любой сбой приходилось чинить руками.
func RequestShutdown() {
	shutdownOnce.Do(func() { close(shutdownCh) })
}

func runOnce(cfg Config) error {
	hdr := httpHeaderWithToken(cfg.Token)
	conn, _, err := websocket.DefaultDialer.Dial(cfg.ServerURL, hdr)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Штатная остановка должна разрывать и живое соединение, иначе цикл
	// чтения не проснётся и агент будет висеть до таймаута SCM.
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		select {
		case <-shutdownCh:
			_ = conn.Close()
		case <-stopWatch:
		}
	}()

	hostname, _ := os.Hostname()
	hello := model.Hello{
		// Имя из --name (то, что пользователь ввёл при подключении ноды) важнее
		// имени ОС: иначе нода «angelica» показывалась в UI как DESKTOP-955JQI1
		// и её было невозможно опознать глазами.
		Hostname:   displayName(cfg.Name, hostname),
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		Version:    model.VersionTag(),
		Token:      cfg.Token,
		MaxMemMB:   cfg.MaxMemMB,
		MaxCPUs:    cfg.MaxCPUs,
		MaxDiskGB:  cfg.MaxDiskGB,
		HasDocker:  DockerAvailable() && !cfg.NoDocker,
		InstanceID: InstanceID(cfg.DataDir),
	}
	setCurrentToken(cfg.Token)
	if err := send(conn, model.MsgHello, hello); err != nil {
		return err
	}

	// ждём welcome
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	// продлеваем read deadline по ping/pong-кадрам сервера (сервер пингует каждые 30с).
	// ВАЖНО: входящие PING обрабатывает SetPingHandler (SetPongHandler ловит только PONG),
	// без этого соединение рвалось по таймауту раз в 90 секунд.
	prolong := func() error { return conn.SetReadDeadline(time.Now().Add(90 * time.Second)) }
	conn.SetPingHandler(func(s string) error {
		_ = prolong()
		return conn.WriteControl(websocket.PongMessage, []byte(s), time.Now().Add(5*time.Second))
	})
	conn.SetPongHandler(func(string) error { return prolong() })
	var env model.Envelope
	if err := conn.ReadJSON(&env); err != nil {
		return err
	}
	if env.Type != model.MsgWelcome {
		log.Printf("[agent] unexpected first frame: %s", env.Type)
		return errBadFirstFrame
	}
	var welcome model.Welcome
	_ = json.Unmarshal(env.Payload, &welcome)
	log.Printf("[agent] connected, node_id=%d (server %s)", welcome.NodeID, welcome.ServerVersion)

	// обработчик исходящих статусов из других горутин
	var mu sync.Mutex
	sendStatus := func(st model.ProjectStatus) {
		mu.Lock()
		defer mu.Unlock()
		if err := send(conn, model.MsgStatus, st); err != nil {
			log.Printf("[agent] send status: %v", err)
		}
	}
	sendLogs := func(projectID int64, data string, done bool) {
		mu.Lock()
		defer mu.Unlock()
		_ = send(conn, model.MsgLogs, model.LogsChunk{ProjectID: projectID, Data: data, Done: done})
	}
	sendResult := func(r model.Result) {
		mu.Lock()
		defer mu.Unlock()
		if err := send(conn, model.MsgResult, r); err != nil {
			log.Printf("[agent] send result: %v", err)
		}
	}
	// sendRecon не нужен отдельно: recon-ответ отправляется через sendResultFunc
	// глобальная ссылка для reconOnce (вызывается из горутины MsgRecon)
	sendResultFunc = func(env model.Envelope) error {
		mu.Lock()
		defer mu.Unlock()
		return sendEnvelope(conn, env)
	}

	// heartbeat каждые 15 секунд
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				m := HostMetrics(RunningContainers())
				mu.Lock()
				err := send(conn, model.MsgPing, model.Ping{Metrics: m})
				mu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()

	// основной цикл чтения команд
	for {
		_ = conn.SetReadDeadline(time.Now().Add(90 * time.Second))
		var env model.Envelope
		if err := conn.ReadJSON(&env); err != nil {
			return err
		}
		handleCommand(env, cfg, sendStatus, sendLogs, sendResult)
	}
}

func handleCommand(env model.Envelope, cfg Config, sendStatus func(model.ProjectStatus), sendLogs func(int64, string, bool), sendResult func(model.Result)) {
	switch env.Type {
	case model.MsgExec:
		var task model.ExecTask
		if json.Unmarshal(env.Payload, &task) != nil {
			return
		}
		go RunExec(task, sendResult, env.RequestID)

	case model.MsgPower:
		var task model.PowerTask
		if json.Unmarshal(env.Payload, &task) != nil {
			return
		}
		go RunPower(task, sendResult)

	case model.MsgShot:
		go RunScreenshot(sendResult, env.RequestID, func(name string, data []byte) error {
			return uploadFile(cfg, name, data)
		})

	case model.MsgDeploy:
		var task model.DeployTask
		if json.Unmarshal(env.Payload, &task) != nil {
			return
		}
		// относительный artifact-путь резолвим через адрес сервера
		if strings.HasPrefix(task.ArtifactURL, "/") {
			base := strings.TrimSuffix(wsToHTTP(cfg.ServerURL), "/agent")
			task.ArtifactURL = base + task.ArtifactURL
		}
		go func() {
			log.Printf("[agent] deploy project %q (mode=%s image=%s)", task.Name, task.Mode, task.Image)
			if task.Mode != "process" {
				if out, err := Pull(task.Image); err != nil {
					log.Printf("[agent] pull failed: %v\n%s", err, out)
					sendStatus(model.ProjectStatus{ProjectID: task.ProjectID, Status: "failed", Error: truncate(out+err.Error(), 500)})
					return
				}
			}
			out, err := Run(task)
			if err != nil {
				log.Printf("[agent] run failed: %v\n%s", err, out)
				sendStatus(model.ProjectStatus{ProjectID: task.ProjectID, Status: "failed", Error: truncate(out+err.Error(), 500)})
				return
			}
			sendStatus(model.ProjectStatus{ProjectID: task.ProjectID, Status: "running", Container: containerName(task.Name)})
		}()

	case model.MsgStop:
		var task model.StopTask
		if json.Unmarshal(env.Payload, &task) != nil {
			return
		}
		go func() {
			if err := Stop(task.Name); err != nil {
				sendStatus(model.ProjectStatus{ProjectID: task.ProjectID, Status: "failed", Error: err.Error()})
				return
			}
			if task.Remove {
				if err := Remove(task.Name); err != nil {
					sendStatus(model.ProjectStatus{ProjectID: task.ProjectID, Status: "failed", Error: err.Error()})
					return
				}
			}
			sendStatus(model.ProjectStatus{ProjectID: task.ProjectID, Status: "stopped"})
		}()

	case model.MsgLogsReq:
		var req model.LogsRequest
		if json.Unmarshal(env.Payload, &req) != nil {
			return
		}
		go func() {
			if req.Follow {
				reader, cancel, err := LogsFollow(req.Name)
				if err != nil {
					sendLogs(req.ProjectID, "log follow error: "+err.Error(), true)
					return
				}
				defer cancel()
				buf := make([]byte, 4096)
				for {
					n, err := reader.Read(buf)
					if n > 0 {
						sendLogs(req.ProjectID, string(buf[:n]), false)
					}
					if err != nil {
						sendLogs(req.ProjectID, "", true)
						return
					}
				}
			}
			data, err := Logs(req.Name, req.Tail)
			if err != nil {
				sendLogs(req.ProjectID, "log error: "+err.Error(), true)
				return
			}
			sendLogs(req.ProjectID, data, true)
		}()

	case model.MsgRecon:
		go func() {
			_ = reconOnce(cfg)
		}()

	case model.MsgUpdate:
		go func() {
			log.Println("[agent] self-update requested")
			if err := downloadAndSwap(cfg.ServerURL); err != nil {
				log.Printf("[agent] self-update failed: %v", err)
			}
		}()
	}
}

// warnDockerMissingOnce — «docker не найден» печатается ОДИН раз за запуск
// агента, а не на каждом цикле recon.
var warnDockerMissingOnce sync.Once

func reconOnce(cfg Config) error {
	// v0.6.0: шлём не только docker-контейнеры, но и process-проекты с живостью
	// процесса. Раньше ноды без Docker (process-режим) были «слепыми»: упавший
	// процесс числился running навсегда, и домен отдавал 502 неделями.
	res := model.ReconResult{Containers: []model.ContainerInfo{}, Processes: []model.ProcessInfo{}}
	infos, err := ListManaged()
	if err != nil {
		// Отсутствие Docker — НОРМА для process-режима, а не поломка.
		// Раньше эта строка печаталась каждые 60 с («docker ps: executable
		// file not found») и при 2 МиБ ротации вытесняла из agent.log все
		// полезные записи, включая сообщения о падениях проектов.
		// Теперь: если Docker есть, а список не получился — это настоящая
		// ошибка, пишем каждый раз; если Docker нет — один раз за запуск.
		if DockerAvailable() {
			log.Printf("[agent] list managed: %v", err)
		} else {
			warnDockerMissingOnce.Do(func() {
				log.Printf("[agent] docker не найден — доступен только process-режим")
			})
		}
	}
	for _, i := range infos {
		res.Containers = append(res.Containers, model.ContainerInfo{Name: i.Name, Image: i.Image, Status: i.Status})
	}
	for _, p := range ListProcesses() {
		res.Processes = append(res.Processes, p)
	}
	return sendResultFunc(model.Envelope{Type: model.MsgReconRes, Payload: mustJSON(res)})
}

// mustJSON — marshal без ошибки (типы статичны).
func mustJSON(v any) []byte {
	raw, _ := json.Marshal(v)
	return raw
}

// sendResultFunc — отправка envelope в текущее соединение; ссылка ставится
// при каждом успешном подключении в readLoop.
var sendResultFunc = func(env model.Envelope) error { return errNoConn }

var (
	tokMu    sync.RWMutex
	tokValue string
)

// setCurrentToken запоминает токен текущего подключения — он нужен агентным
// подсистемам (скачивание артефактов) вне основного цикла чтения.
func setCurrentToken(t string) {
	tokMu.Lock()
	tokValue = t
	tokMu.Unlock()
}

// currentToken возвращает токен текущего подключения ("" если не подключён).
func currentToken() string {
	tokMu.RLock()
	defer tokMu.RUnlock()
	return tokValue
}

var errNoConn = errConst("no active connection")

// sendEnvelope — как send, но принимает готовый envelope.
func sendEnvelope(conn *websocket.Conn, env model.Envelope) error {
	return conn.WriteJSON(env)
}

func send(conn *websocket.Conn, typ string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return conn.WriteJSON(model.Envelope{Type: typ, Payload: raw})
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// httpHeaderWithToken — заголовки для dial (токен передаётся в hello).
func httpHeaderWithToken(token string) map[string][]string {
	return map[string][]string{}
}

var errBadFirstFrame = errConst("unexpected first frame from server")

type errConst string

func (e errConst) Error() string { return string(e) }
