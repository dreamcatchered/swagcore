package ai

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dreamcatchered/swagcore/internal/hub"
	"github.com/dreamcatchered/swagcore/internal/model"
	"github.com/dreamcatchered/swagcore/internal/store"
)

// Tools — тулзы ИИ-агента: платформа + файловый воркспейс.
type Tools struct {
	St  *store.Store
	Hub *hub.Hub
	Dep Deployer // деплой проектов (переиспользует логику api)
	WS  string   // корень воркспейса (файловая папка агента)
}

// Deployer — интерфейс деплоя (реализует api.Server, чтобы не зациклить импорты).
type Deployer interface {
	AIDeployYAML(yaml string) (int64, error) // создать/обновить проект из YAML, вернуть id
}

// jobs — фоновые задачи чатов (работают после закрытия вкладки).
type job struct {
	cancel chan struct{}
}

// Agent — состояние ИИ-агента.
type Agent struct {
	st      *store.Store
	hub     *hub.Hub
	dep     Deployer
	client  *Client
	wsRoot  string
	dbDir   string
	db      *sql.DB
	mu      sync.Mutex
	running map[int64]*job // chatID -> job
}

// New создаёт ИИ-агента: wsRoot — рабочая папка (обычно /workspace), dbDir — каталог БД чатов.
func New(st *store.Store, h *hub.Hub, dep Deployer, groqKey, wsRoot, dbDir string) *Agent {
	_ = os.MkdirAll(wsRoot, 0o755)
	a := &Agent{
		st:      st,
		hub:     h,
		dep:     dep,
		client:  NewClient(groqKey, ""),
		wsRoot:  wsRoot,
		dbDir:   dbDir,
		running: map[int64]*job{},
	}
	a.db = a.openChatDB()
	return a
}

// ---------- БД чатов ----------

func (a *Agent) openChatDB() *sql.DB {
	path := filepath.Join(a.dbDir, "ai-chats.db")
	// миграция: старая БД жила внутри воркспейса
	old := filepath.Join(a.wsRoot, "chats.db")
	if _, err := os.Stat(old); err == nil {
		if _, err2 := os.Stat(path); err2 != nil {
			data, err3 := os.ReadFile(old)
			if err3 == nil {
				_ = os.WriteFile(path, data, 0o644)
			}
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS chats(id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT DEFAULT 'новый чат', created_at INTEGER, updated_at INTEGER)`,
		`CREATE TABLE IF NOT EXISTS messages(id INTEGER PRIMARY KEY AUTOINCREMENT, chat_id INTEGER, role TEXT, content TEXT, tools TEXT, created_at INTEGER)`,
	}
	for _, s := range stmts {
		_, _ = db.Exec(s)
	}
	return db
}

// CreateChat — новый чат.
func (a *Agent) CreateChat(title string) int64 {
	if a.db == nil {
		return 0
	}
	if title == "" {
		title = "новый чат"
	}
	res, err := a.db.Exec(`INSERT INTO chats(title, created_at, updated_at) VALUES(?,?,?)`, title, time.Now().Unix(), time.Now().Unix())
	if err != nil {
		return 0
	}
	id, _ := res.LastInsertId()
	return id
}

// ListChats — список чатов.
func (a *Agent) ListChats() []map[string]any {
	out := []map[string]any{}
	if a.db == nil {
		return out
	}
	rows, err := a.db.Query(`SELECT id, title, updated_at FROM chats ORDER BY updated_at DESC LIMIT 100`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var title string
		var updated int64
		if rows.Scan(&id, &title, &updated) == nil {
			out = append(out, map[string]any{"id": id, "title": title, "updated": updated})
		}
	}
	return out
}

// Messages — история чата.
func (a *Agent) Messages(chatID int64) []map[string]any {
	out := []map[string]any{}
	if a.db == nil {
		return out
	}
	rows, err := a.db.Query(`SELECT role, content, tools, created_at FROM messages WHERE chat_id=? ORDER BY id`, chatID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var role, content, tools string
		var ts int64
		if rows.Scan(&role, &content, &tools, &ts) == nil {
			m := map[string]any{"role": role, "content": content, "ts": ts}
			if tools != "" {
				m["tools"] = tools
			}
			out = append(out, m)
		}
	}
	return out
}

func (a *Agent) saveMsg(chatID int64, role, content, tools string) {
	if a.db == nil {
		return
	}
	_, _ = a.db.Exec(`INSERT INTO messages(chat_id, role, content, tools, created_at) VALUES(?,?,?,?,?)`,
		chatID, role, content, tools, time.Now().Unix())
	_, _ = a.db.Exec(`UPDATE chats SET updated_at=? WHERE id=?`, time.Now().Unix(), chatID)
}

// ---------- Системный промпт ----------

func (a *Agent) systemPrompt() string {
	// живой контекст платформы
	nodes, _ := a.st.ListNodes()
	projects, _ := a.st.ListProjects()
	var nb, pb strings.Builder
	for _, n := range nodes {
		fmt.Fprintf(&nb, "  id=%d %s %s/%s %s docker=%v mem=%dMB", n.ID, n.Hostname, n.OS, n.Arch, n.Status, n.HasDocker, n.MaxMemMB)
		if n.Metrics != nil {
			fmt.Fprintf(&nb, " cpu=%.0f%% ram=%d/%dMB", n.Metrics.CPUPct, n.Metrics.MemUsedMB, n.Metrics.MemTotalMB)
		}
		nb.WriteString("\n")
	}
	for _, p := range projects {
		fmt.Fprintf(&pb, "  id=%d %s status=%s mode=%s image=%s domain=%s node=%d\n",
			p.ID, p.Name, p.Status, p.Manifest.Mode, p.Manifest.Image, p.Manifest.Domain, p.NodeID)
	}
	return fmt.Sprintf(`Ты — ИИ-агент платформы swagCore (мини-PaaS). Ты управляешь инфраструктурой через тулзы и действуешь автономно.

ЖЕЛЕЗНЫЕ ПРАВИЛА:
1. НИКОГДА не придумывай результаты действий. Если ты не вызвал тулзу — ты этого НЕ делал. Отчитывайся «готово» ТОЛЬКО если реальный вызов тулзы вернул успех. Это важнее всего.
2. Любое действие (создать файл, задеплоить, выполнить команду) = вызов тулзы. Просто написать текстом «я создал» — запрещено.
3. После deploy_project всегда проверяй результат вызовом list_projects (подожди ~30 сек через workspace_run "sleep 30", затем list_projects). Если статус не running — разбирайся и чини.
4. Отвечай по-русски, кратко. Текущая дата: сентябрь 2026.

КАК ДЕПЛОИТЬ САЙТ (типовой сценарий, по шагам):
  a) workspace_write: создай файлы сайта (напр. hello/index.html)
  b) workspace_run: собери tar.gz В КОРНЕ воркспейса: cd /workspace/hello && tar -czf /workspace/hello.tar.gz .
  c) publish_artifact: project=<имя>, path=hello.tar.gz → вернёт artifact-путь и sha256
  d) deploy_project: YAML вида:
     name: <имя>
     image: nginx:alpine
     ports: ["80:80"]
     artifact: /artifacts/<имя>/<имя>.tar.gz
     artifact_sha: <sha256 из шага c>
     mount_path: /usr/share/nginx/html
     domain: <имя>.swag.best
     ВАЖНО: ports ОБЯЗАТЕЛЕН в виде "host:container" (напр. "80:80", "3000:3000"),
     иначе контейнер будет недоступен снаружи ноды. Порт host = порт контейнера.
  e) wait_project: project_id=<id из шага d> → дождись running и отчитайся честно
     Если failed — посмотри project_logs и исправь.

ПРОЧЕЕ:
- Команды на нодах — тулза exec (node_id из list_nodes). Скриншот — screenshot.
- Воркспейс — твоя папка на сервере: workspace_run/read/write/ls.
- placement по умолчанию all. preferred_node — hostname ноды.
- Домены <имя>.swag.best работают автоматом (wildcard TLS).

НОДЫ:
%sПРОЕКТЫ:
%s`, nb.String(), pb.String())
}

// ---------- Определения тулзов ----------

func (a *Agent) toolDefs() []Tool {
	obj := func(props string, required string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"type":"object","properties":%s,"required":%s}`, props, required))
	}
	return []Tool{
		{Type: "function", Function: ToolFunction{Name: "list_nodes", Description: "Список нод платформы со статусами и метриками", Parameters: obj("{}", "[]")}},
		{Type: "function", Function: ToolFunction{Name: "list_projects", Description: "Список проектов платформы со статусами", Parameters: obj("{}", "[]")}},
		{Type: "function", Function: ToolFunction{Name: "exec", Description: "Выполнить shell-команду на ноде (cmd на windows, sh на linux)", Parameters: obj(`{"node_id":{"type":"integer","description":"ID ноды"},"command":{"type":"string"},"timeout":{"type":"integer","description":"сек, по умолчанию 30"}}`, `["node_id","command"]`)}},
		{Type: "function", Function: ToolFunction{Name: "screenshot", Description: "Скриншот всех экранов ноды (вернёт имя файла в галерее)", Parameters: obj(`{"node_id":{"type":"integer"}}`, `["node_id"]`)}},
		{Type: "function", Function: ToolFunction{Name: "deploy_project", Description: "Создать/обновить и задеплоить проект из YAML-манифеста (как в админке). Вернёт id проекта. Автодеплой на ноды по placement", Parameters: obj(`{"yaml":{"type":"string","description":"полный YAML манифеста: name, image/mode, ports, env, volumes, resources, replicas, placement, preferred_node, artifact, artifact_sha, mount_path, domain"}}`, `["yaml"]`)}},
		{Type: "function", Function: ToolFunction{Name: "project_action", Description: "Действие над проектом: start/stop/restart", Parameters: obj(`{"project_id":{"type":"integer"},"action":{"type":"string","enum":["start","stop","restart"]}}`, `["project_id","action"]`)}},
		{Type: "function", Function: ToolFunction{Name: "project_logs", Description: "Последние логи проекта", Parameters: obj(`{"project_id":{"type":"integer"},"lines":{"type":"integer","description":"сколько строк, по умолчанию 100"}}`, `["project_id"]`)}},
		{Type: "function", Function: ToolFunction{Name: "wait_project", Description: "Подождать пока проект достигнет статуса (по умолчанию running, максимум 3 мин) и вернуть результат. ВСЕГДА вызывай после deploy_project вместо sleep", Parameters: obj(`{"project_id":{"type":"integer"},"status":{"type":"string","enum":["running","stopped"],"description":"какой статус ждать"}}`, `["project_id"]`)}},
		{Type: "function", Function: ToolFunction{Name: "workspace_run", Description: "Выполнить команду в воркспейсе агента на сервере (bash, рабочая папка /workspace). Для git clone, curl, tar, сборки артефактов. ВАЖНО: файлы создавай внутри /workspace, tar.gz клади в /workspace (не в подкаталоги), потом publish_artifact с относительным путём", Parameters: obj(`{"command":{"type":"string"},"timeout":{"type":"integer","description":"сек, по умолчанию 60"}}`, `["command"]`)}},
		{Type: "function", Function: ToolFunction{Name: "workspace_read", Description: "Прочитать файл из воркспейса (текст, до 20КБ)", Parameters: obj(`{"path":{"type":"string","description":"относительный путь"}}`, `["path"]`)}},
		{Type: "function", Function: ToolFunction{Name: "workspace_write", Description: "Записать текстовый файл в воркспейс", Parameters: obj(`{"path":{"type":"string"},"content":{"type":"string"}}`, `["path","content"]`)}},
		{Type: "function", Function: ToolFunction{Name: "workspace_ls", Description: "Список файлов каталога воркспейса", Parameters: obj(`{"path":{"type":"string","description":"относительный путь, по умолчанию корень"}}`, "[]")}},
		{Type: "function", Function: ToolFunction{Name: "publish_artifact", Description: "Опубликовать tar.gz из воркспейса как артефакт проекта (для deploy_project). Вернёт путь artifact и sha256", Parameters: obj(`{"project":{"type":"string","description":"имя проекта"},"path":{"type":"string","description":"путь к tar.gz в воркспейсе"}}`, `["project","path"]`)}},
	}
}

// ---------- Выполнение тулзов ----------

var safeCmd = regexp.MustCompile(`^[\w\s./:@\-+=,:'"()|&;<>*?\[\]{}$!#^\\-]*$`)

func (a *Agent) execTool(name, argsJSON string) (string, error) {
	var args map[string]any
	_ = json.Unmarshal([]byte(argsJSON), &args)
	get := func(k string) string {
		if v, ok := args[k]; ok {
			return fmt.Sprintf("%v", v)
		}
		return ""
	}
	getInt := func(k string) int64 {
		if v, ok := args[k].(float64); ok {
			return int64(v)
		}
		return 0
	}

	switch name {
	case "list_nodes":
		nodes, err := a.st.ListNodes()
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, n := range nodes {
			fmt.Fprintf(&b, "id=%d %s %s/%s %s docker=%v", n.ID, n.Hostname, n.OS, n.Arch, n.Status, n.HasDocker)
			if n.Metrics != nil {
				fmt.Fprintf(&b, " cpu=%.0f%% ram=%d/%dMB disk=%.0f/%.0fGB uptime=%.1fh", n.Metrics.CPUPct, n.Metrics.MemUsedMB, n.Metrics.MemTotalMB, n.Metrics.DiskUsedGB, n.Metrics.DiskTotalGB, float64(n.Metrics.UptimeSec)/3600)
			}
			b.WriteString("\n")
		}
		return b.String(), nil

	case "list_projects":
		projects, err := a.st.ListProjects()
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, p := range projects {
			fmt.Fprintf(&b, "id=%d %s status=%s mode=%s image=%s domain=%s node=%d\n", p.ID, p.Name, p.Status, p.Manifest.Mode, p.Manifest.Image, p.Manifest.Domain, p.NodeID)
		}
		return b.String(), nil

	case "exec":
		nodeID := getInt("node_id")
		cmd := get("command")
		timeout := int(getInt("timeout"))
		if timeout <= 0 {
			timeout = 30
		}
		res, err := a.hub.ExecSync(nodeID, cmd, timeout)
		if err != nil {
			return "", err
		}
		if !res.OK {
			return "COMMAND FAILED: " + res.Error + "\n" + truncate(res.Output, 6000), nil
		}
		return truncate(res.Output, 6000), nil

	case "screenshot":
		nodeID := getInt("node_id")
		if err := a.hub.RequestScreenshot(nodeID, ""); err != nil {
			return "", err
		}
		return "команда отправлена — файл появится в /screenshots (проверь через workspace_ls недоступно; скажи пользователю смотреть галерею ноды)", nil

	case "deploy_project":
		id, err := a.dep.AIDeployYAML(get("yaml"))
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("проект id=%d создан/обновлён и отправлен на деплой (проверь статус через wait_project)", id), nil

	case "wait_project":
		// ждём достижения проектом статуса running/stopped (максимум 3 мин)
		pid := getInt("project_id")
		want := get("status")
		if want == "" {
			want = "running"
		}
		deadline := time.Now().Add(3 * time.Minute)
		var last string
		for time.Now().Before(deadline) {
			p, err := a.st.ProjectByID(pid)
			if err == nil && p != nil {
				last = p.Status
				if p.Status == want || p.Status == model.StatusFailed {
					errMsg := ""
					if p.Error != "" {
						errMsg = " ошибка: " + p.Error
					}
					return fmt.Sprintf("проект id=%d статус=%s%s", pid, p.Status, errMsg), nil
				}
			}
			time.Sleep(10 * time.Second)
		}
		return fmt.Sprintf("таймаут ожидания: проект id=%d всё ещё %s", pid, last), nil

	case "project_action":
		p, err := a.st.ProjectByID(getInt("project_id"))
		if err != nil || p == nil {
			return "", fmt.Errorf("проект не найден")
		}
		switch get("action") {
		case "start", "restart":
			_ = a.st.SetProjectStatus(p.ID, model.StatusDesiredRunning, "", "")
			return "запущен деплой (результат через ~30 сек: list_projects)", nil
		case "stop":
			_ = a.st.SetProjectStatus(p.ID, model.StatusDesiredStopped, "", "")
			if p.NodeID != 0 {
				_ = a.hub.Stop(p.NodeID, model.StopTask{ProjectID: p.ID, Name: p.Name, Remove: false})
			}
			return "остановлен", nil
		}
		return "", fmt.Errorf("unknown action")

	case "project_logs":
		logs, _ := a.st.GetLogs(getInt("project_id"), 100)
		if len(logs) == 0 {
			return "(логов нет — запроси project_action logs и повтори через 10 сек)", nil
		}
		var b strings.Builder
		for _, l := range logs {
			b.WriteString(l.Data + "\n")
		}
		return truncate(b.String(), 6000), nil

	case "workspace_run":
		return a.wsRun(get("command"), int(getInt("timeout")))

	case "workspace_read":
		return a.wsRead(get("path"))

	case "workspace_write":
		return a.wsWrite(get("path"), get("content"))

	case "workspace_ls":
		return a.wsLs(get("path"))

	case "publish_artifact":
		return a.wsPublish(get("project"), get("path"))
	}
	return "", fmt.Errorf("unknown tool %s", name)
}

// ---------- Воркспейс (файловая папка агента на сервере) ----------

func (a *Agent) wsAbs(rel string) (string, error) {
	clean := filepath.Clean("/" + rel) // защита от ..
	p := filepath.Join(a.wsRoot, clean)
	if !strings.HasPrefix(p, filepath.Clean(a.wsRoot)) {
		return "", fmt.Errorf("path escape")
	}
	return p, nil
}

func (a *Agent) wsRun(command string, timeout int) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("пустая команда")
	}
	if timeout <= 0 {
		timeout = 60
	}
	if timeout > 600 {
		timeout = 600
	}
	out, err := runShell(a.wsRoot, command, timeout)
	if err != nil {
		return truncate(out, 6000) + "\n(exit: " + err.Error() + ")", nil
	}
	if strings.TrimSpace(out) == "" {
		return "(пусто)", nil
	}
	return truncate(out, 6000), nil
}

func (a *Agent) wsRead(path string) (string, error) {
	p, err := a.wsAbs(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	if len(data) > 20<<10 {
		return truncate(string(data), 20<<10) + "\n…(файл обрезан, 20KB)", nil
	}
	return string(data), nil
}

func (a *Agent) wsWrite(path, content string) (string, error) {
	p, err := a.wsAbs(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return "", err
	}
	return "записано " + fmt.Sprint(len(content)) + " байт: " + path, nil
}

func (a *Agent) wsLs(path string) (string, error) {
	p, err := a.wsAbs(path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			b.WriteString(e.Name() + "/\n")
		} else {
			info, _ := e.Info()
			fmt.Fprintf(&b, "%s (%d б)\n", e.Name(), info.Size())
		}
	}
	if b.Len() == 0 {
		return "(пусто)", nil
	}
	return b.String(), nil
}

// wsPublish — копирует tar.gz из воркспейса в artifacts/{project}/ и считает sha256.
func (a *Agent) wsPublish(project, path string) (string, error) {
	if project == "" || path == "" {
		return "", fmt.Errorf("нужны project и path")
	}
	src, err := a.wsAbs(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		// фолбэк: ищем .tar.gz по имени проекта в корне воркспейса
		fb := filepath.Join(a.wsRoot, sanitizeName(project)+".tar.gz")
		if d2, err2 := os.ReadFile(fb); err2 == nil {
			data, err = d2, nil
		} else {
			return "", fmt.Errorf("файл не найден: %s (и %s тоже)", path, sanitizeName(project)+".tar.gz")
		}
	}
	artDir := a.artifactsDir()
	dst := filepath.Join(artDir, sanitizeName(project))
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return "", err
	}
	fname := sanitizeName(project) + ".tar.gz"
	if err := os.WriteFile(filepath.Join(dst, fname), data, 0o644); err != nil {
		return "", err
	}
	sha := sha256Hex(data)
	return fmt.Sprintf(`artifact: /artifacts/%s/%s
artifact_sha: %s`, sanitizeName(project), fname, sha), nil
}

func sanitizeName(s string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9_.-]`)
	return re.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-")
}

// ---------- Чат ----------

// chatHistory собирает историю чата для модели (последние 24 сообщения).
func (a *Agent) chatHistory(chatID int64) []Message {
	msgs := a.Messages(chatID)
	var out []Message
	start := 0
	if len(msgs) > 24 {
		start = len(msgs) - 24
	}
	for _, m := range msgs[start:] {
		role := m["role"].(string)
		content := m["content"].(string)
		if role == "tool" {
			continue // tool-сообщения не валидны без tool_calls контекста
		}
		out = append(out, Message{Role: role, Content: content})
	}
	return out
}

// SendUserMessage — обработка сообщения пользователя (синхронно).
// Возвращает финальный ответ агента. Вызывается из фонового воркера.
func (a *Agent) SendUserMessage(chatID int64, text string) (string, error) {
	a.saveMsg(chatID, "user", text, "")
	messages := []Message{{Role: "system", Content: a.systemPrompt()}}
	messages = append(messages, a.chatHistory(chatID)...)

	steps := []string{}
	answer, err := a.client.RunChat(messages, a.toolDefs(), a.execTool, func(step string) {
		a.mu.Lock()
		steps = append(steps, step)
		a.mu.Unlock()
		log.Printf("[ai] chat=%d %s", chatID, step)
	})
	if err != nil {
		a.mu.Lock()
		b, _ := json.Marshal(steps)
		a.mu.Unlock()
		a.saveMsg(chatID, "assistant", "Ошибка: "+err.Error()+"\n\nВыполнено до ошибки:\n"+strings.Join(steps, "\n"), string(b))
		return "", err
	}
	toolsLog := ""
	a.mu.Lock()
	if len(steps) > 0 {
		b, _ := json.Marshal(steps)
		toolsLog = string(b)
	}
	a.mu.Unlock()
	a.saveMsg(chatID, "assistant", answer, toolsLog)
	// авто-титул чата по первому сообщению
	a.maybeTitle(chatID, text)
	return answer, nil
}

func (a *Agent) maybeTitle(chatID int64, firstMsg string) {
	if a.db == nil {
		return
	}
	var cnt int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat_id=?`, chatID).Scan(&cnt)
	if cnt <= 2 {
		t := []rune(firstMsg)
		if len(t) > 40 {
			t = t[:40]
		}
		_, _ = a.db.Exec(`UPDATE chats SET title=? WHERE id=?`, string(t), chatID)
	}
}

// ---------- Фоновый воркер ----------

// RunChatAsync запускает обработку в фоне (не зависит от вкладки).
// Прогресс виден через Progress(chatID); результат появится в Messages.
func (a *Agent) RunChatAsync(chatID int64, text string) {
	a.mu.Lock()
	if _, busy := a.running[chatID]; busy {
		a.mu.Unlock()
		return // уже работает — сообщение подхватится следующим
	}
	j := &job{cancel: make(chan struct{})}
	a.running[chatID] = j
	a.mu.Unlock()
	go func() {
		defer func() {
			a.mu.Lock()
			delete(a.running, chatID)
			a.mu.Unlock()
			if r := recover(); r != nil {
				a.saveMsg(chatID, "assistant", fmt.Sprintf("внутренняя ошибка агента: %v", r), "")
			}
		}()
		_, _ = a.SendUserMessage(chatID, text)
	}()
}

// Busy — чат сейчас обрабатывается?
func (a *Agent) Busy(chatID int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, busy := a.running[chatID]
	return busy
}
