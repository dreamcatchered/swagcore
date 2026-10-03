// Package api — REST API для админки и хендлеры HTML-страниц.
package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/dreamcatchered/swagcore/internal/ai"
	"github.com/dreamcatchered/swagcore/internal/hub"
	"github.com/dreamcatchered/swagcore/internal/model"
	"github.com/dreamcatchered/swagcore/internal/sched"
	"github.com/dreamcatchered/swagcore/internal/store"
)

// Server — зависимости API.
type Server struct {
	St         *store.Store
	Hub        *hub.Hub
	AdminToken string

	// PublicBase — публичный адрес платформы (напр. https://core.swag.best).
	// Используется для генерации команд установки на странице «Добавить ноду».
	PublicBase string

	tpl          *template.Template
	artifactsDir string
	AI           *ai.Agent

	// loginMu/loginFails — простая защита /login от перебора токена.
	loginMu    sync.Mutex
	loginFails map[string]loginFail
}

type loginFail struct {
	count int
	until time.Time
}

// New создаёт API-сервер и парсит шаблоны.
func New(st *store.Store, h *hub.Hub, adminToken, publicBase string) *Server {
	artDir := "/opt/swagcore/data/artifacts"
	if runtime.GOOS == "windows" {
		artDir = filepath.Join(os.TempDir(), "swagcore-artifacts")
	}
	publicBase = strings.TrimSuffix(strings.TrimSpace(publicBase), "/")
	if publicBase == "" {
		publicBase = "https://core.swag.best"
	}
	return &Server{
		St:           st,
		Hub:          h,
		AdminToken:   adminToken,
		PublicBase:   publicBase,
		artifactsDir: artDir,
		loginFails:   map[string]loginFail{},
		tpl: template.Must(template.New("root").Funcs(template.FuncMap{
			"seq":     seq,
			"divf":    divAny,
			"mulf":    mulAny,
			"mb":      humanMB,
			"gb":      humanGB,
			"pct":     pctOf,
			"since":   sinceHuman,
			"join":    joinAny,
			"baseURL": func() string { return publicBase },
		}).Parse(tplRoot + tplDashboard + tplNode + tplAddNode + tplNodes + tplProjects + tplProject + tplEvents + tplLogin + tplAI + tplFoot)),
	}
}

// RegisterRoutes вешает все маршруты.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// агенты (WS)
	mux.HandleFunc("/agent", s.Hub.HandleAgentWS)

	// страницы админки
	mux.HandleFunc("/", s.handlePage)
	mux.HandleFunc("/favicon.ico", s.handleFavicon)
	mux.HandleFunc("/favicon.svg", s.handleFavicon)
	mux.HandleFunc("/nodes", s.handlePage)
	mux.HandleFunc("/nodes/", s.handleNodePage)
	mux.HandleFunc("/projects", s.handlePage)
	mux.HandleFunc("/projects/", s.handleProjectPage)
	mux.HandleFunc("/events", s.handlePage)
	mux.HandleFunc("/add-node", s.handlePage)
	mux.HandleFunc("/ai", s.handlePage)
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)

	// action-роуты (POST из форм)
	mux.HandleFunc("/projects/deploy", s.requireAdmin(s.handleDeploy))
	mux.HandleFunc("/projects/action", s.requireAdmin(s.handleProjectAction))
	mux.HandleFunc("/projects/delete", s.requireAdmin(s.handleProjectDelete))
	mux.HandleFunc("/nodes/create-token", s.requireAdmin(s.handleCreateToken))
	mux.HandleFunc("/nodes/delete", s.requireAdmin(s.handleNodeDelete))
	mux.HandleFunc("/nodes/enable", s.requireAdmin(s.handleNodeEnable))
	mux.HandleFunc("/nodes/maxmem", s.requireAdmin(s.handleNodeMaxMem))
	mux.HandleFunc("/nodes/limits", s.requireAdmin(s.handleNodeLimits))
	mux.HandleFunc("/nodes/delete-token", s.requireAdmin(s.handleDeleteToken))
	mux.HandleFunc("/nodes/recon", s.requireAdmin(s.handleNodeRecon))
	mux.HandleFunc("/nodes/update", s.requireAdmin(s.handleNodeUpdate))
	mux.HandleFunc("/nodes/update-all", s.requireAdmin(s.handleNodeUpdateAll))
	mux.HandleFunc("/nodes/exec", s.requireAdmin(s.handleNodeExec))
	mux.HandleFunc("/nodes/power", s.requireAdmin(s.handleNodePower))
	mux.HandleFunc("/nodes/screenshot", s.requireAdmin(s.handleNodeScreenshot))

	// Раздача артефактов проектов.
	//
	// v0.6.0 (безопасность): раньше /artifacts/ был открыт всем, и листинг
	// директории показывал имена tar.gz — исходники/сборки проектов скачивал
	// кто угодно по ссылке. Теперь доступ только админу (cookie/Bearer) либо
	// агенту с валидным X-Node-Token (агенту артефакты нужны для деплоя).
	mux.Handle("/artifacts/", s.requireNodeOrAdmin(
		http.StripPrefix("/artifacts/", http.FileServer(http.Dir(s.artifactsDir)))))

	// upload артефакта проекта (multipart, поле file) — из админки
	mux.HandleFunc("/api/artifacts/", s.requireAdmin(s.handleArtifactUpload))

	// Раздача скриншотов — только админу.
	//
	// v0.6.0 (безопасность): /screenshots/ был полностью открыт, а там лежат
	// снимки рабочего стола пользователя. Имя файла угадывается (4 hex), так
	// что это была реальная утечка, а не только теоретическая.
	mux.Handle("/screenshots/", s.requireAdminHandler(
		http.StripPrefix("/screenshots/", http.FileServer(http.Dir(screenshotDir())))))

	// upload от агентов (скриншоты) — своя авторизация внутри
	mux.HandleFunc("/upload/", s.handleAgentUpload)

	// Одноразовая команда подключения новой ноды (POST из панели «Добавить ноду»)
	mux.HandleFunc("/api/bootstrap", s.requireAdmin(s.apiBootstrap))

	// JSON API
	mux.HandleFunc("/api/ai/chats", s.requireAdmin(s.apiAIChats))
	mux.HandleFunc("/api/ai/messages", s.requireAdmin(s.apiAIMessages))
	mux.HandleFunc("/api/ai/send", s.requireAdmin(s.apiAISend))
	mux.HandleFunc("/api/action", s.requireAdmin(s.apiAction))
	mux.HandleFunc("/api/screenshots", s.requireAdmin(s.apiScreenshots))
	mux.HandleFunc("/api/nodes", s.requireAdmin(s.apiNodes))
	mux.HandleFunc("/api/projects", s.requireAdmin(s.apiProjects))
	mux.HandleFunc("/api/events", s.requireAdmin(s.apiEvents))
	mux.HandleFunc("/api/logs/", s.requireAdmin(s.apiLogs))
	mux.HandleFunc("/api/summary", s.requireAdmin(s.apiSummary))
	mux.HandleFunc("/api/version", s.apiVersion)
	// /api/health — без авторизации: только факт «живо» + версия. Без секретов.
	mux.HandleFunc("/api/health", s.apiHealth)
}

// requireAdminHandler — то же, что requireAdmin, но для http.Handler
// (нужно для file server'ов /artifacts/ и /screenshots/).
func (s *Server) requireAdminHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.adminAuthorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireNodeOrAdmin пускает админа (cookie/Bearer) ИЛИ агента с валидным
// X-Node-Token. Нужно для /artifacts/: агенты качают оттуда файлы проектов.
func (s *Server) requireNodeOrAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.adminAuthorized(r) {
			next.ServeHTTP(w, r)
			return
		}
		if s.nodeAuthorized(r) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

// nodeAuthorized проверяет X-Node-Token агента.
//
// Принимаем ТОЛЬКО ЗАВЕДЕННЫЙ токен — либо ещё не использованный (запись в
// node_tokens), либо уже закреплённый за конкретной нодой (хеш в nodes).
// Второй вариант важен: если админ удалит или ротирует токен, уже подключённая
// нода не должна тут же потерять возможность заливать скриншоты и качать
// артефакты — иначе «скриншот перестал работать» без всяких ошибок на проде.
func (s *Server) nodeAuthorized(r *http.Request) bool {
	nt := r.Header.Get("X-Node-Token")
	if nt == "" {
		return false
	}
	h := hub.HashToken(nt)
	if tok, err := s.St.TokenByHash(h); err == nil && tok != nil {
		return true
	}
	return s.St.NodeOwnsToken(h)
}

// adminAuthorized — проверка админ-токена из Bearer или cookie.
func (s *Server) adminAuthorized(r *http.Request) bool {
	tok := ""
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		tok = strings.TrimPrefix(auth, "Bearer ")
	} else if c, err := r.Cookie("admin_token"); err == nil {
		tok = c.Value
	}
	if s.AdminToken == "" || tok == "" {
		return false
	}
	return subtleConstEq(tok, s.AdminToken)
}

// jsonRedirect вместо http.Redirect: всегда отвечает JSON {ok,message}.
// Обычные формы тоже получат JSON — браузер покажет его как текст, но все
// кнопки UI переведены на AJAX (/api/action), поэтому это ок.
func jsonRedirect(w http.ResponseWriter, _ string, msg string) {
	writeJSON(w, map[string]any{"ok": true, "message": msg})
}

// actionMessage — человекочитаемый текст результата действия проекта.
func actionMessage(action string) string {
	switch action {
	case "start":
		return "запуск: манифест отправлен на ноды ✓"
	case "stop":
		return "остановка: контейнеры/процессы гасятся ✓"
	case "restart":
		return "перезапуск ✓"
	case "logs":
		return "логи запрошены — появятся через пару секунд"
	}
	return "готово ✓"
}

// apiAction — единая AJAX-точка для всех действий админки (UI не перезагружается).
// Формы с onsubmit="return act(this,event)" постят сюда; ответ — JSON {ok,message|error}.
func (s *Server) apiAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	parseFormAny(r)
	// диспетчеризация на существующие хендлеры; нормализуем слэши
	// (curl|python-клиенты могут прислать //nodes/..., браузер — ровно один "/")
	do := r.PostFormValue("__do")
	for len(do) > 1 && do[0] == '/' && do[1] == '/' {
		do = do[1:]
	}
	r.URL.Path = do
	capt := &respCapture{ResponseWriter: w, code: 200}
	switch r.URL.Path {
	case "/projects/action":
		s.handleProjectAction(capt, r)
	case "/projects/delete":
		s.handleProjectDelete(capt, r)
	case "/projects/deploy":
		s.handleDeploy(capt, r)
	case "/nodes/recon":
		s.handleNodeRecon(capt, r)
	case "/nodes/update":
		s.handleNodeUpdate(capt, r)
	case "/nodes/update-all":
		s.handleNodeUpdateAll(capt, r)
	case "/nodes/delete":
		s.handleNodeDelete(capt, r)
	case "/nodes/delete-token":
		s.handleDeleteToken(capt, r)
	case "/nodes/limits":
		s.handleNodeLimits(capt, r)
	case "/nodes/exec":
		s.handleNodeExec(capt, r)
	case "/nodes/power":
		s.handleNodePower(capt, r)
	case "/nodes/screenshot":
		s.handleNodeScreenshot(capt, r)
	case "/nodes/create-token":
		s.handleCreateToken(capt, r)
	default:
		writeJSON(w, map[string]any{"ok": false, "error": "unknown action " + r.URL.Path})
		return
	}
	// Переводим http.Redirect хендлеров в JSON-ответ.
	if capt.redirect != "" {
		writeJSON(w, map[string]any{"ok": capt.code < 400, "message": capt.redirect})
		return
	}
	if capt.body != "" {
		// Хендлер уже записал готовый JSON (jsonRedirect/writeJSON) — отдаём как есть.
		var probe map[string]any
		if err := json.Unmarshal([]byte(capt.body), &probe); err == nil {
			if _, has := probe["ok"]; has {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(capt.body))
				return
			}
		}
		// иначе это http.Error (plain text) — заворачиваем в JSON-ошибку
		code := capt.code
		if code == 0 {
			code = 400
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		writeJSON(w, map[string]any{"ok": false, "error": capt.body})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "message": "готово ✓"})
}

// respCapture перехватывает Redirect/Error ответа вложенного хендлера.
type respCapture struct {
	http.ResponseWriter
	code     int
	redirect string
	body     string
	wrote    bool
}

func (c *respCapture) WriteHeader(code int) {
	if c.wrote {
		return
	}
	c.wrote, c.code = true, code
}

func (c *respCapture) Write(b []byte) (int, error) {
	if !c.wrote {
		c.wrote, c.code = true, 200
	}
	c.body += string(b)
	return len(b), nil
}

func (c *respCapture) Header() http.Header { return c.ResponseWriter.Header() }

// apiScreenshots — JSON-список свежих скриншотов ноды (для живой подгрузки в UI).
func (s *Server) apiScreenshots(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("node_id"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 8
	}
	files, err := s.St.ListScreenshotFiles(id, limit)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "files": files})
}

// requireAdmin проверяет Bearer-токен или cookie admin_token (для форм).
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.adminAuthorized(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// subtleConstEq — сравнение токенов без раннего выхода по длине и без раннего
// выхода по первому несовпавшему байту (защита от тайминг-атак).
func subtleConstEq(a, b string) bool {
	if len(a) != len(b) {
		// длина всё равно утекает, но не возвращаемся сразу — сравниваем
		// побайтово по максимуму, чтобы время ответа не зависело от позиции
		// первого различия.
		var v byte
		n := len(a)
		if len(b) > n {
			n = len(b)
		}
		for i := 0; i < n; i++ {
			var x, y byte
			if i < len(a) {
				x = a[i]
			}
			if i < len(b) {
				y = b[i]
			}
			v |= x ^ y
		}
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// ---------- JSON API ----------

func (s *Server) apiNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.St.ListNodes()
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, nodes)
}

func (s *Server) apiProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.St.ListProjects()
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, projects)
}

func (s *Server) apiEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := s.St.RecentEvents(limit)
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, events)
}

func (s *Server) apiLogs(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/logs/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	logs, err := s.St.GetLogs(id, 500)
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, logs)
}

func (s *Server) apiVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"version": model.VersionTag()})
}

// apiHealth — публичный эндпоинт «живо ли сервер». Без секретов.
// Нужен внешнему мониторингу и проверке «платформа поднялась после деплоя».
func (s *Server) apiHealth(w http.ResponseWriter, r *http.Request) {
	nodes, _ := s.St.ListNodes()
	online := 0
	for _, n := range nodes {
		if n.Status == "online" && s.Hub.IsOnline(n.ID) {
			online++
		}
	}
	projects, _ := s.St.ListProjects()
	running := countStatus(projects, model.StatusRunning)
	dbSize, walSize := s.St.DBSize()
	writeJSON(w, map[string]any{
		"ok":      true,
		"version": model.VersionTag(),
		"nodes":   len(nodes),
		"online":  online,
		"projects": len(projects),
		"running": running,
		"db_bytes": dbSize,
		"wal_bytes": walSize,
	})
}

// apiSummary — сводка для дашборда (ноды, проекты, инстансы, здоровье).
func (s *Server) apiSummary(w http.ResponseWriter, r *http.Request) {
	nodes, _ := s.St.ListNodes()
	projects, _ := s.St.ListProjects()
	insts, _ := s.St.AllInstances()
	online := 0
	for _, n := range nodes {
		if n.Status == "online" && s.Hub.IsOnline(n.ID) {
			online++
		}
	}
	byStatus := map[string]int{}
	for _, p := range projects {
		byStatus[p.Status]++
	}
	instByStatus := map[string]int{}
	for _, in := range insts {
		instByStatus[in.Status]++
	}
	dbSize, walSize := s.St.DBSize()
	writeJSON(w, map[string]any{
		"version":        model.VersionTag(),
		"nodes_total":    len(nodes),
		"nodes_online":   online,
		"projects_total": len(projects),
		"project_status": byStatus,
		"instances":      instByStatus,
		"db_bytes":       dbSize,
		"wal_bytes":      walSize,
	})
}

// hostOnly — голый хост из публичного адреса (без схемы и слэшей).
// Нужен, потому что PUBLIC_URL хранится как "https://core.swag.best",
// а команды установки собираются с собственной схемой.
func hostOnly(u string) string {
	u = strings.TrimSpace(u)
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	return strings.TrimSuffix(u, "/")
}

// installCommand — готовая команда подключения для Windows (PowerShell).
// Именно её копирует пользователь на «чистую» Windows и вставляет в PowerShell:
// скрипт сам скачивается, сам повышает права до администратора, ставит агента
// как службу Windows, запускает и проверяет подключение.
func (s *Server) installCommandWin(token string) string {
	return `irm https://` + hostOnly(s.PublicBase) + `/download/install.ps1 | iex`
}

// installCommandWinToken — вариант с токеном, зашитым в команду (кнопка «скопировать»).
func (s *Server) installCommandWinToken(token, name string) string {
	if name == "" {
		name = "windows-node"
	}
	return `irm https://` + hostOnly(s.PublicBase) +
		`/download/install.ps1 -OutFile $env:TEMP\swagcore-install.ps1; ` +
		`& $env:TEMP\swagcore-install.ps1 -Token "` + token + `" -Name "` + name + `"`
}

// installCommandLinux — одна команда для Linux (root).
func (s *Server) installCommandLinux(token string) string {
	return `curl -fsSL https://` + hostOnly(s.PublicBase) + `/download/install.sh | sudo bash -s -- ` + token
}

// apiBootstrap — создаёт токен ноды и возвращает готовые команды установки.
// Кнопка «Добавить ноду» в панели вызывает именно это.
func (s *Server) apiBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	parseFormAny(r)
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		name = "node-" + time.Now().Format("0102-1504")
	}
	if !safeTokenName(name) {
		http.Error(w, "недопустимое имя ноды", 400)
		return
	}
	maxMem, _ := strconv.Atoi(r.PostFormValue("max_mem"))
	maxCPUs, _ := strconv.ParseFloat(r.PostFormValue("max_cpus"), 64)
	maxDisk, _ := strconv.Atoi(r.PostFormValue("max_disk"))
	if maxMem < 0 || maxDisk < 0 || maxCPUs < 0 {
		maxMem, maxCPUs, maxDisk = 0, 0, 0
	}
	tok := genToken()
	if err := s.St.CreateToken(name, tok, hub.HashToken(tok), maxMem, maxCPUs, maxDisk); err != nil {
		httpError(w, err)
		return
	}
	s.St.AddEvent("info", "api", fmt.Sprintf("создан токен ноды %q (лимиты: %dMB/%.1fcpu/%dGB)", name, maxMem, maxCPUs, maxDisk))
	writeJSON(w, map[string]any{
		"ok":            true,
		"name":          name,
		"token":         tok,
		"cmd_windows":   s.installCommandWinToken(tok, name),
		"cmd_windows_m": s.installCommandWin(""),
		"cmd_linux":     s.installCommandLinux(tok),
		"server":        s.PublicBase,
		"version":       model.VersionTag(),
	})
}

// safeTokenName — имя ноды: буквы/цифры/точка/дефис/подчёркивание, до 40 символов.
// Этими символами имя потом попадает в systemd-юнит и в путь каталога.
func safeTokenName(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// handleFavicon отдаёт инлайн SVG-иконку.
func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(faviconSVG))
}

// handleArtifactUpload принимает tar.gz проектных файлов (multipart, поле file)
// и кладёт в {artifactsDir}/{project}/. Раздаются без авторизации через /artifacts/.
func (s *Server) handleArtifactUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		http.Error(w, "POST only", 405)
		return
	}
	project := strings.TrimPrefix(r.URL.Path, "/api/artifacts/")
	project = strings.Trim(project, "/")
	if project == "" || strings.Contains(project, "/") || strings.Contains(project, "..") {
		http.Error(w, "bad project", 400)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "no file field 'file'", 400)
		return
	}
	defer file.Close()
	name := filepath.Base(hdr.Filename)
	if name == "" || name == "." || strings.Contains(name, "..") {
		http.Error(w, "bad filename", 400)
		return
	}
	dir := filepath.Join(s.artifactsDir, project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		httpError(w, err)
		return
	}
	out, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		httpError(w, err)
		return
	}
	defer out.Close()
	if _, err := io.Copy(out, file); err != nil {
		httpError(w, err)
		return
	}
	sum, err := fileSHA256Local(out.Name())
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, map[string]string{"ok": "saved", "url": "/artifacts/" + project + "/" + name, "sha256": sum})
}

// fileSHA256Local считает sha256 локального файла.
func fileSHA256Local(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// handleAgentUpload принимает файлы от агентов (скриншоты).
// Авторизация: Bearer admin-токен ИЛИ X-Node-Token (валидный токен ноды).
func (s *Server) handleAgentUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	if !s.uploadAuthorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "bad multipart", 400)
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "no file", 400)
		return
	}
	defer file.Close()
	name := filepath.Base(hdr.Filename)
	if name == "" || name == "." || strings.Contains(name, "..") {
		http.Error(w, "bad filename", 400)
		return
	}
	dir := screenshotDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		httpError(w, err)
		return
	}
	out, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		httpError(w, err)
		return
	}
	defer out.Close()
	if _, err := io.Copy(out, file); err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, map[string]string{"ok": "saved", "file": name})
}

// uploadAuthorized проверяет админ-токен или токен ноды.
func (s *Server) uploadAuthorized(r *http.Request) bool {
	if s.nodeAuthorized(r) {
		return true
	}
	tok := r.Header.Get("Authorization")
	if strings.HasPrefix(tok, "Bearer ") && subtleConstEq(strings.TrimPrefix(tok, "Bearer "), s.AdminToken) {
		return true
	}
	if c, err := r.Cookie("admin_token"); err == nil && subtleConstEq(c.Value, s.AdminToken) {
		return true
	}
	return false
}

// screenshotDir — каталог скриншотов (linux прод / локально tmp).
func screenshotDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), "swagcore-shots")
	}
	return "/opt/swagcore/screenshots"
}

// handleCreateToken создаёт токен ноды (с лимитами) и показывает страницу добавления.
func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		name = "node"
	}
	maxMem, _ := strconv.Atoi(r.PostFormValue("max_mem"))
	maxCPUs, _ := strconv.ParseFloat(r.PostFormValue("max_cpus"), 64)
	maxDisk, _ := strconv.Atoi(r.PostFormValue("max_disk"))
	if maxMem < 0 || maxDisk < 0 || maxCPUs < 0 {
		maxMem, maxCPUs, maxDisk = 0, 0, 0
	}
	tok := genToken()
	if err := s.St.CreateToken(name, tok, hub.HashToken(tok), maxMem, maxCPUs, maxDisk); err != nil {
		httpError(w, err)
		return
	}
	s.St.AddEvent("info", "api", fmt.Sprintf("создан токен ноды «%s» (лимиты: %d МБ / %.1f CPU / %d ГБ)", name, maxMem, maxCPUs, maxDisk))
	jsonRedirect(w, "/add-node", "токен создан ✓ — скопируйте команду установки ниже")
}

// handleDeleteToken удаляет неиспользованный токен ноды.
func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	parseFormAny(r)
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	force := r.PostFormValue("force") == "1"

	// v0.6.0: не даём молча отрезать работающую ноду от загрузки скриншотов.
	// Раньше удаление токена оставляло WS-соединение живым, а HTTP-часть
	// (скриншоты, артефакты) начинала отдавать 401 — выглядело как «сломались
	// скриншоты» без всякого объяснения.
	if !force {
		if toks, err := s.St.ListTokens(); err == nil {
			for _, t := range toks {
				if t.ID != id {
					continue
				}
if host, used := s.St.TokenInUseByNode(hub.HashToken(t.Token)); used {
					msg := "токен сейчас использует нода " + host + " (она на связи). "
					msg += "Если удалить токен, нода потеряет возможность загружать скриншоты "
					msg += "и качать артефакты проектов. Сначала выключите ноду, либо подтвердите удаление."
					http.Error(w, msg, 409)
					return
				}
			}
		}
	}

	if err := s.St.DeleteToken(id); err != nil {
		httpError(w, err)
		return
	}
	s.St.AddEvent("info", "api", "удалён токен ноды #"+strconv.FormatInt(id, 10))
	jsonRedirect(w, "/add-node", "токен удалён ✓")
}

// handleNodeLimits сохраняет лимиты и режим планирования ноды.
func (s *Server) handleNodeLimits(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	maxMem, _ := strconv.Atoi(r.PostFormValue("max_mem"))
	maxCPUs, _ := strconv.ParseFloat(r.PostFormValue("max_cpus"), 64)
	maxDisk, _ := strconv.Atoi(r.PostFormValue("max_disk"))
	if maxMem < 0 || maxDisk < 0 || maxCPUs < 0 {
		maxMem, maxCPUs, maxDisk = 0, 0, 0
	}
	enabled := r.PostFormValue("enabled") == "1"
	_ = s.St.SetNodeLimits(id, maxMem, maxCPUs, maxDisk)
	_ = s.St.SetNodeEnabled(id, enabled)
	s.St.AddEvent("info", "api", fmt.Sprintf("нода %d: лимиты %d МБ / %.1f CPU / %d ГБ, в планировщике=%v", id, maxMem, maxCPUs, maxDisk, enabled))
	jsonRedirect(w, "/nodes/"+strconv.FormatInt(id, 10), "лимиты сохранены ✓")
}

// genToken — случайный 48-символьный hex-токен.
func genToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	hex.EncodeToString(b)
	return hex.EncodeToString(b)
}

// handleNodeEnable вкл/выкл ноды для планировщика.
func (s *Server) handleNodeEnable(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	enabled := r.PostFormValue("enabled") == "1"
	_ = s.St.SetNodeEnabled(id, enabled)
	s.St.AddEvent("info", "api", "нода "+strconv.FormatInt(id, 10)+": в планировщике="+r.PostFormValue("enabled"))
	jsonRedirect(w, "/nodes/"+strconv.FormatInt(id, 10), "нода "+map[bool]string{true: "включена", false: "выключена"}[enabled]+" ✓")
}

// handleNodeMaxMem задаёт лимит RAM ноды.
func (s *Server) handleNodeMaxMem(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	mb, _ := strconv.Atoi(r.PostFormValue("mb"))
	_ = s.St.SetNodeMaxMem(id, mb)
	s.St.AddEvent("info", "api", "нода "+strconv.FormatInt(id, 10)+": лимит памяти "+strconv.Itoa(mb)+" МБ")
	jsonRedirect(w, "/nodes/"+strconv.FormatInt(id, 10), "лимит RAM сохранён ✓")
}

// handleNodeExec выполняет команду на ноде.
func (s *Server) handleNodeExec(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	command := strings.TrimSpace(r.PostFormValue("command"))
	timeout, _ := strconv.Atoi(r.PostFormValue("timeout"))
	if command == "" {
		http.Error(w, "empty command", 400)
		return
	}
	reqID, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	reqIDStr := "e" + strconv.FormatInt(reqID.Int64(), 36)
	if err := s.Hub.ExecCommand(id, reqIDStr, command, timeout); err != nil {
		s.St.AddEvent("warn", "api", "команда на ноде "+strconv.FormatInt(id, 10)+": "+err.Error())
		http.Error(w, err.Error(), 502)
		return
	}
	jsonRedirect(w, "/nodes/"+strconv.FormatInt(id, 10), "команда отправлена — результат появится в истории через пару секунд")
}

// handleNodePower отправляет reboot/shutdown.
func (s *Server) handleNodePower(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	action := r.PostFormValue("action")
	if action != "reboot" && action != "shutdown" {
		http.Error(w, "bad action", 400)
		return
	}
	if err := s.Hub.PowerAction(id, action); err != nil {
		s.St.AddEvent("warn", "api", action+" node "+strconv.FormatInt(id, 10)+": "+err.Error())
		http.Error(w, err.Error(), 502)
		return
	}
	s.St.AddEvent("info", "api", action+" sent to node "+strconv.FormatInt(id, 10))
	msg := "команда «выключить» отправлена"
	if action == "reboot" {
		msg = "команда «перезагрузить» отправлена"
	}
	jsonRedirect(w, "/nodes/"+strconv.FormatInt(id, 10), msg)
}

// handleNodeScreenshot просит ноду снять экран.
func (s *Server) handleNodeScreenshot(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if !s.Hub.IsOnline(id) {
		http.Error(w, "нода offline — снимок сделать нельзя", 502)
		return
	}
	if err := s.Hub.RequestScreenshot(id, ""); err != nil {
		s.St.AddEvent("warn", "api", "screenshot node "+strconv.FormatInt(id, 10)+": "+err.Error())
		http.Error(w, err.Error(), 502)
		return
	}
	jsonRedirect(w, "/nodes/"+strconv.FormatInt(id, 10), "команда отправлена — снимок появится в галерее через несколько секунд 📷")
}

// ---------- Действия ----------

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	yamlSrc := strings.TrimSpace(r.PostFormValue("yaml"))
	if yamlSrc == "" {
		http.Error(w, "empty yaml", 400)
		return
	}
	var mf model.Manifest
	if err := yaml.Unmarshal([]byte(yamlSrc), &mf); err != nil {
		http.Error(w, "bad yaml: "+err.Error(), 400)
		return
	}
	if mf.Name == "" {
		http.Error(w, "manifest needs name", 400)
		return
	}
	if mf.Image == "" && mf.Mode != "process" {
		http.Error(w, "manifest needs image (or mode: process)", 400)
		return
	}
	if mf.Mode == "process" && strings.TrimSpace(mf.Command) == "" {
		http.Error(w, "process mode needs command", 400)
		return
	}

	p, err := s.St.ProjectByName(mf.Name)
	if err != nil {
		httpError(w, err)
		return
	}
	if p == nil {
		p, err = s.St.CreateProject(mf.Name, mf)
		if err != nil {
			httpError(w, err)
			return
		}
	} else {
		_ = s.St.UpdateManifest(p.ID, mf)
	}

	if err := s.deployProject(p.ID); err != nil {
		_ = s.St.SetProjectStatus(p.ID, model.StatusFailed, "", err.Error())
		s.St.AddEvent("error", "api", "деплой "+p.Name+": "+err.Error())
		http.Error(w, "deploy failed: "+err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/projects", http.StatusSeeOther)
}

// deployProject планирует и отправляет задачу деплоя.
func (s *Server) deployProject(id int64) error {
	p, err := s.St.ProjectByID(id)
	if err != nil || p == nil {
		return fmt.Errorf("project not found")
	}
	// проект уже привязан к живой ноде — деплоим туда
	if p.NodeID != 0 && s.Hub.IsOnline(p.NodeID) {
		return s.sendDeploy(p)
	}
	node, err := sched.PickNode(s.St, p.Manifest, s.Hub.IsOnline)
	if err != nil {
		return err
	}
	_ = s.St.SetProjectNode(p.ID, node.ID)
	p.NodeID = node.ID // важно: p — копия, обновляем для sendDeploy
	s.St.AddEvent("info", "sched", "проект "+p.Name+" размещён на ноде "+node.Hostname)
	return s.sendDeploy(p)
}

func (s *Server) sendDeploy(p *model.Project) error {
	mf := p.Manifest
	task := model.DeployTask{
		ProjectID:   p.ID,
		Name:        p.Name,
		Mode:        mf.Mode,
		Image:       mf.Image,
		Command:     mf.Command,
		Ports:       mf.Ports,
		Env:         mf.Env,
		Volumes:     mf.Volumes,
		Memory:      mf.Resources.Memory,
		CPUs:        mf.Resources.CPUs,
		ArtifactURL: mf.Artifact,
		ArtifactSHA: mf.ArtifactSHA,
		MountPath:   mf.MountPath,
	}
	_ = s.St.SetProjectStatus(p.ID, model.StatusStarting, p.Container, "")
	return s.Hub.Deploy(p.NodeID, task)
}

// AIDeployYAML — создание/обновление и деплой проекта из YAML (тулза ИИ-агента).
func (s *Server) AIDeployYAML(yamlSrc string) (int64, error) {
	yamlSrc = strings.TrimSpace(yamlSrc)
	if yamlSrc == "" {
		return 0, fmt.Errorf("empty yaml")
	}
	var mf model.Manifest
	if err := yaml.Unmarshal([]byte(yamlSrc), &mf); err != nil {
		return 0, fmt.Errorf("bad yaml: %w", err)
	}
	if mf.Name == "" {
		return 0, fmt.Errorf("manifest needs name")
	}
	if mf.Image == "" && mf.Mode != "process" {
		return 0, fmt.Errorf("manifest needs image (or mode: process)")
	}
	p, err := s.St.ProjectByName(mf.Name)
	if err != nil {
		return 0, err
	}
	if p == nil {
		p, err = s.St.CreateProject(mf.Name, mf)
		if err != nil {
			return 0, err
		}
		s.St.AddEvent("info", "ai", "создан проект "+mf.Name+" (ИИ-агент)")
	} else {
		if err := s.St.UpdateManifest(p.ID, mf); err != nil {
			return 0, err
		}
	}
	_ = s.St.SetProjectStatus(p.ID, model.StatusDesiredRunning, "", "")
	if err := s.deployProject(p.ID); err != nil {
		return p.ID, err
	}
	return p.ID, nil
}

func (s *Server) handleProjectAction(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	action := r.PostFormValue("action")
	p, err := s.St.ProjectByID(id)
	if err != nil || p == nil {
		http.Error(w, "project not found", 404)
		return
	}
	instances, _ := s.St.InstancesOfProject(p.ID)
	switch action {
	case "start", "restart":
		_ = s.St.SetProjectStatus(p.ID, model.StatusDesiredRunning, p.Container, "")
		if action == "restart" {
			s.stopEverywhere(p, instances, false)
		}
		if err := s.deployProject(p.ID); err != nil {
			_ = s.St.SetProjectStatus(p.ID, model.StatusFailed, p.Container, err.Error())
			s.St.AddEvent("error", "api", action+" "+p.Name+": "+err.Error())
			http.Error(w, err.Error(), 502)
			return
		}
	case "stop":
		_ = s.St.SetProjectStatus(p.ID, model.StatusDesiredStopped, "", "")
		s.stopEverywhere(p, instances, false)
	case "logs":
		// логи берём с любой ОНЛАЙН-ноды, где есть экземпляр (v0.6.0:
		// раньше смотрели только на легаси-колонку p.NodeID, из-за чего у
		// многонодных проектов логи были недоступны)
		for _, in := range instances {
			if s.Hub.IsOnline(in.NodeID) {
				_ = s.Hub.RequestLogs(in.NodeID, model.LogsRequest{ProjectID: p.ID, Name: p.Name, Tail: "300"})
				goto ok
			}
		}
		if s.Hub.IsOnline(p.NodeID) {
			_ = s.Hub.RequestLogs(p.NodeID, model.LogsRequest{ProjectID: p.ID, Name: p.Name, Tail: "300"})
			goto ok
		}
		http.Error(w, "нет онлайн-ноды с этим проектом — логи недоступны", 502)
		return
	}
ok:
	jsonRedirect(w, "/projects/"+strconv.FormatInt(p.ID, 10), actionMessage(action))
}

// stopEverywhere отправляет команду остановки на ВСЕ ноды, где есть экземпляр
// проекта. В v0.5.x стоп уходил только на p.NodeID — легаси-колонку одной ноды,
// поэтому на остальных нодах контейнеры продолжали работать.
func (s *Server) stopEverywhere(p *model.Project, instances []model.ProjectInstance, remove bool) {
	task := model.StopTask{ProjectID: p.ID, Name: p.Name, Remove: remove}
	seen := map[int64]bool{}
	for _, in := range instances {
		if in.NodeID == 0 || seen[in.NodeID] {
			continue
		}
		seen[in.NodeID] = true
		if err := s.Hub.Stop(in.NodeID, task); err != nil {
			_ = s.St.SetInstanceStatus(p.ID, in.NodeID, model.InstStopped, in.Container,
				"нода offline: "+err.Error())
		}
	}
	if p.NodeID != 0 && !seen[p.NodeID] && s.Hub.IsOnline(p.NodeID) {
		_ = s.Hub.Stop(p.NodeID, task)
	}
}

func (s *Server) handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	p, err := s.St.ProjectByID(id)
	if err != nil || p == nil {
		http.Error(w, "project not found", 404)
		return
	}
	// v0.6.0: останавливаем на ВСЕХ нодах (было — только на легаси p.NodeID),
	// затем удаляем запись каскадом (было — только строку из projects,
	// экземпляры оставались сиротами).
	instances, _ := s.St.InstancesOfProject(p.ID)
	s.stopEverywhere(p, instances, true)
	if err := s.St.DeleteProject(p.ID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if err := s.St.DeleteProjectArtifacts(id, s.artifactsDir); err != nil {
		s.St.AddEvent("warn", "api", "не удалось удалить артефакты "+p.Name+": "+err.Error())
	}
	s.St.AddEvent("info", "api", "проект удалён: "+p.Name)
	jsonRedirect(w, "/projects", "проект "+p.Name+" удалён ✓")
}

func (s *Server) handleNodeDelete(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	force := r.PostFormValue("force") == "1"
	n, _ := s.St.NodeByID(id)
	if n == nil {
		http.Error(w, "нода не найдена", 404)
		return
	}
	if n != nil && s.Hub.IsOnline(id) && !force {
		http.Error(w, "нода онлайн — подтвердите силу (кнопка уже с force) или выключите агента сначала", 409)
		return
	}
	if err := s.St.DeleteNodeCascade(id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.St.AddEvent("info", "api", "нода удалена #"+strconv.FormatInt(id, 10)+" ("+n.Hostname+")")
	jsonRedirect(w, "/nodes", "нода "+n.Hostname+" удалена ✓ (агент на машине продолжает работать — остановите его отдельно)")
}

func (s *Server) handleNodeRecon(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if err := s.Hub.RequestReconcile(id); err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	jsonRedirect(w, "/nodes", "опрос ноды запущен ✓")
}

// handleNodeUpdate отправляет ноде команду самообновления.
func (s *Server) handleNodeUpdate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, _ := strconv.ParseInt(r.PostFormValue("id"), 10, 64)
	if err := s.Hub.SendTo(id, model.Envelope{Type: model.MsgUpdate}); err != nil {
		s.St.AddEvent("warn", "api", "обновление ноды "+strconv.FormatInt(id, 10)+": "+err.Error())
		http.Error(w, err.Error(), 502)
		return
	}
	s.St.AddEvent("info", "api", "команда обновления отправлена ноде "+strconv.FormatInt(id, 10))
	jsonRedirect(w, "/nodes", "команда обновления отправлена — агент перезапустится на новой версии")
}

// handleNodeUpdateAll отправляет команду самообновления всем онлайн-нодам.
func (s *Server) handleNodeUpdateAll(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.St.ListNodes()
	if err != nil {
		httpError(w, err)
		return
	}
	sent, skipped := 0, 0
	for _, n := range nodes {
		if n.Status != "online" || !s.Hub.IsOnline(n.ID) {
			skipped++
			continue
		}
		if err := s.Hub.SendTo(n.ID, model.Envelope{Type: model.MsgUpdate}); err == nil {
			sent++
		} else {
			skipped++
		}
	}
	s.St.AddEvent("info", "api", fmt.Sprintf("обновление всех нод: отправлено %d, пропущено %d (не в сети)", sent, skipped))
	jsonRedirect(w, "/nodes", fmt.Sprintf("обновление отправлено %d нодам (пропущено %d offline)", sent, skipped))
}

// ---------- Аутентификация (cookie) ----------

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		ip := clientAddr(r)
		if _, blocked := s.loginBlocked(ip); blocked {
			http.Redirect(w, r, "/login?err=rate", http.StatusSeeOther)
			return
		}
		tok := r.PostFormValue("admin_token")
		if s.AdminToken != "" && subtleConstEq(tok, s.AdminToken) {
			s.loginOK(ip)
			http.SetCookie(w, &http.Cookie{
				Name: "admin_token", Value: tok, Path: "/",
				HttpOnly: true, SameSite: http.SameSiteLaxMode,
				// Secure ставим только если реально пришли по HTTPS — иначе
				// cookie не сохранится на http://127.0.0.1:8181 (dev-режим).
				Secure: isHTTPS(r),
				MaxAge: 30 * 24 * 3600,
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		s.loginFail(ip)
		http.Redirect(w, r, "/login?err=1", http.StatusSeeOther)
		return
	}
	errCode := r.URL.Query().Get("err")
	s.render(w, "login", map[string]any{
		"Error":       errCode != "",
		"RateLimited": errCode == "rate",
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "admin_token", Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---- Защита /login от перебора (v0.6.0) ----
// Раньше токен админа можно было подбирать бесконечно: ни лимита попыток,
// ни задержки. Теперь 8 ошибок с одного IP → пауза 60 с (экспоненциально до 15 мин).

const loginMaxFails = 8

func (s *Server) loginBlocked(ip string) (time.Duration, bool) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	f, ok := s.loginFails[ip]
	if !ok || time.Now().After(f.until) {
		delete(s.loginFails, ip)
		return 0, false
	}
	if f.count < loginMaxFails {
		return 0, false
	}
	return time.Until(f.until), true
}

func (s *Server) loginFail(ip string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	f := s.loginFails[ip]
	f.count++
	if f.count >= loginMaxFails {
		shift := f.count - loginMaxFails
		if shift > 5 {
			shift = 5
		}
		f.until = time.Now().Add(time.Duration(60*(1<<shift)) * time.Second)
	}
	s.loginFails[ip] = f
}

func (s *Server) loginOK(ip string) {
	s.loginMu.Lock()
	delete(s.loginFails, ip)
	s.loginMu.Unlock()
}

func clientAddr(r *http.Request) string {
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// isHTTPS — пришёл ли запрос по HTTPS (напрямую или через nginx).
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if r.Header.Get("X-Forwarded-Proto") == "https" {
		return true
	}
	return strings.HasPrefix(r.Header.Get("X-Forwarded-Proto"), "https")
}

// ---------- Страницы ----------

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/login" {
		s.handleLogin(w, r)
		return
	}
	if r.URL.Path == "/logout" {
		s.handleLogout(w, r)
		return
	}
	if !s.isLoggedIn(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	switch r.URL.Path {
	case "/":
		s.pageDashboard(w, r)
	case "/nodes":
		s.pageNodes(w, r)
	case "/projects":
		s.pageProjects(w, r)
	case "/events":
		s.pageEvents(w, r)
	case "/add-node":
		s.handleAddNodePage(w, r)
	case "/ai":
	s.render(w, "ai", map[string]any{"Version": model.VersionTag()})
	default:
		http.NotFound(w, r)
	}
}

// handleNodePage — страница управления нодой (/nodes/{id} или /nodes/?id=N).
func (s *Server) handleNodePage(w http.ResponseWriter, r *http.Request) {
	if !s.isLoggedIn(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	idStr := strings.Trim(strings.TrimPrefix(r.URL.Path, "/nodes"), "/")
	if idStr == "" {
		idStr = r.URL.Query().Get("id")
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	n, err := s.St.NodeByID(id)
	if err != nil || n == nil {
		http.NotFound(w, r)
		return
	}
	results, _ := s.St.NodeResults(id, 25)
	shots, _ := s.St.ListScreenshotFiles(id, 8)
	// проекты этой ноды (для панели «Проекты на ноде»)
	insts, _ := s.St.AllInstances()
	projects, _ := s.St.ListProjects()
	pmap := map[int64]model.Project{}
	for _, p := range projects {
		pmap[p.ID] = p
	}
	type nodeProjRow struct {
		Project model.Project
		Inst    model.ProjectInstance
	}
	var nodeProjs []nodeProjRow
	for _, in := range insts {
		if in.NodeID != id {
			continue
		}
		p, ok := pmap[in.ProjectID]
		if !ok {
			p = model.Project{Name: "?"}
		}
		nodeProjs = append(nodeProjs, nodeProjRow{Project: p, Inst: in})
	}
	// свежие результаты в хронологическом порядке
	for i, j := 0, len(results)-1; i < j; i, j = i+1, j-1 {
		results[i], results[j] = results[j], results[i]
	}
	s.render(w, "node", map[string]any{
		"N": n, "Results": results, "Shots": shots, "NodeProjs": nodeProjs,
		"Version": model.VersionTag(),
	})
}

// handleAddNodePage — страница «Добавить ноду»: одна кнопка → одна команда.
func (s *Server) handleAddNodePage(w http.ResponseWriter, r *http.Request) {
	if !s.isLoggedIn(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	tokens, _ := s.St.ListTokens()
	newTok := r.URL.Query().Get("token")
	newName := r.URL.Query().Get("name")
	data := map[string]any{
		"Tokens":  tokens,
		"Version": model.VersionTag(),
		"BaseURL": s.PublicBase,
		"CmdWin":  s.installCommandWinToken(newTok, newName),
		"CmdWinM": s.installCommandWin(""),
		"CmdLinux": s.installCommandLinux(newTok),
	}
	if newTok != "" {
		data["NewToken"] = newTok
		data["NewName"] = newName
		data["CmdWin"] = s.installCommandWinToken(newTok, newName)
		data["CmdLinux"] = s.installCommandLinux(newTok)
	}
	s.render(w, "addnode", data)
}

func (s *Server) handleProjectPage(w http.ResponseWriter, r *http.Request) {
	if !s.isLoggedIn(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if r.URL.Path != "/projects/" && !strings.HasPrefix(r.URL.Path, "/projects/") {
		http.NotFound(w, r)
		return
	}
	// /projects/, /projects/2 (path-style) и /projects/?id=2 — все работают
	idStr := strings.Trim(strings.TrimPrefix(r.URL.Path, "/projects"), "/")
	if idStr == "" {
		idStr = r.URL.Query().Get("id")
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	p, err := s.St.ProjectByID(id)
	if err != nil || p == nil {
		http.NotFound(w, r)
		return
	}
	logs, _ := s.St.GetLogs(p.ID, 300)
	instances, _ := s.St.InstancesOfProject(p.ID)
	nodes, _ := s.St.ListNodes()
	yamlBytes, _ := yaml.Marshal(&p.Manifest)
	s.render(w, "project", map[string]any{
		"P": p, "Logs": logs, "Instances": instances, "Nodes": nodes,
		"ManifestYAML": string(yamlBytes),
	})
}

func (s *Server) isLoggedIn(r *http.Request) bool {
	if c, err := r.Cookie("admin_token"); err == nil && subtleConstEq(c.Value, s.AdminToken) {
		return true
	}
	return false
}

// pageDashboard — главная страница: здоровье платформы + что требует внимания.
func (s *Server) pageDashboard(w http.ResponseWriter, r *http.Request) {
	nodes, _ := s.St.ListNodes()
	projects, _ := s.St.ListProjects()
	// v0.6.0: журнал без reconcile-мусора (он больше не пишется, но старые
	// записи могли остаться) — плюс берём больше, чтобы хватало на страницу.
	events, _ := s.St.RecentEvents(40, "reconcile:")
	online, dockerNodes := 0, 0
	totalCPU := 0.0
	totalMemUsed, totalMem := uint64(0), uint64(0)
	var diskUsed, diskTotal float64
	for _, n := range nodes {
		if n.Status == "online" && s.Hub.IsOnline(n.ID) {
			online++
		}
		if n.HasDocker && n.Enabled {
			dockerNodes++
		}
		if n.Metrics != nil {
			totalCPU += n.Metrics.CPUPct
			totalMemUsed += n.Metrics.MemUsedMB
			totalMem += n.Metrics.MemTotalMB
			diskUsed += n.Metrics.DiskUsedGB
			diskTotal += n.Metrics.DiskTotalGB
		}
	}
	insts, _ := s.St.AllInstances()
	nodeProj, nodeProjRun := nodeProjectCounts(insts)

	// «Проблемы» — самое полезное на главной: что чинить прямо сейчас.
	type issue struct {
		Level string
		Text  string
		Link  string
	}
	var issues []issue
	if len(nodes) == 0 {
		issues = append(issues, issue{"warn", "Нет ни одной ноды — подключите первую одной командой", "/add-node"})
	}
	for _, n := range nodes {
		if n.Status == "online" {
			continue
		}
		issues = append(issues, issue{"err", "Нода " + n.Hostname + " offline (была " + n.LastSeen.Format("02.01 15:04") + ")", "/nodes/" + strconv.FormatInt(n.ID, 10)})
		if n.InstanceID == "" {
			issues = append(issues, issue{"warn", "Нода " + n.Hostname + " на старом агенте (< v0.6.0) — обновите её", "/nodes/" + strconv.FormatInt(n.ID, 10)})
		}
	}
	for _, p := range projects {
		if p.Status == model.StatusFailed {
			issues = append(issues, issue{"err", "Проект " + p.Name + " упал: " + p.Error, "/projects/" + strconv.FormatInt(p.ID, 10)})
		}
		if p.Status == model.StatusRunning && p.Error != "" {
			issues = append(issues, issue{"warn", "Проект " + p.Name + " работает частично — " + p.Error, "/projects/" + strconv.FormatInt(p.ID, 10)})
		}
	}
	tokens, _ := s.St.ListTokens()
	unused := 0
	for _, t := range tokens {
		if !t.Used {
			unused++
		}
	}
	if unused > 0 {
		issues = append(issues, issue{"info", "Неиспользованных токенов нод: " + strconv.Itoa(unused) + " — можно удалить", "/add-node"})
	}
	dbSize, walSize := s.St.DBSize()

	s.render(w, "dashboard", map[string]any{
		"Online": online, "TotalNodes": len(nodes), "DockerNodes": dockerNodes,
		"TotalProjects": len(projects),
		"Nodes":         nodes, "Projects": projects, "Events": events,
		"Running":  countStatus(projects, model.StatusRunning),
		"Failed":   countStatus(projects, model.StatusFailed),
		"NodeProj": nodeProj, "NodeProjRun": nodeProjRun,
		"Issues":    issues,
		"AvgCPU":    totalCPU,
		"MemUsed":   totalMemUsed, "MemTotal": totalMem,
		"DiskUsed":  diskUsed, "DiskTotal": diskTotal,
		"DBSize":    dbSize, "WALSize": walSize,
		"BaseURL":   s.PublicBase,
		"Version":   model.VersionTag(),
	})
}

func (s *Server) pageNodes(w http.ResponseWriter, r *http.Request) {
	nodes, _ := s.St.ListNodes()
	insts, _ := s.St.AllInstances()
	nodeProj, nodeProjRun := nodeProjectCounts(insts)
	// квоты: занято/лимит по каждой ноде (чтобы было видно, что лимит работает)
	usage := map[int64][3]any{}
	for _, n := range nodes {
		mem, cpu, cnt := s.St.NodeUsage(n.ID)
		usage[n.ID] = [3]any{mem, cpu, cnt}
	}
	s.render(w, "nodes", map[string]any{
		"Nodes": nodes, "NodeProj": nodeProj, "NodeProjRun": nodeProjRun,
		"Usage": usage, "Version": model.VersionTag(), "BaseURL": s.PublicBase,
	})
}

// nodeProjectCounts — сколько проектов (и из них running) на каждой ноде.
func nodeProjectCounts(insts []model.ProjectInstance) (total, running map[int64]int) {
	total = map[int64]int{}
	running = map[int64]int{}
	for _, in := range insts {
		total[in.NodeID]++
		if in.Status == model.StatusRunning || in.Status == model.StatusStarting {
			running[in.NodeID]++
		}
	}
	return total, running
}

func (s *Server) pageProjects(w http.ResponseWriter, r *http.Request) {
	projects, _ := s.St.ListProjects()
	nodes, _ := s.St.ListNodes()
	type projRow struct {
		Project   model.Project
		Instances []model.ProjectInstance
	}
	rows := make([]projRow, 0, len(projects))
	for _, p := range projects {
		inst, _ := s.St.InstancesOfProject(p.ID)
		rows = append(rows, projRow{Project: p, Instances: inst})
	}
	s.render(w, "projects", map[string]any{"Rows": rows, "Projects": projects, "Nodes": nodes, "Version": model.VersionTag()})
}

func (s *Server) pageEvents(w http.ResponseWriter, r *http.Request) {
	// v0.6.0: 300 событий без reconcile-мусора. Раньше тут было 100 записей,
	// из которых ~100 были результатами опроса нод — реальные события
	// (подключения, падения, деплои) уезжали за горизонт страницы.
	events, _ := s.St.RecentEvents(300, "reconcile:")
	dbSize, walSize := s.St.DBSize()
	s.render(w, "events", map[string]any{
		"Events": events, "Version": model.VersionTag(),
		"DBSize": dbSize, "WALSize": walSize,
	})
}

func countStatus(projects []model.Project, status string) int {
	c := 0
	for _, p := range projects {
		if p.Status == status {
			c++
		}
	}
	return c
}

func (s *Server) render(w http.ResponseWriter, name string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "same-origin")
	if data == nil {
		data = map[string]any{}
	}
	data["Active"] = name
	if _, ok := data["Title"]; !ok {
		data["Title"] = pageTitles[name]
	}
	if err := s.tpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("[api] template %s: %v", name, err)
	}
}

// pageTitles — человекочитаемые заголовки страниц (используются в <title>).
var pageTitles = map[string]string{
	"dashboard": "Обзор",
	"nodes":     "Ноды",
	"node":      "Нода",
	"addnode":   "Добавить ноду",
	"projects":  "Проекты",
	"project":   "Проект",
	"events":    "События",
	"ai":        "ИИ-агент",
	"login":     "Вход",
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), 500)
}

// seq — вспомогательная функция для шаблонов: seq(n) -> [0..n-1].
func seq(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// toFloat — универсальное преобразование чисел из шаблонов (uint64/float64/int).
func toFloat(v any) float64 {
	switch x := v.(type) {
	case uint64:
		return float64(x)
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case float64:
		return x
	case uint32:
		return float64(x)
	}
	return 0
}

// divAny — деление для шаблонов с любыми числовыми типами.
func divAny(a, b any) float64 {
	bb := toFloat(b)
	if bb == 0 {
		return 0
	}
	return toFloat(a) / bb
}

// mulAny — умножение для шаблонов (проценты: mulf a b -> a/b*100).
func mulAny(a, b any) float64 {
	return divAny(a, b) * 100
}

// humanMB — «1.5 ГБ» / «512 МБ» для дашборда.
func humanMB(mb any) string {
	v := toFloat(mb)
	if v <= 0 {
		return "—"
	}
	if v >= 1024 {
		return fmt.Sprintf("%.1f ГБ", v/1024)
	}
	return fmt.Sprintf("%.0f МБ", v)
}

// humanGB — «74 ГБ» для диска.
func humanGB(g any) string {
	v := toFloat(g)
	if v <= 0 {
		return "—"
	}
	if v < 10 {
		return fmt.Sprintf("%.1f ГБ", v)
	}
	return fmt.Sprintf("%.0f ГБ", v)
}

// pctOf — процент a от b, с ограничением 0..100 (для CSS-ширин).
func pctOf(a, b any) float64 {
	v := divAny(a, b) * 100
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// joinAny — склейка среза строк через разделитель (для списка портов и т.п.).
// Реализован вручную: в text/template нет встроенного join.
func joinAny(v any, sep string) string {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Slice {
		return ""
	}
	parts := make([]string, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		parts = append(parts, fmt.Sprintf("%v", rv.Index(i).Interface()))
	}
	return strings.Join(parts, sep)
}

// sinceHuman — «3 мин назад», «2 ч назад», «4 дн назад».
func sinceHuman(t time.Time) string {
	if t.IsZero() {
		return "никогда"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "только что"
	case d < time.Hour:
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч назад", int(d.Hours()))
	default:
		return fmt.Sprintf("%d дн назад", int(d.Hours()/24))
	}
}

// ---------- ИИ-агент: HTTP API ----------

// parseFormAny — ParseForm + явный multipart (браузерные FormData шлют
// multipart, который ParseForm не читает — без этого все поля пустые).
func parseFormAny(r *http.Request) {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		_ = r.ParseMultipartForm(32 << 20)
	} else {
		_ = r.ParseForm()
	}
}

// apiAIChats — список чатов (GET) / создать чат (POST title=...).
func (s *Server) apiAIChats(w http.ResponseWriter, r *http.Request) {
	if s.AI == nil {
		writeJSON(w, map[string]any{"ok": false, "error": "ИИ-агент не настроен (нужен GROQ_API_KEY)"})
		return
	}
	if r.Method == http.MethodPost {
		parseFormAny(r)
		id := s.AI.CreateChat(r.PostFormValue("title"))
		writeJSON(w, map[string]any{"ok": true, "id": id})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "chats": s.AI.ListChats()})
}

// apiAIMessages — история чата (GET ?chat_id=).
func (s *Server) apiAIMessages(w http.ResponseWriter, r *http.Request) {
	if s.AI == nil {
		writeJSON(w, map[string]any{"ok": false, "error": "ИИ-агент не настроен"})
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("chat_id"), 10, 64)
	writeJSON(w, map[string]any{"ok": true, "messages": s.AI.Messages(id), "busy": s.AI.Busy(id)})
}

// apiAISend — отправить сообщение агенту (POST chat_id=, text=). Работает в фоне.
func (s *Server) apiAISend(w http.ResponseWriter, r *http.Request) {
	if s.AI == nil {
		writeJSON(w, map[string]any{"ok": false, "error": "ИИ-агент не настроен (нужен GROQ_API_KEY на сервере)"})
		return
	}
	parseFormAny(r)
	chatID, _ := strconv.ParseInt(r.PostFormValue("chat_id"), 10, 64)
	text := strings.TrimSpace(r.PostFormValue("text"))
	if text == "" {
		writeJSON(w, map[string]any{"ok": false, "error": "пустое сообщение"})
		return
	}
	if chatID == 0 {
		chatID = s.AI.CreateChat("")
	}
	if s.AI.Busy(chatID) {
		writeJSON(w, map[string]any{"ok": false, "error": "агент ещё занят предыдущей задачей — подожди"})
		return
	}
	s.AI.RunChatAsync(chatID, text)
	writeJSON(w, map[string]any{"ok": true, "chat_id": chatID, "message": "агент взял задачу в работу"})
}
