// Package hub — WebSocket-хаб: держит соединения агентов, маршрутизирует команды.
package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dreamcatchered/swagcore/internal/model"
	"github.com/dreamcatchered/swagcore/internal/store"
)

// AgentConn — живое соединение с агентом.
type AgentConn struct {
	NodeID   int64
	Hostname string
	Token    string // сырой токен (нужен для NodeTouch)
	realIP   string
	conn     *websocket.Conn
	send     chan []byte
	hub      *Hub
}

// pendingCall — ожидающий синхронного ответа вызов (для ExecSync).
type pendingCall struct {
	ch   chan model.Result
	task string
}

// ExecSync выполняет команду на ноде и ждёт результат (для ИИ-агента и API).
// Таймаут обязателен: агент может быть медленным или умереть посреди вызова.
func (h *Hub) ExecSync(nodeID int64, command string, timeoutSec int) (model.Result, error) {
	if timeoutSec <= 0 {
		timeoutSec = 30
	}
	if timeoutSec > 300 {
		timeoutSec = 300
	}
	reqID := "ai-" + hex.EncodeToString([]byte(time.Now().Format("150405.000000000")))
	h.mu.RLock()
	ac := h.conns[nodeID]
	h.mu.RUnlock()
	if ac == nil {
		return model.Result{}, ErrNodeOffline
	}
	ch := make(chan model.Result, 1)
	h.pendingMu.Lock()
	h.pending[reqID] = &pendingCall{ch: ch, task: "exec"}
	h.pendingMu.Unlock()
	defer func() {
		h.pendingMu.Lock()
		delete(h.pending, reqID)
		h.pendingMu.Unlock()
	}()
	task := model.ExecTask{Command: command, TimeoutSec: timeoutSec}
	raw, _ := json.Marshal(task)
	if err := h.SendTo(nodeID, model.Envelope{Type: model.MsgExec, RequestID: reqID, Payload: raw}); err != nil {
		return model.Result{}, err
	}
	select {
	case res := <-ch:
		return res, nil
	case <-time.After(time.Duration(timeoutSec+5) * time.Second):
		return model.Result{}, fmt.Errorf("node exec timeout (%ds)", timeoutSec)
	}
}

// Hub — реестр подключённых агентов.
type Hub struct {
	mu    sync.RWMutex
	conns map[int64]*AgentConn // по node_id

	st        *store.Store
	upgrader  websocket.Upgrader
	logsCb    func(projectID int64, data string)
	shotSaved func(nodeID int64, file string)
	driftCb   func(drift [][2]int64)

	pending   map[string]*pendingCall
	pendingMu sync.Mutex
}

// New создаёт хаб (токены проверяются через store.TokenByHash).
func New(st *store.Store) *Hub {
	return &Hub{
		conns:   map[int64]*AgentConn{},
		st:      st,
		pending: map[string]*pendingCall{},
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin:     func(r *http.Request) bool { return true },
		},
	}
}

// SetLogsCallback регистрирует обработчик входящих логов от агентов.
func (h *Hub) SetLogsCallback(cb func(projectID int64, data string)) {
	h.logsCb = cb
}

// SetDriftCallback регистрирует обработчик расхождений факта и БД
// (реконсилер передеплоит потерянные инстансы).
func (h *Hub) SetDriftCallback(cb func(drift [][2]int64)) {
	h.driftCb = cb
}

// HandleAgentWS — HTTP-хендлер апгрейда до WS для агентов.
func (h *Hub) HandleAgentWS(w http.ResponseWriter, r *http.Request) {
	c, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[hub] upgrade: %v", err)
		return
	}
	ac := &AgentConn{conn: c, send: make(chan []byte, 256), hub: h, realIP: clientIP(r)}
	go ac.writePump()
	go ac.readLoop()
}

// retireDuplicateNodes убирает из реестра записи той же машины, кроме текущей.
//
// Два независимых механизма против дублей (в v0.5.x их не было вовсе, из-за чего
// платформа годами показывала две «одни и те же» ноды и приложения гасили друг
// друга гонкой за порт — в логах это было как «os error 10048»):
//
//  1) instance_id — постоянный идентификатор МАШИНЫ в файле агента. Две записи
//     автозагрузки с РАЗНЫМИ токенами дают разные node_id, и хаб их не вытеснял.
//     Сейчас новая нода приводит к себе, старые дубли удаляются каскадом.
//
//  2) legacy-ноды без instance_id (агенты < v0.6.0) с тем же hostname/ос/арх.
//     При обновлении платформы старый агент успевает переподключиться и создать
//     свою ноду; после его остановки остаётся «призрак». Новая нода подчищает
//     его автоматически — не нужно руками чистить список после апгрейда.
func (h *Hub) retireDuplicateNodes(ac *AgentConn, hello model.Hello, nodeID int64) {
	drop := func(ids []int64, reason string) {
		for _, old := range ids {
			if old == nodeID {
				continue
			}
			h.mu.Lock()
			if oc := h.conns[old]; oc != nil {
				oc.conn.Close()
				delete(h.conns, old)
			}
			h.mu.Unlock()
			if err := h.st.DeleteNodeCascade(old); err != nil {
				log.Printf("[hub] dedupe: delete node %d: %v", old, err)
				continue
			}
			h.st.AddEvent("warn", "hub", fmt.Sprintf(
				"убрана дублирующая запись ноды #%d (%s): %s", old, hello.Hostname, reason))
		}
	}

	if dups, err := h.st.DuplicateNodesByInstance(hello.InstanceID, nodeID); err == nil {
		drop(dups, "это та же машина, подключилась с другим токеном (проверьте автозагрузку — запись должна быть одна)")
	}
	if legacy, err := h.st.StaleLegacyNodes(hello.Hostname, hello.OS, hello.Arch); err == nil {
		drop(legacy, "осталась от агента старой версии, который уже заменён")
	}
}

// clientIP достаёт реальный IP агента (за nginx — из X-Real-IP/X-Forwarded-For).
func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	return r.RemoteAddr
}

// SendTo отправляет envelope конкретной ноде.
func (h *Hub) SendTo(nodeID int64, env model.Envelope) error {
	h.mu.RLock()
	ac := h.conns[nodeID]
	h.mu.RUnlock()
	if ac == nil {
		return ErrNodeOffline
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	select {
	case ac.send <- raw:
		return nil
	default:
		return ErrSendQueueFull
	}
}

// IsOnline проверяет, подключена ли нода.
func (h *Hub) IsOnline(nodeID int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.conns[nodeID] != nil
}

// OnlineCount — количество подключённых агентов.
func (h *Hub) OnlineCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

// RequestReconcile просит ноду прислать список контейнеров.
func (h *Hub) RequestReconcile(nodeID int64) error {
	return h.SendTo(nodeID, model.Envelope{Type: model.MsgRecon})
}

// ---------- внутреннее ----------

func (ac *AgentConn) readLoop() {
	defer ac.close()
	c := ac.conn
	// первый кадр — hello
	_ = c.SetReadDeadline(time.Now().Add(15 * time.Second))
	var env model.Envelope
	if err := c.ReadJSON(&env); err != nil {
		return
	}
	if env.Type != model.MsgHello {
		return
	}
	var hello model.Hello
	if err := json.Unmarshal(env.Payload, &hello); err != nil {
		return
	}
	_ = c.SetReadDeadline(time.Time{}) // дальше — только ping-таймаут
	c.SetReadLimit(1 << 20)

	hash := HashToken(hello.Token)
	tok, err := ac.hub.st.TokenByHash(hash)
	if err != nil {
		log.Printf("[hub] token lookup: %v", err)
		return
	}
	if tok == nil {
		log.Printf("[hub] node %q: unknown token", hello.Hostname)
		ac.send <- mustJSON(model.Envelope{Type: "error", Payload: mustJSON(map[string]string{"error": "unknown token"})})
		return
	}
	ac.hub.st.MarkTokenUsed(hash, hello.Hostname)

	ip := ac.realIP
	if ip == "" {
		ip = remoteIP(c)
	}
	// Лимиты: токен важнее hello (админ задал их осознанно при создании токена),
	// hello используется как fallback (флаги --max-mem/--max-cpu/--max-disk агента).
	mm, mc, md := hello.MaxMemMB, hello.MaxCPUs, hello.MaxDiskGB
	if tok.MaxMemMB > 0 || tok.MaxCPUs > 0 || tok.MaxDiskGB > 0 {
		mm, mc, md = tok.MaxMemMB, tok.MaxCPUs, tok.MaxDiskGB
	}
	nodeID, err := ac.hub.st.UpsertNodeByToken(hash, hello.Hostname, hello.OS, hello.Arch, hello.Version, ip, hello.HasDocker, mm, mc, md, hello.InstanceID)
	if err != nil {
		log.Printf("[hub] upsert node: %v", err)
		return
	}
	ac.NodeID = nodeID
	ac.Hostname = hello.Hostname
	ac.Token = hello.Token

	// ---- Анти-дубль машинки (v0.6.0) ----
	// Раньше две записи автозагрузки с РАЗНЫМИ токенами на одной Windows-машине
	// создавали две ноды с одинаковым hostname: разные token_hash -> разные
	// node_id -> вытеснения в хабе не было. Обе ноды работали, обе получали
	// один и тот же проект с placement: all, и приложения убивали друг друга
	// гонкой за порт (наблюдалось как "os error 10048"). Теперь агент шлёт
	// постоянный instance_id: новая нода приводит к себе, старый дубль
	// отключается и удаляется каскадом.
	// ---- Убираем дубли этой же машины (v0.6.0) ----
	ac.hub.retireDuplicateNodes(ac, hello, nodeID)

	ac.hub.mu.Lock()
	if old := ac.hub.conns[nodeID]; old != nil && old != ac {
		old.conn.Close() // вытесняем старое соединение той же ноды
	}
	ac.hub.conns[nodeID] = ac
	ac.hub.mu.Unlock()

	ac.hub.st.AddEvent("info", "hub", "нода подключилась: "+hello.Hostname+" (агент "+hello.Version+")")
	// welcome отправляем через очередь writePump — gorilla/websocket не терпит
	// конкурентных записей в одно соединение.
	ac.send <- mustJSON(model.Envelope{Type: model.MsgWelcome, Payload: mustJSON(model.Welcome{NodeID: nodeID, ServerVersion: model.Version})})

	// read deadline для heartbeat: если агент молчит 90 секунд — рвём
	for {
		_ = c.SetReadDeadline(time.Now().Add(90 * time.Second))
		var env model.Envelope
		if err := c.ReadJSON(&env); err != nil {
			break
		}
		ac.handleMessage(env)
	}
}

func (ac *AgentConn) handleMessage(env model.Envelope) {
	switch env.Type {
	case model.MsgPing:
		var ping model.Ping
		if json.Unmarshal(env.Payload, &ping) == nil {
			_ = ac.hub.st.NodeTouch(HashToken(ac.Token), &ping.Metrics)
		}
	case model.MsgStatus:
		var st model.ProjectStatus
		if json.Unmarshal(env.Payload, &st) == nil {
			mapped := mapStatus(st.Status)
			ac.hub.st.SetProjectStatus(st.ProjectID, mapped, st.Container, st.Error)
			// обновляем и статус экземпляра на этой ноде — иначе реконсилер
			// не узнает, что деплой завершился
			_ = ac.hub.st.SetInstanceStatus(st.ProjectID, ac.NodeID, instStatus(st.Status), st.Container, st.Error)
			ac.hub.st.AddEvent("info", "agent:"+ac.Hostname,
				"проект #"+itoa(st.ProjectID)+" → "+st.StatusLabel()+" "+st.Error)
		}
	case model.MsgLogs:
		var lc model.LogsChunk
		if json.Unmarshal(env.Payload, &lc) == nil && ac.hub.logsCb != nil {
			ac.hub.logsCb(lc.ProjectID, lc.Data)
		}
	case model.MsgReconRes:
		var rr model.ReconResult
		if json.Unmarshal(env.Payload, &rr) == nil {
			seenProc := map[string]bool{}
			for _, p := range rr.Processes {
				seenProc[p.Name] = p.Alive
			}
			// v0.6.0: успешный reconcile больше НЕ пишется в журнал. Раньше это давало
			// 1440 записей в сутки на ноду (94% таблицы events) — реальные события уезжали.
			// В журнал попадает только сам дрейф (warn) из SyncRecon ниже.
			seen := map[string]string{}
			for _, c := range rr.Containers {
				seen[c.Name] = c.Status
			}
			if drift, err := ac.hub.st.SyncRecon(ac.NodeID, seen, seenProc); err == nil && len(drift) > 0 && ac.hub.driftCb != nil {
				ac.hub.driftCb(drift)
			}
		}
	case model.MsgResult:
		var res model.Result
		if json.Unmarshal(env.Payload, &res) == nil {
			// синхронный вызов (ExecSync): отдать результат ожидающему
			if env.RequestID != "" {
				ac.hub.pendingMu.Lock()
				if pc, ok := ac.hub.pending[env.RequestID]; ok {
					delete(ac.hub.pending, env.RequestID)
					ac.hub.pendingMu.Unlock()
					select {
					case pc.ch <- res:
					default:
					}
					goto delivered
				}
				ac.hub.pendingMu.Unlock()
			}
		delivered:
			if res.Kind == "screenshot" && res.File != "" && ac.hub.shotSaved != nil {
				ac.hub.shotSaved(ac.NodeID, res.File)
			}
			_ = ac.hub.st.AddResult(ac.NodeID, res)
			if res.OK {
				ac.hub.st.AddEvent("info", "agent:"+ac.Hostname, res.KindLabel()+" — выполнено: "+res.Command+" → "+truncateStr(res.Output, 80))
			} else {
				ac.hub.st.AddEvent("warn", "agent:"+ac.Hostname, res.KindLabel()+" — ошибка: "+res.Error)
			}
		}
	}
}

// truncateStr обрезает строку для событий.
func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ExecCommand выполняет команду на ноде.
func (h *Hub) ExecCommand(nodeID int64, reqID, command string, timeoutSec int) error {
	task := model.ExecTask{Command: command, TimeoutSec: timeoutSec}
	raw, _ := json.Marshal(task)
	return h.SendTo(nodeID, model.Envelope{Type: model.MsgExec, RequestID: reqID, Payload: raw})
}

// PowerAction отправляет reboot/shutdown ноде.
func (h *Hub) PowerAction(nodeID int64, action string) error {
	raw, _ := json.Marshal(model.PowerTask{Action: action})
	return h.SendTo(nodeID, model.Envelope{Type: model.MsgPower, Payload: raw})
}

// RequestScreenshot просит ноду снять экран.
func (h *Hub) RequestScreenshot(nodeID int64, reqID string) error {
	return h.SendTo(nodeID, model.Envelope{Type: model.MsgShot, RequestID: reqID})
}

// SetScreenshotCallback регистрирует колбэк сохранения скриншота (для WS-броадкастов в UI).
func (h *Hub) SetScreenshotCallback(cb func(nodeID int64, file string)) {
	h.shotSaved = cb
}

var _ = context.Background

// mapStatus переводит статус агента в статус БД.
func mapStatus(s string) string {
	switch s {
	case "running":
		return model.StatusRunning
	case "stopped":
		return model.StatusStopped
	default:
		return model.StatusFailed
	}
}

// instStatus переводит статус агента в статус экземпляра.
func instStatus(s string) string {
	switch s {
	case "running":
		return model.InstRunning
	case "stopped":
		return model.InstStopped
	default:
		return model.InstFailed
	}
}

func (ac *AgentConn) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		ac.conn.Close()
	}()
	for {
		select {
		case msg, ok := <-ac.send:
			_ = ac.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				_ = ac.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := ac.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = ac.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := ac.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (ac *AgentConn) close() {
	if ac.NodeID != 0 {
		ac.hub.mu.Lock()
		if cur := ac.hub.conns[ac.NodeID]; cur == ac {
			delete(ac.hub.conns, ac.NodeID)
		}
		ac.hub.mu.Unlock()
		ac.hub.st.NodeOffline(ac.NodeID)
		ac.hub.st.AddEvent("warn", "hub", "нода отключилась: "+ac.Hostname)
	}
	close(ac.send)
	ac.conn.Close()
}

// Deploy запускает проект на ноде.
func (h *Hub) Deploy(nodeID int64, task model.DeployTask) error {
	raw, _ := json.Marshal(task)
	return h.SendTo(nodeID, model.Envelope{Type: model.MsgDeploy, Payload: raw})
}

// Stop останавливает (и опционально удаляет) проект на ноде.
func (h *Hub) Stop(nodeID int64, task model.StopTask) error {
	raw, _ := json.Marshal(task)
	return h.SendTo(nodeID, model.Envelope{Type: model.MsgStop, Payload: raw})
}

// RequestLogs запрашивает логи у ноды.
func (h *Hub) RequestLogs(nodeID int64, req model.LogsRequest) error {
	raw, _ := json.Marshal(req)
	return h.SendTo(nodeID, model.Envelope{Type: model.MsgLogsReq, Payload: raw})
}

// Sweep отключает ноды, которые не пинговали долго.
func (h *Hub) Sweep(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := h.st.SweepOffline(2 * time.Minute); err == nil && n > 0 {
				// Имена нод и алерты разбирает watcher (StartWatcher).
				// Без него в лог попадало только «swept N offline node(s)»,
				// и падение ноды можно было не заметить сутками — так
				// и пропал компьютер angelica.
				log.Printf("[hub] swept %d offline node(s)", n)
			}
		}
	}
}

// StartWatcher запускает наблюдателя за падением нод: переход
// online -> offline попадает в лог с именем, в события панели и в webhook.
func (h *Hub) StartWatcher(ctx context.Context) {
	go newWatcher(h.st, h).run(ctx.Done())
}

// ---------- утилиты ----------

// HashToken — sha256 токена (в БД храним только хеш).
func HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func remoteIP(c *websocket.Conn) string {
	return c.RemoteAddr().String()
}

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func itoa(i int64) string {
	return fmtInt(i)
}

func fmtInt(i int64) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
