// Package store — слой хранения на SQLite (pure-Go драйвер modernc.org/sqlite).
// Хранит ноды, проекты, события и логи.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dreamcatchered/swagcore/internal/model"
)

// Store — обёртка над *sql.DB.
type Store struct {
	db     *sql.DB
	dbPath string
}

// Open открывает (и создаёт) БД в указанном каталоге.
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir data dir: %w", err)
	}
	dbPath := filepath.Join(dataDir, "swagcore.db")
	dsn := "file:" + dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite не любит много соединений на запись.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, dbPath: dbPath}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS nodes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	token_hash TEXT UNIQUE NOT NULL,
	hostname TEXT NOT NULL,
	os TEXT NOT NULL DEFAULT '',
	arch TEXT NOT NULL DEFAULT '',
	agent_version TEXT NOT NULL DEFAULT '',
	ip TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'offline',
	last_seen TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	metrics TEXT NOT NULL DEFAULT '{}',
	labels TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS projects (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT UNIQUE NOT NULL,
	manifest TEXT NOT NULL,
	node_id INTEGER REFERENCES nodes(id) ON DELETE SET NULL,
	status TEXT NOT NULL DEFAULT 'desired_stopped',
	container TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);	CREATE TABLE IF NOT EXISTS events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		level TEXT NOT NULL DEFAULT 'info',
		source TEXT NOT NULL DEFAULT '',
		message TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS logs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id INTEGER NOT NULL,
		ts TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		data TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_logs_project ON logs(project_id, id);
	CREATE INDEX IF NOT EXISTS idx_events_ts ON events(ts);
	CREATE TABLE IF NOT EXISTS node_tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		token TEXT UNIQUE NOT NULL,
		token_hash TEXT UNIQUE NOT NULL,
		used_by TEXT NOT NULL DEFAULT '',
		used INTEGER NOT NULL DEFAULT 0,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);	CREATE TABLE IF NOT EXISTS instances (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id INTEGER NOT NULL,
		node_id INTEGER NOT NULL,
		status TEXT NOT NULL DEFAULT 'desired',
		container TEXT NOT NULL DEFAULT '',
		error TEXT NOT NULL DEFAULT '',
		updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(project_id, node_id)
	);
	CREATE INDEX IF NOT EXISTS idx_instances_project ON instances(project_id);
CREATE TABLE IF NOT EXISTS results (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	ts TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	node_id INTEGER NOT NULL,
	kind TEXT NOT NULL,
	action TEXT NOT NULL DEFAULT '',
	command TEXT NOT NULL DEFAULT '',
	ok INTEGER NOT NULL DEFAULT 0,
	output TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	file TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_results_node ON results(node_id, id);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	// идемпотентные миграции колонок (для живых БД старых версий)
	for _, stmt := range []string{
		`ALTER TABLE nodes ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE nodes ADD COLUMN max_mem_mb INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN has_docker INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN max_cpus REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN max_disk_gb INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE node_tokens ADD COLUMN max_mem_mb INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE node_tokens ADD COLUMN max_cpus REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE node_tokens ADD COLUMN max_disk_gb INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE results ADD COLUMN ts INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN instance_id TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_instance ON nodes(instance_id)`,
		`CREATE INDEX IF NOT EXISTS idx_nodes_tokenhash ON nodes(token_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_instances_node ON instances(node_id)`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			// колонка уже существует — норма (идемпотентная миграция);
			// SQLite: "duplicate column name: X"
			s := err.Error()
			if strings.Contains(s, "duplicate column") || strings.Contains(s, "already exists") {
				continue
			}
			return err
		}
	}
	return nil
}

// parseTime парсит timestamp из SQLite. Драйвер может отдавать и text
// ("2006-01-02 15:04:05"), и RFC3339 (при конвертации time.Time -> string).
func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ---------- События ----------

// AddEvent пишет событие в журнал.
func (s *Store) AddEvent(level, source, message string) {
	_, _ = s.db.Exec(`INSERT INTO events(level, source, message) VALUES(?,?,?)`, level, source, message)
}

// Event — запись журнала.
type Event struct {
	ID      int64     `json:"id"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Source  string    `json:"source"`
	Message string    `json:"message"`
}

// RecentEvents возвращает последние события.
//
// v0.6.0: добавлен exclude — фильтр по префиксу сообщения. Раньше в журнал
// писался каждый успешный ответ на reconcile (раз в минуту на ноду), из-за чего
// 94% таблицы составлял мусор и реальные события уезжали за горизонт.
func (s *Store) RecentEvents(limit int, exclude ...string) ([]Event, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, ts, level, source, message FROM events`
	var args []any
	for _, ex := range exclude {
		if ex == "" {
			continue
		}
		if q == `SELECT id, ts, level, source, message FROM events` {
			q += ` WHERE message NOT LIKE ?`
		} else {
			q += ` AND message NOT LIKE ?`
		}
		args = append(args, ex+"%")
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var ts string
		if err := rows.Scan(&e.ID, &ts, &e.Level, &e.Source, &e.Message); err != nil {
			return nil, err
		}
		e.Time = parseTime(ts)
		out = append(out, e)
	}
	return out, rows.Err()
}

// PruneEvents оставляет только последние keep записей журнала.
// Без этого таблица событий растёт бесконечно (события идут постоянно).
func (s *Store) PruneEvents(keep int) (int64, error) {
	if keep <= 0 {
		keep = 5000
	}
	res, err := s.db.Exec(`DELETE FROM events WHERE id <= (SELECT COALESCE(MAX(id),0) - ? FROM events)`, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Checkpoint сжимает WAL-файл. Без периодического чекпоинта WAL на проде
// рос до 4+ МБ и не уменьшался.
func (s *Store) Checkpoint() error {
	_, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// DBSize возвращает размер БД и WAL в байтах (для дашборда).
func (s *Store) DBSize() (db, wal int64) {
	fi, err := os.Stat(s.dbPath)
	if err == nil {
		db = fi.Size()
	}
	if fi, err := os.Stat(s.dbPath + "-wal"); err == nil {
		wal = fi.Size()
	}
	return
}

// ---------- Ноды ----------

// UpsertNodeByToken находит ноду по хешу токена и обновляет её данные.
// Внимание: LastInsertId после ON CONFLICT DO UPDATE в SQLite возвращает
// посторонний rowid, поэтому делаем явный SELECT, затем UPDATE или INSERT.
// Лимиты (mem/cpu/disk) — серверные настройки: пишутся только при первой
// регистрации (INSERT), при повторных hello НЕ перезаписываются.
const nodeCols = `id, hostname, os, arch, agent_version, ip, status, last_seen, created_at, metrics, labels, enabled, max_mem_mb, has_docker, max_cpus, max_disk_gb, instance_id`

// UpsertNodeByToken находит ноду по хешу токена и обновляет её данные.
// Внимание: LastInsertId после ON CONFLICT DO UPDATE в SQLite возвращает
// посторонний rowid, поэтому делаем явный SELECT, затем UPDATE или INSERT.
// Лимиты (mem/cpu/disk) — серверные настройки: пишутся только при первой
// регистрации (INSERT), при повторных hello НЕ перезаписываются.
func (s *Store) UpsertNodeByToken(tokenHash, hostname, osName, arch, version, ip string, hasDocker bool, maxMemMB int, maxCPUs float64, maxDiskGB int, instanceID string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM nodes WHERE token_hash=?`, tokenHash).Scan(&id)
	switch {
	case err == nil:
		// instance_id обновляем всегда: ноды, заведённые до v0.6.0, должны
		// получить его при первом же подключении, иначе анти-дубль не работает.
		_, err = s.db.Exec(`UPDATE nodes SET hostname=?, os=?, arch=?, agent_version=?, ip=?, status='online', last_seen=CURRENT_TIMESTAMP, has_docker=?, instance_id=? WHERE id=?`,
			hostname, osName, arch, version, ip, boolInt(hasDocker), instanceID, id)
		return id, err
	case errors.Is(err, sql.ErrNoRows):
		res, ierr := s.db.Exec(`INSERT INTO nodes(token_hash, hostname, os, arch, agent_version, ip, status, last_seen, has_docker, max_mem_mb, max_cpus, max_disk_gb, instance_id)
			VALUES(?,?,?,?,?,?,'online',CURRENT_TIMESTAMP,?,?,?,?,?)`,
			tokenHash, hostname, osName, arch, version, ip, boolInt(hasDocker), maxMemMB, maxCPUs, maxDiskGB, instanceID)
		if ierr != nil {
			return 0, ierr
		}
		return res.LastInsertId()
	default:
		return 0, err
	}
}

// DuplicateNodesByInstance возвращает id нод с тем же instance_id, кроме exclude.
// Это анти-дубль: две записи автозагрузки с разными токенами на одной машине
// создавали две ноды с одинаковым hostname, и обе работали — платформа думала,
// что машин две, а гонка за порты ломала проекты (os error 10048).
func (s *Store) DuplicateNodesByInstance(instanceID string, exclude int64) ([]int64, error) {
	if instanceID == "" {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT id FROM nodes WHERE instance_id=? AND id<>? AND instance_id<>''`, instanceID, exclude)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// StaleLegacyNodes возвращает OFFLINE-ноды той же машины, заведённые агентами
// версии < v0.6.0 (у них instance_id пустой, поле появилось только в v0.6.0).
//
// Зачем: при обновлении платформы старый агент успевает переподключиться до
// того, как его остановят, и создаёт свою ноду. Потом его убивают, а запись
// остаётся навсегда — в списке нод появляется «призрак» с тем же hostname.
// Новая нода (с instance_id) при подключении сама подчищает таких предшественников,
// и платформа не требует ручной чистки после апгрейда.
func (s *Store) StaleLegacyNodes(hostname, osName, arch string) ([]int64, error) {
	rows, err := s.db.Query(
		`SELECT id FROM nodes
		  WHERE instance_id='' AND status<>'online' AND hostname=? AND os=? AND arch=?`,
		hostname, osName, arch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// NodeTouch обновляет last_seen и метрики ноды.
func (s *Store) NodeTouch(tokenHash string, m *model.Metrics) error {
	raw, _ := json.Marshal(m)
	_, err := s.db.Exec(`UPDATE nodes SET last_seen=CURRENT_TIMESTAMP, status='online', metrics=? WHERE token_hash=?`, string(raw), tokenHash)
	return err
}

// NodeOffline помечает ноду оффлайн.
func (s *Store) NodeOffline(nodeID int64) {
	_, _ = s.db.Exec(`UPDATE nodes SET status='offline' WHERE id=?`, nodeID)
}

// SweepOffline помечает оффлайн ноды, не присылавшие heartbeat дольше timeout.
func (s *Store) SweepOffline(timeout time.Duration) (int64, error) {
	res, err := s.db.Exec(`UPDATE nodes SET status='offline' WHERE status='online' AND last_seen < datetime('now', ?)`,
		fmt.Sprintf("-%d seconds", int(timeout.Seconds())))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// NodeByID возвращает ноду по ID.
func (s *Store) NodeByID(id int64) (*model.Node, error) {
	row := s.db.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE id=?`, id)
	n, err := scanNode(row.Scan)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, sql.ErrNoRows
	}
	return n, nil
}

// NodeByTokenHash возвращает ноду по хешу токена.
func (s *Store) NodeByTokenHash(hash string) (*model.Node, error) {
	row := s.db.QueryRow(`SELECT `+nodeCols+` FROM nodes WHERE token_hash=?`, hash)
	return scanNode(row.Scan)
}

// ListNodes возвращает все ноды.
func (s *Store) ListNodes() ([]model.Node, error) {
	rows, err := s.db.Query(`SELECT ` + nodeCols + ` FROM nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		n, err := scanNode(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// DeleteNode удаляет ноду.
func (s *Store) DeleteNode(id int64) error {
	_, err := s.db.Exec(`DELETE FROM nodes WHERE id=?`, id)
	return err
}

// DeleteNodeCascade удаляет ноду и все связанные записи (instances, results,
// скриншоты). Проекты не трогает — их экземпляры на ноде помечаются удалением.
func (s *Store) DeleteNodeCascade(id int64) error {
	for _, stmt := range []string{
		`DELETE FROM instances WHERE node_id=?`,
		`DELETE FROM results WHERE node_id=?`,
		`DELETE FROM nodes WHERE id=?`,
	} {
		if _, err := s.db.Exec(stmt, id); err != nil {
			return err
		}
	}
	return nil
}

// SetNodeEnabled включает/выключает ноду для планировщика.
func (s *Store) SetNodeEnabled(id int64, enabled bool) error {
	_, err := s.db.Exec(`UPDATE nodes SET enabled=? WHERE id=?`, boolInt(enabled), id)
	return err
}

// SetNodeMaxMem задаёт лимит RAM ноды.
func (s *Store) SetNodeMaxMem(id int64, mb int) error {
	_, err := s.db.Exec(`UPDATE nodes SET max_mem_mb=? WHERE id=?`, mb, id)
	return err
}

// SetNodeLimits задаёт все лимиты ноды: RAM (MB), CPU (ядра), диск (GB). 0 = без лимита.
func (s *Store) SetNodeLimits(id int64, maxMemMB int, maxCPUs float64, maxDiskGB int) error {
	_, err := s.db.Exec(`UPDATE nodes SET max_mem_mb=?, max_cpus=?, max_disk_gb=? WHERE id=?`, maxMemMB, maxCPUs, maxDiskGB, id)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---------- Токены нод ----------

// CountTokens — количество токенов (для первичной инициализации).
func (s *Store) CountTokens() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM node_tokens`).Scan(&n)
	return n, err
}

// CreateToken создаёт именованный токен подключения с лимитами ноды (0 = без лимита).
func (s *Store) CreateToken(name, token, tokenHash string, maxMemMB int, maxCPUs float64, maxDiskGB int) error {
	_, err := s.db.Exec(`INSERT INTO node_tokens(name, token, token_hash, max_mem_mb, max_cpus, max_disk_gb) VALUES(?,?,?,?,?,?)`,
		name, token, tokenHash, maxMemMB, maxCPUs, maxDiskGB)
	return err
}

const tokCols = `id, name, token, token_hash, used_by, used, created_at, max_mem_mb, max_cpus, max_disk_gb`

// scanToken сканирует строку токена (общая для List/ByHash).
func scanToken(scan func(dest ...any) error) (model.NodeToken, error) {
	var t model.NodeToken
	var usedBy string
	var used int
	var createdAt string
	if err := scan(&t.ID, &t.Name, &t.Token, &t.TokenHash, &usedBy, &used, &createdAt, &t.MaxMemMB, &t.MaxCPUs, &t.MaxDiskGB); err != nil {
		return t, err
	}
	t.UsedBy = usedBy
	t.Used = used == 1
	t.CreatedAt = parseTime(createdAt)
	return t, nil
}

// ListTokens возвращает все токены.
func (s *Store) ListTokens() ([]model.NodeToken, error) {
	rows, err := s.db.Query(`SELECT ` + tokCols + ` FROM node_tokens ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.NodeToken
	for rows.Next() {
		t, err := scanToken(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TokenInUseByNode сообщает, использует ли какой-либо нодой этот токен.
//
// v0.6.0: удаление токена из node_tokens НЕ ДОЛЖНО тихо ломать уже
// подключённую ноду — её WS-соединение остаётся живым (проверка была при
// подключении), а HTTP-часть (загрузка скриншотов, скачивание артефактов)
// начинает отдавать 401. Наблюдалось как «скриншот не работает».
// Поэтому: (1) токен, занятый нодой, нельзя удалить без force;
//           (2) сама авторизация ноды принимает и токен из node_tokens,
//               и хеш, сохранённый в самой ноде (см. NodeAuthHashes).
func (s *Store) TokenInUseByNode(tokenHash string) (string, bool) {
	var hostname string
	err := s.db.QueryRow(`SELECT hostname FROM nodes WHERE token_hash=? AND status='online'`, tokenHash).Scan(&hostname)
	if err != nil {
		return "", false
	}
	return hostname, true
}

// NodeOwnsToken сообщает, совпадает ли хеш токена с записью в nodes.
// Именно это позволяет ноде работать после ротации токена: её личность
// хранится в самой ноде, а не только в таблице выданных токенов.
func (s *Store) NodeOwnsToken(tokenHash string) bool {
	var id int64
	return s.db.QueryRow(`SELECT id FROM nodes WHERE token_hash=?`, tokenHash).Scan(&id) == nil
}
func (s *Store) TokenByHash(hash string) (*model.NodeToken, error) {
	row := s.db.QueryRow(`SELECT `+tokCols+` FROM node_tokens WHERE token_hash=?`, hash)
	t, err := scanToken(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// MarkTokenUsed помечает токен использованным нодой.
func (s *Store) MarkTokenUsed(hash, hostname string) {
	_, _ = s.db.Exec(`UPDATE node_tokens SET used=1, used_by=? WHERE token_hash=?`, hostname, hash)
}

// DeleteToken удаляет токен.
func (s *Store) DeleteToken(id int64) error {
	_, err := s.db.Exec(`DELETE FROM node_tokens WHERE id=?`, id)
	return err
}

// ---------- Экземпляры проектов ----------

// UpsertInstance создаёт/обновляет запись экземпляра.
func (s *Store) UpsertInstance(projectID, nodeID int64, status, container, errMsg string) error {
	// v0.6.0: НЕ создаём экземпляр для несуществующего проекта.
	//
	// Баг: агент присылает статус с задержкой (деплой, рестарт, таймаут). Если
	// проект к этому моменту уже удалён из панели, SetInstanceStatus делал
	// UPSERT — и «воскрешал» строку instances с project_id удалённого проекта.
	// В таблице появлялись фантомы, а UI показывал проекты, которых нет.
	_, err := s.db.Exec(`INSERT INTO instances(project_id, node_id, status, container, error, updated_at)
		SELECT ?,?,?,?,?,CURRENT_TIMESTAMP
		 WHERE EXISTS (SELECT 1 FROM projects WHERE id=?)
		ON CONFLICT(project_id, node_id) DO UPDATE SET status=excluded.status, container=excluded.container, error=excluded.error, updated_at=CURRENT_TIMESTAMP`,
		projectID, nodeID, status, container, errMsg, projectID)
	return err
}

// SetInstanceStatus обновляет статус экземпляра.
func (s *Store) SetInstanceStatus(projectID, nodeID int64, status, container, errMsg string) error {
	res, err := s.db.Exec(`UPDATE instances SET status=?, container=?, error=?, updated_at=CURRENT_TIMESTAMP WHERE project_id=? AND node_id=?`,
		status, container, errMsg, projectID, nodeID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// строки нет — создаём, но только если проект ещё жив (см. UpsertInstance)
		return s.UpsertInstance(projectID, nodeID, status, container, errMsg)
	}
	return nil
}

// PurgeOrphanInstances удаляет экземпляры, чьих проектов/нод больше нет.
// Защита от «фантомов» из старых версий и от гонок с поздними статусами.
func (s *Store) PurgeOrphanInstances() (int64, error) {
	res, err := s.db.Exec(`DELETE FROM instances
		WHERE project_id NOT IN (SELECT id FROM projects)
		   OR node_id     NOT IN (SELECT id FROM nodes)`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}


// InstancesOfProject — экземпляры проекта.
func (s *Store) InstancesOfProject(projectID int64) ([]model.ProjectInstance, error) {
	rows, err := s.db.Query(`SELECT id, project_id, node_id, status, container, error, updated_at FROM instances WHERE project_id=?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ProjectInstance
	for rows.Next() {
		var i model.ProjectInstance
		var updatedAt string
		if err := rows.Scan(&i.ID, &i.ProjectID, &i.NodeID, &i.Status, &i.Container, &i.Error, &updatedAt); err != nil {
			return nil, err
		}
		i.UpdatedAt = parseTime(updatedAt)
		out = append(out, i)
	}
	return out, rows.Err()
}

// AllInstances — все экземпляры.
func (s *Store) AllInstances() ([]model.ProjectInstance, error) {
	rows, err := s.db.Query(`SELECT id, project_id, node_id, status, container, error, updated_at FROM instances`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ProjectInstance
	for rows.Next() {
		var i model.ProjectInstance
		var updatedAt string
		if err := rows.Scan(&i.ID, &i.ProjectID, &i.NodeID, &i.Status, &i.Container, &i.Error, &updatedAt); err != nil {
			return nil, err
		}
		i.UpdatedAt = parseTime(updatedAt)
		out = append(out, i)
	}
	return out, rows.Err()
}

// DeleteInstancesOfProject удаляет экземпляры проекта (при удалении проекта).
func (s *Store) DeleteInstancesOfProject(projectID int64) error {
	_, err := s.db.Exec(`DELETE FROM instances WHERE project_id=?`, projectID)
	return err
}

// SyncRecon — сверка фактических контейнеров на ноде с БД (drift-детекция).
// Возвращает список (projectID, containerName) инстансов, которые БД считает
// running/starting, но контейнера на ноде нет — реконсилер их передеплоит.
// SyncRecon сверяет фактическое состояние ноды с БД и возвращает список
// «дрейф» в виде [projectID, nodeID] — реконсилер их передеплоит.
//
// v0.6.0: раньше сверялись ТОЛЬКО docker-контейнеры, поэтому на нодах без
// Docker (process-режим — типичная Windows-машина за NAT) падение процесса
// было невидимо: экземпляр навсегда оставался running, а публичный домен
// отдавал 502. Теперь seenProcess передаёт фактическое состояние процессов.
//
// seen[name] — статус контейнера ("Up 4 days", "exited (0) 3 hours ago", …).
// seenProc[name] — живость процесса.
func (s *Store) SyncRecon(nodeID int64, seen map[string]string, seenProc map[string]bool) ([][2]int64, error) {
	insts, err := s.AllInstances()
	if err != nil {
		return nil, err
	}
	nodes, err := s.ListNodes()
	if err != nil {
		return nil, err
	}
	hostByID := map[int64]string{}
	for _, n := range nodes {
		hostByID[n.ID] = n.Hostname
	}
	projects, err := s.ListProjects()
	if err != nil {
		return nil, err
	}
	nameByProject := map[int64]string{}
	for _, p := range projects {
		nameByProject[p.ID] = p.Name
	}

	var drift [][2]int64
	for _, in := range insts {
		if in.NodeID != nodeID {
			continue
		}
		if in.Status != model.InstRunning && in.Status != model.InstStarting {
			continue // желаемое stopped/failed/desired — сверять нечего
		}
		pname, ok := nameByProject[in.ProjectID]
		if !ok {
			continue // проект удалён — не трогаем
		}
		cname := in.Container
		if cname == "" {
			cname = "swag_" + pname
		}

		// 1) process-проект: проверяем живость процесса
		if isProcessProject(projects, in.ProjectID) {
			alive, reported := seenProc[pname]
			if !reported {
				// агент не прислал процессы (старый агент) — пропускаем
				continue
			}
			if !alive {
				drift = append(drift, [2]int64{in.ProjectID, in.NodeID})
				s.AddEvent("warn", "reconciler", "drift: процесс "+cname+" на "+
					hostByID[nodeID]+" не запущен (в БД "+in.Status+") — передеплой")
			}
			continue
		}

		// 2) docker-проект: контейнер должен существовать и быть живым
		status, ok := seen[cname]
		if !ok {
			drift = append(drift, [2]int64{in.ProjectID, in.NodeID})
			s.AddEvent("warn", "reconciler", "drift: проект #"+itoa(in.ProjectID)+
				" ("+cname+") числится "+in.Status+" на "+hostByID[nodeID]+", но контейнера нет — передеплой")
		} else if status != "running" && status != "restarting" && !strings.HasPrefix(status, "Up ") {
			// контейнер есть, но в плохом состоянии (exited/created/dead).
			// "Up N seconds" — только что пересоздан, не считать дрейфом.
			drift = append(drift, [2]int64{in.ProjectID, in.NodeID})
			s.AddEvent("warn", "reconciler", "drift: контейнер "+cname+" на "+
				hostByID[nodeID]+" в состоянии "+status+" — передеплой")
		}
	}
	return drift, nil
}

// isProcessProject — размещён ли проект в режиме process (без Docker).
func isProcessProject(projects []model.Project, projectID int64) bool {
	for i := range projects {
		if projects[i].ID == projectID {
			return projects[i].Manifest.Mode == "process"
		}
	}
	return false
}

// itoa — маленький хелпер без strconv в горячем пути.
func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

// ---------- Результаты exec/power/screenshot ----------

// AddResult сохраняет результат команды со страницы ноды.
func (s *Store) AddResult(nodeID int64, r model.Result) error {
	_, err := s.db.Exec(`INSERT INTO results(node_id, kind, action, command, ok, output, error, file, ts) VALUES(?,?,?,?,?,?,?,?,?)`,
		nodeID, r.Kind, r.Action, r.Command, boolInt(r.OK), r.Output, r.Error, r.File, time.Now().Unix())
	return err
}

// NodeResults — последние результаты ноды.
func (s *Store) NodeResults(nodeID int64, limit int) ([]model.Result, error) {
	if limit <= 0 {
		limit = 30
	}
	rows, err := s.db.Query(`SELECT kind, action, command, ok, output, error, file, ts FROM results WHERE node_id=? ORDER BY id DESC LIMIT ?`, nodeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Result
	for rows.Next() {
		var r model.Result
		var ok int
		var ts int64
		if err := rows.Scan(&r.Kind, &r.Action, &r.Command, &ok, &r.Output, &r.Error, &r.File, &ts); err != nil {
			return nil, err
		}
		r.OK = ok == 1
		r.CreatedAt = time.Unix(ts, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListResultsFiles — файлы скриншотов ноды (kind=screenshot).
func (s *Store) ListScreenshotFiles(nodeID int64, limit int) ([]model.Result, error) {
	if limit <= 0 {
		limit = 12
	}
	rows, err := s.db.Query(`SELECT kind, action, command, ok, output, error, file, ts FROM results WHERE node_id=? AND kind='screenshot' AND file!='' ORDER BY id DESC LIMIT ?`, nodeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Result
	for rows.Next() {
		var r model.Result
		var ok int
		var ts int64
		if err := rows.Scan(&r.Kind, &r.Action, &r.Command, &ok, &r.Output, &r.Error, &r.File, &ts); err != nil {
			return nil, err
		}
		r.OK = ok == 1
		r.CreatedAt = time.Unix(ts, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

type scanFunc func(dest ...any) error

func scanNode(scan scanFunc) (*model.Node, error) {
	var n model.Node
	var metrics, labels string
	var lastSeen, createdAt string
	var enabled, hasDocker, maxDiskGB int
	var maxCPUs float64
	err := scan(&n.ID, &n.Hostname, &n.OS, &n.Arch, &n.AgentVer, &n.IP, &n.Status, &lastSeen, &createdAt, &metrics, &labels, &enabled, &n.MaxMemMB, &hasDocker, &maxCPUs, &maxDiskGB, &n.InstanceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	n.LastSeen = parseTime(lastSeen)
	n.CreatedAt = parseTime(createdAt)
	var m model.Metrics
	_ = json.Unmarshal([]byte(metrics), &m)
	n.Metrics = &m
	_ = json.Unmarshal([]byte(labels), &n.Labels)
	n.Enabled = enabled == 1
	n.HasDocker = hasDocker == 1
	n.MaxCPUs = maxCPUs
	n.MaxDiskGB = maxDiskGB
	return &n, nil
}

// ---------- Проекты ----------

// CreateProject создаёт проект.
func (s *Store) CreateProject(name string, mf model.Manifest) (*model.Project, error) {
	raw, _ := json.Marshal(mf)
	res, err := s.db.Exec(`INSERT INTO projects(name, manifest, status) VALUES(?,?,?)`,
		name, string(raw), model.StatusDesiredStopped)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.ProjectByID(id)
}

// UpdateManifest обновляет манифест проекта.
func (s *Store) UpdateManifest(id int64, mf model.Manifest) error {
	raw, _ := json.Marshal(mf)
	_, err := s.db.Exec(`UPDATE projects SET manifest=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, string(raw), id)
	return err
}

// SetProjectStatus обновляет статус проекта.
func (s *Store) SetProjectStatus(id int64, status, container, errMsg string) error {
	_, err := s.db.Exec(`UPDATE projects SET status=?, container=?, error=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		status, container, errMsg, id)
	return err
}

// SetProjectNode привязывает проект к ноде.
func (s *Store) SetProjectNode(id, nodeID int64) error {
	_, err := s.db.Exec(`UPDATE projects SET node_id=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, nodeID, id)
	return err
}

// ProjectByID возвращает проект по ID.
func (s *Store) ProjectByID(id int64) (*model.Project, error) {
	row := s.db.QueryRow(`SELECT id, name, manifest, node_id, status, container, error, created_at, updated_at FROM projects WHERE id=?`, id)
	return scanProject(row.Scan)
}

// ProjectByName возвращает проект по имени.
func (s *Store) ProjectByName(name string) (*model.Project, error) {
	row := s.db.QueryRow(`SELECT id, name, manifest, node_id, status, container, error, created_at, updated_at FROM projects WHERE name=?`, name)
	return scanProject(row.Scan)
}

// ListProjects возвращает все проекты.
func (s *Store) ListProjects() ([]model.Project, error) {
	rows, err := s.db.Query(`SELECT id, name, manifest, node_id, status, container, error, created_at, updated_at FROM projects ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Project
	for rows.Next() {
		p, err := scanProject(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// DeleteProject удаляет проект из БД КАСКАДНО.
//
// v0.6.0 (баг v0.5.x): удалялась только строка из projects, а экземпляры в
// instances оставались сиротами (project_id указывал на несуществующий проект).
// Из-за этого планировщик/реконсилер видели «проект на ноде», которого нет, а
// панель показывала мусорные строки. Теперь чистим instances и logs.
func (s *Store) DeleteProject(id int64) error {
	for _, stmt := range []string{
		`DELETE FROM instances WHERE project_id=?`,
		`DELETE FROM logs WHERE project_id=?`,
		`UPDATE projects SET node_id=NULL WHERE id=?`, // если бы была ссылка
		`DELETE FROM projects WHERE id=?`,
	} {
		if _, err := s.db.Exec(stmt, id); err != nil {
			return err
		}
	}
	return nil
}

// DeleteProjectArtifacts убирает файлы проекта с диска (артефакты, каталог агента).
func (s *Store) DeleteProjectArtifacts(id int64, artifactsDir string) error {
	p, err := s.ProjectByID(id)
	if err != nil || p == nil {
		return nil
	}
	if artifactsDir == "" || !safeName(p.Name) {
		return nil
	}
	return os.RemoveAll(filepath.Join(artifactsDir, p.Name))
}

// safeName — имя без разделителей пути и «..» (защита от path traversal).
func safeName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	if strings.ContainsAny(s, "/\\\x00") || strings.Contains(s, "..") {
		return false
	}
	return true
}

// NodeUsage — сумма запрошенных ресурсов проектов, размещённых на ноде.
// Используется квотами (max_mem_mb/max_cpus/max_disk_gb).
// memMB/cpus/diskGB — сумма по манифестам всех экземпляров в статусах
// running/starting (то, что реально занимает место).
func (s *Store) NodeUsage(nodeID int64) (memMB int, cpus float64, projects int) {
	insts, err := s.InstancesOfNode(nodeID)
	if err != nil {
		return
	}
	projs, err := s.ListProjects()
	if err != nil {
		return
	}
	byID := map[int64]model.Project{}
	for _, p := range projs {
		byID[p.ID] = p
	}
	for _, in := range insts {
		if in.Status != model.InstRunning && in.Status != model.InstStarting {
			continue
		}
		p, ok := byID[in.ProjectID]
		if !ok {
			continue
		}
		projects++
		memMB += parseMemMB(p.Manifest.Resources.Memory)
		if v, err := strconv.ParseFloat(p.Manifest.Resources.CPUs, 64); err == nil {
			cpus += v
		}
	}
	return
}

// InstancesOfNode возвращает все экземпляры, размещённые на ноде.
func (s *Store) InstancesOfNode(nodeID int64) ([]model.ProjectInstance, error) {
	rows, err := s.db.Query(`SELECT id, project_id, node_id, status, container, error, updated_at FROM instances WHERE node_id=? ORDER BY id`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ProjectInstance
	for rows.Next() {
		var in model.ProjectInstance
		var updatedAt string
		if err := rows.Scan(&in.ID, &in.ProjectID, &in.NodeID, &in.Status, &in.Container, &in.Error, &updatedAt); err != nil {
			return nil, err
		}
		in.UpdatedAt = parseTime(updatedAt)
		out = append(out, in)
	}
	return out, rows.Err()
}

// ParseMemMB — публичная обёртка: "256m"/"1g"/"512" -> MB.
// Используется реконсилером для квот.
func ParseMemMB(s string) int { return parseMemMB(s) }

// parseMemMB — "256m"/"1g"/"512" -> MB.
func parseMemMB(s string) int {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0
	}
	suffix := byte(0)
	if s[len(s)-1] >= '0' && s[len(s)-1] <= '9' {
		// без суффикса — мегабайты
	} else {
		suffix = s[len(s)-1]
		s = s[:len(s)-1]
	}
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || v < 0 {
		return 0
	}
	switch suffix {
	case 'g':
		return v * 1024
	case 'b':
		return v / (1024 * 1024)
	case 'm', 0:
		return v
	}
	return 0
}

func scanProject(scan scanFunc) (*model.Project, error) {
	var p model.Project
	var manifest string
	var nodeID sql.NullInt64
	var createdAt, updatedAt string
	err := scan(&p.ID, &p.Name, &manifest, &nodeID, &p.Status, &p.Container, &p.Error, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if nodeID.Valid {
		p.NodeID = nodeID.Int64
	}
	_ = json.Unmarshal([]byte(manifest), &p.Manifest)
	p.CreatedAt = parseTime(createdAt)
	p.UpdatedAt = parseTime(updatedAt)
	return &p, nil
}

// ---------- Логи ----------

// AppendLog добавляет строку лога проекта.
func (s *Store) AppendLog(projectID int64, data string) {
	_, _ = s.db.Exec(`INSERT INTO logs(project_id, data) VALUES(?,?)`, projectID, data)
}

// LogLine — строка лога.
type LogLine struct {
	ID   int64     `json:"id"`
	Time time.Time `json:"time"`
	Data string    `json:"data"`
}

// GetLogs возвращает последние строки лога проекта.
func (s *Store) GetLogs(projectID int64, limit int) ([]LogLine, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT id, ts, data FROM logs WHERE project_id=? ORDER BY id DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogLine
	for rows.Next() {
		var l LogLine
		var ts string
		if err := rows.Scan(&l.ID, &ts, &l.Data); err != nil {
			return nil, err
		}
		l.Time = parseTime(ts)
		out = append(out, l)
	}
	// разворачиваем в хронологический порядок
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}
