package hub

// Наблюдение за падением нод.
//
// ПРОБЛЕМА (v0.7.0, воспроизведено на ноде angelica): SweepOffline молча
// помечал ноды офлайн и писал в лог одну безымянную строку
// «swept 1 offline node(s)». Ни события в панели, ни уведомления — про падение
// ноды можно было не знать сутки, пока не посмотришь случайно.
//
// Теперь переход online -> offline:
//   - попадает в лог с именем ноды, ОС и временем последнего heartbeat;
//   - регистрируется как событие в панели (его видно на /events);
//   - уходит в webhook, если он задан (Telegram-боты, слак-входящие и т.п.).
//
// Отдельный файл, чтобы логика алертов не размазывалась по Sweep().

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dreamcatchered/swagcore/internal/model"
	"github.com/dreamcatchered/swagcore/internal/store"
)

// AlertWebhookURL — адрес для алертов. Задаётся переменной окружения
// SWAGCORE_ALERT_WEBHOOK. Пусто — алерты только в лог и в панель.
func AlertWebhookURL() string {
	return strings.TrimSpace(os.Getenv("SWAGCORE_ALERT_WEBHOOK"))
}

// watcher следит за переходами online -> offline.
type watcher struct {
	st  *store.Store
	hub *Hub

	mu   sync.Mutex
	prev map[int64]string // nodeID -> прошлый статус
}

func newWatcher(st *store.Store, h *Hub) *watcher {
	return &watcher{st: st, hub: h, prev: nil}
}

// run — тик раз в 15 секунд: замечает пропажу нод.
func (w *watcher) run(stop <-chan struct{}) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			w.check()
		}
	}
}

// check сравнивает текущие статусы с предыдущими и шлёт алерты.
func (w *watcher) check() {
	nodes, err := w.st.ListNodes()
	if err != nil {
		return
	}

	w.mu.Lock()
	first := w.prev == nil
	now := make(map[int64]string, len(nodes))
	for i := range nodes {
		now[nodes[i].ID] = nodes[i].Status
	}
	prev := w.prev
	w.prev = now
	w.mu.Unlock()

	if first {
		// Стартовый срез: молча пропустить нельзя.
		//
		// Watcher ловит ПЕРЕХОДЫ, но платформу часто перезапускают, когда
		// какая-то нода уже лежит. Тогда prev заполняется уже офлайн-состоянием,
		// перехода не происходит — и про упавшую ноду не узнаёшь вообще.
		// Именно так вышло с angelica: сервер перезапустился, нода была офлайн,
		// алерт не сработал.
		var down []string
		for i := range nodes {
			if nodes[i].Status != "online" {
				down = append(down, nodes[i].Hostname)
			}
		}
		if len(down) > 0 {
			msg := "при запуске платформы не в сети: " + strings.Join(down, ", ")
			log.Printf("[alert] %s", msg)
			w.st.AddEvent("warn", "node", msg)
			w.post(map[string]any{
				"level": "warn", "source": "node", "event": "startup_offline",
				"message": msg, "nodes": down, "at": time.Now().Format(time.RFC3339),
			})
		}
		return
	}

	for i := range nodes {
		n := &nodes[i]
		was, known := prev[n.ID]
		if !known {
			continue
		}
		if was == n.Status {
			continue
		}
		switch {
		case was == "online" && n.Status != "online":
			w.alertDown(n)
		case was != "online" && n.Status == "online":
			w.alertUp(n)
		}
	}
}

func (w *watcher) alertDown(n *model.Node) {
	age := ""
	if !n.LastSeen.IsZero() {
		age = humanSince(time.Since(n.LastSeen))
	}
	msg := "нода " + n.Hostname + " (" + n.OS + ", id " + itoa(n.ID) + ") перестала отвечать"
	if age != "" {
		msg += "; последний heartbeat " + age + " назад"
	}

	// 1) лог — с именем, а не «swept N offline»
	log.Printf("[alert] %s", msg)

	// 2) событие в панели
	w.st.AddEvent("warn", "node", msg)

	// 3) webhook
	w.post(map[string]any{
		"level":   "warn",
		"source":  "node",
		"message": msg,
		"node":    n.Hostname,
		"node_id": n.ID,
		"os":      n.OS,
		"event":   "node_offline",
		"at":      time.Now().Format(time.RFC3339),
	})
}

func (w *watcher) alertUp(n *model.Node) {
	msg := "нода " + n.Hostname + " (" + n.OS + ", id " + itoa(n.ID) + ") снова на связи"
	log.Printf("[alert] %s", msg)
	w.st.AddEvent("info", "node", msg)
	w.post(map[string]any{
		"level":   "info",
		"source":  "node",
		"message": msg,
		"node":    n.Hostname,
		"node_id": n.ID,
		"os":      n.OS,
		"event":   "node_online",
		"at":      time.Now().Format(time.RFC3339),
	})
}

// post отправляет алерт в webhook. Ошибки только в лог: алерт не должен
// ронять наблюдателя.
func (w *watcher) post(payload map[string]any) {
	url := AlertWebhookURL()
	if url == "" {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		log.Printf("[alert] webhook %s недоступен: %v", url, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("[alert] webhook %s ответил %d", url, resp.StatusCode)
	}
}

func humanSince(d time.Duration) string {
	switch {
	case d < time.Minute:
		return itoa(int64(d.Seconds())) + " с"
	case d < time.Hour:
		return itoa(int64(d.Minutes())) + " мин"
	default:
		return itoa(int64(d.Hours())) + " ч"
	}
}

